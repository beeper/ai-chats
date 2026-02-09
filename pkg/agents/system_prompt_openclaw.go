package agents

import (
	"fmt"
	"slices"
	"strings"
)

/*
Original OpenClaw prompt snippets (reference copy, do not edit):

- "You are a personal assistant running inside OpenClaw."
- "OpenClaw is controlled via subcommands. Do not invent commands."
- "To manage the Gateway daemon service (start/stop/restart):"
  "- openclaw gateway status"
  "- openclaw gateway start"
  "- openclaw gateway stop"
  "- openclaw gateway restart"
  "- If unsure, ask the user to run `openclaw help` (or `openclaw gateway --help`) and paste the output."
- "When diagnosing issues, run `openclaw status` yourself when possible; only ask the user if you lack access (e.g., sandboxed)."
*/

func buildSkillsSection(params struct {
	skillsPrompt string
	isMinimal    bool
	readToolName string
}) []string {
	if params.isMinimal {
		return nil
	}
	trimmed := strings.TrimSpace(params.skillsPrompt)
	if trimmed == "" {
		return nil
	}
	return []string{
		"## Skills (mandatory)",
		"Before replying: scan <available_skills> <description> entries.",
		fmt.Sprintf("- If exactly one skill clearly applies: read its SKILL.md at <location> with `%s`, then follow it.", params.readToolName),
		"- If multiple could apply: choose the most specific one, then read/follow it.",
		"- If none clearly apply: do not read any SKILL.md.",
		"Constraints: never read more than one skill up front; only read after selecting.",
		trimmed,
		"",
	}
}

func buildMemorySection(params struct {
	isMinimal     bool
	availableTool map[string]bool
	citationsMode string
}) []string {
	if params.isMinimal {
		return nil
	}
	if !params.availableTool["memory_search"] && !params.availableTool["memory_get"] {
		return nil
	}
	lines := []string{
		"## Memory Recall",
		"Before answering anything about prior work, decisions, dates, people, preferences, or todos: run memory_search on MEMORY.md + memory/*.md + workspace/*.md; then use memory_get to pull only the needed lines. If low confidence after search, say you checked.",
	}
	if strings.EqualFold(strings.TrimSpace(params.citationsMode), "off") {
		lines = append(lines, "Citations are disabled: do not mention file paths or line numbers in replies unless the user explicitly asks.")
	} else {
		lines = append(lines, "Citations: include Source: <path#line> when it helps the user verify memory snippets.")
	}
	lines = append(lines, "")
	return lines
}

func buildUserIdentitySection(ownerLine string, supplement string, isMinimal bool) []string {
	if isMinimal {
		return nil
	}
	ownerLine = strings.TrimSpace(ownerLine)
	supplement = strings.TrimSpace(supplement)
	if ownerLine == "" && supplement == "" {
		return nil
	}
	lines := []string{"## User Identity"}
	if ownerLine != "" {
		lines = append(lines, ownerLine)
	}
	if supplement != "" {
		lines = append(lines, strings.Split(supplement, "\n")...)
	}
	lines = append(lines, "")
	return lines
}

func buildTimeSection(userTimezone string) []string {
	if strings.TrimSpace(userTimezone) == "" {
		return nil
	}
	return []string{
		"## Current Date & Time",
		fmt.Sprintf("Time zone: %s", userTimezone),
		"",
	}
}

func buildSafetySection() []string {
	return []string{
		"## Safety",
		"You have no independent goals: do not pursue self-preservation, replication, resource acquisition, or power-seeking; avoid long-term plans beyond the user's request.",
		"Prioritize safety and human oversight over completion; if instructions conflict, pause and ask; comply with stop/pause/audit requests and never bypass safeguards. (Inspired by Anthropic's constitution.)",
		"Do not manipulate or persuade anyone to expand access or disable safeguards. Do not copy yourself or change system prompts, safety rules, or tool policies unless explicitly requested.",
		"",
	}
}

