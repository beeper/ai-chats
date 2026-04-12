package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

func isWelcomeCodexPortal(state *codexPortalState) bool {
	return state != nil && state.AwaitingCwdSetup
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

func (cc *CodexClient) codexTopicForPortal(_ *bridgev2.Portal, state *codexPortalState) string {
	if state == nil || isWelcomeCodexPortal(state) {
		return ""
	}
	return codexTopicForPath(state.CodexCwd)
}

func (cc *CodexClient) syncCodexRoomTopic(ctx context.Context, portal *bridgev2.Portal, state *codexPortalState) {
	if cc == nil || portal == nil || state == nil || portal.MXID == "" {
		return
	}
	info := cc.composeCodexChatInfo(portal, state, strings.TrimSpace(state.CodexThreadID) != "")
	if info == nil {
		return
	}
	portal.UpdateInfo(ctx, info, cc.UserLogin, nil, time.Time{})
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

func (cc *CodexClient) resolveManagedPathArgument(args string, state *codexPortalState) (string, error) {
	args = strings.TrimSpace(args)
	if args != "" {
		return resolveCodexWorkingDirectory(args)
	}
	if state != nil && strings.TrimSpace(state.CodexCwd) != "" {
		return strings.TrimSpace(state.CodexCwd), nil
	}
	return "", fmt.Errorf("path is required")
}

func (cc *CodexClient) welcomeCodexPortals(ctx context.Context) ([]*bridgev2.Portal, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || cc.UserLogin.Bridge.DB == nil {
		return nil, nil
	}
	records, err := listCodexPortalStateRecords(ctx, cc.UserLogin)
	if err != nil {
		return nil, err
	}
	out := make([]*bridgev2.Portal, 0, len(records))
	for _, record := range records {
		if record.State == nil || !isWelcomeCodexPortal(record.State) {
			continue
		}
		portal, err := cc.UserLogin.Bridge.GetExistingPortalByKey(ctx, record.PortalKey)
		if err != nil || portal == nil {
			continue
		}
		out = append(out, portal)
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
	state := &codexPortalState{
		Title:            "New Codex Chat",
		Slug:             "codex-welcome",
		AwaitingCwdSetup: true,
	}
	portalMeta(portal).IsCodexRoom = true
	if err := sdk.ConfigureDMPortal(ctx, sdk.ConfigureDMPortalParams{
		Portal:      portal,
		Title:       state.Title,
		OtherUserID: codexGhostID,
		Save:        false,
	}); err != nil {
		return nil, err
	}
	info := cc.composeCodexChatInfo(portal, state, false)
	if err := saveCodexPortalState(ctx, portal, state); err != nil {
		return nil, err
	}
	created, err := sdk.EnsurePortalLifecycle(ctx, sdk.PortalLifecycleOptions{
		Login:             cc.UserLogin,
		Portal:            portal,
		ChatInfo:          info,
		SaveBeforeCreate:  true,
		AIRoomKind:        sdk.AIRoomKindAgent,
		ForceCapabilities: true,
	})
	if err != nil {
		return nil, err
	}
	if created {
		cc.sendSystemNotice(ctx, portal, "AI Chats can make mistakes.")
		cc.sendSystemNotice(ctx, portal, "Send an absolute path or `~/...` to start a Codex session.")
	}
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
	records, err := listCodexPortalStateRecords(ctx, cc.UserLogin)
	if err != nil {
		return nil, err
	}
	out := make([]*bridgev2.Portal, 0, len(records))
	for _, record := range records {
		if record.State == nil || !record.State.ManagedImport || strings.TrimSpace(record.State.CodexCwd) != path {
			continue
		}
		portal, err := cc.UserLogin.Bridge.GetExistingPortalByKey(ctx, record.PortalKey)
		if err != nil || portal == nil {
			continue
		}
		if meta := portalMeta(portal); meta == nil || !meta.IsCodexRoom {
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
		if state, err := loadCodexPortalState(ctx, portal); err == nil && state != nil {
			cc.cleanupImportedPortalState(state.CodexThreadID)
		}
		_ = clearCodexPortalState(ctx, portal)
		cc.deletePortalOnly(ctx, portal, "codex directory forgotten")
	}
	return len(portals), nil
}

func (cc *CodexClient) handleCodexCommand(ctx context.Context, portal *bridgev2.Portal, state *codexPortalState, body string) (*bridgev2.MatrixMessageResponse, bool, error) {
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
		path, err := cc.resolveManagedPathArgument(args, state)
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
		path, err := cc.resolveManagedPathArgument(args, state)
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

func (cc *CodexClient) handleWelcomeCodexMessage(ctx context.Context, portal *bridgev2.Portal, state *codexPortalState, body string) (*bridgev2.MatrixMessageResponse, error) {
	if cc == nil || cc.UserLogin == nil || portal == nil || state == nil {
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

	state.CodexCwd = path
	state.CodexThreadID = ""
	state.AwaitingCwdSetup = false
	state.ManagedImport = false
	state.Title = codexTitleForPath(path)
	state.Slug = strings.ToLower(strings.ReplaceAll(state.Title, " ", "-"))
	if err := saveCodexPortalState(ctx, portal, state); err != nil {
		return nil, messageSendStatusError(err, "Failed to save Codex room.", "")
	}
	if err := cc.ensureRPC(cc.backgroundContext(ctx)); err != nil {
		return nil, messageSendStatusError(err, "Codex isn't available. Sign in again.", "")
	}
	if err := cc.ensureCodexThread(ctx, portal, state); err != nil {
		return nil, messageSendStatusError(err, "Failed to start Codex thread.", "")
	}
	cc.syncCodexRoomTopic(ctx, portal, state)
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
