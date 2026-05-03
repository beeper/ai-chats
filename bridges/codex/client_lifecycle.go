package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	"github.com/beeper/agentremote/sdk"
)

func (cc *CodexClient) Connect(ctx context.Context) {
	cc.SetLoggedIn(false)
	if err := cc.ensureRPC(cc.backgroundContext(ctx)); err != nil {
		cc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateTransientDisconnect,
			Error:      AIAuthFailed,
			Message:    fmt.Sprintf("Codex isn't available: %v", err),
		})
		return
	}

	// Best-effort account/read.
	readCtx, cancel := context.WithTimeout(cc.backgroundContext(ctx), 10*time.Second)
	defer cancel()
	var resp struct {
		Account *struct {
			Type  string `json:"type"`
			Email string `json:"email"`
		} `json:"account"`
		RequiresOpenaiAuth bool `json:"requiresOpenaiAuth"`
	}
	_ = cc.rpc.Call(readCtx, "account/read", map[string]any{"refreshToken": false}, &resp)
	if resp.Account != nil {
		cc.SetLoggedIn(true)
		meta := loginMetadata(cc.UserLogin)
		if strings.TrimSpace(resp.Account.Email) != "" {
			meta.CodexAccountEmail = strings.TrimSpace(resp.Account.Email)
			_ = cc.UserLogin.Save(cc.backgroundContext(ctx))
		}
	}
	if resp.Account == nil {
		state := status.StateBadCredentials
		message := "Codex login is no longer authenticated."
		if isHostAuthLogin(loginMetadata(cc.UserLogin)) {
			state = status.StateTransientDisconnect
			message = "Codex host authentication is unavailable."
		}
		cc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: state,
			Error:      AIAuthFailed,
			Message:    message,
		})
		return
	}

	cc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
		Message:    "Connected",
	})
}

func (cc *CodexClient) Disconnect() {
	cc.SetLoggedIn(false)
	if cc.approvalFlow != nil {
		cc.approvalFlow.Close()
	}

	// Signal dispatchNotifications goroutine to stop.
	if cc.notifDone != nil {
		select {
		case <-cc.notifDone:
			// Already closed.
		default:
			close(cc.notifDone)
		}
	}

	cc.rpcMu.Lock()
	if cc.rpc != nil {
		_ = cc.rpc.Close()
		cc.rpc = nil
	}
	cc.rpcMu.Unlock()

	cc.loadedMu.Lock()
	cc.loadedThreads = make(map[string]bool)
	cc.loadedMu.Unlock()

	cc.activeMu.Lock()
	cc.activeTurns = make(map[string]*codexActiveTurn)
	cc.activeMu.Unlock()

	cc.subMu.Lock()
	cc.turnSubs = make(map[string]chan codexNotif)
	cc.subMu.Unlock()

	cc.roomMu.Lock()
	cc.activeRooms = make(map[id.RoomID]bool)
	cc.roomMu.Unlock()
}

func (cc *CodexClient) GetUserLogin() *bridgev2.UserLogin { return cc.UserLogin }

func (cc *CodexClient) GetApprovalHandler() sdk.ApprovalReactionHandler {
	return cc.approvalFlow
}

func (cc *CodexClient) senderForPortal() bridgev2.EventSender {
	if cc == nil || cc.UserLogin == nil {
		return bridgev2.EventSender{Sender: codexGhostID}
	}
	return bridgev2.EventSender{Sender: codexGhostID, SenderLogin: cc.UserLogin.ID}
}

func (cc *CodexClient) senderForHuman() bridgev2.EventSender {
	if cc == nil || cc.UserLogin == nil {
		return bridgev2.EventSender{IsFromMe: true}
	}
	return bridgev2.EventSender{Sender: cc.HumanUserID(), SenderLogin: cc.UserLogin.ID, IsFromMe: true}
}

func (cc *CodexClient) LogoutRemote(ctx context.Context) {
	meta := loginMetadata(cc.UserLogin)
	// Only managed per-login auth should trigger upstream account/logout.
	if !isHostAuthLogin(meta) {
		if err := cc.ensureRPC(cc.backgroundContext(ctx)); err == nil && cc.rpc != nil {
			callCtx, cancel := context.WithTimeout(cc.backgroundContext(ctx), 10*time.Second)
			defer cancel()
			var out map[string]any
			_ = cc.rpc.Call(callCtx, "account/logout", nil, &out)
		}
	}
	// Best-effort: remove on-disk Codex state for this login.
	cc.purgeCodexHomeBestEffort(ctx)
	// Best-effort: remove on-disk per-room Codex working dirs.
	cc.purgeCodexCwdsBestEffort(ctx)

	cc.Disconnect()

	if cc.connector != nil {
		cc.connector.clientsMu.Lock()
		delete(cc.connector.clients, cc.UserLogin.ID)
		cc.connector.clientsMu.Unlock()
	}

	cc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateLoggedOut,
		Message:    "Disconnected by user",
	})
}

