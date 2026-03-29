package turns

import (
	"context"
	"errors"
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

var (
	ErrClosed          = errors.New("stream session closed")
	ErrNoPublisher     = errors.New("stream session has no publisher")
	ErrNoRoomID        = errors.New("stream session has no room id")
	ErrNoTargetEventID = errors.New("stream session has no target event id")
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

	GetStreamPublisher func(ctx context.Context) (bridgev2.BeeperStreamPublisher, bool)
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

	descriptorMu     sync.Mutex
	descriptor       *event.BeeperStreamInfo
	descriptorLoaded bool
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
		return nil, ErrClosed
	}
	s.descriptorMu.Lock()
	if s.descriptorLoaded {
		descriptor := s.descriptor
		s.descriptorMu.Unlock()
		return descriptor, nil
	}
	s.descriptorMu.Unlock()

	if s.params.GetStreamPublisher == nil {
		return nil, ErrNoPublisher
	}
	publisher, ok := s.params.GetStreamPublisher(ctx)
	if !ok || publisher == nil {
		return nil, ErrNoPublisher
	}
	roomID := s.roomID()
	if roomID == "" {
		return nil, ErrNoRoomID
	}
	descriptor, err := publisher.NewDescriptor(ctx, roomID, s.streamType())
	if err != nil {
		return nil, err
	}
	s.descriptorMu.Lock()
	defer s.descriptorMu.Unlock()
	if s.descriptorLoaded {
		return s.descriptor, nil
	}
	s.descriptor = descriptor
	s.descriptorLoaded = true
	return s.descriptor, nil
}

func (s *StreamSession) Start(ctx context.Context, targetEventID id.EventID) error {
	if s == nil || s.IsClosed() {
		return ErrClosed
	}
	if targetEventID == "" {
		return ErrNoTargetEventID
	}
	var (
		publisher bridgev2.BeeperStreamPublisher
		ok        bool
	)
	if s.params.GetStreamPublisher != nil {
		publisher, ok = s.params.GetStreamPublisher(ctx)
	}
	publisherAvailable := ok && publisher != nil
	hookAvailable := s.params.SendHook != nil
	roomID := s.roomID()

	if !publisherAvailable && !hookAvailable {
		return ErrNoPublisher
	}
	if publisherAvailable && roomID == "" && !hookAvailable {
		return ErrNoRoomID
	}

	var descriptor *event.BeeperStreamInfo
	var err error
	if publisherAvailable && roomID != "" {
		descriptor, err = s.Descriptor(ctx)
		if err != nil {
			return err
		}
	}
	_, _, err = s.tryStart(ctx, publisher, roomID, targetEventID, descriptor)
	if err != nil {
		return err
	}
	return s.FlushPending(ctx)
}

func (s *StreamSession) tryStart(
	ctx context.Context,
	publisher bridgev2.BeeperStreamPublisher,
	roomID id.RoomID,
	targetEventID id.EventID,
	descriptor *event.BeeperStreamInfo,
) (alreadyStarted bool, pendingCount int, err error) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	pendingCount = len(s.pendingParts)
	if s.streamStarted && s.targetEventID == targetEventID {
		return true, pendingCount, nil
	}
	s.targetEventID = targetEventID
	if publisher != nil {
		if roomID == "" || descriptor == nil {
			return false, pendingCount, nil
		}
		err = publisher.Register(ctx, roomID, targetEventID, descriptor)
		if err != nil {
			return false, pendingCount, err
		}
		s.streamStarted = true
		return false, pendingCount, nil
	}
	s.streamStarted = true
	return false, pendingCount, nil
}

func (s *StreamSession) EnsureStarted(ctx context.Context) (bool, error) {
	if s == nil || s.IsClosed() {
		return false, ErrClosed
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
	s.streamMu.Unlock()
	if !started || targetEventID == "" {
		return
	}
	if s.params.GetStreamPublisher == nil {
		return
	}
	publisher, ok := s.params.GetStreamPublisher(ctx)
	if !ok || publisher == nil {
		return
	}
	publisher.Unregister(s.roomID(), targetEventID)
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

	targetEventID, err := s.currentTargetEventID(ctx)
	if err != nil {
		s.logWarn("resolve_target_event_id_failed", err)
		return
	}
	if targetEventID == "" {
		return
	}
	if err = s.Start(ctx, targetEventID); err != nil {
		s.logWarn("stream_start_failed", err, "event_id", targetEventID.String())
	}
}

func (s *StreamSession) FlushPending(ctx context.Context) error {
	if s == nil || s.IsClosed() {
		return ErrClosed
	}
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	targetEventID, err := s.currentTargetEventID(ctx)
	if err != nil || targetEventID == "" {
		return err
	}
	for {
		pending, ok := s.dequeuePendingPart()
		if !ok {
			return nil
		}
		err = s.publishPendingPart(ctx, targetEventID, pending)
		if err != nil {
			s.requeuePendingFront(pending)
			s.logWarn("stream_publish_failed", err, "event_id", targetEventID.String(), "seq", pending.seq)
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
			return eventID, nil
		}
	}
	s.streamMu.Lock()
	if eventID := s.targetEventID; eventID != "" {
		s.streamMu.Unlock()
		return eventID, nil
	}
	s.streamMu.Unlock()

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

func (s *StreamSession) publishPendingPart(ctx context.Context, targetEventID id.EventID, pending pendingStreamPart) error {
	delta, err := matrixevents.BuildStreamEventEnvelope(strings.TrimSpace(s.params.TurnID), pending.seq, pending.part, matrixevents.StreamEventOpts{
		RelatesToEventID: string(targetEventID),
		AgentID:          strings.TrimSpace(s.params.AgentID),
	})
	if err != nil {
		return err
	}
	deltaKey := s.streamType() + ".deltas"
	if descriptorType := strings.TrimSpace(s.descriptorType()); descriptorType != "" {
		deltaKey = descriptorType + ".deltas"
	}
	content := map[string]any{
		deltaKey: []map[string]any{delta},
	}
	txnID := matrixevents.BuildStreamEventTxnID(s.params.TurnID, pending.seq)
	if s.params.SendHook != nil && s.params.SendHook(s.params.TurnID, pending.seq, content, txnID) {
		return nil
	}
	if s.params.GetStreamPublisher == nil {
		return ErrNoPublisher
	}
	publisher, ok := s.params.GetStreamPublisher(ctx)
	if !ok || publisher == nil {
		return ErrNoPublisher
	}
	roomID := s.roomID()
	if roomID == "" {
		return ErrNoRoomID
	}
	return publisher.Publish(ctx, roomID, targetEventID, content)
}

func (s *StreamSession) descriptorType() string {
	if s == nil || s.descriptor == nil {
		return ""
	}
	return s.descriptor.Type
}

func (s *StreamSession) logWarn(reason string, err error, kv ...any) {
	if s == nil || s.params.Logger == nil {
		return
	}
	logEvt := s.params.Logger.Warn().Str("reason", reason)
	if err != nil {
		logEvt = logEvt.Err(err)
	}
	if turnID := strings.TrimSpace(s.params.TurnID); turnID != "" {
		logEvt = logEvt.Str("turn_id", turnID)
	}
	if roomID := s.roomID(); roomID != "" {
		logEvt = logEvt.Stringer("room_id", roomID)
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
	logEvt.Msg("Stream transport operation failed")
}
