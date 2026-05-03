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
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

func (cc *CodexClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta := portalMeta(portal)
	if meta == nil || !meta.IsCodexRoom {
		name := strings.TrimSpace(portal.Name)
		if name == "" {
			name = "Codex"
		}
		return &bridgev2.ChatInfo{
			Name:  ptr.Ptr(name),
			Topic: ptr.NonZero(strings.TrimSpace(portal.Topic)),
		}, nil
	}
	state, err := loadCodexPortalState(ctx, portal)
	if err != nil {
		return nil, err
	}
	return cc.composeCodexChatInfo(portal, state, strings.TrimSpace(state.CodexThreadID) != ""), nil
}

func (cc *CodexClient) GetUserInfo(_ context.Context, _ *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return codexSDKAgent().UserInfo(), nil
}

func (cc *CodexClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return nil, errors.New("login unavailable")
	}
	if !isCodexIdentifier(identifier) {
		return nil, fmt.Errorf("unknown identifier: %s", identifier)
	}

	ghost, err := cc.UserLogin.Bridge.GetGhostByID(ctx, codexGhostID)
	if err != nil {
		return nil, fmt.Errorf("failed to get Codex ghost: %w", err)
	}

	var chat *bridgev2.CreateChatResponse
	if createChat {
		portal, err := cc.createWelcomeCodexChat(ctx, false)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure Codex chat: %w", err)
		}
		if portal == nil {
			return nil, errors.New("codex chat unavailable")
		}
		state, err := loadCodexPortalState(ctx, portal)
		if err != nil {
			return nil, fmt.Errorf("failed to load Codex room state: %w", err)
		}
		chatInfo := cc.composeCodexChatInfo(portal, state, strings.TrimSpace(state.CodexThreadID) != "")
		chat = &bridgev2.CreateChatResponse{
			PortalKey:  portal.PortalKey,
			PortalInfo: chatInfo,
			Portal:     portal,
		}
	}

	return &bridgev2.ResolveIdentifierResponse{
		UserID:   codexGhostID,
		UserInfo: codexSDKAgent().UserInfo(),
		Ghost:    ghost,
		Chat:     chat,
	}, nil
}

func isCodexIdentifier(identifier string) bool {
	return strings.EqualFold(strings.TrimSpace(identifier), "codex")
}

func (cc *CodexClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	resp, err := cc.ResolveIdentifier(ctx, "codex", false)
	if err != nil {
		return nil, err
	}
	return []*bridgev2.ResolveIdentifierResponse{resp}, nil
}

func (cc *CodexClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return aiBaseCaps
}

func (cc *CodexClient) bootstrap(ctx context.Context) {
	syncSucceeded := true
	if err := cc.ensureWelcomeCodexChat(cc.backgroundContext(ctx)); err != nil {
		cc.log.Warn().Err(err).Msg("Failed to ensure default Codex chat during bootstrap")
		syncSucceeded = false
	}
	if err := cc.syncStoredCodexThreads(cc.backgroundContext(ctx)); err != nil {
		cc.log.Warn().Err(err).Msg("Failed to sync Codex threads during bootstrap")
		syncSucceeded = false
	}
	meta := loginMetadata(cc.UserLogin)
	meta.ChatsSynced = syncSucceeded
	_ = cc.UserLogin.Save(ctx)
}

func (cc *CodexClient) composeCodexChatInfo(portal *bridgev2.Portal, portalState *codexPortalState, canBackfill bool) *bridgev2.ChatInfo {
	title := "Codex"
	topic := ""
	if portalState != nil {
		if v := strings.TrimSpace(portalState.Title); v != "" {
			title = v
		}
		topic = cc.codexTopicForPortal(portal, portalState)
	}
	return bridgeutil.BuildDMChatInfo(bridgeutil.DMChatInfoParams{
		Title:          title,
		Topic:          topic,
		HumanUserID:    humanUserID(cc.UserLogin.ID),
		LoginID:        cc.UserLogin.ID,
		BotUserID:      codexGhostID,
		BotDisplayName: "Codex",
		CanBackfill:    canBackfill,
	})
}

func (cc *CodexClient) buildSandboxPolicy(cwd string) map[string]any {
	return map[string]any{
		"type":                "workspaceWrite",
		"writableRoots":       []string{cwd},
		"networkAccess":       cc.codexNetworkAccess(),
		"excludeTmpdirEnvVar": false,
		"excludeSlashTmp":     false,
	}
}

func newRecoveredStreamingState(turnID, model string) *streamingState {
	return &streamingState{
		turnID:                 strings.TrimSpace(turnID),
		currentModel:           strings.TrimSpace(model),
		startedAtMs:            time.Now().UnixMilli(),
		firstToken:             true,
		codexTimelineNotices:   make(map[string]bool),
		codexToolOutputBuffers: make(map[string]*strings.Builder),
	}
}

