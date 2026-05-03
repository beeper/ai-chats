package codex

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

func (cl *CodexLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	cmd := cl.resolveCodexCommand()
	if _, err := exec.LookPath(cmd); err != nil {
		return &bridgev2.LoginStep{
			Type:         bridgev2.LoginStepTypeUserInput,
			StepID:       "com.beeper.agentremote.codex.install",
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
	spec, ok := codexLoginFlowSpecForFlow(cl.FlowID)
	if !ok {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	cl.setAuthMode(spec.authMode)
	if spec.inputStep != nil {
		return spec.inputStep(), nil
	}
	return cl.spawnAndStartLogin(ctx, log, spec, nil)
}
func (cl *CodexLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	cmd := cl.resolveCodexCommand()
	if _, err := exec.LookPath(cmd); err != nil {
		return nil, sdk.WrapLoginRespError(fmt.Errorf("codex CLI not found (%q): %w", cmd, err), http.StatusInternalServerError, "CODEX", "CLI_NOT_FOUND")
	}
	log := cl.logger(ctx)
	spec, ok := codexLoginFlowSpecForFlow(cl.FlowID)
	if !ok {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	cl.setAuthMode(spec.authMode)
	switch cl.FlowID {
	case FlowCodexAPIKey:
		apiKey := strings.TrimSpace(input["api_key"])
		if apiKey == "" {
			return nil, errCodexAPIKeyRequired
		}
		return cl.spawnAndStartLogin(ctx, log, spec, map[string]string{
			"apiKey": apiKey,
		})
	case FlowCodexChatGPTExternalTokens:
		accessToken := strings.TrimSpace(input["access_token"])
		accountID := strings.TrimSpace(input["chatgpt_account_id"])
		planType := strings.TrimSpace(input["chatgpt_plan_type"])
		if accessToken == "" || accountID == "" {
			return nil, errCodexExternalTokens
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
		return cl.spawnAndStartLogin(ctx, log, spec, credentials)
	case FlowCodexChatGPT:
		// Browser login starts during Start(); user input is not needed.
		return cl.displayWaitStep(spec.waitStepID, spec, "Open the login URL and complete ChatGPT authentication, then wait here.", strings.TrimSpace(cl.getAuthURL())), nil
	default:
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
}

// backgroundProcessContext returns a long-lived context for spawning child processes.
