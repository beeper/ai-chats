package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/shared/streamui"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func newTestStreamingStateWithTurn() *streamingState {
	state := newStreamingState(context.Background(), nil, "")
	conv := bridgesdk.NewConversation[*AIClient, *Config](context.Background(), nil, nil, bridgev2.EventSender{}, nil, nil)
	state.turn = conv.StartTurn(context.Background(), nil, nil)
	return state
}

func TestStreamingStateHasTargets(t *testing.T) {
	t.Run("event-id", func(t *testing.T) {
		state := newTestStreamingStateWithTurn()
		// Simulate Turn having sent an initial message with an event ID.
		state.turn.SetSendFunc(func(ctx context.Context) (id.EventID, networkid.MessageID, error) {
			return id.EventID("$evt"), "", nil
		})
		// Trigger ensureStarted by calling Writer.
		state.writer().TextDelta(context.Background(), "x")
		if !state.hasEphemeralTarget() {
			t.Fatalf("expected event-id target to be a valid ephemeral target")
		}
	})

	t.Run("network-message-id", func(t *testing.T) {
		state := newTestStreamingStateWithTurn()
		state.turn.SetSendFunc(func(ctx context.Context) (id.EventID, networkid.MessageID, error) {
			return "", networkid.MessageID("msg-1"), nil
		})
		state.writer().TextDelta(context.Background(), "x")
		if !state.hasEditTarget() {
			t.Fatalf("expected network-message-id target to be a valid edit target")
		}
		if state.hasEphemeralTarget() {
			t.Fatalf("did not expect network-message-id alone to be a valid ephemeral target")
		}
	})

	t.Run("none", func(t *testing.T) {
		state := newTestStreamingStateWithTurn()
		state.turn.SetSuppressSend(true)
		state.writer().TextDelta(context.Background(), "x")
		if state.hasEditTarget() || state.hasEphemeralTarget() {
			t.Fatalf("expected empty state to have no targets")
		}
	})
}

func TestStreamFailureErrorUsesAnyMessageTarget(t *testing.T) {
	testErr := errors.New("boom")

	t.Run("with-network-message-id", func(t *testing.T) {
		state := newTestStreamingStateWithTurn()
		state.turn.SetSendFunc(func(ctx context.Context) (id.EventID, networkid.MessageID, error) {
			return "", networkid.MessageID("msg-1"), nil
		})
		state.writer().TextDelta(context.Background(), "x")
		err := streamFailureError(state, testErr)
		var nf *NonFallbackError
		if !errors.As(err, &nf) {
			t.Fatalf("expected NonFallbackError, got %T", err)
		}
	})

	t.Run("without-target", func(t *testing.T) {
		state := newTestStreamingStateWithTurn()
		state.turn.SetSuppressSend(true)
		state.writer().TextDelta(context.Background(), "x")
		err := streamFailureError(state, testErr)
		var pf *PreDeltaError
		if !errors.As(err, &pf) {
			t.Fatalf("expected PreDeltaError, got %T", err)
		}
	})
}

func TestFinishStreamingWithFailureCancelledEndsTurnAsCancelled(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	state.turn.SetSuppressSend(true)
	state.writer().TextDelta(context.Background(), "hello")

	err := (&AIClient{}).finishStreamingWithFailure(
		context.Background(),
		zerolog.Nop(),
		nil,
		state,
		nil,
		"cancelled",
		context.Canceled,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected wrapped cancellation error, got %#v", err)
	}

	message := streamui.SnapshotUIMessage(state.turn.UIState())
	metadata, _ := message["metadata"].(map[string]any)
	if metadata["finish_reason"] != "cancelled" {
		t.Fatalf("expected cancelled finish_reason, got %#v", metadata["finish_reason"])
	}
}
