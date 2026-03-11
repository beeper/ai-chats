package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/id"
)

type managedBeeperAuth struct {
	UserMXID id.UserID
	BaseURL  string
	Token    string
}

func (auth managedBeeperAuth) HasIdentity() bool {
	return auth.UserMXID != ""
}

func (auth managedBeeperAuth) Complete() bool {
	return auth.UserMXID != "" && auth.BaseURL != "" && auth.Token != ""
}

func (oc *OpenAIConnector) resolveManagedBeeperAuth() managedBeeperAuth {
	if oc == nil {
		return managedBeeperAuth{}
	}

	return managedBeeperAuth{
		UserMXID: id.UserID(strings.TrimSpace(oc.Config.Beeper.UserMXID)),
		BaseURL:  normalizeBeeperBaseURL(oc.Config.Beeper.BaseURL),
		Token:    strings.TrimSpace(oc.Config.Beeper.Token),
	}
}

func (oc *OpenAIConnector) hasManagedBeeperAuth() bool {
	return oc.resolveManagedBeeperAuth().Complete()
}

func (oc *OpenAIConnector) getManagedBeeperLogin(ctx context.Context, user *bridgev2.User) *bridgev2.UserLogin {
	if oc == nil || user == nil {
		return nil
	}
	login, err := oc.reconcileManagedBeeperLoginForUser(ctx, user)
	if err != nil {
		oc.br.Log.Warn().Err(err).Stringer("user_mxid", user.MXID).Msg("Failed to reconcile managed Beeper Cloud login")
	}
	return login
}

func (oc *OpenAIConnector) getPreferredUserLogin(ctx context.Context, user *bridgev2.User) *bridgev2.UserLogin {
	if user == nil {
		return nil
	}

	var managed *bridgev2.UserLogin
	if oc != nil {
		managed = oc.getManagedBeeperLogin(ctx, user)
	}
	defaultLogin := user.GetDefaultLogin()
	allLogins := user.GetUserLogins()
	preferred := selectPreferredUserLogin(managed, defaultLogin, allLogins, oc.isSelectableUserLogin)
	if preferred != nil {
		return preferred
	}
	if defaultLogin != nil {
		return defaultLogin
	}
	return managed
}

func (oc *OpenAIConnector) reconcileManagedBeeperLogin(ctx context.Context) (*bridgev2.UserLogin, error) {
	auth := oc.resolveManagedBeeperAuth()
	if !auth.HasIdentity() || oc == nil || oc.br == nil {
		return nil, nil
	}
	user, err := oc.br.GetUserByMXID(ctx, auth.UserMXID)
	if err != nil {
		return nil, err
	}
	return oc.reconcileManagedBeeperLoginForUser(ctx, user)
}

func (oc *OpenAIConnector) reconcileManagedBeeperLoginForUser(ctx context.Context, user *bridgev2.User) (*bridgev2.UserLogin, error) {
	if oc == nil || oc.br == nil || user == nil {
		return nil, nil
	}

	auth := oc.resolveManagedBeeperAuth()
	if !auth.HasIdentity() || user.MXID != auth.UserMXID {
		return nil, nil
	}

	loginID := managedBeeperLoginID(user.MXID)
	login, err := oc.br.GetExistingUserLoginByID(ctx, loginID)
	if err != nil {
		return nil, err
	}
	if login == nil {
		login, err = oc.br.GetExistingUserLoginByID(ctx, legacyManagedBeeperLoginID(user.MXID))
		if err != nil {
			return nil, err
		}
	}

	effectiveToken := auth.Token
	if !auth.Complete() {
		effectiveToken = ""
	}

	isNew := false
	if login == nil {
		if !auth.Complete() {
			return nil, nil
		}
		login, err = user.NewLogin(ctx, &database.UserLogin{
			ID:         loginID,
			RemoteName: "Beeper Cloud",
			RemoteProfile: status.RemoteProfile{
				Name: "Beeper Cloud",
			},
			Metadata: &UserLoginMetadata{
				Provider: ProviderBeeper,
				BaseURL:  auth.BaseURL,
				APIKey:   effectiveToken,
			},
		}, &bridgev2.NewLoginParams{
			DeleteOnConflict: true,
		})
		if err != nil {
			return nil, err
		}
		isNew = true
	} else {
		meta := loginMetadata(login)
		meta.Provider = ProviderBeeper
		meta.BaseURL = auth.BaseURL
		meta.APIKey = effectiveToken
		login.Metadata = meta
		login.RemoteName = "Beeper Cloud"
		login.RemoteProfile.Name = "Beeper Cloud"
		if err := login.Save(ctx); err != nil {
			return nil, err
		}
	}

	if err := oc.LoadUserLogin(ctx, login); err != nil {
		return nil, err
	}

	if aiClient, ok := login.Client.(*AIClient); ok && aiClient != nil && !aiClient.IsLoggedIn() {
		go login.Client.Connect(login.Log.WithContext(context.Background()))
	}

	if !auth.Complete() {
		login.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Error:      AIAuthFailed,
			Message:    "Beeper Cloud credentials are missing or incomplete",
		})
		return login, nil
	}

	if isNew {
		login.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateConnected,
			Message:    "Connected",
		})
	}

	return login, nil
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
		switch meta.Provider {
		case ProviderBeeper:
			if oc.resolveBeeperBaseURL(meta) == "" {
				return false
			}
		case ProviderMagicProxy:
			if normalizeMagicProxyBaseURL(meta.BaseURL) == "" {
				return false
			}
		}
	}
	return true
}

func selectPreferredUserLogin(
	managed *bridgev2.UserLogin,
	defaultLogin *bridgev2.UserLogin,
	allLogins []*bridgev2.UserLogin,
	isSelectable func(*bridgev2.UserLogin) bool,
) *bridgev2.UserLogin {
	if managed != nil && (isSelectable == nil || isSelectable(managed)) {
		return managed
	}
	if defaultLogin != nil && defaultLogin != managed && (isSelectable == nil || isSelectable(defaultLogin)) {
		return defaultLogin
	}
	for _, login := range allLogins {
		if login == nil || login == managed {
			continue
		}
		if isSelectable == nil || isSelectable(login) {
			return login
		}
	}
	return nil
}
