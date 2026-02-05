package connector

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
)

// HelpSectionAI is the help section for AI-related commands
var HelpSectionAI = commands.HelpSection{
	Name:  "AI Chat",
	Order: 30,
}

var reservedAgentIDs = map[string]struct{}{
	"none":  {},
	"clear": {},
	"boss":  {},
}

// getAIClient retrieves the AIClient from the command event's user login
func getAIClient(ce *commands.Event) *AIClient {
	login := ce.User.GetDefaultLogin()
	if login == nil {
		return nil
	}
	client, ok := login.Client.(*AIClient)
	if !ok {
		return nil
	}
	return client
}

// getPortalMeta retrieves the PortalMetadata from the command event's portal
func getPortalMeta(ce *commands.Event) *PortalMetadata {
	if ce.Portal == nil {
		return nil
	}
	return portalMeta(ce.Portal)
}

func isValidAgentID(agentID string) bool {
	if agentID == "" {
		return false
	}
	for i := 0; i < len(agentID); i++ {
		ch := agentID[i]
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return false
		}
	}
	return true
}

func splitQuotedArgs(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false

	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}

	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}

		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}

	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if escaped {
		current.WriteRune('\\')
	}
	flush()
	return args, nil
}

// CommandModel handles the !ai model command
var CommandModel = registerAICommand(commandregistry.Definition{
	Name:           "model",
	Description:    "Get or set the AI model for this chat",
	Args:           "[_model name_]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnModel,
})

// CommandSetRoom handles the !ai set-room command.
var CommandSetRoom = registerAICommand(commandregistry.Definition{
	Name:           "set-room",
	Description:    "Set per-room parameters (model, temperature, system prompt)",
	Args:           "<model|temp|system-prompt> <value>",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnSetRoom,
})

func fnSetRoom(ce *commands.Event) {
	if len(ce.Args) < 2 {
		ce.Reply("Usage: !ai set-room <model|temp|system-prompt> <value>")
		return
	}

	field := strings.ToLower(strings.TrimSpace(ce.Args[0]))
	ce.Args = ce.Args[1:]

	switch field {
	case "model":
		fnModel(ce)
	case "temp", "temperature":
		fnTemp(ce)
	case "system-prompt", "prompt", "system":
		fnSystemPrompt(ce)
	default:
		ce.Reply("Unknown field: %s", field)
	}
}

func fnModel(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	if len(ce.Args) == 0 {
		ce.Reply("Current model: %s", client.effectiveModel(meta))
		return
	}

	if rejectBossOverrides(ce, meta, "Cannot change model in a room managed by the Boss agent") {
		return
	}

	modelID := ce.Args[0]
	valid, err := client.validateModel(ce.Ctx, modelID)
	if err != nil || !valid {
		ce.Reply("Invalid model: %s", modelID)
		return
	}

	agentID := resolveAgentID(meta)
	if agentID != "" {
		ce.Reply("Cannot set room model while an agent is assigned. Edit the agent instead.")
		return
	}

	meta.Model = modelID
	meta.Capabilities = getModelCapabilities(modelID, client.findModelInfo(modelID))
	client.savePortalQuiet(ce.Ctx, ce.Portal, "model change")
	client.ensureGhostDisplayName(ce.Ctx, modelID)
	ce.Reply("Model changed to: %s", modelID)
}

// CommandTemp handles the !ai temp command
var CommandTemp = registerAICommand(commandregistry.Definition{
	Name:           "temp",
	Aliases:        []string{"temperature"},
	Description:    "Get or set the temperature (0-2)",
	Args:           "[_value_]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnTemp,
})

func fnTemp(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	if len(ce.Args) == 0 {
		ce.Reply("Current temperature: %.2f", client.effectiveTemperature(meta))
		return
	}

	if rejectBossOverrides(ce, meta, "Cannot change temperature in a room managed by the Boss agent") {
		return
	}

	var temp float64
	if _, err := fmt.Sscanf(ce.Args[0], "%f", &temp); err != nil || temp < 0 || temp > 2 {
		ce.Reply("Invalid temperature. Must be between 0 and 2.")
		return
	}

	meta.Temperature = temp
	client.savePortalQuiet(ce.Ctx, ce.Portal, "temperature change")
	ce.Reply("Temperature set to: %.2f", temp)
}

// CommandSystemPrompt handles the !ai system-prompt command
var CommandSystemPrompt = registerAICommand(commandregistry.Definition{
	Name:           "system-prompt",
	Aliases:        []string{"prompt", "system"},
	Description:    "Get or set the system prompt (shows full constructed prompt)",
	Args:           "[_text_]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnSystemPrompt,
})

