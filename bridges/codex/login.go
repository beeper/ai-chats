package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/codex/codexrpc"
)

var (
	_ bridgev2.LoginProcess               = (*CodexLogin)(nil)
	_ bridgev2.LoginProcessUserInput      = (*CodexLogin)(nil)
	_ bridgev2.LoginProcessDisplayAndWait = (*CodexLogin)(nil)
)

// CodexLogin provisions a provider=codex user login backed by a local `codex app-server` process.
// Tokens are persisted by Codex itself under an isolated CODEX_HOME per login.
type CodexLogin struct {
	User      *bridgev2.User
	Connector *CodexConnector
	FlowID    string

	mu         sync.Mutex // protects mutable login state
	rpc        *codexrpc.Client
	cancel     context.CancelFunc // cancels the background goroutine
	codexHome  string
	instanceID string
	authMode   string
	loginID    string
	authURL    string
	waitUntil  time.Time

	loginDoneCh chan codexLoginDone

	startCh chan error

	chatgptAccountID string
	chatgptPlanType  string
}

type codexLoginDone struct {
	success bool
	errText string
}

// codexAccountInfo is the common response shape for account/read calls.
type codexAccountInfo struct {
	Type  string `json:"type"`
	Email string `json:"email"`
}

func (cl *CodexLogin) logger(ctx context.Context) *zerolog.Logger {
	var l zerolog.Logger
	if cl != nil && cl.User != nil {
		l = cl.User.Log.With().Str("component", "codex_login").Logger()
	} else {
		l = zerolog.Nop()
	}
	return agentremote.LoggerFromContext(ctx, &l)
}

func (cl *CodexLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	cmd := cl.resolveCodexCommand()
	if _, err := exec.LookPath(cmd); err != nil {
		return &bridgev2.LoginStep{
			Type:         bridgev2.LoginStepTypeUserInput,
			StepID:       "io.ai-bridge.codex.install",
			Instructions: fmt.Sprintf("Codex CLI (%q) not found on PATH. Install Codex, then submit this step again.", cmd),
			UserInputParams: &bridgev2.LoginUserInputParams{
				Fields: []bridgev2.LoginInputDataField{
					{
						Type:         bridgev2.LoginInputFieldTypeURL,
						ID:           "install_url",
						Name:         "Install Codex",
						Description:  "Install Codex and retry. (Input is ignored; this field is just a reminder.)",
						DefaultValue: "https://github.com/openai/codex",
					},
				},
			},
		}, nil
	}
	log := cl.logger(ctx)
	switch cl.FlowID {
	case FlowCodexChatGPT:
		cl.setAuthMode("chatgpt")
		return cl.spawnAndStartLogin(ctx, log, "chatgpt", nil)
	case FlowCodexAPIKey:
		cl.setAuthMode("apiKey")
		return &bridgev2.LoginStep{
			Type:         bridgev2.LoginStepTypeUserInput,
			StepID:       "io.ai-bridge.codex.enter_api_key",
			Instructions: "Enter your OpenAI API key.",
			UserInputParams: &bridgev2.LoginUserInputParams{
				Fields: []bridgev2.LoginInputDataField{
					{
						Type:        bridgev2.LoginInputFieldTypeToken,
						ID:          "api_key",
						Name:        "OpenAI API key",
						Description: "Paste your OpenAI API key (sk-...).",
					},
				},
			},
		}, nil
	case FlowCodexChatGPTExternalTokens:
		cl.setAuthMode("chatgptAuthTokens")
		return &bridgev2.LoginStep{
			Type:         bridgev2.LoginStepTypeUserInput,
			StepID:       "io.ai-bridge.codex.enter_chatgpt_tokens",
			Instructions: "Enter externally managed ChatGPT tokens.",
			UserInputParams: &bridgev2.LoginUserInputParams{
				Fields: []bridgev2.LoginInputDataField{
					{
						Type:        bridgev2.LoginInputFieldTypeToken,
						ID:          "access_token",
						Name:        "ChatGPT access token",
						Description: "Paste the ChatGPT accessToken JWT.",
					},
					{
						Type:        bridgev2.LoginInputFieldTypeUsername,
						ID:          "chatgpt_account_id",
						Name:        "ChatGPT account ID",
						Description: "Paste the ChatGPT workspace/account identifier.",
					},
					{
						Type:        bridgev2.LoginInputFieldTypeUsername,
						ID:          "chatgpt_plan_type",
						Name:        "ChatGPT plan type",
						Description: "Optional. Leave blank to let Codex infer it.",
					},
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("login flow %s is not available", cl.FlowID)
	}
}

func (cl *CodexLogin) Cancel() {
	cl.cancelLoginAttempt(true)
}

func (cl *CodexLogin) getRPC() *codexrpc.Client {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.rpc
}

func (cl *CodexLogin) setRPC(rpc *codexrpc.Client) {
	cl.mu.Lock()
	cl.rpc = rpc
	cl.mu.Unlock()
}

func (cl *CodexLogin) getLoginID() string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.loginID
}

func (cl *CodexLogin) getAuthURL() string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.authURL
}

func (cl *CodexLogin) getAuthMode() string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.authMode
}

