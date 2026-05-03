package aihelpers

import (
	"context"
	"net/http"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

// ValidateLoginState checks that the user and bridge are non-nil. This is the
// common preamble shared by all bridge LoginProcess implementations.
func ValidateLoginState(user *bridgev2.User, br *bridgev2.Bridge) error {
	if user == nil {
		return NewLoginRespError(http.StatusInternalServerError, "Missing user context for login.", "LOGIN", "MISSING_USER_CONTEXT")
	}
	if br == nil {
		return NewLoginRespError(http.StatusInternalServerError, "Connector is not initialized.", "LOGIN", "CONNECTOR_NOT_INITIALIZED")
	}
	return nil
}

type PersistLoginCompletionOptions struct {
	NewLoginParams *bridgev2.NewLoginParams
	Load           func(context.Context, *bridgev2.UserLogin) error
	AfterPersist   func(context.Context, *bridgev2.UserLogin) error
	Cleanup        func(context.Context, *bridgev2.UserLogin)
}

// PersistAndCompleteLoginWithOptions persists a login, optionally runs extra
// setup, reloads the typed client when requested, reconnects it in the
// background, and returns the standard completion step.
func PersistAndCompleteLoginWithOptions(
	persistCtx context.Context,
	connectCtx context.Context,
	user *bridgev2.User,
	loginData *database.UserLogin,
	stepID string,
	opts PersistLoginCompletionOptions,
) (*bridgev2.UserLogin, *bridgev2.LoginStep, error) {
	if user == nil || loginData == nil {
		return nil, nil, nil
	}
	login, err := user.NewLogin(persistCtx, loginData, opts.NewLoginParams)
	if err != nil {
		return nil, nil, err
	}
	if opts.AfterPersist != nil {
		if err = opts.AfterPersist(persistCtx, login); err != nil {
			if opts.Cleanup != nil {
				opts.Cleanup(persistCtx, login)
			}
			return login, nil, err
		}
	}
	if opts.Load != nil {
		if err = opts.Load(persistCtx, login); err != nil {
			if opts.Cleanup != nil {
				opts.Cleanup(persistCtx, login)
			}
			return login, nil, err
		}
	}
	if login.Client != nil {
		go login.Client.Connect(login.Log.WithContext(connectCtx))
	}
	step := &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeComplete,
		StepID: stepID,
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: login.ID,
			UserLogin:   login,
		},
	}
	return login, step, nil
}
