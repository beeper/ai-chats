package sdk

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
)

type conversationRuntime interface {
	agent() *Agent
	agentCatalog() AgentCatalog
	roomFeatures(conv *Conversation) *RoomFeatures
	commands() []Command
	turnConfig() *TurnConfig
	conversationStore() *conversationStateStore
	approvalFlowValue() *agentremote.ApprovalFlow[*pendingSDKApprovalData]
	providerIdentity() ProviderIdentity
}

type staticRuntime[SessionT SessionValue, ConfigDataT ConfigValue] struct {
	cfg      *Config[SessionT, ConfigDataT]
	session  SessionT
	login    *bridgev2.UserLogin
	store    *conversationStateStore
	approval *agentremote.ApprovalFlow[*pendingSDKApprovalData]
}

func (r *staticRuntime[SessionT, ConfigDataT]) agent() *Agent {
	if r == nil || r.cfg == nil {
		return nil
	}
	return r.cfg.Agent
}

func (r *staticRuntime[SessionT, ConfigDataT]) agentCatalog() AgentCatalog {
	if r == nil || r.cfg == nil {
		return nil
	}
	return r.cfg.AgentCatalog
}

func (r *staticRuntime[SessionT, ConfigDataT]) roomFeatures(conv *Conversation) *RoomFeatures {
	if r == nil || r.cfg == nil {
		return nil
	}
	if r.cfg.GetCapabilities != nil {
		if rf := r.cfg.GetCapabilities(r.session, conv); rf != nil {
			return rf
		}
	}
	return r.cfg.RoomFeatures
}

func (r *staticRuntime[SessionT, ConfigDataT]) commands() []Command {
	if r == nil || r.cfg == nil {
		return nil
	}
	return r.cfg.Commands
}

func (r *staticRuntime[SessionT, ConfigDataT]) turnConfig() *TurnConfig {
	if r == nil || r.cfg == nil {
		return nil
	}
	return r.cfg.TurnManagement
}

func (r *staticRuntime[SessionT, ConfigDataT]) conversationStore() *conversationStateStore {
	return r.store
}

func (r *staticRuntime[SessionT, ConfigDataT]) approvalFlowValue() *agentremote.ApprovalFlow[*pendingSDKApprovalData] {
	return r.approval
}

func (r *staticRuntime[SessionT, ConfigDataT]) providerIdentity() ProviderIdentity {
	return resolveProviderIdentity(r.cfg)
}

func resolveProviderIdentity[SessionT SessionValue, ConfigDataT ConfigValue](cfg *Config[SessionT, ConfigDataT]) ProviderIdentity {
	if cfg == nil {
		return normalizedProviderIdentity(ProviderIdentity{})
	}
	return normalizedProviderIdentity(cfg.ProviderIdentity)
}

func normalizedProviderIdentity(identity ProviderIdentity) ProviderIdentity {
	if identity.IDPrefix == "" {
		identity.IDPrefix = "sdk"
	}
	if identity.LogKey == "" {
		identity.LogKey = identity.IDPrefix + "_msg_id"
	}
	if identity.StatusNetwork == "" {
		identity.StatusNetwork = identity.IDPrefix
	}
	return identity
}

// NewConversationOptions configures optional parameters for NewConversation.
type NewConversationOptions struct {
	ApprovalFlow *agentremote.ApprovalFlow[*pendingSDKApprovalData]
}

// NewConversation creates an SDK conversation wrapper for provider bridges that
// want to drive SDK turns without using the default sdkClient implementation.
func NewConversation[SessionT SessionValue, ConfigDataT ConfigValue](ctx context.Context, login *bridgev2.UserLogin, portal *bridgev2.Portal, sender bridgev2.EventSender, cfg *Config[SessionT, ConfigDataT], session SessionT, opts ...NewConversationOptions) *Conversation {
	rt := &staticRuntime[SessionT, ConfigDataT]{
		cfg:     cfg,
		session: session,
		login:   login,
	}
	if len(opts) > 0 && opts[0].ApprovalFlow != nil {
		rt.approval = opts[0].ApprovalFlow
	}
	return newConversation(ctx, portal, login, sender, rt)
}
