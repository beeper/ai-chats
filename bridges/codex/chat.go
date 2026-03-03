package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
)

func (cc *CodexClient) scheduleBootstrap() {
	cc.bootstrapOnce.Do(func() {
		go cc.bootstrap(cc.UserLogin.Bridge.BackgroundCtx)
	})
}

func (cc *CodexClient) bootstrap(ctx context.Context) {
	cc.waitForLoginPersisted(ctx)
	meta := loginMetadata(cc.UserLogin)
	if meta.ChatsSynced {
		return
	}
	if err := cc.ensureDefaultCodexChat(cc.backgroundContext(ctx)); err != nil {
		cc.log.Warn().Err(err).Msg("Failed to ensure default Codex chat during bootstrap")
	}
	meta.ChatsSynced = true
	_ = cc.UserLogin.Save(ctx)
}

func (cc *CodexClient) waitForLoginPersisted(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(60 * time.Second)
	for {
		if _, err := cc.UserLogin.Bridge.DB.UserLogin.GetByID(ctx, cc.UserLogin.ID); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			cc.log.Warn().Msg("Timed out waiting for login to persist, continuing anyway")
			return
		case <-ticker.C:
		}
	}
}

func (cc *CodexClient) ensureDefaultCodexChat(ctx context.Context) error {
	portalKey := defaultCodexChatPortalKey(cc.UserLogin.ID)
	portal, err := cc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return fmt.Errorf("getting portal: %w", err)
	}
	if portal.Metadata == nil {
		portal.Metadata = &PortalMetadata{}
	}
	meta := portalMeta(portal)
	meta.IsCodexRoom = true
	if meta.Title == "" {
		meta.Title = "Codex"
	}
	if meta.Slug == "" {
		meta.Slug = "codex"
	}
	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = codexGhostID
	portal.Name = meta.Title
	portal.NameSet = true
	if err := portal.Save(ctx); err != nil {
		return fmt.Errorf("saving portal: %w", err)
	}

	if portal.MXID == "" {
		info := cc.composeCodexChatInfo(meta.Title)
		if err := portal.CreateMatrixRoom(ctx, cc.UserLogin, info); err != nil {
			return fmt.Errorf("creating matrix room: %w", err)
		}
		cc.sendSystemNotice(ctx, portal, "AI Chats can make mistakes.")
		cc.sendSystemNotice(ctx, portal, "What directory should Codex work in? Send an absolute path.")
		meta.AwaitingCwdSetup = true
		return portal.Save(ctx)
	}

	// Ensure thread started if directory is already set.
	if strings.TrimSpace(meta.CodexCwd) != "" {
		return cc.ensureCodexThread(ctx, portal, meta)
	}
	return nil
}

func (cc *CodexClient) composeCodexChatInfo(title string) *bridgev2.ChatInfo {
	if title == "" {
		title = "Codex"
	}
	members := bridgev2.ChatMemberMap{
		humanUserID(cc.UserLogin.ID): {
			EventSender: bridgev2.EventSender{
				IsFromMe:    true,
				SenderLogin: cc.UserLogin.ID,
			},
			Membership: event.MembershipJoin,
		},
		codexGhostID: {
			EventSender: bridgev2.EventSender{
				Sender:      codexGhostID,
				SenderLogin: cc.UserLogin.ID,
			},
			Membership: event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:  ptr.Ptr("Codex"),
				IsBot: ptr.Ptr(true),
			},
			MemberEventExtra: map[string]any{
				"displayname": "Codex",
			},
		},
	}
	return &bridgev2.ChatInfo{
		Name: ptr.Ptr(title),
		Type: ptr.Ptr(database.RoomTypeDM),
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: codexGhostID,
			MemberMap:   members,
			PowerLevels: &bridgev2.PowerLevelOverrides{
				Events: map[event.Type]int{
					matrixevents.RoomCapabilitiesEventType: 100,
					matrixevents.RoomSettingsEventType:     0,
				},
			},
		},
	}
}

func (cc *CodexClient) buildSandboxPolicy(cwd string) map[string]any {
	return map[string]any{
		"type":          "workspaceWrite",
		"writableRoots": []string{cwd},
		"networkAccess": cc.codexNetworkAccess(),
	}
}

