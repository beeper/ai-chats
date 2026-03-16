package cliutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Metadata struct {
	Instance         string    `json:"instance"`
	BridgeType       string    `json:"bridge_type"`
	RepoPath         string    `json:"repo_path,omitempty"`
	BinaryPath       string    `json:"binary_path,omitempty"`
	ConfigPath       string    `json:"config_path"`
	RegistrationPath string    `json:"registration_path"`
	LogPath          string    `json:"log_path"`
	PIDPath          string    `json:"pid_path"`
	BeeperBridgeName string    `json:"beeper_bridge_name"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type StatePaths struct {
	Root             string
	ConfigPath       string
	RegistrationPath string
	LogPath          string
	PIDPath          string
	MetaPath         string
}

func BuildStatePaths(root, instanceName string) *StatePaths {
	dir := filepath.Join(root, instanceName)
	return &StatePaths{
		Root:             dir,
		ConfigPath:       filepath.Join(dir, "config.yaml"),
		RegistrationPath: filepath.Join(dir, "registration.yaml"),
		LogPath:          filepath.Join(dir, "bridge.log"),
		PIDPath:          filepath.Join(dir, "bridge.pid"),
		MetaPath:         filepath.Join(dir, "meta.json"),
	}
}

func EnsureStateLayout(paths *StatePaths) error {
	return os.MkdirAll(paths.Root, 0o700)
}

func ReadMetadata(path string) (*Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta Metadata
	if err = json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func WriteMetadata(meta *Metadata, path string) error {
	meta.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func PrintRuntimePaths(meta *Metadata) {
	fmt.Printf("paths:\n")
	fmt.Printf("  config: %s\n", meta.ConfigPath)
	fmt.Printf("  registration: %s\n", meta.RegistrationPath)
	fmt.Printf("  log: %s\n", meta.LogPath)
	fmt.Printf("  pid: %s\n", meta.PIDPath)
}

func ListDirectories(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}
