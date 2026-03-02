package connector

import (
	"testing"

	"maunium.net/go/mautrix/id"

	runtimeparse "github.com/beeper/ai-bridge/pkg/runtime"
)

func TestResolveFinalReplyTarget_ModeOffStripsDirectiveReply(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Channels: &ChannelsConfig{
					Matrix: &ChannelConfig{ReplyToMode: "off"},
				},
			},
		},
	}
	state := &streamingState{
		replyTarget: ReplyTarget{
			ReplyTo:    "$inbound",
			ThreadRoot: "$thread",
		},
	}
	directives := &runtimeparse.ReplyDirectiveResult{
		ReplyToID:      "$explicit",
		ReplyToCurrent: true,
		HasReplyTag:    true,
	}
	target := client.resolveFinalReplyTarget(nil, state, directives)
	if target.ReplyTo != "" {
		t.Fatalf("expected reply target to be stripped in off mode, got %q", target.ReplyTo)
	}
	if target.ThreadRoot != "" {
		t.Fatalf("expected thread root to be cleared in off mode, got %q", target.ThreadRoot)
	}
}

func TestResolveFinalReplyTarget_ModeAllUsesExplicit(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Channels: &ChannelsConfig{
					Matrix: &ChannelConfig{ReplyToMode: "all"},
				},
			},
		},
	}
	state := &streamingState{
		replyTarget: ReplyTarget{
			ReplyTo:    id.EventID("$inbound"),
			ThreadRoot: id.EventID("$thread"),
		},
	}
	directives := &runtimeparse.ReplyDirectiveResult{
		ReplyToID:   id.EventID("$explicit").String(),
		HasReplyTag: true,
	}
	target := client.resolveFinalReplyTarget(nil, state, directives)
	if target.ReplyTo != id.EventID("$explicit") {
		t.Fatalf("expected explicit reply target, got %q", target.ReplyTo)
	}
	if target.ThreadRoot != id.EventID("$thread") {
		t.Fatalf("expected existing thread root to remain, got %q", target.ThreadRoot)
	}
}