func (cc *CodexClient) purgeCodexHomeBestEffort(_ context.Context) {
	if cc.UserLogin == nil {
		return
	}
	meta, ok := cc.UserLogin.Metadata.(*UserLoginMetadata)
	if !ok || meta == nil {
		return
	}
	// Don't delete unmanaged homes (e.g. the user's own ~/.codex).
	if !isManagedAuthLogin(meta) {
		return
	}
	codexHome := strings.TrimSpace(meta.CodexHome)
	if codexHome == "" {
		return
	}
	// Safety: refuse to delete suspicious paths.
	clean := filepath.Clean(codexHome)
	if clean == string(os.PathSeparator) || clean == "." {
		return
	}
	// Best-effort recursive delete.
	_ = os.RemoveAll(clean)
}

func (cc *CodexClient) purgeCodexCwdsBestEffort(ctx context.Context) {
	if cc.UserLogin == nil || cc.UserLogin.Bridge == nil || cc.UserLogin.Bridge.DB == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	records, err := listCodexPortalStateRecords(ctx, cc.UserLogin)
	if err != nil || len(records) == 0 {
		return
	}

	tmp := filepath.Clean(os.TempDir())
	if tmp == "" || tmp == "." || tmp == string(os.PathSeparator) {
		// Should never happen, but avoid deleting arbitrary dirs if it does.
		return
	}

	seen := make(map[string]struct{})
	for _, record := range records {
		if record.State == nil {
			continue
		}
		cwd := strings.TrimSpace(record.State.CodexCwd)
		if cwd == "" {
			continue
		}
		clean := filepath.Clean(cwd)
		if clean == "." || clean == string(os.PathSeparator) {
			continue
		}
		// Safety: only delete dirs we created in agentremote-codex temp roots.
		if !isManagedCodexTempDirPath(clean) {
			continue
		}
		if !strings.HasPrefix(clean, tmp+string(os.PathSeparator)) {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		_ = os.RemoveAll(clean)
	}
}

func isManagedCodexTempDirPath(path string) bool {
	for _, segment := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if segment == "agentremote-codex" || strings.HasPrefix(segment, "agentremote-codex-") {
			return true
		}
	}
	return false
}

func (cc *CodexClient) ensureRPC(ctx context.Context) error {
	cc.rpcMu.Lock()
	defer cc.rpcMu.Unlock()
	if cc.rpc != nil {
		return nil
	}

	// New app-server process => previously loaded thread ids are no longer in memory.
	cc.loadedMu.Lock()
	cc.loadedThreads = make(map[string]bool)
	cc.loadedMu.Unlock()

	meta := loginMetadata(cc.UserLogin)
	cmd := cc.resolveCodexCommand(meta)
	if _, err := exec.LookPath(cmd); err != nil {
		return err
	}
	codexHome := strings.TrimSpace(meta.CodexHome)
	var env []string
	if codexHome != "" {
		if err := os.MkdirAll(codexHome, 0o700); err != nil {
			return err
		}
		env = []string{"CODEX_HOME=" + codexHome}
	}
	launch, err := cc.connector.resolveAppServerLaunch()
	if err != nil {
		return err
	}
	rpc, err := codexrpc.StartProcess(ctx, codexrpc.ProcessConfig{
		Command:      cmd,
		Args:         launch.Args,
		Env:          env,
		WebSocketURL: launch.WebSocketURL,
	})
	if err != nil {
		return err
	}
	cc.rpc = rpc

	initCtx, cancelInit := context.WithTimeout(ctx, 45*time.Second)
	defer cancelInit()
	ci := cc.connector.Config.Codex.ClientInfo
	_, err = rpc.InitializeWithOptions(initCtx, codexrpc.ClientInfo{Name: ci.Name, Title: ci.Title, Version: ci.Version}, codexrpc.InitializeOptions{
		// Thread recovery uses persistExtendedHistory, which currently requires
		// the experimental API capability during initialize.
		ExperimentalAPI: true,
	})
	if err != nil {
		_ = rpc.Close()
		cc.rpc = nil
		return err
	}

	cc.startDispatching()

	rpc.OnNotification(func(method string, params json.RawMessage) {
		if !cc.IsLoggedIn() {
			return
		}
		select {
		case cc.notifCh <- codexNotif{Method: method, Params: params}:
		default:
		}
	})

	// Approval requests.
	rpc.HandleRequest("item/commandExecution/requestApproval", cc.handleCommandApprovalRequest)
	rpc.HandleRequest("item/fileChange/requestApproval", cc.handleFileChangeApprovalRequest)
	rpc.HandleRequest("item/permissions/requestApproval", cc.handlePermissionsApprovalRequest)

	return nil
}

