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

func agentModeTestMeta(agentID string) *PortalMetadata {
	return &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetAgent,
			GhostID: agentUserID(agentID),
			AgentID: agentID,
		},
	}
}
