package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// modelConfig is the SINGLE SOURCE OF TRUTH for which models are available.
// Only model IDs and optional display name overrides are defined here.
// ALL capabilities are fetched from the OpenRouter API.
//
// Map format: model_id -> display_name (empty string = use API name)
var modelConfig = struct {
	// Models to fetch from OpenRouter - ID -> display name override (empty = use API name)
	Models map[string]string
	// Aliases for stable references
	Aliases map[string]string
}{
	Models: map[string]string{
		// MiniMax
		"minimax/minimax-m2.1": "MiniMax M2.1",
		"minimax/minimax-m2":   "MiniMax M2",

		// GLM (Z.AI) - supports reasoning via parameter
		"z-ai/glm-4.7":  "GLM 4.7",
		"z-ai/glm-4.6v": "GLM 4.6V",

		// Kimi (Moonshot)
		"moonshotai/kimi-k2.5":        "Kimi K2.5",
		"moonshotai/kimi-k2-0905":     "Kimi K2 (0905)",
		"moonshotai/kimi-k2-thinking": "Kimi K2 (Thinking)",

		// Qwen
		"qwen/qwen3-235b-a22b-thinking-2507": "Qwen 3 235B (Thinking)",
		"qwen/qwen3-235b-a22b":               "Qwen 3 235B",

		// Grok (xAI)
		"x-ai/grok-4.1-fast": "Grok 4.1 Fast",

		// DeepSeek
		"deepseek/deepseek-v3.2": "DeepSeek v3.2",

		// Llama (Meta)
		"meta-llama/llama-4-scout":    "Llama 4 Scout",
		"meta-llama/llama-4-maverick": "Llama 4 Maverick",

		// Gemini (Google) via OpenRouter
		"google/gemini-2.5-flash-image":     "Nano Banana",
		"google/gemini-3-flash-preview":     "Gemini 3 Flash",
		"google/gemini-3-pro-image-preview": "Nano Banana Pro",
		"google/gemini-3-pro-preview":       "Gemini 3 Pro",

		// Claude (Anthropic) via OpenRouter
		"anthropic/claude-sonnet-4.5": "Claude Sonnet 4.5",
		"anthropic/claude-opus-4.5":   "Claude Opus 4.5",
		"anthropic/claude-opus-4.6":   "Claude Opus 4.6",
		"anthropic/claude-haiku-4.5":  "Claude Haiku 4.5",

		// OpenAI models via OpenRouter
		"openai/gpt-4.1":           "GPT-4.1",
		"openai/gpt-5":             "GPT-5",
		"openai/gpt-5.2":           "GPT-5.2",
		"openai/gpt-5.2-pro":       "GPT-5.2 Pro",
		"openai/gpt-5-mini":        "GPT-5 Mini",
		"openai/gpt-5-nano":        "GPT-5 Nano",
		"openai/o1":                "O1",
		"openai/o3-mini":           "O3 Mini",
		"openai/gpt-4o":            "GPT-4o",
		"openai/gpt-4o-mini":       "GPT-4o Mini",
		"openai/chatgpt-4o-latest": "ChatGPT-4o",
	},
	Aliases: map[string]string{
		// Default alias
		"beeper/default": "anthropic/claude-opus-4.6",

		// Stable aliases that can be remapped
		"beeper/fast":      "openai/gpt-5-mini",
		"beeper/smart":     "openai/gpt-5.2",
		"beeper/reasoning": "openai/gpt-5.2", // Uses reasoning effort parameter
	},
}

// OpenRouterArchitecture contains model architecture information
type OpenRouterArchitecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Modality         string   `json:"modality"`
	Tokenizer        string   `json:"tokenizer"`
	InstructType     string   `json:"instruct_type"`
}

// OpenRouterPricing contains model pricing information
type OpenRouterPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
	WebSearch  string `json:"web_search"`
}

// OpenRouterTopProvider contains top provider information
type OpenRouterTopProvider struct {
	MaxCompletionTokens int  `json:"max_completion_tokens"`
	IsModerated         bool `json:"is_moderated"`
}

// OpenRouterModel represents a model from OpenRouter API with full capability fields
type OpenRouterModel struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description"`
	ContextLength       int                    `json:"context_length"`
	Architecture        OpenRouterArchitecture `json:"architecture"`
	Pricing             OpenRouterPricing      `json:"pricing"`
	TopProvider         OpenRouterTopProvider  `json:"top_provider"`
	SupportedParameters []string               `json:"supported_parameters"`
}

// OpenRouterResponse represents the API response
type OpenRouterResponse struct {
	Data []OpenRouterModel `json:"data"`
}

// ModelCapabilities holds auto-detected capabilities for a model
type ModelCapabilities struct {
	Vision          bool
	ToolCalling     bool
	Reasoning       bool
	WebSearch       bool
	ImageGen        bool
	Audio           bool
	Video           bool
	PDF             bool
	ContextWindow   int
	MaxOutputTokens int
}

