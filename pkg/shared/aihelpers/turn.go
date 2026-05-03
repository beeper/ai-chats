package aihelpers

import (
	"context"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-chats/pkg/shared/streamui"
	"github.com/beeper/ai-chats/pkg/shared/turns"
)

type FinalMetadataProvider interface {
	FinalMetadata(turn *Turn, finishReason string) any
}
type FinalMetadataProviderFunc func(turn *Turn, finishReason string) any

func (f FinalMetadataProviderFunc) FinalMetadata(turn *Turn, finishReason string) any {
	if f == nil {
		return nil
	}
	return f(turn, finishReason)
}

type PlaceholderMessagePayload struct {
	Content    *event.MessageEventContent
	Extra      map[string]any
	DBMetadata any
}
type FinalEditPayload struct {
	Content       *event.MessageEventContent
	Extra         map[string]any
	TopLevelExtra map[string]any
}

// Turn is the central abstraction for an AI response turn.
type Turn struct {
	ctx     context.Context
	turnCtx context.Context
	cancel  context.CancelFunc

	conv    *Conversation
	emitter *streamui.Emitter
	state   *streamui.UIState
	session *turns.StreamSession
	turnID  string

	started bool
	ended   bool

	agent  *Agent
	source *SourceRef

	replyTo     id.EventID
	threadRoot  id.EventID
	startedAtMs int64

	sender           bridgev2.EventSender
	networkMessageID networkid.MessageID
	initialEventID   id.EventID
	sessionOnce      sync.Once
	streamStartOnce  sync.Once

	visibleText strings.Builder
	metadata    map[string]any
	startErr    error
	mu          sync.Mutex

	streamHook            func(turnID string, seq int, content map[string]any, txnID string) bool
	streamPublisherFunc   func(ctx context.Context) (bridgev2.BeeperStreamPublisher, bool)
	approvalRequester     func(ctx context.Context, turn *Turn, req ApprovalRequest) ApprovalHandle
	finalMetadataProvider FinalMetadataProvider
	placeholderPayload    *PlaceholderMessagePayload
	finalEditPayload      *FinalEditPayload
	sendFunc              func(ctx context.Context) (id.EventID, networkid.MessageID, error)
	sendFinalEditFunc     func(ctx context.Context)
	suppressSend          bool
	suppressFinalEdit     bool
	idleTimer             *time.Timer
	idleTimerSeq          uint64
}

func newTurn(ctx context.Context, conv *Conversation, agent *Agent, source *SourceRef) *Turn {
	if ctx == nil {
		ctx = context.Background()
	}
	turnCtx, cancel := context.WithCancel(ctx)
	turnID := NewTurnID()
	state := &streamui.UIState{TurnID: turnID}
	state.InitMaps()

	t := &Turn{
		ctx:         ctx,
		turnCtx:     turnCtx,
		cancel:      cancel,
		conv:        conv,
		state:       state,
		turnID:      turnID,
		agent:       agent,
		source:      source,
		startedAtMs: time.Now().UnixMilli(),
		metadata:    make(map[string]any),
	}

	t.emitter = &streamui.Emitter{
		State: state,
		Emit: func(callCtx context.Context, portal *bridgev2.Portal, part map[string]any) {
			t.emitPart(callCtx, portal, part, func() {
				if t.session != nil {
					t.session.EmitPart(callCtx, part)
				}
			})
		},
	}
	return t
}
func (t *Turn) providerIdentity() ProviderIdentity {
	if t.conv != nil {
		return t.conv.providerIdentity
	}
	return normalizedProviderIdentity(ProviderIdentity{})
}
func (t *Turn) resolveAgent(ctx context.Context) *Agent {
	if t.agent != nil {
		return t.agent
	}
	if t.conv == nil {
		return nil
	}
	agent, _ := t.conv.resolveDefaultAgent(ctx)
	return agent
}
func (t *Turn) resolveSender(ctx context.Context) bridgev2.EventSender {
	if t.sender.Sender != "" || t.sender.IsFromMe {
		return t.sender
	}
	if agent := t.resolveAgent(ctx); agent != nil && t.conv != nil && t.conv.login != nil {
		t.sender = agent.EventSender(t.conv.login.ID)
		return t.sender
	}
	if t.conv != nil {
		t.sender = t.conv.sender
	}
	return t.sender
}

