package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/beeper/bridge-manager/api/beeperapi"

	"github.com/beeper/agentremote/cmd/internal/cliutil"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

func parseBridgeFlags(fs *flag.FlagSet) (*string, *string, *string) {
	profile := fs.String("profile", defaultProfile, "profile name")
	name := fs.String("name", "", "instance name (for running multiple instances of the same bridge)")
	env := fs.String("env", "", "override beeper env for this bridge")
	return profile, name, env
}

type bridgeSetup struct {
	instName   string
	beeperName string
	bridgeType string
	profile    string
	meta       *metadata
}

// setupBridgeCmd consolidates the common setup sequence used by lifecycle
// commands: parse bridge flags, resolve args, ensure layout & init, and
// optionally ensure registration.
func setupBridgeCmd(fs *flag.FlagSet, args []string, withRegistration bool, extraFlags func(*flag.FlagSet)) (*bridgeSetup, *string, error) {
	profile, name, env := parseBridgeFlags(fs)
	if extraFlags != nil {
		extraFlags(fs)
	}
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	bridgeType, err := resolveBridgeArgs(fs)
	if err != nil {
		return nil, nil, err
	}
	deviceID, err := ensureProfileDeviceID(*profile)
	if err != nil {
		return nil, nil, err
	}
	instName := instanceDirName(bridgeType, *name)
	beeperName := beeperBridgeName(deviceID, bridgeType, *name)

	sp, err := ensureInstanceLayout(*profile, instName)
	if err != nil {
		return nil, nil, err
	}
	meta, err := ensureInitialized(instName, bridgeType, beeperName, sp)
	if err != nil {
		return nil, nil, err
	}
	if withRegistration {
		if err = ensureRegistration(*profile, *env, meta, bridgeType); err != nil {
			return nil, nil, err
		}
	}
	return &bridgeSetup{
		instName:   instName,
		beeperName: beeperName,
		bridgeType: bridgeType,
		profile:    *profile,
		meta:       meta,
	}, env, nil
}

func availableBridgeNames() string {
	return strings.Join(bridgeNames(), ", ")
}

func resolveBridgeArgs(fs *flag.FlagSet) (bridgeType string, err error) {
	posArgs := fs.Args()
	if len(posArgs) != 1 {
		return "", fmt.Errorf("expected exactly one bridge type argument (available: %s)", availableBridgeNames())
	}
	bridgeType = posArgs[0]
	if _, ok := lookupBridge(bridgeType); !ok {
		return "", fmt.Errorf("unknown bridge type %q (available: %s)", bridgeType, availableBridgeNames())
	}
	return bridgeType, nil
}

func cmdStart(args []string) error {
	fs := newFlagSet("start")
	var wait *bool
	var waitTimeout *time.Duration
	bs, env, err := setupBridgeCmd(fs, args, true, func(fs *flag.FlagSet) {
		wait = fs.Bool("wait", false, "block until bridge is connected (timeout 60s)")
		waitTimeout = fs.Duration("wait-timeout", 60*time.Second, "timeout for --wait")
	})
	if err != nil {
		return err
	}
	running, pid := bridgeutil.ProcessAliveFromPIDFile(bs.meta.PIDPath)
	if running {
		fmt.Printf("%s already running (pid %d)\n", bs.instName, pid)
		if *wait {
			return waitForBridge(bs.profile, *env, bs.beeperName, *waitTimeout)
		}
		return nil
	}
	if err = startBridgeProcess(bs.meta, bs.bridgeType); err != nil {
		return err
	}
	fmt.Printf("started %s\n", bs.instName)
	cliutil.PrintRuntimePaths(bs.meta)
	if *wait {
		return waitForBridge(bs.profile, *env, bs.beeperName, *waitTimeout)
	}
	return nil
}

func waitForBridge(profile, envOverride, beeperName string, timeout time.Duration) error {
	cfg, err := getAuthWithOverride(profile, envOverride)
	if err != nil {
		return err
	}
	fmt.Printf("waiting for %s to be connected...\n", beeperName)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := beeperapi.Whoami(cfg.Domain, cfg.Token)
		if err == nil {
			if bridge, ok := resp.User.Bridges[beeperName]; ok {
				state := string(bridge.BridgeState.StateEvent)
				if state == "RUNNING" || state == "CONNECTED" {
					fmt.Printf("%s is %s\n", beeperName, state)
					return nil
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for %s to be connected", beeperName)
}

func cmdRun(args []string) error {
	fs := newFlagSet("run")
	bs, _, err := setupBridgeCmd(fs, args, true, nil)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find own executable: %w", err)
	}
	argv := []string{exe, "__bridge", bs.bridgeType, "-c", bs.meta.ConfigPath}
	fmt.Printf("running %s in foreground\n", bs.instName)
	cliutil.PrintRuntimePaths(bs.meta)
	if err = os.Chdir(filepath.Dir(bs.meta.ConfigPath)); err != nil {
		return fmt.Errorf("failed to chdir: %w", err)
	}
	return syscall.Exec(exe, argv, os.Environ())
}

func cmdInit(args []string) error {
	fs := newFlagSet("init")
	bs, _, err := setupBridgeCmd(fs, args, false, nil)
	if err != nil {
		return err
	}
	fmt.Printf("initialized %s\n", bs.instName)
	cliutil.PrintRuntimePaths(bs.meta)
	return nil
}

func cmdStop(args []string) error {
	fs := newFlagSet("stop")
	profile := fs.String("profile", defaultProfile, "profile name")
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
	pidPath := sp.PIDPath
	if meta, err := cliutil.ReadMetadata(sp.MetaPath); err == nil {
		pidPath = meta.PIDPath
	}
	stopped, err := bridgeutil.StopByPIDFile(pidPath)
	if err != nil {
		return err
	}
	if stopped {
		fmt.Printf("stopped %s\n", instName)
	} else {
		fmt.Printf("%s is not running\n", instName)
	}
	return nil
}

func cmdStopAll(args []string) error {
	fs := newFlagSet("stop-all")
	profile := fs.String("profile", defaultProfile, "profile name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instances, err := listInstancesForProfile(*profile)
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		fmt.Println("no instances found")
		return nil
	}
	for _, inst := range instances {
		sp, err := getInstancePaths(*profile, inst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: error: %v\n", inst, err)
			continue
		}
		stopped, err := bridgeutil.StopByPIDFile(sp.PIDPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: error stopping: %v\n", inst, err)
			continue
		}
		if stopped {
			fmt.Printf("stopped %s\n", inst)
		}
	}
	return nil
}

func cmdRestart(args []string) error {
	fs := newFlagSet("restart")
	profile, name, _ := parseBridgeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	bridgeType, err := resolveBridgeArgs(fs)
	if err != nil {
		return err
	}
	instName := instanceDirName(bridgeType, *name)
	if err := cmdStop([]string{"--profile", *profile, instName}); err != nil {
		return err
	}
	startArgs := []string{"--profile", *profile}
	if *name != "" {
		startArgs = append(startArgs, "--name", *name)
	}
	startArgs = append(startArgs, bridgeType)
	return cmdStart(startArgs)
}
