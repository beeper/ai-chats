package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

func cmdVersion() error {
	fmt.Printf("%s %s\n", binaryName, Tag)
	fmt.Printf("commit: %s\n", Commit)
	fmt.Printf("built: %s\n", BuildTime)
	return nil
}

func cmdDoctor(args []string) error {
	fs := newFlagSet("doctor")
	profile := fs.String("profile", defaultProfile, "profile name")
	output := fs.String("output", "text", "output format (text|json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	authPath, err := authConfigPath(*profile)
	if err != nil {
		return err
	}
	deviceID, err := ensureProfileDeviceID(*profile)
	if err != nil {
		return err
	}
	authCfg, authErr := loadAuthConfig(*profile)
	instances, instErr := listInstancesForProfile(*profile)
	if instErr != nil {
		return instErr
	}
	type instanceState struct {
		Name       string `json:"name"`
		Running    bool   `json:"running"`
		PID        int    `json:"pid,omitempty"`
		ConfigPath string `json:"config_path"`
	}
	report := struct {
		Profile   string          `json:"profile"`
		DeviceID  string          `json:"device_id"`
		AuthPath  string          `json:"auth_path"`
		LoggedIn  bool            `json:"logged_in"`
		UserID    string          `json:"user_id,omitempty"`
		Env       string          `json:"env,omitempty"`
		Instances []instanceState `json:"instances"`
		AuthError string          `json:"auth_error,omitempty"`
	}{
		Profile:  *profile,
		DeviceID: deviceID,
		AuthPath: authPath,
		LoggedIn: authErr == nil,
	}
	if authErr == nil {
		report.UserID = fmt.Sprintf("@%s:%s", authCfg.Username, authCfg.Domain)
		report.Env = authCfg.Env
	} else {
		report.AuthError = authErr.Error()
	}
	for _, inst := range instances {
		sp, err := getInstancePaths(*profile, inst)
		if err != nil {
			return err
		}
		running, pid := bridgeutil.ProcessAliveFromPIDFile(sp.PIDPath)
		state := instanceState{Name: inst, Running: running, ConfigPath: sp.ConfigPath}
		if running {
			state.PID = pid
		}
		report.Instances = append(report.Instances, state)
	}
	if *output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Printf("Profile: %s\n", report.Profile)
	fmt.Printf("Device ID: %s\n", report.DeviceID)
	fmt.Printf("Auth path: %s\n", report.AuthPath)
	if report.LoggedIn {
		fmt.Printf("Logged in: yes (%s)\n", report.UserID)
		if report.Env != "" {
			fmt.Printf("Env: %s\n", report.Env)
		}
	} else {
		fmt.Printf("Logged in: no\n")
		if report.AuthError != "" {
			fmt.Printf("Auth error: %s\n", report.AuthError)
		}
	}
	if len(report.Instances) == 0 {
		fmt.Println("Instances: none")
		return nil
	}
	fmt.Println("Instances:")
	for _, inst := range report.Instances {
		fmt.Printf("  %s: %s\n", inst.Name, colorLocal(inst.Running, inst.PID))
		fmt.Printf("    config: %s\n", dim(inst.ConfigPath))
	}
	return nil
}

func cmdAuth(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("auth requires subcommand: set-token|show|whoami")
	}
	switch args[0] {
	case "set-token":
		fs := newFlagSet("auth set-token")
		profile := fs.String("profile", defaultProfile, "profile name")
		token := fs.String("token", "", "beeper access token (syt_...)")
		env := fs.String("env", "prod", "beeper env (prod|staging|dev|local)")
		username := fs.String("username", "", "matrix username")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *token == "" {
			return fmt.Errorf("--token is required")
		}
		domain, err := beeperauth.DomainForEnv(*env)
		if err != nil {
			return err
		}
		cfg := authConfig{Env: *env, Domain: domain, Username: *username, Token: *token}
		if err := saveAuthConfig(*profile, cfg); err != nil {
			return err
		}
		fmt.Printf("auth config saved (profile: %s)\n", *profile)
		return nil
	case "show":
		fs := newFlagSet("auth show")
		profile := fs.String("profile", defaultProfile, "profile name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cfg, err := loadAuthConfig(*profile)
		if err != nil {
			return err
		}
		masked := cfg.Token
		if len(masked) > 8 {
			masked = masked[:4] + "..." + masked[len(masked)-4:]
		}
		fmt.Printf("profile=%s env=%s domain=%s username=%s token=%s\n", *profile, cfg.Env, cfg.Domain, cfg.Username, masked)
		return nil
	case "whoami":
		return cmdWhoami(args[1:])
	default:
		return fmt.Errorf("unknown auth subcommand %q", args[0])
	}
}

func cmdCompletion(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: %s completion <bash|zsh|fish>", binaryName)
	}
	switch args[0] {
	case "bash":
		fmt.Print(generateBashCompletion())
	case "zsh":
		fmt.Print(generateZshCompletion())
	case "fish":
		fmt.Print(generateFishCompletion())
	default:
		return fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish)", args[0])
	}
	return nil
}

// ── Instance management helpers ──
