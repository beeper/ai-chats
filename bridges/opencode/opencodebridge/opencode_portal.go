package opencodebridge

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
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
	return bridgeadapter.BuildDMChatInfo(bridgeadapter.DMChatInfoParams{
		Title:             title,
		HumanUserID:       b.host.HumanUserID(login.ID),
		LoginID:           login.ID,
		BotUserID:         OpenCodeUserID(instanceID),
		BotDisplayName:    b.opencodeDisplayName(instanceID),
		CanBackfill:       true,
		CapabilitiesEvent: b.host.RoomCapabilitiesEventType(),
		SettingsEvent:     b.host.RoomSettingsEventType(),
	})
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

	// Use a placeholder session ID; the real session is created after the
	// user provides a working directory path.
	placeholderSessionID := "setup-" + uuid.New().String()

	displayTitle := title
	if displayTitle == "" {
		displayTitle = "OpenCode Session"
	}

	portalKey := OpenCodePortalKey(login.ID, instanceID, placeholderSessionID)
	portal, err := login.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, err
	}

	meta := &PortalMeta{
		IsOpenCodeRoom: true,
		InstanceID:     instanceID,
		SessionID:      "",
		AwaitingPath:   true,
		TitlePending:   pendingTitle,
		Title:          displayTitle,
	}
	if meta.AgentID == "" {
		meta.AgentID = b.host.DefaultAgentID()
	}

	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = OpenCodeUserID(instanceID)
	portal.Name = displayTitle
	portal.NameSet = true
	b.host.SetPortalMeta(portal, meta)

	if err := b.host.SavePortal(ctx, portal); err != nil {
		return nil, err
	}

	chatInfo := b.composeOpenCodeChatInfo(displayTitle, instanceID)
	if err := portal.CreateMatrixRoom(ctx, login, chatInfo); err != nil {
		b.host.CleanupPortal(ctx, portal, "failed to create OpenCode room")
		return nil, err
	}

	b.host.SendSystemNotice(ctx, portal, "AI Chats can make mistakes.")
	b.host.SendSystemNotice(ctx, portal, "What directory should OpenCode work in? Send an absolute path.")

	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		PortalInfo: chatInfo,
		Portal:     portal,
	}, nil
}
