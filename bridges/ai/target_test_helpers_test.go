package ai

func modelModeTestMeta(modelID string) *PortalMetadata {
	return &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			GhostID: modelUserID(modelID),
			ModelID: modelID,
		},
	}
}
