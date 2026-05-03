package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/beeper/agentremote/cmd/internal/cliutil"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

func cmdLogs(args []string) error {
	fs := newFlagSet("logs")
	profile := fs.String("profile", defaultProfile, "profile name")
	follow := fs.Bool("follow", false, "follow logs")
	fs.BoolVar(follow, "f", false, "follow logs (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	posArgs := fs.Args()
	if len(posArgs) != 1 {
		return fmt.Errorf("expected exactly one instance name argument")
	}
	instName := posArgs[0]

	sp, err := getInstancePaths(*profile, instName)
	if err != nil {
		return err
	}
	if *follow {
		cmd := exec.Command("tail", "-f", sp.LogPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	f, err := os.Open(sp.LogPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

func cmdRegister(args []string) error {
	fs := newFlagSet("register")
	var output *string
	var jsonOut *bool
	bs, _, err := setupBridgeCmd(fs, args, true, func(fs *flag.FlagSet) {
		output = fs.String("output", "-", "output path for registration YAML")
		jsonOut = fs.Bool("json", false, "print registration metadata as JSON")
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		payload := map[string]any{
			"instance":     bs.instName,
			"bridge_name":  bs.meta.BeeperBridgeName,
			"bridge_type":  bs.bridgeType,
			"profile":      bs.profile,
			"config":       bs.meta.ConfigPath,
			"registration": bs.meta.RegistrationPath,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}
	if *output != "-" {
		data, err := os.ReadFile(bs.meta.RegistrationPath)
		if err != nil {
			return err
		}
		if err = os.WriteFile(*output, data, 0o600); err != nil {
			return err
		}
		fmt.Printf("registration written to %s\n", *output)
		return nil
	}
	fmt.Printf("registration ensured for %s\n", bs.instName)
	return nil
}

func cmdList() error {
	fmt.Println("Available bridge types:")
	for _, name := range bridgeNames() {
		def, _ := lookupBridge(name)
		fmt.Printf("  %-10s %s\n", name, def.Description)
	}
	return nil
}

func cmdDelete(args []string) error {
	fs := newFlagSet("delete")
	profile := fs.String("profile", defaultProfile, "profile name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	posArgs := fs.Args()
	if len(posArgs) == 0 {
		return printRunningInstances(*profile)
	}
	if len(posArgs) != 1 {
		return fmt.Errorf("expected at most one instance name argument")
	}
	instName := posArgs[0]

	sp, err := getInstancePaths(*profile, instName)
	if err != nil {
		return err
	}
	// Stop if running
	if _, err := bridgeutil.StopByPIDFile(sp.PIDPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to stop: %v\n", err)
	}
	meta, readErr := cliutil.ReadMetadata(sp.MetaPath)
	if readErr == nil {
		if err := deleteRemoteBridge(*profile, meta.BeeperBridgeName); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to delete remote bridge: %v\n", err)
		}
	}
	if err := os.RemoveAll(sp.Root); err != nil {
		return err
	}
	fmt.Printf("deleted %s\n", instName)
	return nil
}
