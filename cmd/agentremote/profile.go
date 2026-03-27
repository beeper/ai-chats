package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
	"github.com/beeper/agentremote/cmd/internal/cliutil"
)

const defaultProfile = "default"

type authConfig = beeperauth.Config

type profileState struct {
	Auth     *authConfig `json:"auth,omitempty"`
	DeviceID string      `json:"device_id,omitempty"`
}

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

func loadProfileState(profile string) (*profileState, error) {
	path, err := authConfigPath(profile)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state profileState
	if err = json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	state.DeviceID = strings.TrimSpace(strings.ToLower(state.DeviceID))
	return &state, nil
}

func saveProfileState(profile string, state *profileState) error {
	if state == nil {
		state = &profileState{}
	}
	path, err := authConfigPath(profile)
	if err != nil {
		return err
	}
	if state.Auth != nil && state.Auth.Domain == "" && state.Auth.Env != "" {
		domain, err := beeperauth.DomainForEnv(state.Auth.Env)
		if err != nil {
			return err
		}
		state.Auth.Domain = domain
	}
	state.DeviceID = strings.TrimSpace(strings.ToLower(state.DeviceID))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func generateDeviceID() (string, error) {
	var buf [5]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func ensureProfileDeviceID(profile string) (string, error) {
	state, err := loadProfileState(profile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if state == nil {
		state = &profileState{}
	}
	if state.DeviceID != "" {
		return state.DeviceID, nil
	}
	deviceID, err := generateDeviceID()
	if err != nil {
		return "", err
	}
	state.DeviceID = deviceID
	if err := saveProfileState(profile, state); err != nil {
		return "", err
	}
	return state.DeviceID, nil
}

func loadAuthConfig(profile string) (authConfig, error) {
	state, err := loadProfileState(profile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return authConfig{}, missingAuthError(profile)()
		}
		return authConfig{}, err
	}
	if state.Auth == nil {
		return authConfig{}, missingAuthError(profile)()
	}
	cfg := *state.Auth
	if cfg.Domain == "" && cfg.Env != "" {
		domain, err := beeperauth.DomainForEnv(cfg.Env)
		if err != nil {
			return authConfig{}, err
		}
		cfg.Domain = domain
	}
	if cfg.Token == "" || cfg.Domain == "" {
		return authConfig{}, missingAuthError(profile)()
	}
	return cfg, nil
}

func saveAuthConfig(profile string, cfg authConfig) error {
	state, err := loadProfileState(profile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if state == nil {
		state = &profileState{}
	}
	if strings.TrimSpace(state.DeviceID) == "" {
		deviceID, err := generateDeviceID()
		if err != nil {
			return err
		}
		state.DeviceID = deviceID
	}
	state.Auth = &cfg
	return saveProfileState(profile, state)
}

func getAuthOrEnv(profile string) (authConfig, error) {
	if tok := os.Getenv("BEEPER_ACCESS_TOKEN"); tok != "" {
		env := os.Getenv("BEEPER_ENV")
		if env == "" {
			env = "prod"
		}
		domain, err := beeperauth.DomainForEnv(env)
		if err != nil {
			return authConfig{}, fmt.Errorf("invalid BEEPER_ENV %q", env)
		}
		return authConfig{
			Env:      env,
			Domain:   domain,
			Username: os.Getenv("BEEPER_USERNAME"),
			Token:    tok,
		}, nil
	}
	return loadAuthConfig(profile)
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
