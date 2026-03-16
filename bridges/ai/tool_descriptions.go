package ai

import (
	"strings"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/pkg/shared/toolspec"
)

func (oc *AIClient) toolDescriptionForPortal(meta *PortalMetadata, toolName string, fallback string) string {
	name := strings.TrimSpace(toolName)
	switch name {
	case toolspec.ImageName:
		if meta != nil && oc.getModelCapabilitiesForMeta(meta).SupportsVision {
			return toolspec.ImageDescriptionVisionHint
		}
	case toolspec.WebSearchName:
		return stringutil.FirstNonEmpty(fallback, toolspec.WebSearchDescription)
	}
	return fallback
}
