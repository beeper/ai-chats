package ai

import (
	"encoding/json"
	"testing"

	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
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

func TestPortalMetadataMarshalsPersistentPortalState(t *testing.T) {
	meta := &PortalMetadata{
		AckReactionEmoji:       "👍",
		AckReactionRemoveAfter: true,
		PDFConfig:              &PDFConfig{Engine: "mistral"},
		Slug:                   "chat-1",
		WelcomeSent:            true,
		AutoGreetingSent:       true,
		SessionResetAt:         123,
		InternalRoomKind:       "cron",
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
		"welcome_sent",
		"auto_greeting_sent",
		"session_reset_at",
		"internal_room_kind",
	} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("expected %q to be persisted in portal metadata, got %s", key, string(data))
		}
	}
}

func TestPortalMetadataJSONRoundTrip(t *testing.T) {
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
		SessionBootstrapByAgent: map[string]int64{
			"beeper": 789,
		},
		InternalRoomKind:               "cron",
		CompactionLastPromptTokens:     5000,
		CompactionLastCompletionTokens: 1200,
		MemoryModuleState: &integrationruntime.MemoryState{
			CompactionInFlight:           true,
			LastCompactionAt:             111,
			LastCompactionDroppedCount:   4,
			LastCompactionError:          "boom",
			LastCompactionRefreshAt:      222,
			OverflowFlushAt:              333,
			OverflowFlushCompactionCount: 9,
			MemoryBootstrapAt:            444,
		},
		SubagentParentRoomID:  "!parent:example.com",
		DebounceMs:            250,
		TypingMode:            "thinking",
		TypingIntervalSeconds: ptrInt(15),
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var restored PortalMetadata
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if restored.Slug != "chat-7" || !restored.TitleGenerated || !restored.WelcomeSent {
		t.Fatalf("expected portal metadata to round-trip, got %#v", restored)
	}
	if restored.SessionBootstrapByAgent["beeper"] != 789 {
		t.Fatalf("expected bootstrap map to round-trip, got %#v", restored.SessionBootstrapByAgent)
	}
	if restored.InternalRoomKind != "cron" {
		t.Fatalf("expected internal room kind to round-trip, got %#v", restored)
	}
	if restored.CompactionLastPromptTokens != 5000 || restored.CompactionLastCompletionTokens != 1200 {
		t.Fatalf("expected compaction usage to round-trip, got %#v", restored)
	}
	if restored.MemoryModuleState == nil || !restored.MemoryModuleState.CompactionInFlight || restored.MemoryModuleState.MemoryBootstrapAt != 444 || restored.MemoryModuleState.OverflowFlushCompactionCount != 9 {
		t.Fatalf("expected memory state to round-trip, got %#v", restored.MemoryModuleState)
	}
	if restored.TypingIntervalSeconds == nil || *restored.TypingIntervalSeconds != 15 || restored.TypingMode != "thinking" || restored.DebounceMs != 250 {
		t.Fatalf("expected room config to round-trip, got %#v", restored)
	}
}

func ptrInt(v int) *int { return &v }
