package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/rs/zerolog"
	"go.mau.fi/util/random"

	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

// OpenAIProvider implements AIProvider for OpenAI's API
type OpenAIProvider struct {
	client  openai.Client
	log     zerolog.Logger
	baseURL string
}

// pdfEngineContextKey is the context key for per-request PDF engine override
type pdfEngineContextKey struct{}

// GetPDFEngineFromContext retrieves the PDF engine override from context
func GetPDFEngineFromContext(ctx context.Context) string {
	return contextValue[string](ctx, pdfEngineContextKey{})
}

// WithPDFEngine adds a PDF engine override to the context
func WithPDFEngine(ctx context.Context, engine string) context.Context {
	return context.WithValue(ctx, pdfEngineContextKey{}, engine)
}

// NewOpenAIProviderWithBaseURL creates an OpenAI provider with custom base URL
// Used for OpenRouter, Beeper proxy, or custom endpoints
func NewOpenAIProviderWithBaseURL(apiKey, baseURL string, log zerolog.Logger) (*OpenAIProvider, error) {
	return NewOpenAIProviderWithUserID(apiKey, baseURL, "", log)
}

// NewOpenAIProviderWithUserID creates an OpenAI provider that passes user_id with each request.
// Used for Beeper proxy to ensure correct rate limiting and feature flags per user.
func NewOpenAIProviderWithUserID(apiKey, baseURL, userID string, log zerolog.Logger) (*OpenAIProvider, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}

	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	opts = appendUserIDOption(opts, userID)
	opts = append(opts, option.WithMiddleware(makeRequestTraceMiddleware(log)))

	client := openai.NewClient(opts...)

	return &OpenAIProvider{
		client:  client,
		log:     log.With().Str("provider", "openai").Logger(),
		baseURL: baseURL,
	}, nil
}

func newOutboundRequestID() string {
	return "abr_" + random.String(12)
}

func appendUserIDOption(opts []option.RequestOption, userID string) []option.RequestOption {
	if userID == "" {
		return opts
	}
	return append(opts, option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		q := req.URL.Query()
		q.Set("user_id", userID)
		req.URL.RawQuery = q.Encode()
		return next(req)
	}))
}

func makeRequestTraceMiddleware(log zerolog.Logger) option.Middleware {
	traceLog := log.With().Str("component", "openai_http").Logger()
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		start := time.Now()
		requestID := strings.TrimSpace(req.Header.Get("x-request-id"))
		if requestID == "" {
			requestID = newOutboundRequestID()
			req.Header.Set("x-request-id", requestID)
		}

		reqMethod := req.Method
		reqHost := ""
		reqPath := ""
		if req.URL != nil {
			reqHost = req.URL.Host
			reqPath = req.URL.Path
		}

		traceLog.Debug().
			Str("request_id", requestID).
			Str("request_method", reqMethod).
			Str("request_host", reqHost).
			Str("request_path", reqPath).
			Msg("Dispatching provider HTTP request")

		resp, err := next(req)
		elapsedMs := time.Since(start).Milliseconds()
		if err != nil {
			traceLog.Error().
				Err(err).
				Str("request_id", requestID).
				Str("request_method", reqMethod).
				Str("request_host", reqHost).
				Str("request_path", reqPath).
				Int64("duration_ms", elapsedMs).
				Msg("Provider HTTP request failed")
			return nil, err
		}

		upstreamRequestID := strings.TrimSpace(resp.Header.Get("x-request-id"))
		if upstreamRequestID == "" {
			upstreamRequestID = strings.TrimSpace(resp.Header.Get("x-openai-request-id"))
		}

		event := traceLog.Debug().
			Str("request_id", requestID).
			Str("request_method", reqMethod).
			Str("request_host", reqHost).
			Str("request_path", reqPath).
			Int("status_code", resp.StatusCode).
			Int64("duration_ms", elapsedMs)

		if upstreamRequestID != "" {
			event = event.Str("upstream_request_id", upstreamRequestID)
		}
		if cfRay := strings.TrimSpace(resp.Header.Get("cf-ray")); cfRay != "" {
			event = event.Str("cf_ray", cfRay)
		}
		if server := strings.TrimSpace(resp.Header.Get("server")); server != "" {
			event = event.Str("response_server", server)
		}

		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			event.Msg("Provider HTTP response error")
		} else {
			event.Msg("Provider HTTP response")
		}
		return resp, nil
	}
}

