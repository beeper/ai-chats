package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3/option"
	"github.com/rs/zerolog"
)

// MakePDFPluginMiddleware creates middleware that injects the file-parser plugin for PDFs.
// The defaultEngine parameter is used as a fallback when no per-request engine is set in context.
// To set a per-request engine, use WithPDFEngine() to add it to the request context.
func MakePDFPluginMiddleware(defaultEngine string) option.Middleware {
	// Validate default engine, default to mistral-ocr
	switch defaultEngine {
	case "pdf-text", "mistral-ocr", "native":
		// valid
	default:
		defaultEngine = "mistral-ocr"
	}

	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		// Only modify POST requests with JSON body (API calls)
		if req.Method != http.MethodPost || req.Body == nil {
			return next(req)
		}
		// Only apply PDF plugin to Responses requests.
		isResponses := strings.Contains(req.URL.Path, "/responses")
		if !isResponses {
			return next(req)
		}

		// Check context for per-request engine override
		engine := GetPDFEngineFromContext(req.Context())
		if engine == "" {
			engine = defaultEngine
		}
		// Validate per-request engine
		switch engine {
		case "pdf-text", "mistral-ocr", "native":
			// valid
		default:
			engine = defaultEngine
		}

		contentType := req.Header.Get("Content-Type")
		if !strings.Contains(contentType, "application/json") {
			return next(req)
		}

		// Read the existing body
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return next(req)
		}
		req.Body.Close()

		// Parse as JSON
		var body map[string]any
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			// Not valid JSON, pass through
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return next(req)
		}

		hasPDF := func() bool {
			hasPDFFile := func(fileData any) bool {
				data, ok := fileData.(string)
				return ok && strings.Contains(data, "application/pdf")
			}
			hasPDFInParts := func(parts []any) bool {
				for _, part := range parts {
					partMap, ok := part.(map[string]any)
					if !ok {
						continue
					}
					partType, _ := partMap["type"].(string)
					switch partType {
					case "file":
						if fileObj, ok := partMap["file"].(map[string]any); ok {
							if hasPDFFile(fileObj["file_data"]) {
								return true
							}
						}
					case "input_file":
						if fileObj, ok := partMap["input_file"].(map[string]any); ok {
							if hasPDFFile(fileObj["file_data"]) {
								return true
							}
						}
					}
				}
				return false
			}
			// Chat Completions: messages[].content[]
			if messages, ok := body["messages"].([]any); ok {
				for _, msg := range messages {
					msgMap, ok := msg.(map[string]any)
					if !ok {
						continue
					}
					content, ok := msgMap["content"].([]any)
					if ok && hasPDFInParts(content) {
						return true
					}
				}
			}
			// Responses: input[] with type=message content[]
			if inputItems, ok := body["input"].([]any); ok {
				for _, item := range inputItems {
					itemMap, ok := item.(map[string]any)
					if !ok {
						continue
					}
					itemType, _ := itemMap["type"].(string)
					if itemType != "message" {
						continue
					}
					content, ok := itemMap["content"].([]any)
					if ok && hasPDFInParts(content) {
						return true
					}
				}
			}
			return false
		}()

		if !hasPDF {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return next(req)
		}

		// Add plugins array with file-parser plugin
		plugins := []map[string]any{
			{
				"id": "file-parser",
				"pdf": map[string]any{
					"engine": engine,
				},
			},
		}

		// Merge with existing plugins if any
		if existingPlugins, ok := body["plugins"].([]any); ok {
			for _, p := range existingPlugins {
				if pMap, ok := p.(map[string]any); ok {
					plugins = append(plugins, pMap)
				}
			}
		}
		body["plugins"] = plugins

		// Re-encode
		newBody, err := json.Marshal(body)
		if err != nil {
			// Encoding failed, use original
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return next(req)
		}

		req.Body = io.NopCloser(bytes.NewReader(newBody))
		req.ContentLength = int64(len(newBody))
		req.Header.Set("Content-Length", fmt.Sprintf("%d", len(newBody)))

		return next(req)
	}
}

// MakeToolDedupMiddleware removes duplicate tool names from outbound Responses requests.
func MakeToolDedupMiddleware(log zerolog.Logger) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if req.Method != http.MethodPost || req.Body == nil {
			return next(req)
		}
		if !strings.Contains(req.URL.Path, "/responses") {
			return next(req)
		}
		if !strings.Contains(req.Header.Get("Content-Type"), "application/json") {
			return next(req)
		}

		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return next(req)
		}
		req.Body.Close()

		var body map[string]any
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return next(req)
		}

		toolsRaw, ok := body["tools"].([]any)
		if !ok || len(toolsRaw) == 0 {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return next(req)
		}

		var toolNames []string
		for _, tool := range toolsRaw {
			toolMap, ok := tool.(map[string]any)
			if !ok {
				continue
			}
			toolType, _ := toolMap["type"].(string)
			if toolType == "function" {
				if name, ok := toolMap["name"].(string); ok && name != "" {
					toolNames = append(toolNames, name)
					continue
				}
			}
			if toolType != "" {
				toolNames = append(toolNames, toolType)
			}
		}
		if len(toolNames) > 0 {
			slices.Sort(toolNames)
			log.Debug().Int("tool_count", len(toolsRaw)).Strs("tools", toolNames).Msg("Outgoing tools payload")
		}

		seen := make(map[string]int, len(toolsRaw))
		deduped := make([]any, 0, len(toolsRaw))
		for _, tool := range toolsRaw {
			toolMap, ok := tool.(map[string]any)
			if !ok {
				deduped = append(deduped, tool)
				continue
			}
			toolType, _ := toolMap["type"].(string)
			key := ""
			if toolType == "function" {
				if name, ok := toolMap["name"].(string); ok && name != "" {
					key = "function:" + name
				}
			} else if toolType != "" {
				key = "type:" + toolType
			}
			if key == "" {
				deduped = append(deduped, tool)
				continue
			}
			seen[key]++
			if seen[key] == 1 {
				deduped = append(deduped, tool)
			}
		}

		if len(deduped) != len(toolsRaw) {
			var dupes []string
			for key, count := range seen {
				if count > 1 {
					name := strings.TrimPrefix(key, "function:")
					name = strings.TrimPrefix(name, "type:")
					dupes = append(dupes, fmt.Sprintf("%s(%d)", name, count))
				}
			}
			slices.Sort(dupes)
			log.Warn().Strs("dupes", dupes).Msg("Deduped tool names in request payload")

			body["tools"] = deduped
			if newBody, err := json.Marshal(body); err == nil {
				bodyBytes = newBody
			}
		}

		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
		req.Header.Set("Content-Length", fmt.Sprintf("%d", len(bodyBytes)))

		resp, err := next(req)
		if err != nil || resp == nil || resp.Body == nil {
			return resp, err
		}

		if resp.StatusCode >= http.StatusBadRequest {
			respBytes, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				resp.Body = io.NopCloser(bytes.NewReader(respBytes))
				return resp, err
			}
			resp.Body = io.NopCloser(bytes.NewReader(respBytes))

			if bytes.Contains(respBytes, []byte("tools: Tool names must be unique")) {
				log.Warn().
					Str("request_json", string(bodyBytes)).
					Str("response_json", string(respBytes)).
					Msg("Responses request rejected: duplicate tools")
			}
		}

		return resp, err
	}
}
