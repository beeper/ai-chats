package ai

import "os"

func GetEnvAPIKey(provider string) string {
	switch provider {
	case "github-copilot":
		if v := os.Getenv("COPILOT_GITHUB_TOKEN"); v != "" {
			return v
		}
		if v := os.Getenv("GH_TOKEN"); v != "" {
			return v
		}
		return os.Getenv("GITHUB_TOKEN")
	case "anthropic":
		if v := os.Getenv("ANTHROPIC_OAUTH_TOKEN"); v != "" {
			return v
		}
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "azure-openai-responses":
		return os.Getenv("AZURE_OPENAI_API_KEY")
	case "google":
		return os.Getenv("GEMINI_API_KEY")
	case "groq":
		return os.Getenv("GROQ_API_KEY")
	case "cerebras":
		return os.Getenv("CEREBRAS_API_KEY")
	case "xai":
		return os.Getenv("XAI_API_KEY")
	case "openrouter":
		return os.Getenv("OPENROUTER_API_KEY")
	case "vercel-ai-gateway":
		return os.Getenv("AI_GATEWAY_API_KEY")
	case "zai":
		return os.Getenv("ZAI_API_KEY")
	case "mistral":
		return os.Getenv("MISTRAL_API_KEY")
	case "minimax":
		return os.Getenv("MINIMAX_API_KEY")
	case "minimax-cn":
		return os.Getenv("MINIMAX_CN_API_KEY")
	case "huggingface":
		return os.Getenv("HF_TOKEN")
	case "opencode", "opencode-go":
		return os.Getenv("OPENCODE_API_KEY")
	case "kimi-coding":
		return os.Getenv("KIMI_API_KEY")
	default:
		return ""
	}
}
