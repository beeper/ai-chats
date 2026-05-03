package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

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
