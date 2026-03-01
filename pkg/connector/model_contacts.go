package connector

import (
	"net/url"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

func modelContactName(modelID string, info *ModelInfo) string {
	if info != nil && info.Name != "" {
		return info.Name
	}
	return GetModelDisplayName(modelID)
}

func modelContactProvider(modelID string, info *ModelInfo) string {
	if info != nil && info.Provider != "" {
		return info.Provider
	}
	if backend, _ := ParseModelPrefix(modelID); backend != "" {
		return string(backend)
	}
	return ""
}

func modelContactIdentifiers(modelID string, info *ModelInfo) []string {
	identifiers := []string{modelID}
	name := modelContactName(modelID, info)
	if name != "" && name != modelID {
		identifiers = append(identifiers, name)
	}
	if provider := modelContactProvider(modelID, info); provider != "" {
		if name != "" {
			identifiers = append(identifiers, provider+"/"+name)
		}
		lowerProvider := strings.ToLower(provider) + "/"
		if !strings.HasPrefix(strings.ToLower(modelID), lowerProvider) {
			identifiers = append(identifiers, provider+"/"+modelID)
		}
	}
	if openRouterURL := modelContactOpenRouterURL(modelID, info); openRouterURL != "" {
		identifiers = append(identifiers, "uri:"+openRouterURL)
	}
	return stringutil.DedupeStrings(identifiers)
}

func modelContactOpenRouterURL(modelID string, info *ModelInfo) string {
	if modelID == "" {
		return ""
	}
	if info != nil {
		if !strings.EqualFold(info.Provider, "openrouter") {
			return ""
		}
	} else {
		backend, actual := ParseModelPrefix(modelID)
		if backend != BackendOpenRouter {
			return ""
		}
		modelID = actual
	}
	if backend, actual := ParseModelPrefix(modelID); backend == BackendOpenRouter {
		modelID = actual
	}
	return openRouterModelURL(modelID)
}

func openRouterModelURL(modelID string) string {
	if modelID == "" {
		return ""
	}
	parts := strings.Split(modelID, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return "https://openrouter.ai/models/" + strings.Join(parts, "/")
}
