package providers

import (
	"os"
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestParseDeploymentNameMap(t *testing.T) {
	mapped := ParseDeploymentNameMap("gpt-5=my-gpt5, claude-opus-4-6 = claude-deploy , invalid")
	if mapped["gpt-5"] != "my-gpt5" {
		t.Fatalf("expected gpt-5 mapping, got %#v", mapped)
	}
	if mapped["claude-opus-4-6"] != "claude-deploy" {
		t.Fatalf("expected claude mapping, got %#v", mapped)
	}
	if _, ok := mapped["invalid"]; ok {
		t.Fatalf("expected invalid entry to be ignored")
	}
}

func TestResolveAzureConfig(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_VERSION", "")
	t.Setenv("AZURE_OPENAI_BASE_URL", "")
	t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "")

	_, _, err := ResolveAzureConfig(ai.Model{}, nil)
	if err == nil {
		t.Fatalf("expected missing base url error")
	}

	baseURL, version, err := ResolveAzureConfig(ai.Model{
		BaseURL: "https://custom.openai.azure.com/openai/v1/",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error resolving from model base URL: %v", err)
	}
	if baseURL != "https://custom.openai.azure.com/openai/v1" {
		t.Fatalf("expected trimmed base URL, got %s", baseURL)
	}
	if version != "v1" {
		t.Fatalf("expected default api version v1, got %s", version)
	}

	t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "my-resource")
	baseURL, _, err = ResolveAzureConfig(ai.Model{}, nil)
	if err != nil {
		t.Fatalf("unexpected error resolving from resource name: %v", err)
	}
	if baseURL != "https://my-resource.openai.azure.com/openai/v1" {
		t.Fatalf("unexpected default resource base URL: %s", baseURL)
	}
}

func TestBuildAzureOpenAIResponsesParams(t *testing.T) {
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-5=my-deployment")

	temp := 0.5
	params := BuildAzureOpenAIResponsesParams(
		ai.Model{
			ID:        "gpt-5",
			Name:      "GPT-5",
			Provider:  "azure-openai-responses",
			API:       ai.APIAzureOpenAIResponse,
			Reasoning: true,
		},
		ai.Context{
			SystemPrompt: "system",
			Messages: []ai.Message{
				{Role: ai.RoleUser, Text: "hello"},
			},
		},
		AzureOpenAIResponsesOptions{
			OpenAIResponsesOptions: OpenAIResponsesOptions{
				StreamOptions: ai.StreamOptions{
					SessionID:   "session-id",
					Temperature: &temp,
					MaxTokens:   2048,
				},
				ReasoningEffort:  ai.ThinkingHigh,
				ReasoningSummary: "auto",
			},
		},
	)

	if params["model"] != "my-deployment" {
		t.Fatalf("expected deployment name from env map, got %#v", params["model"])
	}
	if params["prompt_cache_key"] != "session-id" {
		t.Fatalf("expected prompt cache key")
	}
	if params["max_output_tokens"] != 2048 {
		t.Fatalf("expected max output tokens 2048")
	}
	if params["temperature"] != 0.5 {
		t.Fatalf("expected temperature 0.5")
	}
	reasoning, ok := params["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("unexpected reasoning payload: %#v", params["reasoning"])
	}
	if _, ok := params["include"]; !ok {
		t.Fatalf("expected include reasoning encrypted content")
	}
}

func TestResolveDeploymentNamePriority(t *testing.T) {
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-5=map-deployment")

	model := ai.Model{ID: "gpt-5"}
	name := ResolveDeploymentName(model, &AzureOpenAIResponsesOptions{
		AzureDeploymentName: "direct-deployment",
	})
	if name != "direct-deployment" {
		t.Fatalf("expected direct deployment override, got %s", name)
	}

	os.Unsetenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP")
	name = ResolveDeploymentName(model, nil)
	if name != "gpt-5" {
		t.Fatalf("expected fallback to model id, got %s", name)
	}
}
