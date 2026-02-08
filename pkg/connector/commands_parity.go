package connector

import (
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
)

// CommandStatus handles the !ai status command.
var CommandStatus = registerAICommand(commandregistry.Definition{
	Name:           "status",
	Description:    "Show current session status",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnStatus,
})

func fnStatus(ce *commands.Event) {
	portal, ok := requirePortal(ce)
	if !ok {
		return
	}
	meta := getPortalMeta(ce)
	if meta == nil {
		ce.Reply("Couldn't load room settings. Try again.")
		return
	}

	if meta.IsCodexRoom {
		cc := getCodexClient(ce)
		if cc == nil {
			ce.Reply("Codex isn't available for this login. Try again.")
			return
		}
		threadID := strings.TrimSpace(meta.CodexThreadID)
		cwd := strings.TrimSpace(meta.CodexCwd)
		ce.Reply("Codex: logged_in=%v thread_id=%s cwd=%s", cc.IsLoggedIn(), threadID, cwd)
		return
	}

	client := getAIClient(ce)
	if client == nil {
		ce.Reply("Couldn't load AI settings. Try again.")
		return
	}
	isGroup := client.isGroupChat(ce.Ctx, portal)
	queueSettings, _, _, _ := client.resolveQueueSettingsForPortal(ce.Ctx, portal, meta, "", QueueInlineOptions{})
	ce.Reply("%s", client.buildStatusText(ce.Ctx, portal, meta, isGroup, queueSettings))
}

// CommandApprove handles the !ai approve command.
var CommandApprove = registerAICommand(commandregistry.Definition{
	Name:           "approve",
	Description:    "Resolve a pending approval request",
	Args:           "<approvalId> <allow|always|deny> [reason]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnApprove,
})

func fnApprove(ce *commands.Event) {
	portal, ok := requirePortal(ce)
	if !ok {
		return
	}
	meta := getPortalMeta(ce)
	if meta == nil {
		ce.Reply("Couldn't load room settings. Try again.")
		return
	}

	if len(ce.Args) < 2 {
		ce.Reply("Usage: `!ai approve <approvalId> <allow|always|deny> [reason]`")
		return
	}
	approvalID := strings.TrimSpace(ce.Args[0])
	actionToken := strings.ToLower(strings.TrimSpace(ce.Args[1]))
	reason := strings.TrimSpace(strings.Join(ce.Args[2:], " "))
	if approvalID == "" || actionToken == "" {
		ce.Reply("Usage: `!ai approve <approvalId> <allow|always|deny> [reason]`")
		return
	}

	approve := false
	always := false
	switch actionToken {
	case "allow", "approve", "yes", "y", "true", "1":
		approve = true
	case "allow-once", "once":
		approve = true
	case "allow-always", "always":
		approve = true
		always = true
	case "deny", "reject", "no", "n", "false", "0":
		approve = false
	default:
		ce.Reply("Usage: `!ai approve <approvalId> <allow|always|deny> [reason]`")
		return
	}

	if meta.IsCodexRoom {
		cc := getCodexClient(ce)
		if cc == nil {
			ce.Reply("Codex isn't available for this login. Try again.")
			return
		}
		err := cc.resolveToolApproval(approvalID, ToolApprovalDecisionCodex{
			Approve:   approve,
			Reason:    reason,
			DecidedAt: time.Now(),
			DecidedBy: ce.User.MXID,
		})
		if err != nil {
			ce.Reply("%s", formatSystemAck(err.Error()))
			return
		}
		if approve {
			ce.Reply("%s", formatSystemAck("Approved."))
		} else {
			ce.Reply("%s", formatSystemAck("Denied."))
		}
		return
	}

	client := getAIClient(ce)
	if client == nil {
		ce.Reply("Couldn't load AI settings. Try again.")
		return
	}
	err := client.resolveToolApproval(portal.MXID, approvalID, ToolApprovalDecision{
		Approve:   approve,
		Always:    always,
		Reason:    reason,
		DecidedAt: time.Now(),
		DecidedBy: ce.User.MXID,
	})
	if err != nil {
		ce.Reply("%s", formatSystemAck(err.Error()))
		return
	}
	if approve {
		if always {
			ce.Reply("%s", formatSystemAck("Approved (always allow)."))
		} else {
			ce.Reply("%s", formatSystemAck("Approved."))
		}
		return
	}
	ce.Reply("%s", formatSystemAck("Denied."))
}

