package ai

import (
	"errors"

	"maunium.net/go/mautrix/bridgev2"
)

var errAgentsDisabled = errors.New("agents are disabled by bridge config")

func (c *Config) agentsEnabled() bool {
	if c == nil || c.Agents == nil || c.Agents.Enabled == nil {
		return true
	}
	return *c.Agents.Enabled
}

func (oc *OpenAIConnector) agentsEnabled() bool {
	if oc == nil {
		return true
	}
	return oc.Config.agentsEnabled()
}

func (oc *AIClient) agentsEnabled() bool {
	if oc == nil || oc.connector == nil {
		return true
	}
	return oc.connector.agentsEnabled()
}

func (oc *AIClient) agentFeaturesDisabledErr() error {
	return errAgentsDisabled
}

func (oc *AIClient) agentTargetBlocked(meta *PortalMetadata) bool {
	return oc != nil && !oc.agentsEnabled() && resolveAgentID(meta) != ""
}

func (oc *AIClient) ensureAgentTargetAllowed(meta *PortalMetadata) error {
	if oc.agentTargetBlocked(meta) {
		return oc.agentFeaturesDisabledErr()
	}
	return nil
}

func (oc *AIClient) shouldExcludeVisiblePortal(meta *PortalMetadata) bool {
	if shouldExcludeModelVisiblePortal(meta) {
		return true
	}
	return oc.agentTargetBlocked(meta)
}

func (oc *AIClient) isDefaultChatCandidate(portal *bridgev2.Portal) bool {
	return portal != nil && !oc.shouldExcludeVisiblePortal(portalMeta(portal))
}

func (oc *AIClient) chooseDefaultChatPortal(portals []*bridgev2.Portal) *bridgev2.Portal {
	var defaultPortal *bridgev2.Portal
	var (
		minIdx   int
		haveSlug bool
	)
	for _, portal := range portals {
		if !oc.isDefaultChatCandidate(portal) {
			continue
		}
		pm := portalMeta(portal)
		if idx, ok := parseChatSlug(pm.Slug); ok {
			if !haveSlug || idx < minIdx {
				minIdx = idx
				defaultPortal = portal
				haveSlug = true
			}
		} else if defaultPortal == nil && !haveSlug {
			defaultPortal = portal
		}
	}
	return defaultPortal
}
