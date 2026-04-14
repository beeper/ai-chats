package ai

import (
	"testing"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func TestResolveQueueSettingsUsesConfigDefaults(t *testing.T) {
	debounce := 2500
	capValue := 7
	cfg := &Config{
		Messages: &MessagesConfig{
			Queue: &QueueConfig{
				Mode:       "followup",
				DebounceMs: &debounce,
				Cap:        &capValue,
				Drop:       "new",
			},
		},
	}

	settings := resolveQueueSettings(queueResolveParams{
		cfg:     cfg,
		channel: "matrix",
	})

	if settings.Mode != airuntime.QueueModeFollowup {
		t.Fatalf("expected followup mode, got %q", settings.Mode)
	}
	if settings.DebounceMs != debounce {
		t.Fatalf("expected debounce %d, got %d", debounce, settings.DebounceMs)
	}
	if settings.Cap != capValue {
		t.Fatalf("expected cap %d, got %d", capValue, settings.Cap)
	}
	if settings.DropPolicy != airuntime.QueueDropNew {
		t.Fatalf("expected drop policy %q, got %q", airuntime.QueueDropNew, settings.DropPolicy)
	}
}

func TestResolveQueueSettingsInlineOverridesWin(t *testing.T) {
	debounce := 900
	capValue := 3
	dropPolicy := airuntime.QueueDropOld

	settings := resolveQueueSettings(queueResolveParams{
		cfg:        &Config{},
		channel:    "matrix",
		inlineMode: airuntime.QueueModeSteer,
		inlineOpts: airuntime.QueueInlineOptions{
			DebounceMs: &debounce,
			Cap:        &capValue,
			DropPolicy: &dropPolicy,
		},
	})

	if settings.Mode != airuntime.QueueModeSteer {
		t.Fatalf("expected steer mode, got %q", settings.Mode)
	}
	if settings.DebounceMs != debounce {
		t.Fatalf("expected debounce %d, got %d", debounce, settings.DebounceMs)
	}
	if settings.Cap != capValue {
		t.Fatalf("expected cap %d, got %d", capValue, settings.Cap)
	}
	if settings.DropPolicy != dropPolicy {
		t.Fatalf("expected drop policy %q, got %q", dropPolicy, settings.DropPolicy)
	}
}
