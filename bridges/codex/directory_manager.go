package codex

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func codexTopicForPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return fmt.Sprintf("Working directory: %s", path)
}

func codexTitleForPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "Codex"
	}
	base := strings.TrimSpace(filepath.Base(path))
	switch base {
	case "", ".", string(filepath.Separator):
		return path
	default:
		return base
	}
}

func (cc *CodexClient) codexTopicForPortal(_ *bridgev2.Portal, meta *PortalMetadata) string {
	if meta == nil || isWelcomeCodexPortal(meta) {
		return ""
	}
	return codexTopicForPath(meta.CodexCwd)
}

func (cc *CodexClient) setRoomName(ctx context.Context, portal *bridgev2.Portal, name string) error {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || portal == nil {
		return fmt.Errorf("portal unavailable")
	}
	if portal.MXID == "" {
		return fmt.Errorf("portal has no Matrix room ID")
	}
	_, err := cc.UserLogin.Bridge.Bot.SendState(ctx, portal.MXID, event.StateRoomName, "", &event.Content{
		Parsed: &event.RoomNameEventContent{Name: name},
	}, time.Time{})
	if err != nil {
		return fmt.Errorf("failed to set room name: %w", err)
	}
	portal.Name = name
	portal.NameSet = true
	return portal.Save(ctx)
}

func (cc *CodexClient) setRoomTopic(ctx context.Context, portal *bridgev2.Portal, topic string) error {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || portal == nil {
		return fmt.Errorf("portal unavailable")
	}
	if portal.MXID == "" {
		return fmt.Errorf("portal has no Matrix room ID")
	}
	_, err := cc.UserLogin.Bridge.Bot.SendState(ctx, portal.MXID, event.StateTopic, "", &event.Content{
		Parsed: &event.TopicEventContent{Topic: topic},
	}, time.Time{})
	if err != nil {
		return fmt.Errorf("failed to set room topic: %w", err)
	}
	portal.Topic = topic
	return portal.Save(ctx)
}

func (cc *CodexClient) syncCodexRoomTopic(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) {
	if cc == nil || portal == nil || meta == nil {
		return
	}
	want := cc.codexTopicForPortal(portal, meta)
	if strings.TrimSpace(portal.Topic) == strings.TrimSpace(want) {
		return
	}
	if err := cc.setRoomTopic(ctx, portal, want); err != nil {
		cc.log.Warn().Err(err).Stringer("room", portal.MXID).Msg("Failed to sync Codex room topic")
	}
}

func parseCodexCommand(body string) (string, string, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", false
	}
	fields := strings.Fields(body)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "!codex") {
		return "", "", false
	}
	if len(fields) == 1 {
		return "help", "", true
	}
	command := strings.ToLower(strings.TrimSpace(fields[1]))
	args := strings.TrimSpace(strings.TrimPrefix(body, fields[0]))
	args = strings.TrimSpace(strings.TrimPrefix(args, fields[1]))
	return command, args, true
}

func codexCommandHelpText() string {
	return strings.Join([]string{
		"`!codex help` shows this message.",
		"`!codex dirs` lists tracked workspaces.",
		"`!codex new /abs/path` starts a fresh Codex thread.",
		"`!codex add /abs/path` tracks a workspace and backfills stored Codex threads for that subtree.",
		"`!codex remove /abs/path` untracks a workspace and deletes bridged rooms for that subtree.",
	}, "\n")
}

func (cc *CodexClient) resolveManagedPathArgument(args string, meta *PortalMetadata) (string, error) {
	args = strings.TrimSpace(args)
	if args != "" {
		return resolveCodexWorkingDirectory(args)
	}
	if meta != nil && strings.TrimSpace(meta.CodexCwd) != "" {
		return strings.TrimSpace(meta.CodexCwd), nil
	}
	return "", fmt.Errorf("path is required")
}

func (cc *CodexClient) welcomeCodexPortals(ctx context.Context) ([]*bridgev2.Portal, error) {
	portals, err := cc.allCodexPortals(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*bridgev2.Portal, 0, len(portals))
	for _, portal := range portals {
		if isWelcomeCodexPortal(portalMeta(portal)) {
			out = append(out, portal)
		}
	}
	return out, nil
}

func (cc *CodexClient) createWelcomeCodexChat(ctx context.Context) (*bridgev2.Portal, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return nil, fmt.Errorf("login unavailable")
	}
	portalKey, err := codexWelcomePortalKey(cc.UserLogin.ID)
	if err != nil {
		return nil, err
	}
	portal, err := cc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, err
	}
	if portal.Metadata == nil {
		portal.Metadata = &PortalMetadata{}
	}
	meta := portalMeta(portal)
	meta.IsCodexRoom = true
	meta.PortalKind = codexPortalKindWelcome
	meta.Title = "Codex"
	meta.Slug = "codex-welcome"
	meta.CodexThreadID = ""
	meta.CodexCwd = ""
	meta.WorkspaceRoot = ""
	meta.ManagedImport = false
	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = codexGhostID
	portal.Name = meta.Title
	portal.NameSet = true
	info := cc.composeCodexChatInfo(portal, meta.Title, false)
	created, err := bridgesdk.EnsurePortalLifecycle(ctx, bridgesdk.PortalLifecycleOptions{
		Login:             cc.UserLogin,
		Portal:            portal,
		ChatInfo:          info,
		SaveBeforeCreate:  true,
		AIRoomKind:        agentremote.AIRoomKindAgent,
		ForceCapabilities: true,
	})
	if err != nil {
		return nil, err
	}
	if created {
		cc.sendSystemNotice(ctx, portal, "AI Chats can make mistakes.")
		cc.sendSystemNotice(ctx, portal, "Use `!codex new /abs/path` to start a fresh Codex thread.")
	}
	if err := portal.Save(ctx); err != nil {
		return nil, err
	}
	cc.syncCodexRoomTopic(ctx, portal, meta)
	return portal, nil
}

