package opencodebridge

import (
	"context"
	"errors"
	"strings"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/opencode"
)

func (b *Bridge) ensureOpenCodeSessionPortal(ctx context.Context, inst *openCodeInstance, session opencode.Session) error {
	return b.ensureOpenCodeSessionPortalWithRoom(ctx, inst, session, true)
}

func (b *Bridge) ensureOpenCodeSessionPortalWithRoom(ctx context.Context, inst *openCodeInstance, session opencode.Session, createRoom bool) error {
	if b == nil || b.host == nil || inst == nil {
		return nil
	}
	login := b.host.Login()
	if login == nil || login.Bridge == nil {
		return nil
	}
	if strings.TrimSpace(session.ID) == "" {
		return nil
	}

	portalKey := OpenCodePortalKey(login.ID, inst.cfg.ID, session.ID)
	portal, err := login.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return err
	}
	if portal == nil {
		return nil
	}

	meta := b.portalMeta(portal)
	if meta == nil {
		meta = &PortalMeta{}
	}

	title := strings.TrimSpace(session.Title)
	if title == "" {
		if strings.TrimSpace(session.Slug) != "" {
			title = "OpenCode " + session.Slug
		} else {
			title = "OpenCode Session " + session.ID
		}
	}

	meta.IsOpenCodeRoom = true
	meta.InstanceID = inst.cfg.ID
	meta.SessionID = session.ID
	meta.ReadOnly = !inst.connected
	meta.TitlePending = false
	if meta.AgentID == "" {
		meta.AgentID = b.host.DefaultAgentID()
	}
	meta.Title = title

	previousName := portal.Name
	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = OpenCodeUserID(inst.cfg.ID)
	portal.Name = title
	portal.NameSet = true
	b.host.SetPortalMeta(portal, meta)

	if err := b.host.SavePortal(ctx, portal); err != nil {
		return err
	}

	if portal.MXID == "" {
		if !createRoom {
			return nil
		}
		chatInfo := b.composeOpenCodeChatInfo(title, inst.cfg.ID)
		if err := portal.CreateMatrixRoom(ctx, login, chatInfo); err != nil {
			b.host.CleanupPortal(ctx, portal, "failed to create OpenCode room")
			return err
		}
		return nil
	}

	if portal.MXID != "" && previousName != title {
		_ = b.host.SetRoomName(ctx, portal, title)
	}

	return nil
}

func (b *Bridge) removeOpenCodeSessionPortal(ctx context.Context, instanceID, sessionID, reason string) {
	if b == nil || b.host == nil {
		return
	}
	login := b.host.Login()
	if login == nil || login.Bridge == nil {
		return
	}
	portalKey := OpenCodePortalKey(login.ID, instanceID, sessionID)
	portal, err := login.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil || portal == nil {
		return
	}
	b.host.CleanupPortal(ctx, portal, reason)
}

func (b *Bridge) findOpenCodePortal(ctx context.Context, instanceID, sessionID string) *bridgev2.Portal {
	if b == nil || b.host == nil {
		return nil
	}
	login := b.host.Login()
	if login == nil || login.Bridge == nil {
		return nil
	}
	portalKey := OpenCodePortalKey(login.ID, instanceID, sessionID)
	portal, err := login.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil
	}
	return portal
}

func (b *Bridge) composeOpenCodeChatInfo(title, instanceID string) *bridgev2.ChatInfo {
	if b == nil || b.host == nil {
		return nil
	}
	login := b.host.Login()
	if login == nil {
		return nil
	}
	displayName := b.opencodeDisplayName(instanceID)
	ownUserID := b.host.HumanUserID(login.ID)
	members := bridgev2.ChatMemberMap{
		ownUserID: {
			EventSender: bridgev2.EventSender{
				IsFromMe:    true,
				SenderLogin: login.ID,
			},
			Membership: event.MembershipJoin,
		},
		OpenCodeUserID(instanceID): {
			EventSender: bridgev2.EventSender{
				Sender:      OpenCodeUserID(instanceID),
				SenderLogin: login.ID,
			},
			Membership: event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:  ptr.Ptr(displayName),
				IsBot: ptr.Ptr(true),
			},
			MemberEventExtra: map[string]any{
				"displayname": displayName,
			},
		},
	}

	return &bridgev2.ChatInfo{
		Name:  ptr.Ptr(title),
		Type:  ptr.Ptr(database.RoomTypeDM),
		Topic: nil,
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: OpenCodeUserID(instanceID),
			MemberMap:   members,
			PowerLevels: &bridgev2.PowerLevelOverrides{
				Events: map[event.Type]int{
					b.host.RoomCapabilitiesEventType(): 100,
					b.host.RoomSettingsEventType():     0,
				},
			},
		},
		CanBackfill: true,
	}
}

func (b *Bridge) createOpenCodeSessionChat(ctx context.Context, instanceID, title string, pendingTitle bool) (*bridgev2.CreateChatResponse, error) {
	if b == nil || b.host == nil {
		return nil, errors.New("login unavailable")
	}
	login := b.host.Login()
	if login == nil {
		return nil, errors.New("login unavailable")
	}
	if b.manager == nil {
		return nil, errors.New("OpenCode integration is not available")
	}
	inst := b.manager.getInstance(instanceID)
	if inst == nil {
		return nil, errors.New("OpenCode instance not connected")
	}

	session, err := b.manager.CreateSession(ctx, instanceID, title)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, errors.New("OpenCode session creation failed")
	}

	if err := b.ensureOpenCodeSessionPortalWithRoom(ctx, inst, *session, false); err != nil {
		return nil, err
	}
	portal := b.findOpenCodePortal(ctx, instanceID, session.ID)
	if portal == nil {
		return nil, errors.New("failed to load OpenCode portal")
	}
	meta := b.portalMeta(portal)
	if meta != nil {
		meta.TitlePending = pendingTitle
		if title != "" {
			meta.Title = title
		}
		b.host.SetPortalMeta(portal, meta)
		_ = b.host.SavePortal(ctx, portal)
	}

	displayTitle := title
	if displayTitle == "" && meta != nil {
		displayTitle = meta.Title
	}
	if displayTitle == "" {
		displayTitle = "OpenCode Session " + session.ID
	}

	chatInfo := b.composeOpenCodeChatInfo(displayTitle, instanceID)
	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		PortalInfo: chatInfo,
		Portal:     portal,
	}, nil
}
