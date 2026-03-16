package sdk

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

// sdkAutoLogin is a no-op login process for when the CLI handles auth.
type sdkAutoLogin struct {
	user *bridgev2.User
}

func (l *sdkAutoLogin) Start(_ context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:           bridgev2.LoginStepTypeComplete,
		StepID:         "sdk-auto",
		Instructions:   "Login handled by agentremote CLI",
		CompleteParams: &bridgev2.LoginCompleteParams{},
	}, nil
}

func (l *sdkAutoLogin) Cancel() {}