func (cl *CodexLogin) setAuthMode(mode string) {
	cl.mu.Lock()
	cl.authMode = mode
	cl.mu.Unlock()
}

func (cl *CodexLogin) setLoginSession(loginID, authURL string) {
	cl.mu.Lock()
	cl.loginID = loginID
	cl.authURL = authURL
	cl.mu.Unlock()
}

// signalStart sends a non-blocking signal on startCh.
func (cl *CodexLogin) signalStart(err error) {
	select {
	case cl.startCh <- err:
	default:
	}
}

func (cl *CodexLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	cmd := cl.resolveCodexCommand()
	if _, err := exec.LookPath(cmd); err != nil {
		return nil, fmt.Errorf("codex CLI not found (%q): %w", cmd, err)
	}
	log := cl.logger(ctx)
	switch cl.FlowID {
	case FlowCodexAPIKey:
		cl.setAuthMode("apiKey")
		apiKey := strings.TrimSpace(input["api_key"])
		if apiKey == "" {
			return nil, errors.New("api_key is required")
		}
		return cl.spawnAndStartLogin(ctx, log, "apiKey", map[string]string{
			"apiKey": apiKey,
		})
	case FlowCodexChatGPTExternalTokens:
		cl.setAuthMode("chatgptAuthTokens")
		accessToken := strings.TrimSpace(input["access_token"])
		accountID := strings.TrimSpace(input["chatgpt_account_id"])
		planType := strings.TrimSpace(input["chatgpt_plan_type"])
		if accessToken == "" || accountID == "" {
			return nil, errors.New("access_token and chatgpt_account_id are required")
		}
		credentials := map[string]string{
			"accessToken":      accessToken,
			"chatgptAccountId": accountID,
		}
		if planType != "" {
			credentials["chatgptPlanType"] = planType
		}
		cl.mu.Lock()
		cl.chatgptAccountID = accountID
		cl.chatgptPlanType = planType
		cl.mu.Unlock()
		return cl.spawnAndStartLogin(ctx, log, "chatgptAuthTokens", credentials)
	case FlowCodexChatGPT:
		// Browser login starts during Start(); user input is not needed.
		return &bridgev2.LoginStep{
			Type:         bridgev2.LoginStepTypeDisplayAndWait,
			StepID:       "io.ai-bridge.codex.chatgpt",
			Instructions: "Open the login URL and complete ChatGPT authentication, then wait here.",
			DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{
				Type: bridgev2.LoginDisplayTypeCode,
				Data: strings.TrimSpace(cl.getAuthURL()),
			},
		}, nil
	default:
		return nil, fmt.Errorf("login flow %s is not available", cl.FlowID)
	}
}

// backgroundProcessContext returns a long-lived context for spawning child processes.
func (cl *CodexLogin) backgroundProcessContext() context.Context {
	if cl.Connector != nil && cl.Connector.br != nil && cl.Connector.br.BackgroundCtx != nil {
		return cl.Connector.br.BackgroundCtx
	}
	return context.Background()
}

func (cl *CodexLogin) initializeExperimental(_ string) bool {
	// The bridge relies on persistExtendedHistory for thread recovery, which is
	// gated behind the experimental API capability in current Codex app-server builds.
	return true
}

func (cl *CodexLogin) cancelLoginAttempt(removeHome bool) {
	cl.mu.Lock()
	rpc := cl.rpc
	cl.rpc = nil
	cancel := cl.cancel
	cl.cancel = nil
	loginID := cl.loginID
	authMode := cl.authMode
	codexHome := cl.codexHome
	if removeHome {
		cl.codexHome = ""
		cl.chatgptAccountID = ""
		cl.chatgptPlanType = ""
	}
	cl.mu.Unlock()

	if rpc != nil && strings.TrimSpace(loginID) != "" && strings.TrimSpace(authMode) == "chatgpt" {
		callCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		var out struct{}
		_ = rpc.Call(callCtx, "account/login/cancel", map[string]any{"loginId": loginID}, &out)
		stop()
	}
	if cancel != nil {
		cancel()
	}
	if rpc != nil {
		_ = rpc.Close()
	}
	if removeHome && strings.TrimSpace(codexHome) != "" {
		_ = os.RemoveAll(codexHome)
	}
}

