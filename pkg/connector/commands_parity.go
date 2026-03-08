package connector

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// CommandStatus handles the !ai status command.
var _ = registerAICommand(commandregistry.Definition{
	Name:           "status",
	Description:    "Show current session status",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnStatus,
})

// CommandLastHeartbeat handles the !ai last-heartbeat command.
var _ = registerAICommand(commandregistry.Definition{
	Name:           "last-heartbeat",
	Description:    "Show the last heartbeat event for this login",
	Section:        HelpSectionAI,
	RequiresPortal: false,
	RequiresLogin:  true,
	Handler:        fnLastHeartbeat,
})

func fnStatus(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	isGroup := client.isGroupChat(ce.Ctx, ce.Portal)
	queueSettings, _, _, _ := client.resolveQueueSettingsForPortal(ce.Ctx, ce.Portal, meta, "", airuntime.QueueInlineOptions{})
	ce.Reply("%s", client.buildStatusText(ce.Ctx, ce.Portal, meta, isGroup, queueSettings))
}

func fnLastHeartbeat(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}
	evt := getLastHeartbeatEventForLogin(client.UserLogin)
	if evt == nil {
		ce.Reply("No heartbeat yet.")
		return
	}
	pretty, err := json.MarshalIndent(evt, "", "  ")
	if err != nil {
		ce.Reply("Failed to serialize last heartbeat: %s", err.Error())
		return
	}
	// Keep replies bounded; fall back to compact JSON if needed.
	if len(pretty) > 8000 {
		compact, err2 := json.Marshal(evt)
		if err2 == nil {
			pretty = compact
		}
	}
	ce.Reply("```json\n%s\n```", string(pretty))
}

// CommandReset handles the !ai reset command.
var _ = registerAICommand(commandregistry.Definition{
	Name:           "reset",
	Description:    "Start a new session/thread in this room",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnReset,
})

func fnReset(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	meta.SessionResetAt = time.Now().UnixMilli()
	meta.GroupIntroSent = false
	meta.GroupActivationNeedsIntro = true
	client.savePortalQuiet(ce.Ctx, ce.Portal, "session reset")
	client.clearPendingQueue(ce.Portal.MXID)
	client.cancelRoomRun(ce.Portal.MXID)

	ce.Reply("%s", formatSystemAck("Session reset."))
}

// CommandStop handles the !ai stop command.
var _ = registerAICommand(commandregistry.Definition{
	Name:           "stop",
	Description:    "Abort the current run and clear the pending queue",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnStop,
})

func fnStop(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	stopped := client.abortRoom(ce.Ctx, ce.Portal, meta)
	ce.Reply("%s", formatAbortNotice(stopped))
}

// CommandQueue handles the !ai queue command.
var _ = registerAICommand(commandregistry.Definition{
	Name:           "queue",
	Description:    "Inspect or configure the message queue",
	Args:           "[status|reset|<mode>] [debounce:<dur>] [cap:<n>] [drop:<old|new|summarize>]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnQueue,
})

func fnQueue(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	portal := ce.Portal

	queueSettings, _, storeRef, sessionKey := client.resolveQueueSettingsForPortal(ce.Ctx, portal, meta, "", airuntime.QueueInlineOptions{})

	if len(ce.Args) == 0 || strings.EqualFold(strings.TrimSpace(ce.Args[0]), "status") {
		ce.Reply("%s", buildQueueStatusLine(queueSettings))
		return
	}

	if strings.EqualFold(strings.TrimSpace(ce.Args[0]), "reset") {
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
		queueSettings, _, _, _ = client.resolveQueueSettingsForPortal(ce.Ctx, portal, meta, "", airuntime.QueueInlineOptions{})
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

	queueSettings, _, _, _ = client.resolveQueueSettingsForPortal(ce.Ctx, portal, meta, "", airuntime.QueueInlineOptions{})
	ce.Reply("%s", buildQueueStatusLine(queueSettings))
}

// CommandThink handles the !ai think command.
var _ = registerAICommand(commandregistry.Definition{
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
	if len(ce.Args) == 0 {
		ce.Reply("Thinking: %s", client.defaultThinkLevel(meta))
		return
	}
	level, ok := stringutil.NormalizeEnum(ce.Args[0], thinkLevelAliases)
	if !ok {
		ce.Reply("Usage: `!ai think off|minimal|low|medium|high|xhigh`")
		return
	}
	applyThinkingLevel(meta, level)
	client.savePortalQuiet(ce.Ctx, ce.Portal, "think change")
	ce.Reply("%s", formatThinkingAck(level))
}

// CommandVerbose handles the !ai verbose command.
var _ = registerAICommand(commandregistry.Definition{
	Name:           "verbose",
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
	if len(ce.Args) == 0 {
		current := meta.VerboseLevel
		if current == "" {
			current = "off"
		}
		ce.Reply("Verbosity: %s", current)
		return
	}
	level, ok := stringutil.NormalizeEnum(ce.Args[0], verboseLevelAliases)
	if !ok {
		ce.Reply("Usage: `!ai verbose on|off|full`")
		return
	}
	meta.VerboseLevel = level
	client.savePortalQuiet(ce.Ctx, ce.Portal, "verbose change")
	ce.Reply("%s", formatVerboseAck(level))
}

// CommandReasoning handles the !ai reasoning command.
var _ = registerAICommand(commandregistry.Definition{
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
	level, ok := stringutil.NormalizeEnum(ce.Args[0], reasoningLevelAliases)
	if !ok {
		ce.Reply("Usage: `!ai reasoning off|on|low|medium|high|xhigh`")
		return
	}
	applyReasoningLevel(meta, level)
	client.savePortalQuiet(ce.Ctx, ce.Portal, "reasoning change")
	ce.Reply("%s", formatReasoningAck(level))
}

// CommandElevated handles the !ai elevated command.
var _ = registerAICommand(commandregistry.Definition{
	Name:           "elevated",
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
	if len(ce.Args) == 0 {
		current := meta.ElevatedLevel
		if current == "" {
			current = "off"
		}
		ce.Reply("Elevated access: %s", current)
		return
	}
	level, ok := stringutil.NormalizeElevatedLevel(ce.Args[0])
	if !ok {
		ce.Reply("Usage: `!ai elevated off|on|ask|full`")
		return
	}
	meta.ElevatedLevel = level
	client.savePortalQuiet(ce.Ctx, ce.Portal, "elevated change")
	ce.Reply("%s", formatElevatedAck(level))
}

// CommandActivation handles the !ai activation command.
var _ = registerAICommand(commandregistry.Definition{
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
	isGroup := client.isGroupChat(ce.Ctx, ce.Portal)
	if !isGroup {
		ce.Reply("%s", formatSystemAck("Group activation only applies to group chats."))
		return
	}
	if len(ce.Args) == 0 {
		ce.Reply("%s", formatSystemAck("Usage: `!ai activation mention|always`"))
		return
	}
	level, ok := stringutil.NormalizeEnum(ce.Args[0], groupActivationAliases)
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
var _ = registerAICommand(commandregistry.Definition{
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
	if len(ce.Args) == 0 {
		ce.Reply("%s", formatSystemAck("Usage: `!ai send on|off|inherit`"))
		return
	}
	mode, ok := stringutil.NormalizeEnum(ce.Args[0], sendPolicyAliases)
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
var _ = registerAICommand(commandregistry.Definition{
	Name:           "whoami",
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
