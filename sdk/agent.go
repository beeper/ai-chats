package sdk

import (
	"context"
	"strings"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// AgentCapabilities contains the SDK-relevant capability truth for an agent.
type AgentCapabilities struct {
	SupportsStreaming   bool
	SupportsReasoning   bool
	SupportsToolCalling bool

	SupportsTextInput  bool
	SupportsImageInput bool
	SupportsAudioInput bool
	SupportsVideoInput bool
	SupportsFileInput  bool
	SupportsPDFInput   bool

	SupportsImageOutput bool
	SupportsAudioOutput bool
	SupportsFilesOutput bool

	MaxTextLength int
}

// Agent is the thin SDK identity model for an AI agent.
type Agent struct {
	ID           string
	Name         string
	Description  string
	AvatarURL    string
	Identifiers  []string
	ModelKey     string
	Capabilities AgentCapabilities
	Metadata     map[string]any
}

// AgentMember is kept as a compatibility alias while the SDK surface migrates.
type AgentMember = Agent

// AgentCatalog resolves agents for contacts, identifier lookup, and default selection.
type AgentCatalog interface {
	DefaultAgent(ctx context.Context, login *bridgev2.UserLogin) (*Agent, error)
	ListAgents(ctx context.Context, login *bridgev2.UserLogin) ([]*Agent, error)
	ResolveAgent(ctx context.Context, login *bridgev2.UserLogin, identifier string) (*Agent, error)
}

// EnsureGhost ensures the ghost user exists in the bridge database.
func (a *Agent) EnsureGhost(ctx context.Context, login *bridgev2.UserLogin) error {
	if a == nil || login == nil || login.Bridge == nil || strings.TrimSpace(a.ID) == "" {
		return nil
	}
	ghost, err := login.Bridge.GetGhostByID(ctx, networkid.UserID(a.ID))
	if err != nil {
		return err
	}
	if ghost == nil {
		return nil
	}
	ghost.UpdateInfo(ctx, a.UserInfo())
	return nil
}

// EventSender returns the bridgev2.EventSender for this agent.
func (a *Agent) EventSender(loginID networkid.UserLoginID) bridgev2.EventSender {
	if a == nil {
		return bridgev2.EventSender{}
	}
	return bridgev2.EventSender{
		Sender:      networkid.UserID(a.ID),
		SenderLogin: loginID,
	}
}

// UserInfo returns a bridgev2.UserInfo for this agent.
func (a *Agent) UserInfo() *bridgev2.UserInfo {
	if a == nil {
		return nil
	}
	info := &bridgev2.UserInfo{
		Name:        ptr.NonZero(a.Name),
		IsBot:       ptr.Ptr(true),
		Identifiers: a.Identifiers,
	}
	return info
}
