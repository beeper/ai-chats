package ai

import "context"

func (oc *AIClient) getModelCapabilitiesForMeta(meta *PortalMetadata) ModelCapabilities {
	modelID := oc.effectiveModel(meta)
	return getModelCapabilities(modelID, oc.findModelInfo(modelID))
}

func (oc *AIClient) canRunMediaUnderstanding(ctx context.Context, meta *PortalMetadata, capability MediaUnderstandingCapability) bool {
	if !oc.canUseMediaUnderstanding(meta) {
		return false
	}
	toolsCfg := oc.connector.Config.Tools.Media
	capCfg := toolsCfg.ConfigForCapability(capability)
	if capCfg != nil && capCfg.Enabled != nil && !*capCfg.Enabled {
		return false
	}
	if capability == MediaCapabilityImage && oc.modelSupportsVision(ctx, meta) {
		return true
	}
	entries := resolveMediaEntries(toolsCfg, capCfg, capability)
	if len(entries) > 0 {
		return true
	}
	if auto := oc.resolveAutoMediaEntries(capability, capCfg, meta); len(auto) > 0 {
		return true
	}
	return false
}

// getRoomCapabilities returns effective room capabilities, including media-understanding
// unions (image, audio, video) when an agent is assigned.
func (oc *AIClient) getRoomCapabilities(ctx context.Context, meta *PortalMetadata) ModelCapabilities {
	caps := oc.getModelCapabilitiesForMeta(meta)
	if !caps.SupportsVision && oc.canRunMediaUnderstanding(ctx, meta, MediaCapabilityImage) {
		caps.SupportsVision = true
	}
	if !caps.SupportsAudio && oc.canRunMediaUnderstanding(ctx, meta, MediaCapabilityAudio) {
		caps.SupportsAudio = true
	}
	if !caps.SupportsVideo && oc.canRunMediaUnderstanding(ctx, meta, MediaCapabilityVideo) {
		caps.SupportsVideo = true
	}
	return caps
}