// SetReplyTo sets the m.in_reply_to relation for this turn's message.
func (t *Turn) SetReplyTo(eventID id.EventID) {
	t.replyTo = eventID
}

// SetThread sets the m.thread relation for this turn's message.
func (t *Turn) SetThread(rootEventID id.EventID) {
	t.threadRoot = rootEventID
}

// SetStreamHook captures stream envelopes instead of sending ephemeral Matrix events when provided.
func (t *Turn) SetStreamHook(hook func(turnID string, seq int, content map[string]any, txnID string) bool) {
	t.streamHook = hook
}

// SetFinalMetadataProvider overrides the final DB metadata object persisted for the assistant message.
func (t *Turn) SetFinalMetadataProvider(provider FinalMetadataProvider) {
	t.finalMetadataProvider = provider
}

// SetPlaceholderMessagePayload overrides the placeholder message content while
// leaving stream descriptor attachment and relation wiring to the AIHelper.
func (t *Turn) SetPlaceholderMessagePayload(payload *PlaceholderMessagePayload) {
	t.placeholderPayload = payload
}

// SetFinalEditPayload stores the final edit payload that the AIHelper should send
// when the turn completes.
func (t *Turn) SetFinalEditPayload(payload *FinalEditPayload) {
	t.finalEditPayload = payload
}

// SetSuppressFinalEdit disables the AIHelper's automatic final edit construction
// when the bridge does not provide an explicit final edit payload.
func (t *Turn) SetSuppressFinalEdit(suppress bool) {
	t.suppressFinalEdit = suppress
}

// SetSendFunc overrides the default placeholder message sending in ensureStarted.
// The function should send the initial message and return the event/message IDs.
func (t *Turn) SetSendFunc(fn func(ctx context.Context) (id.EventID, networkid.MessageID, error)) {
	t.sendFunc = fn
}

// SetSuppressSend prevents the turn from sending any messages to the room.
// The turn still tracks state and emits UI events for local consumption.
func (t *Turn) SetSuppressSend(suppress bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.suppressSend = suppress
}

// SuppressSend reports whether room sends are currently suppressed.
func (t *Turn) SuppressSend() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.suppressSend
}

// InitialEventID returns the Matrix event ID of the placeholder message.
func (t *Turn) InitialEventID() id.EventID {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.initialEventID
}

// ThreadRoot returns the Matrix thread root event ID for this turn.
func (t *Turn) ThreadRoot() id.EventID {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.threadRoot
}

// NetworkMessageID returns the bridge network message ID of the placeholder.
func (t *Turn) NetworkMessageID() networkid.MessageID {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.networkMessageID
}

// SendStatus emits a bridge-level status update for the source event when possible.
func (t *Turn) SendStatus(status event.MessageStatus, message string) {
	if t.conv == nil || t.conv.portal == nil || t.source == nil || t.source.EventID == "" {
		return
	}
	SendMessageStatus(t.turnCtx, t.conv.portal, &event.Event{
		ID:     id.EventID(t.source.EventID),
		RoomID: t.conv.portal.MXID,
	}, bridgev2.MessageStatus{
		Status:    status,
		Message:   message,
		IsCertain: true,
	})
}

// ID returns the turn's unique identifier.
func (t *Turn) ID() string { return t.turnID }

// SetID overrides the turn identifier before the turn starts. Provider bridges
// can use this to preserve upstream turn/message IDs in AI helper-managed streams.
func (t *Turn) SetID(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || t.started {
		return
	}
	t.turnID = turnID
	if t.state != nil {
		t.state.TurnID = turnID
	}
}

// Context returns the turn-scoped context.
func (t *Turn) Context() context.Context { return t.turnCtx }

// Source returns the turn's structured source reference.
func (t *Turn) Source() *SourceRef { return t.source }

// SetSender overrides the bridge sender used for turn output. Call before the
// turn produces visible output.
func (t *Turn) SetSender(sender bridgev2.EventSender) { t.sender = sender }

// UIState returns the underlying streamui.UIState.
func (t *Turn) UIState() *streamui.UIState { return t.state }

// Err returns any startup error encountered by the turn transport.
func (t *Turn) Err() error {
	return t.startErr
}
