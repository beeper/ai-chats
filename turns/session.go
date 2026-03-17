package turns

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
)

type EndReason string

const (
	EndReasonFinish     EndReason = "finish"
	EndReasonDisconnect EndReason = "disconnect"
	EndReasonError      EndReason = "error"
)

type StreamSessionParams struct {
	TurnID  string
	AgentID string

	GetStreamTarget      func() StreamTarget
	ResolveTargetEventID TargetEventResolver
	GetTargetEventID     func() id.EventID
	GetRoomID            func() id.RoomID
	GetSuppressSend      func() bool
	GetStreamType        func() string
	NextSeq              func() int

	GetStreamTransport func(ctx context.Context) (bridgev2.StreamTransport, bool)
	ClearTurnGate      func()
	SendHook           func(turnID string, seq int, content map[string]any, txnID string) bool
	Logger             *zerolog.Logger
}

type StreamSession struct {
	params StreamSessionParams

	closed atomic.Bool

	targetMu          sync.Mutex
	resolvedTargetIDs map[StreamTarget]id.EventID

	streamMu      sync.Mutex
	streamStarted bool
	targetEventID id.EventID
	pendingParts  []pendingStreamPart

	flushMu sync.Mutex

	descriptorOnce sync.Once
	descriptor     *event.BeeperStreamInfo
	descriptorErr  error
}

type pendingStreamPart struct {
	seq  int
	part map[string]any
}

func NewStreamSession(params StreamSessionParams) *StreamSession {
	return &StreamSession{
		params:            params,
		resolvedTargetIDs: make(map[StreamTarget]id.EventID),
	}
}

func (s *StreamSession) IsClosed() bool {
	return s == nil || s.closed.Load()
}

func (s *StreamSession) Descriptor(ctx context.Context) (*event.BeeperStreamInfo, error) {
	if s == nil {
		return nil, context.Canceled
	}
	s.descriptorOnce.Do(func() {
		transport, ok := s.params.GetStreamTransport(ctx)
		if !ok || transport == nil {
			s.descriptorErr = context.Canceled
			return
		}
		roomID := s.roomID()
		if roomID == "" {
			s.descriptorErr = context.Canceled
			return
		}
		s.descriptor, s.descriptorErr = transport.BuildDescriptor(ctx, &bridgev2.StreamDescriptorRequest{
			RoomID: roomID,
			Type:   s.streamType(),
		})
	})
	return s.descriptor, s.descriptorErr
}

func (s *StreamSession) Start(ctx context.Context, targetEventID id.EventID) error {
	if s == nil || s.IsClosed() {
		return context.Canceled
	}
	roomID := s.roomID()
	if roomID == "" || targetEventID == "" {
		return context.Canceled
	}
	transport, ok := s.params.GetStreamTransport(ctx)
	if !ok || transport == nil {
		return context.Canceled
	}
	descriptor, err := s.Descriptor(ctx)
	if err != nil {
		return err
	}
	alreadyStarted, pendingCount, err := s.tryStart(ctx, transport, roomID, targetEventID, descriptor)
	if err != nil {
		return err
	}
	if alreadyStarted {
		s.logDebug("stream_already_started", nil,
			"room_id", roomID.String(),
			"event_id", targetEventID.String(),
			"pending_count", pendingCount,
		)
	} else {
		s.logDebug("stream_started", nil,
			"room_id", roomID.String(),
			"event_id", targetEventID.String(),
			"pending_count", pendingCount,
			"stream_type", s.streamType(),
		)
	}
	return s.FlushPending(ctx)
}

func (s *StreamSession) tryStart(ctx context.Context, transport bridgev2.StreamTransport, roomID id.RoomID, targetEventID id.EventID, descriptor *event.BeeperStreamInfo) (alreadyStarted bool, pendingCount int, err error) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	pendingCount = len(s.pendingParts)
	if s.streamStarted && s.targetEventID == targetEventID {
		return true, pendingCount, nil
	}
	err = transport.Start(ctx, &bridgev2.StartStreamRequest{
		RoomID:     roomID,
		EventID:    targetEventID,
		Type:       s.streamType(),
		Descriptor: descriptor,
	})
	if err != nil {
		return false, pendingCount, err
	}
	s.streamStarted = true
	s.targetEventID = targetEventID
	return false, pendingCount, nil
}

