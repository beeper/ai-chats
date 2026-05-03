package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
	"github.com/beeper/agentremote/cmd/internal/bridgeentry"
)

type flagDef struct {
	Name    string   // e.g., "profile"
	Short   string   // e.g., "f"
	Help    string   // description
	Default string   // default value for display ("" = no default shown)
	Values  []string // completion values (e.g., ["prod", "staging"])
	IsBool  bool     // boolean flag (no value argument)
}

type cmdDef struct {
	Name        string
	Group       string // "Auth", "Bridges", "Other"
	Description string
	Usage       string // full usage line
	LongHelp    string // optional extra paragraph
	PosArgs     string // positional arg type for completions: "bridge", "instance", "shell", "command", ""
	Flags       []flagDef
	Examples    []string
	Run         func([]string) error
	Hidden      bool // e.g., __bridge
}

var commands []cmdDef

func initCommands() {
	commands = []cmdDef{
		{
			Name: "__bridge", Group: "", Hidden: true,
			Run: cmdInternalBridge,
		},
		{
			Name: "login", Group: "Auth",
			Description: "Log in to Beeper",
			Usage:       "agentremote login [flags]",
			Flags: []flagDef{
				{Name: "env", Help: "Beeper environment", Default: "prod", Values: envNames()},
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "email", Help: "Email address (will prompt if not provided)"},
				{Name: "code", Help: "Login code (will prompt if not provided)"},
			},
			Examples: []string{
				"agentremote login",
				"agentremote login --env staging --email user@example.com",
			},
			Run: cmdLogin,
		},
		{
			Name: "logout", Group: "Auth",
			Description: "Clear stored credentials",
			Usage:       "agentremote logout [flags]",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
			},
			Examples: []string{
				"agentremote logout",
				"agentremote logout --profile work",
			},
			Run: cmdLogout,
		},
		{
			Name: "whoami", Group: "Auth",
			Description: "Show current user info",
			Usage:       "agentremote whoami [flags]",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "output", Help: "Output format", Default: "text", Values: []string{"text", "json"}},
			},
			Run: cmdWhoami,
		},
		{
			Name: "profiles", Group: "Auth",
			Description: "List all profiles",
			Usage:       "agentremote profiles [flags]",
			Flags: []flagDef{
				{Name: "output", Help: "Output format", Default: "text", Values: []string{"text", "json"}},
			},
			Run: cmdProfiles,
		},
		{
			Name: "start", Group: "Bridges",
			Description: "Start a bridge in the background",
			Usage:       "agentremote start <bridge> [flags]",
			PosArgs:     "bridge",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "name", Help: "Instance name (for multiple instances of the same bridge)"},
				{Name: "env", Help: "Override beeper env for this bridge", Values: envNames()},
				{Name: "wait", Help: "Block until bridge is connected", IsBool: true},
				{Name: "wait-timeout", Help: "Timeout for --wait", Default: "60s"},
			},
			Examples: []string{
				"agentremote start ai",
				"agentremote start codex --name test",
				"agentremote start codex --profile work",
				"agentremote start ai --wait",
				"agentremote start ai --wait --wait-timeout 120s",
			},
			Run: cmdStart,
		},
		{
			Name: "run", Group: "Bridges",
			Description: "Run a bridge in the foreground",
			Usage:       "agentremote run <bridge> [flags]",
			PosArgs:     "bridge",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "name", Help: "Instance name (for multiple instances of the same bridge)"},
				{Name: "env", Help: "Override beeper env for this bridge", Values: envNames()},
			},
			Examples: []string{
				"agentremote run ai",
				"agentremote run codex --name dev",
			},
			Run: cmdRun,
		},
		{
			Name: "init", Group: "Bridges",
			Description: "Initialize local config and metadata for a bridge",
			Usage:       "agentremote init <bridge> [flags]",
			PosArgs:     "bridge",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "name", Help: "Instance name (for multiple instances of the same bridge)"},
				{Name: "env", Help: "Override beeper env for this bridge", Values: envNames()},
			},
			Examples: []string{
				"agentremote init ai",
				"agentremote init codex --name dev",
			},
			Run: cmdInit,
		},
		{
			Name: "stop", Group: "Bridges",
			Description: "Stop a running bridge",
			Usage:       "agentremote stop <instance> [flags]",
			PosArgs:     "instance",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
			},
			Examples: []string{
				"agentremote stop ai",
				"agentremote stop codex-test",
			},
			Run: cmdStop,
		},
		{
			Name: "stop-all", Group: "Bridges",
			Description: "Stop all running bridges",
			Usage:       "agentremote stop-all [flags]",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
			},
			Run: cmdStopAll,
		},
		{
			Name: "restart", Group: "Bridges",
			Description: "Restart a bridge",
			Usage:       "agentremote restart <bridge> [flags]",
			PosArgs:     "bridge",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "name", Help: "Instance name"},
			},
			Examples: []string{
				"agentremote restart ai",
			},
			Run: cmdRestart,
		},
		{
			Name: "status", Group: "Bridges",
			Description: "Show bridge status",
			Usage:       "agentremote status [instance...] [flags]",
			LongHelp:    "Shows local instance status and remote bridge state from the Beeper server.\nIf no instance names are given, shows all instances.",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "no-remote", Help: "Skip fetching remote bridge state from server", IsBool: true},
				{Name: "output", Help: "Output format", Default: "text", Values: []string{"text", "json"}},
			},
			Examples: []string{
				"agentremote status",
				"agentremote status ai",
				"agentremote status --no-remote",
			},
			Run: cmdStatus,
		},
		{
			Name: "register", Group: "Bridges",
			Description: "Ensure bridge registration without starting the process",
			Usage:       "agentremote register <bridge> [flags]",
			PosArgs:     "bridge",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "name", Help: "Instance name (for multiple instances of the same bridge)"},
				{Name: "env", Help: "Override beeper env for this bridge", Values: envNames()},
				{Name: "output", Help: "Write registration YAML to a separate path", Default: "-"},
				{Name: "json", Help: "Print registration metadata as JSON", IsBool: true},
			},
			Examples: []string{
				"agentremote register ai",
				"agentremote register codex --name dev --json",
			},
			Run: cmdRegister,
		},
		{
			Name: "logs", Group: "Bridges",
			Description: "View bridge logs",
			Usage:       "agentremote logs <instance> [flags]",
			PosArgs:     "instance",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "follow", Short: "f", Help: "Follow log output (like tail -f)", IsBool: true},
			},
			Examples: []string{
				"agentremote logs ai",
				"agentremote logs ai -f",
			},
			Run: cmdLogs,
		},
		{
			Name: "list", Group: "Bridges",
			Description: "List available bridge types",
			Usage:       "agentremote list",
			Run:         func(args []string) error { return cmdList() },
		},
		{
			Name: "instances", Group: "Bridges",
			Description: "List local bridge instances for a profile",
			Usage:       "agentremote instances [flags]",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "output", Help: "Output format", Default: "text", Values: []string{"text", "json"}},
			},
			Run: cmdInstances,
		},
		{
			Name: "delete", Group: "Bridges",
			Description: "Delete a bridge instance locally and remotely",
			Usage:       "agentremote delete [instance] [flags]",
			PosArgs:     "instance",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
			},
			Examples: []string{
				"agentremote delete",
				"agentremote delete ai",
				"agentremote delete codex-test",
			},
			Run: cmdDelete,
		},
		{
			Name: "version", Group: "Other",
			Description: "Show version info",
			Usage:       "agentremote version",
			Run:         func(args []string) error { return cmdVersion() },
		},
		{
			Name: "doctor", Group: "Other",
			Description: "Check AgentRemote Manager auth and local instance state",
			Usage:       "agentremote doctor [flags]",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "output", Help: "Output format", Default: "text", Values: []string{"text", "json"}},
			},
			Run: cmdDoctor,
		},
		{
			Name: "auth", Group: "Other",
			Description: "Manage stored auth tokens",
			Usage:       "agentremote auth <set-token|show|whoami> [flags]",
			PosArgs:     "command",
			Examples: []string{
				"agentremote auth set-token --token syt_...",
				"agentremote auth show --profile work",
				"agentremote auth whoami",
			},
			Run: cmdAuth,
		},
		{
			Name: "completion", Group: "Other",
			Description: "Generate shell completion script",
			Usage:       "agentremote completion <bash|zsh|fish>",
			PosArgs:     "shell",
			Examples: []string{
				"# Bash (add to ~/.bashrc)",
				"source <(agentremote completion bash)",
				"",
				"# Zsh (add to ~/.zshrc)",
				"source <(agentremote completion zsh)",
				"",
				"# Fish",
				"agentremote completion fish | source",
			},
			Run: cmdCompletion,
		},
		{
			Name: "help", Group: "Other",
			Description: "Show help for a command",
			Usage:       "agentremote help [command]",
			PosArgs:     "command",
			Run:         cmdHelp,
		},
	}
	normalizeCommandSpecs()
}