func (cc *CodexClient) subscribeTurn(threadID, turnID string) chan codexNotif {
	key := codexTurnKey(threadID, turnID)
	ch := make(chan codexNotif, 4096)
	cc.subMu.Lock()
	cc.turnSubs[key] = ch
	cc.subMu.Unlock()
	return ch
}

func (cc *CodexClient) unsubscribeTurn(threadID, turnID string) {
	key := codexTurnKey(threadID, turnID)
	cc.subMu.Lock()
	delete(cc.turnSubs, key)
	cc.subMu.Unlock()
}

func codexExtractThreadTurn(params json.RawMessage) (threadID, turnID string, ok bool) {
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     *struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", "", false
	}
	threadID = strings.TrimSpace(p.ThreadID)
	turnID = strings.TrimSpace(p.TurnID)
	return threadID, turnID, threadID != "" && turnID != ""
}

func (cc *CodexClient) dispatchNotifications() {
	for {
		var evt codexNotif
		select {
		case <-cc.notifDone:
			return
		case e, ok := <-cc.notifCh:
			if !ok {
				return
			}
			evt = e
		}
		// Track logged-in state if Codex emits these (best-effort).
		if evt.Method == "account/updated" {
			var p struct {
				AuthMode *string `json:"authMode"`
			}
			_ = json.Unmarshal(evt.Params, &p)
			cc.SetLoggedIn(p.AuthMode != nil && strings.TrimSpace(*p.AuthMode) != "")
			continue
		}

		threadID, turnID, ok := codexExtractThreadTurn(evt.Params)
		if !ok {
			continue
		}
		if evt.Method == "turn/completed" || evt.Method == "error" {
			cc.log.Debug().Str("method", evt.Method).Str("thread", threadID).Str("turn", turnID).
				Msg("Codex terminal notification")
		}
		key := codexTurnKey(threadID, turnID)
		if evt.Method == "turn/completed" {
			cc.activeMu.Lock()
			if active := cc.activeTurns[key]; active != nil && (active.streamState == nil || active.streamState.turn == nil) {
				delete(cc.activeTurns, key)
			}
			cc.activeMu.Unlock()
		}

		cc.subMu.Lock()
		ch := cc.turnSubs[key]
		cc.subMu.Unlock()
		if ch == nil {
			// Race: turn/start just returned but subscribeTurn() hasn't registered yet.
			// Spin-wait briefly for terminal events that must not be dropped.
			if evt.Method == "turn/completed" || evt.Method == "error" {
				for i := 0; i < 20; i++ {
					time.Sleep(50 * time.Millisecond)
					cc.subMu.Lock()
					ch = cc.turnSubs[key]
					cc.subMu.Unlock()
					if ch != nil {
						break
					}
				}
			}
			if ch == nil {
				continue
			}
		}

		// Try non-blocking, but ensure critical terminal events are delivered.
		select {
		case ch <- evt:
		default:
			if evt.Method == "turn/completed" || evt.Method == "error" {
				select {
				case ch <- evt:
				case <-time.After(2 * time.Second):
				}
			}
		}
	}
}

func (cc *CodexClient) resolveCodexCommand(meta *UserLoginMetadata) string {
	if meta != nil {
		if v := strings.TrimSpace(meta.CodexCommand); v != "" {
			return v
		}
	}
	if cc.connector == nil {
		return "codex"
	}
	return resolveCodexCommandFromConfig(cc.connector.Config.Codex)
}

func (cc *CodexClient) codexNetworkAccess() bool {
	if cc.connector == nil || cc.connector.Config.Codex == nil || cc.connector.Config.Codex.NetworkAccess == nil {
		return true
	}
	return *cc.connector.Config.Codex.NetworkAccess
}

func (cc *CodexClient) backgroundContext(ctx context.Context) context.Context {
	base := context.Background()
	if cc.UserLogin != nil && cc.UserLogin.Bridge != nil && cc.UserLogin.Bridge.BackgroundCtx != nil {
		base = cc.UserLogin.Bridge.BackgroundCtx
	}
	return cc.loggerForContext(ctx).WithContext(base)
}
