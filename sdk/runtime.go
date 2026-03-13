package sdk

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
)

type conversationRuntime interface {
	config() *Config
	sessionValue() any
	loginValue() *bridgev2.UserLogin
	conversationStore() *conversationStateStore
	approvalFlowValue() *agentremote.ApprovalFlow[*pendingSDKApprovalData]
	providerIdentity() ProviderIdentity
}

type staticRuntime struct {
	cfg      *Config
	session  any
	login    *bridgev2.UserLogin
	store    *conversationStateStore
	approval *agentremote.ApprovalFlow[*pendingSDKApprovalData]
}

func (r *staticRuntime) config() *Config { return r.cfg }

func (r *staticRuntime) sessionValue() any { return r.session }

func (r *staticRuntime) loginValue() *bridgev2.UserLogin { return r.login }

func (r *staticRuntime) conversationStore() *conversationStateStore { return r.store }

func (r *staticRuntime) approvalFlowValue() *agentremote.ApprovalFlow[*pendingSDKApprovalData] {
	return r.approval
}

func (r *staticRuntime) providerIdentity() ProviderIdentity {
	return resolveProviderIdentity(r.cfg)
}

func resolveProviderIdentity(cfg *Config) ProviderIdentity {
	if cfg == nil {
		return defaultProviderIdentity()
	}
	return normalizedProviderIdentity(cfg.ProviderIdentity)
}

func defaultProviderIdentity() ProviderIdentity {
	return ProviderIdentity{
		IDPrefix:      "sdk",
		LogKey:        "sdk_msg_id",
		StatusNetwork: "sdk",
	}
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
func NewConversation(ctx context.Context, login *bridgev2.UserLogin, portal *bridgev2.Portal, sender bridgev2.EventSender, cfg *Config, session any, opts ...NewConversationOptions) *Conversation {
	rt := &staticRuntime{
		cfg:     cfg,
		session: session,
		login:   login,
	}
	if len(opts) > 0 && opts[0].ApprovalFlow != nil {
		rt.approval = opts[0].ApprovalFlow
	}
	return newConversation(ctx, portal, login, sender, rt)
}