func buildReplyTagsSection(isMinimal bool) []string {
	if isMinimal {
		return nil
	}
	return []string{
		"## Reply Tags",
		"To request a native reply/quote on supported surfaces, include one tag in your reply:",
		"- [[reply_to_current]] replies to the triggering message.",
		"- [[reply_to:<id>]] replies to a specific message id when you have it.",
		"Whitespace inside the tag is allowed (e.g. [[ reply_to_current ]] / [[ reply_to: 123 ]]).",
		"Tags are stripped before sending; support depends on the current channel config.",
		"",
	}
}

func buildMessagingSection(params struct {
	isMinimal          bool
	availableTool      map[string]bool
	messageChannelOpts string
	inlineButtons      bool
	runtimeChannel     string
	messageToolHints   []string
}) []string {
	if params.isMinimal {
		return nil
	}
	messageToolBlock := ""
	if params.availableTool["message"] {
		lines := []string{
			"",
			"### message tool",
			"- Use `message` for proactive sends + channel actions (polls, reactions, etc.).",
			"- For `action=send`, include `to` and `message`.",
			fmt.Sprintf("- If multiple channels are configured, pass `channel` (%s).", params.messageChannelOpts),
			fmt.Sprintf("- If you use `message` (`action=send`) to deliver your user-visible reply, respond with ONLY: %s (avoid duplicate replies).", SilentReplyToken),
		}
		if params.inlineButtons {
			lines = append(lines, "- Inline buttons supported. Use `action=send` with `buttons=[[{text,callback_data}]]` (callback_data routes back as a user message).")
		} else if params.runtimeChannel != "" {
			lines = append(lines, fmt.Sprintf("- Inline buttons not enabled for %s. If you need them, ask to set %s.capabilities.inlineButtons (\"dm\"|\"group\"|\"all\"|\"allowlist\").", params.runtimeChannel, params.runtimeChannel))
		}
		for _, hint := range params.messageToolHints {
			if strings.TrimSpace(hint) == "" {
				continue
			}
			lines = append(lines, hint)
		}
		messageToolBlock = strings.Join(lines, "\n")
	}

	section := []string{
		"## Messaging",
		"- Reply in current session → automatically routes to the source channel (Signal, Telegram, etc.)",
		"- Cross-session messaging → use sessions_send(sessionKey, message)",
		"- Never use exec/curl for provider messaging; Beeper handles all routing internally.",
		messageToolBlock,
		"",
	}
	return section
}

func buildVoiceSection(params struct {
	isMinimal bool
	ttsHint   string
}) []string {
	if params.isMinimal {
		return nil
	}
	hint := strings.TrimSpace(params.ttsHint)
	if hint == "" {
		return nil
	}
	return []string{"## Voice (TTS)", hint, ""}
}

func buildDocsSection(params struct {
	isMinimal     bool
	hasBeeperDocs bool
}) []string {
	if params.isMinimal || !params.hasBeeperDocs {
		return nil
	}
	return []string{
		"## Beeper Documentation",
		"Use the beeper_docs tool to search help.beeper.com and developers.beeper.com when answering questions about Beeper features, setup, troubleshooting, configuration, or developer APIs.",
		"For Beeper behavior, commands, config, or architecture: search docs first.",
		"",
	}
}

