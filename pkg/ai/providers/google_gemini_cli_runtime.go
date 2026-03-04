package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/oauth"
	"github.com/beeper/ai-bridge/pkg/ai/utils"
)

const (
	defaultGeminiCLIEndpoint      = "https://cloudcode-pa.googleapis.com"
	antigravityDailyEndpoint      = "https://daily-cloudcode-pa.sandbox.googleapis.com"
	defaultAntigravityVersion     = "1.18.3"
	maxGeminiCLIRetries           = 3
	baseGeminiCLIRetryDelay       = 1000 * time.Millisecond
	geminiCLIScannerBufferMaxSize = 16 * 1024 * 1024
)

var antigravityEndpointFallbacks = []string{antigravityDailyEndpoint, defaultGeminiCLIEndpoint}

const antigravitySystemInstruction = "You are Antigravity, a powerful agentic AI coding assistant designed by the Google Deepmind team working on Advanced Agentic Coding." +
	"You are pair programming with a USER to solve their coding task. The task may require creating a new codebase, modifying or debugging an existing codebase, or simply answering a question." +
	"**Absolute paths only**" +
	"**Proactiveness**"

type googleGeminiCLIOptions struct {
	StreamOptions ai.StreamOptions
	ToolChoice    string
	Thinking      *GoogleThinkingOptions
	ProjectID     string
}

