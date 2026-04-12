package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func isWelcomeCodexPortal(meta *PortalMetadata) bool {
	return meta != nil && meta.IsCodexRoom && meta.AwaitingCwdSetup
}

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

func (cc *CodexClient) portalConversation(ctx context.Context, portal *bridgev2.Portal) (*bridgesdk.Conversation, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || portal == nil {
		return nil, fmt.Errorf("portal unavailable")
	}
	if portal.MXID == "" {
		return nil, fmt.Errorf("portal has no Matrix room ID")
	}
	if cc.connector == nil || cc.connector.sdkConfig == nil {
		return nil, fmt.Errorf("sdk configuration unavailable")
	}
	return bridgesdk.NewConversation(ctx, cc.UserLogin, portal, bridgev2.EventSender{}, cc.connector.sdkConfig, cc), nil
}

func (cc *CodexClient) setRoomName(ctx context.Context, portal *bridgev2.Portal, name string) error {
	conv, err := cc.portalConversation(ctx, portal)
	if err != nil {
		return err
	}
	if err := conv.SetRoomName(ctx, name); err != nil {
		return fmt.Errorf("failed to set room name: %w", err)
	}
	portal.Name = name
	portal.NameSet = true
	return portal.Save(ctx)
}

func (cc *CodexClient) setRoomTopic(ctx context.Context, portal *bridgev2.Portal, topic string) error {
	conv, err := cc.portalConversation(ctx, portal)
	if err != nil {
		return err
	}
	if err := conv.SetRoomTopic(ctx, topic); err != nil {
		return fmt.Errorf("failed to set room topic: %w", err)
	}
	portal.Topic = topic
	portal.TopicSet = true
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
		"`!codex new` creates a fresh welcome room.",
		"`!codex dirs` lists tracked directories.",
		"`!codex import /abs/path` tracks a directory and imports stored Codex threads for it.",
		"`!codex forget /abs/path` stops tracking a directory and unbridges imported rooms for it.",
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
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || cc.UserLogin.Bridge.DB == nil {
		return nil, nil
	}
	userPortals, err := cc.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, cc.UserLogin.UserLogin)
	if err != nil {
		return nil, err
	}
	out := make([]*bridgev2.Portal, 0, len(userPortals))
	for _, userPortal := range userPortals {
		if userPortal == nil {
			continue
		}
		portal, err := cc.UserLogin.Bridge.GetExistingPortalByKey(ctx, userPortal.Portal)
		if err != nil || portal == nil {
			continue
		}
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
	portalKey, err := codexWelcomePortalKey(cc.UserLogin.ID, generateShortID())
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
	meta.Title = "New Codex Chat"
	meta.Slug = "codex-welcome"
	meta.CodexThreadID = ""
	meta.CodexCwd = ""
	meta.AwaitingCwdSetup = true
	meta.ManagedImport = false
	if err := agentremote.ConfigureDMPortal(ctx, agentremote.ConfigureDMPortalParams{
		Portal:      portal,
		Title:       meta.Title,
		OtherUserID: codexGhostID,
		Save:        false,
	}); err != nil {
		return nil, err
	}
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
		cc.sendSystemNotice(ctx, portal, "Send an absolute path or `~/...` to start a Codex session.")
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
}

func (cc *CodexClient) managedImportedPortalsForPath(ctx context.Context, path string) ([]*bridgev2.Portal, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || cc.UserLogin.Bridge.DB == nil {
		return nil, nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	userPortals, err := cc.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, cc.UserLogin.UserLogin)
	if err != nil {
		return nil, err
	}
	out := make([]*bridgev2.Portal, 0, len(userPortals))
	for _, userPortal := range userPortals {
		if userPortal == nil {
			continue
		}
		portal, err := cc.UserLogin.Bridge.GetExistingPortalByKey(ctx, userPortal.Portal)
		if err != nil || portal == nil {
			continue
		}
		meta := portalMeta(portal)
		if meta == nil || !meta.IsCodexRoom || !meta.ManagedImport || strings.TrimSpace(meta.CodexCwd) != path {
			continue
		}
		out = append(out, portal)
	}
	return out, nil
}

