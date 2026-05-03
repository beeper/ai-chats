package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/beeper/bridge-manager/api/beeperapi"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

func cmdLogin(args []string) error {
	fs := newFlagSet("login")
	env := fs.String("env", "prod", "beeper env (prod|staging|dev|local)")
	profile := fs.String("profile", defaultProfile, "profile name")
	email := fs.String("email", "", "email address")
	code := fs.String("code", "", "login code")
	if err := fs.Parse(args); err != nil {
		return err
	}
	domain, err := beeperauth.DomainForEnv(*env)
	if err != nil {
		return err
	}
	fmt.Printf("Logging into %s (env: %s)\n", domain, *env)
	cfg, err := beeperauth.Login(context.Background(), beeperauth.LoginParams{
		Env:               *env,
		Email:             *email,
		Code:              *code,
		DeviceDisplayName: binaryName,
		Prompt:            bridgeutil.PromptLine,
	})
	if err != nil {
		return err
	}
	if err = saveAuthConfig(*profile, cfg); err != nil {
		return err
	}
	fmt.Printf("logged in as @%s:%s (profile: %s)\n", cfg.Username, cfg.Domain, *profile)
	return nil
}

func cmdLogout(args []string) error {
	fs := newFlagSet("logout")
	profile := fs.String("profile", defaultProfile, "profile name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := authConfigPath(*profile)
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Printf("logged out (profile: %s)\n", *profile)
	return nil
}

func cmdWhoami(args []string) error {
	fs := newFlagSet("whoami")
	profile := fs.String("profile", defaultProfile, "profile name")
	output := fs.String("output", "text", "output format (text|json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := getAuthOrEnv(*profile)
	if err != nil {
		return err
	}
	resp, err := beeperapi.Whoami(cfg.Domain, cfg.Token)
	if err != nil {
		return err
	}
	if cfg.Username != resp.UserInfo.Username {
		cfg.Username = resp.UserInfo.Username
		if err := saveAuthConfig(*profile, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to save auth config: %v\n", err)
		}
	}
	if *output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]string{
			"user_id": fmt.Sprintf("@%s:%s", resp.UserInfo.Username, cfg.Domain),
			"email":   resp.UserInfo.Email,
			"cluster": resp.UserInfo.BridgeClusterID,
			"profile": *profile,
		})
	}
	fmt.Printf("User ID: @%s:%s\n", resp.UserInfo.Username, cfg.Domain)
	fmt.Printf("Email: %s\n", resp.UserInfo.Email)
	fmt.Printf("Cluster: %s\n", resp.UserInfo.BridgeClusterID)
	fmt.Printf("Profile: %s\n", *profile)
	return nil
}

func cmdProfiles(args []string) error {
	fs := newFlagSet("profiles")
	output := fs.String("output", "text", "output format (text|json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	profiles, err := listProfiles()
	if err != nil {
		return err
	}
	if *output == "json" {
		type profileInfo struct {
			Name     string `json:"name"`
			Username string `json:"username,omitempty"`
			Domain   string `json:"domain,omitempty"`
			Env      string `json:"env,omitempty"`
		}
		var result []profileInfo
		for _, p := range profiles {
			pi := profileInfo{Name: p}
			if cfg, err := loadAuthConfig(p); err == nil {
				pi.Username = cfg.Username
				pi.Domain = cfg.Domain
				pi.Env = cfg.Env
			}
			result = append(result, pi)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	if len(profiles) == 0 {
		fmt.Println("no profiles found")
		return nil
	}
	for _, p := range profiles {
		cfg, err := loadAuthConfig(p)
		if err != nil {
			fmt.Printf("%s: not logged in\n", p)
		} else {
			fmt.Printf("%s: @%s:%s (%s)\n", p, cfg.Username, cfg.Domain, cfg.Env)
		}
	}
	return nil
}

// ── Bridge lifecycle commands ──