func normalizeCommandSpecs() {
	for i := range commands {
		commands[i].Description = strings.ReplaceAll(commands[i].Description, "agentremote", binaryName)
		commands[i].Usage = strings.ReplaceAll(commands[i].Usage, "agentremote", binaryName)
		commands[i].LongHelp = strings.ReplaceAll(commands[i].LongHelp, "agentremote", binaryName)
		for j := range commands[i].Examples {
			commands[i].Examples[j] = strings.ReplaceAll(commands[i].Examples[j], "agentremote", binaryName)
		}
	}
}

func envNames() []string {
	names := beeperauth.EnvNames()
	slices.Sort(names)
	return names
}

func bridgeNames() []string {
	return bridgeentry.Names()
}

func visibleCommands() []cmdDef {
	var out []cmdDef
	for _, c := range commands {
		if !c.Hidden {
			out = append(out, c)
		}
	}
	return out
}

func commandNames() []string {
	var out []string
	for _, c := range visibleCommands() {
		out = append(out, c.Name)
	}
	return out
}

func visibleCommandsByGroup(group string) []cmdDef {
	var out []cmdDef
	for _, c := range visibleCommands() {
		if c.Group == group {
			out = append(out, c)
		}
	}
	return out
}

func visibleCommandsByPosArg() map[string][]string {
	groups := make(map[string][]string)
	for _, c := range visibleCommands() {
		if c.PosArgs != "" {
			groups[c.PosArgs] = append(groups[c.PosArgs], c.Name)
		}
	}
	return groups
}

