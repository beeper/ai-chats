package ai

import (
	"context"
	"encoding/json"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func newResponderMetadataTestClient(t *testing.T) *AIClient {
	client := newCatalogTestClient(t)
	setTestLoginState(client, &loginRuntimeState{
		ModelCache: &ModelCache{
			Models: []ModelInfo{
				{
					ID:                  "openai/gpt-5",
					Name:                "GPT-5",
					ContextWindow:       400000,
					SupportsVision:      true,
					SupportsReasoning:   true,
					SupportsPDF:         true,
					SupportsToolCalling: true,
				},
				{
					ID:                  "openai/gpt-5-mini",
					Name:                "GPT-5 Mini",
					ContextWindow:       128000,
					SupportsVision:      true,
					SupportsToolCalling: true,
				},
			},
		}})
	return client
}

func decodeExtraProfileValue[T any](t *testing.T, extra database.ExtraProfile, key string) T {
	t.Helper()
	raw, ok := extra[key]
	if !ok {
		t.Fatalf("expected extra profile key %q", key)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("failed to decode extra profile key %q: %v", key, err)
	}
	return out
}

func TestModelContactResponseIncludesResponderMetadata(t *testing.T) {
	oc := newResponderMetadataTestClient(t)
	resp := oc.modelContactResponse(context.Background(), &ModelInfo{
		ID:                  "openai/gpt-5",
		Name:                "GPT-5",
		ContextWindow:       400000,
		SupportsVision:      true,
		SupportsReasoning:   true,
		SupportsPDF:         true,
		SupportsToolCalling: true,
	})
	if resp == nil || resp.UserInfo == nil {
		t.Fatalf("expected contact response with user info, got %#v", resp)
	}
	if got := decodeExtraProfileValue[string](t, resp.UserInfo.ExtraProfile, "com.beeper.ai.model_id"); got != "openai/gpt-5" {
		t.Fatalf("unexpected model id %q", got)
	}
	if got := decodeExtraProfileValue[int](t, resp.UserInfo.ExtraProfile, "com.beeper.ai.context_limit"); got != 400000 {
		t.Fatalf("unexpected context limit %d", got)
	}
	caps := decodeExtraProfileValue[ModelCapabilities](t, resp.UserInfo.ExtraProfile, "com.beeper.ai.capabilities")
	if !caps.SupportsVision || !caps.SupportsReasoning || !caps.SupportsPDF || !caps.SupportsToolCalling {
		t.Fatalf("unexpected capabilities %#v", caps)
	}
}

func TestApplyAgentChatInfoIncludesResponderMetadata(t *testing.T) {
	oc := newResponderMetadataTestClient(t)
	chatInfo := &bridgev2.ChatInfo{
		Members: &bridgev2.ChatMemberList{
			MemberMap: bridgev2.ChatMemberMap{
				humanUserID(oc.UserLogin.ID): {},
			},
		},
	}

	oc.applyAgentChatInfo(context.Background(), chatInfo, "custom-agent", "Custom Agent", "openai/gpt-5-mini")

	agentGhostID := networkid.UserID(agentUserIDForLogin(oc.UserLogin.ID, "custom-agent"))
	member := chatInfo.Members.MemberMap[agentGhostID]
	if member.UserInfo == nil {
		t.Fatal("expected agent member user info")
	}
	if got := member.MemberEventExtra["com.beeper.ai.model_id"]; got != "openai/gpt-5-mini" {
		t.Fatalf("unexpected member model id %#v", got)
	}
	if got := member.MemberEventExtra["com.beeper.ai.agent"]; got != "custom-agent" {
		t.Fatalf("unexpected member agent %#v", got)
	}
	if got := member.MemberEventExtra["com.beeper.ai.context_limit"]; got != 128000 {
		t.Fatalf("unexpected member context limit %#v", got)
	}
	caps, ok := member.MemberEventExtra["com.beeper.ai.capabilities"].(ModelCapabilities)
	if !ok {
		t.Fatalf("expected capabilities payload, got %#v", member.MemberEventExtra["com.beeper.ai.capabilities"])
	}
	if !caps.SupportsVision || !caps.SupportsToolCalling {
		t.Fatalf("unexpected capabilities %#v", caps)
	}
}
