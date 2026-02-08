package connector

import (
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
)

// CommandManage handles the !ai manage command.
// This creates or opens the Builder room for advanced users to manage custom agents.
var CommandManage = registerAICommand(commandregistry.Definition{
	Name:          "manage",
	Description:   "Open the agent management room (for creating custom agents)",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnManage,
})

func fnManage(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	meta := loginMetadata(client.UserLogin)

	// Check if Builder room already exists
	if meta.BuilderRoomID != "" {
		portalKey := networkid.PortalKey{
			ID:       meta.BuilderRoomID,
			Receiver: client.UserLogin.ID,
		}
		portal, err := client.UserLogin.Bridge.GetPortalByKey(ce.Ctx, portalKey)
		if err == nil && portal != nil && portal.MXID != "" {
			ce.Reply("Agent management room: %s", portal.MXID)
			return
		}
		// Room doesn't exist anymore, will create new one
	}

	// Create Builder room on-demand
	if err := client.ensureBuilderRoom(ce.Ctx); err != nil {
		ce.Reply("Couldn't create the management room: %v", err)
		return
	}

	// Get the newly created room
	meta = loginMetadata(client.UserLogin)
	portalKey := networkid.PortalKey{
		ID:       meta.BuilderRoomID,
		Receiver: client.UserLogin.ID,
	}
	portal, err := client.UserLogin.Bridge.GetPortalByKey(ce.Ctx, portalKey)
	if err != nil || portal == nil || portal.MXID == "" {
		ce.Reply("Management room created, but the link isn't available.")
		return
	}

	ce.Reply("Created agent management room: %s\n\nIn this room you can:\n- Create custom agents\n- Manage existing agents\n- Configure advanced settings", portal.MXID)
}
