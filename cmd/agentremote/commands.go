package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
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
				"agentremote start opencode --profile work",
				"agentremote start ai --wait",
				"agentremote start ai --wait --wait-timeout 120s",
			},
			Run: cmdStart,
		},
		{
			Name: "up", Group: "Bridges",
			Description: "Start a bridge in the background",
			Usage:       "agentremote up <bridge> [flags]",
			PosArgs:     "bridge",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
				{Name: "name", Help: "Instance name (for multiple instances of the same bridge)"},
				{Name: "env", Help: "Override beeper env for this bridge", Values: envNames()},
				{Name: "wait", Help: "Block until bridge is connected", IsBool: true},
				{Name: "wait-timeout", Help: "Timeout for --wait", Default: "60s"},
			},
			Examples: []string{
				"agentremote up ai",
				"agentremote up codex --name test",
			},
			Run: cmdUp,
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
				"agentremote init openclaw --name dev",
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
			Name: "down", Group: "Bridges",
			Description: "Stop a running bridge",
			Usage:       "agentremote down <instance> [flags]",
			PosArgs:     "instance",
			Flags: []flagDef{
				{Name: "profile", Help: "Profile name", Default: "default"},
			},
			Examples: []string{
				"agentremote down ai",
				"agentremote down codex-test",
			},
			Run: cmdDown,
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
				{Name: "remote", Help: "Deprecated: remote deletion always happens", IsBool: true},
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
			Description: "Check AgentRemote CLI auth and local instance state",
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
	return slices.Sorted(maps.Keys(bridgeRegistry))
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
	b.WriteString("AgentRemote CLI - unified bridge manager for Beeper\n")
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

// ── Generated completions ──

