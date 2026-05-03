package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"

	"github.com/beeper/agentremote/sdk"
)

var aiCommandSection = commands.HelpSection{Name: "AI", Order: 40}

func (oc *OpenAIConnector) registerCommands(br *bridgev2.Bridge) {
	if br == nil {
		return
	}
	proc, ok := br.Commands.(*commands.Processor)
	if !ok || proc == nil {
		return
	}
	proc.AddHandlers(
		aiCommand("model", "Show or change the model for this chat", "[model ID]", aiCommandModel),
		aiCommand("thinking", "Show or change the thinking level for this chat", "[none|low|medium|high]", aiCommandThinking),
		aiCommand("fork", "Fork this model chat with the full transcript", "[model ID]", aiCommandFork),
		aiCommand("new", "Create a new empty model chat", "[model ID]", aiCommandNew),
		aiCommand("stop", "Stop the current room's active and queued AI work", "", aiCommandStop),
		aiCommand("status", "Show this model chat's configuration", "", aiCommandStatus),
		aiCommand("edit-mode", "Show or change edit regeneration behavior", "[next-turn|all-nexts]", aiCommandEditMode),
	)
}

func aiCommand(name, description, args string, fn func(*commands.Event, *AIClient)) *commands.FullHandler {
	return &commands.FullHandler{
		Name:           name,
		RequiresLogin:  true,
		RequiresPortal: true,
		Help: commands.HelpMeta{
			Section:     aiCommandSection,
			Description: description,
			Args:        args,
		},
		Func: func(ce *commands.Event) {
			client := aiClientForCommand(ce)
			if client == nil {
				ce.Reply("You're not logged into AI Chats in this room.")
				return
			}
			fn(ce, client)
		},
	}
}

func aiClientForCommand(ce *commands.Event) *AIClient {
	if ce == nil || ce.User == nil {
		return nil
	}
	login, err := sdk.ResolveCommandLogin(ce.Ctx, ce, ce.User.GetDefaultLogin())
	if err != nil || login == nil {
		return nil
	}
	client, _ := login.Client.(*AIClient)
	return client
}

func commandPortal(ctx context.Context, oc *AIClient, portal *bridgev2.Portal) (*bridgev2.Portal, *PortalMetadata, error) {
	if portal == nil {
		return nil, nil, fmt.Errorf("missing portal")
	}
	resolved, err := resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return nil, nil, err
	}
	return resolved, portalMeta(resolved), nil
}

func aiCommandModel(ce *commands.Event, oc *AIClient) {
	portal, meta, err := commandPortal(ce.Ctx, oc, ce.Portal)
	if err != nil {
		ce.Reply("Failed to resolve this chat: %v", err)
		return
	}
	if strings.TrimSpace(ce.RawArgs) == "" {
		ce.Reply("Current model: `%s`\n\nUsage: `$cmdprefix model <model ID>`", oc.effectiveModel(meta))
		return
	}
	modelID := strings.TrimSpace(ce.RawArgs)
	resolved, valid, err := oc.resolveModelID(ce.Ctx, modelID)
	if err != nil {
		ce.Reply("Failed to validate model: %v", err)
		return
	}
	if !valid || resolved == "" {
		ce.Reply("Unknown model: `%s`", modelID)
		return
	}
	if err := oc.switchPortalModel(ce.Ctx, portal, resolved); err != nil {
		ce.Reply("Failed to switch model: %v", err)
		return
	}
	ce.Reply("Model set to `%s`.", resolved)
}

