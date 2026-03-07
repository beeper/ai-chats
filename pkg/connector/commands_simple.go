package connector

import (
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
)

// CommandSimple handles the !ai simple command with sub-commands.
var _ = registerAICommand(commandregistry.Definition{
	Name:          "simple",
	Description:   "Manage AI chat rooms (new, list)",
	Args:          "<new [model] | list>",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnSimple,
})

func fnSimple(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	subCmd := ""
	if len(ce.Args) > 0 {
		subCmd = strings.ToLower(ce.Args[0])
	}

	switch subCmd {
	case "new":
		var modelID string
		if len(ce.Args) > 1 {
			resolved, valid, err := client.resolveModelID(ce.Ctx, ce.Args[1])
			if err != nil || !valid || resolved == "" {
				ce.Reply("That model isn't available: %s", ce.Args[1])
				return
			}
			modelID = resolved
		} else {
			modelID = client.effectiveModel(nil)
		}
		go client.createAndOpenSimpleChat(ce.Ctx, ce.Portal, modelID)
		ce.Reply("Creating AI chat with %s...", modelID)

	case "list":
		models, err := client.listAvailableModels(ce.Ctx, false)
		if err != nil {
			ce.Reply("Couldn't load models.")
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
			if len(caps) > 0 {
				sb.WriteString(fmt.Sprintf("  %s\n", strings.Join(caps, " · ")))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("Use `!ai simple new [model]` to create a chat")
		ce.Reply(sb.String())

	default:
		ce.Reply("Usage:\n• `!ai simple new [model]` — Create a new AI chat\n• `!ai simple list` — List available models")
	}
}
