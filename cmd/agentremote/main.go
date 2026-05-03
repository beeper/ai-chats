package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/beeper/agentremote/cmd/internal/cliutil"
)

var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

const binaryName = "agentremote"

type metadata = cliutil.Metadata

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	initCommands()
	if len(os.Args) < 2 {
		fmt.Print(generateUsage())
		return nil
	}
	name := os.Args[1]
	if name == "-h" || name == "--help" {
		name = "help"
	}
	if name == "--version" || name == "-v" {
		return cmdVersion()
	}
	c := findCommand(name)
	if c == nil {
		return didYouMean(name)
	}
	err := c.Run(os.Args[2:])
	if errors.Is(err, flag.ErrHelp) {
		// Flag parsing hit -h/--help; show our generated help instead of Go's default
		if !c.Hidden {
			fmt.Print(generateCommandHelp(c))
		}
		return nil
	}
	return err
}

// newFlagSet creates a FlagSet that suppresses Go's default -h output,
// so our generated help is shown instead.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// ANSI color helpers — automatically disabled when stdout is not a terminal.
var colorEnabled = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}()

func colorize(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + "\033[0m"
}

func green(s string) string  { return colorize("\033[32m", s) }
func red(s string) string    { return colorize("\033[31m", s) }
func yellow(s string) string { return colorize("\033[33m", s) }
func dim(s string) string    { return colorize("\033[2m", s) }

func colorState(state string) string {
	switch state {
	case "RUNNING", "CONNECTED":
		return green(state)
	case "STARTING", "RECONNECTING":
		return yellow(state)
	case "STOPPED", "ERROR", "BRIDGE_UNREACHABLE", "TRANSIENT_DISCONNECT":
		return red(state)
	default:
		return state
	}
}

func colorLocal(running bool, pid int) string {
	if running {
		return green("running") + fmt.Sprintf(" (pid %d)", pid)
	}
	return red("stopped")
}

func cmdHelp(args []string) error {
	if len(args) == 0 {
		fmt.Print(generateUsage())
		return nil
	}
	if c := findCommand(args[0]); c != nil && !c.Hidden {
		fmt.Print(generateCommandHelp(c))
		return nil
	}
	return didYouMean(args[0])
}

func didYouMean(input string) error {
	best := ""
	bestDist := 4 // only suggest if distance <= 3
	for _, name := range commandNames() {
		d := levenshtein(input, name)
		if d < bestDist {
			bestDist = d
			best = name
		}
	}
	if best != "" {
		return fmt.Errorf("unknown command %q. Did you mean %q?", input, best)
	}
	return fmt.Errorf("unknown command %q, run '%s help' for usage", input, binaryName)
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
