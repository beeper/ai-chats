package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
	"github.com/beeper/agentremote/cmd/internal/cliutil"
	"github.com/beeper/agentremote/cmd/internal/selfhost"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

func ensureInitialized(instName, bridgeType, beeperName string, sp *instancePaths) (*metadata, error) {
	meta, err := readOrSynthesizeMetadata(instName, bridgeType, beeperName, sp)
	if err != nil {
		return nil, err
	}
	if _, err = os.Stat(meta.ConfigPath); errors.Is(err, os.ErrNotExist) {
		if err = generateExampleConfig(meta); err != nil {
			return nil, err
		}
	}
	def, ok := lookupBridge(bridgeType)
	if !ok {
		return nil, fmt.Errorf("unknown bridge type %q (available: %s)", bridgeType, availableBridgeNames())
	}
	overrides := map[string]any{
		"appservice.address":  "websocket",
		"appservice.hostname": "127.0.0.1",
		"appservice.port":     def.Port,
		"database.type":       "sqlite3-fk-wal",
		"database.uri":        fmt.Sprintf("file:%s?_txlock=immediate", def.DBName),
		"bridge.permissions": map[string]any{
			"*":          "relay",
			"beeper.com": "admin",
		},
	}
	if err = bridgeutil.ApplyConfigOverrides(meta.ConfigPath, overrides); err != nil {
		return nil, err
	}
	if err = cliutil.WriteMetadata(meta, sp.MetaPath); err != nil {
		return nil, err
	}
	return meta, nil
}

func readOrSynthesizeMetadata(instName, bridgeType, beeperName string, sp *instancePaths) (*metadata, error) {
	var m metadata
	if existing, err := cliutil.ReadMetadata(sp.MetaPath); err == nil {
		m = *existing
	}
	// Always override paths and identity from current arguments so stale
	// metadata files don't strand an instance on old paths.
	m.Instance = instName
	m.BridgeType = bridgeType
	m.BeeperBridgeName = beeperName
	m.ConfigPath = sp.ConfigPath
	m.RegistrationPath = sp.RegistrationPath
	m.LogPath = sp.LogPath
	m.PIDPath = sp.PIDPath
	return &m, nil
}

func generateExampleConfig(meta *metadata) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find own executable: %w", err)
	}
	cmd := exec.Command(exe, "__bridge", meta.BridgeType, "-c", meta.ConfigPath, "-e")
	cmd.Dir = filepath.Dir(meta.ConfigPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func saveAuthFunc(profile string, preserve *authConfig) func(beeperauth.Config) error {
	return func(cfg beeperauth.Config) error {
		if preserve != nil {
			cfg.Env = preserve.Env
			cfg.Domain = preserve.Domain
		}
		return saveAuthConfig(profile, cfg)
	}
}

func ensureRegistration(profile, envOverride string, meta *metadata, bridgeType string) error {
	def, ok := lookupBridge(bridgeType)
	if !ok {
		return fmt.Errorf("unknown bridge type %q (available: %s)", bridgeType, availableBridgeNames())
	}
	auth, err := getAuthWithOverride(profile, envOverride)
	if err != nil {
		return err
	}
	var preserve *authConfig
	if strings.TrimSpace(envOverride) != "" {
		if cfg, loadErr := loadAuthConfig(profile); loadErr == nil {
			preserve = &cfg
		}
	}
	return selfhost.EnsureRegistration(context.Background(), selfhost.RegistrationParams{
		Auth:             auth,
		SaveAuth:         saveAuthFunc(profile, preserve),
		ConfigPath:       meta.ConfigPath,
		RegistrationPath: meta.RegistrationPath,
		BeeperBridgeName: meta.BeeperBridgeName,
		BridgeType:       bridgeType,
		DBName:           def.DBName,
	})
}

func deleteRemoteBridge(profile, beeperName string) error {
	auth, err := getAuthOrEnv(profile)
	if err != nil {
		return err
	}
	return selfhost.DeleteRemoteBridge(
		context.Background(),
		auth,
		saveAuthFunc(profile, nil),
		beeperName,
	)
}

// ── Process lifecycle ──

func startBridgeProcess(meta *metadata, bridgeType string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find own executable: %w", err)
	}
	return bridgeutil.StartBridgeFromConfig(exe, []string{"__bridge", bridgeType, "-c", meta.ConfigPath}, meta.ConfigPath, meta.LogPath, meta.PIDPath)
}
