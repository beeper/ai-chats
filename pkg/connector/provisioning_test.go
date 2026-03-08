package connector

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
)

func strPtr(v string) *string {
	return &v
}

func TestApplyProfilePayloadSetsAndClearsFields(t *testing.T) {
	meta := &UserLoginMetadata{}
	err := applyProfilePayload(meta, profilePayload{
		Name:               strPtr(" Batuhan "),
		Occupation:         strPtr(" Product engineer "),
		AboutUser:          strPtr(" Works on AI tooling "),
		CustomInstructions: strPtr(" Be direct "),
		Timezone:           strPtr("Europe/Amsterdam"),
	})
	if err != nil {
		t.Fatalf("applyProfilePayload returned error: %v", err)
	}
	if meta.Profile == nil {
		t.Fatalf("expected profile to be initialized")
	}
	if meta.Profile.Name != "Batuhan" || meta.Profile.Occupation != "Product engineer" || meta.Profile.AboutUser != "Works on AI tooling" || meta.Profile.CustomInstructions != "Be direct" {
		t.Fatalf("unexpected profile contents: %+v", meta.Profile)
	}
	if meta.Timezone != "Europe/Amsterdam" {
		t.Fatalf("expected timezone to be stored, got %q", meta.Timezone)
	}

	err = applyProfilePayload(meta, profilePayload{
		Name:               strPtr(""),
		Occupation:         strPtr(""),
		AboutUser:          strPtr(""),
		CustomInstructions: strPtr(""),
		Timezone:           strPtr(""),
	})
	if err != nil {
		t.Fatalf("applyProfilePayload clear returned error: %v", err)
	}
	if meta.Profile != nil {
		t.Fatalf("expected empty profile to be cleared, got %+v", meta.Profile)
	}
	if meta.Timezone != "" {
		t.Fatalf("expected timezone to be cleared, got %q", meta.Timezone)
	}
}

func TestApplyProfilePayloadRejectsInvalidTimezone(t *testing.T) {
	meta := &UserLoginMetadata{}
	err := applyProfilePayload(meta, profilePayload{Timezone: strPtr("Mars/Olympus")})
	if err == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestNormalizeAgentUpsertRequestCreatesDefinition(t *testing.T) {
	agent, err := normalizeAgentUpsertRequest(agentUpsertRequest{
		Name:            "Helper",
		Description:     "Useful",
		Model:           "openai/gpt-5.2",
		ModelFallback:   []string{" anthropic/claude-sonnet-4.6 ", ""},
		SystemPrompt:    "Be useful",
		PromptMode:      "append",
		Tools:           &toolpolicy.ToolPolicyConfig{Allow: []string{"web_search"}},
		IdentityName:    "Beep",
		IdentityPersona: "Helpful assistant",
	}, "")
	if err != nil {
		t.Fatalf("normalizeAgentUpsertRequest returned error: %v", err)
	}
	if agent == nil {
		t.Fatal("expected agent definition")
	}
	if agent.ID == "" {
		t.Fatal("expected generated agent id")
	}
	if agent.Name != "Helper" {
		t.Fatalf("expected name Helper, got %q", agent.Name)
	}
	if agent.Model.Primary != "openai/gpt-5.2" {
		t.Fatalf("expected primary model to be preserved, got %q", agent.Model.Primary)
	}
	if len(agent.Model.Fallbacks) != 1 || agent.Model.Fallbacks[0] != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("unexpected fallback models: %#v", agent.Model.Fallbacks)
	}
	if agent.Tools == nil || len(agent.Tools.Allow) != 1 || agent.Tools.Allow[0] != "web_search" {
		t.Fatalf("expected tools policy to be preserved, got %#v", agent.Tools)
	}
}

func TestNormalizeMCPRequestValidatesAndNormalizes(t *testing.T) {
	name, cfg, err := normalizeMCPRequest(mcpServerUpsertRequest{
		Name:      " Search ",
		Transport: "streamable_http",
		Endpoint:  "https://example.com/mcp",
		AuthType:  "bearer",
		Token:     "secret",
	}, "")
	if err != nil {
		t.Fatalf("normalizeMCPRequest returned error: %v", err)
	}
	if name != "search" {
		t.Fatalf("expected normalized name 'search', got %q", name)
	}
	if cfg.Transport != mcpTransportStreamableHTTP {
		t.Fatalf("expected transport %q, got %q", mcpTransportStreamableHTTP, cfg.Transport)
	}
	if cfg.Endpoint != "https://example.com/mcp" {
		t.Fatalf("expected endpoint to be preserved, got %q", cfg.Endpoint)
	}
	if cfg.Token != "secret" {
		t.Fatalf("expected token to be preserved, got %q", cfg.Token)
	}
}

func TestNormalizeMCPRequestRejectsMissingTarget(t *testing.T) {
	_, _, err := normalizeMCPRequest(mcpServerUpsertRequest{Name: "search"}, "")
	if err == nil {
		t.Fatal("expected missing target error")
	}
}
