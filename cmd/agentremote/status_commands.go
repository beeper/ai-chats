package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/beeper/bridge-manager/api/beeperapi"

	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

type bridgeStatus struct {
	Name       string        `json:"name"`
	State      string        `json:"state,omitempty"`
	SelfHosted bool          `json:"self_hosted,omitempty"`
	Local      *localStatus  `json:"local,omitempty"`
	Logins     []loginStatus `json:"logins,omitempty"`
}

type localStatus struct {
	Running    bool   `json:"running"`
	PID        int    `json:"pid,omitempty"`
	ConfigPath string `json:"config_path"`
}

type loginStatus struct {
	RemoteID   string `json:"remote_id"`
	State      string `json:"state"`
	RemoteName string `json:"remote_name,omitempty"`
}

func cmdStatus(args []string) error {
	fs := newFlagSet("status")
	profile := fs.String("profile", defaultProfile, "profile name")
	noRemote := fs.Bool("no-remote", false, "skip fetching remote bridge state from server")
	output := fs.String("output", "text", "output format (text|json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	deviceID, err := ensureProfileDeviceID(*profile)
	if err != nil {
		return err
	}

	// Fetch remote bridges from server
	var remoteBridges map[string]beeperapi.WhoamiBridge
	if !*noRemote {
		if cfg, err := getAuthOrEnv(*profile); err == nil {
			if resp, err := beeperapi.Whoami(cfg.Domain, cfg.Token); err == nil {
				remoteBridges = resp.User.Bridges
			} else {
				fmt.Fprintf(os.Stderr, "warning: failed to fetch remote state: %v\n", err)
			}
		}
	}

	// Build set of local instances
	filterInstances := fs.Args()
	localInstances, _ := listInstancesForProfile(*profile)
	localSet := make(map[string]bool, len(localInstances))
	for _, inst := range localInstances {
		localSet[inst] = true
	}

	// Determine which bridges to show
	seen := make(map[string]bool)
	var toShow []string

	if len(filterInstances) > 0 {
		toShow = filterInstances
	} else {
		toShow = append(toShow, localInstances...)
		for _, inst := range localInstances {
			seen[inst] = true
			if remoteName, ok := remoteBridgeNameForLocalInstance(deviceID, inst); ok {
				seen[remoteName] = true
			}
		}
		for name := range remoteBridges {
			if !seen[name] {
				toShow = append(toShow, name)
				seen[name] = true
			}
		}
	}

	if len(toShow) == 0 {
		if *output == "json" {
			fmt.Println("[]")
		} else {
			fmt.Println("no instances found")
		}
		return nil
	}

	var statuses []bridgeStatus
	for _, inst := range toShow {
		remoteName := inst
		localName := inst
		if strings.HasPrefix(inst, "sh-") {
			if resolvedLocal, ok := localInstanceNameForRemoteBridge(deviceID, inst); ok {
				localName = resolvedLocal
			} else {
				localName = ""
			}
		} else if resolvedRemote, ok := remoteBridgeNameForLocalInstance(deviceID, inst); ok {
			remoteName = resolvedRemote
		}

		rb, hasRemote := remoteBridges[remoteName]
		hasLocal := localName != "" && localSet[localName]

		bs := bridgeStatus{Name: remoteName}
		if hasRemote {
			bs.State = string(rb.BridgeState.StateEvent)
			bs.SelfHosted = rb.BridgeState.IsSelfHosted
		}

		if hasLocal {
			sp, err := getInstancePaths(*profile, localName)
			if err == nil {
				running, pid := bridgeutil.ProcessAliveFromPIDFile(sp.PIDPath)
				ls := &localStatus{Running: running, ConfigPath: sp.ConfigPath}
				if running {
					ls.PID = pid
				}
				bs.Local = ls
			}
		}

		if hasRemote {
			for remoteID, rs := range rb.RemoteState {
				ls := loginStatus{
					RemoteID: remoteID,
					State:    string(rs.StateEvent),
				}
				if rs.RemoteName != "" {
					ls.RemoteName = rs.RemoteName
				}
				bs.Logins = append(bs.Logins, ls)
			}
		}

		statuses = append(statuses, bs)
	}

	if *output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(statuses)
	}

	fmt.Printf("Bridges (profile: %s):\n", *profile)
	for _, bs := range statuses {
		if bs.State != "" {
			selfHosted := ""
			if bs.SelfHosted {
				selfHosted = dim(" (self-hosted)")
			}
			fmt.Printf("  %s: %s%s\n", bs.Name, colorState(bs.State), selfHosted)
		} else if bs.Local != nil {
			fmt.Printf("  %s:\n", bs.Name)
		} else {
			fmt.Printf("  %s: %s\n", bs.Name, dim("unknown"))
		}

		if bs.Local != nil {
			fmt.Printf("    local: %s\n", colorLocal(bs.Local.Running, bs.Local.PID))
			fmt.Printf("    config: %s\n", dim(bs.Local.ConfigPath))
		}

		if len(bs.Logins) > 0 {
			fmt.Printf("    logins:\n")
			for _, l := range bs.Logins {
				name := ""
				if l.RemoteName != "" {
					name = dim(fmt.Sprintf(" (%s)", l.RemoteName))
				}
				fmt.Printf("      - %s: %s%s\n", l.RemoteID, colorState(l.State), name)
			}
		}
	}
	return nil
}

func cmdInstances(args []string) error {
	fs := newFlagSet("instances")
	profile := fs.String("profile", defaultProfile, "profile name")
	output := fs.String("output", "text", "output format (text|json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instances, err := listInstancesForProfile(*profile)
	if err != nil {
		return err
	}
	if *output == "json" {
		type instanceInfo struct {
			Name       string `json:"name"`
			Running    bool   `json:"running"`
			PID        int    `json:"pid,omitempty"`
			ConfigPath string `json:"config_path"`
		}
		result := make([]instanceInfo, 0, len(instances))
		for _, inst := range instances {
			sp, err := getInstancePaths(*profile, inst)
			if err != nil {
				return err
			}
			running, pid := bridgeutil.ProcessAliveFromPIDFile(sp.PIDPath)
			info := instanceInfo{Name: inst, Running: running, ConfigPath: sp.ConfigPath}
			if running {
				info.PID = pid
			}
			result = append(result, info)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	if len(instances) == 0 {
		fmt.Println("no instances found")
		return nil
	}
	fmt.Printf("Instances (profile: %s):\n", *profile)
	for _, inst := range instances {
		sp, err := getInstancePaths(*profile, inst)
		if err != nil {
			return err
		}
		running, pid := bridgeutil.ProcessAliveFromPIDFile(sp.PIDPath)
		state := colorLocal(running, pid)
		fmt.Printf("  %s: %s\n", inst, state)
		fmt.Printf("    config: %s\n", dim(sp.ConfigPath))
	}
	return nil
}

func printRunningInstances(profile string) error {
	instances, err := listInstancesForProfile(profile)
	if err != nil {
		return err
	}

	found := false
	fmt.Printf("Running bridges (profile: %s):\n", profile)
	for _, inst := range instances {
		sp, err := getInstancePaths(profile, inst)
		if err != nil {
			return err
		}
		running, pid := bridgeutil.ProcessAliveFromPIDFile(sp.PIDPath)
		if !running {
			continue
		}
		found = true
		fmt.Printf("  %s: %s\n", inst, colorLocal(true, pid))
		fmt.Printf("    config: %s\n", dim(sp.ConfigPath))
	}
	if !found {
		fmt.Println("  none")
	}
	return nil
}
