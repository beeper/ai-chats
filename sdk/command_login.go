package sdk

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
)

// ResolveCommandLogin resolves the login for a command event.
//
// In-room commands are bound to the portal owner and must not fall back to a
// different default login if that ownership can't be resolved.
func ResolveCommandLogin(ctx context.Context, ce *commands.Event, defaultLogin *bridgev2.UserLogin) (*bridgev2.UserLogin, error) {
	if ce == nil {
		return defaultLogin, nil
	}
	if ce.Portal == nil {
		return defaultLogin, nil
	}

	br := ce.Bridge
	if ce.User != nil && ce.User.Bridge != nil {
		br = ce.User.Bridge
	}
	if ce.Portal.Receiver != "" && br != nil {
		login, err := br.GetExistingUserLoginByID(ctx, ce.Portal.Receiver)
		if err == nil && login != nil {
			if ce.User == nil || login.UserMXID == ce.User.MXID {
				return login, nil
			}
		}
	}
	if ce.User != nil {
		login, _, err := ce.Portal.FindPreferredLogin(ctx, ce.User, false)
		if err == nil && login != nil {
			return login, nil
		}
		if err != nil {
			return nil, err
		}
	}
	if defaultLogin != nil {
		return nil, errors.New("portal-scoped commands require the owning login")
	}
	return nil, bridgev2.ErrNotLoggedIn
}