func (cc *CodexClient) ensureWelcomeCodexChat(ctx context.Context) error {
	cc.defaultChatMu.Lock()
	defer cc.defaultChatMu.Unlock()

	portals, err := cc.welcomeCodexPortals(ctx)
	if err != nil {
		return err
	}
	if len(portals) > 0 {
		for _, extra := range portals[1:] {
			cc.deletePortalOnly(ctx, extra, "duplicate codex welcome room")
		}
		return nil
	}
	_, err = cc.createWelcomeCodexChat(ctx)
	return err
}

func (cc *CodexClient) cleanupImportedPortalState(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || cc == nil {
		return
	}
	cc.loadedMu.Lock()
	delete(cc.loadedThreads, threadID)
	cc.loadedMu.Unlock()

	cc.activeMu.Lock()
	for key, active := range cc.activeTurns {
		if active != nil && strings.TrimSpace(active.threadID) == threadID {
			delete(cc.activeTurns, key)
		}
	}
	cc.activeMu.Unlock()
}

func (cc *CodexClient) deletePortalOnly(ctx context.Context, portal *bridgev2.Portal, reason string) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || portal == nil {
		return
	}
	if portal.MXID != "" {
		if err := portal.Delete(ctx); err != nil {
			cc.log.Warn().Err(err).
				Str("portal_id", string(portal.PortalKey.ID)).
				Stringer("mxid", portal.MXID).
				Str("reason", reason).
				Msg("Failed to delete Matrix room during Codex cleanup")
		}
	}
	if err := cc.UserLogin.Bridge.DB.Portal.Delete(ctx, portal.PortalKey); err != nil {
		cc.log.Warn().Err(err).
			Str("portal_id", string(portal.PortalKey.ID)).
			Str("reason", reason).
			Msg("Failed to delete Codex portal record")
	}
}

func (cc *CodexClient) managedImportedPortalsForPath(ctx context.Context, path string) ([]*bridgev2.Portal, error) {
	portals, err := cc.allCodexPortals(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*bridgev2.Portal, 0, len(portals))
	for _, portal := range portals {
		meta := portalMeta(portal)
		if meta == nil || !isCodexChatPortal(meta) || !meta.ManagedImport || !workspaceContains(path, meta.CodexCwd) {
			continue
		}
		out = append(out, portal)
	}
	return out, nil
}

func (cc *CodexClient) handleCodexCommand(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, body string) (*bridgev2.MatrixMessageResponse, bool, error) {
	command, args, ok := parseCodexCommand(body)
	if !ok {
		return nil, false, nil
	}
	if cc == nil || cc.UserLogin == nil || portal == nil {
		return &bridgev2.MatrixMessageResponse{Pending: false}, true, nil
	}

	loginMeta := loginMetadata(cc.UserLogin)
	switch command {
	case "help":
		cc.sendSystemNotice(ctx, portal, codexCommandHelpText())
	case "new":
		path, err := cc.resolveManagedPathArgument(args, meta)
		if err != nil {
			cc.sendSystemNotice(ctx, portal, "Usage: `!codex new /abs/path`")
			break
		}
		path, err = resolveExistingDirectory(path)
		if err != nil {
			cc.sendSystemNotice(ctx, portal, fmt.Sprintf("That path doesn't exist or isn't a directory: %s", path))
			break
		}
		newPortal, err := cc.createFreshCodexChat(ctx, path)
		if err != nil {
			return nil, true, messageSendStatusError(err, "Failed to start a new Codex thread.", "")
		}
		cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Started a new Codex thread in %s (%s).", path, newPortal.MXID))
	case "dirs":
		paths := managedCodexPaths(loginMeta)
		if len(paths) == 0 {
			cc.sendSystemNotice(ctx, portal, "No tracked workspaces.")
			break
		}
		cc.sendSystemNotice(ctx, portal, "Tracked workspaces:\n"+strings.Join(paths, "\n"))
	case "add":
		path, err := cc.resolveManagedPathArgument(args, meta)
		if err != nil {
			cc.sendSystemNotice(ctx, portal, "Usage: `!codex add /abs/path`")
			break
		}
		path, err = resolveExistingDirectory(path)
		if err != nil {
			cc.sendSystemNotice(ctx, portal, fmt.Sprintf("That path doesn't exist or isn't a directory: %s", path))
			break
		}
		added, err := cc.trackWorkspace(ctx, path, "matrix")
		if err != nil {
			return nil, true, messageSendStatusError(err, "Failed to track Codex workspace.", "")
		}
		if added {
			cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Tracked %s.", path))
		} else {
			cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Workspace already tracked: %s", path))
		}
	case "remove":
		path, err := cc.resolveManagedPathArgument(args, meta)
		if err != nil {
			cc.sendSystemNotice(ctx, portal, "Usage: `!codex remove /abs/path`")
			break
		}
		path = filepath.Clean(path)
		removed, err := cc.untrackWorkspace(ctx, path, "matrix")
		if err != nil {
			return nil, true, messageSendStatusError(err, "Failed to remove Codex workspace.", "")
		}
		if !removed {
			cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Workspace is not tracked: %s", path))
			break
		}
		cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Removed %s.", path))
	default:
		cc.sendSystemNotice(ctx, portal, codexCommandHelpText())
	}
	return &bridgev2.MatrixMessageResponse{Pending: false}, true, nil
}
