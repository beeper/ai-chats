package toolpolicy

import (
	"slices"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/globmatch"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// ToolProfileID defines access levels (OpenClaw-style).
type ToolProfileID string

const (
	ProfileSimple    ToolProfileID = "simple"
	ProfileCoding    ToolProfileID = "coding"
	ProfileMessaging ToolProfileID = "messaging"
	ProfileFull      ToolProfileID = "full"
	ProfileBoss      ToolProfileID = "boss"
)

// Tool group constants for policy composition (OpenClaw-style shorthands).
const (
	GroupSearch      = "group:search"
	GroupCalc        = "group:calc"
	GroupBuilder     = "group:builder"
	GroupMessaging   = "group:messaging"
	GroupRuntime     = "group:runtime"
	GroupSessions    = "group:sessions"
	GroupMemory      = "group:memory"
	GroupWeb         = "group:web"
	GroupMedia       = "group:media"
	GroupUI          = "group:ui"
	GroupAutomation  = "group:automation"
	GroupNodes       = "group:nodes"
	GroupStatus      = "group:status"
	GroupOpenClaw    = "group:openclaw"
	GroupAgentRemote = "group:agentremote"
	GroupAIBridge    = "group:ai-bridge"
	GroupFS          = "group:fs"
)

var agentRemoteExtras = []string{"gravatar_fetch", "gravatar_set", "beeper_docs", "beeper_send_feedback", "image_generate", "tts", "calculator"}

// ToolGroups maps group names to tool names for policy composition.
var ToolGroups = map[string][]string{
	GroupSearch:    {"web_search"},
	GroupCalc:      {"calculator"},
	GroupBuilder:   {"create_agent", "fork_agent", "edit_agent", "delete_agent", "list_agents", "run_internal_command"},
	GroupMessaging: {"message"},
	// OpenClaw semantics: session management tools only.
	GroupSessions:   {"sessions_list", "sessions_history", "sessions_send", "sessions_spawn", "session_status"},
	GroupMemory:     {"memory_search", "memory_get"},
	GroupRuntime:    {"exec", "process"},
	GroupWeb:        {"web_search", "web_fetch"},
	GroupMedia:      {"image", "image_generate", "tts"},
	GroupUI:         {"browser", "canvas"},
	GroupAutomation: {"cron", "gateway"},
	GroupNodes:      {"nodes"},
	GroupStatus:     {"session_status"},
	// Strict OpenClaw native tool set (excludes provider plugins + agentremote-only tools).
	GroupOpenClaw: {
		"browser",
		"canvas",
		"nodes",
		"cron",
		"message",
		"gateway",
		"agents_list",
		"sessions_list",
		"sessions_history",
		"sessions_send",
		"sessions_spawn",
		"session_status",
		"memory_search",
		"memory_get",
		"web_search",
		"web_fetch",
		"image",
	},
	// AgentRemote extras (keep separate so group:openclaw stays portable with OpenClaw configs).
	GroupAgentRemote: agentRemoteExtras,
	GroupAIBridge:    agentRemoteExtras,
	GroupFS:          {"read", "write", "edit", "apply_patch"},
}

var ownerOnlyToolNames = map[string]struct{}{
	"whatsapp_login": {},
}

type toolProfilePolicy struct {
	Allow []string
	Deny  []string
}

// ToolProfiles define which tool groups each profile allows.
var ToolProfiles = map[ToolProfileID]toolProfilePolicy{
	ProfileSimple: {Allow: []string{"session_status", "web_search"}},
	// OpenClaw semantics: allow workspace tools + runtime + session tooling + memory + image.
	ProfileCoding: {Allow: []string{GroupFS, GroupRuntime, GroupSessions, GroupMemory, "image"}},
	// OpenClaw semantics: messaging + limited session inspection/sends.
	ProfileMessaging: {Allow: []string{GroupMessaging, "sessions_list", "sessions_history", "sessions_send", "session_status"}},
	ProfileFull:      {},
	ProfileBoss:      {},
}

// ToolPolicyConfig matches OpenClaw's allow/deny policy (global or per-agent).
type ToolPolicyConfig struct {
	Allow      []string                    `json:"allow,omitempty" yaml:"allow"`
	AlsoAllow  []string                    `json:"also_allow,omitempty" yaml:"also_allow"`
	Deny       []string                    `json:"deny,omitempty" yaml:"deny"`
	Profile    ToolProfileID               `json:"profile,omitempty" yaml:"profile"`
	ByProvider map[string]ToolPolicyConfig `json:"by_provider,omitempty" yaml:"by_provider"`
}

// GlobalToolPolicyConfig extends ToolPolicyConfig with subagent defaults.
type GlobalToolPolicyConfig struct {
	ToolPolicyConfig `yaml:",inline"`
	Subagents        *SubagentToolPolicyConfig `json:"subagents,omitempty" yaml:"subagents"`
}

// SubagentToolPolicyConfig configures subagent tool defaults.
type SubagentToolPolicyConfig struct {
	Tools *ToolPolicyConfig `json:"tools,omitempty" yaml:"tools"`
}

// ToolPolicy is a resolved allow/deny policy.
type ToolPolicy struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// clonePolicyLists returns shallow copies of the Allow/AlsoAllow/Deny slices.
func clonePolicyLists(src ToolPolicyConfig) ToolPolicyConfig {
	out := ToolPolicyConfig{Profile: src.Profile}
	out.Allow = slices.Clone(src.Allow)
	out.AlsoAllow = slices.Clone(src.AlsoAllow)
	out.Deny = slices.Clone(src.Deny)
	return out
}

// Clone creates a deep copy of ToolPolicyConfig.
func (c *ToolPolicyConfig) Clone() *ToolPolicyConfig {
	if c == nil {
		return nil
	}
	out := clonePolicyLists(*c)
	if len(c.ByProvider) > 0 {
		out.ByProvider = make(map[string]ToolPolicyConfig, len(c.ByProvider))
		for key, value := range c.ByProvider {
			clone := clonePolicyLists(value)
			if len(value.ByProvider) > 0 {
				clone.ByProvider = make(map[string]ToolPolicyConfig, len(value.ByProvider))
				for subKey, subVal := range value.ByProvider {
					subClone := clonePolicyLists(subVal)
					subClone.ByProvider = nil
					clone.ByProvider[subKey] = subClone
				}
			}
			out.ByProvider[key] = clone
		}
	}
	return &out
}

// NormalizeToolName converts to lowercase without accepting legacy aliases.
func NormalizeToolName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// IsOwnerOnlyToolName reports whether the tool is restricted to owners.
func IsOwnerOnlyToolName(name string) bool {
	normalized := NormalizeToolName(name)
	if normalized == "" {
		return false
	}
	_, ok := ownerOnlyToolNames[normalized]
	return ok
}

// NormalizeToolList normalizes each tool name in a list.
func NormalizeToolList(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		normalized := NormalizeToolName(entry)
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

// ExpandToolGroups expands group shorthands to tool names.
func ExpandToolGroups(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	normalized := NormalizeToolList(list)
	expanded := make([]string, 0, len(normalized))
	for _, value := range normalized {
		if group, ok := ToolGroups[value]; ok {
			expanded = append(expanded, group...)
			continue
		}
		expanded = append(expanded, value)
	}
	return stringutil.DedupeStrings(expanded)
}

// ResolveToolProfilePolicy returns the allow/deny lists for a profile.
func ResolveToolProfilePolicy(profile ToolProfileID) *ToolPolicy {
	if profile == "" {
		return nil
	}
	policy, ok := ToolProfiles[profile]
	if !ok {
		return nil
	}
	if len(policy.Allow) == 0 && len(policy.Deny) == 0 {
		return nil
	}
	return &ToolPolicy{
		Allow: slices.Clone(policy.Allow),
		Deny:  slices.Clone(policy.Deny),
	}
}

// MergeAlsoAllow appends also_allow into an allowlist if present.
func MergeAlsoAllow(policy *ToolPolicy, also_allow []string) *ToolPolicy {
	if policy == nil || len(also_allow) == 0 {
		return policy
	}
	if len(policy.Allow) == 0 {
		return policy
	}
	merged := slices.Clone(policy.Allow)
	merged = append(merged, also_allow...)
	return &ToolPolicy{
		Allow: stringutil.DedupeStrings(merged),
		Deny:  slices.Clone(policy.Deny),
	}
}

func unionAllow(base []string, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	if len(base) == 0 {
		return stringutil.DedupeStrings(append([]string{"*"}, extra...))
	}
	return stringutil.DedupeStrings(append(base, extra...))
}

// PickToolPolicy merges allow/also_allow/deny into a resolved policy.
func PickToolPolicy(config *ToolPolicyConfig) *ToolPolicy {
	if config == nil {
		return nil
	}
	allow := config.Allow
	if len(config.AlsoAllow) > 0 {
		allow = unionAllow(allow, config.AlsoAllow)
	}
	deny := config.Deny
	if len(allow) == 0 && len(deny) == 0 {
		return nil
	}
	return &ToolPolicy{
		Allow: slices.Clone(allow),
		Deny:  slices.Clone(deny),
	}
}

// EffectiveToolPolicy collects all resolved tool policies for evaluation.
type EffectiveToolPolicy struct {
	GlobalPolicy         *ToolPolicy
	GlobalProviderPolicy *ToolPolicy
	AgentPolicy          *ToolPolicy
	AgentProviderPolicy  *ToolPolicy
	Profile              ToolProfileID
	ProviderProfile      ToolProfileID
	ProfileAlsoAllow     []string
	ProviderAlsoAllow    []string
}

// ResolveEffectiveToolPolicy resolves global and agent policies plus provider overrides.
func ResolveEffectiveToolPolicy(params struct {
	Global        *GlobalToolPolicyConfig
	Agent         *ToolPolicyConfig
	ModelProvider string
	ModelID       string
}) EffectiveToolPolicy {
	globalTools := params.Global
	agentTools := params.Agent
	globalPolicy := globalAsToolPolicy(globalTools)

	var profile ToolProfileID
	if agentTools != nil && agentTools.Profile != "" {
		profile = agentTools.Profile
	} else if globalTools != nil {
		profile = globalTools.Profile
	}

	var globalByProvider map[string]ToolPolicyConfig
	if globalTools != nil {
		globalByProvider = globalTools.ByProvider
	}
	var agentByProvider map[string]ToolPolicyConfig
	if agentTools != nil {
		agentByProvider = agentTools.ByProvider
	}
	providerPolicy := resolveProviderToolPolicy(globalByProvider, params.ModelProvider, params.ModelID)
	agentProviderPolicy := resolveProviderToolPolicy(agentByProvider, params.ModelProvider, params.ModelID)

	return EffectiveToolPolicy{
		GlobalPolicy:         PickToolPolicy(globalPolicy),
		GlobalProviderPolicy: PickToolPolicy(providerPolicy),
		AgentPolicy:          PickToolPolicy(agentTools),
		AgentProviderPolicy:  PickToolPolicy(agentProviderPolicy),
		Profile:              profile,
		ProviderProfile:      resolveProfileFromProvider(agentProviderPolicy, providerPolicy),
		ProfileAlsoAllow:     resolveAlsoAllow(agentTools, globalPolicy),
		ProviderAlsoAllow:    resolveAlsoAllow(agentProviderPolicy, providerPolicy),
	}
}

func resolveProfileFromProvider(agentProvider, globalProvider *ToolPolicyConfig) ToolProfileID {
	if agentProvider != nil && agentProvider.Profile != "" {
		return agentProvider.Profile
	}
	if globalProvider != nil {
		return globalProvider.Profile
	}
	return ""
}

func resolveAlsoAllow(agent *ToolPolicyConfig, global *ToolPolicyConfig) []string {
	if agent != nil && len(agent.AlsoAllow) > 0 {
		return agent.AlsoAllow
	}
	if global != nil {
		return global.AlsoAllow
	}
	return nil
}

func globalAsToolPolicy(global *GlobalToolPolicyConfig) *ToolPolicyConfig {
	if global == nil {
		return nil
	}
	return &global.ToolPolicyConfig
}

func resolveProviderToolPolicy(by_provider map[string]ToolPolicyConfig, provider string, modelID string) *ToolPolicyConfig {
	if provider == "" || len(by_provider) == 0 {
		return nil
	}
	lookup := make(map[string]ToolPolicyConfig, len(by_provider))
	for key, value := range by_provider {
		if normalized := NormalizeToolName(key); normalized != "" {
			lookup[normalized] = value
		}
	}

	normalizedProvider := NormalizeToolName(provider)
	rawModel := strings.ToLower(strings.TrimSpace(modelID))

	// Try full model path first (e.g. "anthropic/claude-sonnet-4.5"), then provider alone.
	if rawModel != "" {
		fullModel := rawModel
		if !strings.Contains(rawModel, "/") {
			fullModel = normalizedProvider + "/" + rawModel
		}
		if match, ok := lookup[fullModel]; ok {
			return &match
		}
	}
	if match, ok := lookup[normalizedProvider]; ok {
		return &match
	}
	return nil
}

// compilePatterns expands tool groups and compiles glob patterns.
func compilePatterns(patterns []string) []globmatch.Pattern {
	return globmatch.CompileAll(ExpandToolGroups(patterns))
}

func makeToolPolicyMatcher(policy *ToolPolicy) func(string) bool {
	return globmatch.MakePredicate(compilePatterns(policy.Allow), compilePatterns(policy.Deny))
}

// FilterToolsByPolicy filters tools by policy.
func FilterToolsByPolicy(names []string, policy *ToolPolicy) []string {
	if policy == nil {
		return names
	}
	matcher := makeToolPolicyMatcher(policy)
	var result []string
	for _, name := range names {
		if matcher(name) {
			result = append(result, name)
		}
	}
	return result
}

var defaultSubagentDeny = []string{
	"sessions_list",
	"sessions_history",
	"sessions_send",
	"sessions_spawn",
	"session_status",
	"create_agent",
	"fork_agent",
	"edit_agent",
	"delete_agent",
	"list_agents",
	"agents_list",
	"list_models",
	"gateway",
	"cron",
	"run_internal_command",
	"modify_room",
	"memory_search",
	"memory_get",
	"gravatar_fetch",
	"gravatar_set",
}

// ResolveSubagentToolPolicy returns the default subagent policy (deny wins).
func ResolveSubagentToolPolicy(global *GlobalToolPolicyConfig) *ToolPolicy {
	deny := slices.Clone(defaultSubagentDeny)
	var allow []string
	if global != nil && global.Subagents != nil && global.Subagents.Tools != nil {
		deny = append(deny, global.Subagents.Tools.Deny...)
		allow = global.Subagents.Tools.Allow
	}
	return &ToolPolicy{
		Allow: allow,
		Deny:  deny,
	}
}

// PluginToolGroups tracks plugin tool groupings.
type PluginToolGroups struct {
	All      []string
	ByPlugin map[string][]string
}

// BuildPluginToolGroups groups tools by plugin id.
func BuildPluginToolGroups[T any](tools []T, toolName func(T) string, toolMeta func(T) (string, bool)) PluginToolGroups {
	var all []string
	byPlugin := make(map[string][]string)
	for _, tool := range tools {
		pluginID, ok := toolMeta(tool)
		if !ok {
			continue
		}
		name := NormalizeToolName(toolName(tool))
		if name == "" {
			continue
		}
		all = append(all, name)
		key := strings.ToLower(pluginID)
		byPlugin[key] = append(byPlugin[key], name)
	}
	return PluginToolGroups{All: all, ByPlugin: byPlugin}
}

// ExpandPluginGroups expands plugin group shorthands inside a list.
func ExpandPluginGroups(list []string, groups PluginToolGroups) []string {
	if len(list) == 0 {
		return list
	}
	expanded := make([]string, 0, len(list))
	for _, entry := range list {
		normalized := NormalizeToolName(entry)
		switch {
		case normalized == "group:plugins":
			if len(groups.All) > 0 {
				expanded = append(expanded, groups.All...)
			} else {
				expanded = append(expanded, normalized)
			}
		case groups.ByPlugin != nil:
			if tools, ok := groups.ByPlugin[normalized]; ok && len(tools) > 0 {
				expanded = append(expanded, tools...)
			} else {
				expanded = append(expanded, normalized)
			}
		default:
			expanded = append(expanded, normalized)
		}
	}
	return stringutil.DedupeStrings(expanded)
}

// ExpandPolicyWithPluginGroups expands plugin group shorthands inside a policy.
func ExpandPolicyWithPluginGroups(policy *ToolPolicy, groups PluginToolGroups) *ToolPolicy {
	if policy == nil {
		return nil
	}
	return &ToolPolicy{
		Allow: ExpandPluginGroups(policy.Allow, groups),
		Deny:  ExpandPluginGroups(policy.Deny, groups),
	}
}

// StripPluginOnlyAllowlist removes allowlists that only target plugin tools.
func StripPluginOnlyAllowlist(policy *ToolPolicy, groups PluginToolGroups, coreTools map[string]struct{}) (bool, []string, *ToolPolicy) {
	if policy == nil || len(policy.Allow) == 0 {
		return false, nil, policy
	}
	normalized := NormalizeToolList(policy.Allow)
	if len(normalized) == 0 {
		return false, nil, policy
	}

	pluginIDs := map[string]struct{}{}
	for id := range groups.ByPlugin {
		pluginIDs[id] = struct{}{}
	}
	pluginTools := map[string]struct{}{}
	for _, tool := range groups.All {
		pluginTools[tool] = struct{}{}
	}

	var unknownAllowlist []string
	hasCoreEntry := false
	for _, entry := range normalized {
		if entry == "*" {
			hasCoreEntry = true
			continue
		}
		_, isPluginID := pluginIDs[entry]
		_, isPluginTool := pluginTools[entry]
		isPluginEntry := entry == "group:plugins" || isPluginID || isPluginTool
		expanded := ExpandToolGroups([]string{entry})
		isCoreEntry := false
		for _, name := range expanded {
			if _, ok := coreTools[name]; ok {
				isCoreEntry = true
				break
			}
		}
		if isCoreEntry {
			hasCoreEntry = true
		}
		if !isCoreEntry && !isPluginEntry {
			unknownAllowlist = append(unknownAllowlist, entry)
		}
	}

	stripped := !hasCoreEntry
	if stripped {
		return true, stringutil.DedupeStrings(unknownAllowlist), &ToolPolicy{
			Allow: nil,
			Deny:  slices.Clone(policy.Deny),
		}
	}
	return false, stringutil.DedupeStrings(unknownAllowlist), policy
}
