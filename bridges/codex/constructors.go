package codex

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/sdk"
)

func NewConnector() *CodexConnector {
	cc := &CodexConnector{clients: make(map[networkid.UserLoginID]bridgev2.NetworkAPI)}
	cc.sdkConfig = &sdk.Config[*CodexClient, *Config]{
		Name:             "codex",
		Description:      "Codex bridge built with the AgentRemote SDK.",
		ProtocolID:       "ai-codex",
		ProviderIdentity: sdk.ProviderIdentity{IDPrefix: "codex", LogKey: "codex_msg_id", StatusNetwork: "codex"},
		ClientCacheMu:    &cc.clientsMu,
		ClientCache:      &cc.clients,
		MakeBrokenLogin: func(l *bridgev2.UserLogin, reason string) *sdk.BrokenLoginClient {
			return newBrokenLoginClient(l, cc, reason)
		},
		CreateClient: func(login *bridgev2.UserLogin) (bridgev2.NetworkAPI, error) {
			return newCodexClient(login, cc)
		},
		UpdateClient: func(client bridgev2.NetworkAPI, login *bridgev2.UserLogin) {
			if typed, ok := client.(*CodexClient); ok {
				typed.SetUserLogin(login)
			}
		},
		AfterLoadClient: func(client bridgev2.NetworkAPI) {
			if c, ok := client.(*CodexClient); ok {
				c.scheduleBootstrapOnce()
			}
		},
	}
	cc.sdkConfig.Agent = codexSDKAgent()
	return cc
}

func codexSDKAgent() *sdk.Agent {
	return &sdk.Agent{
		ID:           string(codexGhostID),
		Name:         "Codex",
		Description:  "Codex agent",
		Identifiers:  []string{"codex"},
		ModelKey:     "codex",
		Capabilities: sdk.BaseAgentCapabilities(),
	}
}

func newBrokenLoginClient(login *bridgev2.UserLogin, connector *CodexConnector, reason string) *sdk.BrokenLoginClient {
	c := &sdk.BrokenLoginClient{UserLogin: login, Reason: reason}
	c.OnLogout = func(ctx context.Context, login *bridgev2.UserLogin) {
		tmp := &CodexClient{UserLogin: login, connector: connector}
		tmp.purgeCodexHomeBestEffort(ctx)
		tmp.purgeCodexCwdsBestEffort(ctx)
		if connector != nil && login != nil {
			connector.clientsMu.Lock()
			delete(connector.clients, login.ID)
			connector.clientsMu.Unlock()
		}
	}
	return c
}