func fnSystemPrompt(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	if len(ce.Args) == 0 {
		// Show full constructed prompt (agent + room levels merged)
		fullPrompt := client.effectiveAgentPrompt(ce.Ctx, ce.Portal, meta)
		if fullPrompt == "" {
			fullPrompt = client.effectivePrompt(meta)
		}
		if fullPrompt == "" {
			fullPrompt = "(none)"
		}
		// Truncate for display
		totalLen := len(fullPrompt)
		if totalLen > 500 {
			fullPrompt = fullPrompt[:500] + "...\n\n(truncated, full prompt is " + strconv.Itoa(totalLen) + " chars)"
		}
		ce.Reply("Current system prompt:\n%s", fullPrompt)
		return
	}

	if rejectBossOverrides(ce, meta, "Cannot change system prompt in a room managed by the Boss agent") {
		return
	}

	meta.SystemPrompt = ce.RawArgs
	client.savePortalQuiet(ce.Ctx, ce.Portal, "system prompt change")
	ce.Reply("System prompt updated.")
}

// CommandContext handles the !ai context command
var CommandContext = registerAICommand(commandregistry.Definition{
	Name:           "context",
	Description:    "Get or set context message limit (1-100)",
	Args:           "[_count_]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnContext,
})

func fnContext(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	if len(ce.Args) == 0 {
		ce.Reply("Current context limit: %d messages", client.historyLimit(meta))
		return
	}

	var limit int
	if _, err := fmt.Sscanf(ce.Args[0], "%d", &limit); err != nil || limit < 1 || limit > 100 {
		ce.Reply("Invalid context limit. Must be between 1 and 100.")
		return
	}

	meta.MaxContextMessages = limit
	client.savePortalQuiet(ce.Ctx, ce.Portal, "context change")
	ce.Reply("Context limit set to: %d messages", limit)
}

// CommandTokens handles the !ai tokens command
var CommandTokens = registerAICommand(commandregistry.Definition{
	Name:           "tokens",
	Aliases:        []string{"maxtokens"},
	Description:    "Get or set max completion tokens (1-16384)",
	Args:           "[_count_]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnTokens,
})

func fnTokens(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	if len(ce.Args) == 0 {
		ce.Reply("Current max tokens: %d", client.effectiveMaxTokens(meta))
		return
	}

	var tokens int
	if _, err := fmt.Sscanf(ce.Args[0], "%d", &tokens); err != nil || tokens < 1 || tokens > 16384 {
		ce.Reply("Invalid max tokens. Must be between 1 and 16384.")
		return
	}

	meta.MaxCompletionTokens = tokens
	client.savePortalQuiet(ce.Ctx, ce.Portal, "tokens change")
	ce.Reply("Max tokens set to: %d", tokens)
}

// CommandConfig handles the !ai config command
var CommandConfig = registerAICommand(commandregistry.Definition{
	Name:           "config",
	Description:    "Show current chat configuration",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnConfig,
})

// CommandSetDesktopAPIToken handles the !ai set-desktop-api-token command
var CommandSetDesktopAPIToken = registerAICommand(commandregistry.Definition{
	Name:           "set-desktop-api-token",
	Description:    "Set the Beeper Desktop API access token for desktop sessions",
	Args:           "<token|clear>",
	Section:        HelpSectionAI,
	RequiresPortal: false,
	RequiresLogin:  true,
	Handler:        fnSetDesktopAPIToken,
})

var CommandAddDesktopAPIInstance = registerAICommand(commandregistry.Definition{
	Name:           "add-desktop-api-instance",
	Description:    "Add or update a Beeper Desktop API instance",
	Args:           "<name> <token> [baseURL]",
	Section:        HelpSectionAI,
	RequiresPortal: false,
	RequiresLogin:  true,
	Handler:        fnAddDesktopAPIInstance,
})

var CommandRemoveDesktopAPIInstance = registerAICommand(commandregistry.Definition{
	Name:           "remove-desktop-api-instance",
	Description:    "Remove a Beeper Desktop API instance",
	Args:           "<name>",
	Section:        HelpSectionAI,
	RequiresPortal: false,
	RequiresLogin:  true,
	Handler:        fnRemoveDesktopAPIInstance,
})

var CommandListDesktopAPIInstances = registerAICommand(commandregistry.Definition{
	Name:           "list-desktop-api-instances",
	Description:    "List configured Beeper Desktop API instances",
	Section:        HelpSectionAI,
	RequiresPortal: false,
	RequiresLogin:  true,
	Handler:        fnListDesktopAPIInstances,
})

