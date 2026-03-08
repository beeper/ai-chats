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

	oc.runtimeMatrixMu.RLock()
	runtimeUserMXID := oc.runtimeMatrixUserMXID
	runtimeToken := strings.TrimSpace(oc.runtimeMatrixAccessToken)
	runtimeHomeserver := strings.TrimSpace(oc.runtimeMatrixHomeserver)
	oc.runtimeMatrixMu.RUnlock()

	userMXID := id.UserID(strings.TrimSpace(oc.Config.Beeper.UserMXID))
	if userMXID == "" {
		userMXID = runtimeUserMXID
	}

	baseURL := normalizeBeeperBaseURL(oc.Config.Beeper.BaseURL)
	if baseURL == "" {
		baseURL = normalizeBeeperBaseURL(runtimeHomeserver)
	}

	token := strings.TrimSpace(oc.Config.Beeper.Token)
	if token == "" {
		token = runtimeToken
	}

	return managedBeeperAuth{
		UserMXID: userMXID,
		BaseURL:  baseURL,
		Token:    token,
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
	if managed := oc.getManagedBeeperLogin(ctx, user); managed != nil {
		return managed
	}
	return user.GetDefaultLogin()
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
