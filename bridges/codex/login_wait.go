package codex

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote/sdk"
)

func (cl *CodexLogin) Wait(ctx context.Context) (*bridgev2.LoginStep, error) {
	log := cl.logger(ctx)
	rpc := cl.getRPC()
	if rpc == nil {
		return nil, errCodexNotStarted
	}
	if cl.loginDoneCh == nil {
		return nil, errCodexWaitMissing
	}
	if cl.waitUntil.IsZero() {
		if spec, ok := codexLoginFlowSpecForFlow(cl.FlowID); ok && spec.waitDeadline > 0 {
			cl.waitUntil = time.Now().Add(spec.waitDeadline)
		} else {
			cl.waitUntil = time.Now().Add(10 * time.Minute)
		}
	}
	return sdk.RunDisplayAndWaitLoop[error, codexLoginDone](ctx, sdk.DisplayAndWaitLoopConfig[error, codexLoginDone]{
		Deadline:         cl.waitUntil,
		PollInterval:     2 * time.Second,
		ReturnAfter:      20 * time.Second,
		StartSignal:      cl.startCh,
		CompletionSignal: cl.loginDoneCh,
		OnStartSignal: func(_ context.Context, err error) (*sdk.DisplayAndWaitLoopResult, error) {
			if err != nil {
				return nil, err
			}
			return &sdk.DisplayAndWaitLoopResult{Continue: true}, nil
		},
		OnCompletionSignal: func(_ context.Context, done codexLoginDone) (*sdk.DisplayAndWaitLoopResult, error) {
			loginID := cl.getLoginID()
			if !done.success {
				if done.errText == "" {
					done.errText = "login failed"
				}
				log.Warn().Str("login_id", loginID).Str("error", done.errText).Msg("Codex login failed")
				cl.cancelLoginAttempt(true)
				return nil, sdk.NewLoginRespError(http.StatusBadRequest, done.errText, "CODEX", "LOGIN_FAILED")
			}
			log.Info().Str("login_id", loginID).Msg("Codex login completed (notification)")
			step, err := cl.finishLogin(cl.backgroundProcessContext())
			if err != nil {
				return nil, err
			}
			return &sdk.DisplayAndWaitLoopResult{Step: step}, nil
		},
		OnPoll: func(context.Context) (*sdk.DisplayAndWaitLoopResult, error) {
			rpc = cl.getRPC()
			if rpc == nil {
				return nil, errCodexStopped
			}
			readCtx, cancel := context.WithTimeout(cl.backgroundProcessContext(), 10*time.Second)
			var resp struct {
				Account            *codexAccountInfo `json:"account"`
				RequiresOpenaiAuth bool              `json:"requiresOpenaiAuth"`
			}
			err := rpc.Call(readCtx, "account/read", map[string]any{"refreshToken": true}, &resp)
			cancel()
			if err == nil && (resp.Account != nil || !resp.RequiresOpenaiAuth) {
				log.Info().Str("login_id", cl.getLoginID()).Msg("Codex login completed (account/read)")
				step, err := cl.finishLogin(cl.backgroundProcessContext())
				if err != nil {
					return nil, err
				}
				return &sdk.DisplayAndWaitLoopResult{Step: step}, nil
			}
			authURL := strings.TrimSpace(cl.getAuthURL())
			if spec, ok := codexLoginFlowSpecForFlow(cl.FlowID); ok && spec.usesBrowserUI && authURL != "" {
				return &sdk.DisplayAndWaitLoopResult{Step: cl.displayWaitStep(spec.waitStepID, spec, "Open this URL in a browser and complete login, then wait here.", authURL)}, nil
			}
			return &sdk.DisplayAndWaitLoopResult{Continue: true}, nil
		},
		ReturnStep: func() *bridgev2.LoginStep {
			log.Debug().Str("login_id", cl.getLoginID()).Msg("Codex login still waiting")
			return cl.buildStillWaitingStep("Keep this screen open.")
		},
		ContextDoneStep: func() *bridgev2.LoginStep {
			log.Debug().Str("login_id", cl.getLoginID()).Msg("Codex login wait context ended; returning still-waiting step")
			return cl.buildStillWaitingStep("Keep this screen open after completing the browser login.")
		},
		OnTimeout: func() error {
			log.Warn().Str("login_id", cl.getLoginID()).Msg("Codex login timed out")
			cl.cancelLoginAttempt(true)
			return errCodexTimedOut
		},
	})
}