// CommandDesktopAPI handles the !ai desktop-api command
var CommandDesktopAPI = registerAICommand(commandregistry.Definition{
	Name:           "desktop-api",
	Description:    "Manage Beeper Desktop API instances",
	Args:           "<add|list|remove> [args]",
	Section:        HelpSectionAI,
	RequiresPortal: false,
	RequiresLogin:  true,
	Handler:        fnDesktopAPI,
})

func fnConfig(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	mode := meta.ConversationMode
	if mode == "" {
		mode = "messages"
	}

	roomCaps := client.getRoomCapabilities(ce.Ctx, meta)
	config := fmt.Sprintf(
		"Current configuration:\n• Model: %s\n• Temperature: %.2f\n• Context: %d messages\n• Max tokens: %d\n• Vision: %v\n• Mode: %s",
		client.effectiveModel(meta), client.effectiveTemperature(meta), client.historyLimit(meta),
		client.effectiveMaxTokens(meta), roomCaps.SupportsVision, mode)
	ce.Reply(config)
}

func fnSetDesktopAPIToken(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}
	login := client.UserLogin
	if login == nil {
		ce.Reply("No active login found")
		return
	}
	meta := loginMetadata(login)
	if meta == nil {
		ce.Reply("Failed to access login metadata")
		return
	}

	if len(ce.Args) == 0 {
		instances := client.desktopAPIInstances()
		if len(instances) == 0 {
			ce.Reply("Desktop API instances: none configured")
			return
		}
		lines := make([]string, 0, len(instances))
		for _, name := range client.desktopAPIInstanceNames() {
			config := instances[name]
			status := "set"
			if strings.TrimSpace(config.Token) == "" {
				status = "missing token"
			}
			if strings.TrimSpace(config.BaseURL) != "" {
				lines = append(lines, fmt.Sprintf("- %s: %s (base URL %s)", name, status, strings.TrimSpace(config.BaseURL)))
			} else {
				lines = append(lines, fmt.Sprintf("- %s: %s", name, status))
			}
		}
		ce.Reply("Desktop API instances:\n%s", strings.Join(lines, "\n"))
		return
	}

	token := strings.TrimSpace(strings.Join(ce.Args, " "))
	if token == "" {
		ce.Reply("Usage: !ai set-desktop-api-token <token|clear>")
		return
	}
	if strings.EqualFold(token, "clear") || strings.EqualFold(token, "none") || strings.EqualFold(token, "unset") {
		if meta.ServiceTokens == nil {
			meta.ServiceTokens = &ServiceTokens{}
		}
		meta.ServiceTokens.DesktopAPI = ""
		if meta.ServiceTokens.DesktopAPIInstances != nil {
			delete(meta.ServiceTokens.DesktopAPIInstances, desktopDefaultInstance)
		}
		if err := login.Save(ce.Ctx); err != nil {
			ce.Reply("Failed to clear Desktop API token: %s", err)
			return
		}
		ce.Reply("Desktop API token cleared")
		return
	}

	if meta.ServiceTokens == nil {
		meta.ServiceTokens = &ServiceTokens{}
	}
	meta.ServiceTokens.DesktopAPI = token
	if meta.ServiceTokens.DesktopAPIInstances == nil {
		meta.ServiceTokens.DesktopAPIInstances = map[string]DesktopAPIInstance{}
	}
	defaultConfig := meta.ServiceTokens.DesktopAPIInstances[desktopDefaultInstance]
	defaultConfig.Token = token
	meta.ServiceTokens.DesktopAPIInstances[desktopDefaultInstance] = defaultConfig
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to set Desktop API token: %s", err)
		return
	}
	ce.Reply("Desktop API token saved")
}

func fnAddDesktopAPIInstance(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}
	login := client.UserLogin
	if login == nil {
		ce.Reply("No active login found")
		return
	}
	meta := loginMetadata(login)
	if meta == nil {
		ce.Reply("Failed to access login metadata")
		return
	}
	if len(ce.Args) < 2 {
		ce.Reply("Usage: !ai add-desktop-api-instance <name> <token> [baseURL]")
		return
	}
	name := normalizeDesktopInstanceName(ce.Args[0])
	if name == "" {
		ce.Reply("Instance name is required")
		return
	}
	token := strings.TrimSpace(ce.Args[1])
	if token == "" {
		ce.Reply("Token is required")
		return
	}
	baseURL := ""
	if len(ce.Args) > 2 {
		baseURL = strings.TrimSpace(strings.Join(ce.Args[2:], " "))
	}
	if meta.ServiceTokens == nil {
		meta.ServiceTokens = &ServiceTokens{}
	}
	if meta.ServiceTokens.DesktopAPIInstances == nil {
		meta.ServiceTokens.DesktopAPIInstances = map[string]DesktopAPIInstance{}
	}
	config := meta.ServiceTokens.DesktopAPIInstances[name]
	config.Token = token
	if baseURL != "" {
		config.BaseURL = baseURL
	}
	meta.ServiceTokens.DesktopAPIInstances[name] = config
	if name == desktopDefaultInstance {
		meta.ServiceTokens.DesktopAPI = token
	}
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to save Desktop API instance: %s", err)
		return
	}
	if baseURL != "" {
		ce.Reply("Desktop API instance '%s' saved (base URL %s)", name, baseURL)
		return
	}
	ce.Reply("Desktop API instance '%s' saved", name)
}