// NewOpenAIProviderWithPDFPlugin creates an OpenAI provider with PDF plugin middleware.
// Used for OpenRouter/Beeper to enable universal PDF support via file-parser plugin.
func NewOpenAIProviderWithPDFPlugin(apiKey, baseURL, userID, pdfEngine string, headers map[string]string, log zerolog.Logger) (*OpenAIProvider, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}

	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	opts = appendUserIDOption(opts, userID)

	opts = httputil.AppendHeaderOptions(opts, headers)

	// Add PDF plugin middleware
	opts = append(opts, option.WithMiddleware(MakePDFPluginMiddleware(pdfEngine)))
	// Deduplicate tools in the final request payload (OpenRouter/Anthropic requires unique names)
	opts = append(opts, option.WithMiddleware(MakeToolDedupMiddleware(log)))
	opts = append(opts, option.WithMiddleware(makeRequestTraceMiddleware(log)))

	client := openai.NewClient(opts...)

	return &OpenAIProvider{
		client:  client,
		log:     log.With().Str("provider", "openai").Str("pdf_engine", pdfEngine).Logger(),
		baseURL: baseURL,
	}, nil
}

func (o *OpenAIProvider) Name() string {
	return "openai"
}

// Client returns the underlying OpenAI client for direct access
// Used by the bridge for advanced features like Responses API
func (o *OpenAIProvider) Client() openai.Client {
	return o.client
}

// ListModels returns available OpenAI models
func (o *OpenAIProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	// Try to list models from API
	page, err := o.client.Models.List(ctx)
	if err != nil {
		// Fallback to known models
		return defaultOpenAIModels(), nil
	}

	var models []ModelInfo
	for page != nil {
		for _, model := range page.Data {
			// Filter to only relevant models
			if !strings.HasPrefix(model.ID, "gpt-") &&
				!strings.HasPrefix(model.ID, "o1") &&
				!strings.HasPrefix(model.ID, "o3") &&
				!strings.HasPrefix(model.ID, "chatgpt") {
				continue
			}

			fullModelID := AddModelPrefix(BackendOpenAI, model.ID)
			models = append(models, ModelInfo{
				ID:                  fullModelID,
				Name:                GetModelDisplayName(fullModelID),
				Provider:            "openai",
				API:                 "openai-responses",
				SupportsVision:      strings.Contains(model.ID, "vision") || strings.Contains(model.ID, "4o") || strings.Contains(model.ID, "4-turbo"),
				SupportsToolCalling: true,
				SupportsReasoning:   strings.HasPrefix(model.ID, "o1") || strings.HasPrefix(model.ID, "o3"),
			})
		}

		// Get next page
		page, err = page.GetNextPage()
		if err != nil {
			break
		}
	}

	if len(models) == 0 {
		return defaultOpenAIModels(), nil
	}

	return models, nil
}

// defaultOpenAIModels returns an empty list (model catalog is provided via VFS).
func defaultOpenAIModels() []ModelInfo {
	return nil
}

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
		// Only apply PDF plugin to Responses or Chat Completions requests.
		isResponses := strings.Contains(req.URL.Path, "/responses")
		isChatCompletions := strings.Contains(req.URL.Path, "/chat/completions")
		if !isResponses && !isChatCompletions {
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

// ToOpenAITools converts tool definitions to OpenAI Responses API format
func ToOpenAITools(tools []ToolDefinition, strictMode ToolStrictMode, log *zerolog.Logger) []responses.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}

	var result []responses.ToolUnionParam
	for _, tool := range tools {
		schema := tool.Parameters
		var stripped []string
		if schema != nil {
			schema, stripped = sanitizeToolSchemaWithReport(schema)
			logSchemaSanitization(log, tool.Name, stripped)
		}
		strict := shouldUseStrictMode(strictMode, schema)
		toolParam := responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:       tool.Name,
				Parameters: schema,
				Strict:     param.NewOpt(strict),
				Type:       constant.ValueOf[constant.Function](),
			},
		}

		// Add description if available (SDK helper doesn't support this directly)
		if tool.Description != "" {
			toolParam.OfFunction.Description = openai.String(tool.Description)
		}

		result = append(result, toolParam)
	}

	return result
}

