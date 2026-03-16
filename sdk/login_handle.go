package sdk

import (
	"context"
	"fmt"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// LoginHandle wraps a UserLogin and provides convenience methods for creating
// conversations and accessing login state.
type LoginHandle struct {
	login   *bridgev2.UserLogin
	runtime conversationRuntime
}

func newLoginHandle(login *bridgev2.UserLogin, runtime conversationRuntime) *LoginHandle {
	return &LoginHandle{
		login:   login,
		runtime: runtime,
	}
}

// Conversation returns a Conversation for the given portal ID.
func (l *LoginHandle) Conversation(ctx context.Context, portalID string) (*Conversation, error) {
	if l.login == nil || l.login.Bridge == nil {
		return nil, fmt.Errorf("login or bridge unavailable")
	}
	portalKey := networkid.PortalKey{
		ID:       networkid.PortalID(portalID),
		Receiver: l.login.ID,
	}
	portal, err := l.login.Bridge.GetExistingPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, fmt.Errorf("portal lookup failed: %w", err)
	}
	if portal == nil {
		return nil, fmt.Errorf("portal %q not found", portalID)
	}
	return newConversation(ctx, portal, l.login, bridgev2.EventSender{}, l.runtime), nil
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
	if portal.Metadata == nil {
		portal.Metadata = &SDKPortalMetadata{}
	}
	conv := newConversation(ctx, portal, l.login, bridgev2.EventSender{}, l.runtime)
	if err := conv.saveState(ctx, state); err != nil {
		return nil, err
	}
	info := &bridgev2.ChatInfo{Name: ptr.NonZero(portal.Name)}
	_, err = EnsurePortalLifecycle(ctx, PortalLifecycleOptions{
		Login:             l.login,
		Portal:            portal,
		ChatInfo:          info,
		SaveBeforeCreate:  true,
		AIRoomKind:        conv.aiRoomKind(),
		ForceCapabilities: true,
		RefreshExtra: func(ctx context.Context, portal *bridgev2.Portal) {
			if l.runtime == nil || l.runtime.config() == nil || len(l.runtime.config().Commands) == 0 {
				return
			}
			BroadcastCommandDescriptions(ctx, portal, l.login.Bridge.Bot, l.runtime.config().Commands)
		},
	})
	if err != nil {
		return nil, err
	}
	return conv, nil
}

// UserLogin returns the underlying bridgev2.UserLogin.
func (l *LoginHandle) UserLogin() *bridgev2.UserLogin {
	return l.login
}
