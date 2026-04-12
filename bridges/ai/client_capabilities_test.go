package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestGetCapabilities_ModelRoomRejectsReplyThreadAndEdit(t *testing.T) {
	oc := newTestAIClientWithProvider("")
	oc.connector = &OpenAIConnector{}
	setTestLoginState(oc, &loginRuntimeState{
		ModelCache: &ModelCache{Models: []ModelInfo{{ID: "openai/gpt-5", SupportsToolCalling: true}}},
	})
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			OtherUserID: modelUserID("openai/gpt-5"),
			Metadata:    modelModeTestMeta("openai/gpt-5"),
		},
	}

	caps := oc.GetCapabilities(context.Background(), portal)
	if caps.Reply != event.CapLevelRejected {
		t.Fatalf("expected reply rejected in model room, got %v", caps.Reply)
	}
	if caps.Thread != event.CapLevelRejected {
		t.Fatalf("expected thread rejected in model room, got %v", caps.Thread)
	}
	if caps.Edit != event.CapLevelRejected {
		t.Fatalf("expected edit rejected in model room, got %v", caps.Edit)
	}
	if caps.Reaction != event.CapLevelFullySupported {
		t.Fatalf("expected reaction fully supported in model room, got %v", caps.Reaction)
	}

	raw, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("failed to marshal room features: %v", err)
	}
	rawJSON := string(raw)
	if !strings.Contains(rawJSON, `"reaction":2`) {
		t.Fatalf("expected serialized room features to contain reaction=2, got: %s", rawJSON)
	}
	if !strings.Contains(rawJSON, `"reply":-2`) {
		t.Fatalf("expected serialized room features to contain reply=-2, got: %s", rawJSON)
	}
	if !strings.Contains(rawJSON, `"thread":-2`) {
		t.Fatalf("expected serialized room features to contain thread=-2, got: %s", rawJSON)
	}
	if !strings.Contains(rawJSON, `"edit":-2`) {
		t.Fatalf("expected serialized room features to contain edit=-2, got: %s", rawJSON)
	}
}

func TestGetCapabilities_AgentRoomEnablesReplyEditReaction(t *testing.T) {
	oc := newTestAIClientWithProvider("")
	oc.connector = &OpenAIConnector{}
	setTestLoginState(oc, &loginRuntimeState{
		ModelCache: &ModelCache{Models: []ModelInfo{{ID: DefaultModelOpenRouter, SupportsToolCalling: true}}},
	})
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			OtherUserID: agentUserID("beeper"),
			Metadata:    agentModeTestMeta("beeper"),
		},
	}

	caps := oc.GetCapabilities(context.Background(), portal)
	if caps.Reply != event.CapLevelFullySupported {
		t.Fatalf("expected reply fully supported, got %v", caps.Reply)
	}
	if caps.Edit != event.CapLevelFullySupported {
		t.Fatalf("expected edit fully supported, got %v", caps.Edit)
	}
	if caps.Reaction != event.CapLevelFullySupported {
		t.Fatalf("expected reaction fully supported, got %v", caps.Reaction)
	}
}

func TestGetCapabilities_MessageToolDisabledDisablesReplyEditReaction(t *testing.T) {
	oc := &AIClient{connector: &OpenAIConnector{}}
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			OtherUserID: agentUserID("beeper"),
			Metadata: &PortalMetadata{
				ResolvedTarget: agentModeTestMeta("beeper").ResolvedTarget,
				DisabledTools: []string{
					ToolNameMessage,
				},
			},
		},
	}

	caps := oc.GetCapabilities(context.Background(), portal)
	if caps.Reply != event.CapLevelRejected {
		t.Fatalf("expected reply rejected when message tool is disabled, got %v", caps.Reply)
	}
	if caps.Edit != event.CapLevelRejected {
		t.Fatalf("expected edit rejected when message tool is disabled, got %v", caps.Edit)
	}
	if caps.Reaction != event.CapLevelRejected {
		t.Fatalf("expected reaction rejected when message tool is disabled, got %v", caps.Reaction)
	}
}

func TestConnectorCapabilitiesEnableContactListProvisioning(t *testing.T) {
	conn := NewAIConnector()
	caps := conn.GetCapabilities()
	if caps == nil {
		t.Fatal("expected capabilities")
	}
	if !caps.Provisioning.ResolveIdentifier.ContactList {
		t.Fatal("expected contact list provisioning to be enabled")
	}
}