func (cc *CodexClient) restoreRecoveredActiveTurns(portal *bridgev2.Portal, portalState *codexPortalState, thread codexThread, model string) {
	if cc == nil || portal == nil || portalState == nil {
		return
	}
	threadID := strings.TrimSpace(thread.ID)
	if threadID == "" {
		return
	}
	cc.activeMu.Lock()
	defer cc.activeMu.Unlock()
	for _, turn := range thread.Turns {
		if !strings.EqualFold(strings.TrimSpace(turn.Status), "inProgress") {
			continue
		}
		turnID := strings.TrimSpace(turn.ID)
		if turnID == "" {
			continue
		}
		key := codexTurnKey(threadID, turnID)
		if _, exists := cc.activeTurns[key]; exists {
			continue
		}
		cc.activeTurns[key] = &codexActiveTurn{
			portal:      portal,
			portalState: portalState,
			streamState: newRecoveredStreamingState(turnID, model),
			threadID:    threadID,
			turnID:      turnID,
			model:       strings.TrimSpace(model),
		}
	}
}

func (cc *CodexClient) ensureCodexThread(ctx context.Context, portal *bridgev2.Portal, portalState *codexPortalState) error {
	if portalState == nil || portal == nil {
		return errors.New("missing portal/meta")
	}
	if strings.TrimSpace(portalState.CodexCwd) == "" {
		return errors.New("codex working directory not set")
	}
	if _, err := os.Stat(portalState.CodexCwd); err != nil {
		return fmt.Errorf("working directory %s no longer exists", portalState.CodexCwd)
	}
	if strings.TrimSpace(portalState.CodexThreadID) != "" {
		return cc.ensureCodexThreadLoaded(ctx, portal, portalState)
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return err
	}
	model := cc.connector.Config.Codex.DefaultModel
	var resp struct {
		Thread codexThread `json:"thread"`
		Model  string      `json:"model"`
	}
	callCtx, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	defer cancelCall()
	err := cc.rpc.Call(callCtx, "thread/start", map[string]any{
		"model":                  model,
		"cwd":                    portalState.CodexCwd,
		"approvalPolicy":         "untrusted",
		"sandbox":                "workspace-write",
		"experimentalRawEvents":  false,
		"persistExtendedHistory": true,
	}, &resp)
	if err != nil {
		return err
	}
	portalState.CodexThreadID = strings.TrimSpace(resp.Thread.ID)
	if portalState.CodexThreadID == "" {
		return errors.New("codex returned empty thread id")
	}
	if err := saveCodexPortalState(ctx, portal, portalState); err != nil {
		return err
	}
	cc.finishCodexThreadLoad(ctx, portal, portalState, resp.Thread, resp.Model)
	return nil
}

func (cc *CodexClient) ensureCodexThreadLoaded(ctx context.Context, portal *bridgev2.Portal, portalState *codexPortalState) error {
	if portalState == nil {
		return errors.New("missing metadata")
	}
	threadID := strings.TrimSpace(portalState.CodexThreadID)
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
		return err
	}
	var resp struct {
		Thread codexThread `json:"thread"`
		Model  string      `json:"model"`
	}
	callCtx, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	defer cancelCall()
	err := cc.rpc.Call(callCtx, "thread/resume", map[string]any{
		"threadId":               threadID,
		"model":                  cc.connector.Config.Codex.DefaultModel,
		"cwd":                    portalState.CodexCwd,
		"approvalPolicy":         "untrusted",
		"sandbox":                "workspace-write",
		"persistExtendedHistory": true,
	}, &resp)
	if err != nil {
		return err
	}
	cc.finishCodexThreadLoad(ctx, portal, portalState, resp.Thread, resp.Model)
	return nil
}

func (cc *CodexClient) finishCodexThreadLoad(
	ctx context.Context,
	portal *bridgev2.Portal,
	portalState *codexPortalState,
	thread codexThread,
	model string,
) {
	if cc == nil || portalState == nil {
		return
	}
	threadID := strings.TrimSpace(portalState.CodexThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(thread.ID)
	}
	if threadID != "" {
		cc.loadedMu.Lock()
		cc.loadedThreads[threadID] = true
		cc.loadedMu.Unlock()
	}
	cc.restoreRecoveredActiveTurns(portal, portalState, thread, model)
	if portal != nil && portal.MXID != "" {
		if info := cc.composeCodexChatInfo(portal, portalState, threadID != ""); info != nil {
			portal.UpdateInfo(ctx, info, cc.UserLogin, nil, time.Time{})
		}
	}
}

// HandleMatrixDeleteChat best-effort archives the Codex thread and removes the temp cwd.
// The core bridge handles Matrix-side room cleanup separately.
