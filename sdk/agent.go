package sdk

import (
	"context"
	"strings"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
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

const DefaultAgentMaxTextLength = 100000

// BaseAgentCapabilities returns the common capabilities shared by text-first bridge agents.
func BaseAgentCapabilities() AgentCapabilities {
	return AgentCapabilities{
		SupportsStreaming:   true,
		SupportsReasoning:   true,
		SupportsToolCalling: true,
		SupportsTextInput:   true,
		SupportsFilesOutput: true,
		MaxTextLength:       DefaultAgentMaxTextLength,
	}
}

// MultimodalAgentCapabilities extends the base agent capabilities with broad media input support.
func MultimodalAgentCapabilities() AgentCapabilities {
	caps := BaseAgentCapabilities()
	caps.SupportsImageInput = true
	caps.SupportsAudioInput = true
	caps.SupportsVideoInput = true
	caps.SupportsFileInput = true
	caps.SupportsPDFInput = true
	return caps
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
	if a.AvatarURL != "" {
		info.Avatar = &bridgev2.Avatar{
			ID:  networkid.AvatarID(a.AvatarURL),
			MXC: id.ContentURIString(a.AvatarURL),
		}
	}
	return info
}
