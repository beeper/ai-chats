package openclawconv

import (
	"regexp"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/jsonutil"
)

var (
	validAgentIDRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	invalidAgentIDRe = regexp.MustCompile(`[^a-z0-9_-]+`)
)

func AgentIDFromSessionKey(sessionKey string) string {
	parts := strings.Split(strings.TrimSpace(sessionKey), ":")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "agent") {
		return ""
	}
	agentID := strings.TrimSpace(parts[1])
	if agentID == "" {
		return ""
	}
	if validAgentIDRe.MatchString(agentID) {
		return strings.ToLower(agentID)
	}
	normalized := strings.ToLower(agentID)
	normalized = invalidAgentIDRe.ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, "-")
	if len(normalized) > 64 {
		normalized = normalized[:64]
	}
	return normalized
}

func ContentBlocks(message map[string]any) []map[string]any {
	raw := message["content"]
	switch typed := raw.(type) {
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if block, ok := item.(map[string]any); ok {
				out = append(out, block)
			}
		}
		return out
	case []map[string]any:
		return typed
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		return []map[string]any{{"type": "text", "text": text}}
	default:
		return nil
	}
}

func ExtractMessageText(message map[string]any) string {
	if message == nil {
		return ""
	}
	if text := strings.TrimSpace(StringValue(message["text"])); text != "" {
		return text
	}
	var parts []string
	for _, block := range ContentBlocks(message) {
		switch strings.ToLower(strings.TrimSpace(StringValue(block["type"]))) {
		case "text", "input_text", "output_text":
			if text := strings.TrimSpace(StringsTrimDefault(StringValue(block["text"]), StringValue(block["content"]))); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func ExtractAttachmentBlocks(message map[string]any) []map[string]any {
	blocks := ContentBlocks(message)
	out := make([]map[string]any, 0)
	for _, block := range blocks {
		if !IsAttachmentBlock(block) {
			continue
		}
		out = append(out, block)
	}
	return out
}

func IsAttachmentBlock(block map[string]any) bool {
	blockType := strings.ToLower(strings.TrimSpace(StringValue(block["type"])))
	switch blockType {
	case "", "text", "input_text", "output_text", "toolcall", "tooluse", "functioncall", "source-url", "source_document", "source-document", "reasoning":
		return false
	case "input_image", "input_file", "image", "file", "audio", "video":
		return true
	}
	if len(jsonutil.ToMap(block["source"])) > 0 {
		return true
	}
	for _, key := range []string{"file", "image_url", "imageUrl", "asset", "blob", "src"} {
		value := block[key]
		if strings.TrimSpace(StringValue(value)) != "" {
			return true
		}
		if len(jsonutil.ToMap(value)) > 0 {
			return true
		}
	}
	if strings.TrimSpace(StringValue(block["url"])) != "" || strings.TrimSpace(StringValue(block["href"])) != "" {
		return true
	}
	if strings.TrimSpace(StringValue(block["content"])) != "" || strings.TrimSpace(StringValue(block["data"])) != "" {
		return true
	}
	if strings.TrimSpace(StringValue(block["fileName"])) != "" || strings.TrimSpace(StringValue(block["filename"])) != "" {
		if strings.TrimSpace(StringValue(block["mimeType"])) != "" || strings.TrimSpace(StringValue(block["mediaType"])) != "" || strings.TrimSpace(StringValue(block["contentType"])) != "" {
			return true
		}
	}
	return false
}

func StringValue(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case interface{ String() string }:
		return typed.String()
	default:
		return ""
	}
}

func StringsTrimDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