func generateBashCompletion() string {
	var b strings.Builder
	names := commandNames()
	bridges := bridgeNames()

	b.WriteString("_" + binaryName + "() {\n")
	b.WriteString("    local cur prev commands\n")
	b.WriteString("    COMPREPLY=()\n")
	b.WriteString("    cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("    prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	fmt.Fprintf(&b, "    commands=%q\n", strings.Join(names, " "))
	b.WriteString("\n    case \"${prev}\" in\n")
	fmt.Fprintf(&b, "        %s)\n", binaryName)
	b.WriteString("            COMPREPLY=($(compgen -W \"${commands}\" -- \"${cur}\"))\n")
	b.WriteString("            return 0\n")
	b.WriteString("            ;;\n")

	// Group commands by PosArgs type for positional completion
	posGroups := visibleCommandsByPosArg()
	if cmds, ok := posGroups["bridge"]; ok {
		fmt.Fprintf(&b, "        %s)\n", strings.Join(cmds, "|"))
		fmt.Fprintf(&b, "            COMPREPLY=($(compgen -W %q -- \"${cur}\"))\n", strings.Join(bridges, " "))
		b.WriteString("            return 0\n")
		b.WriteString("            ;;\n")
	}
	if _, ok := posGroups["command"]; ok {
		b.WriteString("        help)\n")
		b.WriteString("            COMPREPLY=($(compgen -W \"${commands}\" -- \"${cur}\"))\n")
		b.WriteString("            return 0\n")
		b.WriteString("            ;;\n")
	}
	if _, ok := posGroups["shell"]; ok {
		b.WriteString("        completion)\n")
		b.WriteString("            COMPREPLY=($(compgen -W \"bash zsh fish\" -- \"${cur}\"))\n")
		b.WriteString("            return 0\n")
		b.WriteString("            ;;\n")
	}

	// Value completion for flags with Values
	valueFlags := map[string][]string{} // flag name → values
	for _, c := range visibleCommands() {
		for _, f := range c.Flags {
			if len(f.Values) > 0 {
				valueFlags["--"+f.Name] = f.Values
			}
		}
	}
	for flag, vals := range valueFlags {
		fmt.Fprintf(&b, "        %s)\n", flag)
		fmt.Fprintf(&b, "            COMPREPLY=($(compgen -W %q -- \"${cur}\"))\n", strings.Join(vals, " "))
		b.WriteString("            return 0\n")
		b.WriteString("            ;;\n")
	}

	b.WriteString("    esac\n\n")

	// Flag completions per command
	b.WriteString("    if [[ \"${cur}\" == -* ]]; then\n")
	b.WriteString("        case \"${COMP_WORDS[1]}\" in\n")
	for _, c := range visibleCommands() {
		if len(c.Flags) == 0 {
			continue
		}
		var flagNames []string
		for _, f := range c.Flags {
			flagNames = append(flagNames, "--"+f.Name)
			if f.Short != "" {
				flagNames = append(flagNames, "-"+f.Short)
			}
		}
		fmt.Fprintf(&b, "            %s)\n", c.Name)
		fmt.Fprintf(&b, "                COMPREPLY=($(compgen -W %q -- \"${cur}\"))\n", strings.Join(flagNames, " "))
		b.WriteString("                ;;\n")
	}
	b.WriteString("        esac\n")
	b.WriteString("        return 0\n")
	b.WriteString("    fi\n")
	b.WriteString("}\n")
	fmt.Fprintf(&b, "complete -F _%s %s\n", binaryName, binaryName)

	return b.String()
}

func generateZshCompletion() string {
	var b strings.Builder
	bridges := bridgeNames()

	fmt.Fprintf(&b, "#compdef %s\n\n", binaryName)
	b.WriteString("_" + binaryName + "() {\n")
	b.WriteString("    local -a commands bridges shells envs outputs\n")

	// Commands list
	b.WriteString("    commands=(\n")
	for _, c := range visibleCommands() {
		fmt.Fprintf(&b, "        '%s:%s'\n", c.Name, c.Description)
	}
	b.WriteString("    )\n")
	fmt.Fprintf(&b, "    bridges=(%s)\n", strings.Join(bridges, " "))
	b.WriteString("    shells=(bash zsh fish)\n")

	b.WriteString("\n    if (( CURRENT == 2 )); then\n")
	fmt.Fprintf(&b, "        _describe -t commands '%s command' commands\n", binaryName)
	b.WriteString("        return\n")
	b.WriteString("    fi\n")

	b.WriteString("\n    case \"${words[2]}\" in\n")

	for _, c := range visibleCommands() {
		if len(c.Flags) == 0 && c.PosArgs == "" {
			continue
		}
		fmt.Fprintf(&b, "        %s)\n", c.Name)

		if c.PosArgs == "bridge" {
			b.WriteString("            if (( CURRENT == 3 )); then\n")
			b.WriteString("                _describe -t bridges 'bridge type' bridges\n")
			b.WriteString("            else\n")
			writeZshArguments(&b, c.Flags, "                ")
			b.WriteString("            fi\n")
		} else if c.PosArgs == "shell" {
			b.WriteString("            if (( CURRENT == 3 )); then\n")
			b.WriteString("                _describe -t shells 'shell' shells\n")
			b.WriteString("            fi\n")
		} else if c.PosArgs == "command" {
			b.WriteString("            if (( CURRENT == 3 )); then\n")
			b.WriteString("                _describe -t commands 'command' commands\n")
			b.WriteString("            fi\n")
		} else if len(c.Flags) > 0 {
			writeZshArguments(&b, c.Flags, "            ")
		}

		b.WriteString("            ;;\n")
	}

	b.WriteString("    esac\n")
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "_%s \"$@\"\n", binaryName)

	return b.String()
}

func writeZshArguments(b *strings.Builder, flags []flagDef, indent string) {
	if len(flags) == 1 {
		f := flags[0]
		fmt.Fprintf(b, "%s_arguments '%s'\n", indent, zshFlagSpec(f))
		return
	}
	fmt.Fprintf(b, "%s_arguments \\\n", indent)
	for i, f := range flags {
		spec := zshFlagSpec(f)
		if i < len(flags)-1 {
			fmt.Fprintf(b, "%s    '%s' \\\n", indent, spec)
		} else {
			fmt.Fprintf(b, "%s    '%s'\n", indent, spec)
		}
	}
}