func fnRemoveDesktopAPIInstance(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}
	login := client.UserLogin
	if login == nil {
		ce.Reply("No active login found")
		return
	}
	meta := loginMetadata(login)
	if meta == nil {
		ce.Reply("Failed to access login metadata")
		return
	}
	if len(ce.Args) == 0 {
		ce.Reply("Usage: !ai remove-desktop-api-instance <name>")
		return
	}
	name := normalizeDesktopInstanceName(strings.Join(ce.Args, " "))
	if name == "" {
		ce.Reply("Instance name is required")
		return
	}
	if meta.ServiceTokens == nil || meta.ServiceTokens.DesktopAPIInstances == nil {
		ce.Reply("Desktop API instance '%s' not found", name)
		return
	}
	if _, ok := meta.ServiceTokens.DesktopAPIInstances[name]; !ok {
		ce.Reply("Desktop API instance '%s' not found", name)
		return
	}
	delete(meta.ServiceTokens.DesktopAPIInstances, name)
	if name == desktopDefaultInstance {
		meta.ServiceTokens.DesktopAPI = ""
	}
	if len(meta.ServiceTokens.DesktopAPIInstances) == 0 {
		meta.ServiceTokens.DesktopAPIInstances = nil
	}
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to remove Desktop API instance: %s", err)
		return
	}
	ce.Reply("Desktop API instance '%s' removed", name)
}

func fnListDesktopAPIInstances(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}
	instances := client.desktopAPIInstances()
	if len(instances) == 0 {
		ce.Reply("Desktop API instances: none configured")
		return
	}
	lines := make([]string, 0, len(instances))
	for _, name := range client.desktopAPIInstanceNames() {
		config := instances[name]
		status := "set"
		if strings.TrimSpace(config.Token) == "" {
			status = "missing token"
		}
		if strings.TrimSpace(config.BaseURL) != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s (base URL %s)", name, status, strings.TrimSpace(config.BaseURL)))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s", name, status))
		}
	}
	ce.Reply("Desktop API instances:\n%s", strings.Join(lines, "\n"))
}

func fnDesktopAPI(ce *commands.Event) {
	if len(ce.Args) == 0 {
		ce.Reply("Usage: `!ai desktop-api list` | `!ai desktop-api add <token> [baseURL]` | `!ai desktop-api add <name> <token> [baseURL]` | `!ai desktop-api remove <name>`.")
		return
	}

	sub := strings.ToLower(strings.TrimSpace(ce.Args[0]))
	switch sub {
	case "list", "ls":
		ce.Args = ce.Args[1:]
		fnListDesktopAPIInstances(ce)
		return
	case "add", "set":
		switch len(ce.Args) {
		case 2:
			ce.Args = ce.Args[1:]
			fnSetDesktopAPIToken(ce)
			return
		default:
			if len(ce.Args) >= 3 {
				ce.Args = ce.Args[1:]
				fnAddDesktopAPIInstance(ce)
				return
			}
		}
		ce.Reply("Usage: `!ai desktop-api add <token> [baseURL]` or `!ai desktop-api add <name> <token> [baseURL]`.")
		return
	case "remove", "rm", "delete":
		if len(ce.Args) < 2 {
			ce.Reply("Usage: `!ai desktop-api remove <name>`.")
			return
		}
		ce.Args = ce.Args[1:]
		fnRemoveDesktopAPIInstance(ce)
		return
	default:
		ce.Reply("Usage: `!ai desktop-api list` | `!ai desktop-api add <token> [baseURL]` | `!ai desktop-api add <name> <token> [baseURL]` | `!ai desktop-api remove <name>`.")
	}
}

// CommandDebounce handles the !ai debounce command
var CommandDebounce = registerAICommand(commandregistry.Definition{
	Name:           "debounce",
	Description:    "Get or set message debounce delay (ms), 'off' to disable, 'default' to reset",
	Args:           "[_delay_|off|default]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnDebounce,
})