// ToOpenAIChatTools converts tool definitions to OpenAI Chat Completions tool format.
func ToOpenAIChatTools(tools []ToolDefinition, log *zerolog.Logger) []openai.ChatCompletionToolUnionParam {
	if len(tools) == 0 {
		return nil
	}

	var result []openai.ChatCompletionToolUnionParam
	for _, tool := range tools {
		schema := tool.Parameters
		var stripped []string
		if schema != nil {
			schema, stripped = sanitizeToolSchemaWithReport(schema)
			logSchemaSanitization(log, tool.Name, stripped)
		}
		function := openai.FunctionDefinitionParam{
			Name:       tool.Name,
			Parameters: schema,
		}
		if tool.Description != "" {
			function.Description = openai.String(tool.Description)
		}

		result = append(result, openai.ChatCompletionToolUnionParam{
			OfFunction: &openai.ChatCompletionFunctionToolParam{
				Function: function,
				Type:     constant.ValueOf[constant.Function](),
			},
		})
	}

	return result
}

// dedupeToolParams removes tools with duplicate identifiers to satisfy providers
// like Anthropic that reject duplicated tool names.
func dedupeToolParams(tools []responses.ToolUnionParam) []responses.ToolUnionParam {
	seen := make(map[string]struct{}, len(tools))
	var result []responses.ToolUnionParam
	for _, t := range tools {
		key := ""
		switch {
		case t.OfFunction != nil:
			key = "function:" + t.OfFunction.Name
		case t.OfWebSearch != nil:
			key = "web_search"
		default:
			key = fmt.Sprintf("%v", t) // fallback, should rarely hit
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, t)
	}
	return result
}

func logToolParamDuplicates(log *zerolog.Logger, tools []responses.ToolUnionParam) {
	if log == nil || len(tools) == 0 {
		return
	}

	counts := make(map[string]int, len(tools))
	for _, t := range tools {
		name := ""
		switch {
		case t.OfFunction != nil:
			name = t.OfFunction.Name
		case t.OfWebSearch != nil:
			name = "web_search"
		}
		if name == "" {
			continue
		}
		counts[name]++
	}

	var dupes []string
	for name, count := range counts {
		if count > 1 {
			dupes = append(dupes, fmt.Sprintf("%s(%d)", name, count))
		}
	}
	if len(dupes) > 0 {
		slices.Sort(dupes)
		log.Warn().Strs("dupes", dupes).Msg("Duplicate tool names detected for request")
	}
}

// dedupeChatToolParams removes tools with duplicate identifiers in Chat Completions.
func dedupeChatToolParams(tools []openai.ChatCompletionToolUnionParam) []openai.ChatCompletionToolUnionParam {
	seen := make(map[string]struct{}, len(tools))
	var result []openai.ChatCompletionToolUnionParam
	for _, t := range tools {
		key := ""
		switch {
		case t.OfFunction != nil:
			key = "function:" + t.OfFunction.Function.Name
		case t.OfCustom != nil:
			key = "custom:" + t.OfCustom.Custom.Name
		default:
			key = fmt.Sprintf("%v", t)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, t)
	}
	return result
}

func isOpenRouterBaseURL(baseURL string) bool {
	if baseURL == "" {
		return false
	}
	lowered := strings.ToLower(baseURL)
	return strings.Contains(lowered, "openrouter") || strings.Contains(lowered, "/openrouter/")
}
