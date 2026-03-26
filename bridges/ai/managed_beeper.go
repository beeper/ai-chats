package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"
)

func (oc *OpenAIConnector) getPreferredUserLogin(_ context.Context, user *bridgev2.User) *bridgev2.UserLogin {
	if user == nil {
		return nil
	}
	return selectPreferredUserLogin(user.GetDefaultLogin(), user.GetUserLogins(), oc.isSelectableUserLogin)
}

func (oc *OpenAIConnector) isSelectableUserLogin(login *bridgev2.UserLogin) bool {
	if login == nil || login.Client == nil {
		return false
	}
	meta := loginMetadata(login)
	if meta == nil || strings.TrimSpace(meta.Provider) == "" {
		return false
	}
	if login.BridgeState != nil {
		switch login.BridgeState.GetPrev().StateEvent {
		case status.StateBadCredentials, status.StateLoggedOut:
			return false
		}
	}
	if oc != nil {
		if strings.TrimSpace(oc.resolveProviderAPIKey(meta)) == "" {
			return false
		}
		if normalizeProvider(meta.Provider) != ProviderMagicProxy {
			return false
		}
		if normalizeProxyBaseURL(meta.BaseURL) == "" {
			return false
		}
	}
	return true
}

func selectPreferredUserLogin(
	defaultLogin *bridgev2.UserLogin,
	allLogins []*bridgev2.UserLogin,
	isSelectable func(*bridgev2.UserLogin) bool,
) *bridgev2.UserLogin {
	if defaultLogin != nil && (isSelectable == nil || isSelectable(defaultLogin)) {
		return defaultLogin
	}
	for _, login := range allLogins {
		if login == nil || login == defaultLogin {
			continue
		}
		if isSelectable == nil || isSelectable(login) {
			return login
		}
	}
	return nil
}
