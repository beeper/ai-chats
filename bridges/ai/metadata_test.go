package ai

import (
	"encoding/json"
	"testing"
)

func TestClonePortalMetadataDeepCopiesConfig(t *testing.T) {
	orig := &PortalMetadata{
		PDFConfig:             &PDFConfig{Engine: "mistral"},
		TypingIntervalSeconds: ptrInt(42),
		SessionBootstrapByAgent: map[string]int64{
			"beeper": 123,
		},
	}

	clone := clonePortalMetadata(orig)
	if clone == nil {
		t.Fatal("expected clone to be non-nil")
	}
	if clone == orig {
		t.Fatal("expected clone to be a different pointer")
	}
	if clone.PDFConfig == orig.PDFConfig {
		t.Fatal("expected PDF config to be copied")
	}
	if clone.TypingIntervalSeconds == orig.TypingIntervalSeconds {
		t.Fatal("expected typing interval to be copied")
	}
	if clone.SessionBootstrapByAgent["beeper"] != 123 {
		t.Fatalf("expected session bootstrap map to be copied, got %#v", clone.SessionBootstrapByAgent)
	}

	clone.PDFConfig.Engine = "other"
	*clone.TypingIntervalSeconds = 99
	clone.SessionBootstrapByAgent["beeper"] = 456

	if orig.PDFConfig.Engine != "mistral" {
		t.Fatalf("expected original PDF engine to remain, got %q", orig.PDFConfig.Engine)
	}
	if *orig.TypingIntervalSeconds != 42 {
		t.Fatalf("expected original typing interval to remain, got %d", *orig.TypingIntervalSeconds)
	}
	if orig.SessionBootstrapByAgent["beeper"] != 123 {
		t.Fatalf("expected original session bootstrap map to remain, got %#v", orig.SessionBootstrapByAgent)
	}
}

func TestPortalMetadataDoesNotMarshalPersistentState(t *testing.T) {
	meta := &PortalMetadata{
		AckReactionEmoji:       "👍",
		Slug:                   "chat-1",
		Title:                  "Chat",
		WelcomeSent:            true,
		AutoGreetingSent:       true,
		SessionResetAt:         123,
		ModuleMeta:             map[string]any{"cron": map[string]any{"is_internal_room": true}},
		SubagentParentRoomID:   "!parent:example.com",
		TypingMode:             "thinking",
		TypingIntervalSeconds:   ptrInt(12),
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(data) != "{}" {
		t.Fatalf("expected persistent portal state to be omitted from JSON, got %s", string(data))
	}
}

func TestPersistedPortalStateRoundTrip(t *testing.T) {
	orig := &PortalMetadata{
		AckReactionEmoji:     "👍",
		AckReactionRemoveAfter: true,
		PDFConfig:            &PDFConfig{Engine: "mistral"},
		Slug:                 "chat-7",
		Title:                "Example",
		TitleGenerated:       true,
		WelcomeSent:          true,
		AutoGreetingSent:     true,
		SessionResetAt:       123,
		AbortedLastRun:       true,
		CompactionCount:      9,
		SessionBootstrappedAt: 456,
		SessionBootstrapByAgent: map[string]int64{
			"beeper": 789,
		},
		ModuleMeta: map[string]any{
			"cron": map[string]any{"is_internal_room": true},
		},
		SubagentParentRoomID: "!parent:example.com",
		DebounceMs:           250,
		TypingMode:           "thinking",
		TypingIntervalSeconds: ptrInt(15),
	}

	state := persistedPortalStateFromMeta(orig)
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var restored aiPersistedPortalState
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	clone := &PortalMetadata{}
	applyPersistedPortalState(clone, &restored)

	if clone.AckReactionEmoji != orig.AckReactionEmoji || !clone.AckReactionRemoveAfter || clone.PDFConfig == nil {
		t.Fatalf("unexpected restored state: %#v", clone)
	}
	if clone.Slug != orig.Slug || clone.Title != orig.Title || !clone.TitleGenerated {
		t.Fatalf("expected title fields to round-trip: %#v", clone)
	}
	if clone.SessionBootstrapByAgent["beeper"] != 789 {
		t.Fatalf("expected bootstrap map to round-trip, got %#v", clone.SessionBootstrapByAgent)
	}
	if clone.ModuleMeta == nil || clone.ModuleMeta["cron"] == nil {
		t.Fatalf("expected module meta to round-trip, got %#v", clone.ModuleMeta)
	}
	if clone.TypingIntervalSeconds == nil || *clone.TypingIntervalSeconds != 15 {
		t.Fatalf("expected typing interval to round-trip, got %#v", clone.TypingIntervalSeconds)
	}
}

func ptrInt(v int) *int { return &v }