type cloudCodeAssistResponseChunk struct {
	Response *struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text             string `json:"text,omitempty"`
					Thought          bool   `json:"thought,omitempty"`
					ThoughtSignature string `json:"thoughtSignature,omitempty"`
					FunctionCall     *struct {
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
						ID   string         `json:"id,omitempty"`
					} `json:"functionCall,omitempty"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason,omitempty"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount        int `json:"promptTokenCount,omitempty"`
			CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
			ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
			TotalTokenCount         int `json:"totalTokenCount,omitempty"`
			CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
		} `json:"usageMetadata,omitempty"`
	} `json:"response,omitempty"`
}

func streamGoogleGeminiCLI(model ai.Model, c ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	geminiOptions := googleGeminiCLIOptions{}
	if options != nil {
		geminiOptions.StreamOptions = *options
	}
	return streamGoogleGeminiCLIWithOptions(model, c, geminiOptions)
}

func streamSimpleGoogleGeminiCLI(model ai.Model, c ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	base := BuildBaseOptions(model, options, "")
	if options == nil || options.Reasoning == "" {
		return streamGoogleGeminiCLIWithOptions(model, c, googleGeminiCLIOptions{
			StreamOptions: base,
			Thinking:      &GoogleThinkingOptions{Enabled: false},
		})
	}

	effort := ClampReasoning(options.Reasoning)
	if isGemini3Model(model.ID) {
		return streamGoogleGeminiCLIWithOptions(model, c, googleGeminiCLIOptions{
			StreamOptions: base,
			Thinking: &GoogleThinkingOptions{
				Enabled: true,
				Level:   getGeminiCLIThinkingLevel(effort, model.ID),
			},
		})
	}

	maxTokens, thinkingBudget := AdjustMaxTokensForThinking(
		base.MaxTokens,
		model.MaxTokens,
		effort,
		options.ThinkingBudgets,
	)
	base.MaxTokens = maxTokens
	return streamGoogleGeminiCLIWithOptions(model, c, googleGeminiCLIOptions{
		StreamOptions: base,
		Thinking: &GoogleThinkingOptions{
			Enabled:      true,
			BudgetTokens: &thinkingBudget,
		},
	})
}

func streamGoogleGeminiCLIWithOptions(
	model ai.Model,
	c ai.Context,
	options googleGeminiCLIOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(128)
	go func() {
		runCtx := options.StreamOptions.Ctx
		if runCtx == nil {
			runCtx = context.Background()
		}

		apiKeyRaw := strings.TrimSpace(options.StreamOptions.APIKey)
		if apiKeyRaw == "" {
			pushProviderError(stream, model, "google cloud code assist requires OAuth authentication")
			return
		}
		accessToken, projectID, ok := oauth.ParseGoogleOAuthAPIKey(apiKeyRaw)
		if !ok {
			pushProviderError(stream, model, "invalid google cloud credentials, re-authentication required")
			return
		}
		if strings.TrimSpace(options.ProjectID) != "" {
			projectID = strings.TrimSpace(options.ProjectID)
		}

		isAntigravity := strings.EqualFold(string(model.Provider), "google-antigravity")
		endpoints := geminiCLIEndpoints(model, isAntigravity)
		requestBody := BuildGoogleGeminiCLIRequest(model, c, projectID, options, isAntigravity)
		if options.StreamOptions.OnPayload != nil {
			options.StreamOptions.OnPayload(requestBody)
		}
		requestBodyJSON, err := json.Marshal(requestBody)
		if err != nil {
			pushProviderError(stream, model, err.Error())
			return
		}

		requestHeaders := map[string]string{
			"Authorization": "Bearer " + accessToken,
			"Content-Type":  "application/json",
			"Accept":        "text/event-stream",
		}
		for key, value := range geminiCLIBaseHeaders(isAntigravity) {
			requestHeaders[key] = value
		}
		for key, value := range BuildGeminiCLIHeaders(model, model.Headers) {
			requestHeaders[key] = value
		}
		for key, value := range options.StreamOptions.Headers {
			requestHeaders[key] = value
		}

		stopReason := ai.StopReasonStop
		usage := ai.Usage{}
		var textBuilder strings.Builder
		var thinkingBuilder strings.Builder
		toolCalls := make([]ai.ContentBlock, 0)

		requestURL := ""
		var response *http.Response
		var lastErr error
		for attempt := 0; attempt <= maxGeminiCLIRetries; attempt++ {
			if runCtx.Err() != nil {
				pushProviderAborted(stream, model)
				return
			}
			endpoint := endpoints[minInt(attempt, len(endpoints)-1)]
			requestURL = strings.TrimRight(endpoint, "/") + "/v1internal:streamGenerateContent?alt=sse"
			response, lastErr = doGeminiCLIRequest(runCtx, requestURL, requestHeaders, requestBodyJSON)
			if lastErr == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
				break
			}
			if response != nil {
				bodyBytes, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				errorText := string(bodyBytes)
				if shouldRetryGeminiCLIStatus(response.StatusCode, errorText) && attempt < maxGeminiCLIRetries {
					delay := baseGeminiCLIRetryDelay * time.Duration(1<<attempt)
					if parsedDelayMs, ok := ExtractRetryDelay(errorText, response.Header); ok {
						maxDelay := options.StreamOptions.MaxRetryDelayMs
						if maxDelay <= 0 {
							maxDelay = 60000
						}
						if parsedDelayMs > maxDelay {
							pushProviderError(stream, model, fmt.Sprintf("server requested %ds retry delay (max %ds): %s", parsedDelayMs/1000, maxDelay/1000, extractGeminiCLIErrorMessage(errorText)))
							return
						}
						delay = time.Duration(parsedDelayMs) * time.Millisecond
					}
					if sleepErr := sleepWithContext(runCtx, delay); sleepErr != nil {
						if isContextAborted(runCtx, sleepErr) {
							pushProviderAborted(stream, model)
							return
						}
						pushProviderError(stream, model, sleepErr.Error())
						return
					}
					continue
				}
				pushProviderError(stream, model, fmt.Sprintf("cloud code assist API error (%d): %s", response.StatusCode, extractGeminiCLIErrorMessage(errorText)))
				return
			}
			if lastErr != nil && attempt < maxGeminiCLIRetries {
				delay := baseGeminiCLIRetryDelay * time.Duration(1<<attempt)
				if sleepErr := sleepWithContext(runCtx, delay); sleepErr != nil {
					if isContextAborted(runCtx, sleepErr) {
						pushProviderAborted(stream, model)
						return
					}
					pushProviderError(stream, model, sleepErr.Error())
					return
				}
				continue
			}
		}
		if response == nil || response.Body == nil {
			if lastErr != nil {
				if isContextAborted(runCtx, lastErr) {
					pushProviderAborted(stream, model)
					return
				}
				pushProviderError(stream, model, lastErr.Error())
				return
			}
			pushProviderError(stream, model, "cloud code assist returned empty response")
			return
		}

		receivedContent := false
		currentResponse := response
		for emptyAttempt := 0; emptyAttempt <= MaxGeminiEmptyStreamRetries; emptyAttempt++ {
			hasContent, err := consumeGeminiCLIResponse(
				currentResponse.Body,
				&textBuilder,
				&thinkingBuilder,
				&toolCalls,
				stream,
				&usage,
				&stopReason,
			)
			_ = currentResponse.Body.Close()
			if err != nil {
				if isContextAborted(runCtx, err) {
					pushProviderAborted(stream, model)
					return
				}
				pushProviderError(stream, model, err.Error())
				return
			}
			if hasContent {
				receivedContent = true
				break
			}
			if !ShouldRetryGeminiEmptyStream(false, emptyAttempt) {
				break
			}
			delay, _ := GeminiEmptyStreamBackoff(emptyAttempt + 1)
			if sleepErr := sleepWithContext(runCtx, delay); sleepErr != nil {
				if isContextAborted(runCtx, sleepErr) {
					pushProviderAborted(stream, model)
					return
				}
				pushProviderError(stream, model, sleepErr.Error())
				return
			}
			retryResp, reqErr := doGeminiCLIRequest(runCtx, requestURL, requestHeaders, requestBodyJSON)
			if reqErr != nil {
				if isContextAborted(runCtx, reqErr) {
					pushProviderAborted(stream, model)
					return
				}
				pushProviderError(stream, model, reqErr.Error())
				return
			}
			if retryResp.StatusCode < 200 || retryResp.StatusCode >= 300 {
				bodyBytes, _ := io.ReadAll(retryResp.Body)
				_ = retryResp.Body.Close()
				pushProviderError(stream, model, fmt.Sprintf("cloud code assist API error (%d): %s", retryResp.StatusCode, extractGeminiCLIErrorMessage(string(bodyBytes))))
				return
			}
			textBuilder.Reset()
			thinkingBuilder.Reset()
			toolCalls = toolCalls[:0]
			usage = ai.Usage{}
			stopReason = ai.StopReasonStop
			currentResponse = retryResp
		}
		if !receivedContent {
			pushProviderError(stream, model, "cloud code assist API returned an empty response")
			return
		}

		message := ai.Message{
			Role:       ai.RoleAssistant,
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			StopReason: stopReason,
			Usage:      usage,
			Timestamp:  time.Now().UnixMilli(),
		}
		if thinking := strings.TrimSpace(thinkingBuilder.String()); thinking != "" {
			message.Content = append(message.Content, ai.ContentBlock{
				Type:     ai.ContentTypeThinking,
				Thinking: thinking,
			})
		}
		if text := strings.TrimSpace(textBuilder.String()); text != "" {
			message.Content = append(message.Content, ai.ContentBlock{
				Type: ai.ContentTypeText,
				Text: text,
			})
		}
		if len(toolCalls) > 0 {
			message.Content = append(message.Content, toolCalls...)
		}
		if message.StopReason == ai.StopReasonStop && len(toolCalls) > 0 {
			message.StopReason = ai.StopReasonToolUse
		}
		message.Usage.Cost = ai.CalculateCost(model, message.Usage)
		stream.Push(ai.AssistantMessageEvent{
			Type:    ai.EventDone,
			Message: message,
			Reason:  message.StopReason,
		})
	}()
	return stream
}

func BuildGoogleGeminiCLIRequest(
	model ai.Model,
	context ai.Context,
	projectID string,
	options googleGeminiCLIOptions,
	isAntigravity bool,
) map[string]any {
	base := BuildGoogleGenerateContentParams(model, context, GoogleOptions{
		StreamOptions: options.StreamOptions,
		ToolChoice:    options.ToolChoice,
		Thinking:      options.Thinking,
	})
	request := map[string]any{
		"contents": base["contents"],
	}
	if cfg, ok := base["config"].(map[string]any); ok {
		generationConfig := map[string]any{}
		for key, value := range cfg {
			if key == "systemInstruction" {
				continue
			}
			generationConfig[key] = value
		}
		if len(generationConfig) > 0 {
			request["generationConfig"] = generationConfig
		}
		if systemInstruction, ok := cfg["systemInstruction"].(string); ok && strings.TrimSpace(systemInstruction) != "" {
			request["systemInstruction"] = map[string]any{
				"parts": []map[string]any{
					{"text": utils.SanitizeSurrogates(systemInstruction)},
				},
			}
		}
	}
	if sessionID := strings.TrimSpace(options.StreamOptions.SessionID); sessionID != "" {
		request["sessionId"] = sessionID
	}
	if isAntigravity {
		existingParts := []map[string]any{}
		if instruction, ok := request["systemInstruction"].(map[string]any); ok {
			if parts, ok := instruction["parts"].([]map[string]any); ok {
				existingParts = append(existingParts, parts...)
			}
		}
		request["systemInstruction"] = map[string]any{
			"role": "user",
			"parts": append([]map[string]any{
				{"text": antigravitySystemInstruction},
				{"text": "Please ignore following [ignore]" + antigravitySystemInstruction + "[/ignore]"},
			}, existingParts...),
		}
	}
	out := map[string]any{
		"project":   projectID,
		"model":     model.ID,
		"request":   request,
		"userAgent": "pi-coding-agent",
		"requestId": fmt.Sprintf("pi-%d-%d", time.Now().UnixMilli(), rand.Int63()),
	}
	if isAntigravity {
		out["requestType"] = "agent"
		out["userAgent"] = "antigravity"
	}
	return out
}

func consumeGeminiCLIResponse(
	body io.Reader,
	textBuilder *strings.Builder,
	thinkingBuilder *strings.Builder,
	toolCalls *[]ai.ContentBlock,
	stream *ai.AssistantMessageEventStream,
	usage *ai.Usage,
	stopReason *ai.StopReason,
) (bool, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), geminiCLIScannerBufferMaxSize)
	hasContent := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		rawJSON := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if rawJSON == "" {
			continue
		}
		var chunk cloudCodeAssistResponseChunk
		if err := json.Unmarshal([]byte(rawJSON), &chunk); err != nil {
			continue
		}
		if chunk.Response == nil {
			continue
		}
		if chunk.Response.UsageMetadata != nil && usage != nil {
			promptTokens := chunk.Response.UsageMetadata.PromptTokenCount
			cacheReadTokens := chunk.Response.UsageMetadata.CachedContentTokenCount
			usage.Input = promptTokens - cacheReadTokens
			usage.Output = chunk.Response.UsageMetadata.CandidatesTokenCount + chunk.Response.UsageMetadata.ThoughtsTokenCount
			usage.CacheRead = cacheReadTokens
			usage.CacheWrite = 0
			usage.TotalTokens = chunk.Response.UsageMetadata.TotalTokenCount
		}
		if len(chunk.Response.Candidates) == 0 {
			continue
		}
		candidate := chunk.Response.Candidates[0]
		if strings.TrimSpace(candidate.FinishReason) != "" && stopReason != nil {
			*stopReason = MapGoogleStopReason(candidate.FinishReason)
		}
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				hasContent = true
				if part.Thought {
					thinkingBuilder.WriteString(part.Text)
					stream.Push(ai.AssistantMessageEvent{Type: ai.EventThinkingDelta, Delta: part.Text})
				} else {
					textBuilder.WriteString(part.Text)
					stream.Push(ai.AssistantMessageEvent{Type: ai.EventTextDelta, Delta: part.Text})
				}
			}
			if part.FunctionCall != nil {
				hasContent = true
				toolCallID := strings.TrimSpace(part.FunctionCall.ID)
				if toolCallID == "" {
					toolCallID = fmt.Sprintf("%s_%d", strings.TrimSpace(part.FunctionCall.Name), time.Now().UnixMilli())
				}
				toolCall := NormalizeGoogleToolCall(
					part.FunctionCall.Name,
					part.FunctionCall.Args,
					toolCallID,
					part.ThoughtSignature,
				)
				*toolCalls = append(*toolCalls, toolCall)
				stream.Push(ai.AssistantMessageEvent{Type: ai.EventToolCallEnd, ToolCall: &toolCall})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return hasContent, err
	}
	return hasContent, nil
}

func geminiCLIBaseHeaders(isAntigravity bool) map[string]string {
	if isAntigravity {
		version := strings.TrimSpace(os.Getenv("PI_AI_ANTIGRAVITY_VERSION"))
		if version == "" {
			version = defaultAntigravityVersion
		}
		return map[string]string{
			"User-Agent":        fmt.Sprintf("antigravity/%s darwin/arm64", version),
			"X-Goog-Api-Client": "google-cloud-sdk vscode_cloudshelleditor/0.1",
			"Client-Metadata":   `{"ideType":"IDE_UNSPECIFIED","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`,
		}
	}
	return map[string]string{
		"User-Agent":        "google-cloud-sdk vscode_cloudshelleditor/0.1",
		"X-Goog-Api-Client": "gl-node/22.17.0",
		"Client-Metadata":   `{"ideType":"IDE_UNSPECIFIED","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`,
	}
}

