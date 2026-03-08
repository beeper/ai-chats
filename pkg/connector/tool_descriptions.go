package connector

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

func (oc *AIClient) toolDescriptionForPortal(meta *PortalMetadata, toolName string, fallback string) string {
	name := strings.TrimSpace(toolName)
	switch name {
	case toolspec.ImageName:
		if meta != nil && oc.getModelCapabilitiesForMeta(meta).SupportsVision {
			return toolspec.ImageDescriptionVisionHint
		}
	case toolspec.WebSearchName:
		return oc.resolveWebSearchDescription(fallback)
	}
	return fallback
}

func (oc *AIClient) resolveWebSearchDescription(fallback string) string {
	return stringutil.FirstNonEmpty(fallback, toolspec.WebSearchDescription)
}
