package connector

import (
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
	"github.com/beeper/ai-bridge/pkg/opencode"
	"github.com/beeper/ai-bridge/pkg/opencodebridge"
)

// CommandOpenCodeConnect handles the !ai opencode-connect command.
var CommandOpenCodeConnect = registerAICommand(commandregistry.Definition{
	Name:          "opencode-connect",
	Description:   "Connect to an OpenCode server and sync sessions",
	Args:          "<http_url> <password> [username]",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnOpenCodeConnect,
})

// CommandOpenCodeNew handles the !ai opencode-new command.
var CommandOpenCodeNew = registerAICommand(commandregistry.Definition{
	Name:          "opencode-new",
	Description:   "Create a new OpenCode session",
	Args:          "[instance_id_or_url] [title]",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnOpenCodeNew,
})

func fnOpenCodeConnect(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	if len(ce.Args) < 2 {
		ce.Reply("Usage: !ai opencode-connect <http_url> <password> [username]")
		return
	}

	url := strings.TrimSpace(ce.Args[0])
	password := strings.TrimSpace(ce.Args[1])
	username := "opencode"
	if len(ce.Args) >= 3 {
		username = strings.TrimSpace(ce.Args[2])
		if username == "" {
			username = "opencode"
		}
	}

	if client.opencodeBridge == nil {
		client.opencodeBridge = opencodebridge.NewBridge(client)
	}

	inst, count, err := client.opencodeBridge.Connect(ce.Ctx, url, password, username)
	if err != nil {
		ce.Reply("Failed to connect to OpenCode: %v", err)
		return
	}

	ce.Reply("Connected to OpenCode %s as %s. Synced %d sessions. Instance ID: %s", inst.URL, inst.Username, count, inst.ID)
}

func fnOpenCodeNew(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}
	if client.opencodeBridge == nil {
		ce.Reply("OpenCode integration is not available.")
		return
	}

	meta := portalMeta(ce.Portal)
	instanceID := ""
	title := ""

	instances := loginMetadata(client.UserLogin)
	if instances == nil || len(instances.OpenCodeInstances) == 0 {
		ce.Reply("No OpenCode instances are connected. Use `!ai opencode-connect` first.")
		return
	}

	if meta != nil && meta.IsOpenCodeRoom && meta.OpenCodeInstanceID != "" {
		instanceID = meta.OpenCodeInstanceID
		title = strings.TrimSpace(strings.Join(ce.Args, " "))
	} else {
		if len(ce.Args) > 0 {
			candidate := strings.TrimSpace(ce.Args[0])
			if resolved, ok := resolveOpenCodeInstanceArg(client, candidate); ok {
				instanceID = resolved
				if len(ce.Args) > 1 {
					title = strings.TrimSpace(strings.Join(ce.Args[1:], " "))
				}
			} else if len(instances.OpenCodeInstances) == 1 {
				for id := range instances.OpenCodeInstances {
					instanceID = id
					break
				}
				title = strings.TrimSpace(strings.Join(ce.Args, " "))
			} else {
				ce.Reply("Unknown OpenCode instance. Provide an instance ID or URL.")
				return
			}
		} else if len(instances.OpenCodeInstances) == 1 {
			for id := range instances.OpenCodeInstances {
				instanceID = id
				break
			}
		} else {
			ce.Reply("Multiple OpenCode instances connected. Provide an instance ID or URL.")
			return
		}
	}

	pendingTitle := strings.TrimSpace(title) == ""
	chatResp, err := client.opencodeBridge.CreateSessionChat(ce.Ctx, instanceID, title, pendingTitle)
	if err != nil {
		ce.Reply("Failed to create OpenCode session: %v", err)
		return
	}

	portal := chatResp.Portal
	if portal == nil {
		portal, _ = client.UserLogin.Bridge.GetPortalByKey(ce.Ctx, chatResp.PortalKey)
	}
	if portal != nil && portal.MXID == "" {
		if err := portal.CreateMatrixRoom(ce.Ctx, client.UserLogin, chatResp.PortalInfo); err != nil {
			ce.Reply("Failed to create room: %v", err)
			return
		}
	}

	if portal != nil && portal.MXID != "" {
		roomLink := fmt.Sprintf("https://matrix.to/#/%s", portal.MXID)
		ce.Reply("Created new OpenCode session.\nOpen: %s", roomLink)
		return
	}
	ce.Reply("Created new OpenCode session.")
}

func resolveOpenCodeInstanceArg(client *AIClient, candidate string) (string, bool) {
	if client == nil {
		return "", false
	}
	if instanceID, ok := opencodebridge.ParseOpenCodeIdentifier(candidate); ok {
		if client.opencodeBridge != nil && client.opencodeBridge.InstanceConfig(instanceID) != nil {
			return instanceID, true
		}
	}
	if client.opencodeBridge != nil {
		if cfg := client.opencodeBridge.InstanceConfig(candidate); cfg != nil {
			return candidate, true
		}
	}
	if normalized, err := opencode.NormalizeBaseURL(candidate); err == nil {
		meta := loginMetadata(client.UserLogin)
		if meta != nil {
			for id, cfg := range meta.OpenCodeInstances {
				if cfg != nil && strings.EqualFold(cfg.URL, normalized) {
					return id, true
				}
			}
		}
	}
	return "", false
}