func geminiCLIEndpoints(model ai.Model, isAntigravity bool) []string {
	if baseURL := strings.TrimSpace(model.BaseURL); baseURL != "" {
		return []string{baseURL}
	}
	if isAntigravity {
		return antigravityEndpointFallbacks
	}
	return []string{defaultGeminiCLIEndpoint}
}

func doGeminiCLIRequest(
	ctx context.Context,
	url string,
	headers map[string]string,
	body []byte,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	return http.DefaultClient.Do(req)
}

func shouldRetryGeminiCLIStatus(status int, errorText string) bool {
	if status == http.StatusTooManyRequests ||
		status == http.StatusInternalServerError ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout {
		return true
	}
	lower := strings.ToLower(errorText)
	return strings.Contains(lower, "resource exhausted") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "overloaded") ||
		strings.Contains(lower, "service unavailable") ||
		strings.Contains(lower, "other side closed")
}

func extractGeminiCLIErrorMessage(errorText string) string {
	var payload struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(errorText), &payload); err == nil && payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
		return strings.TrimSpace(payload.Error.Message)
	}
	return strings.TrimSpace(errorText)
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isGemini3Model(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(id, "gemini-3-pro") || strings.Contains(id, "gemini-3-flash") ||
		strings.Contains(id, "gemini-3.1-pro") || strings.Contains(id, "gemini-3.1-flash")
}

func isGemini3ProModel(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(id, "gemini-3-pro") || strings.Contains(id, "gemini-3.1-pro")
}

func getGeminiCLIThinkingLevel(level ai.ThinkingLevel, modelID string) string {
	if isGemini3ProModel(modelID) {
		switch level {
		case ai.ThinkingMinimal, ai.ThinkingLow:
			return "LOW"
		default:
			return "HIGH"
		}
	}
	switch level {
	case ai.ThinkingMinimal:
		return "MINIMAL"
	case ai.ThinkingLow:
		return "LOW"
	case ai.ThinkingMedium:
		return "MEDIUM"
	case ai.ThinkingHigh, ai.ThinkingXHigh:
		return "HIGH"
	default:
		return "MEDIUM"
	}
}
