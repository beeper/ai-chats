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
		"anthropic/claude-haiku-4.5":    "Claude Haiku 4.5",
		"anthropic/claude-opus-4.6":     "Claude Opus 4.6",
		"anthropic/claude-sonnet-4.6":   "Claude Sonnet 4.6",
		"deepseek/deepseek-r1-0528":     "DeepSeek R1 (0528)",
		"deepseek/deepseek-v3.2":        "DeepSeek v3.2",
		"google/gemini-2.5-flash-lite":  "Gemini 2.5 Flash Lite",
		"google/gemini-2.5-pro":         "Gemini 2.5 Pro",
		"google/gemini-3-flash-preview": "Gemini 3 Flash",
		"meta-llama/llama-4-maverick":   "Llama 4 Maverick",
		"minimax/minimax-m2.7":          "MiniMax M2.7",
		"mistralai/devstral-2512":       "Devstral 2",
		"mistralai/mistral-small-2603":  "Mistral Small 4",
		"moonshotai/kimi-k2.5":          "Kimi K2.5",
		"openai/gpt-5-mini":             "GPT-5 mini",
		"openai/gpt-5.2":                "GPT-5.2",
		"openai/gpt-5.3-codex":          "GPT-5.3 Codex",
		"openai/gpt-5.4":                "GPT-5.4",
		"openai/gpt-5.4-mini":           "GPT-5.4 Mini",
		"openai/o3":                     "o3",
		"openai/o4-mini":                "o4-mini",
		"qwen/qwen2.5-vl-32b-instruct":  "Qwen 2.5 32B",
		"qwen/qwen3-coder-next":         "Qwen 3 Coder Next",
		"qwen/qwen3.5-flash-02-23":      "Qwen 3.5 Flash",
		"qwen/qwen3.5-plus-02-15":       "Qwen 3.5 Plus",
		"x-ai/grok-4.1-fast":            "Grok 4.1 Fast",
		"x-ai/grok-4.20-beta":           "Grok 4.20 Beta",
		"x-ai/grok-code-fast-1":         "Grok Code Fast 1",
		"z-ai/glm-5-turbo":              "GLM 5 Turbo",
	},
	Aliases: map[string]string{
		// Default alias
		"beeper/default": "anthropic/claude-opus-4.6",

		// Stable aliases that can be remapped
		"beeper/fast":      "openai/gpt-5.4-mini",
		"beeper/smart":     "openai/gpt-5.4",
		"beeper/reasoning": "openai/o3",
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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	token := flag.String("openrouter-token", "", "Optional OpenRouter API token")
	outputFile := flag.String("output", "bridges/ai/beeper_models_generated.go", "Output Go file")
	jsonFile := flag.String("json", "pkg/ai/beeper_models.json", "Output JSON file for clients")
	flag.Parse()

	models, err := fetchOpenRouterModels(*token)
	if err != nil {
		return fmt.Errorf("fetching models: %w", err)
	}

	if err := generateGoFile(models, *outputFile); err != nil {
		return fmt.Errorf("generating Go file: %w", err)
	}
	fmt.Printf("Generated %s with %d models\n", *outputFile, len(modelConfig.Models))

	if err := generateJSONFile(models, *jsonFile); err != nil {
		return fmt.Errorf("generating JSON file: %w", err)
	}
	fmt.Printf("Generated %s\n", *jsonFile)
	return nil
}

