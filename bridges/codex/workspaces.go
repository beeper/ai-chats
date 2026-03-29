package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

const (
	codexPortalKindWelcome        = "welcome"
	codexPortalKindWorkspaceSpace = "workspace_space"
	codexPortalKindChat           = "chat"
)

func normalizeTrackedWorkspaceRoots(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if clean == "." {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return out
}

func NormalizeTrackedWorkspaceRootsCLI(paths []string) []string {
	return normalizeTrackedWorkspaceRoots(paths)
}

func workspaceContains(root, cwd string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if root == "." || cwd == "." || root == "" || cwd == "" {
		return false
	}
	if root == cwd {
		return true
	}
	if root == string(filepath.Separator) {
		return strings.HasPrefix(cwd, root)
	}
	return strings.HasPrefix(cwd, root+string(filepath.Separator))
}

func ResolveCodexWorkingDirectoryCLI(raw string) (string, error) {
	return resolveCodexWorkingDirectory(raw)
}

func longestMatchingWorkspaceRoot(roots []string, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	best := ""
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if !workspaceContains(root, cwd) {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	return best
}

func trackedWorkspaceRootsFromConfig(cfg *CodexConfig) []string {
	if cfg == nil {
		return nil
	}
	cfg.TrackedPaths = normalizeTrackedWorkspaceRoots(cfg.TrackedPaths)
	return slices.Clone(cfg.TrackedPaths)
}

func setTrackedWorkspaceRoots(meta *UserLoginMetadata, roots []string) {
	if meta == nil {
		return
	}
	meta.ManagedPaths = normalizeManagedCodexPaths(roots)
}

func (cc *CodexClient) trackedWorkspaceRoots() []string {
	return managedCodexPaths(loginMetadata(cc.UserLogin))
}

func (cc *CodexClient) workspaceRootForCwd(cwd string) string {
	return longestMatchingWorkspaceRoot(cc.trackedWorkspaceRoots(), cwd)
}

func (cc *CodexClient) allCodexPortals(ctx context.Context) ([]*bridgev2.Portal, error) {
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
		meta := portalMeta(portal)
		if meta == nil || !meta.IsCodexRoom {
			continue
		}
		out = append(out, portal)
	}
	return out, nil
}

func isWelcomeCodexPortal(meta *PortalMetadata) bool {
	return meta != nil && meta.IsCodexRoom && meta.PortalKind == codexPortalKindWelcome
}

func isWorkspaceSpacePortal(meta *PortalMetadata) bool {
	return meta != nil && meta.IsCodexRoom && meta.PortalKind == codexPortalKindWorkspaceSpace
}

func isCodexChatPortal(meta *PortalMetadata) bool {
	return meta != nil && meta.IsCodexRoom && meta.PortalKind == codexPortalKindChat
}

func (cc *CodexClient) composeWorkspaceSpaceInfo(root string) *bridgev2.ChatInfo {
	title := codexTitleForPath(root)
	if title == "" {
		title = root
	}
	info := agentremote.BuildLoginDMChatInfo(agentremote.LoginDMChatInfoParams{
		Title:             title,
		Login:             cc.UserLogin,
		HumanUserIDPrefix: cc.HumanUserIDPrefix,
		BotUserID:         codexGhostID,
		BotDisplayName:    "Codex",
		CanBackfill:       false,
	})
	if info != nil {
		info.Type = ptr.Ptr(database.RoomTypeSpace)
		info.Topic = ptr.Ptr(codexTopicForPath(root))
	}
	return info
}

func (cc *CodexClient) workspaceSpaceForRoot(ctx context.Context, root string) (*bridgev2.Portal, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return nil, fmt.Errorf("login unavailable")
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	portalKey, err := codexWorkspacePortalKey(cc.UserLogin.ID, root)
	if err != nil {
		return nil, err
	}
	return cc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
}

func (cc *CodexClient) ensureWorkspaceSpace(ctx context.Context, root string) (*bridgev2.Portal, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("workspace root is required")
	}
	portal, err := cc.workspaceSpaceForRoot(ctx, root)
	if err != nil {
		return nil, err
	}
	meta := portalMeta(portal)
	meta.IsCodexRoom = true
	meta.PortalKind = codexPortalKindWorkspaceSpace
	meta.WorkspaceRoot = root
	meta.CodexCwd = root
	meta.CodexThreadID = ""
	meta.ManagedImport = false
	meta.Title = codexTitleForPath(root)
	meta.Slug = codexThreadSlug(root)
	portal.RoomType = database.RoomTypeSpace
	portal.OtherUserID = codexGhostID
	portal.Name = meta.Title
	portal.NameSet = true
	portal.Topic = codexTopicForPath(root)
	portal.TopicSet = true
	_, err = bridgesdk.EnsurePortalLifecycle(ctx, bridgesdk.PortalLifecycleOptions{
		Login:             cc.UserLogin,
		Portal:            portal,
		ChatInfo:          cc.composeWorkspaceSpaceInfo(root),
		SaveBeforeCreate:  true,
		AIRoomKind:        agentremote.AIRoomKindAgent,
		ForceCapabilities: true,
	})
	if err != nil {
		return nil, err
	}
	return portal, portal.Save(ctx)
}