// CommandReset handles the !ai reset command.
var CommandReset = registerAICommand(commandregistry.Definition{
	Name:           "reset",
	Description:    "Start a new session/thread in this room",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnReset,
})

func fnReset(ce *commands.Event) {
	portal, ok := requirePortal(ce)
	if !ok {
		return
	}
	meta := getPortalMeta(ce)
	if meta == nil {
		ce.Reply("Couldn't load room settings. Try again.")
		return
	}

	if meta.IsCodexRoom {
		cc := getCodexClient(ce)
		if cc == nil {
			ce.Reply("Codex isn't available for this login. Try again.")
			return
		}
		if err := cc.resetThread(ce.Ctx, portal, meta); err != nil {
			ce.Reply("%s", formatSystemAck("Couldn't start a new Codex thread: "+err.Error()))
		} else {
			ce.Reply("%s", formatSystemAck("New Codex thread started."))
		}
		return
	}

	client := getAIClient(ce)
	if client == nil {
		ce.Reply("Couldn't load AI settings. Try again.")
		return
	}

	meta.SessionResetAt = time.Now().UnixMilli()
	meta.GroupIntroSent = false
	meta.GroupActivationNeedsIntro = true
	client.savePortalQuiet(ce.Ctx, portal, "session reset")
	client.clearPendingQueue(portal.MXID)
	client.cancelRoomRun(portal.MXID)

	ce.Reply("%s", formatSystemAck("Session reset."))

	// Keep legacy behavior: after reset, prompt the assistant to greet.
	go client.dispatchInternalMessage(client.backgroundContext(ce.Ctx), portal, meta, sessionGreetingPrompt, "session-reset", true)
}

// CommandStop handles the !ai stop command.
var CommandStop = registerAICommand(commandregistry.Definition{
	Name:           "stop",
	Aliases:        []string{"abort", "interrupt"},
	Description:    "Abort the current run and clear the pending queue",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnStop,
})

func fnStop(ce *commands.Event) {
	portal, ok := requirePortal(ce)
	if !ok {
		return
	}
	meta := getPortalMeta(ce)
	if meta == nil {
		ce.Reply("Couldn't load room settings. Try again.")
		return
	}
	if meta.IsCodexRoom {
		ce.Reply("%s", formatSystemAck("Stop is not supported in Codex rooms."))
		return
	}
	client, _, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	stopped := client.abortRoom(ce.Ctx, portal, meta)
	ce.Reply("%s", formatAbortNotice(stopped))
}

// CommandQueue handles the !ai queue command.
var CommandQueue = registerAICommand(commandregistry.Definition{
	Name:           "queue",
	Description:    "Inspect or configure the message queue",
	Args:           "[status|reset|<mode>] [debounce:<dur>] [cap:<n>] [drop:<old|new|summarize>]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnQueue,
})