func normalizeThinking(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none":
		return "none"
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func effectiveThinking(meta *PortalMetadata) string {
	if meta == nil {
		return "none"
	}
	if value := normalizeThinking(meta.Thinking); value != "" {
		return value
	}
	return "none"
}

func (oc *AIClient) modelSupportsThinking(ctx context.Context, meta *PortalMetadata) bool {
	return oc.getModelCapabilitiesForMeta(ctx, meta).SupportsReasoning
}

func aiCommandThinking(ce *commands.Event, oc *AIClient) {
	portal, meta, err := commandPortal(ce.Ctx, oc, ce.Portal)
	if err != nil {
		ce.Reply("Failed to resolve this chat: %v", err)
		return
	}
	if strings.TrimSpace(ce.RawArgs) == "" {
		support := "available"
		if !oc.modelSupportsThinking(ce.Ctx, meta) {
			support = "not supported by the current model"
		}
		ce.Reply("Current thinking: `%s`\nAvailable values: `none`, `low`, `medium`, `high` (%s).", effectiveThinking(meta), support)
		return
	}
	value := normalizeThinking(ce.RawArgs)
	if value == "" {
		ce.Reply("Usage: `$cmdprefix thinking none|low|medium|high`")
		return
	}
	if value != "none" && !oc.modelSupportsThinking(ce.Ctx, meta) {
		ce.Reply("The current model `%s` doesn't support thinking levels.", oc.effectiveModel(meta))
		return
	}
	meta.Thinking = value
	if err := oc.savePortal(ce.Ctx, portal, "thinking"); err != nil {
		ce.Reply("Failed to save thinking level: %v", err)
		return
	}
	ce.Reply("Thinking set to `%s`.", value)
}

func aiCommandNew(ce *commands.Event, oc *AIClient) {
	modelID, err := oc.modelArgOrCurrent(ce.Ctx, portalMeta(ce.Portal), ce.RawArgs)
	if err != nil {
		ce.Reply("%v", err)
		return
	}
	resp, err := oc.createChat(ce.Ctx, chatCreateParams{ModelID: modelID})
	if err != nil {
		ce.Reply("Failed to create chat: %v", err)
		return
	}
	ce.Reply("Created new chat with `%s`: %s", modelID, resp.Portal.MXID.URI().MatrixToURL())
}

func aiCommandFork(ce *commands.Event, oc *AIClient) {
	source, sourceMeta, err := commandPortal(ce.Ctx, oc, ce.Portal)
	if err != nil {
		ce.Reply("Failed to resolve this chat: %v", err)
		return
	}
	modelID, err := oc.modelArgOrCurrent(ce.Ctx, sourceMeta, ce.RawArgs)
	if err != nil {
		ce.Reply("%v", err)
		return
	}
	resp, err := oc.createChat(ce.Ctx, chatCreateParams{ModelID: modelID})
	if err != nil {
		ce.Reply("Failed to create fork: %v", err)
		return
	}
	if err := oc.copyTranscriptToFork(ce.Ctx, source, resp.Portal); err != nil {
		ce.Reply("Created fork, but failed to copy transcript: %v", err)
		return
	}
	ce.Reply("Forked chat with `%s`: %s", modelID, resp.Portal.MXID.URI().MatrixToURL())
}

func (oc *AIClient) modelArgOrCurrent(ctx context.Context, meta *PortalMetadata, raw string) (string, error) {
	modelID := strings.TrimSpace(raw)
	if modelID == "" {
		modelID = oc.effectiveModel(meta)
	}
	resolved, valid, err := oc.resolveModelID(ctx, modelID)
	if err != nil {
		return "", fmt.Errorf("failed to validate model: %w", err)
	}
	if !valid || resolved == "" {
		return "", fmt.Errorf("unknown model: `%s`", modelID)
	}
	return resolved, nil
}

func aiCommandStop(ce *commands.Event, oc *AIClient) {
	portal, meta, err := commandPortal(ce.Ctx, oc, ce.Portal)
	if err != nil {
		ce.Reply("Failed to resolve this chat: %v", err)
		return
	}
	result := oc.handleUserStop(ce.Ctx, userStopRequest{
		Portal:             portal,
		Meta:               meta,
		RequestedByEventID: ce.EventID,
		RequestedVia:       "command",
	})
	ce.Reply("%s", formatAbortNotice(result))
}

func aiCommandStatus(ce *commands.Event, oc *AIClient) {
	portal, meta, err := commandPortal(ce.Ctx, oc, ce.Portal)
	if err != nil {
		ce.Reply("Failed to resolve this chat: %v", err)
		return
	}
	caps := oc.getModelCapabilitiesForMeta(ce.Ctx, meta)
	queueSettings := resolveQueueSettings(queueResolveParams{cfg: &oc.connector.Config})
	_, sourceEventID, _, _ := oc.roomRunTarget(portal.MXID)
	tools := oc.selectedBuiltinToolsForTurn(ce.Ctx, meta)
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			toolNames = append(toolNames, tool.Name)
		}
	}
	sort.Strings(toolNames)
	if len(toolNames) == 0 {
		toolNames = append(toolNames, "none")
	}
	ce.Reply(strings.Join([]string{
		fmt.Sprintf("Model: `%s`", oc.effectiveModel(meta)),
		fmt.Sprintf("Provider: `%s`", loginMetadata(oc.UserLogin).Provider),
		fmt.Sprintf("Thinking: `%s`", effectiveThinking(meta)),
		fmt.Sprintf("Edit mode: `%s`", resolveEditMode(meta)),
		fmt.Sprintf("Tools: `%s`", strings.Join(toolNames, "`, `")),
		fmt.Sprintf("Capabilities: vision=%t audio=%t video=%t pdf=%t imagegen=%t tool-calling=%t reasoning=%t", caps.SupportsVision, caps.SupportsAudio, caps.SupportsVideo, caps.SupportsPDF, caps.SupportsImageGen, caps.SupportsToolCalling, caps.SupportsReasoning),
		fmt.Sprintf("History: direct=%d group=%d", oc.historyLimit(ce.Ctx, portal, meta), oc.resolveGroupHistoryLimit()),
		fmt.Sprintf("Queue: mode=%s debounce_ms=%d cap=%d drop=%s", queueSettings.Mode, queueSettings.DebounceMs, queueSettings.Cap, queueSettings.DropPolicy),
		fmt.Sprintf("Links: enabled=%t", getLinkPreviewConfig(&oc.connector.Config).Enabled),
		fmt.Sprintf("Running: %t queued=%t source=%s", sourceEventID != "", oc.roomHasPendingQueueWork(portal.MXID), sourceEventID),
	}, "\n"))
}

