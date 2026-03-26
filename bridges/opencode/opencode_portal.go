package opencode

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/opencode/api"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func (b *Bridge) ensureOpenCodeSessionPortal(ctx context.Context, inst *openCodeInstance, session api.Session) error {
	return b.ensureOpenCodeSessionPortalWithRoom(ctx, inst, session, true)
}

// defaultPortalLifecycleOptions returns the standard PortalLifecycleOptions
// shared by all OpenCode room creation paths.
func (b *Bridge) defaultPortalLifecycleOptions(login *bridgev2.UserLogin, portal *bridgev2.Portal, chatInfo *bridgev2.ChatInfo) bridgesdk.PortalLifecycleOptions {
	return bridgesdk.PortalLifecycleOptions{
		Login:            login,
		Portal:           portal,
		ChatInfo:         chatInfo,
		SaveBeforeCreate: true,
		CleanupOnCreateError: func(ctx context.Context, portal *bridgev2.Portal) {
			b.host.CleanupPortal(ctx, portal, "failed to create OpenCode room")
		},
		AIRoomKind:        agentremote.AIRoomKindAgent,
		ForceCapabilities: true,
	}
}

func (b *Bridge) ensureOpenCodeSessionPortalWithRoom(ctx context.Context, inst *openCodeInstance, session api.Session, createRoom bool) error {
	if b == nil || b.host == nil || inst == nil {
		return nil
	}
	login := b.host.GetUserLogin()
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

	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = OpenCodeUserID(inst.cfg.ID)
	portal.Name = title
	portal.NameSet = true
	b.host.SetPortalMeta(portal, meta)

	chatInfo := b.composeOpenCodeChatInfo(title, inst.cfg.ID)
	if !createRoom && portal.MXID == "" {
		return nil
	}
	_, err = bridgesdk.EnsurePortalLifecycle(ctx, b.defaultPortalLifecycleOptions(login, portal, chatInfo))
	if err != nil {
		return err
	}

	return nil
}

func (b *Bridge) removeOpenCodeSessionPortal(ctx context.Context, instanceID, sessionID, reason string) {
	if b == nil || b.host == nil {
		return
	}
	login := b.host.GetUserLogin()
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
	login := b.host.GetUserLogin()
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
	login := b.host.GetUserLogin()
	if login == nil {
		return nil
	}
	return agentremote.BuildLoginDMChatInfo(agentremote.LoginDMChatInfoParams{
		Title:             title,
		Login:             login,
		HumanUserIDPrefix: "opencode-user",
		BotUserID:         OpenCodeUserID(instanceID),
		BotDisplayName:    b.DisplayName(instanceID),
		CanBackfill:       true,
	})
}

func (b *Bridge) CreateSessionChat(ctx context.Context, instanceID, title string, pendingTitle bool) (*bridgev2.CreateChatResponse, error) {
	if b == nil || b.host == nil {
		return nil, errors.New("login unavailable")
	}
	login := b.host.GetUserLogin()
	if login == nil {
		return nil, errors.New("login unavailable")
	}
	if b.manager == nil {
		return nil, errors.New("OpenCode integration is not available")
	}
	cfg := b.InstanceConfig(instanceID)
	if cfg == nil {
		return nil, errors.New("OpenCode instance not found")
	}
	if cfg.Mode == OpenCodeModeManagedLauncher {
		return b.createManagedLauncherChat(ctx, login, instanceID, title, pendingTitle)
	}
	inst := b.manager.getInstance(instanceID)
	if inst == nil || !inst.connected {
		return nil, errors.New("OpenCode instance not connected")
	}
	session, err := b.manager.CreateSession(ctx, instanceID, title, "")
	if err != nil {
		return nil, err
	}
	if err = b.ensureOpenCodeSessionPortalWithRoom(ctx, inst, *session, true); err != nil {
		return nil, err
	}
	portal := b.findOpenCodePortal(ctx, instanceID, session.ID)
	if portal == nil {
		return nil, errors.New("failed to create OpenCode portal")
	}
	meta := b.portalMeta(portal)
	meta.TitlePending = pendingTitle
	if title != "" {
		meta.Title = title
	}
	b.host.SetPortalMeta(portal, meta)
	if err = b.host.SavePortal(ctx, portal); err != nil {
		return nil, err
	}
	chatInfo := b.composeOpenCodeChatInfo(portal.Name, instanceID)
	b.host.SendSystemNotice(ctx, portal, "AI Chats can make mistakes.")
	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		PortalInfo: chatInfo,
		Portal:     portal,
	}, nil
}

func (b *Bridge) createManagedLauncherChat(ctx context.Context, login *bridgev2.UserLogin, instanceID, title string, pendingTitle bool) (*bridgev2.CreateChatResponse, error) {
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
		AwaitingPath:   true,
		TitlePending:   pendingTitle,
		Title:          displayTitle,
		AgentID:        b.host.DefaultAgentID(),
	}

	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = OpenCodeUserID(instanceID)
	portal.Name = displayTitle
	portal.NameSet = true
	b.host.SetPortalMeta(portal, meta)

	chatInfo := b.composeOpenCodeChatInfo(displayTitle, instanceID)
	_, err = bridgesdk.EnsurePortalLifecycle(ctx, b.defaultPortalLifecycleOptions(login, portal, chatInfo))
	if err != nil {
		return nil, err
	}

	b.host.SendSystemNotice(ctx, portal, "AI Chats can make mistakes.")
	b.host.SendSystemNotice(ctx, portal, "What directory should OpenCode work in? Send an absolute path or `~/...`, or send an empty message to use the managed default path.")

	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		PortalInfo: chatInfo,
		Portal:     portal,
	}, nil
}

func (b *Bridge) ReIDPortalToSession(ctx context.Context, portal *bridgev2.Portal, instanceID, sessionID string) (*bridgev2.Portal, error) {
	if b == nil || b.host == nil || portal == nil {
		return portal, nil
	}
	login := b.host.GetUserLogin()
	if login == nil || login.Bridge == nil {
		return portal, errors.New("login unavailable")
	}
	target := OpenCodePortalKey(login.ID, instanceID, sessionID)
	if portal.PortalKey == target {
		return portal, nil
	}
	result, updated, err := login.Bridge.ReIDPortal(ctx, portal.PortalKey, target)
	if err != nil {
		return nil, err
	}
	switch result {
	case bridgev2.ReIDResultSourceReIDd, bridgev2.ReIDResultTargetDeletedAndSourceReIDd, bridgev2.ReIDResultNoOp:
		var refreshed *bridgev2.Portal
		if updated != nil {
			refreshed = updated
		} else {
			refreshed = b.findOpenCodePortal(ctx, instanceID, sessionID)
		}
		if refreshed != nil {
			bridgesdk.RefreshPortalLifecycle(ctx, bridgesdk.PortalLifecycleOptions{
				Login:             login,
				Portal:            refreshed,
				AIRoomKind:        agentremote.AIRoomKindAgent,
				ForceCapabilities: true,
			})
		}
		return refreshed, nil
	default:
		return nil, fmt.Errorf("unexpected portal re-id result: %v", result)
	}
}
