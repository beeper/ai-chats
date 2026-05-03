package connector

func modelModeTestMeta(modelID string) *PortalMetadata {
	return &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			GhostID: modelUserID(modelID),
			ModelID: modelID,
		},
	}
}
