package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/event/cmdschema"
)

func TestCommandDescriptionEventType_ParsesRawStateContent(t *testing.T) {
	parsed := &cmdschema.EventContent{
		Command:     "config",
		Description: event.MakeExtensibleText("Show current chat configuration"),
		Parameters: []*cmdschema.Parameter{{
			Key:         "model",
			Schema:      cmdschema.PrimitiveTypeString.Schema(),
			Optional:    true,
			Description: event.MakeExtensibleText("[model]"),
		}},
	}
	if err := parsed.Validate(); err != nil {
		t.Fatalf("validate command description: %v", err)
	}

	raw, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("marshal raw content: %v", err)
	}

	content := &event.Content{VeryRaw: raw}
	if err = json.Unmarshal(raw, &content.Raw); err != nil {
		t.Fatalf("unmarshal raw content: %v", err)
	}
	if err = content.ParseRaw(CommandDescriptionEventType); err != nil {
		t.Fatalf("parse raw command description: %v", err)
	}
}

func TestBuildCommandDescriptionContent_ValidMSC4391(t *testing.T) {
	handler := &commands.FullHandler{
		Name: "cron",
		Help: commands.HelpMeta{
			Description: "Inspect/manage scheduled jobs",
			Args:        "[status|list|add|update|run|remove] ...",
		},
	}

	content := buildCommandDescriptionContent(handler)
	if err := content.Validate(); err != nil {
		t.Fatalf("validate built command description: %v", err)
	}
	if content.Command != "cron" {
		t.Fatalf("unexpected command: %q", content.Command)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal command description: %v", err)
	}
	serialized := string(raw)
	if !strings.Contains(serialized, "Inspect/manage scheduled jobs") {
		t.Fatalf("expected updated description in %s", serialized)
	}
	if !strings.Contains(serialized, "add|update") {
		t.Fatalf("expected expanded args in %s", serialized)
	}
	if content.TailParam != "args" {
		t.Fatalf("expected tail param args, got %q", content.TailParam)
	}
	if len(content.Parameters) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(content.Parameters))
	}
	if content.Parameters[0].Key != "status" {
		t.Fatalf("expected first parameter key status, got %q", content.Parameters[0].Key)
	}
	if content.Parameters[1].Key != "args" {
		t.Fatalf("expected second parameter key args, got %q", content.Parameters[1].Key)
	}
}
