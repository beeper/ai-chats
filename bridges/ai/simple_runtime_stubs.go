package ai

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

const maxStreamingToolTurns = 1

type mentionContext struct {
	WasMentioned   bool
	HasExplicit    bool
	MentionRegexes []*regexp.Regexp
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

func (oc *AIClient) buildResponsesStreamingParams(ctx context.Context, meta *PortalMetadata, systemPrompt string, input responses.ResponseInputParam, store bool) responses.ResponseNewParams {
	return responses.ResponseNewParams{Model: oc.modelIDForAPI(oc.effectiveModel(meta)), Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input}}
}

func runStreamingStep[T any](
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

func touchStreamingActivity(context.Context) {}

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
