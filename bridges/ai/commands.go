package ai

func isValidAgentID(agentID string) bool {
	if agentID == "" {
		return false
	}
	for i := range len(agentID) {
		ch := agentID[i]
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return false
		}
	}
	return true
}