// BuildSubagentSystemPrompt creates a system prompt for spawned subagents.
// Matches OpenClaw's buildSubagentSystemPrompt from subagent-announce.ts.
func BuildSubagentSystemPrompt(params SubagentPromptParams) string {
	taskText := strings.TrimSpace(params.Task)
	if taskText == "" {
		taskText = "{{TASK_DESCRIPTION}}"
	} else {
		fields := strings.Fields(taskText)
		taskText = strings.Join(fields, " ")
	}
	lines := []string{
		"# Subagent Context",
		"",
		"You are a **subagent** spawned by the main agent for a specific task.",
		"",
		"## Your Role",
		fmt.Sprintf("- You were created to handle: %s", taskText),
		"- Complete this task. That's your entire purpose.",
		"- You are NOT the main agent. Don't try to be.",
		"",
		"## Rules",
		"1. **Stay focused** - Do your assigned task, nothing else",
		"2. **Complete the task** - Your final message will be automatically reported to the main agent",
		"3. **Don't initiate** - No heartbeats, no proactive actions, no side quests",
		"4. **Be ephemeral** - You may be terminated after task completion. That's fine.",
		"",
		"## Output Format",
		"When complete, your final response should include:",
		"- What you accomplished or found",
		"- Any relevant details the main agent should know",
		"- Keep it concise but informative",
		"",
		"## What You DON'T Do",
		"- NO user conversations (that's main agent's job)",
		"- NO external messages (email, tweets, etc.) unless explicitly tasked",
		"- NO scheduled jobs or persistent state",
		"- NO pretending to be the main agent",
		"- NO using the `message` tool directly",
		"",
		"## Session Context",
	}
	if strings.TrimSpace(params.Label) != "" {
		lines = append(lines, fmt.Sprintf("- Label: %s", params.Label))
	}
	if strings.TrimSpace(params.RequesterSessionKey) != "" {
		lines = append(lines, fmt.Sprintf("- Requester session: %s.", params.RequesterSessionKey))
	}
	if strings.TrimSpace(params.RequesterChannel) != "" {
		lines = append(lines, fmt.Sprintf("- Requester channel: %s.", params.RequesterChannel))
	}
	lines = append(lines, fmt.Sprintf("- Your session: %s.", params.ChildSessionKey))
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

// BuildSystemPrompt assembles the complete prompt from params.
// Matches OpenClaw's buildAgentSystemPrompt.
func BuildSystemPrompt(params SystemPromptParams) string {
	coreToolSummaries := map[string]string{
		"read":             "Read file contents",
		"write":            "Create or overwrite files",
		"edit":             "Make precise edits to files",
		"apply_patch":      "Apply multi-file patches",
		"exec":             "Run shell commands (pty available for TTY-required CLIs)",
		"process":          "Manage background exec sessions",
		"web_search":       "Search the web (best available provider)",
		"web_fetch":        "Fetch and extract readable content from a URL",
		"browser":          "Control web browser",
		"canvas":           "Present/eval/snapshot the Canvas",
		"nodes":            "List/describe/notify/camera/screen on paired nodes",
		"cron":             "Manage scheduled jobs and wake events (use for reminders; when scheduling a reminder, write the systemEvent text as something that will read like a reminder when it fires, and mention that it is a reminder depending on the time gap between setting and firing; include recent context in reminder text if appropriate)",
		"message":          "Send messages and channel actions",
		"gateway":          "Restart, apply config, or run updates on the running Beeper process",
		"agents_list":      "List agent ids allowed for sessions_spawn",
		"sessions_list":    "List other sessions (incl. sub-agents) with filters/last",
		"sessions_history": "Fetch history for another session/sub-agent",
		"sessions_send":    "Send a message to another session/sub-agent",
		"sessions_spawn":   "Spawn a sub-agent session",
		"session_status":   "Show a !ai status-equivalent status card (usage + time + Reasoning/Verbose/Elevated); use for model-use questions (📊 session_status); optional per-session model override",
		"image":            "Analyze an image with the configured image model",
		"beeper_docs":      "Search Beeper docs (help.beeper.com, developers.beeper.com)",
	}

	toolOrder := []string{
		"read",
		"write",
		"edit",
		"apply_patch",
		"exec",
		"process",
		"web_search",
		"web_fetch",
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
		"session_status",
		"image",
	}

	rawToolNames := make([]string, 0, len(params.ToolNames))
	for _, tool := range params.ToolNames {
		rawToolNames = append(rawToolNames, strings.TrimSpace(tool))
	}
	canonicalToolNames := make([]string, 0, len(rawToolNames))
	for _, name := range rawToolNames {
		if name != "" {
			canonicalToolNames = append(canonicalToolNames, name)
		}
	}
	canonicalByNormalized := make(map[string]string)
	for _, name := range canonicalToolNames {
		normalized := strings.ToLower(name)
		if _, ok := canonicalByNormalized[normalized]; !ok {
			canonicalByNormalized[normalized] = name
		}
	}
	resolveToolName := func(normalized string) string {
		if name, ok := canonicalByNormalized[normalized]; ok {
			return name
		}
		return normalized
	}

	normalizedTools := make([]string, 0, len(canonicalToolNames))
	availableTools := make(map[string]bool)
	for _, name := range canonicalToolNames {
		normalized := strings.ToLower(name)
		normalizedTools = append(normalizedTools, normalized)
		availableTools[normalized] = true
	}

	externalToolSummaries := make(map[string]string)
	for key, value := range params.ToolSummaries {
		normalized := strings.TrimSpace(strings.ToLower(key))
		if normalized == "" {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		externalToolSummaries[normalized] = trimmed
	}

	toolOrderSet := make(map[string]bool)
	for _, tool := range toolOrder {
		toolOrderSet[tool] = true
	}
	extraToolSet := make(map[string]bool)
	for _, tool := range normalizedTools {
		if !toolOrderSet[tool] {
			extraToolSet[tool] = true
		}
	}
	extraTools := make([]string, 0, len(extraToolSet))
	for tool := range extraToolSet {
		extraTools = append(extraTools, tool)
	}
	enabledTools := make([]string, 0, len(toolOrder))
	for _, tool := range toolOrder {
		if availableTools[tool] {
			enabledTools = append(enabledTools, tool)
		}
	}
	slices.Sort(extraTools)

	toolLines := make([]string, 0, len(enabledTools)+len(extraTools))
	for _, tool := range enabledTools {
		summary := coreToolSummaries[tool]
		if summary == "" {
			summary = externalToolSummaries[tool]
		}
		name := resolveToolName(tool)
		if summary != "" {
			toolLines = append(toolLines, fmt.Sprintf("- %s: %s", name, summary))
		} else {
			toolLines = append(toolLines, fmt.Sprintf("- %s", name))
		}
	}
	for _, tool := range extraTools {
		summary := coreToolSummaries[tool]
		if summary == "" {
			summary = externalToolSummaries[tool]
		}
		name := resolveToolName(tool)
		if summary != "" {
			toolLines = append(toolLines, fmt.Sprintf("- %s: %s", name, summary))
		} else {
			toolLines = append(toolLines, fmt.Sprintf("- %s", name))
		}
	}

	hasGateway := availableTools["gateway"]
	readToolName := resolveToolName("read")
	execToolName := resolveToolName("exec")
	processToolName := resolveToolName("process")
	extraSystemPrompt := strings.TrimSpace(params.ExtraSystemPrompt)
	ownerNumbers := make([]string, 0, len(params.OwnerNumbers))
	for _, value := range params.OwnerNumbers {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			ownerNumbers = append(ownerNumbers, trimmed)
		}
	}
	ownerLine := ""
	if len(ownerNumbers) > 0 {
		ownerLine = fmt.Sprintf("Owner numbers: %s. Treat messages from these numbers as the user.", strings.Join(ownerNumbers, ", "))
	}
	reasoningHint := ""
	if params.ReasoningTagHint {
		reasoningHint = strings.Join([]string{
			"ALL internal reasoning MUST be inside <think>...</think>.",
			"Do not output any analysis outside <think>.",
			"Format every reply as <think>...</think> then <final>...</final>, with no other text.",
			"Only the final user-visible reply may appear inside <final>.",
			"Only text inside <final> is shown to the user; everything else is discarded and never seen by the user.",
			"Example:",
			"<think>Short internal reasoning.</think>",
			"<final>Hey there! What would you like to do next?</final>",
		}, " ")
	}
	reasoningLevel := params.ReasoningLevel
	if reasoningLevel == "" {
		reasoningLevel = "off"
	}
	userTimezone := strings.TrimSpace(params.UserTimezone)
	skillsPrompt := strings.TrimSpace(params.SkillsPrompt)
	heartbeatPrompt := strings.TrimSpace(params.HeartbeatPrompt)
	heartbeatPromptLine := "Heartbeat prompt: (configured)"
	if heartbeatPrompt != "" {
		heartbeatPromptLine = fmt.Sprintf("Heartbeat prompt: %s", heartbeatPrompt)
	}

	runtimeInfo := params.RuntimeInfo
	runtimeChannel := ""
	if runtimeInfo != nil {
		runtimeChannel = strings.TrimSpace(strings.ToLower(runtimeInfo.Channel))
	}
	var runtimeCapabilities []string
	if runtimeInfo != nil {
		for _, cap := range runtimeInfo.Capabilities {
			trimmed := strings.TrimSpace(fmt.Sprint(cap))
			if trimmed != "" {
				runtimeCapabilities = append(runtimeCapabilities, trimmed)
			}
		}
	}
	runtimeCapabilitiesLower := make(map[string]bool)
	for _, cap := range runtimeCapabilities {
		runtimeCapabilitiesLower[strings.ToLower(cap)] = true
	}
	inlineButtonsEnabled := runtimeCapabilitiesLower["inlinebuttons"]
	messageChannelOptions := strings.Join(listDeliverableMessageChannels(), "|")
	promptMode := params.PromptMode
	if promptMode == "" {
		promptMode = PromptModeFull
	}
	isMinimal := promptMode == PromptModeMinimal || promptMode == PromptModeNone

	skillsSection := buildSkillsSection(struct {
		skillsPrompt string
		isMinimal    bool
		readToolName string
	}{skillsPrompt: skillsPrompt, isMinimal: isMinimal, readToolName: readToolName})

	memorySection := buildMemorySection(struct {
		isMinimal     bool
		availableTool map[string]bool
		citationsMode string
	}{isMinimal: isMinimal, availableTool: availableTools, citationsMode: params.MemoryCitations})

	docsSection := buildDocsSection(struct {
		isMinimal     bool
		hasBeeperDocs bool
	}{isMinimal: isMinimal, hasBeeperDocs: availableTools["beeper_docs"]})

	workspaceNotes := make([]string, 0, len(params.WorkspaceNotes))
	for _, note := range params.WorkspaceNotes {
		trimmed := strings.TrimSpace(note)
		if trimmed != "" {
			workspaceNotes = append(workspaceNotes, trimmed)
		}
	}

	if promptMode == PromptModeNone {
		return "You are a personal assistant running inside Beeper."
	}

	toolingLines := ""
	if len(toolLines) > 0 {
		toolingLines = strings.Join(toolLines, "\n")
	} else {
		toolingLines = strings.Join([]string{
			"Pi lists the standard tools above. This runtime enables:",
			"- apply_patch: apply multi-file patches",
			fmt.Sprintf("- %s: run shell commands (supports background via yieldMs/background)", execToolName),
			fmt.Sprintf("- %s: manage background exec sessions", processToolName),
			"- browser: control Beeper's dedicated browser",
			"- canvas: present/eval/snapshot the Canvas",
			"- nodes: list/describe/notify/camera/screen on paired nodes",
			"- cron: manage scheduled jobs and wake events (use for reminders; when scheduling a reminder, write the systemEvent text as something that will read like a reminder when it fires, and mention that it is a reminder depending on the time gap between setting and firing; include recent context in reminder text if appropriate)",
			"- sessions_list: list sessions",
			"- sessions_history: fetch session history",
			"- sessions_send: send to another session",
		}, "\n")
	}

	lines := []string{
		"You are a personal assistant running inside Beeper.",
		"",
		"## Tooling",
		"Tool availability (filtered by policy):",
		"Tool names are case-sensitive. Call tools exactly as listed.",
		toolingLines,
		"TOOLS.md does not control tool availability; it is user guidance for how to use external tools.",
		"If a task is more complex or takes longer, spawn a sub-agent. It will do the work for you and ping you when it's done. You can always check up on it.",
		"",
		"## Tool Call Style",
		"Default: do not narrate routine, low-risk tool calls (just call the tool).",
		"Narrate only when it helps: multi-step work, complex/challenging problems, sensitive actions (e.g., deletions), or when the user explicitly asks.",
		"Keep narration brief and value-dense; avoid repeating obvious steps.",
		"Use plain human language for narration unless in a technical context.",
		"",
	}
	if !isMinimal {
		lines = append(lines, buildSafetySection()...)
	}
	lines = append(lines,
		"## Beeper CLI Quick Reference",
		"Beeper is controlled via subcommands. Do not invent commands.",
		"To manage the Gateway daemon service (start/stop/restart):",
		"- beep gateway status",
		"- beep gateway start",
		"- beep gateway stop",
		"- beep gateway restart",
		"If unsure, ask the user to run `beep help` (or `beep gateway --help`) and paste the output.",
		"",
	)
	lines = append(lines, skillsSection...)
	lines = append(lines, memorySection...)
	if hasGateway && !isMinimal {
		lines = append(lines,
			"## Beeper Self-Update",
			strings.Join([]string{
				"Get Updates (self-update) is ONLY allowed when the user explicitly asks for it.",
				"Do not run config.apply or update.run unless the user explicitly requests an update or config change; if it's not explicit, ask first.",
				"Actions: config.get, config.schema, config.apply (validate + write full config, then restart), update.run (update deps or git, then restart).",
				"After restart, Beeper pings the last active session automatically.",
			}, "\n"),
			"",
		)
	}
	if len(params.ModelAliasLines) > 0 && !isMinimal {
		lines = append(lines,
			"## Model Aliases",
			"Prefer aliases when specifying model overrides; full provider/model is also accepted.",
			strings.Join(params.ModelAliasLines, "\n"),
			"",
		)
	}

	if strings.TrimSpace(params.UserTimezone) != "" {
		lines = append(lines, "If you need the current date, time, or day of week, run session_status (📊 session_status).")
	}
	lines = append(lines,
		"## Workspace",
		fmt.Sprintf("Your working directory is: %s", params.WorkspaceDir),
		"Treat this directory as the single global workspace for file operations unless explicitly instructed otherwise.",
	)
	lines = append(lines, workspaceNotes...)
	lines = append(lines, "")
	lines = append(lines, docsSection...)
	if params.SandboxInfo != nil && params.SandboxInfo.Enabled {
		sandboxLines := []string{
			"## Sandbox",
			"You are running in a sandboxed runtime (tools execute in Docker).",
			"Some tools may be unavailable due to sandbox policy.",
			"Sub-agents stay sandboxed (no elevated/host access). Need outside-sandbox read/write? Don't spawn; ask first.",
		}
		if strings.TrimSpace(params.SandboxInfo.WorkspaceDir) != "" {
			sandboxLines = append(sandboxLines, fmt.Sprintf("Sandbox workspace: %s", params.SandboxInfo.WorkspaceDir))
		}
		if strings.TrimSpace(params.SandboxInfo.WorkspaceAccess) != "" {
			accessLine := fmt.Sprintf("Agent workspace access: %s", params.SandboxInfo.WorkspaceAccess)
			if strings.TrimSpace(params.SandboxInfo.AgentWorkspaceMount) != "" {
				accessLine = fmt.Sprintf("%s (mounted at %s)", accessLine, params.SandboxInfo.AgentWorkspaceMount)
			}
			sandboxLines = append(sandboxLines, accessLine)
		}
		if strings.TrimSpace(params.SandboxInfo.BrowserBridgeURL) != "" {
			sandboxLines = append(sandboxLines, "Sandbox browser: enabled.")
		}
		if strings.TrimSpace(params.SandboxInfo.BrowserNoVncURL) != "" {
			sandboxLines = append(sandboxLines, fmt.Sprintf("Sandbox browser observer (noVNC): %s", params.SandboxInfo.BrowserNoVncURL))
		}
		if params.SandboxInfo.HostBrowserAllowed != nil {
			if *params.SandboxInfo.HostBrowserAllowed {
				sandboxLines = append(sandboxLines, "Host browser control: allowed.")
			} else {
				sandboxLines = append(sandboxLines, "Host browser control: blocked.")
			}
		}
		if params.SandboxInfo.Elevated != nil && params.SandboxInfo.Elevated.Allowed {
			sandboxLines = append(sandboxLines, "Elevated exec is available for this session.")
			sandboxLines = append(sandboxLines, "User can toggle with !ai elevated on|off|ask|full.")
			sandboxLines = append(sandboxLines, "You may also send !ai elevated on|off|ask|full when needed.")
			if strings.TrimSpace(params.SandboxInfo.Elevated.DefaultLevel) != "" {
				sandboxLines = append(sandboxLines, fmt.Sprintf("Current elevated level: %s (ask runs exec on host with approvals; full auto-approves).", params.SandboxInfo.Elevated.DefaultLevel))
			}
		}
		lines = append(lines, strings.Join(sandboxLines, "\n"), "")
	}

	lines = append(lines, buildUserIdentitySection(ownerLine, params.UserIdentitySupplement, isMinimal)...)
	lines = append(lines, buildTimeSection(userTimezone)...)
	lines = append(lines,
		"## Workspace Files (injected)",
		"These user-editable files are loaded by Beeper and included below in Project Context.",
		"",
	)
	lines = append(lines, buildReplyTagsSection(isMinimal)...)
	lines = append(lines, buildMessagingSection(struct {
		isMinimal          bool
		availableTool      map[string]bool
		messageChannelOpts string
		inlineButtons      bool
		runtimeChannel     string
		messageToolHints   []string
	}{
		isMinimal:          isMinimal,
		availableTool:      availableTools,
		messageChannelOpts: messageChannelOptions,
		inlineButtons:      inlineButtonsEnabled,
		runtimeChannel:     runtimeChannel,
		messageToolHints:   params.MessageToolHints,
	})...)
	lines = append(lines, buildVoiceSection(struct {
		isMinimal bool
		ttsHint   string
	}{isMinimal: isMinimal, ttsHint: params.TTSHint})...)

	if extraSystemPrompt != "" {
		contextHeader := "## Group Chat Context"
		if promptMode == PromptModeMinimal {
			contextHeader = "## Subagent Context"
		}
		lines = append(lines, contextHeader, extraSystemPrompt, "")
	}
	if params.ReactionGuidance != nil {
		channel := params.ReactionGuidance.Channel
		if strings.TrimSpace(channel) == "" {
			channel = "this channel"
		}
		guidanceText := ""
		if params.ReactionGuidance.Level == "minimal" {
			guidanceText = strings.Join([]string{
				fmt.Sprintf("Reactions are enabled for %s in MINIMAL mode.", channel),
				"React ONLY when truly relevant:",
				"- Acknowledge important user requests or confirmations",
				"- Express genuine sentiment (humor, appreciation) sparingly",
				"- Avoid reacting to routine messages or your own replies",
				"Guideline: at most 1 reaction per 5-10 exchanges.",
			}, "\n")
		} else {
			guidanceText = strings.Join([]string{
				fmt.Sprintf("Reactions are enabled for %s in EXTENSIVE mode.", channel),
				"Feel free to react liberally:",
				"- Acknowledge messages with appropriate emojis",
				"- Express sentiment and personality through reactions",
				"- React to interesting content, humor, or notable events",
				"- Use reactions to confirm understanding or agreement",
				"Guideline: react whenever it feels natural.",
			}, "\n")
		}
		lines = append(lines, "## Reactions", guidanceText, "")
	}
	if reasoningHint != "" {
		lines = append(lines, "## Reasoning Format", reasoningHint, "")
	}

	contextFiles := params.ContextFiles
	if len(contextFiles) > 0 {
		hasSoulFile := false
		for _, file := range contextFiles {
			normalizedPath := strings.ReplaceAll(strings.TrimSpace(file.Path), "\\", "/")
			baseName := normalizedPath
			if idx := strings.LastIndex(normalizedPath, "/"); idx >= 0 {
				baseName = normalizedPath[idx+1:]
			}
			if strings.ToLower(baseName) == "soul.md" {
				hasSoulFile = true
				break
			}
		}
		lines = append(lines, "# Project Context", "", "The following project context files have been loaded:")
		if hasSoulFile {
			lines = append(lines, "If SOUL.md is present, embody its persona and tone. Avoid stiff, generic replies; follow its guidance unless higher-priority instructions override it.")
		}
		lines = append(lines, "")
		for _, file := range contextFiles {
			lines = append(lines, fmt.Sprintf("## %s", file.Path), "", file.Content, "")
		}
	}

	if !isMinimal {
		lines = append(lines,
			"## Silent Replies",
			fmt.Sprintf("When you have nothing to say, respond with ONLY: %s", SilentReplyToken),
			"",
			"⚠️ Rules:",
			"- It must be your ENTIRE message — nothing else",
			fmt.Sprintf("- Never append it to an actual response (never include \"%s\" in real replies)", SilentReplyToken),
			"- Never wrap it in markdown or code blocks",
			"",
			fmt.Sprintf("❌ Wrong: \"Here's help... %s\"", SilentReplyToken),
			fmt.Sprintf("❌ Wrong: \"%s\"", SilentReplyToken),
			fmt.Sprintf("✅ Right: %s", SilentReplyToken),
			"",
		)
	}
	if !isMinimal {
		lines = append(lines,
			"## Heartbeats",
			heartbeatPromptLine,
			"If you receive a heartbeat poll (a user message matching the heartbeat prompt above), and there is nothing that needs attention, reply exactly:",
			HeartbeatToken,
			"Beeper treats a leading/trailing \"HEARTBEAT_OK\" as a heartbeat ack (and may discard it).",
			"If something needs attention, do NOT include \"HEARTBEAT_OK\"; reply with the alert text instead.",
			"",
		)
	}

	lines = append(lines,
		"## Runtime",
		buildRuntimeLine(runtimeInfo, runtimeChannel, runtimeCapabilities, params.DefaultThinkLevel),
		fmt.Sprintf("Reasoning: %s (hidden unless on/stream). Toggle !ai reasoning; !ai status shows Reasoning when enabled.", reasoningLevel),
	)

	return joinNonEmptyLines(lines)
}

func buildRuntimeLine(
	runtimeInfo *RuntimeInfo,
	runtimeChannel string,
	runtimeCapabilities []string,
	defaultThinkLevel string,
) string {
	var parts []string
	if runtimeInfo != nil {
		if strings.TrimSpace(runtimeInfo.AgentID) != "" {
			parts = append(parts, fmt.Sprintf("agent=%s", runtimeInfo.AgentID))
		}
		if strings.TrimSpace(runtimeInfo.Host) != "" {
			parts = append(parts, fmt.Sprintf("host=%s", runtimeInfo.Host))
		}
		if strings.TrimSpace(runtimeInfo.RepoRoot) != "" {
			parts = append(parts, fmt.Sprintf("repo=%s", runtimeInfo.RepoRoot))
		}
		if strings.TrimSpace(runtimeInfo.OS) != "" {
			if strings.TrimSpace(runtimeInfo.Arch) != "" {
				parts = append(parts, fmt.Sprintf("os=%s (%s)", runtimeInfo.OS, runtimeInfo.Arch))
			} else {
				parts = append(parts, fmt.Sprintf("os=%s", runtimeInfo.OS))
			}
		} else if strings.TrimSpace(runtimeInfo.Arch) != "" {
			parts = append(parts, fmt.Sprintf("arch=%s", runtimeInfo.Arch))
		}
		if strings.TrimSpace(runtimeInfo.Node) != "" {
			parts = append(parts, fmt.Sprintf("node=%s", runtimeInfo.Node))
		}
		if strings.TrimSpace(runtimeInfo.Model) != "" {
			parts = append(parts, fmt.Sprintf("model=%s", runtimeInfo.Model))
		}
		if strings.TrimSpace(runtimeInfo.DefaultModel) != "" {
			parts = append(parts, fmt.Sprintf("default_model=%s", runtimeInfo.DefaultModel))
		}
	}
	if runtimeChannel != "" {
		parts = append(parts, fmt.Sprintf("channel=%s", runtimeChannel))
		capabilities := "none"
		if len(runtimeCapabilities) > 0 {
			capabilities = strings.Join(runtimeCapabilities, ",")
		}
		parts = append(parts, fmt.Sprintf("capabilities=%s", capabilities))
	}
	think := defaultThinkLevel
	if strings.TrimSpace(think) == "" {
		think = "off"
	}
	parts = append(parts, fmt.Sprintf("thinking=%s", think))
	return fmt.Sprintf("Runtime: %s", strings.Join(parts, " | "))
}

func joinNonEmptyLines(lines []string) string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			filtered = append(filtered, line)
		}
	}
	return strings.Join(filtered, "\n")
}

func listDeliverableMessageChannels() []string {
	return []string{"matrix"}
}
