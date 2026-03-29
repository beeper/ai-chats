package ai

import (
	"net/url"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

func canonicalModelIdentifier(modelID string) string {
	modelID = strings.TrimSpace(ResolveAlias(modelID))
	if modelID == "" {
		return ""
	}
	return "model:" + modelID
}

func parseCanonicalModelIdentifier(identifier string) string {
	if suffix, ok := strings.CutPrefix(strings.TrimSpace(identifier), "model:"); ok {
		return strings.TrimSpace(ResolveAlias(suffix))
	}
	return ""
}

func canonicalAgentIdentifier(agentID string) string {
	agentID = normalizeAgentID(agentID)
	if agentID == "" {
		return ""
	}
	return "agent:" + agentID
}

func parseCanonicalAgentIdentifier(identifier string) string {
	if suffix, ok := strings.CutPrefix(strings.TrimSpace(identifier), "agent:"); ok {
		return normalizeAgentID(suffix)
	}
	return ""
}

func modelContactName(modelID string, info *ModelInfo) string {
	if info != nil && info.Name != "" {
		return info.Name
	}
	return ResolveAlias(modelID)
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
	_ = info
	identifiers := []string{}
	if ident := canonicalModelIdentifier(modelID); ident != "" {
		identifiers = append(identifiers, ident)
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
