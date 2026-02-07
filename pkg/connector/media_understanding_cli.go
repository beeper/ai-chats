package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func runMediaCLI(
	ctx context.Context,
	command string,
	args []string,
	prompt string,
	maxChars int,
	mediaPath string,
) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("missing cli command")
	}

	outputDir, err := os.MkdirTemp("", "ai-bridge-media-cli-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(outputDir)

	templCtx := map[string]string{
		"MediaPath":  mediaPath,
		"MediaDir":   filepath.Dir(mediaPath),
		"OutputDir":  outputDir,
		"OutputBase": strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath)),
		"Prompt":     prompt,
		"MaxChars":   fmt.Sprintf("%d", maxChars),
	}

	resolvedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		resolvedArgs = append(resolvedArgs, applyMediaTemplate(arg, templCtx))
	}

	cmd := exec.CommandContext(ctx, command, resolvedArgs...)
	out, err := cmd.CombinedOutput()
	stdout := strings.TrimSpace(string(out))
	if err != nil {
		if stdout != "" {
			return "", fmt.Errorf("media cli failed: %w: %s", err, stdout)
		}
		return "", fmt.Errorf("media cli failed: %w", err)
	}

	if resolved := resolveCLIOutput(command, resolvedArgs, stdout, mediaPath); resolved != "" {
		return resolved, nil
	}

	if stdout == "" {
		return "", nil
	}

	var payload map[string]any
	if json.Unmarshal([]byte(stdout), &payload) == nil {
		if value, ok := payload["response"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}

	return stdout, nil
}

func applyMediaTemplate(value string, ctx map[string]string) string {
	out := value
	for key, replacement := range ctx {
		out = strings.ReplaceAll(out, "{{"+key+"}}", replacement)
	}
	return out
}

func resolveCLIOutput(command string, args []string, stdout string, mediaPath string) string {
	base := strings.ToLower(filepath.Base(command))
	base = strings.TrimSuffix(base, ".exe")

	if base == "whisper" {
		if outputPath := resolveWhisperOutputPath(args, mediaPath); outputPath != "" {
			if content, err := os.ReadFile(outputPath); err == nil {
				if trimmed := strings.TrimSpace(string(content)); trimmed != "" {
					return trimmed
				}
			}
		}
	}

	if base == "whisper-cli" {
		if outputPath := resolveWhisperCPPOutputPath(args); outputPath != "" {
			if content, err := os.ReadFile(outputPath); err == nil {
				if trimmed := strings.TrimSpace(string(content)); trimmed != "" {
					return trimmed
				}
			}
		}
	}

	if base == "gemini" {
		if response := extractGeminiResponse(stdout); response != "" {
			return response
		}
	}

	if base == "sherpa-onnx-offline" {
		if response := extractSherpaOnnxText(stdout); response != "" {
			return response
		}
	}

	return strings.TrimSpace(stdout)
}

func resolveWhisperOutputPath(args []string, mediaPath string) string {
	outputDir := findArgValue(args, "--output_dir", "-o")
	outputFormat := findArgValue(args, "--output_format")
	if outputDir == "" || outputFormat == "" {
		return ""
	}
	formats := strings.Split(outputFormat, ",")
	hasTxt := false
	for _, format := range formats {
		if strings.TrimSpace(format) == "txt" {
			hasTxt = true
			break
		}
	}
	if !hasTxt {
		return ""
	}
	base := strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath))
	return filepath.Join(outputDir, base+".txt")
}

func resolveWhisperCPPOutputPath(args []string) string {
	if !hasArg(args, "-otxt", "--output-txt") {
		return ""
	}
	outputBase := findArgValue(args, "-of", "--output-file")
	if outputBase == "" {
		return ""
	}
	return outputBase + ".txt"
}

func findArgValue(args []string, keys ...string) string {
	for i, arg := range args {
		for _, key := range keys {
			if arg == key && i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return ""
}

func hasArg(args []string, keys ...string) bool {
	for _, arg := range args {
		for _, key := range keys {
			if arg == key {
				return true
			}
		}
	}
	return false
}

func extractGeminiResponse(raw string) string {
	payload := extractLastJSONObject(raw)
	if payload == nil {
		return ""
	}
	response, ok := payload["response"]
	if !ok {
		return ""
	}
	text, ok := response.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func extractSherpaOnnxText(raw string) string {
	tryParse := func(value string) string {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return ""
		}
		head := trimmed[0]
		if head != '{' && head != '"' {
			return ""
		}
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return ""
		}
		switch value := parsed.(type) {
		case string:
			return extractSherpaOnnxText(value)
		case map[string]any:
			if text, ok := value["text"].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
		return ""
	}

	if direct := tryParse(raw); direct != "" {
		return direct
	}
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if parsed := tryParse(lines[i]); parsed != "" {
			return parsed
		}
	}
	return ""
}

func extractLastJSONObject(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	start := strings.LastIndex(trimmed, "{")
	if start == -1 {
		return nil
	}
	slice := trimmed[start:]
	var payload map[string]any
	if err := json.Unmarshal([]byte(slice), &payload); err != nil {
		return nil
	}
	return payload
}
