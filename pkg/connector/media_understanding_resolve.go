package connector

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

func resolveMediaTimeoutSeconds(value int, cfg *MediaUnderstandingConfig, fallback int) time.Duration {
	seconds := value
	if seconds <= 0 && cfg != nil && cfg.TimeoutSeconds > 0 {
		seconds = cfg.TimeoutSeconds
	}
	if seconds <= 0 {
		seconds = fallback
	}
	if seconds <= 0 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

func resolveMediaPrompt(
	capability MediaUnderstandingCapability,
	entryPrompt string,
	cfg *MediaUnderstandingConfig,
	maxChars int,
) string {
	base := strings.TrimSpace(entryPrompt)
	if base == "" && cfg != nil {
		base = strings.TrimSpace(cfg.Prompt)
	}
	if base == "" {
		base = defaultPromptByCapability[capability]
	}
	if maxChars <= 0 || capability == MediaCapabilityAudio {
		return base
	}
	return base + " Respond in at most " + strconv.Itoa(maxChars) + " characters."
}

func resolveMediaMaxChars(capability MediaUnderstandingCapability, entry MediaUnderstandingModelConfig, cfg *MediaUnderstandingConfig) int {
	if entry.MaxChars > 0 {
		return entry.MaxChars
	}
	if cfg != nil && cfg.MaxChars > 0 {
		return cfg.MaxChars
	}
	return defaultMaxCharsByCapability[capability]
}

func resolveMediaMaxBytes(capability MediaUnderstandingCapability, entry MediaUnderstandingModelConfig, cfg *MediaUnderstandingConfig) int {
	if entry.MaxBytes > 0 {
		return entry.MaxBytes
	}
	if cfg != nil && cfg.MaxBytes > 0 {
		return cfg.MaxBytes
	}
	return defaultMaxBytesByCapability[capability]
}

func resolveMediaLanguage(entry MediaUnderstandingModelConfig, cfg *MediaUnderstandingConfig) string {
	if strings.TrimSpace(entry.Language) != "" {
		return strings.TrimSpace(entry.Language)
	}
	if cfg != nil && strings.TrimSpace(cfg.Language) != "" {
		return strings.TrimSpace(cfg.Language)
	}
	return ""
}

func resolveMediaEntries(cfg *MediaToolsConfig, capCfg *MediaUnderstandingConfig, capability MediaUnderstandingCapability) []MediaUnderstandingModelConfig {
	type entryWithSource struct {
		entry  MediaUnderstandingModelConfig
		source string
	}
	var entries []entryWithSource
	if capCfg != nil {
		for _, entry := range capCfg.Models {
			entries = append(entries, entryWithSource{entry: entry, source: "capability"})
		}
	}
	if cfg != nil {
		for _, entry := range cfg.Models {
			entries = append(entries, entryWithSource{entry: entry, source: "shared"})
		}
	}
	if len(entries) == 0 {
		return nil
	}

	filtered := make([]MediaUnderstandingModelConfig, 0, len(entries))
	for _, item := range entries {
		entry := item.entry
		if len(entry.Capabilities) > 0 {
			if !capabilityInList(capability, entry.Capabilities) {
				continue
			}
			filtered = append(filtered, entry)
			continue
		}
		if item.source == "shared" {
			provider := normalizeMediaProviderID(entry.Provider)
			if provider == "" {
				continue
			}
			if caps, ok := mediaProviderCapabilities[provider]; ok && capabilityInCapabilities(capability, caps) {
				filtered = append(filtered, entry)
			}
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func capabilityInList(capability MediaUnderstandingCapability, list []string) bool {
	for _, entry := range list {
		if strings.TrimSpace(strings.ToLower(entry)) == string(capability) {
			return true
		}
	}
	return false
}

func capabilityInCapabilities(capability MediaUnderstandingCapability, list []MediaUnderstandingCapability) bool {
	return slices.Contains(list, capability)
}
