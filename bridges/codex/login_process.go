package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	"github.com/beeper/agentremote/sdk"
)

func (cl *CodexLogin) spawnAndStartLogin(ctx context.Context, log *zerolog.Logger, spec codexLoginFlowSpec, credentials map[string]string) (*bridgev2.LoginStep, error) {
	homeBase := cl.resolveCodexHomeBaseDir()
	instanceID := generateShortID()
	codexHome := filepath.Join(homeBase, instanceID)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to create CODEX_HOME: %w", err), http.StatusInternalServerError, "CODEX", "CREATE_HOME_FAILED")
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
	if spec.authMode != "chatgptAuthTokens" {
		cl.chatgptAccountID = ""
		cl.chatgptPlanType = ""
	}
	cl.waitUntil = time.Now().Add(spec.waitDeadline)

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
		_, initErr := rpc.Initialize(initCtx, codexrpc.ClientInfo{Name: ci.Name, Title: ci.Title, Version: ci.Version}, cl.initializeExperimental(spec.authMode))
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

		if spec.authMode == "apiKey" || spec.authMode == "chatgptAuthTokens" {
			loginParams := map[string]any{"type": spec.authMode}
			for k, v := range credentials {
				loginParams[k] = strings.TrimSpace(v)
			}
			startCtx, cancel := context.WithTimeout(procCtx, 60*time.Second)
			startErr := rpc.Call(startCtx, "account/login/start", loginParams, &struct{}{})
			cancel()
			if startErr != nil {
				log.Warn().Err(startErr).Str("mode", spec.authMode).Msg("Codex login start failed")
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

	return cl.displayWaitStep(spec.startStepID, spec, spec.startMessage, ""), nil
}
