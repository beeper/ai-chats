package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

func (cc *CodexClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if msg == nil || msg.Content == nil || msg.Portal == nil || msg.Event == nil {
		return nil, errors.New("invalid message")
	}
	portal := msg.Portal
	meta := portalMeta(portal)
	if meta == nil || !meta.IsCodexRoom {
		return nil, sdk.UnsupportedMessageStatus(errors.New("not a Codex room"))
	}
	state, err := loadCodexPortalState(ctx, portal)
	if err != nil {
		return nil, err
	}
	if sdk.IsMatrixBotUser(ctx, cc.UserLogin.Bridge, msg.Event.Sender) {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	// Only text messages.
	switch msg.Content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
	default:
		return nil, sdk.UnsupportedMessageStatus(fmt.Errorf("%s messages are not supported", msg.Content.MsgType))
	}
	if msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplaceID() != "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	body := strings.TrimSpace(msg.Content.Body)
	if body == "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	if res, handled, err := cc.handleCodexCommand(ctx, portal, state, body); handled {
		return res, err
	}

	if state.AwaitingCwdSetup {
		return cc.handleWelcomeCodexMessage(ctx, portal, state, body)
	}

	if err := cc.ensureRPC(cc.backgroundContext(ctx)); err != nil {
		return nil, sdk.MessageSendStatusError(err, "Codex isn't available. Sign in again.", "", messageStatusForError, messageStatusReasonForError)
	}
	if strings.TrimSpace(state.CodexThreadID) == "" || strings.TrimSpace(state.CodexCwd) == "" {
		if err := cc.ensureCodexThread(ctx, portal, state); err != nil {
			return nil, sdk.MessageSendStatusError(err, "Codex thread unavailable. Try !codex reset.", "", messageStatusForError, messageStatusReasonForError)
		}
	}
	if err := cc.ensureCodexThreadLoaded(ctx, portal, state); err != nil {
		return nil, sdk.MessageSendStatusError(err, "Codex thread unavailable. Try !codex reset.", "", messageStatusForError, messageStatusReasonForError)
	}

	roomID := portal.MXID
	if roomID == "" {
		return nil, errors.New("portal has no room id")
	}

	userMsg := &database.Message{
		ID:        sdk.MatrixMessageID(msg.Event.ID),
		MXID:      msg.Event.ID,
		Room:      portal.PortalKey,
		SenderID:  humanUserID(cc.UserLogin.ID),
		Timestamp: sdk.MatrixEventTimestamp(msg.Event),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: body},
		},
	}
	if msg.InputTransactionID != "" {
		userMsg.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
	}
	if _, err := cc.UserLogin.Bridge.GetGhostByID(ctx, userMsg.SenderID); err != nil {
		cc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving message")
	}

	if !cc.acquireRoomIfIdle(roomID) {
		err := fmt.Errorf("a Codex turn is already running in this room")
		return nil, bridgev2.WrapErrorInStatus(err).
			WithStatus(event.MessageStatusRetriable).
			WithErrorReason(event.MessageStatusGenericError).
			WithMessage("A Codex turn is already running. Try again when it finishes.").
			WithIsCertain(true).
			WithSendNotice(false)
	}

	go func() {
		defer cc.releaseRoom(roomID)
		cc.runTurn(cc.backgroundContext(ctx), portal, state, msg.Event, body)
	}()

	return &bridgev2.MatrixMessageResponse{
		DB:      userMsg,
		Pending: true,
	}, nil
}

func (cc *CodexClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if msg == nil || msg.Portal == nil {
		return nil
	}
	meta := portalMeta(msg.Portal)
	if meta == nil || !meta.IsCodexRoom {
		return nil
	}
	state, err := loadCodexPortalState(ctx, msg.Portal)
	if err != nil {
		return err
	}
	if state.AwaitingCwdSetup {
		go func() {
			time.Sleep(1 * time.Second)
			_ = cc.ensureWelcomeCodexChat(cc.backgroundContext(ctx))
		}()
		return nil
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return nil
	}

	// If a turn is in-flight for this thread, try to interrupt it.
	tid := strings.TrimSpace(state.CodexThreadID)
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
	if cwd := strings.TrimSpace(state.CodexCwd); cwd != "" {
		_ = os.RemoveAll(cwd)
	}
	state.CodexThreadID = ""
	state.CodexCwd = ""
	_ = saveCodexPortalState(ctx, msg.Portal, state)
	return nil
}

func (cc *CodexClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, message string) {
	if cc == nil || portal == nil || strings.TrimSpace(message) == "" {
		return
	}
	send := func(sendCtx context.Context) error {
		return sdk.SendSystemMessage(sendCtx, cc.UserLogin, portal, cc.senderForPortal(), message)
	}
	if portal.MXID == "" {
		go func() {
			retryCtx := cc.backgroundContext(ctx)
			for attempt := 0; attempt < 3; attempt++ {
				if portal.MXID != "" {
					if err := send(retryCtx); err != nil {
						cc.log.Warn().Err(err).Msg("Failed to send system notice")
					}
					return
				}
				time.Sleep(250 * time.Millisecond)
			}
			if portal.MXID == "" {
				cc.log.Warn().Msg("Portal MXID never became available, dropping system notice")
				return
			}
			if err := send(retryCtx); err != nil {
				cc.log.Warn().Err(err).Msg("Failed to send system notice")
			}
		}()
		return
	}
	if err := send(ctx); err != nil {
		cc.log.Warn().Err(err).Msg("Failed to send system notice")
	}
}

func (cc *CodexClient) acquireRoomIfIdle(roomID id.RoomID) bool {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	if cc.activeRooms[roomID] {
		return false
	}
	cc.activeRooms[roomID] = true
	return true
}

func (cc *CodexClient) releaseRoom(roomID id.RoomID) {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	delete(cc.activeRooms, roomID)
}

// Streaming helpers (Codex -> Matrix AI SDK chunk mapping)
