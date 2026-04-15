package ai

import (
	"errors"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/textfs"
)

var (
	errTextFSUnavailable           = errors.New("storage unavailable")
	errTextFSLoginIdentityRequired = errors.New("storage login identity unavailable")
)

func (oc *AIClient) textFSStoreForAgent(agentID string) (*textfs.Store, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return nil, errTextFSUnavailable
	}
	db := oc.bridgeDB()
	if db == nil {
		return nil, errTextFSUnavailable
	}
	loginID := canonicalLoginID(oc.UserLogin)
	if loginID == "" {
		return nil, errTextFSLoginIdentityRequired
	}
	normalizedAgentID := normalizeAgentID(agentID)
	if normalizedAgentID == "" {
		normalizedAgentID = normalizeAgentID(agents.DefaultAgentID)
	}
	return textfs.NewStore(db, canonicalLoginBridgeID(oc.UserLogin), loginID, normalizedAgentID), nil
}