func fnDebounce(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	if len(ce.Args) == 0 {
		// Show current setting
		switch {
		case meta.DebounceMs < 0:
			ce.Reply("Message debouncing is **disabled** for this room")
		case meta.DebounceMs == 0:
			ce.Reply("Message debounce: **%d ms** (default)", DefaultDebounceMs)
		default:
			ce.Reply("Message debounce: **%d ms**", meta.DebounceMs)
		}
		return
	}

	arg := strings.ToLower(ce.Args[0])
	switch arg {
	case "off", "disable", "disabled":
		meta.DebounceMs = -1
		client.savePortalQuiet(ce.Ctx, ce.Portal, "debounce disabled")
		ce.Reply("Message debouncing disabled for this room")
	case "default", "reset":
		meta.DebounceMs = 0
		client.savePortalQuiet(ce.Ctx, ce.Portal, "debounce reset")
		ce.Reply("Message debounce reset to default (%d ms)", DefaultDebounceMs)
	default:
		// Parse as integer
		delay, err := strconv.Atoi(arg)
		if err != nil || delay < 0 || delay > 10000 {
			ce.Reply("Invalid debounce delay. Use a number 0-10000 (ms), 'off', or 'default'.")
			return
		}
		meta.DebounceMs = delay
		client.savePortalQuiet(ce.Ctx, ce.Portal, "debounce change")
		if delay == 0 {
			ce.Reply("Message debounce reset to default (%d ms)", DefaultDebounceMs)
		} else {
			ce.Reply("Message debounce set to: %d ms", delay)
		}
	}
}

// CommandTools handles the !ai tools command
var CommandTools = registerAICommand(commandregistry.Definition{
	Name:           "tools",
	Description:    "Enable/disable tools",
	Args:           "[on|off] [_tool_]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnTools,
})

func fnTools(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	// Run async to avoid blocking
	go client.handleToolsCommand(ce.Ctx, ce.Portal, meta, ce.RawArgs)
}

// CommandMode handles the !ai mode command
var CommandMode = registerAICommand(commandregistry.Definition{
	Name:           "mode",
	Description:    "Set conversation mode (messages|responses)",
	Args:           "[_mode_]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnMode,
})

func fnMode(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	mode := meta.ConversationMode
	if mode == "" {
		mode = "messages"
	}

	if len(ce.Args) == 0 {
		ce.Reply("Conversation modes:\n• messages - Build full message history for each request (default)\n• responses - Use OpenAI's previous_response_id for context chaining\n\nCurrent mode: %s", mode)
		return
	}

	newMode := strings.ToLower(ce.Args[0])
	if newMode != "messages" && newMode != "responses" {
		ce.Reply("Invalid mode. Use 'messages' or 'responses'.")
		return
	}

	meta.ConversationMode = newMode
	if newMode == "messages" {
		meta.LastResponseID = ""
	}
	client.savePortalQuiet(ce.Ctx, ce.Portal, "mode change")
	_ = client.BroadcastRoomState(ce.Ctx, ce.Portal)
	ce.Reply("Conversation mode set to: %s", newMode)
}

// CommandNew handles the !ai new command
var CommandNew = registerAICommand(commandregistry.Definition{
	Name:           "new",
	Description:    "Create a new chat using current agent/model (or specify agent/model)",
	Args:           "[agent <agent_id> | model <model_id>]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnNew,
})

func fnNew(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	// Run async
	go client.handleNewChat(ce.Ctx, nil, ce.Portal, meta, ce.Args)
}

// CommandFork handles the !ai fork command
var CommandFork = registerAICommand(commandregistry.Definition{
	Name:           "fork",
	Description:    "Fork conversation to a new chat",
	Args:           "[_event_id_]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnFork,
})

func fnFork(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	var arg string
	if len(ce.Args) > 0 {
		arg = ce.Args[0]
	}

	// Run async
	go client.handleFork(ce.Ctx, nil, ce.Portal, meta, arg)
}

// CommandRegenerate handles the !ai regenerate command
var CommandRegenerate = registerAICommand(commandregistry.Definition{
	Name:           "regenerate",
	Description:    "Regenerate the last AI response",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnRegenerate,
})

func fnRegenerate(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	// Run async
	go client.handleRegenerate(ce.Ctx, nil, ce.Portal, meta)
}

// CommandTitle handles the !ai title command
var CommandTitle = registerAICommand(commandregistry.Definition{
	Name:           "title",
	Aliases:        []string{"retitle"},
	Description:    "Regenerate the chat room title",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnTitle,
})