func (s *StreamSession) EnsureStarted(ctx context.Context) (bool, error) {
	if s == nil || s.IsClosed() {
		return false, context.Canceled
	}
	targetEventID, err := s.currentTargetEventID(ctx)
	if err != nil {
		return false, err
	}
	if targetEventID == "" {
		return false, nil
	}
	return true, s.Start(ctx, targetEventID)
}

func (s *StreamSession) End(ctx context.Context, _ EndReason) {
	if s == nil || !s.closed.CompareAndSwap(false, true) {
		return
	}
	defer func() {
		if s.params.ClearTurnGate != nil {
			s.params.ClearTurnGate()
		}
	}()

	s.streamMu.Lock()
	targetEventID := s.targetEventID
	started := s.streamStarted
	hasPending := len(s.pendingParts) > 0
	s.streamMu.Unlock()
	if !started && hasPending {
		s.logWarn("stream_target_never_resolved", nil)
	}
	if !started || targetEventID == "" {
		return
	}
	transport, ok := s.params.GetStreamTransport(ctx)
	if !ok || transport == nil {
		return
	}
	_ = transport.Finish(ctx, &bridgev2.FinishStreamRequest{
		RoomID:  s.roomID(),
		EventID: targetEventID,
	})
}

func (s *StreamSession) EmitPart(ctx context.Context, part map[string]any) {
	if s.IsClosed() || part == nil {
		return
	}
	if s.params.GetSuppressSend != nil && s.params.GetSuppressSend() {
		return
	}
	if s.params.NextSeq == nil {
		return
	}

	seq := s.params.NextSeq()
	s.enqueuePendingPart(seq, part)
	s.logDebug("stream_part_enqueued", nil,
		"seq", seq,
		"pending_count", s.pendingCount(),
	)

	targetEventID, err := s.currentTargetEventID(ctx)
	if err != nil {
		s.logWarn("resolve_target_event_id_failed", err)
		return
	}
	if targetEventID == "" {
		s.logDebug("stream_target_event_id_pending", nil,
			"pending_count", s.pendingCount(),
		)
		return
	}
	if err = s.Start(ctx, targetEventID); err != nil {
		s.logWarn("stream_start_failed", err)
	}
}

func (s *StreamSession) FlushPending(ctx context.Context) error {
	if s == nil || s.IsClosed() {
		return context.Canceled
	}
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	targetEventID, err := s.currentTargetEventID(ctx)
	if err != nil || targetEventID == "" {
		return err
	}
	roomID := s.roomID()
	s.logDebug("stream_flush_begin", nil,
		"room_id", roomID.String(),
		"event_id", targetEventID.String(),
		"pending_count", s.pendingCount(),
	)
	for {
		pending, ok := s.dequeuePendingPart()
		if !ok {
			s.logDebug("stream_flush_complete", nil,
				"room_id", roomID.String(),
				"event_id", targetEventID.String(),
			)
			return nil
		}
		s.logDebug("stream_publish_pending_part", nil,
			"room_id", roomID.String(),
			"event_id", targetEventID.String(),
			"seq", pending.seq,
			"pending_count", s.pendingCount(),
		)
		err = s.publishPendingPart(ctx, targetEventID, pending)
		if err != nil {
			s.requeuePendingFront(pending)
			s.logWarn("stream_publish_failed", err)
			return err
		}
	}
}

func (s *StreamSession) currentTargetEventID(ctx context.Context) (id.EventID, error) {
	if s == nil {
		return "", context.Canceled
	}
	if s.params.GetTargetEventID != nil {
		if eventID := s.params.GetTargetEventID(); eventID != "" {
			s.logDebug("stream_target_event_id_immediate", nil,
				"event_id", eventID.String(),
			)
			return eventID, nil
		}
	}

	target := StreamTarget{}
	if s.params.GetStreamTarget != nil {
		target = s.params.GetStreamTarget()
	}
	if !target.HasEditTarget() {
		return "", nil
	}

	s.targetMu.Lock()
	if resolved, ok := s.resolvedTargetIDs[target]; ok {
		s.targetMu.Unlock()
		s.logDebug("stream_target_event_id_cached", nil,
			"event_id", resolved.String(),
			"network_message_id", string(target.NetworkMessageID),
		)
		return resolved, nil
	}
	s.targetMu.Unlock()

	if s.params.ResolveTargetEventID == nil {
		return "", nil
	}
	resolved, err := s.params.ResolveTargetEventID(ctx, target)
	if err != nil || resolved == "" {
		return resolved, err
	}
	s.targetMu.Lock()
	s.resolvedTargetIDs[target] = resolved
	s.targetMu.Unlock()
	s.logDebug("stream_target_event_id_resolved", nil,
		"event_id", resolved.String(),
		"network_message_id", string(target.NetworkMessageID),
	)
	return resolved, nil
}

