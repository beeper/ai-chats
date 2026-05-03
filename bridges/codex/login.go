package codex

import (
	"context"
	"net/http"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.LoginProcess               = (*CodexLogin)(nil)
	_ bridgev2.LoginProcessUserInput      = (*CodexLogin)(nil)
	_ bridgev2.LoginProcessDisplayAndWait = (*CodexLogin)(nil)

	errCodexAPIKeyRequired = sdk.NewLoginRespError(http.StatusBadRequest, "Enter your OpenAI API key.", "CODEX", "API_KEY_REQUIRED")
	errCodexExternalTokens = sdk.NewLoginRespError(http.StatusBadRequest, "Enter both access_token and chatgpt_account_id.", "CODEX", "CHATGPT_TOKENS_REQUIRED")
	errCodexNotStarted     = sdk.NewLoginRespError(http.StatusBadRequest, "Codex login has not started yet.", "CODEX", "NOT_STARTED")
	errCodexWaitMissing    = sdk.NewLoginRespError(http.StatusBadRequest, "Codex login wait state is unavailable.", "CODEX", "WAIT_UNAVAILABLE")
	errCodexTimedOut       = sdk.NewLoginRespError(http.StatusBadRequest, "Timed out waiting for Codex login to complete.", "CODEX", "LOGIN_TIMEOUT")
	errCodexStopped        = sdk.NewLoginRespError(http.StatusBadRequest, "Codex login process stopped before login completed.", "CODEX", "PROCESS_STOPPED")
	errCodexMissingUser    = sdk.NewLoginRespError(http.StatusInternalServerError, "Missing user context for Codex login.", "CODEX", "MISSING_USER_CONTEXT")
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

type codexLoginFlowSpec struct {
	authMode      string
	startStepID   string
	startMessage  string
	waitStepID    string
	waitMessage   string
	waitDeadline  time.Duration
	displayType   bridgev2.LoginDisplayType
	inputStep     func() *bridgev2.LoginStep
	usesBrowserUI bool
}

func codexLoginFlowSpecForFlow(flowID string) (codexLoginFlowSpec, bool) {
	switch flowID {
	case FlowCodexChatGPT:
		return codexLoginFlowSpec{
			authMode:      "chatgpt",
			startStepID:   "com.beeper.agentremote.codex.starting",
			startMessage:  "Starting Codex browser login…",
			waitStepID:    "com.beeper.agentremote.codex.chatgpt",
			waitMessage:   "Still waiting for Codex login to complete.",
			waitDeadline:  10 * time.Minute,
			displayType:   bridgev2.LoginDisplayTypeCode,
			usesBrowserUI: true,
		}, true
	case FlowCodexAPIKey:
		return codexLoginFlowSpec{
			authMode:     "apiKey",
			startStepID:  "com.beeper.agentremote.codex.validating",
			startMessage: "Validating the API key with Codex. Keep this screen open.",
			waitStepID:   "com.beeper.agentremote.codex.validating",
			waitMessage:  "Still validating the API key with Codex. Keep this screen open.",
			waitDeadline: 5 * time.Minute,
			displayType:  bridgev2.LoginDisplayTypeNothing,
			inputStep: func() *bridgev2.LoginStep {
				return &bridgev2.LoginStep{
					Type:         bridgev2.LoginStepTypeUserInput,
					StepID:       "com.beeper.agentremote.codex.enter_api_key",
					Instructions: "Enter your OpenAI API key.",
					UserInputParams: &bridgev2.LoginUserInputParams{
						Fields: []bridgev2.LoginInputDataField{{
							Type:        bridgev2.LoginInputFieldTypeToken,
							ID:          "api_key",
							Name:        "OpenAI API key",
							Description: "Paste your OpenAI API key (sk-...).",
						}},
					},
				}
			},
		}, true
	case FlowCodexChatGPTExternalTokens:
		return codexLoginFlowSpec{
			authMode:     "chatgptAuthTokens",
			startStepID:  "com.beeper.agentremote.codex.validating_external_tokens",
			startMessage: "Validating ChatGPT external tokens with Codex. Keep this screen open.",
			waitStepID:   "com.beeper.agentremote.codex.validating_external_tokens",
			waitMessage:  "Still validating ChatGPT external tokens with Codex. Keep this screen open.",
			waitDeadline: 5 * time.Minute,
			displayType:  bridgev2.LoginDisplayTypeNothing,
			inputStep: func() *bridgev2.LoginStep {
				return &bridgev2.LoginStep{
					Type:         bridgev2.LoginStepTypeUserInput,
					StepID:       "com.beeper.agentremote.codex.enter_chatgpt_tokens",
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
				}
			},
		}, true
	default:
		return codexLoginFlowSpec{}, false
	}
}
