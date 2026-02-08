package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
)

// CommandPlayground handles the !ai playground command.
// This creates a playground room with minimal tools and no agent personality.
var CommandPlayground = registerAICommand(commandregistry.Definition{
	Name:          "playground",
	Aliases:       []string{"sandbox"},
	Description:   "Create a model playground chat (minimal tools, no personality)",
	Args:          "<model>",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnPlayground,
})

func fnPlayground(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	if len(ce.Args) == 0 {
		ce.Reply("Usage: !ai playground <model>\n\nExample: !ai playground claude-sonnet-4.5\n\nThis creates a raw model sandbox with minimal tools and no agent personality.")
		return
	}

	modelArg := ce.Args[0]

	// Resolve the model (handles aliases, prefixes, etc.)
	modelID, valid, err := client.resolveModelID(ce.Ctx, modelArg)
	if err != nil || !valid || modelID == "" {
		ce.Reply("That model isn't available: %s", modelArg)
		return
	}

	// Create a playground room with the specified model
	go func() {
		chatResp, err := client.createPlaygroundChat(ce.Ctx, modelID)
		if err != nil {
			client.log.Err(err).Str("model", modelID).Msg("Failed to create playground room")
			return
		}

		if chatResp != nil && chatResp.Portal != nil && chatResp.Portal.MXID != "" {
			client.sendSystemNotice(ce.Ctx, ce.Portal,
				"Playground room created: "+string(chatResp.Portal.MXID))
		}
	}()

	ce.Reply("Creating playground room with %s...", modelID)
}

// createPlaygroundChat creates a new chat room configured for playground mode.
// This sets up the room with the playground agent and the specified model.
func (oc *AIClient) createPlaygroundChat(ctx context.Context, modelID string) (*bridgev2.CreateChatResponse, error) {
	// Get the playground agent
	playgroundAgent := agents.GetPlaygroundAgent()

	// Create the chat using createAgentChatWithModel
	chatResp, err := oc.createAgentChatWithModel(ctx, playgroundAgent, modelID, true)
	if err != nil {
		return nil, err
	}

	// Set the IsRawMode flag on the portal metadata
	if chatResp != nil && chatResp.Portal != nil {
		meta := portalMeta(chatResp.Portal)
		meta.IsRawMode = true
		oc.savePortalQuiet(ctx, chatResp.Portal, "playground mode setup")
	}

	return chatResp, nil
}