func (s *StreamSession) roomID() id.RoomID {
	if s == nil || s.params.GetRoomID == nil {
		return ""
	}
	return s.params.GetRoomID()
}

func (s *StreamSession) streamType() string {
	if s == nil || s.params.GetStreamType == nil {
		return matrixevents.StreamEventMessageType.Type
	}
	streamType := strings.TrimSpace(s.params.GetStreamType())
	if streamType == "" {
		return matrixevents.StreamEventMessageType.Type
	}
	return streamType
}

func (s *StreamSession) enqueuePendingPart(seq int, part map[string]any) {
	if s == nil || seq <= 0 || part == nil {
		return
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	s.pendingParts = append(s.pendingParts, pendingStreamPart{
		seq:  seq,
		part: maps.Clone(part),
	})
}

func (s *StreamSession) dequeuePendingPart() (pendingStreamPart, bool) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if len(s.pendingParts) == 0 {
		return pendingStreamPart{}, false
	}
	pending := s.pendingParts[0]
	s.pendingParts = s.pendingParts[1:]
	return pending, true
}

func (s *StreamSession) requeuePendingFront(pending pendingStreamPart) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	s.pendingParts = slices.Insert(s.pendingParts, 0, pending)
}

func (s *StreamSession) pendingCount() int {
	if s == nil {
		return 0
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	return len(s.pendingParts)
}

func (s *StreamSession) publishPendingPart(ctx context.Context, targetEventID id.EventID, pending pendingStreamPart) error {
	delta, err := matrixevents.BuildStreamEventEnvelope(strings.TrimSpace(s.params.TurnID), pending.seq, pending.part, matrixevents.StreamEventOpts{
		RelatesToEventID: string(targetEventID),
		AgentID:          strings.TrimSpace(s.params.AgentID),
	})
	if err != nil {
		return err
	}
	content := map[string]any{
		"com.beeper.llm.deltas": []map[string]any{delta},
	}
	txnID := matrixevents.BuildStreamEventTxnID(s.params.TurnID, pending.seq)
	if s.params.SendHook != nil && s.params.SendHook(s.params.TurnID, pending.seq, content, txnID) {
		return nil
	}
	transport, ok := s.params.GetStreamTransport(ctx)
	if !ok || transport == nil {
		return context.Canceled
	}
	return transport.Publish(ctx, &bridgev2.PublishStreamRequest{
		RoomID:  s.roomID(),
		EventID: targetEventID,
		Content: content,
	})
}

func (s *StreamSession) logWarn(reason string, err error) {
	if s == nil || s.params.Logger == nil {
		return
	}
	logEvt := s.params.Logger.Warn().Str("reason", reason)
	if err != nil {
		logEvt = logEvt.Err(err)
	}
	logEvt.Msg("Stream transport operation failed")
}

func (s *StreamSession) logDebug(reason string, err error, kv ...any) {
	if s == nil || s.params.Logger == nil {
		return
	}
	logEvt := s.params.Logger.Debug().Str("reason", reason)
	if err != nil {
		logEvt = logEvt.Err(err)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok || key == "" {
			continue
		}
		switch value := kv[i+1].(type) {
		case string:
			logEvt = logEvt.Str(key, value)
		case int:
			logEvt = logEvt.Int(key, value)
		case int64:
			logEvt = logEvt.Int64(key, value)
		case bool:
			logEvt = logEvt.Bool(key, value)
		default:
			logEvt = logEvt.Interface(key, value)
		}
	}
	logEvt.Msg("Stream transport diagnostic")
}