func fnQueue(ce *commands.Event) {
	portal, ok := requirePortal(ce)
	if !ok {
		return
	}
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	if meta.IsCodexRoom {
		ce.Reply("%s", formatSystemAck("Queue settings aren't supported in Codex rooms."))
		return
	}

	queueSettings, _, storeRef, sessionKey := client.resolveQueueSettingsForPortal(ce.Ctx, portal, meta, "", QueueInlineOptions{})

	if len(ce.Args) == 0 || strings.EqualFold(strings.TrimSpace(ce.Args[0]), "status") {
		ce.Reply("%s", buildQueueStatusLine(queueSettings))
		return
	}

	// Allow `!ai queue reset` as a command-oriented alias for clearing session overrides.
	if strings.EqualFold(strings.TrimSpace(ce.Args[0]), "reset") || strings.EqualFold(strings.TrimSpace(ce.Args[0]), "default") || strings.EqualFold(strings.TrimSpace(ce.Args[0]), "clear") {
		if sessionKey != "" {
			client.updateSessionEntry(ce.Ctx, storeRef, sessionKey, func(entry sessionEntry) sessionEntry {
				entry.QueueMode = ""
				entry.QueueDebounceMs = nil
				entry.QueueCap = nil
				entry.QueueDrop = ""
				entry.UpdatedAt = time.Now().UnixMilli()
				return entry
			})
		}
		client.clearPendingQueue(portal.MXID)
		queueSettings, _, _, _ = client.resolveQueueSettingsForPortal(ce.Ctx, portal, meta, "", QueueInlineOptions{})
		ce.Reply("%s", buildQueueStatusLine(queueSettings))
		return
	}

	raw := strings.TrimSpace(strings.Join(ce.Args, " "))
	_, directive := parseQueueDirectiveArgs(raw)
	if directive.HasDebounce && directive.DebounceMs == nil {
		ce.Reply("Invalid debounce \"%s\". Use ms/s/m (e.g. debounce:1500ms, debounce:2s).", directive.RawDebounce)
		return
	}
	if directive.HasCap && directive.Cap == nil {
		ce.Reply("Invalid cap \"%s\". Use a positive integer (e.g. cap:10).", directive.RawCap)
		return
	}
	if directive.HasDrop && directive.DropPolicy == nil {
		ce.Reply("Invalid drop policy \"%s\". Use drop:old, drop:new, or drop:summarize.", directive.RawDrop)
		return
	}
	if directive.QueueMode == "" && !directive.HasOptions {
		ce.Reply("Usage: `!ai queue [status|reset|<mode>] [debounce:<dur>] [cap:<n>] [drop:<old|new|summarize>]`")
		return
	}

	if sessionKey != "" {
		client.updateSessionEntry(ce.Ctx, storeRef, sessionKey, func(entry sessionEntry) sessionEntry {
			if directive.QueueMode != "" {
				entry.QueueMode = string(directive.QueueMode)
			}
			if directive.DebounceMs != nil {
				entry.QueueDebounceMs = directive.DebounceMs
			}
			if directive.Cap != nil {
				entry.QueueCap = directive.Cap
			}
			if directive.DropPolicy != nil {
				entry.QueueDrop = string(*directive.DropPolicy)
			}
			entry.UpdatedAt = time.Now().UnixMilli()
			return entry
		})
	}

	queueSettings, _, _, _ = client.resolveQueueSettingsForPortal(ce.Ctx, portal, meta, "", QueueInlineOptions{})
	ce.Reply("%s", buildQueueStatusLine(queueSettings))
}

// CommandThink handles the !ai think command.
var CommandThink = registerAICommand(commandregistry.Definition{
	Name:           "think",
	Description:    "Get or set thinking level (off|minimal|low|medium|high|xhigh)",
	Args:           "[level]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnThink,
})

func fnThink(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	if meta.IsCodexRoom {
		ce.Reply("%s", formatSystemAck("Thinking level isn't supported in Codex rooms."))
		return
	}
	if len(ce.Args) == 0 {
		ce.Reply("Thinking: %s", client.defaultThinkLevel(meta))
		return
	}
	level, ok := normalizeThinkLevel(ce.Args[0])
	if !ok {
		ce.Reply("Usage: `!ai think off|minimal|low|medium|high|xhigh`")
		return
	}
	applyThinkingLevel(meta, level)
	client.savePortalQuiet(ce.Ctx, ce.Portal, "think change")
	ce.Reply("%s", formatThinkingAck(level))
}

// CommandVerbose handles the !ai verbose command.
var CommandVerbose = registerAICommand(commandregistry.Definition{
	Name:           "verbose",
	Aliases:        []string{"v"},
	Description:    "Get or set verbosity (off|on|full)",
	Args:           "[level]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnVerbose,
})

func fnVerbose(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	if meta.IsCodexRoom {
		ce.Reply("%s", formatSystemAck("Verbosity isn't supported in Codex rooms."))
		return
	}
	if len(ce.Args) == 0 {
		current := meta.VerboseLevel
		if current == "" {
			current = "off"
		}
		ce.Reply("Verbosity: %s", current)
		return
	}
	level, ok := normalizeVerboseLevel(ce.Args[0])
	if !ok {
		ce.Reply("Usage: `!ai verbose on|off|full`")
		return
	}
	meta.VerboseLevel = level
	client.savePortalQuiet(ce.Ctx, ce.Portal, "verbose change")
	ce.Reply("%s", formatVerboseAck(level))
}

// CommandReasoning handles the !ai reasoning command.
var CommandReasoning = registerAICommand(commandregistry.Definition{
	Name:           "reasoning",
	Description:    "Get or set reasoning visibility/effort (off|on|low|medium|high|xhigh)",
	Args:           "[level]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnReasoning,
})