func fnTitle(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}
	if _, ok := requirePortal(ce); !ok {
		return
	}

	// Run async
	go client.handleRegenerateTitle(ce.Ctx, ce.Portal)
}

// CommandModels handles the !ai models command
var CommandModels = registerAICommand(commandregistry.Definition{
	Name:          "models",
	Description:   "List all available models",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnModels,
})

func fnModels(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	// Get portal meta if available (for showing current model)
	meta := getPortalMeta(ce)

	models, err := client.listAvailableModels(ce.Ctx, false)
	if err != nil {
		ce.Reply("Failed to fetch models")
		return
	}

	var sb strings.Builder
	sb.WriteString("Available models:\n\n")
	for _, m := range models {
		var caps []string
		if m.SupportsVision {
			caps = append(caps, "Vision")
		}
		if m.SupportsReasoning {
			caps = append(caps, "Reasoning")
		}
		if m.SupportsWebSearch {
			caps = append(caps, "Web Search")
		}
		if m.SupportsImageGen {
			caps = append(caps, "Image Gen")
		}
		if m.SupportsToolCalling {
			caps = append(caps, "Tools")
		}
		sb.WriteString(fmt.Sprintf("• **%s** (`%s`)\n", m.Name, m.ID))
		if m.Description != "" {
			sb.WriteString(fmt.Sprintf("  %s\n", m.Description))
		}
		if len(caps) > 0 {
			sb.WriteString(fmt.Sprintf("  %s\n", strings.Join(caps, " · ")))
		}
		sb.WriteString("\n")
	}

	currentModel := ""
	if meta != nil {
		currentModel = client.effectiveModel(meta)
	} else {
		currentModel = client.effectiveModel(nil)
	}
	sb.WriteString(fmt.Sprintf("Current: **%s**\nUse `!ai model <id>` to switch models", currentModel))
	ce.Reply(sb.String())
}

// CommandTimezone handles the !ai timezone command
var CommandTimezone = registerAICommand(commandregistry.Definition{
	Name:           "timezone",
	Aliases:        []string{"tz"},
	Description:    "Get or set your timezone for all chats (IANA name)",
	Args:           "[_timezone_|reset]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnTimezone,
})

func fnTimezone(ce *commands.Event) {
	client, _, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	loginMeta := loginMetadata(client.UserLogin)
	if loginMeta == nil {
		ce.Reply("Failed to load login metadata")
		return
	}

	if len(ce.Args) == 0 {
		tz := strings.TrimSpace(loginMeta.Timezone)
		if tz == "" {
			ce.Reply("No timezone set. Use `!ai timezone <IANA>` (example: `America/Los_Angeles`).")
			return
		}
		ce.Reply("Timezone: %s", tz)
		return
	}

	arg := strings.TrimSpace(ce.Args[0])
	switch strings.ToLower(arg) {
	case "reset", "default", "clear":
		loginMeta.Timezone = ""
		if err := client.UserLogin.Save(ce.Ctx); err != nil {
			ce.Reply("Failed to clear timezone: %s", err.Error())
			return
		}
		ce.Reply("Timezone cleared. Falling back to UTC unless TZ is set.")
		return
	default:
		tz, _, err := normalizeTimezone(arg)
		if err != nil {
			ce.Reply("Invalid timezone. Use an IANA name like `America/Los_Angeles` or `Europe/London`.")
			return
		}
		loginMeta.Timezone = tz
		if err := client.UserLogin.Save(ce.Ctx); err != nil {
			ce.Reply("Failed to save timezone: %s", err.Error())
			return
		}
		ce.Reply("Timezone set to: %s", tz)
	}
}

// CommandGravatar handles the !ai gravatar command
var CommandGravatar = registerAICommand(commandregistry.Definition{
	Name:           "gravatar",
	Description:    "Fetch or set the Gravatar profile for this login",
	Args:           "[fetch|set] [email]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnGravatar,
})

