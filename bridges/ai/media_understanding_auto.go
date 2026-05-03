package ai

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

func (oc *AIClient) resolveAutoMediaEntries(
	capability MediaUnderstandingCapability,
	cfg *MediaUnderstandingConfig,
	meta *PortalMetadata,
) []MediaUnderstandingModelConfig {
	var headers map[string]string
	if cfg != nil {
		headers = cfg.Headers
	}
	hasProviderAuth := func(providerID string) bool {
		if hasProviderAuthHeader(providerID, headers) {
			return true
		}
		return strings.TrimSpace(oc.resolveMediaProviderAPIKey(providerID, "", "")) != ""
	}

	if oc != nil && meta != nil {
		responder := oc.responderForMeta(context.Background(), meta)
		if responder != nil && strings.TrimSpace(responder.ModelID) != "" {
			providerID, model := splitModelProvider(responder.ModelID)
			if providerID == "" {
				providerID = normalizeMediaProviderID(oc.responderProvider(responder))
			}
			if providerID != "" && providerSupportsCapability(providerID, capability) && hasProviderAuth(providerID) {
				return []MediaUnderstandingModelConfig{{
					Provider: providerID,
					Model:    model,
				}}
			}
		}
	}

	if capability == MediaCapabilityAudio {
		if local := resolveLocalAudioEntry(); local != nil {
			return []MediaUnderstandingModelConfig{*local}
		}
	}

	if gemini := resolveGeminiCliEntry(); gemini != nil {
		return []MediaUnderstandingModelConfig{*gemini}
	}

	switch capability {
	case MediaCapabilityImage:
		if hasProviderAuth("openrouter") {
			return []MediaUnderstandingModelConfig{{
				Provider: "openrouter",
				Model:    defaultOpenRouterGoogleModel,
			}}
		}
		if hasProviderAuth("openai") {
			return []MediaUnderstandingModelConfig{{
				Provider: "openai",
				Model:    defaultImageModelsByProvider["openai"],
			}}
		}
	case MediaCapabilityVideo:
		if hasProviderAuth("openrouter") {
			return []MediaUnderstandingModelConfig{{
				Provider: "openrouter",
				Model:    defaultOpenRouterGoogleModel,
			}}
		}
		if hasProviderAuth("google") {
			return []MediaUnderstandingModelConfig{{
				Provider: "google",
				Model:    defaultGoogleVideoModel,
			}}
		}
	case MediaCapabilityAudio:
		candidates := []struct {
			provider string
			model    string
		}{
			{"openai", defaultAudioModelsByProvider["openai"]},
			{"groq", defaultAudioModelsByProvider["groq"]},
			{"deepgram", defaultAudioModelsByProvider["deepgram"]},
			{"google", defaultGoogleAudioModel},
		}
		for _, candidate := range candidates {
			if hasProviderAuth(candidate.provider) {
				return []MediaUnderstandingModelConfig{{
					Provider: candidate.provider,
					Model:    candidate.model,
				}}
			}
		}
	}

	return nil
}

func providerSupportsCapability(providerID string, capability MediaUnderstandingCapability) bool {
	spec, ok := mediaProviderSpecFor(providerID)
	if !ok {
		return false
	}
	return slices.Contains(spec.capabilities, capability)
}

var hasBinaryCache sync.Map

func hasBinary(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if v, ok := hasBinaryCache.Load(name); ok {
		return v.(bool)
	}
	_, err := exec.LookPath(name)
	found := err == nil
	hasBinaryCache.Store(name, found)
	return found
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func resolveLocalWhisperCPPEntry() *MediaUnderstandingModelConfig {
	if !hasBinary("whisper-cli") {
		return nil
	}
	envModel := strings.TrimSpace(os.Getenv("WHISPER_CPP_MODEL"))
	defaultModel := "/opt/homebrew/share/whisper-cpp/for-tests-ggml-tiny.bin"
	modelPath := defaultModel
	if envModel != "" && fileExists(envModel) {
		modelPath = envModel
	}
	if !fileExists(modelPath) {
		return nil
	}
	return &MediaUnderstandingModelConfig{
		Type:    "cli",
		Command: "whisper-cli",
		Args:    []string{"-m", modelPath, "-otxt", "-of", "{{OutputBase}}", "-np", "-nt", "{{MediaPath}}"},
	}
}

func resolveLocalWhisperEntry() *MediaUnderstandingModelConfig {
	if !hasBinary("whisper") {
		return nil
	}
	return &MediaUnderstandingModelConfig{
		Type:    "cli",
		Command: "whisper",
		Args: []string{
			"--model",
			"turbo",
			"--output_format",
			"txt",
			"--output_dir",
			"{{OutputDir}}",
			"--verbose",
			"False",
			"{{MediaPath}}",
		},
	}
}

func resolveSherpaOnnxEntry() *MediaUnderstandingModelConfig {
	if !hasBinary("sherpa-onnx-offline") {
		return nil
	}
	modelDir := strings.TrimSpace(os.Getenv("SHERPA_ONNX_MODEL_DIR"))
	if modelDir == "" {
		return nil
	}
	tokens := filepath.Join(modelDir, "tokens.txt")
	encoder := filepath.Join(modelDir, "encoder.onnx")
	decoder := filepath.Join(modelDir, "decoder.onnx")
	joiner := filepath.Join(modelDir, "joiner.onnx")
	if !fileExists(tokens) || !fileExists(encoder) || !fileExists(decoder) || !fileExists(joiner) {
		return nil
	}
	return &MediaUnderstandingModelConfig{
		Type:    "cli",
		Command: "sherpa-onnx-offline",
		Args: []string{
			"--tokens=" + tokens,
			"--encoder=" + encoder,
			"--decoder=" + decoder,
			"--joiner=" + joiner,
			"{{MediaPath}}",
		},
	}
}

func resolveLocalAudioEntry() *MediaUnderstandingModelConfig {
	if entry := resolveSherpaOnnxEntry(); entry != nil {
		return entry
	}
	if entry := resolveLocalWhisperCPPEntry(); entry != nil {
		return entry
	}
	return resolveLocalWhisperEntry()
}

func resolveGeminiCliEntry() *MediaUnderstandingModelConfig {
	if !hasBinary("gemini") {
		return nil
	}
	return &MediaUnderstandingModelConfig{
		Type:    "cli",
		Command: "gemini",
		Args: []string{
			"--output-format",
			"json",
			"--allowed-tools",
			"read_many_files",
			"--include-directories",
			"{{MediaDir}}",
			"{{Prompt}}",
			"Use read_many_files to read {{MediaPath}} and respond with only the text output.",
		},
	}
}
