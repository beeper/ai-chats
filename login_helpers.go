package agentremote

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

// ValidateLoginState checks that the user and bridge are non-nil. This is the
// common preamble shared by all bridge LoginProcess implementations.
func ValidateLoginState(user *bridgev2.User, br *bridgev2.Bridge) error {
	if user == nil {
		return errors.New("missing user context for login")
	}
	if br == nil {
		return errors.New("connector is not initialized")
	}
	return nil
}

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

// CreateAndCompleteLogin creates a user login and returns the standard completion step.
func CreateAndCompleteLogin(
	persistCtx context.Context,
	connectCtx context.Context,
	user *bridgev2.User,
	loginType string,
	remoteName string,
	metadata any,
	stepID string,
	load func(context.Context, *bridgev2.UserLogin) error,
) (*bridgev2.UserLogin, *bridgev2.LoginStep, error) {
	if user == nil {
		return nil, nil, nil
	}
	login, err := user.NewLogin(persistCtx, &database.UserLogin{
		ID:         NextUserLoginID(user, loginType),
		RemoteName: remoteName,
		Metadata:   metadata,
	}, nil)
	if err != nil {
		return nil, nil, err
	}
	step, err := LoadConnectAndCompleteLogin(persistCtx, connectCtx, login, stepID, load)
	if err != nil {
		return nil, nil, err
	}
	return login, step, nil
}

// UpdateAndCompleteLogin saves an existing login and returns the standard completion step.
func UpdateAndCompleteLogin(
	persistCtx context.Context,
	connectCtx context.Context,
	login *bridgev2.UserLogin,
	remoteName string,
	metadata any,
	stepID string,
	load func(context.Context, *bridgev2.UserLogin) error,
) (*bridgev2.LoginStep, error) {
	if login == nil {
		return nil, nil
	}
	login.RemoteName = remoteName
	login.Metadata = metadata
	if err := login.Save(persistCtx); err != nil {
		return nil, err
	}
	return LoadConnectAndCompleteLogin(persistCtx, connectCtx, login, stepID, load)
}