// ensureCodexThread creates a new Codex thread for the portal if none exists.
func (cc *CodexClient) ensureCodexThread(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) error {
	if meta == nil || portal == nil {
		return errors.New("missing portal/meta")
	}
	if strings.TrimSpace(meta.CodexCwd) == "" {
		return errors.New("codex working directory not set")
	}
	if _, err := os.Stat(meta.CodexCwd); err != nil {
		return fmt.Errorf("working directory %s no longer exists: %w", meta.CodexCwd, err)
	}
	if err := portal.Save(ctx); err != nil {
		return fmt.Errorf("saving portal: %w", err)
	}
	if strings.TrimSpace(meta.CodexThreadID) != "" {
		return cc.ensureCodexThreadLoaded(ctx, portal, meta)
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return fmt.Errorf("ensuring RPC: %w", err)
	}
	model := cc.connector.Config.Codex.DefaultModel
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	callCtx, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	defer cancelCall()
	err := cc.rpc.Call(callCtx, "thread/start", map[string]any{
		"model":          model,
		"cwd":            meta.CodexCwd,
		"approvalPolicy": "untrusted",
		"sandboxPolicy":  cc.buildSandboxPolicy(meta.CodexCwd),
	}, &resp)
	if err != nil {
		return fmt.Errorf("thread/start: %w", err)
	}
	meta.CodexThreadID = strings.TrimSpace(resp.Thread.ID)
	if meta.CodexThreadID == "" {
		return errors.New("codex returned empty thread id")
	}
	if err := portal.Save(ctx); err != nil {
		return fmt.Errorf("saving thread id: %w", err)
	}
	cc.loadedMu.Lock()
	cc.loadedThreads[meta.CodexThreadID] = true
	cc.loadedMu.Unlock()
	return nil
}

// ensureCodexThreadLoaded resumes a thread that was previously started.
func (cc *CodexClient) ensureCodexThreadLoaded(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) error {
	if meta == nil {
		return errors.New("missing metadata")
	}
	threadID := strings.TrimSpace(meta.CodexThreadID)
	if threadID == "" {
		return errors.New("missing thread id")
	}
	cc.loadedMu.Lock()
	loaded := cc.loadedThreads[threadID]
	cc.loadedMu.Unlock()
	if loaded {
		return nil
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return fmt.Errorf("ensuring RPC: %w", err)
	}
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	callCtx, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	defer cancelCall()
	err := cc.rpc.Call(callCtx, "thread/resume", map[string]any{
		"threadId":       threadID,
		"model":          cc.connector.Config.Codex.DefaultModel,
		"cwd":            meta.CodexCwd,
		"approvalPolicy": "untrusted",
		"sandboxPolicy":  cc.buildSandboxPolicy(meta.CodexCwd),
	}, &resp)
	if err != nil {
		// If the stored thread can't be resumed, fall back to a fresh thread.
		meta.CodexThreadID = ""
		if err2 := portal.Save(ctx); err2 != nil {
			return fmt.Errorf("saving cleared thread: %w", err2)
		}
		return cc.ensureCodexThread(ctx, portal, meta)
	}
	cc.loadedMu.Lock()
	cc.loadedThreads[threadID] = true
	cc.loadedMu.Unlock()
	return nil
}

// HandleMatrixDeleteChat archives the Codex thread and removes temp working directories.
func (cc *CodexClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if msg == nil || msg.Portal == nil {
		return nil
	}
	meta := portalMeta(msg.Portal)
	if meta == nil || !meta.IsCodexRoom {
		return nil
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return nil
	}

	// If a turn is in-flight for this thread, try to interrupt it.
	tid := strings.TrimSpace(meta.CodexThreadID)
	cc.activeMu.Lock()
	var active *codexActiveTurn
	for _, at := range cc.activeTurns {
		if at != nil && strings.TrimSpace(at.threadID) == tid {
			active = at
			break
		}
	}
	cc.activeMu.Unlock()
	if active != nil && strings.TrimSpace(active.threadID) == tid {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = cc.rpc.Call(callCtx, "turn/interrupt", map[string]any{
			"threadId": active.threadID,
			"turnId":   active.turnID,
		}, &struct{}{})
		cancel()
	}

	if tid != "" {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = cc.rpc.Call(callCtx, "thread/archive", map[string]any{"threadId": tid}, &struct{}{})
		cancel()
		cc.loadedMu.Lock()
		delete(cc.loadedThreads, tid)
		cc.loadedMu.Unlock()
	}
	if cwd := strings.TrimSpace(meta.CodexCwd); cwd != "" {
		_ = os.RemoveAll(cwd)
	}
	meta.CodexThreadID = ""
	meta.CodexCwd = ""
	_ = msg.Portal.Save(ctx)
	return nil
}
