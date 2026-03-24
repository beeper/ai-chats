package main

import (
	"fmt"
	"os"

	"maunium.net/go/mautrix/bridgev2"
)

// cmdInternalBridge handles the hidden "__bridge" subcommand.
// Usage: agentremote __bridge <bridge-type> [bridge-flags...]
// This is invoked by the start/run commands via self-exec.
func cmdInternalBridge(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("__bridge requires a bridge type argument")
	}
	bridgeType := args[0]
	def, ok := bridgeRegistry[bridgeType]
	if !ok {
		return fmt.Errorf("unknown bridge type %q", bridgeType)
	}

	// Replace os.Args so mxmain sees: <binary> [bridge-flags...]
	// e.g. agentremote __bridge ai -c config.yaml → ai -c config.yaml
	os.Args = append([]string{def.Name}, args[1:]...)
	if bridgeType == "ai" || bridgeType == "agent" {
		bridgev2.PortalEventBuffer = 0
	}

	m := def.Definition.NewMain(def.NewFunc())
	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
	return nil
}