func (cl *CodexLogin) buildStillWaitingStep(suffix string) *bridgev2.LoginStep {
	spec, ok := codexLoginFlowSpecForFlow(cl.FlowID)
	if !ok {
		spec = codexLoginFlowSpec{
			waitStepID:    "com.beeper.agentremote.codex.chatgpt",
			waitMessage:   "Still waiting for Codex login to complete.",
			displayType:   bridgev2.LoginDisplayTypeCode,
			usesBrowserUI: true,
		}
	}
	message := spec.waitMessage
	if spec.usesBrowserUI && suffix != "" {
		message = strings.TrimSpace(spec.waitMessage + " " + suffix)
	}
	data := ""
	if spec.usesBrowserUI {
		data = strings.TrimSpace(cl.getAuthURL())
	}
	return cl.displayWaitStep(spec.waitStepID, spec, message, data)
}

func (cl *CodexLogin) displayWaitStep(stepID string, spec codexLoginFlowSpec, instructions, data string) *bridgev2.LoginStep {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeDisplayAndWait,
		StepID:       stepID,
		Instructions: instructions,
		DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{
			Type: spec.displayType,
			Data: data,
		},
	}
}

func (cl *CodexLogin) finishLogin(ctx context.Context) (*bridgev2.LoginStep, error) {
	if cl.User == nil {
		return nil, errCodexMissingUser
	}
	log := cl.logger(ctx)

	bgCtx := cl.backgroundProcessContext()
	loginID := sdk.NextUserLoginID(cl.User, "codex")
	remoteName := "Codex"
	dupCount := 0
	for _, existing := range cl.User.GetUserLogins() {
		if existing == nil || existing.Metadata == nil {
			continue
		}
		meta, ok := existing.Metadata.(*UserLoginMetadata)
		if !ok || meta == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) &&
			isManagedAuthLogin(meta) &&
			existing.ID != loginID {
			dupCount++
		}
	}
	if dupCount > 0 {
		remoteName = fmt.Sprintf("%s (%d)", remoteName, dupCount+1)
	}

	// Best-effort read account email (chatgpt mode).
	accountEmail := ""
	if rpc := cl.getRPC(); rpc != nil {
		readCtx, cancelRead := context.WithTimeout(bgCtx, 10*time.Second)
		defer cancelRead()
		var acct struct {
			Account *codexAccountInfo `json:"account"`
		}
		_ = rpc.Call(readCtx, "account/read", map[string]any{"refreshToken": false}, &acct)
		if acct.Account != nil && strings.TrimSpace(acct.Account.Email) != "" {
			accountEmail = strings.TrimSpace(acct.Account.Email)
		}
	}

	meta := &UserLoginMetadata{
		Provider:          ProviderCodex,
		CodexHome:         cl.codexHome,
		CodexAuthSource:   CodexAuthSourceManaged,
		CodexAuthMode:     cl.getAuthMode(),
		CodexAccountEmail: accountEmail,
		ChatGPTAccountID:  strings.TrimSpace(cl.chatgptAccountID),
		ChatGPTPlanType:   strings.TrimSpace(cl.chatgptPlanType),
	}

	login, step, err := sdk.PersistAndCompleteLoginWithOptions(
		bgCtx,
		bgCtx,
		cl.User,
		&database.UserLogin{
			ID:         loginID,
			RemoteName: remoteName,
			Metadata:   meta,
		},
		"com.beeper.agentremote.codex.complete",
		sdk.PersistLoginCompletionOptions{
			Load: cl.Connector.LoadUserLogin,
		},
	)
	if err != nil {
		cl.cancelLoginAttempt(true)
		return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to create login: %w", err), http.StatusInternalServerError, "CODEX", "CREATE_LOGIN_FAILED")
	}
	log.Info().Str("user_login_id", string(login.ID)).Msg("Created new Codex login")
	cl.cancelLoginAttempt(false)

	return step, nil
}
