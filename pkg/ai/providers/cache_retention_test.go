package providers

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestCacheRetentionAnthropicAndOpenAIResponses(t *testing.T) {
	baseContext := ai.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages: []ai.Message{
			{Role: ai.RoleUser, Text: "Hello", Timestamp: 1},
		},
	}

	t.Run("anthropic default short has ephemeral cache no ttl", func(t *testing.T) {
		t.Setenv("PI_CACHE_RETENTION", "")
		model := ai.Model{
			ID:       "claude-3-5-haiku-20241022",
			API:      ai.APIAnthropicMessages,
			Provider: "anthropic",
			BaseURL:  "https://api.anthropic.com",
		}
		params := BuildAnthropicParams(model, baseContext, AnthropicOptions{})
		system := params["system"].([]map[string]any)
		cc := system[0]["cache_control"].(map[string]any)
		if cc["type"] != "ephemeral" {
			t.Fatalf("expected ephemeral cache control, got %#v", cc)
		}
		if _, ok := cc["ttl"]; ok {
			t.Fatalf("expected no ttl for default retention")
		}
	})

	t.Run("anthropic long retention adds 1h ttl for direct api", func(t *testing.T) {
		t.Setenv("PI_CACHE_RETENTION", "long")
		model := ai.Model{
			ID:       "claude-3-5-haiku-20241022",
			API:      ai.APIAnthropicMessages,
			Provider: "anthropic",
			BaseURL:  "https://api.anthropic.com",
		}
		params := BuildAnthropicParams(model, baseContext, AnthropicOptions{})
		system := params["system"].([]map[string]any)
		cc := system[0]["cache_control"].(map[string]any)
		if cc["ttl"] != "1h" {
			t.Fatalf("expected ttl=1h, got %#v", cc["ttl"])
		}
	})

	t.Run("anthropic long retention omits ttl on proxy base url", func(t *testing.T) {
		model := ai.Model{
			ID:       "claude-3-5-haiku-20241022",
			API:      ai.APIAnthropicMessages,
			Provider: "anthropic",
			BaseURL:  "https://my-proxy.example.com/v1",
		}
		params := BuildAnthropicParams(model, baseContext, AnthropicOptions{
			StreamOptions: ai.StreamOptions{
				CacheRetention: ai.CacheRetentionLong,
			},
		})
		system := params["system"].([]map[string]any)
		cc := system[0]["cache_control"].(map[string]any)
		if _, ok := cc["ttl"]; ok {
			t.Fatalf("expected ttl omitted for proxy base url")
		}
	})

	t.Run("anthropic cache retention none omits cache_control", func(t *testing.T) {
		model := ai.Model{
			ID:       "claude-3-5-haiku-20241022",
			API:      ai.APIAnthropicMessages,
			Provider: "anthropic",
			BaseURL:  "https://api.anthropic.com",
		}
		params := BuildAnthropicParams(model, baseContext, AnthropicOptions{
			StreamOptions: ai.StreamOptions{
				CacheRetention: ai.CacheRetentionNone,
			},
		})
		system := params["system"].([]map[string]any)
		if _, ok := system[0]["cache_control"]; ok {
			t.Fatalf("expected cache_control omitted for cacheRetention=none")
		}
	})

	t.Run("openai responses default has no prompt_cache_retention", func(t *testing.T) {
		t.Setenv("PI_CACHE_RETENTION", "")
		model := ai.Model{
			ID:       "gpt-4o-mini",
			API:      ai.APIOpenAIResponses,
			Provider: "openai",
			BaseURL:  "https://api.openai.com/v1",
		}
		params := BuildOpenAIResponsesParams(model, baseContext, OpenAIResponsesOptions{})
		if _, ok := params["prompt_cache_retention"]; ok {
			t.Fatalf("expected prompt_cache_retention omitted by default")
		}
	})

	t.Run("openai responses long sets retention and key", func(t *testing.T) {
		model := ai.Model{
			ID:       "gpt-4o-mini",
			API:      ai.APIOpenAIResponses,
			Provider: "openai",
			BaseURL:  "https://api.openai.com/v1",
		}
		params := BuildOpenAIResponsesParams(model, baseContext, OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{
				CacheRetention: ai.CacheRetentionLong,
				SessionID:      "session-2",
			},
		})
		if params["prompt_cache_key"] != "session-2" {
			t.Fatalf("expected prompt_cache_key=session-2, got %v", params["prompt_cache_key"])
		}
		if params["prompt_cache_retention"] != "24h" {
			t.Fatalf("expected prompt_cache_retention=24h, got %v", params["prompt_cache_retention"])
		}
	})

	t.Run("openai responses long proxy base omits prompt_cache_retention", func(t *testing.T) {
		model := ai.Model{
			ID:       "gpt-4o-mini",
			API:      ai.APIOpenAIResponses,
			Provider: "openai",
			BaseURL:  "https://my-proxy.example.com/v1",
		}
		params := BuildOpenAIResponsesParams(model, baseContext, OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{
				CacheRetention: ai.CacheRetentionLong,
				SessionID:      "session-2",
			},
		})
		if _, ok := params["prompt_cache_retention"]; ok {
			t.Fatalf("expected prompt_cache_retention omitted for proxy base URL")
		}
	})

	t.Run("openai responses cache retention none omits key and retention", func(t *testing.T) {
		model := ai.Model{
			ID:       "gpt-4o-mini",
			API:      ai.APIOpenAIResponses,
			Provider: "openai",
			BaseURL:  "https://api.openai.com/v1",
		}
		params := BuildOpenAIResponsesParams(model, baseContext, OpenAIResponsesOptions{
			StreamOptions: ai.StreamOptions{
				CacheRetention: ai.CacheRetentionNone,
				SessionID:      "session-1",
			},
		})
		if _, ok := params["prompt_cache_key"]; ok {
			t.Fatalf("expected prompt_cache_key omitted for cacheRetention=none")
		}
		if _, ok := params["prompt_cache_retention"]; ok {
			t.Fatalf("expected prompt_cache_retention omitted for cacheRetention=none")
		}
	})
}
