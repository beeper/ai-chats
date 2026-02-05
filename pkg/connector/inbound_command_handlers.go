package connector

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/agents"
)

type inboundCommandResult struct {
	handled  bool
	newBody  string
	response string
}

func (oc *AIClient) handleInboundCommand(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	isGroup bool,
	queueSettings QueueSettings,
	cmd inboundCommand,
) inboundCommandResult {
	if portal == nil || meta == nil {
		return inboundCommandResult{}
	}

	switch cmd.Name {
	case "status":
		return inboundCommandResult{handled: true, response: oc.buildStatusText(ctx, portal, meta, isGroup, queueSettings)}
	case "context":
		return inboundCommandResult{handled: true, response: oc.buildContextStatus(ctx, portal, meta)}
	case "tools":
		if strings.TrimSpace(cmd.Args) == "" || strings.EqualFold(strings.TrimSpace(cmd.Args), "list") {
			return inboundCommandResult{handled: true, response: oc.buildToolsStatusText(meta)}
		}
		return inboundCommandResult{handled: true, response: "Usage:\n• /tools - Show current tool status\n• /tools list - List available tools\nTool toggles are managed by tool policy."}
	case "typing":
		args := strings.TrimSpace(cmd.Args)
		if args == "" {
			mode := oc.resolveTypingMode(meta, &TypingContext{IsGroup: isGroup, WasMentioned: !isGroup}, false)
			interval := oc.resolveTypingInterval(meta)
			response := fmt.Sprintf("Typing: mode=%s interval=%s", mode, formatTypingInterval(interval))
			if meta.TypingMode != "" || meta.TypingIntervalSeconds != nil {
				overrideMode := "default"
				if meta.TypingMode != "" {
					overrideMode = meta.TypingMode
				}
				overrideInterval := "default"
				if meta.TypingIntervalSeconds != nil {
					overrideInterval = fmt.Sprintf("%ds", *meta.TypingIntervalSeconds)
				}
				response = fmt.Sprintf("%s (session override: mode=%s interval=%s)", response, overrideMode, overrideInterval)
			}
			return inboundCommandResult{handled: true, response: response}
		}

		token, rest := splitCommandArgs(args)
		token = strings.ToLower(strings.TrimSpace(token))
		rest = strings.TrimSpace(rest)

		switch token {
		case "reset", "default":
			meta.TypingMode = ""
			meta.TypingIntervalSeconds = nil
			oc.savePortalQuiet(ctx, portal, "typing reset")
			return inboundCommandResult{handled: true, response: "Typing settings reset to defaults."}
		case "off":
			meta.TypingMode = string(TypingModeNever)
			oc.savePortalQuiet(ctx, portal, "typing mode")
			if rest == "" {
				return inboundCommandResult{handled: true, response: "Typing disabled for this session."}
			}
			return inboundCommandResult{newBody: rest}
		case "interval":
			if rest == "" {
				return inboundCommandResult{handled: true, response: "Usage: /typing interval <seconds>"}
			}
			seconds, err := parsePositiveInt(rest)
			if err != nil || seconds <= 0 {
				return inboundCommandResult{handled: true, response: "Interval must be a positive integer (seconds)."}
			}
			meta.TypingIntervalSeconds = &seconds
			oc.savePortalQuiet(ctx, portal, "typing interval")
			return inboundCommandResult{handled: true, response: fmt.Sprintf("Typing interval set to %ds.", seconds)}
		default:
			if mode, ok := normalizeTypingMode(token); ok {
				meta.TypingMode = string(mode)
				oc.savePortalQuiet(ctx, portal, "typing mode")
				if rest == "" {
					return inboundCommandResult{handled: true, response: fmt.Sprintf("Typing mode set to %s.", mode)}
				}
				return inboundCommandResult{newBody: rest}
			}
		}
		return inboundCommandResult{handled: true, response: "Usage: /typing <never|instant|thinking|message>\n• /typing interval <seconds>\n• /typing off\n• /typing reset"}
	case "model":
		modelToken, rest := splitCommandArgs(cmd.Args)
		if modelToken == "" {
			return inboundCommandResult{handled: true, response: fmt.Sprintf("Current model: %s", oc.effectiveModel(meta))}
		}
		if agents.IsBossAgent(meta.AgentID) || agents.IsBossAgent(meta.DefaultAgentID) {
			return inboundCommandResult{handled: true, response: "Cannot change model in a room managed by the Boss agent."}
		}
		if agentID := resolveAgentID(meta); agentID != "" {
			return inboundCommandResult{handled: true, response: "Cannot set room model while an agent is assigned. Edit the agent instead."}
		}

		oldModel := meta.Model
		normalized := strings.TrimSpace(modelToken)
		if strings.EqualFold(normalized, "default") || strings.EqualFold(normalized, "reset") {
			meta.Model = ""
			newModel := oc.effectiveModel(meta)
			meta.Capabilities = getModelCapabilities(newModel, oc.findModelInfo(newModel))
			oc.savePortalQuiet(ctx, portal, "model reset")
			if oldModel != "" && newModel != "" && newModel != oldModel {
				oc.handleModelSwitch(ctx, portal, oldModel, newModel)
			}
			if rest == "" {
				return inboundCommandResult{handled: true, response: fmt.Sprintf("Model reset to default: %s", newModel)}
			}
			return inboundCommandResult{newBody: rest}
		}

		valid, err := oc.validateModel(ctx, normalized)
		if err != nil || !valid {
			return inboundCommandResult{handled: true, response: fmt.Sprintf("Invalid model: %s", normalized)}
		}
		meta.Model = normalized
		meta.Capabilities = getModelCapabilities(normalized, oc.findModelInfo(normalized))
		oc.savePortalQuiet(ctx, portal, "model change")
		oc.ensureGhostDisplayName(ctx, normalized)
		if oldModel != "" && normalized != oldModel {
			oc.handleModelSwitch(ctx, portal, oldModel, normalized)
		}
		if rest == "" {
			return inboundCommandResult{handled: true, response: fmt.Sprintf("Model changed to: %s", normalized)}
		}
		return inboundCommandResult{newBody: rest}
	case "think":
		levelToken, rest := splitCommandArgs(cmd.Args)
		if levelToken == "" {
			current := meta.ThinkingLevel
			if current == "" {
				if meta.EmitThinking {
					current = "on"
				} else {
					current = "off"
				}
			}
			return inboundCommandResult{handled: true, response: fmt.Sprintf("Thinking: %s", current)}
		}
		level, ok := normalizeThinkLevel(levelToken)
		if !ok {
			return inboundCommandResult{handled: true, response: "Usage: /think off|minimal|low|medium|high|xhigh"}
		}
		meta.ThinkingLevel = level
		meta.EmitThinking = level != "off"
		if level == "minimal" {
			meta.ReasoningEffort = "low"
		} else if level == "low" || level == "medium" || level == "high" || level == "xhigh" {
			meta.ReasoningEffort = level
		}
		oc.savePortalQuiet(ctx, portal, "think change")
		if rest == "" {
			if level == "off" {
				return inboundCommandResult{handled: true, response: "Thinking disabled."}
			}
			return inboundCommandResult{handled: true, response: fmt.Sprintf("Thinking level set to %s.", level)}
		}
		return inboundCommandResult{newBody: rest}
	case "verbose":
		levelToken, rest := splitCommandArgs(cmd.Args)
		if levelToken == "" {
			current := meta.VerboseLevel
			if current == "" {
				current = "off"
			}
			return inboundCommandResult{handled: true, response: fmt.Sprintf("Verbosity: %s", current)}
		}
		level, ok := normalizeVerboseLevel(levelToken)
		if !ok {
			return inboundCommandResult{handled: true, response: "Usage: /verbose on|off|full"}
		}
		meta.VerboseLevel = level
		oc.savePortalQuiet(ctx, portal, "verbose change")
		if rest == "" {
			switch level {
			case "off":
				return inboundCommandResult{handled: true, response: formatSystemAck("Verbose logging disabled.")}
			case "full":
				return inboundCommandResult{handled: true, response: formatSystemAck("Verbose logging set to full.")}
			default:
				return inboundCommandResult{handled: true, response: formatSystemAck("Verbose logging enabled.")}
			}
		}
		return inboundCommandResult{newBody: rest}
	case "reasoning":
		levelToken, rest := splitCommandArgs(cmd.Args)
		if levelToken == "" {
			current := meta.ReasoningEffort
			if current == "" {
				current = "off"
			}
			return inboundCommandResult{handled: true, response: fmt.Sprintf("Reasoning: %s", current)}
		}
		level, ok := normalizeReasoningLevel(levelToken)
		if !ok {
			return inboundCommandResult{handled: true, response: "Usage: /reasoning off|on|low|medium|high|xhigh"}
		}
		if level == "off" {
			meta.EmitThinking = false
			meta.ReasoningEffort = ""
		} else if level == "on" {
			meta.EmitThinking = true
		} else {
			meta.EmitThinking = true
			meta.ReasoningEffort = level
		}
		oc.savePortalQuiet(ctx, portal, "reasoning change")
		if rest == "" {
			switch level {
			case "off":
				return inboundCommandResult{handled: true, response: formatSystemAck("Reasoning visibility disabled.")}
			case "on":
				return inboundCommandResult{handled: true, response: formatSystemAck("Reasoning visibility enabled.")}
			default:
				return inboundCommandResult{handled: true, response: formatSystemAck("Reasoning visibility enabled.")}
			}
		}
		return inboundCommandResult{newBody: rest}
	case "elevated":
		levelToken, rest := splitCommandArgs(cmd.Args)
		if levelToken == "" {
			current := meta.ElevatedLevel
			if current == "" {
				current = "off"
			}
			return inboundCommandResult{handled: true, response: fmt.Sprintf("Elevated access: %s", current)}
		}
		level, ok := normalizeElevatedLevel(levelToken)
		if !ok {
			return inboundCommandResult{handled: true, response: "Usage: /elevated off|on|ask|full"}
		}
		meta.ElevatedLevel = level
		oc.savePortalQuiet(ctx, portal, "elevated change")
		if rest == "" {
			switch level {
			case "off":
				return inboundCommandResult{handled: true, response: formatSystemAck("Elevated mode disabled.")}
			case "full":
				return inboundCommandResult{handled: true, response: formatSystemAck("Elevated mode set to full (auto-approve).")}
			default:
				return inboundCommandResult{handled: true, response: formatSystemAck("Elevated mode set to ask (approvals may still apply).")}
			}
		}
		return inboundCommandResult{newBody: rest}
	case "activation":
		if !isGroup {
			return inboundCommandResult{handled: true, response: formatSystemAck("Group activation only applies to group chats.")}
		}
		levelToken, rest := splitCommandArgs(cmd.Args)
		if levelToken == "" {
			return inboundCommandResult{handled: true, response: formatSystemAck("Usage: /activation mention|always")}
		}
		level, ok := normalizeGroupActivation(levelToken)
		if !ok {
			return inboundCommandResult{handled: true, response: formatSystemAck("Usage: /activation mention|always")}
		}
		meta.GroupActivation = level
		meta.GroupActivationNeedsIntro = true
		meta.GroupIntroSent = false
		oc.savePortalQuiet(ctx, portal, "activation change")
		if rest == "" {
			return inboundCommandResult{handled: true, response: formatSystemAck(fmt.Sprintf("Group activation set to %s.", level))}
		}
		return inboundCommandResult{newBody: rest}
	case "send":
		modeToken, rest := splitCommandArgs(cmd.Args)
		if modeToken == "" {
			return inboundCommandResult{handled: true, response: formatSystemAck("Usage: /send on|off|inherit")}
		}
		mode, ok := normalizeSendPolicy(modeToken)
		if !ok {
			return inboundCommandResult{handled: true, response: formatSystemAck("Usage: /send on|off|inherit")}
		}
		if mode == "inherit" {
			meta.SendPolicy = ""
		} else {
			meta.SendPolicy = mode
		}
		oc.savePortalQuiet(ctx, portal, "send policy change")
		if rest == "" {
			label := mode
			if mode == "inherit" {
				label = "inherit"
			} else if mode == "allow" {
				label = "on"
			} else if mode == "deny" {
				label = "off"
			}
			return inboundCommandResult{handled: true, response: formatSystemAck(fmt.Sprintf("Send policy set to %s.", label))}
		}
		return inboundCommandResult{newBody: rest}
	case "new", "reset":
		meta.SessionResetAt = time.Now().UnixMilli()
		meta.GroupIntroSent = false
		meta.GroupActivationNeedsIntro = true
		oc.savePortalQuiet(ctx, portal, "session reset")
		oc.clearPendingQueue(portal.MXID)
		oc.cancelRoomRun(portal.MXID)
		if strings.TrimSpace(cmd.Args) == "" {
			return inboundCommandResult{newBody: sessionGreetingPrompt}
		}
		return inboundCommandResult{newBody: cmd.Args}
	default:
	}

	return inboundCommandResult{}
}

func parsePositiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("value must be positive")
	}
	return value, nil
}