// spawnAndStartLogin creates an isolated CODEX_HOME, spawns an app-server, and starts auth.
func (cl *CodexLogin) spawnAndStartLogin(ctx context.Context, log *zerolog.Logger, mode string, credentials map[string]string) (*bridgev2.LoginStep, error) {
	homeBase := cl.resolveCodexHomeBaseDir()
	instanceID := generateShortID()
	codexHome := filepath.Join(homeBase, instanceID)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create CODEX_HOME: %w", err)
	}

	cmd := cl.resolveCodexCommand()
	launch, err := cl.Connector.resolveAppServerLaunch()
	if err != nil {
		return nil, err
	}

	// IMPORTANT: Do not bind the Codex app-server process lifetime to the HTTP request context.
	// The provisioning API cancels r.Context() after the response is written; using it would kill
	// the child process and cause the login to hang forever in Wait().
	procCtx, procCancel := context.WithCancel(cl.backgroundProcessContext())
	rpc, err := codexrpc.StartProcess(procCtx, codexrpc.ProcessConfig{
		Command:      cmd,
		Args:         launch.Args,
		Env:          []string{"CODEX_HOME=" + codexHome},
		WebSocketURL: launch.WebSocketURL,
		OnStderr: func(line string) {
			log.Debug().Str("codex_home", codexHome).Str("stderr", line).Msg("Codex stderr")
		},
		OnProcessExit: func(err error) {
			if err != nil {
				log.Warn().Err(err).Str("codex_home", codexHome).Msg("Codex process exited with error")
			} else {
				log.Debug().Str("codex_home", codexHome).Msg("Codex process exited normally")
			}
		},
	})
	if err != nil {
		procCancel()
		return nil, err
	}
	cl.setRPC(rpc)
	cl.codexHome = codexHome
	cl.instanceID = instanceID
	cl.loginID = ""
	cl.authURL = ""
	if mode != "chatgptAuthTokens" {
		cl.chatgptAccountID = ""
		cl.chatgptPlanType = ""
	}
	if mode == "apiKey" || mode == "chatgptAuthTokens" {
		cl.waitUntil = time.Now().Add(5 * time.Minute)
	} else {
		cl.waitUntil = time.Now().Add(10 * time.Minute)
	}

	cl.loginDoneCh = make(chan codexLoginDone, 1)
	cl.startCh = make(chan error, 1)

	cl.mu.Lock()
	cl.cancel = procCancel
	cl.mu.Unlock()

	// Make SubmitUserInput return quickly: initialize + login/start can be slow and can freeze provisioning.
	go func() {
		// Initialize first (some Codex builds won't accept login/start before initialize).
		initCtx, cancelInit := context.WithTimeout(procCtx, 45*time.Second)
		ci := cl.Connector.Config.Codex.ClientInfo
		_, initErr := rpc.Initialize(initCtx, codexrpc.ClientInfo{Name: ci.Name, Title: ci.Title, Version: ci.Version}, cl.initializeExperimental(mode))
		cancelInit()
		if initErr != nil {
			log.Warn().Err(initErr).Msg("Codex initialize failed")
			cl.cancelLoginAttempt(true)
			cl.signalStart(initErr)
			return
		}

		// Subscribe to account/login/completed so Wait() can resolve.
		rpc.OnNotification(func(method string, params json.RawMessage) {
			switch method {
			case "account/login/completed":
				var evt struct {
					Success bool    `json:"success"`
					LoginID *string `json:"loginId"`
					Error   *string `json:"error"`
				}
				_ = json.Unmarshal(params, &evt)
				// Some Codex builds omit loginId; only filter when it's present.
				loginID := cl.getLoginID()
				if loginID != "" && evt.LoginID != nil && strings.TrimSpace(*evt.LoginID) != loginID {
					return
				}
				errText := ""
				if evt.Error != nil {
					errText = strings.TrimSpace(*evt.Error)
				}
				select {
				case cl.loginDoneCh <- codexLoginDone{success: evt.Success, errText: errText}:
				default:
				}
			case "account/updated":
				// Some Codex builds only emit account/updated after login.
				var evt struct {
					AuthMode *string `json:"authMode"`
				}
				_ = json.Unmarshal(params, &evt)
				if evt.AuthMode != nil && strings.TrimSpace(*evt.AuthMode) != "" {
					select {
					case cl.loginDoneCh <- codexLoginDone{success: true}:
					default:
					}
				}
			}
		})

		if mode == "apiKey" || mode == "chatgptAuthTokens" {
			loginParams := map[string]any{"type": mode}
			for k, v := range credentials {
				loginParams[k] = strings.TrimSpace(v)
			}
			startCtx, cancel := context.WithTimeout(procCtx, 60*time.Second)
			startErr := rpc.Call(startCtx, "account/login/start", loginParams, &struct{}{})
			cancel()
			if startErr != nil {
				log.Warn().Err(startErr).Str("mode", mode).Msg("Codex login start failed")
				cl.cancelLoginAttempt(true)
			}
			cl.signalStart(startErr)
			return
		}

		var loginResp struct {
			Type    string `json:"type"`
			LoginID string `json:"loginId"`
			AuthURL string `json:"authUrl"`
		}
		startCtx, cancel := context.WithTimeout(procCtx, 60*time.Second)
		startErr := rpc.Call(startCtx, "account/login/start", map[string]any{"type": "chatgpt"}, &loginResp)
		cancel()
		if startErr != nil {
			log.Warn().Err(startErr).Msg("Codex chatgpt login start failed")
			cl.cancelLoginAttempt(true)
			cl.signalStart(startErr)
			return
		}
		loginID := strings.TrimSpace(loginResp.LoginID)
		authURL := strings.TrimSpace(loginResp.AuthURL)
		cl.setLoginSession(loginID, authURL)
		if authURL == "" || loginID == "" {
			cl.cancelLoginAttempt(true)
			cl.signalStart(errors.New("codex returned empty authUrl/loginId"))
			return
		}
		log.Info().Str("instance_id", cl.instanceID).Str("login_id", loginID).Msg("Codex browser login started")
		cl.signalStart(nil)
	}()

	var stepID, instructions string
	switch mode {
	case "apiKey":
		stepID = "io.ai-bridge.codex.validating"
		instructions = "Validating the API key with Codex. Keep this screen open."
	case "chatgptAuthTokens":
		stepID = "io.ai-bridge.codex.validating_external_tokens"
		instructions = "Validating ChatGPT external tokens with Codex. Keep this screen open."
	default:
		stepID = "io.ai-bridge.codex.starting"
		instructions = "Starting Codex browser login…"
	}
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeDisplayAndWait,
		StepID:       stepID,
		Instructions: instructions,
		DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{
			Type: bridgev2.LoginDisplayTypeNothing,
		},
	}, nil
}

