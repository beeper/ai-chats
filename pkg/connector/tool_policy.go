package connector

import (
	"context"
	"slices"
	"strings"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	agenttools "github.com/beeper/ai-bridge/pkg/agents/tools"
)

func canUseNexusToolsForAgent(meta *PortalMetadata) bool {
	if meta == nil {
		return false
	}
	return agents.IsNexusAI(normalizeAgentID(resolveAgentID(meta)))
}

func (oc *AIClient) resolveToolPolicyModelContext(meta *PortalMetadata) (provider string, modelID string) {
	modelID = oc.effectiveModel(meta)
	backend, actual := ParseModelPrefix(modelID)
	if backend != "" {
		modelID = actual
	}
	provider = ""
	if parts := strings.SplitN(modelID, "/", 2); len(parts) == 2 {
		provider = parts[0]
	}
	if provider == "" {
		loginMeta := loginMetadata(oc.UserLogin)
		if loginMeta != nil {
			provider = loginMeta.Provider
		}
	}
	return provider, modelID
}

func (oc *AIClient) isToolAllowedByPolicy(meta *PortalMetadata, toolName string) bool {
	resolution := oc.resolveToolPolicies(meta)
	normalized := toolpolicy.NormalizeToolName(toolName)
	if normalized == "" {
		return false
	}
	_, ok := resolution.allowed[normalized]
	return ok
}

func (oc *AIClient) isToolAvailable(meta *PortalMetadata, toolName string) (bool, SettingSource, string) {
	if meta == nil {
		return false, SourceGlobalDefault, "Missing room metadata"
	}

	if !meta.Capabilities.SupportsToolCalling {
		return false, SourceModelLimit, "Model does not support tools"
	}

	if agenttools.IsBossTool(toolName) && !(meta.IsBuilderRoom || hasBossAgent(meta)) {
		return false, SourceGlobalDefault, "Builder room only"
	}

	if toolName == ToolNameImageGenerate && !oc.canUseImageGeneration() {
		return false, SourceProviderLimit, "Image generation not available for this provider"
	}
	if toolName == ToolNameApplyPatch {
		available, source, reason := oc.applyPatchAvailability(meta)
		if !available {
			return false, source, reason
		}
	}
	if toolName == ToolNameImage {
		if model, _ := oc.resolveVisionModelForImage(context.Background(), meta); model == "" {
			return false, SourceModelLimit, "No vision-capable model available"
		}
	}
	if oc.isMCPToolName(toolName) {
		if !oc.isMCPConfigured() {
			return false, SourceProviderLimit, "MCP tool bridge is not configured"
		}
	}
	// Compact local Nexus wrappers are not MCP tools, but still must be Nexus-agent-only.
	if isNexusCompactToolName(toolName) && !canUseNexusToolsForAgent(meta) {
		return false, SourceAgentPolicy, "Nexus tools are restricted to the Nexus agent"
	}
	return true, SourceGlobalDefault, ""
}

func (oc *AIClient) applyPatchAvailability(meta *PortalMetadata) (bool, SettingSource, string) {
	if oc == nil || oc.connector == nil {
		return false, SourceGlobalDefault, "apply_patch disabled by config"
	}
	cfg := oc.connector.Config.Tools.VFS
	if cfg == nil || cfg.ApplyPatch == nil || cfg.ApplyPatch.Enabled == nil || !*cfg.ApplyPatch.Enabled {
		return false, SourceGlobalDefault, "apply_patch disabled by config"
	}
	if len(cfg.ApplyPatch.AllowModels) == 0 {
		return true, SourceGlobalDefault, ""
	}
	modelID := strings.TrimSpace(oc.effectiveModel(meta))
	if modelID == "" {
		return false, SourceModelLimit, "apply_patch model unavailable"
	}
	provider, _ := oc.resolveToolPolicyModelContext(meta)
	if !applyPatchModelAllowed(cfg.ApplyPatch.AllowModels, modelID, provider) {
		return false, SourceModelLimit, "apply_patch not enabled for model"
	}
	return true, SourceGlobalDefault, ""
}

func applyPatchModelAllowed(allow []string, modelID string, provider string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}
	normalizedProvider := strings.ToLower(strings.TrimSpace(provider))
	_, actual := ParseModelPrefix(modelID)
	normalizedFull := strings.ToLower(strings.TrimSpace(modelID))
	normalizedActual := strings.ToLower(strings.TrimSpace(actual))

	candidates := map[string]struct{}{}
	if normalizedFull != "" {
		candidates[normalizedFull] = struct{}{}
	}
	if normalizedActual != "" {
		candidates[normalizedActual] = struct{}{}
	}
	if normalizedActual != "" {
		if parts := strings.SplitN(normalizedActual, "/", 2); len(parts) == 2 {
			candidates[parts[1]] = struct{}{}
			if normalizedProvider != "" {
				candidates[normalizedProvider+"/"+parts[1]] = struct{}{}
			}
		} else if normalizedProvider != "" {
			candidates[normalizedProvider+"/"+normalizedActual] = struct{}{}
		}
	}

	for _, raw := range allow {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		if _, ok := candidates[entry]; ok {
			return true
		}
	}
	return false
}

// isToolEnabled checks if a specific tool is enabled (policy + availability).
func (oc *AIClient) isToolEnabled(meta *PortalMetadata, toolName string) bool {
	toolName = normalizeToolAlias(toolName)

	if meta != nil && slices.Contains(meta.DisabledTools, toolName) {
		return false
	}

	available, _, _ := oc.isToolAvailable(meta, toolName)
	if !available {
		return false
	}

	return oc.isToolAllowedByPolicy(meta, toolName)
}

func (oc *AIClient) toolNamesForPortal(meta *PortalMetadata) []string {
	nameSet := make(map[string]struct{})
	for _, tool := range BuiltinTools() {
		nameSet[tool.Name] = struct{}{}
	}
	for _, tool := range agenttools.SessionTools() {
		nameSet[tool.Name] = struct{}{}
	}
	if meta != nil && (meta.IsBuilderRoom || hasBossAgent(meta)) {
		for _, tool := range agenttools.BossTools() {
			nameSet[tool.Name] = struct{}{}
		}
	}
	if oc != nil && oc.isMCPConfigured() {
		discoveryCtx, cancel := context.WithTimeout(context.Background(), mcpDiscoveryTimeout)
		for _, name := range oc.mcpDiscoveredToolNames(discoveryCtx) {
			nameSet[name] = struct{}{}
		}
		cancel()
	}
	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
