package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
	"github.com/beeper/agentremote/cmd/internal/cliutil"
)

const defaultProfile = "default"

type authConfig = beeperauth.Config

// configRoot returns ~/.config/agentremote
func configRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "agentremote"), nil
}

// profileRoot returns ~/.config/agentremote/profiles/<profile>
func profileRoot(profile string) (string, error) {
	root, err := configRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "profiles", profile), nil
}

// authConfigPath returns the path to the auth config for a profile.
func authConfigPath(profile string) (string, error) {
	root, err := profileRoot(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "config.json"), nil
}

// instanceRoot returns the instances directory for a profile.
func instanceRoot(profile string) (string, error) {
	root, err := profileRoot(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "instances"), nil
}

type instancePaths = cliutil.StatePaths

func getInstancePaths(profile, instanceName string) (*instancePaths, error) {
	root, err := instanceRoot(profile)
	if err != nil {
		return nil, err
	}
	return cliutil.BuildStatePaths(root, instanceName), nil
}

func ensureInstanceLayout(profile, instanceName string) (*instancePaths, error) {
	sp, err := getInstancePaths(profile, instanceName)
	if err != nil {
		return nil, err
	}
	if err = cliutil.EnsureStateLayout(sp); err != nil {
		return nil, err
	}
	return sp, nil
}

func authStore(profile string) (beeperauth.Store, error) {
	path, err := authConfigPath(profile)
	if err != nil {
		return beeperauth.Store{}, err
	}
	return beeperauth.Store{Path: path, MissingError: missingAuthError(profile)}, nil
}

func loadAuthConfig(profile string) (authConfig, error) {
	store, err := authStore(profile)
	if err != nil {
		return authConfig{}, err
	}
	return beeperauth.Load(store)
}

func saveAuthConfig(profile string, cfg authConfig) error {
	path, err := authConfigPath(profile)
	if err != nil {
		return err
	}
	return beeperauth.Save(path, cfg)
}

func getAuthOrEnv(profile string) (authConfig, error) {
	store, err := authStore(profile)
	if err != nil {
		return authConfig{}, err
	}
	return beeperauth.ResolveFromEnvOrStore(store)
}

func getAuthWithOverride(profile, envOverride string) (authConfig, error) {
	cfg, err := getAuthOrEnv(profile)
	if err != nil {
		return authConfig{}, err
	}
	envOverride = strings.TrimSpace(envOverride)
	if envOverride == "" {
		return cfg, nil
	}
	domain, err := beeperauth.DomainForEnv(envOverride)
	if err != nil {
		return authConfig{}, err
	}
	cfg.Env = envOverride
	cfg.Domain = domain
	return cfg, nil
}

func listProfiles() ([]string, error) {
	root, err := configRoot()
	if err != nil {
		return nil, err
	}
	return cliutil.ListDirectories(filepath.Join(root, "profiles"))
}

func listInstancesForProfile(profile string) ([]string, error) {
	root, err := instanceRoot(profile)
	if err != nil {
		return nil, err
	}
	return cliutil.ListDirectories(root)
}

func missingAuthError(profile string) func() error {
	return func() error {
		return fmt.Errorf("not logged in (profile %q). Run: agentremote login --profile %s", profile, profile)
	}
}