func main() {
	token := flag.String("openrouter-token", "", "OpenRouter API token")
	outputFile := flag.String("output", "pkg/connector/models_generated.go", "Output Go file")
	jsonFile := flag.String("json", "pkg/connector/beeper_models.json", "Output JSON file for clients")
	flag.Parse()

	if *token == "" {
		fmt.Fprintln(os.Stderr, "Error: --openrouter-token is required")
		os.Exit(1)
	}

	models, err := fetchOpenRouterModels(*token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching models: %v\n", err)
		os.Exit(1)
	}

	if err := generateGoFile(models, *outputFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated %s with %d models\n", *outputFile, len(modelConfig.Models))

	if err := generateJSONFile(models, *jsonFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating JSON file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated %s\n", *jsonFile)
}

func fetchOpenRouterModels(token string) (map[string]OpenRouterModel, error) {
	req, err := http.NewRequest(http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var apiResp OpenRouterResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	result := make(map[string]OpenRouterModel)
	for _, model := range apiResp.Data {
		result[model.ID] = model
	}

	return result, nil
}

// detectCapabilities auto-detects all model capabilities from OpenRouter API response
func detectCapabilities(modelID string, apiModel OpenRouterModel, hasAPIData bool) ModelCapabilities {
	if !hasAPIData {
		// Fallback: can't detect capabilities without API data
		fmt.Fprintf(os.Stderr, "Warning: No API data for model %s, using defaults\n", modelID)
		return ModelCapabilities{ToolCalling: true}
	}

	caps := ModelCapabilities{}

	// Vision: "image" in architecture.input_modalities
	caps.Vision = slices.Contains(apiModel.Architecture.InputModalities, "image")
	// Legacy fallback: check modality field
	if !caps.Vision && strings.Contains(apiModel.Architecture.Modality, "image") {
		caps.Vision = true
	}

	// Image Generation: "image" in architecture.output_modalities
	caps.ImageGen = slices.Contains(apiModel.Architecture.OutputModalities, "image")

	// Audio: "audio" in architecture.input_modalities
	caps.Audio = slices.Contains(apiModel.Architecture.InputModalities, "audio")

	// Video: "video" in architecture.input_modalities
	caps.Video = slices.Contains(apiModel.Architecture.InputModalities, "video")

	// PDF: "file" in architecture.input_modalities (OpenRouter uses "file" for document support)
	// Also check for specific PDF-capable models
	caps.PDF = slices.Contains(apiModel.Architecture.InputModalities, "file") ||
		strings.Contains(modelID, "claude") || // Claude models support PDFs
		strings.Contains(modelID, "gemini") // Gemini models support PDFs

	// Tool Calling: "tools" in supported_parameters
	caps.ToolCalling = slices.Contains(apiModel.SupportedParameters, "tools")

	// Reasoning: "reasoning" in supported_parameters
	caps.Reasoning = slices.Contains(apiModel.SupportedParameters, "reasoning")

	// Web Search: pricing.web_search != "" OR "web_search_options" in supported_parameters
	caps.WebSearch = apiModel.Pricing.WebSearch != "" ||
		slices.Contains(apiModel.SupportedParameters, "web_search_options")

	// Context window and max output tokens from API
	caps.ContextWindow = apiModel.ContextLength
	caps.MaxOutputTokens = apiModel.TopProvider.MaxCompletionTokens

	return caps
}

// availableToolsGo returns the Go code representation of available tools
func availableToolsGo(caps ModelCapabilities) string {
	if caps.WebSearch && caps.ToolCalling {
		return "[]string{ToolWebSearch, ToolFunctionCalling}"
	} else if caps.ToolCalling {
		return "[]string{ToolFunctionCalling}"
	} else if caps.WebSearch {
		return "[]string{ToolWebSearch}"
	}
	return "[]string{}"
}

// availableToolsJSON returns the JSON representation of available tools
func availableToolsJSON(caps ModelCapabilities) []string {
	var tools []string
	if caps.WebSearch {
		tools = append(tools, "web_search")
	}
	if caps.ToolCalling {
		tools = append(tools, "function_calling")
	}
	return tools
}

func resolveModelAPIForManifest(modelID string, provider string) string {
	if provider == "openai" || strings.HasPrefix(modelID, "openai/") {
		return "openai-responses"
	}
	return "openai-completions"
}

func generateGoFile(apiModels map[string]OpenRouterModel, outputPath string) error {
	var buf strings.Builder

	buf.WriteString(`// Code generated by generate-models. DO NOT EDIT.
// Generated at: ` + time.Now().UTC().Format(time.RFC3339) + `

package connector

// ModelManifest contains all model definitions and aliases.
// Models are fetched from OpenRouter API, aliases are defined in the generator config.
var ModelManifest = struct {
	Models  map[string]ModelInfo
	Aliases map[string]string
}{
	Models: map[string]ModelInfo{
`)

	// Get sorted model IDs for deterministic output
	modelIDs := slices.Sorted(maps.Keys(modelConfig.Models))

	for _, modelID := range modelIDs {
		displayName := modelConfig.Models[modelID]
		apiModel, hasAPIData := apiModels[modelID]
		// Fallback to API name if display name override is empty
		if displayName == "" && hasAPIData {
			displayName = apiModel.Name
		}
		caps := detectCapabilities(modelID, apiModel, hasAPIData)
		apiLabel := resolveModelAPIForManifest(modelID, "openrouter")

		buf.WriteString(fmt.Sprintf(`		%q: {
			ID:                  %q,
			Name:                %q,
			Provider:            "openrouter",
			API:                 %q,
			SupportsVision:      %t,
			SupportsToolCalling: %t,
			SupportsReasoning:   %t,
			SupportsWebSearch:   %t,
			SupportsImageGen:    %t,
			SupportsAudio:       %t,
			SupportsVideo:       %t,
			SupportsPDF:         %t,
			ContextWindow:       %d,
			MaxOutputTokens:     %d,
			AvailableTools:      %s,
		},
`,
			modelID,
			modelID,
			displayName,
			apiLabel,
			caps.Vision,
			caps.ToolCalling,
			caps.Reasoning,
			caps.WebSearch,
			caps.ImageGen,
			caps.Audio,
			caps.Video,
			caps.PDF,
			caps.ContextWindow,
			caps.MaxOutputTokens,
			availableToolsGo(caps),
		))
	}

	buf.WriteString(`	},
	Aliases: map[string]string{
`)

	// Add aliases
	aliasKeys := slices.Sorted(maps.Keys(modelConfig.Aliases))
	for _, alias := range aliasKeys {
		target := modelConfig.Aliases[alias]
		buf.WriteString(fmt.Sprintf(`		%q: %q,
`, alias, target))
	}

	buf.WriteString(`	},
}
`)

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("failed to format generated code: %w", err)
	}
	return os.WriteFile(outputPath, formatted, 0644)
}

// JSONModelInfo mirrors the connector.ModelInfo struct for JSON output
type JSONModelInfo struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Provider            string   `json:"provider"`
	API                 string   `json:"api,omitempty"`
	Description         string   `json:"description,omitempty"`
	SupportsVision      bool     `json:"supports_vision"`
	SupportsToolCalling bool     `json:"supports_tool_calling"`
	SupportsReasoning   bool     `json:"supports_reasoning"`
	SupportsWebSearch   bool     `json:"supports_web_search"`
	SupportsImageGen    bool     `json:"supports_image_gen,omitempty"`
	SupportsAudio       bool     `json:"supports_audio,omitempty"`
	SupportsVideo       bool     `json:"supports_video,omitempty"`
	SupportsPDF         bool     `json:"supports_pdf,omitempty"`
	ContextWindow       int      `json:"context_window,omitempty"`
	MaxOutputTokens     int      `json:"max_output_tokens,omitempty"`
	AvailableTools      []string `json:"available_tools,omitempty"`
}

// JSONManifest is the full manifest structure for JSON output
type JSONManifest struct {
	Models  []JSONModelInfo   `json:"models"`
	Aliases map[string]string `json:"aliases"`
}

func generateJSONFile(apiModels map[string]OpenRouterModel, outputPath string) error {
	var models []JSONModelInfo

	// Add OpenRouter models
	modelIDs := slices.Sorted(maps.Keys(modelConfig.Models))
	for _, modelID := range modelIDs {
		displayName := modelConfig.Models[modelID]
		apiModel, hasAPIData := apiModels[modelID]
		// Fallback to API name if display name override is empty
		if displayName == "" && hasAPIData {
			displayName = apiModel.Name
		}
		caps := detectCapabilities(modelID, apiModel, hasAPIData)
		apiLabel := resolveModelAPIForManifest(modelID, "openrouter")

		models = append(models, JSONModelInfo{
			ID:                  modelID,
			Name:                displayName,
			Provider:            "openrouter",
			API:                 apiLabel,
			SupportsVision:      caps.Vision,
			SupportsToolCalling: caps.ToolCalling,
			SupportsReasoning:   caps.Reasoning,
			SupportsWebSearch:   caps.WebSearch,
			SupportsImageGen:    caps.ImageGen,
			SupportsAudio:       caps.Audio,
			SupportsVideo:       caps.Video,
			SupportsPDF:         caps.PDF,
			ContextWindow:       caps.ContextWindow,
			MaxOutputTokens:     caps.MaxOutputTokens,
			AvailableTools:      availableToolsJSON(caps),
		})
	}

	manifest := JSONManifest{
		Models:  models,
		Aliases: modelConfig.Aliases,
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n') // Add trailing newline

	return os.WriteFile(outputPath, data, 0644)
}
