package sdk

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

type conversationRuntimeState struct {
	agent                *Agent
	agentCatalog         AgentCatalog
	roomFeatures         *RoomFeatures
	roomFeaturesOverride func(*Conversation) *RoomFeatures
	turnConfig           *TurnConfig
	store                *conversationStateStore
	approvalFlow         *ApprovalFlow[*pendingSDKApprovalData]
	providerIdentity     ProviderIdentity
}

func newConversationRuntimeState[SessionT SessionValue, ConfigDataT ConfigValue](
	cfg *Config[SessionT, ConfigDataT],
	session SessionT,
	store *conversationStateStore,
	approval *ApprovalFlow[*pendingSDKApprovalData],
) *conversationRuntimeState {
	state := &conversationRuntimeState{
		store:            store,
		approvalFlow:     approval,
		providerIdentity: normalizedProviderIdentity(ProviderIdentity{}),
	}
	if cfg == nil {
		return state
	}
	state.providerIdentity = normalizedProviderIdentity(cfg.ProviderIdentity)
	state.agent = cfg.Agent
	state.agentCatalog = cfg.AgentCatalog
	state.roomFeatures = cfg.RoomFeatures
	state.turnConfig = cfg.TurnManagement
	if cfg.GetCapabilities != nil {
		state.roomFeaturesOverride = func(conv *Conversation) *RoomFeatures {
			return cfg.GetCapabilities(session, conv)
		}
	}
	return state
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
	ApprovalFlow *ApprovalFlow[*pendingSDKApprovalData]
}

// NewConversation creates an SDK conversation wrapper for provider bridges that
// want to drive SDK turns without using the default sdkClient implementation.
func NewConversation[SessionT SessionValue, ConfigDataT ConfigValue](ctx context.Context, login *bridgev2.UserLogin, portal *bridgev2.Portal, sender bridgev2.EventSender, cfg *Config[SessionT, ConfigDataT], session SessionT, opts ...NewConversationOptions) *Conversation {
	var approval *ApprovalFlow[*pendingSDKApprovalData]
	if len(opts) > 0 && opts[0].ApprovalFlow != nil {
		approval = opts[0].ApprovalFlow
	}
	return newConversation(ctx, portal, login, sender, newConversationRuntimeState(cfg, session, newConversationStateStore(), approval))
}
