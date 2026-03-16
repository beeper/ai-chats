package turns

import (
	"context"
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

	descriptorOnce sync.Once
	descriptor     *event.BeeperStreamInfo
	descriptorErr  error
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
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.streamStarted && s.targetEventID == targetEventID {
		return nil
	}
	err = transport.Start(ctx, &bridgev2.StartStreamRequest{
		RoomID:     roomID,
		EventID:    targetEventID,
		Type:       s.streamType(),
		Descriptor: descriptor,
	})
	if err != nil {
		return err
	}
	s.streamStarted = true
	s.targetEventID = targetEventID
	return nil
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
	s.streamMu.Unlock()
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

	targetEventID, err := s.currentTargetEventID(ctx)
	if err != nil || targetEventID == "" {
		s.logWarn("missing_target_event_id", err)
		return
	}
	if err = s.Start(ctx, targetEventID); err != nil {
		s.logWarn("stream_start_failed", err)
		return
	}

	seq := s.params.NextSeq()
	delta, err := matrixevents.BuildStreamEventEnvelope(strings.TrimSpace(s.params.TurnID), seq, part, matrixevents.StreamEventOpts{
		RelatesToEventID: string(targetEventID),
		AgentID:          strings.TrimSpace(s.params.AgentID),
	})
	if err != nil {
		s.logWarn("build_stream_event_failed", err)
		return
	}
	content := map[string]any{
		"com.beeper.llm.deltas": []map[string]any{delta},
	}
	txnID := matrixevents.BuildStreamEventTxnID(s.params.TurnID, seq)
	if s.params.SendHook != nil && s.params.SendHook(s.params.TurnID, seq, content, txnID) {
		return
	}
	transport, ok := s.params.GetStreamTransport(ctx)
	if !ok || transport == nil {
		s.logWarn("missing_stream_transport", nil)
		return
	}
	err = transport.Publish(ctx, &bridgev2.PublishStreamRequest{
		RoomID:  s.roomID(),
		EventID: targetEventID,
		Content: content,
	})
	if err != nil {
		s.logWarn("stream_publish_failed", err)
	}
}

func (s *StreamSession) currentTargetEventID(ctx context.Context) (id.EventID, error) {
	if s == nil {
		return "", context.Canceled
	}
	if s.params.GetTargetEventID != nil {
		if eventID := s.params.GetTargetEventID(); eventID != "" {
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
