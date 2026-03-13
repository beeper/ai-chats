package sdk

import (
	"context"

	"github.com/beeper/agentremote"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// LoginHandle wraps a UserLogin and provides convenience methods for creating
// conversations and accessing login state.
type LoginHandle struct {
	login  *bridgev2.UserLogin
	client *sdkClient
}

func newLoginHandle(login *bridgev2.UserLogin, client *sdkClient) *LoginHandle {
	return &LoginHandle{
		login:  login,
		client: client,
	}
}

// Conversation returns a Conversation for the given portal ID.
func (l *LoginHandle) Conversation(ctx context.Context, portalID string) *Conversation {
	if l.login == nil || l.login.Bridge == nil {
		return newConversation(ctx, nil, l.login, bridgev2.EventSender{}, l.client)
	}
	portalKey := networkid.PortalKey{
		ID: networkid.PortalID(portalID),
	}
	if l.login != nil {
		portalKey.Receiver = l.login.ID
	}
	portal, err := l.login.Bridge.GetExistingPortalByKey(ctx, portalKey)
	if err != nil || portal == nil {
		return newConversation(ctx, nil, l.login, bridgev2.EventSender{}, l.client)
	}
	return newConversation(ctx, portal, l.login, bridgev2.EventSender{}, l.client)
}

// ConversationByPortal returns a Conversation for the given bridgev2.Portal.
func (l *LoginHandle) ConversationByPortal(ctx context.Context, portal *bridgev2.Portal) *Conversation {
	return newConversation(ctx, portal, l.login, bridgev2.EventSender{}, l.client)
}

// EnsureConversation resolves or creates a conversation for the given spec.
func (l *LoginHandle) EnsureConversation(ctx context.Context, spec ConversationSpec) (*Conversation, error) {
	if l == nil || l.login == nil || l.login.Bridge == nil {
		return nil, nil
	}
	spec = normalizeConversationSpec(spec)
	portal, err := ensureConversationPortal(ctx, l.login, spec)
	if err != nil {
		return nil, err
	}

	state := conversationStateFromSpec(spec)
	if spec.PrimaryAgentID != "" {
		state.RoomAgents.PrimaryAgentID = spec.PrimaryAgentID
		state.RoomAgents.AgentIDs = []string{spec.PrimaryAgentID}
	}

	if portal.Metadata == nil {
		portal.Metadata = &SDKPortalMetadata{}
	}
	var store *conversationStateStore
	if l.client != nil {
		store = l.client.conversationState
	}
	if err := saveConversationState(ctx, portal, store, state); err != nil {
		return nil, err
	}
	conv := newConversation(ctx, portal, l.login, bridgev2.EventSender{}, l.client)
	if portal.MXID == "" {
		info := &bridgev2.ChatInfo{Name: ptr.NonZero(portal.Name)}
		if err := portal.CreateMatrixRoom(ctx, l.login, info); err != nil {
			return nil, err
		}
	}
	agentremote.SendAIRoomInfo(ctx, portal, conv.aiRoomKind())
	return conv, nil
}

// UserLogin returns the underlying bridgev2.UserLogin.
func (l *LoginHandle) UserLogin() *bridgev2.UserLogin {
	return l.login
}