func (cc *CodexClient) forgetManagedDirectory(ctx context.Context, path string) (int, error) {
	portals, err := cc.managedImportedPortalsForPath(ctx, path)
	if err != nil {
		return 0, err
	}
	for _, portal := range portals {
		meta := portalMeta(portal)
		if meta != nil {
			cc.cleanupImportedPortalState(meta.CodexThreadID)
		}
		cc.deletePortalOnly(ctx, portal, "codex directory forgotten")
	}
	return len(portals), nil
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
		if _, err := cc.createWelcomeCodexChat(ctx); err != nil {
			return nil, true, messageSendStatusError(err, "Failed to create a new welcome room.", "")
		}
		cc.sendSystemNotice(ctx, portal, "Created a new welcome room.")
	case "dirs":
		paths := managedCodexPaths(loginMeta)
		if len(paths) == 0 {
			cc.sendSystemNotice(ctx, portal, "No tracked directories yet.")
			break
		}
		cc.sendSystemNotice(ctx, portal, "Tracked directories:\n"+strings.Join(paths, "\n"))
	case "import":
		path, err := cc.resolveManagedPathArgument(args, meta)
		if err != nil {
			cc.sendSystemNotice(ctx, portal, "Usage: `!codex import /abs/path`")
			break
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() {
			cc.sendSystemNotice(ctx, portal, fmt.Sprintf("That path doesn't exist or isn't a directory: %s", path))
			break
		}
		addManagedCodexPath(loginMeta, path)
		if err := cc.UserLogin.Save(ctx); err != nil {
			return nil, true, messageSendStatusError(err, "Failed to save tracked directories.", "")
		}
		total, created, err := cc.syncStoredCodexThreadsForPath(cc.backgroundContext(ctx), path)
		if err != nil {
			return nil, true, messageSendStatusError(err, "Failed to import stored Codex threads.", "")
		}
		if total == 0 {
			cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Tracked %s. No stored Codex threads matched yet.", path))
			break
		}
		cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Tracked %s. Found %d stored Codex thread(s); created %d new room(s).", path, total, created))
	case "forget":
		path, err := cc.resolveManagedPathArgument(args, meta)
		if err != nil {
			cc.sendSystemNotice(ctx, portal, "Usage: `!codex forget /abs/path`")
			break
		}
		if !removeManagedCodexPath(loginMeta, path) {
			cc.sendSystemNotice(ctx, portal, fmt.Sprintf("That directory is not tracked: %s", path))
			break
		}
		if err := cc.UserLogin.Save(ctx); err != nil {
			return nil, true, messageSendStatusError(err, "Failed to update tracked directories.", "")
		}
		removed, err := cc.forgetManagedDirectory(ctx, path)
		if err != nil {
			return nil, true, messageSendStatusError(err, "Failed to forget Codex directory.", "")
		}
		cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Forgot %s and unbridged %d imported room(s).", path, removed))
	default:
		cc.sendSystemNotice(ctx, portal, codexCommandHelpText())
	}
	return &bridgev2.MatrixMessageResponse{Pending: false}, true, nil
}

func (cc *CodexClient) handleWelcomeCodexMessage(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, body string) (*bridgev2.MatrixMessageResponse, error) {
	if cc == nil || cc.UserLogin == nil || portal == nil || meta == nil {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	path, err := resolveCodexWorkingDirectory(body)
	if err != nil {
		cc.sendSystemNotice(ctx, portal, "That path must be absolute. `~/...` is also accepted.")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		cc.sendSystemNotice(ctx, portal, fmt.Sprintf("That path doesn't exist or isn't a directory: %s", path))
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	addManagedCodexPath(loginMetadata(cc.UserLogin), path)
	if err := cc.UserLogin.Save(ctx); err != nil {
		return nil, messageSendStatusError(err, "Failed to save Codex directory.", "")
	}

	meta.CodexCwd = path
	meta.CodexThreadID = ""
	meta.AwaitingCwdSetup = false
	meta.ManagedImport = false
	meta.Title = codexTitleForPath(path)
	meta.Slug = strings.ToLower(strings.ReplaceAll(meta.Title, " ", "-"))
	if err := portal.Save(ctx); err != nil {
		return nil, messageSendStatusError(err, "Failed to save Codex room.", "")
	}
	if err := cc.setRoomName(ctx, portal, meta.Title); err != nil {
		return nil, messageSendStatusError(err, "Failed to rename Codex room.", "")
	}
	if err := cc.ensureRPC(cc.backgroundContext(ctx)); err != nil {
		return nil, messageSendStatusError(err, "Codex isn't available. Sign in again.", "")
	}
	if err := cc.ensureCodexThread(ctx, portal, meta); err != nil {
		return nil, messageSendStatusError(err, "Failed to start Codex thread.", "")
	}
	cc.syncCodexRoomTopic(ctx, portal, meta)
	cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Started a new Codex session in %s", path))
	go func() {
		if _, err := cc.createWelcomeCodexChat(cc.backgroundContext(ctx)); err != nil {
			cc.log.Warn().Err(err).Msg("Failed to create follow-up welcome Codex chat")
		}
	}()
	go func() {
		if _, _, err := cc.syncStoredCodexThreadsForPath(cc.backgroundContext(ctx), path); err != nil {
			cc.log.Warn().Err(err).Str("cwd", path).Msg("Failed to sync stored Codex threads for path")
		}
	}()
	return &bridgev2.MatrixMessageResponse{Pending: false}, nil
}
