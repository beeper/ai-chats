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

func TestPortalMetadataMarshalsRoomConfigOnly(t *testing.T) {
	meta := &PortalMetadata{
		AckReactionEmoji:       "👍",
		AckReactionRemoveAfter: true,
		PDFConfig:              &PDFConfig{Engine: "mistral"},
		Slug:                   "chat-1",
		WelcomeSent:            true,
		AutoGreetingSent:       true,
		SessionResetAt:         123,
		ModuleMeta:             map[string]any{"cron": map[string]any{"is_internal_room": true}},
		SubagentParentRoomID:   "!parent:example.com",
		TypingMode:             "thinking",
		TypingIntervalSeconds:  ptrInt(12),
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	for _, key := range []string{
		"ack_reaction_emoji",
		"ack_reaction_remove_after",
		"pdf_config",
		"slug",
		"subagent_parent_room_id",
		"typing_mode",
		"typing_interval_seconds",
	} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("expected %q to be persisted in portal metadata, got %s", key, string(data))
		}
	}
	for _, key := range []string{
		"welcome_sent",
		"auto_greeting_sent",
		"session_reset_at",
		"module_meta",
	} {
		if _, ok := raw[key]; ok {
			t.Fatalf("expected %q to remain out of portal metadata JSON, got %s", key, string(data))
		}
	}
}

func TestPersistedPortalStateRoundTrip(t *testing.T) {
	orig := &PortalMetadata{
		AckReactionEmoji:       "👍",
		AckReactionRemoveAfter: true,
		PDFConfig:              &PDFConfig{Engine: "mistral"},
		Slug:                   "chat-7",
		TitleGenerated:         true,
		WelcomeSent:            true,
		AutoGreetingSent:       true,
		SessionResetAt:         123,
		AbortedLastRun:         true,
		CompactionCount:        9,
		SessionBootstrappedAt:  456,
		SessionBootstrapByAgent: map[string]int64{
			"beeper": 789,
		},
		ModuleMeta: map[string]any{
			"cron": map[string]any{"is_internal_room": true},
		},
		SubagentParentRoomID:  "!parent:example.com",
		DebounceMs:            250,
		TypingMode:            "thinking",
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

	if clone.AckReactionEmoji != "" || clone.AckReactionRemoveAfter || clone.PDFConfig != nil {
		t.Fatalf("expected only AI-owned portal state to round-trip: %#v", clone)
	}
	if clone.Slug != "" || !clone.TitleGenerated {
		t.Fatalf("expected only sidecar-owned portal state to round-trip: %#v", clone)
	}
	if clone.SessionBootstrapByAgent["beeper"] != 789 {
		t.Fatalf("expected bootstrap map to round-trip, got %#v", clone.SessionBootstrapByAgent)
	}
	if clone.ModuleMeta == nil || clone.ModuleMeta["cron"] == nil {
		t.Fatalf("expected module meta to round-trip, got %#v", clone.ModuleMeta)
	}
	if clone.TypingIntervalSeconds != nil || clone.TypingMode != "" || clone.DebounceMs != 0 {
		t.Fatalf("expected room config to stay out of sidecar round-trip, got %#v", clone)
	}
}

func ptrInt(v int) *int { return &v }