func fnGravatar(ce *commands.Event) {
	client, _, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	if len(ce.Args) == 0 {
		loginMeta := loginMetadata(client.UserLogin)
		if loginMeta == nil || loginMeta.Gravatar == nil || loginMeta.Gravatar.Primary == nil {
			ce.Reply("No Gravatar profile set. Use `!ai gravatar set <email>`.")
			return
		}
		ce.Reply(formatGravatarMarkdown(loginMeta.Gravatar.Primary, "primary"))
		return
	}

	action := strings.ToLower(strings.TrimSpace(ce.Args[0]))
	switch action {
	case "fetch":
		email := ""
		if len(ce.Args) > 1 {
			email = ce.Args[1]
		}
		if strings.TrimSpace(email) == "" {
			loginMeta := loginMetadata(client.UserLogin)
			if loginMeta != nil && loginMeta.Gravatar != nil && loginMeta.Gravatar.Primary != nil {
				email = loginMeta.Gravatar.Primary.Email
			}
		}
		if strings.TrimSpace(email) == "" {
			ce.Reply("Email is required. Usage: `!ai gravatar fetch <email>`.")
			return
		}
		profile, err := fetchGravatarProfile(ce.Ctx, email)
		if err != nil {
			ce.Reply("Failed to fetch Gravatar profile: %s", err.Error())
			return
		}
		ce.Reply(formatGravatarMarkdown(profile, "fetched"))
		return
	case "set":
		if len(ce.Args) < 2 || strings.TrimSpace(ce.Args[1]) == "" {
			ce.Reply("Email is required. Usage: `!ai gravatar set <email>`.")
			return
		}
		profile, err := fetchGravatarProfile(ce.Ctx, ce.Args[1])
		if err != nil {
			ce.Reply("Failed to fetch Gravatar profile: %s", err.Error())
			return
		}
		state := ensureGravatarState(loginMetadata(client.UserLogin))
		state.Primary = profile
		if err := client.UserLogin.Save(ce.Ctx); err != nil {
			ce.Reply("Failed to save Gravatar profile: %s", err.Error())
			return
		}
		ce.Reply(formatGravatarMarkdown(profile, "primary set"))
		return
	default:
		ce.Reply("Usage: `!ai gravatar fetch <email>` or `!ai gravatar set <email>`.")
	}
}

// CommandAgent handles the !ai agent command
var CommandAgent = registerAICommand(commandregistry.Definition{
	Name:           "agent",
	Description:    "Get or set the agent for this chat",
	Args:           "[_agent id_]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnAgent,
})

func fnAgent(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	store := NewAgentStoreAdapter(client)

	if len(ce.Args) == 0 {
		// Show current agent
		agentID := resolveAgentID(meta)
		if agentID == "" {
			ce.Reply("No agent configured. Using default model: %s", client.effectiveModel(meta))
			return
		}
		agent, err := store.GetAgentByID(ce.Ctx, agentID)
		if err != nil {
			ce.Reply("Current agent ID: %s (not found)", agentID)
			return
		}
		displayName := client.resolveAgentDisplayName(ce.Ctx, agent)
		if displayName == "" {
			displayName = agent.Name
		}
		if displayName == "" {
			displayName = agent.ID
		}
		ce.Reply("Current agent: **%s** (`%s`)\n%s", displayName, agent.ID, agent.Description)
		return
	}

	if rejectBossOverrides(ce, meta, "Cannot change agent in a room managed by the Boss agent") {
		return
	}

	// Set agent
	agentID := ce.Args[0]

	// Special case: "none" clears the agent
	if agentID == "none" || agentID == "clear" {
		meta.AgentID = ""
		meta.DefaultAgentID = ""
		meta.AgentPrompt = ""
		modelID := client.effectiveModel(meta)
		ce.Portal.OtherUserID = modelUserID(modelID)
		client.savePortalQuiet(ce.Ctx, ce.Portal, "agent cleared")
		_ = client.BroadcastRoomState(ce.Ctx, ce.Portal)
		ce.Reply("Agent cleared. Using default model.")
		return
	}

	agent, err := store.GetAgentByID(ce.Ctx, agentID)
	if err != nil {
		ce.Reply("Agent not found: %s", agentID)
		return
	}

	meta.AgentID = agent.ID
	meta.DefaultAgentID = agent.ID
	meta.AgentPrompt = agent.SystemPrompt
	meta.Model = ""
	modelID := client.effectiveModel(meta)
	meta.Capabilities = getModelCapabilities(modelID, client.findModelInfo(modelID))
	ce.Portal.OtherUserID = agentUserID(agent.ID)
	client.savePortalQuiet(ce.Ctx, ce.Portal, "agent change")
	agentName := client.resolveAgentDisplayName(ce.Ctx, agent)
	client.ensureAgentGhostDisplayName(ce.Ctx, agent.ID, modelID, agentName)
	_ = client.BroadcastRoomState(ce.Ctx, ce.Portal)
	displayName := agentName
	if displayName == "" {
		displayName = agent.Name
	}
	if displayName == "" {
		displayName = agent.ID
	}
	ce.Reply("Agent set to: **%s** (`%s`)", displayName, agent.ID)
}

// CommandAgents handles the !ai agents command
var CommandAgents = registerAICommand(commandregistry.Definition{
	Name:          "agents",
	Description:   "List available agents",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnAgents,
})