func aiCommandEditMode(ce *commands.Event, oc *AIClient) {
	portal, meta, err := commandPortal(ce.Ctx, oc, ce.Portal)
	if err != nil {
		ce.Reply("Failed to resolve this chat: %v", err)
		return
	}
	mode := strings.TrimSpace(ce.RawArgs)
	if mode == "" {
		ce.Reply("Current edit mode: `%s`\n\nUsage: `$cmdprefix edit-mode next-turn|all-nexts`", resolveEditMode(meta))
		return
	}
	mode = normalizeEditMode(mode)
	if mode == "" {
		ce.Reply("Usage: `$cmdprefix edit-mode next-turn|all-nexts`")
		return
	}
	meta.EditMode = mode
	if err := oc.savePortal(ce.Ctx, portal, "edit mode"); err != nil {
		ce.Reply("Failed to save edit mode: %v", err)
		return
	}
	ce.Reply("Edit mode set to `%s`.", mode)
}

func (oc *AIClient) copyTranscriptToFork(ctx context.Context, source *bridgev2.Portal, target *bridgev2.Portal) error {
	if source == nil || target == nil {
		return fmt.Errorf("missing portal")
	}
	rows, err := oc.currentTranscriptTurns(ctx, source)
	if err != nil {
		return err
	}
	for _, row := range rows {
		msg := aiHistoryMessageFromTurn(target.PortalKey, row)
		if msg == nil {
			continue
		}
		if err := oc.persistAIConversationMessage(ctx, target, msg); err != nil {
			return err
		}
		if err := oc.replayTranscriptMessage(ctx, target, msg); err != nil {
			return err
		}
	}
	return nil
}

func (oc *AIClient) currentTranscriptTurns(ctx context.Context, portal *bridgev2.Portal) ([]*aiTurnRecord, error) {
	return withResolvedPortalScopeValue(ctx, oc, portal, func(ctx context.Context, portal *bridgev2.Portal, scope *portalScope) ([]*aiTurnRecord, error) {
		rows, err := loadAICurrentContextTurnsByScope(ctx, scope, aiTurnQuery{
			includeInHistory: true,
			roles:            []string{"user", "assistant"},
			limit:            10000,
		})
		if err != nil {
			return nil, err
		}
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
		return rows, nil
	})
}

func (oc *AIClient) replayTranscriptMessage(ctx context.Context, portal *bridgev2.Portal, msg *database.Message) error {
	meta := messageMeta(msg)
	if meta == nil || strings.TrimSpace(meta.Body) == "" {
		return nil
	}
	body := strings.TrimSpace(meta.Body)
	role := strings.TrimSpace(meta.Role)
	if role == "user" {
		body = "User: " + body
	} else if role == "assistant" {
		body = "Assistant: " + body
	}
	rendered := format.RenderMarkdown(body, true, true)
	sender := oc.senderForPortal(ctx, portal)
	if role == "user" {
		sender = bridgev2.EventSender{Sender: humanUserID(oc.UserLogin.ID), SenderLogin: oc.UserLogin.ID}
	}
	_, _, err := sdk.SendViaPortal(sdk.SendViaPortalParams{
		Login:     oc.UserLogin,
		Portal:    portal,
		Sender:    sender,
		IDPrefix:  "ai-fork",
		LogKey:    "ai_fork_msg_id",
		Timestamp: time.Now(),
		Converted: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:   networkid.PartID("0"),
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgNotice,
					Body:          rendered.Body,
					Format:        rendered.Format,
					FormattedBody: rendered.FormattedBody,
					Mentions:      &event.Mentions{},
				},
			}},
		},
	})
	return err
}
