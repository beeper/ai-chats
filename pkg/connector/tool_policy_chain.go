package connector

import (
	"context"
	"strings"

	"github.com/rs/zerolog"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	agenttools "github.com/beeper/ai-bridge/pkg/agents/tools"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

type toolPolicyResolution struct {
	agent    *agents.AgentDefinition
	policies []*toolpolicy.ToolPolicy
	allowed  map[string]struct{}
}

type toolPolicyContext struct {
	names        []string
	coreTools    map[string]struct{}
	pluginGroups toolpolicy.PluginToolGroups
}

func (oc *AIClient) resolveToolPolicies(meta *PortalMetadata) toolPolicyResolution {
	var agent *agents.AgentDefinition
	if meta != nil {
		store := NewAgentStoreAdapter(oc)
		agent, _ = store.GetAgentForRoom(context.Background(), meta)
	}

	globalTools := oc.connector.Config.ToolPolicy
	provider, modelID := oc.resolveToolPolicyModelContext(meta)

	effective := toolpolicy.ResolveEffectiveToolPolicy(struct {
		Global        *toolpolicy.GlobalToolPolicyConfig
		Agent         *toolpolicy.ToolPolicyConfig
		ModelProvider string
		ModelID       string
	}{
		Global: globalTools,
		Agent: func() *toolpolicy.ToolPolicyConfig {
			if agent != nil {
				return agent.Tools
			}
			return nil
		}(),
		ModelProvider: provider,
		ModelID:       modelID,
	})

	profilePolicy := toolpolicy.ResolveToolProfilePolicy(effective.Profile)
	providerProfilePolicy := toolpolicy.ResolveToolProfilePolicy(effective.ProviderProfile)
	profilePolicy = toolpolicy.MergeAlsoAllow(profilePolicy, effective.ProfileAlsoAllow)
	providerProfilePolicy = toolpolicy.MergeAlsoAllow(providerProfilePolicy, effective.ProviderAlsoAllow)

	ctx := oc.buildToolPolicyContext(meta)
	log := oc.log.With().Str("policy", "tools").Logger()
	resolve := newPolicyResolver(log, ctx)

	resolvedPolicies := []*toolpolicy.ToolPolicy{
		resolve.resolvePolicy(profilePolicy, resolvePolicyLabel("tools.profile", effective.Profile)),
		resolve.resolvePolicy(providerProfilePolicy, resolvePolicyLabel("tools.byProvider.profile", effective.ProviderProfile)),
		resolve.resolvePolicy(effective.GlobalPolicy, "tools.allow"),
		resolve.resolvePolicy(effective.GlobalProviderPolicy, "tools.byProvider.allow"),
		resolve.resolvePolicy(effective.AgentPolicy, resolveAgentPolicyLabel("agents.tools.allow", agent)),
		resolve.resolvePolicy(effective.AgentProviderPolicy, resolveAgentPolicyLabel("agents.tools.byProvider.allow", agent)),
		resolve.resolvePolicy(resolveSubagentPolicy(meta, globalTools), "tools.subagents"),
	}
	if agent == nil && !hasAssignedAgent(meta) {
		modelRoomPolicy := &toolpolicy.ToolPolicy{
			Allow: []string{
				toolspec.MemorySearchName,
				toolspec.MemoryGetName,
				toolspec.WebSearchName,
			},
		}
		resolvedPolicies = append(resolvedPolicies, resolve.resolvePolicy(modelRoomPolicy, "tools.model_rooms"))
	}

	allowed := resolve.applyPolicies(ctx.names, resolvedPolicies)

	return toolPolicyResolution{
		agent:    agent,
		policies: resolvedPolicies,
		allowed:  allowed,
	}
}

func (oc *AIClient) buildToolPolicyContext(meta *PortalMetadata) toolPolicyContext {
	names := toolpolicy.NormalizeToolList(oc.toolNamesForPortal(meta))
	coreTools := make(map[string]struct{})
	toolList := make([]*agenttools.Tool, 0, len(names))
	for _, name := range names {
		tool := agenttools.GetTool(name)
		normalizedName := toolpolicy.NormalizeToolName(name)
		if tool == nil {
			if normalizedName != "" {
				coreTools[normalizedName] = struct{}{}
			}
			continue
		}
		toolList = append(toolList, tool)
		if agenttools.IsPluginTool(tool) {
			continue
		}
		if normalizedName != "" {
			coreTools[normalizedName] = struct{}{}
		}
	}

	pluginGroups := toolpolicy.BuildPluginToolGroups(toolList, func(t *agenttools.Tool) string {
		if t == nil {
			return ""
		}
		return t.Name
	}, func(t *agenttools.Tool) (string, bool) {
		return agenttools.PluginIDForTool(t)
	})

	return toolPolicyContext{
		names:        names,
		coreTools:    coreTools,
		pluginGroups: pluginGroups,
	}
}

type policyResolver struct {
	log zerolog.Logger
	ctx toolPolicyContext
}

func newPolicyResolver(log zerolog.Logger, ctx toolPolicyContext) *policyResolver {
	return &policyResolver{log: log, ctx: ctx}
}

func (r *policyResolver) resolvePolicy(policy *toolpolicy.ToolPolicy, label string) *toolpolicy.ToolPolicy {
	if policy == nil {
		return nil
	}
	stripped, unknownAllowlist, resolved := toolpolicy.StripPluginOnlyAllowlist(policy, r.ctx.pluginGroups, r.ctx.coreTools)
	if len(unknownAllowlist) > 0 {
		suffix := "These entries won't match any tool unless the plugin is enabled."
		if stripped {
			suffix = "Ignoring allowlist so core tools remain available. Use tools.alsoAllow for additive plugin tool enablement."
		}
		r.log.Warn().
			Str("policy_label", label).
			Strs("unknown_allowlist", unknownAllowlist).
			Msg("Tool policy allowlist contains unknown entries. " + suffix)
	}
	return toolpolicy.ExpandPolicyWithPluginGroups(resolved, r.ctx.pluginGroups)
}

func (r *policyResolver) applyPolicies(names []string, policies []*toolpolicy.ToolPolicy) map[string]struct{} {
	filtered := names
	for _, policy := range policies {
		if policy == nil {
			continue
		}
		filtered = toolpolicy.FilterToolsByPolicy(filtered, policy)
	}
	allowed := make(map[string]struct{}, len(filtered))
	for _, name := range filtered {
		allowed[name] = struct{}{}
	}
	return allowed
}

func resolvePolicyLabel(prefix string, profile toolpolicy.ToolProfileID) string {
	if profile == "" {
		return prefix
	}
	return prefix + " (" + string(profile) + ")"
}

func resolveAgentPolicyLabel(prefix string, agent *agents.AgentDefinition) string {
	if agent == nil || strings.TrimSpace(agent.ID) == "" {
		return prefix
	}
	return prefix + " (" + agent.ID + ")"
}

func resolveSubagentPolicy(meta *PortalMetadata, global *toolpolicy.GlobalToolPolicyConfig) *toolpolicy.ToolPolicy {
	if meta == nil {
		return nil
	}
	if strings.TrimSpace(meta.SubagentParentRoomID) == "" {
		return nil
	}
	return toolpolicy.ResolveSubagentToolPolicy(global)
}
