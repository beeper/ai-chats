package ai

import (
	"context"
	"strings"
)

type ModelAPI string

const (
	ModelAPIResponses       ModelAPI = "responses"
	ModelAPIChatCompletions ModelAPI = "chat_completions"
)

func isDirectOpenAIModel(modelID string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelID)), "openai/")
}

func normalizeModelAPI(value string) ModelAPI {
	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "responses":
		return ModelAPIResponses
	case "chat_completions":
		return ModelAPIChatCompletions
	default:
		return ""
	}
}

func (oc *AIClient) resolveModelAPI(meta *PortalMetadata) ModelAPI {
	responder := oc.responderForMeta(context.Background(), meta)
	modelID := ""
	if responder != nil {
		modelID = responder.ModelID
	}
	if info := oc.findModelInfo(modelID); info != nil {
		if api := normalizeModelAPI(info.API); api != "" {
			return api
		}
	}
	return ModelAPIResponses
}