func (cc *CodexClient) attachChatToWorkspaceSpace(ctx context.Context, portal *bridgev2.Portal, root string) error {
	if portal == nil {
		return fmt.Errorf("portal unavailable")
	}
	meta := portalMeta(portal)
	if meta == nil || !isCodexChatPortal(meta) {
		return nil
	}
	root = strings.TrimSpace(root)
	meta.WorkspaceRoot = root
	if err := portal.Save(ctx); err != nil {
		return err
	}
	info := cc.composeCodexChatInfo(portal, codexPortalTitle(portal), true)
	if info != nil {
		if root != "" {
			space, err := cc.ensureWorkspaceSpace(ctx, root)
			if err != nil {
				return err
			}
			info.ParentID = ptr.Ptr(space.PortalKey.ID)
		} else {
			info.ParentID = nil
		}
	}
	bridgesdk.RefreshPortalLifecycle(ctx, bridgesdk.PortalLifecycleOptions{
		Login:             cc.UserLogin,
		Portal:            portal,
		ChatInfo:          info,
		AIRoomKind:        agentremote.AIRoomKindAgent,
		ForceCapabilities: true,
	})
	cc.syncCodexRoomTopic(ctx, portal, meta)
	return nil
}

func (cc *CodexClient) deleteWorkspaceSpace(ctx context.Context, root string) error {
	portal, err := cc.workspaceSpaceForRoot(ctx, root)
	if err != nil || portal == nil {
		return err
	}
	meta := portalMeta(portal)
	if meta == nil || !isWorkspaceSpacePortal(meta) {
		return nil
	}
	cc.deletePortalOnly(ctx, portal, "codex workspace removed")
	return nil
}

func resolveExistingDirectory(raw string) (string, error) {
	path, err := resolveCodexWorkingDirectory(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}
	return path, nil
}

func (cc *CodexClient) clearChatRuntimeState(meta *PortalMetadata, portal *bridgev2.Portal) {
	if cc == nil || meta == nil {
		return
	}
	cc.cleanupImportedPortalState(meta.CodexThreadID)
	if portal != nil && portal.MXID != "" {
		cc.roomMu.Lock()
		delete(cc.activeRooms, portal.MXID)
		delete(cc.pendingMessages, portal.MXID)
		cc.roomMu.Unlock()
	}
}

