package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/event"
)

type fakeAgentLoopProvider struct {
	track          bool
	results        []fakeAgentLoopResult
	followUps      map[int][]openai.ChatCompletionMessageParamUnion
	finalizeCalls  int
	continueCalls  int
	roundsObserved []int
}

type fakeAgentLoopResult struct {
	continueLoop bool
	cle          *ContextLengthError
	err          error
}

func (f *fakeAgentLoopProvider) TrackRoomRunStreaming() bool {
	return f.track
}

func (f *fakeAgentLoopProvider) RunAgentTurn(_ context.Context, _ *event.Event, round int) (bool, *ContextLengthError, error) {
	f.roundsObserved = append(f.roundsObserved, round)
	if round >= len(f.results) {
		return false, nil, nil
	}
	result := f.results[round]
	return result.continueLoop, result.cle, result.err
}

func (f *fakeAgentLoopProvider) FinalizeAgentLoop(context.Context) {
	f.finalizeCalls++
}

func (f *fakeAgentLoopProvider) GetFollowUpMessages(_ context.Context) []openai.ChatCompletionMessageParamUnion {
	if len(f.roundsObserved) == 0 {
		return nil
	}
	return f.followUps[f.roundsObserved[len(f.roundsObserved)-1]]
}

func (f *fakeAgentLoopProvider) ContinueAgentLoop(messages []openai.ChatCompletionMessageParamUnion) {
	if len(messages) > 0 {
		f.continueCalls++
	}
}

func TestExecuteAgentLoopRoundsFinalizesOnTerminalTurn(t *testing.T) {
	provider := &fakeAgentLoopProvider{
		results: []fakeAgentLoopResult{
			{continueLoop: true},
			{continueLoop: false},
		},
	}

	success, cle, err := executeAgentLoopRounds(context.Background(), provider, nil)
	if !success {
		t.Fatalf("expected success=true")
	}
	if cle != nil {
		t.Fatalf("expected no context-length error, got %#v", cle)
	}
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if provider.finalizeCalls != 1 {
		t.Fatalf("expected finalize once, got %d", provider.finalizeCalls)
	}
	if len(provider.roundsObserved) != 2 || provider.roundsObserved[0] != 0 || provider.roundsObserved[1] != 1 {
		t.Fatalf("unexpected rounds observed: %#v", provider.roundsObserved)
	}
}

func TestExecuteAgentLoopRoundsStopsOnErrorWithFinalize(t *testing.T) {
	expectedErr := errors.New("boom")
	provider := &fakeAgentLoopProvider{
		results: []fakeAgentLoopResult{
			{err: expectedErr},
		},
	}

	success, cle, err := executeAgentLoopRounds(context.Background(), provider, nil)
	if success {
		t.Fatalf("expected success=false")
	}
	if cle != nil {
		t.Fatalf("expected no context-length error, got %#v", cle)
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected err=%v, got %v", expectedErr, err)
	}
	if provider.finalizeCalls != 1 {
		t.Fatalf("expected finalize on error, got %d", provider.finalizeCalls)
	}
}

func TestExecuteAgentLoopRoundsStopsOnContextLengthWithFinalize(t *testing.T) {
	expectedCLE := &ContextLengthError{RequestedTokens: 2000, ModelMaxTokens: 1000}
	provider := &fakeAgentLoopProvider{
		results: []fakeAgentLoopResult{
			{cle: expectedCLE},
		},
	}

	success, cle, err := executeAgentLoopRounds(context.Background(), provider, nil)
	if success {
		t.Fatalf("expected success=false")
	}
	if cle != expectedCLE {
		t.Fatalf("expected cle=%#v, got %#v", expectedCLE, cle)
	}
	if err != nil {
		t.Fatalf("expected no generic error, got %v", err)
	}
	if provider.finalizeCalls != 1 {
		t.Fatalf("expected finalize on context-length error, got %d", provider.finalizeCalls)
	}
}

func TestExecuteAgentLoopRoundsContinuesForFollowUpMessages(t *testing.T) {
	provider := &fakeAgentLoopProvider{
		results: []fakeAgentLoopResult{
			{continueLoop: false},
			{continueLoop: false},
		},
		followUps: map[int][]openai.ChatCompletionMessageParamUnion{
			0: {openai.UserMessage("follow up")},
		},
	}

	success, cle, err := executeAgentLoopRounds(context.Background(), provider, nil)
	if !success {
		t.Fatalf("expected success=true")
	}
	if cle != nil {
		t.Fatalf("expected no context-length error, got %#v", cle)
	}
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if provider.continueCalls != 1 {
		t.Fatalf("expected one follow-up continuation, got %d", provider.continueCalls)
	}
	if provider.finalizeCalls != 1 {
		t.Fatalf("expected finalize once, got %d", provider.finalizeCalls)
	}
	if len(provider.roundsObserved) != 2 || provider.roundsObserved[0] != 0 || provider.roundsObserved[1] != 1 {
		t.Fatalf("unexpected rounds observed: %#v", provider.roundsObserved)
	}
}
