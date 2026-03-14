package agentremote

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

// CompleteLoginStep builds the standard completion step for a loaded login.
func CompleteLoginStep(stepID string, login *bridgev2.UserLogin) *bridgev2.LoginStep {
	if login == nil {
		return nil
	}
	return &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeComplete,
		StepID: stepID,
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: login.ID,
			UserLogin:   login,
		},
	}
}

// LoadConnectAndCompleteLogin reloads the typed client, reconnects it in the
// background, and returns the standard completion step.
func LoadConnectAndCompleteLogin(
	persistCtx context.Context,
	connectCtx context.Context,
	login *bridgev2.UserLogin,
	stepID string,
	load func(context.Context, *bridgev2.UserLogin) error,
) (*bridgev2.LoginStep, error) {
	if login == nil {
		return nil, nil
	}
	if load != nil {
		if err := load(persistCtx, login); err != nil {
			return nil, err
		}
	}
	if login.Client != nil {
		go login.Client.Connect(login.Log.WithContext(connectCtx))
	}
	return CompleteLoginStep(stepID, login), nil
}