func (cc *CodexClient) createFreshCodexChat(ctx context.Context, cwd string) (*bridgev2.Portal, error) {
	if err := cc.ensureRPC(ctx); err != nil {
		return nil, err
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil, fmt.Errorf("working directory is required")
	}
	model := cc.connector.Config.Codex.DefaultModel
	var resp struct {
		Thread codexThread `json:"thread"`
		Model  string      `json:"model"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	err := cc.rpc.Call(callCtx, "thread/start", map[string]any{
		"model":                  model,
		"cwd":                    cwd,
		"approvalPolicy":         "untrusted",
		"sandbox":                cc.buildSandboxMode(),
		"experimentalRawEvents":  false,
		"persistExtendedHistory": true,
	}, &resp)
	if err != nil {
		return nil, err
	}
	resp.Thread.Cwd = cwd
	portal, _, err := cc.ensureFreshThreadPortal(ctx, resp.Thread)
	if err != nil {
		return nil, err
	}
	cc.restoreRecoveredActiveTurns(portal, portalMeta(portal), resp.Thread, resp.Model)
	return portal, nil
}

func (cc *CodexClient) ensureFreshThreadPortal(ctx context.Context, thread codexThread) (*bridgev2.Portal, bool, error) {
	threadID := strings.TrimSpace(thread.ID)
	if threadID == "" {
		return nil, false, fmt.Errorf("missing thread id")
	}
	portalKey, err := codexThreadPortalKey(cc.UserLogin.ID, threadID)
	if err != nil {
		return nil, false, err
	}
	portal, err := cc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, false, err
	}
	meta := portalMeta(portal)
	meta.IsCodexRoom = true
	meta.PortalKind = codexPortalKindChat
	meta.CodexThreadID = threadID
	meta.CodexCwd = strings.TrimSpace(thread.Cwd)
	meta.ManagedImport = false
	meta.WorkspaceRoot = cc.workspaceRootForCwd(meta.CodexCwd)
	title := codexThreadTitle(thread)
	if title == "" {
		title = codexTitleForPath(meta.CodexCwd)
	}
	meta.Title = title
	if meta.Slug == "" {
		meta.Slug = codexThreadSlug(threadID)
	}
	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = codexGhostID
	portal.Name = title
	portal.NameSet = true
	info := cc.composeCodexChatInfo(portal, title, true)
	if meta.WorkspaceRoot != "" {
		space, err := cc.ensureWorkspaceSpace(ctx, meta.WorkspaceRoot)
		if err != nil {
			return nil, false, err
		}
		info.ParentID = ptr.Ptr(space.PortalKey.ID)
	}
	created, err := bridgesdk.EnsurePortalLifecycle(ctx, bridgesdk.PortalLifecycleOptions{
		Login:             cc.UserLogin,
		Portal:            portal,
		ChatInfo:          info,
		SaveBeforeCreate:  true,
		AIRoomKind:        agentremote.AIRoomKindAgent,
		ForceCapabilities: true,
	})
	if err != nil {
		return nil, false, err
	}
	if err := portal.Save(ctx); err != nil {
		return nil, false, err
	}
	cc.loadedMu.Lock()
	cc.loadedThreads[threadID] = true
	cc.loadedMu.Unlock()
	cc.syncCodexRoomTopic(ctx, portal, meta)
	return portal, created, nil
}

func (cc *CodexClient) reconcileTrackedWorkspacesFromConfig(ctx context.Context) error {
	if cc == nil || cc.UserLogin == nil {
		return nil
	}
	meta := loginMetadata(cc.UserLogin)
	desired := trackedWorkspaceRootsFromConfig(cc.connector.Config.Codex)
	current := managedCodexPaths(meta)
	if isManagedAuthLogin(meta) {
		for _, root := range current {
			if !slices.Contains(desired, root) {
				if _, err := cc.untrackWorkspace(ctx, root, "startup"); err != nil {
					return err
				}
			}
		}
		setTrackedWorkspaceRoots(meta, desired)
		if err := cc.UserLogin.Save(ctx); err != nil {
			return err
		}
	} else {
		merged := slices.Clone(current)
		for _, root := range desired {
			if !slices.Contains(merged, root) {
				merged = append(merged, root)
			}
		}
		setTrackedWorkspaceRoots(meta, merged)
		if err := cc.UserLogin.Save(ctx); err != nil {
			return err
		}
	}
	for _, root := range managedCodexPaths(meta) {
		if err := cc.reconcileWorkspaceAdded(ctx, root); err != nil {
			return err
		}
	}
	return nil
}

func (cc *CodexClient) trackWorkspace(ctx context.Context, root, source string) (bool, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return false, fmt.Errorf("workspace root is required")
	}
	meta := loginMetadata(cc.UserLogin)
	if hasManagedCodexPath(meta, root) {
		return false, cc.reconcileWorkspaceAdded(ctx, root)
	}
	addManagedCodexPath(meta, root)
	if err := cc.UserLogin.Save(ctx); err != nil {
		return false, err
	}
	if cc.connector != nil && cc.connector.Config.Codex != nil && source != "startup" {
		cc.connector.Config.Codex.TrackedPaths = normalizeTrackedWorkspaceRoots(append(cc.connector.Config.Codex.TrackedPaths, root))
	}
	return true, cc.reconcileWorkspaceAdded(ctx, root)
}

func (cc *CodexClient) untrackWorkspace(ctx context.Context, root, source string) (bool, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return false, fmt.Errorf("workspace root is required")
	}
	meta := loginMetadata(cc.UserLogin)
	if !removeManagedCodexPath(meta, root) {
		return false, nil
	}
	if err := cc.UserLogin.Save(ctx); err != nil {
		return false, err
	}
	if cc.connector != nil && cc.connector.Config.Codex != nil && source != "startup" {
		next := make([]string, 0, len(cc.connector.Config.Codex.TrackedPaths))
		for _, existing := range cc.connector.Config.Codex.TrackedPaths {
			if filepath.Clean(strings.TrimSpace(existing)) == root {
				continue
			}
			next = append(next, existing)
		}
		cc.connector.Config.Codex.TrackedPaths = normalizeTrackedWorkspaceRoots(next)
	}
	return true, cc.reconcileWorkspaceRemoved(ctx, root)
}

func (cc *CodexClient) reconcileWorkspaceAdded(ctx context.Context, root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	if _, err := cc.ensureWorkspaceSpace(ctx, root); err != nil {
		return err
	}
	portals, err := cc.allCodexPortals(ctx)
	if err != nil {
		return err
	}
	for _, portal := range portals {
		meta := portalMeta(portal)
		if meta == nil || !isCodexChatPortal(meta) {
			continue
		}
		if !workspaceContains(root, meta.CodexCwd) {
			continue
		}
		meta.WorkspaceRoot = root
		if err := cc.attachChatToWorkspaceSpace(ctx, portal, root); err != nil {
			return err
		}
	}
	_, _, err = cc.syncStoredCodexThreadsForPath(ctx, root)
	return err
}

func (cc *CodexClient) reconcileWorkspaceRemoved(ctx context.Context, root string) error {
	portals, err := cc.allCodexPortals(ctx)
	if err != nil {
		return err
	}
	for _, portal := range portals {
		meta := portalMeta(portal)
		if meta == nil {
			continue
		}
		if isWorkspaceSpacePortal(meta) && filepath.Clean(strings.TrimSpace(meta.WorkspaceRoot)) == filepath.Clean(root) {
			continue
		}
		if !isCodexChatPortal(meta) || !workspaceContains(root, meta.CodexCwd) {
			continue
		}
		cc.clearChatRuntimeState(meta, portal)
		cc.deletePortalOnly(ctx, portal, "codex workspace removed")
	}
	return cc.deleteWorkspaceSpace(ctx, root)
}