func fnAgents(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	store := NewAgentStoreAdapter(client)
	agentsMap, err := store.LoadAgents(ce.Ctx)
	if err != nil {
		ce.Reply("Failed to load agents: %v", err)
		return
	}

	var sb strings.Builder
	sb.WriteString("## Available Agents\n\n")

	// Group by preset vs custom
	var presets, custom []string
	for id, agent := range agentsMap {
		agentName := client.resolveAgentDisplayName(ce.Ctx, agent)
		line := fmt.Sprintf("• **%s** (`%s`)", agentName, id)
		if agent.Description != "" {
			line += fmt.Sprintf(" - %s", agent.Description)
		}
		if agent.IsPreset {
			presets = append(presets, line)
		} else {
			custom = append(custom, line)
		}
	}

	if len(presets) > 0 {
		sb.WriteString("**Presets:**\n")
		for _, line := range presets {
			sb.WriteString(line + "\n")
		}
		sb.WriteString("\n")
	}

	if len(custom) > 0 {
		sb.WriteString("**Custom:**\n")
		for _, line := range custom {
			sb.WriteString(line + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Use `!ai agent <id>` to switch agents")
	ce.Reply(sb.String())
}

// CommandCreateAgent handles the !ai create-agent command
var CommandCreateAgent = registerAICommand(commandregistry.Definition{
	Name:          "create-agent",
	Description:   "Create a new custom agent",
	Args:          "<id> <name> [model] [system prompt...]",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnCreateAgent,
})

func fnCreateAgent(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	args := ce.Args
	if raw := strings.TrimSpace(ce.RawArgs); raw != "" {
		if parsed, err := splitQuotedArgs(raw); err == nil && len(parsed) > 0 {
			args = parsed
		}
	}

	if len(args) < 2 {
		ce.Reply("Usage: !ai create-agent <id> <name> [model] [system prompt...]\nExample: !ai create-agent my-helper \"My Helper\" gpt-4o You are a helpful assistant.")
		return
	}

	agentID := args[0]
	agentName := args[1]

	if _, reserved := reservedAgentIDs[agentID]; reserved {
		ce.Reply("Agent ID '%s' is reserved. Choose a different ID.", agentID)
		return
	}
	if !isValidAgentID(agentID) {
		ce.Reply("Invalid agent ID '%s'. Use only lowercase letters, numbers, and hyphens.", agentID)
		return
	}

	// Parse optional model and system prompt
	var model, systemPrompt string
	if len(args) > 2 {
		model = args[2]
	}
	if len(args) > 3 {
		systemPrompt = strings.Join(args[3:], " ")
	}

	store := NewAgentStoreAdapter(client)

	// Check if agent already exists
	if _, err := store.GetAgentByID(ce.Ctx, agentID); err == nil {
		ce.Reply("Agent with ID '%s' already exists", agentID)
		return
	}

	// Create new agent
	newAgent := &agents.AgentDefinition{
		ID:           agentID,
		Name:         agentName,
		SystemPrompt: systemPrompt,
		Tools:        &toolpolicy.ToolPolicyConfig{Profile: toolpolicy.ProfileFull},
		IsPreset:     false,
		CreatedAt:    time.Now().Unix(),
		UpdatedAt:    time.Now().Unix(),
	}
	if model != "" {
		newAgent.Model = agents.ModelConfig{Primary: model}
	}

	if err := store.SaveAgent(ce.Ctx, newAgent); err != nil {
		ce.Reply("Failed to create agent: %v", err)
		return
	}

	ce.Reply("Created agent: **%s** (`%s`)\nUse `!ai agent %s` to use it", agentName, agentID, agentID)
}

// CommandDeleteAgent handles the !ai delete-agent command
var CommandDeleteAgent = registerAICommand(commandregistry.Definition{
	Name:          "delete-agent",
	Description:   "Delete a custom agent",
	Args:          "<id>",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnDeleteAgent,
})

func fnDeleteAgent(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	if len(ce.Args) < 1 {
		ce.Reply("Usage: !ai delete-agent <id>")
		return
	}

	agentID := ce.Args[0]
	store := NewAgentStoreAdapter(client)

	// Check if it's a preset
	if agents.IsPreset(agentID) || agents.IsBossAgent(agentID) {
		ce.Reply("Cannot delete preset agent: %s", agentID)
		return
	}

	if err := store.DeleteAgent(ce.Ctx, agentID); err != nil {
		ce.Reply("Failed to delete agent: %v", err)
		return
	}

	ce.Reply("Deleted agent: %s", agentID)
}