func (cl *CodexLogin) Wait(ctx context.Context) (*bridgev2.LoginStep, error) {
	log := cl.logger(ctx)
	rpc := cl.getRPC()
	if rpc == nil {
		return nil, errors.New("login not started")
	}
	if cl.loginDoneCh == nil {
		return nil, errors.New("login wait unavailable")
	}
	if cl.waitUntil.IsZero() {
		cl.waitUntil = time.Now().Add(10 * time.Minute)
	}

	overallTimeout := time.Until(cl.waitUntil)
	if overallTimeout <= 0 {
		return nil, errors.New("timed out waiting for Codex login to complete")
	}
	deadline := time.NewTimer(overallTimeout)
	defer deadline.Stop()

	// Poll account/read as a fallback in case the notification is dropped.
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	// Avoid holding a single Wait() request open indefinitely; returning periodically
	// allows polling callers and prevents head-of-line blocking in single-threaded callers.
	returnAfter := time.NewTimer(20 * time.Second)
	defer returnAfter.Stop()

	startCh := cl.startCh
	for {
		select {
		case err := <-startCh:
			// Surface initialize/login-start failures early.
			if err != nil {
				return nil, err
			}
			// Ignore further start signals after the first one.
			startCh = nil
		case done := <-cl.loginDoneCh:
			loginID := cl.getLoginID()
			if !done.success {
				if done.errText == "" {
					done.errText = "login failed"
				}
				log.Warn().Str("login_id", loginID).Str("error", done.errText).Msg("Codex login failed")
				cl.cancelLoginAttempt(true)
				return nil, fmt.Errorf("%s", done.errText)
			}
			log.Info().Str("login_id", loginID).Msg("Codex login completed (notification)")
			return cl.finishLogin(cl.backgroundProcessContext())
		case <-tick.C:
			rpc = cl.getRPC()
			if rpc == nil {
				return nil, errors.New("codex login process stopped")
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
				return cl.finishLogin(cl.backgroundProcessContext())
			}
			// Expose the browser auth URL as soon as it becomes available.
			authURL := strings.TrimSpace(cl.getAuthURL())
			if cl.getAuthMode() == "chatgpt" && authURL != "" {
				return &bridgev2.LoginStep{
					Type:         bridgev2.LoginStepTypeDisplayAndWait,
					StepID:       "io.ai-bridge.codex.chatgpt",
					Instructions: "Open this URL in a browser and complete login, then wait here.",
					DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{
						Type: bridgev2.LoginDisplayTypeCode,
						Data: authURL,
					},
				}, nil
			}
		case <-returnAfter.C:
			log.Debug().Str("login_id", cl.getLoginID()).Msg("Codex login still waiting")
			return cl.buildStillWaitingStep("Keep this screen open."), nil
		case <-deadline.C:
			log.Warn().Str("login_id", cl.getLoginID()).Msg("Codex login timed out")
			cl.cancelLoginAttempt(true)
			return nil, errors.New("timed out waiting for Codex login to complete")
		case <-ctx.Done():
			// Most callers will have their own HTTP/gRPC deadlines. Returning the same waiting
			// step allows the client to poll again without the login process being marked as failed.
			log.Debug().Str("login_id", cl.getLoginID()).Msg("Codex login wait context ended; returning still-waiting step")
			return cl.buildStillWaitingStep("Keep this screen open after completing the browser login."), nil
		}
	}
}

