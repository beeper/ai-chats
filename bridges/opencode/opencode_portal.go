package opencode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
	"github.com/beeper/agentremote/sdk"
)

func (b *Bridge) ensureOpenCodeSessionPortal(ctx context.Context, inst *openCodeInstance, session api.Session) error {
	return b.ensureOpenCodeSessionPortalWithRoom(ctx, inst, session, true)
}

func openCodeSessionTitle(session api.Session) string {
	title := strings.TrimSpace(session.Title)
	if title != "" {
		return title
	}
	if strings.TrimSpace(session.Slug) != "" {
		return "OpenCode " + session.Slug
	}
	return "OpenCode Session " + session.ID
}

func (b *Bridge) bootstrapOpenCodePortal(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	title string,
	meta *PortalMeta,
	createRoom bool,
) (*bridgev2.Portal, *bridgev2.ChatInfo, bool, error) {
	if b == nil || b.host == nil {
		return nil, nil, false, nil
	}
	if login == nil {
		login = b.host.GetUserLogin()
	}
	if login == nil || login.Bridge == nil || portal == nil || meta == nil {
		return nil, nil, false, errors.New("login unavailable")
	}
	chatInfo := b.composeOpenCodeChatInfo(title, meta.InstanceID)
	if err := bridgeutil.ConfigureDMPortal(ctx, bridgeutil.ConfigureDMPortalParams{
		Portal:      portal,
		Title:       title,
		OtherUserID: OpenCodeUserID(meta.InstanceID),
		Save:        false,
		MutatePortal: func(portal *bridgev2.Portal) {
			b.host.SetPortalMeta(portal, meta)
		},
	}); err != nil {
		return nil, nil, false, err
	}
	if err := portal.Save(ctx); err != nil {
		return nil, nil, false, fmt.Errorf("failed to save portal: %w", err)
	}
	if !createRoom {
		return portal, chatInfo, false, nil
	}
	created := portal.MXID == ""
	if created {
		if err := portal.CreateMatrixRoom(ctx, login, chatInfo); err != nil {
			b.host.CleanupPortal(ctx, portal, "failed to create OpenCode room")
			return nil, nil, false, err
		}
	} else {
		portal.UpdateInfo(ctx, chatInfo, login, nil, time.Time{})
	}
	portal.UpdateBridgeInfo(ctx)
	portal.UpdateCapabilities(ctx, login, true)
	return portal, chatInfo, created, nil
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

	title := openCodeSessionTitle(session)
	meta := b.applyOpenCodePortalMeta(b.portalMeta(portal), openCodePortalMetaUpdate{
		setInstanceID: true,
		instanceID:    inst.cfg.ID,
		setSessionID:  true,
		sessionID:     session.ID,
		setReadOnly:   true,
		readOnly:      !inst.connected,
		setPhase:      true,
		phase:         openCodePortalPhaseReady,
		setTitle:      true,
		title:         title,
		ensureAgent:   true,
	})

	_, _, _, err = b.bootstrapOpenCodePortal(ctx, login, portal, title, meta, createRoom)
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
	return bridgeutil.BuildLoginDMChatInfo(bridgeutil.LoginDMChatInfoParams{
		Title:          title,
		Login:          login,
		HumanUserID:    sdk.HumanUserID("opencode-user", login.ID),
		BotUserID:      OpenCodeUserID(instanceID),
		BotDisplayName: b.DisplayName(instanceID),
		CanBackfill:    true,
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
	portalKey := OpenCodePortalKey(login.ID, inst.cfg.ID, session.ID)
	portal, err := login.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, err
	}
	if portal == nil {
		return nil, errors.New("failed to create OpenCode portal")
	}
	displayTitle := openCodeSessionTitle(*session)
	if title != "" {
		displayTitle = title
	}
	meta := b.applyOpenCodePortalMeta(b.portalMeta(portal), openCodePortalMetaUpdate{
		setInstanceID: true,
		instanceID:    inst.cfg.ID,
		setSessionID:  true,
		sessionID:     session.ID,
		setReadOnly:   true,
		readOnly:      !inst.connected,
		setPhase:      true,
		phase:         openCodePortalPhaseReady,
		setTitle:      true,
		title:         displayTitle,
		ensureAgent:   true,
	})
	if pendingTitle {
		meta = b.applyOpenCodePortalMeta(meta, openCodePortalMetaUpdate{
			setPhase:    true,
			phase:       openCodePortalPhaseActiveTitlePending,
			ensureAgent: true,
		})
	}
	portal, chatInfo, _, err := b.bootstrapOpenCodePortal(ctx, login, portal, displayTitle, meta, true)
	if err != nil {
		return nil, err
	}
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

	meta := b.applyOpenCodePortalMeta(nil, openCodePortalMetaUpdate{
		setInstanceID: true,
		instanceID:    instanceID,
		setPhase:      true,
		phase:         openCodePortalPhaseSetup,
		setTitle:      true,
		title:         displayTitle,
		ensureAgent:   true,
	})
	if pendingTitle {
		meta = b.applyOpenCodePortalMeta(meta, openCodePortalMetaUpdate{
			setPhase:    true,
			phase:       openCodePortalPhaseSetupTitlePending,
			ensureAgent: true,
		})
	}

	portal, chatInfo, _, err := b.bootstrapOpenCodePortal(ctx, login, portal, displayTitle, meta, true)
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
			refreshed.UpdateBridgeInfo(ctx)
			refreshed.UpdateCapabilities(ctx, login, true)
		}
		return refreshed, nil
	default:
		return nil, fmt.Errorf("unexpected portal re-id result: %v", result)
	}
}
