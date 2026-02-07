package connector

import (
	"fmt"
	"slices"
	"strings"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
	"github.com/beeper/ai-bridge/pkg/opencode"
	"github.com/beeper/ai-bridge/pkg/opencodebridge"
)

const (
	opencodeManageUsage = "`!ai opencode add [http_url] [password] [username]` | `!ai opencode list` | `!ai opencode new [instance_id_or_url] [title]` | `!ai opencode remove [instance_id_or_url]`."
)

// CommandOpenCode handles the !ai opencode command.
var CommandOpenCode = registerAICommand(commandregistry.Definition{
	Name:          "opencode",
	Aliases:       []string{"openconnect"},
	Description:   "Manage OpenCode connections",
	Args:          "<add|list|new|remove> [args]",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnOpenCodeCommand,
})

func fnOpenCodeCommand(ce *commands.Event) {
	if len(ce.Args) == 0 {
		ce.Reply("Usage: %s", opencodeManageUsage)
		return
	}
	sub := strings.ToLower(strings.TrimSpace(ce.Args[0]))
	switch sub {
	case "add", "connect":
		ce.Args = ce.Args[1:]
		fnOpenCodeConnect(ce)
		return
	case "list", "ls":
		fnOpenCodeList(ce)
		return
	case "remove", "rm", "delete", "disconnect":
		fnOpenCodeRemove(ce)
		return
	case "new":
		ce.Args = ce.Args[1:]
		fnOpenCodeNew(ce)
		return
	default:
		ce.Reply("Usage: %s", opencodeManageUsage)
	}
}

func fnOpenCodeConnect(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	url := "http://127.0.0.1:4096"
	if len(ce.Args) >= 1 {
		url = strings.TrimSpace(ce.Args[0])
	}
	if url == "" {
		ce.Reply("Usage: `!ai opencode add [http_url] [password] [username]`. Default http_url: http://127.0.0.1:4096")
		return
	}

	password := ""
	if len(ce.Args) >= 2 {
		candidate := strings.TrimSpace(ce.Args[1])
		if candidate != "" && candidate != "-" {
			password = candidate
		}
	}
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
		ce.Reply("No OpenCode instances are connected. Use `!ai opencode add` first.")
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

func fnOpenCodeList(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	meta := loginMetadata(client.UserLogin)
	if meta == nil || len(meta.OpenCodeInstances) == 0 {
		ce.Reply("No OpenCode instances are connected. Use `!ai opencode add` first.")
		return
	}

	ids := make([]string, 0, len(meta.OpenCodeInstances))
	for id := range meta.OpenCodeInstances {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		cfg := meta.OpenCodeInstances[id]
		if cfg == nil {
			continue
		}
		status := "disconnected"
		if client.opencodeBridge != nil && client.opencodeBridge.IsConnected(id) {
			status = "connected"
		}
		line := fmt.Sprintf("- %s: %s", id, status)
		if cfg.URL != "" || cfg.Username != "" {
			label := strings.TrimSpace(cfg.URL)
			if cfg.Username != "" {
				if label != "" {
					label = fmt.Sprintf("%s as %s", label, cfg.Username)
				} else {
					label = fmt.Sprintf("as %s", cfg.Username)
				}
			}
			if label != "" {
				line = fmt.Sprintf("%s (%s)", line, label)
			}
		}
		lines = append(lines, line)
	}

	ce.Reply("OpenCode instances:\n%s", strings.Join(lines, "\n"))
}

func fnOpenCodeRemove(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}
	candidate := ""
	if len(ce.Args) >= 2 {
		candidate = strings.TrimSpace(strings.Join(ce.Args[1:], " "))
	}
	instanceID := ""
	if candidate == "" {
		meta := loginMetadata(client.UserLogin)
		if meta == nil || len(meta.OpenCodeInstances) == 0 {
			ce.Reply("No OpenCode instances are connected. Use `!ai opencode add` first.")
			return
		}
		if len(meta.OpenCodeInstances) > 1 {
			ce.Reply("Multiple OpenCode instances connected. Provide an instance ID or URL. Use `!ai opencode list`.")
			return
		}
		for id := range meta.OpenCodeInstances {
			instanceID = id
			break
		}
	} else {
		var ok bool
		instanceID, ok = resolveOpenCodeInstanceArg(client, candidate)
		if !ok {
			ce.Reply("Unknown OpenCode instance. Provide an instance ID or URL. Use `!ai opencode list`.")
			return
		}
	}
	if client.opencodeBridge == nil {
		ce.Reply("OpenCode integration is not available.")
		return
	}
	if err := client.opencodeBridge.RemoveInstance(ce.Ctx, instanceID); err != nil {
		ce.Reply("Failed to remove OpenCode instance: %v", err)
		return
	}
	ce.Reply("OpenCode instance removed: %s", instanceID)
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