func (cl *CodexLogin) buildStillWaitingStep(suffix string) *bridgev2.LoginStep {
	stepID := "io.ai-bridge.codex.chatgpt"
	instr := "Still waiting for Codex login to complete. " + suffix
	displayType := bridgev2.LoginDisplayTypeNothing
	data := ""
	switch cl.getAuthMode() {
	case "apiKey":
		stepID = "io.ai-bridge.codex.validating"
		instr = "Still validating the API key with Codex. Keep this screen open."
	case "chatgptAuthTokens":
		stepID = "io.ai-bridge.codex.validating_external_tokens"
		instr = "Still validating ChatGPT external tokens with Codex. Keep this screen open."
	default:
		if authURL := strings.TrimSpace(cl.getAuthURL()); authURL != "" {
			displayType = bridgev2.LoginDisplayTypeCode
			data = authURL
		}
	}
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeDisplayAndWait,
		StepID:       stepID,
		Instructions: instr,
		DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{
			Type: displayType,
			Data: data,
		},
	}
}

func (cl *CodexLogin) finishLogin(ctx context.Context) (*bridgev2.LoginStep, error) {
	if cl.User == nil {
		return nil, errors.New("missing user")
	}
	log := cl.logger(ctx)

	bgCtx := cl.backgroundProcessContext()
	loginID := agentremote.NextUserLoginID(cl.User, "codex")
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

	login, step, err := agentremote.CreateAndCompleteLogin(
		bgCtx,
		bgCtx,
		cl.User,
		"codex",
		remoteName,
		meta,
		"io.ai-bridge.codex.complete",
		cl.Connector.LoadUserLogin,
	)
	if err != nil {
		cl.cancelLoginAttempt(true)
		return nil, fmt.Errorf("failed to create login: %w", err)
	}
	log.Info().Str("user_login_id", string(login.ID)).Msg("Created new Codex login")
	cl.cancelLoginAttempt(false)

	return step, nil
}

func (cl *CodexLogin) resolveCodexCommand() string {
	if cl.Connector == nil {
		return "codex"
	}
	return resolveCodexCommandFromConfig(cl.Connector.Config.Codex)
}

func (cl *CodexLogin) resolveCodexHomeBaseDir() string {
	var base string
	if cl.Connector != nil && cl.Connector.Config.Codex != nil {
		base = strings.TrimSpace(cl.Connector.Config.Codex.HomeBaseDir)
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			base = filepath.Join(home, ".local", "share", "ai-bridge", "codex")
		} else {
			base = filepath.Join(os.TempDir(), "ai-bridge-codex")
		}
	}
	if expanded, err := agentremote.ExpandUserHome(base); err == nil && expanded != "" {
		base = expanded
	}
	if abs, err := filepath.Abs(base); err == nil {
		return abs
	}
	return base
}
