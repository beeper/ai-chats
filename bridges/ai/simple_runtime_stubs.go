package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const maxAgentLoopToolTurns = 1

func normalizeAgentID(agentID string) string {
	return strings.ToLower(strings.TrimSpace(agentID))
}

type mentionContext struct {
	WasMentioned   bool
	HasExplicit    bool
	MentionRegexes []*regexp.Regexp
}

func (oc *AIClient) recordAgentActivity(context.Context, *bridgev2.Portal, *PortalMetadata) {
}

func (oc *AIClient) notifySessionMutation(context.Context, *bridgev2.Portal, *PortalMetadata, bool) {
}

func (oc *AIClient) resolveMentionContext(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, evt *event.Event, mentions *event.Mentions, body string) mentionContext {
	return mentionContext{WasMentioned: true}
}

func stripMentionPatterns(body string, patterns []*regexp.Regexp) string {
	for _, pattern := range patterns {
		if pattern != nil {
			body = pattern.ReplaceAllString(body, "")
		}
	}
	return body
}

func isTextFileMime(string) bool {
	return false
}

func buildTextFileMessage(caption string, hasUserCaption bool, fileName, mimeType, content string, truncated bool) string {
	return content
}

func (oc *AIClient) downloadTextFile(ctx context.Context, mediaURL string, encryptedFile *event.EncryptedFileInfo, mimeType string) (string, bool, error) {
	return "", false, nil
}

func (oc *AIClient) downloadPDFFile(ctx context.Context, mediaURL string, encryptedFile *event.EncryptedFileInfo, mimeType string) (string, bool, error) {
	return "", false, nil
}

func (oc *AIClient) stopSubagentRuns(context.Context, any) int {
	return 0
}

func estimatePromptContextTokensForModel(prompt PromptContext, modelID string) int {
	return 0
}

func agentLoopInactivityCause(context.Context) error {
	return nil
}

func (oc *AIClient) buildChatCompletionsAgentLoopParams(ctx context.Context, meta *PortalMetadata, messages []openai.ChatCompletionMessageParamUnion) openai.ChatCompletionNewParams {
	return openai.ChatCompletionNewParams{Model: oc.modelIDForAPI(oc.effectiveModel(meta)), Messages: messages}
}

func (oc *AIClient) buildResponsesAgentLoopParams(ctx context.Context, meta *PortalMetadata, systemPrompt string, input responses.ResponseInputParam, store bool) responses.ResponseNewParams {
	return responses.ResponseNewParams{Model: oc.modelIDForAPI(oc.effectiveModel(meta)), Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input}}
}

func runAgentLoopStreamStep[T any](
	ctx context.Context,
	state *streamingState,
	stream *ssestream.Stream[T],
	onEvent func(T) (bool, *ContextLengthError, error),
	extra ...any,
) (bool, *ContextLengthError, error) {
	if stream == nil {
		return true, nil, errors.New("stream is nil")
	}
	defer stream.Close()
	for stream.Next() {
		if err := ctx.Err(); err != nil {
			return true, nil, err
		}
		done, cle, err := onEvent(stream.Current())
		if done || cle != nil || err != nil {
			return done, cle, err
		}
	}
	if err := stream.Err(); err != nil {
		if len(extra) > 0 {
			if handler, ok := extra[0].(func(error) (*ContextLengthError, error)); ok && handler != nil {
				cle, handledErr := handler(err)
				return true, cle, handledErr
			}
		}
		return true, nil, err
	}
	return false, nil, nil
}

func executeChatToolCallsSequentially(context.Context, *AIClient, *bridgev2.Portal, *streamingState, *PortalMetadata, []openai.ChatCompletionChunkChoiceDeltaToolCall) ([]openai.ChatCompletionToolMessageParam, []string) {
	return nil, nil
}

func touchAgentLoopActivity(context.Context) {}

const (
	TTSResultPrefix    = "AUDIO:"
	ImageResultPrefix  = "IMAGE:"
	ImagesResultPrefix = "IMAGES:"
)

func (oc *AIClient) sendGeneratedAudio(ctx context.Context, portal *bridgev2.Portal, data []byte, mimeType string, turnID string) (id.ContentURIString, string, error) {
	return "", "", errors.New("audio generation is disabled")
}

func (oc *AIClient) sendGeneratedImage(ctx context.Context, portal *bridgev2.Portal, data []byte, mimeType string, turnID string, caption string) (id.ContentURIString, string, error) {
	return "", "", errors.New("image generation is disabled")
}

func parseToolArgsPrompt(argsJSON string) (string, error) {
	var args struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	return strings.TrimSpace(args.Prompt), nil
}

func decodeBase64Image(input string) ([]byte, string, error) {
	mimeType := "image/png"
	if strings.HasPrefix(input, "data:") && !strings.Contains(input, ",") {
		return nil, "", errors.New("invalid data URL: no comma separator")
	}
	if before, after, ok := strings.Cut(input, ","); ok && strings.HasPrefix(before, "data:") {
		mimeType = strings.TrimPrefix(strings.TrimSuffix(before, ";base64"), "data:")
		input = after
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(input))
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(strings.TrimSpace(input))
	}
	return data, mimeType, err
}

func normalizeToolArgsJSON(argsJSON string) string {
	argsJSON = strings.TrimSpace(argsJSON)
	if argsJSON == "" {
		return "{}"
	}
	return argsJSON
}

func parseToolInputPayload(argsJSON string) map[string]any {
	var out map[string]any
	_ = json.Unmarshal([]byte(normalizeToolArgsJSON(argsJSON)), &out)
	return out
}

func (oc *AIClient) isBuiltinToolDenied(context.Context, *bridgev2.Portal, *streamingState, *activeToolCall, string, map[string]any) bool {
	return false
}

func toolDisplayTitle(toolName string) string {
	return toolName
}

func (oc *AIClient) modelSupportsToolCalling(ctx context.Context, meta *PortalMetadata) bool {
	return oc.getModelCapabilitiesForMeta(ctx, meta).SupportsToolCalling
}
