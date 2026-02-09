package toolpolicy

import (
	"regexp"
	"slices"
	"strings"
)

// ToolProfileID defines access levels (OpenClaw-style).
type ToolProfileID string

const (
	ProfileMinimal   ToolProfileID = "minimal"
	ProfileCoding    ToolProfileID = "coding"
	ProfileMessaging ToolProfileID = "messaging"
	ProfileFull      ToolProfileID = "full"
	ProfileBoss      ToolProfileID = "boss"
)

// Tool group constants for policy composition (OpenClaw-style shorthands).
const (
	GroupSearch     = "group:search"
	GroupCalc       = "group:calc"
	GroupBuilder    = "group:builder"
	GroupMessaging  = "group:messaging"
	GroupSessions   = "group:sessions"
	GroupMemory     = "group:memory"
	GroupWeb        = "group:web"
	GroupMedia      = "group:media"
	GroupUI         = "group:ui"
	GroupAutomation = "group:automation"
	GroupNodes      = "group:nodes"
	GroupStatus     = "group:status"
	GroupOpenClaw   = "group:openclaw"
	GroupFS         = "group:fs"
	GroupNexus      = "group:nexus"
)

// ToolGroups maps group names to tool names for policy composition.
var ToolGroups = map[string][]string{
	GroupSearch:     {"web_search"},
	GroupCalc:       {"calculator"},
	GroupBuilder:    {"create_agent", "fork_agent", "edit_agent", "delete_agent", "list_agents", "run_internal_command"},
	GroupMessaging:  {"message"},
	GroupSessions:   {"agents_list", "list_models", "list_tools", "modify_room", "sessions_list", "sessions_history", "sessions_send", "sessions_spawn", "session_status"},
	GroupMemory:     {"memory_search", "memory_get"},
	GroupWeb:        {"web_search", "web_fetch"},
	GroupMedia:      {"image", "image_generate", "tts"},
	GroupUI:         {"browser", "canvas"},
	GroupAutomation: {"cron", "gateway"},
	GroupNodes:      {"nodes"},
	GroupStatus:     {"session_status"},
	GroupOpenClaw:   {"message", "agents_list", "list_models", "list_tools", "modify_room", "sessions_list", "sessions_history", "sessions_send", "sessions_spawn", "session_status", "memory_search", "memory_get", "web_search", "web_fetch", "image", "gravatar_fetch", "beeper_docs"},
	GroupFS:         {"read", "write", "edit", "apply_patch"},
	GroupNexus: {
		"get_user_information",
		"contacts",
		"searchContacts",
		"getContact",
		"createContact",
		"updateContact",
		"archive_contact",
		"restore_contact",
		"createNote",
		"getGroups",
		"createGroup",
		"updateGroup",
		"getNotes",
		"getEvents",
		"getUpcomingEvents",
		"getEmails",
		"getRecentEmails",
		"getRecentReminders",
		"getUpcomingReminders",
		"find_duplicates",
		"merge_contacts",
	},
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
	ProfileMinimal:   {Allow: []string{"session_status"}},
	ProfileCoding:    {Allow: []string{GroupFS, GroupOpenClaw}},
	ProfileMessaging: {Allow: []string{GroupOpenClaw}},
	ProfileFull:      {},
	ProfileBoss:      {},
}

// ToolPolicyConfig matches OpenClaw's allow/deny policy (global or per-agent).
type ToolPolicyConfig struct {
	Allow      []string                    `json:"allow,omitempty" yaml:"allow"`
	AlsoAllow  []string                    `json:"alsoAllow,omitempty" yaml:"alsoAllow"`
	Deny       []string                    `json:"deny,omitempty" yaml:"deny"`
	Profile    ToolProfileID               `json:"profile,omitempty" yaml:"profile"`
	ByProvider map[string]ToolPolicyConfig `json:"byProvider,omitempty" yaml:"byProvider"`
}