func zshFlagSpec(f flagDef) string {
	if f.Short != "" && f.IsBool {
		return fmt.Sprintf("{--%s,-%s}[%s]", f.Name, f.Short, f.Help)
	}
	spec := fmt.Sprintf("--%s[%s]", f.Name, f.Help)
	if !f.IsBool {
		if len(f.Values) > 0 {
			spec += fmt.Sprintf(":%s:(%s)", f.Name, strings.Join(f.Values, " "))
		} else {
			spec += fmt.Sprintf(":%s:", f.Name)
		}
	}
	return spec
}

func generateFishCompletion() string {
	var b strings.Builder
	names := commandNames()
	bridges := bridgeNames()

	fmt.Fprintf(&b, "# Fish completions for %s\n\n", binaryName)
	fmt.Fprintf(&b, "set -l commands %s\n", strings.Join(names, " "))
	fmt.Fprintf(&b, "set -l bridges %s\n", strings.Join(bridges, " "))
	b.WriteString("\n# Disable file completions by default\n")
	fmt.Fprintf(&b, "complete -c %s -f\n", binaryName)

	// Top-level commands
	b.WriteString("\n# Top-level commands\n")
	for _, c := range visibleCommands() {
		fmt.Fprintf(&b, "complete -c %s -n \"not __fish_seen_subcommand_from $commands\" -a %q -d %q\n", binaryName, c.Name, c.Description)
	}

	// Positional arg completions
	b.WriteString("\n# Positional argument completions\n")
	posGroups := visibleCommandsByPosArg()
	bridgeCmds := posGroups["bridge"]
	shellCmds := posGroups["shell"]
	commandCmds := posGroups["command"]
	if len(bridgeCmds) > 0 {
		fmt.Fprintf(&b, "complete -c %s -n \"__fish_seen_subcommand_from %s\" -a \"$bridges\"\n", binaryName, strings.Join(bridgeCmds, " "))
	}
	if len(shellCmds) > 0 {
		fmt.Fprintf(&b, "complete -c %s -n \"__fish_seen_subcommand_from %s\" -a \"bash zsh fish\"\n", binaryName, strings.Join(shellCmds, " "))
	}
	if len(commandCmds) > 0 {
		fmt.Fprintf(&b, "complete -c %s -n \"__fish_seen_subcommand_from %s\" -a \"$commands\"\n", binaryName, strings.Join(commandCmds, " "))
	}

	// Flag completions
	b.WriteString("\n# Flag completions\n")
	// Group flags by flag definition to find which commands share them
	type flagCmd struct {
		flag flagDef
		cmds []string
	}
	flagIndex := map[string]*flagCmd{}
	for _, c := range visibleCommands() {
		for _, f := range c.Flags {
			key := f.Name
			if fc, ok := flagIndex[key]; ok {
				fc.cmds = append(fc.cmds, c.Name)
			} else {
				flagIndex[key] = &flagCmd{flag: f, cmds: []string{c.Name}}
			}
		}
	}
	// Sort for deterministic output
	flagKeys := slices.Sorted(maps.Keys(flagIndex))

	for _, key := range flagKeys {
		fc := flagIndex[key]
		f := fc.flag
		condition := fmt.Sprintf("__fish_seen_subcommand_from %s", strings.Join(fc.cmds, " "))
		args := ""
		if len(f.Values) > 0 {
			args = fmt.Sprintf(" -a %q", strings.Join(f.Values, " "))
		}
		fmt.Fprintf(&b, "complete -c %s -n %q -l %s -d %q%s\n", binaryName, condition, f.Name, f.Help, args)
		if f.Short != "" {
			fmt.Fprintf(&b, "complete -c %s -n %q -s %s -d %q\n", binaryName, condition, f.Short, f.Help)
		}
	}

	return b.String()
}
