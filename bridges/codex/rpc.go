package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/bridges/codex/codexrpc"
)

// ensureRPC spawns the Codex app-server process and initializes the RPC client if needed.
func (cc *CodexClient) ensureRPC(ctx context.Context) error {
	cc.rpcMu.Lock()
	defer cc.rpcMu.Unlock()
	if cc.rpc != nil {
		return nil
	}

	// New app-server process => previously loaded thread ids are stale.
	cc.loadedMu.Lock()
	cc.loadedThreads = make(map[string]bool)
	cc.loadedMu.Unlock()

	meta := loginMetadata(cc.UserLogin)
	cmd := cc.resolveCodexCommand(meta)
	if _, err := exec.LookPath(cmd); err != nil {
		return fmt.Errorf("codex CLI not found (%q): %w", cmd, err)
	}

	codexHome := strings.TrimSpace(meta.CodexHome)
	var env []string
	if codexHome != "" {
		if err := os.MkdirAll(codexHome, 0o700); err != nil {
			return fmt.Errorf("creating CODEX_HOME: %w", err)
		}
		env = []string{"CODEX_HOME=" + codexHome}
	}

	launch, err := cc.connector.resolveAppServerLaunch()
	if err != nil {
		return fmt.Errorf("resolving app-server launch: %w", err)
	}

	rpc, err := codexrpc.StartProcess(ctx, codexrpc.ProcessConfig{
		Command:      cmd,
		Args:         launch.Args,
		Env:          env,
		WebSocketURL: launch.WebSocketURL,
	})
	if err != nil {
		return fmt.Errorf("starting codex process: %w", err)
	}
	cc.rpc = rpc

	initCtx, cancelInit := context.WithTimeout(ctx, 45*time.Second)
	defer cancelInit()
	ci := cc.connector.Config.Codex.ClientInfo
	if _, err = rpc.Initialize(initCtx, codexrpc.ClientInfo{Name: ci.Name, Title: ci.Title, Version: ci.Version}, false); err != nil {
		_ = rpc.Close()
		cc.rpc = nil
		return fmt.Errorf("codex initialize: %w", err)
	}

	cc.dispatchOnce.Do(func() {
		go cc.dispatchNotifications()
	})

	rpc.OnNotification(func(method string, params json.RawMessage) {
		if !cc.loggedIn.Load() {
			return
		}
		select {
		case cc.notifCh <- codexNotif{Method: method, Params: params}:
		default:
		}
	})

	rpc.HandleRequest("item/commandExecution/requestApproval", cc.handleCommandApprovalRequest)
	rpc.HandleRequest("item/fileChange/requestApproval", cc.handleFileChangeApprovalRequest)

	return nil
}

// subscribeTurn creates a notification channel for a specific thread+turn pair.
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

// codexExtractThreadTurn extracts threadId and turnId from raw notification params.
func codexExtractThreadTurn(params json.RawMessage) (threadID, turnID string, ok bool) {
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", "", false
	}
	threadID = strings.TrimSpace(p.ThreadID)
	turnID = strings.TrimSpace(p.TurnID)
	return threadID, turnID, threadID != "" && turnID != ""
}

// dispatchNotifications reads from notifCh and routes each notification to the
// correct per-turn subscription channel.
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

		// Track logged-in state from account updates.
		if evt.Method == "account/updated" {
			var p struct {
				AuthMode *string `json:"authMode"`
			}
			_ = json.Unmarshal(evt.Params, &p)
			cc.loggedIn.Store(p.AuthMode != nil && strings.TrimSpace(*p.AuthMode) != "")
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

		ch := cc.findTurnSub(threadID, turnID, evt.Method)
		if ch == nil {
			continue
		}
		cc.deliverToTurnSub(ch, evt)
	}
}

// findTurnSub looks up the subscription channel, with a brief spin-wait for terminal events.
func (cc *CodexClient) findTurnSub(threadID, turnID, method string) chan codexNotif {
	key := codexTurnKey(threadID, turnID)
	cc.subMu.Lock()
	ch := cc.turnSubs[key]
	cc.subMu.Unlock()

	if ch != nil {
		return ch
	}
	// Spin-wait briefly for terminal events that must not be dropped.
	if method != "turn/completed" && method != "error" {
		return nil
	}
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		cc.subMu.Lock()
		ch = cc.turnSubs[key]
		cc.subMu.Unlock()
		if ch != nil {
			return ch
		}
	}
	return nil
}

// deliverToTurnSub sends a notification to a turn subscription channel, blocking
// briefly for critical terminal events.
func (cc *CodexClient) deliverToTurnSub(ch chan codexNotif, evt codexNotif) {
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

func (cc *CodexClient) resolveCodexCommand(meta *UserLoginMetadata) string {
	if meta != nil {
		if v := strings.TrimSpace(meta.CodexCommand); v != "" {
			return v
		}
	}
	if cc.connector != nil && cc.connector.Config.Codex != nil {
		if v := strings.TrimSpace(cc.connector.Config.Codex.Command); v != "" {
			return v
		}
	}
	return "codex"
}

func (cc *CodexClient) codexNetworkAccess() bool {
	if cc.connector == nil || cc.connector.Config.Codex == nil || cc.connector.Config.Codex.NetworkAccess == nil {
		return true
	}
	return *cc.connector.Config.Codex.NetworkAccess
}