// GlobalToolPolicyConfig extends ToolPolicyConfig with subagent defaults.
type GlobalToolPolicyConfig struct {
	Allow      []string                    `json:"allow,omitempty" yaml:"allow"`
	AlsoAllow  []string                    `json:"alsoAllow,omitempty" yaml:"alsoAllow"`
	Deny       []string                    `json:"deny,omitempty" yaml:"deny"`
	Profile    ToolProfileID               `json:"profile,omitempty" yaml:"profile"`
	ByProvider map[string]ToolPolicyConfig `json:"byProvider,omitempty" yaml:"byProvider"`
	Subagents  *SubagentToolPolicyConfig   `json:"subagents,omitempty" yaml:"subagents"`
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

// Clone creates a deep copy of ToolPolicyConfig.
func (c *ToolPolicyConfig) Clone() *ToolPolicyConfig {
	if c == nil {
		return nil
	}
	out := &ToolPolicyConfig{
		Profile: c.Profile,
	}
	if len(c.Allow) > 0 {
		out.Allow = slices.Clone(c.Allow)
	}
	if len(c.AlsoAllow) > 0 {
		out.AlsoAllow = slices.Clone(c.AlsoAllow)
	}
	if len(c.Deny) > 0 {
		out.Deny = slices.Clone(c.Deny)
	}
	if len(c.ByProvider) > 0 {
		out.ByProvider = make(map[string]ToolPolicyConfig, len(c.ByProvider))
		for key, value := range c.ByProvider {
			clone := value
			if len(value.Allow) > 0 {
				clone.Allow = slices.Clone(value.Allow)
			}
			if len(value.AlsoAllow) > 0 {
				clone.AlsoAllow = slices.Clone(value.AlsoAllow)
			}
			if len(value.Deny) > 0 {
				clone.Deny = slices.Clone(value.Deny)
			}
			if len(value.ByProvider) > 0 {
				clone.ByProvider = make(map[string]ToolPolicyConfig, len(value.ByProvider))
				for subKey, subVal := range value.ByProvider {
					subClone := subVal
					if len(subVal.Allow) > 0 {
						subClone.Allow = slices.Clone(subVal.Allow)
					}
					if len(subVal.AlsoAllow) > 0 {
						subClone.AlsoAllow = slices.Clone(subVal.AlsoAllow)
					}
					if len(subVal.Deny) > 0 {
						subClone.Deny = slices.Clone(subVal.Deny)
					}
					subClone.ByProvider = nil
					clone.ByProvider[subKey] = subClone
				}
			}
			out.ByProvider[key] = clone
		}
	}
	return out
}

var toolNameAliases = map[string]string{
	"apply-patch": "apply_patch",
}

// NormalizeToolName converts to lowercase and resolves aliases.
func NormalizeToolName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return ""
	}
	if alias, ok := toolNameAliases[normalized]; ok {
		return alias
	}
	return normalized
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

// ApplyOwnerOnlyToolPolicy filters owner-only tools when senderIsOwner is false.
func ApplyOwnerOnlyToolPolicy(names []string, senderIsOwner bool) []string {
	if senderIsOwner || len(names) == 0 {
		return names
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if !IsOwnerOnlyToolName(name) {
			filtered = append(filtered, name)
		}
	}
	return filtered
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
	return uniqueStrings(expanded)
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

// MergeAlsoAllow appends alsoAllow into an allowlist if present.
func MergeAlsoAllow(policy *ToolPolicy, alsoAllow []string) *ToolPolicy {
	if policy == nil || len(alsoAllow) == 0 {
		return policy
	}
	if len(policy.Allow) == 0 {
		return policy
	}
	merged := slices.Clone(policy.Allow)
	merged = append(merged, alsoAllow...)
	return &ToolPolicy{
		Allow: uniqueStrings(merged),
		Deny:  slices.Clone(policy.Deny),
	}
}

func unionAllow(base []string, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	if len(base) == 0 {
		return uniqueStrings(append([]string{"*"}, extra...))
	}
	return uniqueStrings(append(base, extra...))
}

// PickToolPolicy merges allow/alsoAllow/deny into a resolved policy.
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

	profile := ToolProfileID("")
	if agentTools != nil && agentTools.Profile != "" {
		profile = agentTools.Profile
	} else if globalTools != nil {
		profile = globalTools.Profile
	}

	providerPolicy := resolveProviderToolPolicy(globalTools, params.ModelProvider, params.ModelID)
	agentProviderPolicy := resolveProviderToolPolicy(agentTools, params.ModelProvider, params.ModelID)

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
	return &ToolPolicyConfig{
		Allow:      global.Allow,
		AlsoAllow:  global.AlsoAllow,
		Deny:       global.Deny,
		Profile:    global.Profile,
		ByProvider: global.ByProvider,
	}
}

func normalizeProviderKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func resolveProviderToolPolicy(base any, provider string, modelID string) *ToolPolicyConfig {
	if provider == "" || base == nil {
		return nil
	}
	var byProvider map[string]ToolPolicyConfig
	switch cfg := base.(type) {
	case *GlobalToolPolicyConfig:
		if cfg == nil {
			return nil
		}
		byProvider = cfg.ByProvider
	case *ToolPolicyConfig:
		if cfg == nil {
			return nil
		}
		byProvider = cfg.ByProvider
	}
	if len(byProvider) == 0 {
		return nil
	}
	lookup := make(map[string]ToolPolicyConfig, len(byProvider))
	for key, value := range byProvider {
		normalized := normalizeProviderKey(key)
		if normalized == "" {
			continue
		}
		lookup[normalized] = value
	}

	normalizedProvider := normalizeProviderKey(provider)
	rawModel := strings.ToLower(strings.TrimSpace(modelID))
	fullModel := rawModel
	if rawModel != "" && !strings.Contains(rawModel, "/") {
		fullModel = normalizedProvider + "/" + rawModel
	}

	var candidates []string
	if fullModel != "" {
		candidates = append(candidates, fullModel)
	}
	if normalizedProvider != "" {
		candidates = append(candidates, normalizedProvider)
	}

	for _, key := range candidates {
		if match, ok := lookup[key]; ok {
			return &match
		}
	}
	return nil
}

type compiledPattern struct {
	kind  string
	value string
	re    *regexp.Regexp
}

func compilePattern(pattern string) compiledPattern {
	normalized := NormalizeToolName(pattern)
	if normalized == "" {
		return compiledPattern{kind: "exact", value: ""}
	}
	if normalized == "*" {
		return compiledPattern{kind: "all"}
	}
	if !strings.Contains(normalized, "*") {
		return compiledPattern{kind: "exact", value: normalized}
	}
	escaped := regexp.QuoteMeta(normalized)
	re := regexp.MustCompile("^" + strings.ReplaceAll(escaped, "\\*", ".*") + "$")
	return compiledPattern{kind: "regex", re: re}
}

func compilePatterns(patterns []string) []compiledPattern {
	if len(patterns) == 0 {
		return nil
	}
	expanded := ExpandToolGroups(patterns)
	compiled := make([]compiledPattern, 0, len(expanded))
	for _, pattern := range expanded {
		entry := compilePattern(pattern)
		if entry.kind == "exact" && entry.value == "" {
			continue
		}
		compiled = append(compiled, entry)
	}
	return compiled
}

func matchesAny(name string, patterns []compiledPattern) bool {
	for _, pattern := range patterns {
		switch pattern.kind {
		case "all":
			return true
		case "exact":
			if name == pattern.value {
				return true
			}
		case "regex":
			if pattern.re != nil && pattern.re.MatchString(name) {
				return true
			}
		}
	}
	return false
}

func makeToolPolicyMatcher(policy *ToolPolicy) func(string) bool {
	deny := compilePatterns(policy.Deny)
	allow := compilePatterns(policy.Allow)
	return func(name string) bool {
		normalized := NormalizeToolName(name)
		if matchesAny(normalized, deny) {
			return false
		}
		if len(allow) == 0 {
			return true
		}
		if matchesAny(normalized, allow) {
			return true
		}
		return false
	}
}

// IsToolAllowedByPolicyName checks if a tool is allowed by a single policy.
func IsToolAllowedByPolicyName(name string, policy *ToolPolicy) bool {
	if policy == nil {
		return true
	}
	return makeToolPolicyMatcher(policy)(name)
}

// IsToolAllowedByPolicies checks if a tool is allowed by all policies.
func IsToolAllowedByPolicies(name string, policies []*ToolPolicy) bool {
	for _, policy := range policies {
		if !IsToolAllowedByPolicyName(name, policy) {
			return false
		}
	}
	return true
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
	"list_tools",
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
	if global != nil && global.Subagents != nil && global.Subagents.Tools != nil {
		if len(global.Subagents.Tools.Deny) > 0 {
			deny = append(deny, global.Subagents.Tools.Deny...)
		}
	}
	var allow []string
	if global != nil && global.Subagents != nil && global.Subagents.Tools != nil {
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
	return uniqueStrings(expanded)
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

// CollectExplicitAllowlist returns all explicit allowlist entries.
func CollectExplicitAllowlist(policies []*ToolPolicy) []string {
	var entries []string
	for _, policy := range policies {
		if policy == nil || len(policy.Allow) == 0 {
			continue
		}
		for _, value := range policy.Allow {
			trimmed := strings.TrimSpace(value)
			if trimmed != "" {
				entries = append(entries, trimmed)
			}
		}
	}
	return entries
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
		isPluginEntry := entry == "group:plugins"
		if !isPluginEntry {
			if _, ok := pluginIDs[entry]; ok {
				isPluginEntry = true
			}
			if _, ok := pluginTools[entry]; ok {
				isPluginEntry = true
			}
		}
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
		return true, uniqueStrings(unknownAllowlist), &ToolPolicy{
			Allow: nil,
			Deny:  slices.Clone(policy.Deny),
		}
	}
	return false, uniqueStrings(unknownAllowlist), policy
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