func fetchOpenRouterModels(token string) (map[string]OpenRouterModel, error) {
	req, err := http.NewRequest(http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

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

	// Vision: "image" in architecture.input_modalities (or legacy modality field)
	caps.Vision = slices.Contains(apiModel.Architecture.InputModalities, "image") ||
		strings.Contains(apiModel.Architecture.Modality, "image")

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
	switch {
	case caps.WebSearch && caps.ToolCalling:
		return "[]string{ToolWebSearch, ToolFunctionCalling}"
	case caps.ToolCalling:
		return "[]string{ToolFunctionCalling}"
	case caps.WebSearch:
		return "[]string{ToolWebSearch}"
	default:
		return "[]string{}"
	}
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

func resolveModelAPIForManifest(modelID string) string {
	if strings.HasPrefix(modelID, "openai/") {
		return "openai-responses"
	}
	return "openai-completions"
}

// resolvedModel holds the resolved display name, capabilities, and API label
// for a single model entry. Both Go and JSON generators use this to avoid
// duplicating the resolution logic.
type resolvedModel struct {
	ID          string
	DisplayName string
	API         string
	Caps        ModelCapabilities
}

// resolveAllModels iterates the model config, resolves display names and
// capabilities from the API data, and returns them in sorted order.
func resolveAllModels(apiModels map[string]OpenRouterModel) []resolvedModel {
	modelIDs := slices.Sorted(maps.Keys(modelConfig.Models))
	resolved := make([]resolvedModel, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		displayName := modelConfig.Models[modelID]
		apiModel, hasAPIData := apiModels[modelID]
		if displayName == "" && hasAPIData {
			displayName = apiModel.Name
		}
		resolved = append(resolved, resolvedModel{
			ID:          modelID,
			DisplayName: displayName,
			API:         resolveModelAPIForManifest(modelID),
			Caps:        detectCapabilities(modelID, apiModel, hasAPIData),
		})
	}
	return resolved
}

func generateGoFile(apiModels map[string]OpenRouterModel, outputPath string) error {
	var buf strings.Builder

	buf.WriteString(`// Code generated by generate-models. DO NOT EDIT.
// Generated at: ` + time.Now().UTC().Format(time.RFC3339) + `

package ai

// ModelManifest contains all model definitions and aliases.
// Models are fetched from OpenRouter API, aliases are defined in the generator config.
var ModelManifest = struct {
	Models  map[string]ModelInfo
	Aliases map[string]string
}{
	Models: map[string]ModelInfo{
`)

	for _, m := range resolveAllModels(apiModels) {
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
			m.ID, m.ID, m.DisplayName, m.API,
			m.Caps.Vision, m.Caps.ToolCalling, m.Caps.Reasoning, m.Caps.WebSearch,
			m.Caps.ImageGen, m.Caps.Audio, m.Caps.Video, m.Caps.PDF,
			m.Caps.ContextWindow, m.Caps.MaxOutputTokens,
			availableToolsGo(m.Caps),
		))
	}

	buf.WriteString(`	},
	Aliases: map[string]string{
`)

	aliasKeys := slices.Sorted(maps.Keys(modelConfig.Aliases))
	for _, alias := range aliasKeys {
		fmt.Fprintf(&buf, "\t\t%q: %q,\n", alias, modelConfig.Aliases[alias])
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

// JSONModelInfo mirrors the ai.ModelInfo struct for JSON output.
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

// JSONManifest is the full manifest structure for JSON output.
type JSONManifest struct {
	Models  []JSONModelInfo   `json:"models"`
	Aliases map[string]string `json:"aliases"`
}

func generateJSONFile(apiModels map[string]OpenRouterModel, outputPath string) error {
	resolved := resolveAllModels(apiModels)
	models := make([]JSONModelInfo, 0, len(resolved))
	for _, m := range resolved {
		models = append(models, JSONModelInfo{
			ID:                  m.ID,
			Name:                m.DisplayName,
			Provider:            "openrouter",
			API:                 m.API,
			SupportsVision:      m.Caps.Vision,
			SupportsToolCalling: m.Caps.ToolCalling,
			SupportsReasoning:   m.Caps.Reasoning,
			SupportsWebSearch:   m.Caps.WebSearch,
			SupportsImageGen:    m.Caps.ImageGen,
			SupportsAudio:       m.Caps.Audio,
			SupportsVideo:       m.Caps.Video,
			SupportsPDF:         m.Caps.PDF,
			ContextWindow:       m.Caps.ContextWindow,
			MaxOutputTokens:     m.Caps.MaxOutputTokens,
			AvailableTools:      availableToolsJSON(m.Caps),
		})
	}

	data, err := json.MarshalIndent(JSONManifest{Models: models, Aliases: modelConfig.Aliases}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(outputPath, data, 0644)
}
