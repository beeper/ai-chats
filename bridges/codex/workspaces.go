package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

const (
	codexPortalKindWelcome       = "welcome"
	codexPortalKindWorkspaceSpace = "workspace_space"
	codexPortalKindChat          = "chat"
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