func fnReasoning(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	if meta.IsCodexRoom {
		ce.Reply("%s", formatSystemAck("Reasoning settings aren't supported in Codex rooms."))
		return
	}
	if len(ce.Args) == 0 {
		current := strings.TrimSpace(meta.ReasoningEffort)
		if current == "" {
			if meta.EmitThinking {
				current = "on"
			} else {
				current = "off"
			}
		}
		ce.Reply("Reasoning: %s", current)
		return
	}
	level, ok := normalizeReasoningLevel(ce.Args[0])
	if !ok {
		ce.Reply("Usage: `!ai reasoning off|on|low|medium|high|xhigh`")
		return
	}
	applyReasoningLevel(meta, level)
	client.savePortalQuiet(ce.Ctx, ce.Portal, "reasoning change")
	ce.Reply("%s", formatReasoningAck(level))
}

// CommandElevated handles the !ai elevated command.
var CommandElevated = registerAICommand(commandregistry.Definition{
	Name:           "elevated",
	Aliases:        []string{"elev"},
	Description:    "Get or set elevated access (off|on|ask|full)",
	Args:           "[level]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnElevated,
})

func fnElevated(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	if meta.IsCodexRoom {
		ce.Reply("%s", formatSystemAck("Elevated mode isn't supported in Codex rooms."))
		return
	}
	if len(ce.Args) == 0 {
		current := meta.ElevatedLevel
		if current == "" {
			current = "off"
		}
		ce.Reply("Elevated access: %s", current)
		return
	}
	level, ok := normalizeElevatedLevel(ce.Args[0])
	if !ok {
		ce.Reply("Usage: `!ai elevated off|on|ask|full`")
		return
	}
	meta.ElevatedLevel = level
	client.savePortalQuiet(ce.Ctx, ce.Portal, "elevated change")
	ce.Reply("%s", formatElevatedAck(level))
}

// CommandActivation handles the !ai activation command.
var CommandActivation = registerAICommand(commandregistry.Definition{
	Name:           "activation",
	Description:    "Set group activation policy (mention|always)",
	Args:           "<mention|always>",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnActivation,
})

func fnActivation(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	if meta.IsCodexRoom {
		ce.Reply("%s", formatSystemAck("Activation isn't supported in Codex rooms."))
		return
	}
	isGroup := client.isGroupChat(ce.Ctx, ce.Portal)
	if !isGroup {
		ce.Reply("%s", formatSystemAck("Group activation only applies to group chats."))
		return
	}
	if len(ce.Args) == 0 {
		ce.Reply("%s", formatSystemAck("Usage: `!ai activation mention|always`"))
		return
	}
	level, ok := normalizeGroupActivation(ce.Args[0])
	if !ok {
		ce.Reply("%s", formatSystemAck("Usage: `!ai activation mention|always`"))
		return
	}
	meta.GroupActivation = level
	meta.GroupActivationNeedsIntro = true
	meta.GroupIntroSent = false
	client.savePortalQuiet(ce.Ctx, ce.Portal, "activation change")
	ce.Reply("%s", formatSystemAck(fmt.Sprintf("Group activation set to %s.", level)))
}

// CommandSend handles the !ai send command.
var CommandSend = registerAICommand(commandregistry.Definition{
	Name:           "send",
	Description:    "Allow/deny sending messages (on|off|inherit)",
	Args:           "<on|off|inherit>",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnSend,
})

func fnSend(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	if meta.IsCodexRoom {
		ce.Reply("%s", formatSystemAck("Send policy isn't supported in Codex rooms."))
		return
	}
	if len(ce.Args) == 0 {
		ce.Reply("%s", formatSystemAck("Usage: `!ai send on|off|inherit`"))
		return
	}
	mode, ok := normalizeSendPolicy(ce.Args[0])
	if !ok {
		ce.Reply("%s", formatSystemAck("Usage: `!ai send on|off|inherit`"))
		return
	}
	if mode == "inherit" {
		meta.SendPolicy = ""
	} else {
		meta.SendPolicy = mode
	}
	client.savePortalQuiet(ce.Ctx, ce.Portal, "send policy change")
	label := mode
	if mode == "allow" {
		label = "on"
	} else if mode == "deny" {
		label = "off"
	}
	ce.Reply("%s", formatSystemAck(fmt.Sprintf("Send policy set to %s.", label)))
}

// CommandWhoami handles the !ai whoami command.
var CommandWhoami = registerAICommand(commandregistry.Definition{
	Name:           "whoami",
	Aliases:        []string{"id"},
	Description:    "Show your Matrix user ID",
	Section:        HelpSectionAI,
	RequiresPortal: false,
	RequiresLogin:  false,
	Handler:        fnWhoami,
})

func fnWhoami(ce *commands.Event) {
	if ce == nil || ce.User == nil {
		return
	}
	ce.Reply("You are %s.", ce.User.MXID.String())
}