func findCommand(name string) *cmdDef {
	for i := range commands {
		if commands[i].Name == name {
			return &commands[i]
		}
	}
	return nil
}

// ── Generated help ──

func generateCommandHelp(c *cmdDef) string {
	var b strings.Builder
	b.WriteString(c.Description)
	b.WriteByte('\n')
	if c.LongHelp != "" {
		b.WriteByte('\n')
		b.WriteString(c.LongHelp)
		b.WriteByte('\n')
	}
	if c.Usage != "" {
		b.WriteString("\nUsage: ")
		b.WriteString(c.Usage)
		b.WriteByte('\n')
	}
	if len(c.Flags) > 0 {
		b.WriteString("\nFlags:\n")
		// Compute alignment width
		maxWidth := 0
		for _, f := range c.Flags {
			w := len(f.Name) + 2 // --name
			if f.Short != "" {
				w += len(f.Short) + 3 // , -f
			}
			if maxWidth < w {
				maxWidth = w
			}
		}
		for _, f := range c.Flags {
			label := "--" + f.Name
			if f.Short != "" {
				label += ", -" + f.Short
			}
			help := f.Help
			if f.Default != "" {
				help += fmt.Sprintf(" (default: %s)", f.Default)
			}
			fmt.Fprintf(&b, "  %-*s  %s\n", maxWidth, label, help)
		}
	}
	if len(c.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, ex := range c.Examples {
			if ex == "" {
				b.WriteByte('\n')
			} else {
				b.WriteString("  ")
				b.WriteString(ex)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func generateUsage() string {
	var b strings.Builder
	b.WriteString("AgentRemote Manager - unified bridge manager for Beeper\n")
	b.WriteString("\nUsage: " + binaryName + " <command> [flags] [args]\n")

	groups := []string{"Auth", "Bridges", "Other"}
	for _, group := range groups {
		cmds := visibleCommandsByGroup(group)
		if len(cmds) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s:\n", group)
		for _, c := range cmds {
			fmt.Fprintf(&b, "  %-12s%s\n", c.Name, c.Description)
		}
	}

	b.WriteString("\nGlobal flags:\n")
	b.WriteString("  --profile   Profile name (default: \"default\")\n")
	return b.String()
}
