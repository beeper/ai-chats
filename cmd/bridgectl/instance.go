package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/beeper/bridge-manager/api/beeperapi"
	"github.com/beeper/bridge-manager/api/hungryapi"
	"gopkg.in/yaml.v3"
)

type manifest struct {
	Instances map[string]instanceConfig `yaml:"instances"`
}

type instanceConfig struct {
	BridgeType       string         `yaml:"bridge_type"`
	Mode             string         `yaml:"mode"`
	RepoPath         string         `yaml:"repo_path"`
	BuildCmd         string         `yaml:"build_cmd"`
	BinaryPath       string         `yaml:"binary_path"`
	BeeperBridgeName string         `yaml:"beeper_bridge_name"`
	ConfigOverrides  map[string]any `yaml:"config_overrides"`
}

type metadata struct {
	Instance         string    `json:"instance"`
	BridgeType       string    `json:"bridge_type"`
	RepoPath         string    `json:"repo_path"`
	BinaryPath       string    `json:"binary_path"`
	ConfigPath       string    `json:"config_path"`
	RegistrationPath string    `json:"registration_path"`
	LogPath          string    `json:"log_path"`
	PIDPath          string    `json:"pid_path"`
	BeeperBridgeName string    `json:"beeper_bridge_name"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type statePaths struct {
	Root             string
	ConfigPath       string
	RegistrationPath string
	LogPath          string
	PIDPath          string
	MetaPath         string
}

func cmdUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instance, err := requiredInstanceArg(fs.Args())
	if err != nil {
		return err
	}
	_, cfg, err := loadInstance(*manifestPath, instance)
	if err != nil {
		return err
	}
	state, err := ensureInstanceLayout(instance)
	if err != nil {
		return err
	}
	if err = ensureBuilt(cfg); err != nil {
		return err
	}
	meta, err := ensureInitialized(instance, cfg, state)
	if err != nil {
		return err
	}
	if err = ensureRegistration(meta, cfg); err != nil {
		return err
	}
	running, pid := processAliveFromPIDFile(meta.PIDPath)
	if running {
		fmt.Printf("%s already running (pid %d)\n", instance, pid)
		return nil
	}
	if err = startBridge(meta); err != nil {
		return err
	}
	fmt.Printf("started %s\n", instance)
	printRuntimePaths(meta)
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instance, err := requiredInstanceArg(fs.Args())
	if err != nil {
		return err
	}
	_, cfg, err := loadInstance(*manifestPath, instance)
	if err != nil {
		return err
	}
	state, err := ensureInstanceLayout(instance)
	if err != nil {
		return err
	}
	if err = ensureBuilt(cfg); err != nil {
		return err
	}
	meta, err := ensureInitialized(instance, cfg, state)
	if err != nil {
		return err
	}
	if err = ensureRegistration(meta, cfg); err != nil {
		return err
	}
	if _, err = os.Stat(meta.BinaryPath); err != nil {
		return fmt.Errorf("binary not found: %w", err)
	}
	argv := []string{meta.BinaryPath, "-c", meta.ConfigPath}
	fmt.Printf("running %s in foreground\n", instance)
	printRuntimePaths(meta)
	if err = os.Chdir(filepath.Dir(meta.ConfigPath)); err != nil {
		return fmt.Errorf("failed to chdir: %w", err)
	}
	return syscall.Exec(meta.BinaryPath, argv, os.Environ())
}

func cmdDown(args []string) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instance, err := requiredInstanceArg(fs.Args())
	if err != nil {
		return err
	}
	_, cfg, err := loadInstance(*manifestPath, instance)
	if err != nil {
		return err
	}
	state, err := instancePaths(instance)
	if err != nil {
		return err
	}
	meta, err := readOrSynthesizeMetadata(instance, cfg, state)
	if err != nil {
		return err
	}
	stopped, err := stopBridge(meta)
	if err != nil {
		return err
	}
	if stopped {
		fmt.Printf("stopped %s\n", instance)
	} else {
		fmt.Printf("%s is not running\n", instance)
	}
	return nil
}

func cmdRestart(args []string) error {
	if err := cmdDown(args); err != nil {
		return err
	}
	return cmdUp(args)
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mf, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	instances := fs.Args()
	if len(instances) == 0 {
		for k := range mf.Instances {
			instances = append(instances, k)
		}
	}
	for _, instance := range instances {
		cfg, ok := mf.Instances[instance]
		if !ok {
			fmt.Printf("%s: not in manifest\n", instance)
			continue
		}
		state, err := instancePaths(instance)
		if err != nil {
			return err
		}
		meta, err := readOrSynthesizeMetadata(instance, cfg, state)
		if err != nil {
			fmt.Printf("%s: metadata error: %v\n", instance, err)
			continue
		}
		running, pid := processAliveFromPIDFile(meta.PIDPath)
		status := "stopped"
		if running {
			status = "running"
		}
		fmt.Printf("%s: %s", instance, status)
		if running {
			fmt.Printf(" (pid %d)", pid)
		}
		fmt.Printf("\n  config: %s\n  log: %s\n", meta.ConfigPath, meta.LogPath)
	}
	return nil
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow logs")
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instance, err := requiredInstanceArg(fs.Args())
	if err != nil {
		return err
	}
	_, cfg, err := loadInstance(*manifestPath, instance)
	if err != nil {
		return err
	}
	state, err := instancePaths(instance)
	if err != nil {
		return err
	}
	meta, err := readOrSynthesizeMetadata(instance, cfg, state)
	if err != nil {
		return err
	}
	if *follow {
		cmd := exec.Command("tail", "-f", meta.LogPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	f, err := os.Open(meta.LogPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instance, err := requiredInstanceArg(fs.Args())
	if err != nil {
		return err
	}
	_, cfg, err := loadInstance(*manifestPath, instance)
	if err != nil {
		return err
	}
	state, err := ensureInstanceLayout(instance)
	if err != nil {
		return err
	}
	if err = ensureBuilt(cfg); err != nil {
		return err
	}
	meta, err := ensureInitialized(instance, cfg, state)
	if err != nil {
		return err
	}
	fmt.Printf("initialized %s\nconfig: %s\nregistration: %s\n", instance, meta.ConfigPath, meta.RegistrationPath)
	return nil
}

func cmdRegister(args []string) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	output := fs.String("output", "-", "output path for registration YAML")
	jsonOut := fs.Bool("json", false, "print registration metadata as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instance, err := requiredInstanceArg(fs.Args())
	if err != nil {
		return err
	}
	_, cfg, err := loadInstance(*manifestPath, instance)
	if err != nil {
		return err
	}
	state, err := ensureInstanceLayout(instance)
	if err != nil {
		return err
	}
	if err = ensureBuilt(cfg); err != nil {
		return err
	}
	meta, err := ensureInitialized(instance, cfg, state)
	if err != nil {
		return err
	}
	if err = ensureRegistration(meta, cfg); err != nil {
		return err
	}
	if *jsonOut {
		payload := map[string]any{
			"bridge_name":   meta.BeeperBridgeName,
			"bridge_type":   cfg.BridgeType,
			"registration":  meta.RegistrationPath,
			"homeserver":    "beeper.local",
			"instance":      instance,
			"config":        meta.ConfigPath,
			"manifest_path": *manifestPath,
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	if *output != "-" {
		data, err := os.ReadFile(meta.RegistrationPath)
		if err != nil {
			return err
		}
		if err = os.WriteFile(*output, data, 0o600); err != nil {
			return err
		}
		fmt.Printf("registration written to %s\n", *output)
		return nil
	}
	fmt.Printf("registration ensured for %s\n", instance)
	return nil
}

func cmdDelete(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	remote := fs.Bool("remote", false, "also delete remote beeper bridge")
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instance, err := requiredInstanceArg(fs.Args())
	if err != nil {
		return err
	}
	_, cfg, err := loadInstance(*manifestPath, instance)
	if err != nil {
		return err
	}
	state, err := instancePaths(instance)
	if err != nil {
		return err
	}
	meta, err := readOrSynthesizeMetadata(instance, cfg, state)
	if err != nil {
		return err
	}
	if _, err := stopBridge(meta); err != nil {
		return fmt.Errorf("failed to stop %s: %w", instance, err)
	}
	if *remote {
		if err := deleteRemoteBridge(meta.BeeperBridgeName); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(state.Root); err != nil {
		return err
	}
	fmt.Printf("deleted %s\n", instance)
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mf, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	for k, v := range mf.Instances {
		fmt.Printf("%s\t%s\t%s\n", k, v.BridgeType, v.RepoPath)
	}
	return nil
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	manifestPath := fs.String("manifest", manifestPathDefault, "manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mf, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	fmt.Println("manifest:", *manifestPath)
	fmt.Printf("instances: %d\n", len(mf.Instances))
	for name, cfg := range mf.Instances {
		repo, err := expandPath(cfg.RepoPath)
		if err != nil {
			fmt.Printf("- %s: invalid repo_path: %v\n", name, err)
			continue
		}
		if _, err = os.Stat(repo); err != nil {
			fmt.Printf("- %s: repo missing: %s\n", name, repo)
		} else {
			fmt.Printf("- %s: ok (%s)\n", name, repo)
		}
	}
	return nil
}

// --- manifest and instance helpers ---

func loadManifest(path string) (*manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var mf manifest
	if err = yaml.Unmarshal(data, &mf); err != nil {
		return nil, err
	}
	if len(mf.Instances) == 0 {
		return nil, fmt.Errorf("manifest has no instances")
	}
	return &mf, nil
}

func loadInstance(manifestPath, instance string) (*manifest, instanceConfig, error) {
	mf, err := loadManifest(manifestPath)
	if err != nil {
		return nil, instanceConfig{}, err
	}
	cfg, ok := mf.Instances[instance]
	if !ok {
		return nil, instanceConfig{}, fmt.Errorf("instance %q not found in manifest", instance)
	}
	if cfg.BridgeType == "" {
		cfg.BridgeType = instance
	}
	if cfg.BuildCmd == "" {
		cfg.BuildCmd = "./build.sh"
	}
	if cfg.Mode == "" {
		cfg.Mode = "local-repo"
	}
	if cfg.BeeperBridgeName == "" {
		cfg.BeeperBridgeName = "sh-" + instance
	}
	return mf, cfg, nil
}

func instancePaths(instance string) (*statePaths, error) {
	stateRoot, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(stateRoot, ".local", "share", "ai-bridge-manager", "instances", instance)
	return &statePaths{
		Root:             root,
		ConfigPath:       filepath.Join(root, "config.yaml"),
		RegistrationPath: filepath.Join(root, "registration.yaml"),
		LogPath:          filepath.Join(root, "bridge.log"),
		PIDPath:          filepath.Join(root, "bridge.pid"),
		MetaPath:         filepath.Join(root, "meta.json"),
	}, nil
}

func ensureInstanceLayout(instance string) (*statePaths, error) {
	sp, err := instancePaths(instance)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(sp.Root, 0o700); err != nil {
		return nil, err
	}
	return sp, nil
}

func ensureInitialized(instance string, cfg instanceConfig, sp *statePaths) (*metadata, error) {
	meta, err := readOrSynthesizeMetadata(instance, cfg, sp)
	if err != nil {
		return nil, err
	}
	if _, err = os.Stat(meta.ConfigPath); errors.Is(err, os.ErrNotExist) {
		if err = generateExampleConfig(meta); err != nil {
			return nil, err
		}
	}
	if err = applyConfigOverrides(meta.ConfigPath, cfg.ConfigOverrides); err != nil {
		return nil, err
	}
	if err = writeMetadata(meta, sp.MetaPath); err != nil {
		return nil, err
	}
	return meta, nil
}

func readOrSynthesizeMetadata(instance string, cfg instanceConfig, sp *statePaths) (*metadata, error) {
	if data, err := os.ReadFile(sp.MetaPath); err == nil {
		var m metadata
		if err = json.Unmarshal(data, &m); err == nil {
			return &m, nil
		}
	}
	repo, err := expandPath(cfg.RepoPath)
	if err != nil {
		return nil, err
	}
	binPath := cfg.BinaryPath
	if binPath == "" {
		binPath = cfg.BridgeType
	}
	if !filepath.IsAbs(binPath) {
		binPath = filepath.Join(repo, binPath)
	}
	return &metadata{
		Instance:         instance,
		BridgeType:       cfg.BridgeType,
		RepoPath:         repo,
		BinaryPath:       binPath,
		ConfigPath:       sp.ConfigPath,
		RegistrationPath: sp.RegistrationPath,
		LogPath:          sp.LogPath,
		PIDPath:          sp.PIDPath,
		BeeperBridgeName: cfg.BeeperBridgeName,
		UpdatedAt:        time.Now().UTC(),
	}, nil
}

func writeMetadata(meta *metadata, path string) error {
	meta.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func ensureBuilt(cfg instanceConfig) error {
	repo, err := expandPath(cfg.RepoPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BuildCmd) == "" {
		return fmt.Errorf("empty build_cmd")
	}
	cmd := exec.Command("sh", "-lc", cfg.BuildCmd)
	cmd.Dir = repo
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	fmt.Printf("building %s with %q\n", cfg.BridgeType, cfg.BuildCmd)
	return cmd.Run()
}

func generateExampleConfig(meta *metadata) error {
	if _, err := os.Stat(meta.BinaryPath); err != nil {
		return fmt.Errorf("bridge binary not found at %s (run up to build first): %w", meta.BinaryPath, err)
	}
	cmd := exec.Command(meta.BinaryPath, "-c", meta.ConfigPath, "-e")
	cmd.Dir = filepath.Dir(meta.ConfigPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ensureRegistration(meta *metadata, cfg instanceConfig) error {
	auth, err := getAuthOrEnv()
	if err != nil {
		return err
	}
	who, err := beeperapi.Whoami(auth.Domain, auth.Token)
	if err != nil {
		return fmt.Errorf("whoami failed: %w", err)
	}
	syncAuthUsername(&auth, who.UserInfo.Username)

	hc := hungryapi.NewClient(auth.Domain, auth.Username, auth.Token)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reg, err := hc.GetAppService(ctx, meta.BeeperBridgeName)
	if err != nil {
		reg, err = hc.RegisterAppService(ctx, meta.BeeperBridgeName, hungryapi.ReqRegisterAppService{Push: false, SelfHosted: true})
		if err != nil {
			return fmt.Errorf("register appservice failed: %w", err)
		}
	}
	yml, err := reg.YAML()
	if err != nil {
		return err
	}
	if err = os.WriteFile(meta.RegistrationPath, []byte(yml), 0o600); err != nil {
		return err
	}
	userID := fmt.Sprintf("@%s:%s", auth.Username, auth.Domain)
	if err = patchConfigWithRegistration(meta.ConfigPath, &reg, hc.HomeserverURL.String(), meta.BeeperBridgeName, cfg.BridgeType, auth.Domain, reg.AppToken, userID, who.User.AsmuxData.LoginToken); err != nil {
		return err
	}

	state := beeperapi.ReqPostBridgeState{
		StateEvent:   "STARTING",
		Reason:       "SELF_HOST_REGISTERED",
		IsSelfHosted: true,
		BridgeType:   cfg.BridgeType,
	}
	_ = beeperapi.PostBridgeState(auth.Domain, auth.Username, meta.BeeperBridgeName, reg.AppToken, state)
	return nil
}

func deleteRemoteBridge(name string) error {
	auth, err := getAuthOrEnv()
	if err != nil {
		return err
	}
	if auth.Username == "" {
		who, werr := beeperapi.Whoami(auth.Domain, auth.Token)
		if werr == nil {
			auth.Username = who.UserInfo.Username
			_ = saveAuthConfig(auth)
		}
	}
	if auth.Username != "" {
		hc := hungryapi.NewClient(auth.Domain, auth.Username, auth.Token)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = hc.DeleteAppService(ctx, name)
		cancel()
	}
	if err = beeperapi.DeleteBridge(auth.Domain, name, auth.Token); err != nil {
		return fmt.Errorf("failed to delete bridge in beeper api: %w", err)
	}
	return nil
}

func printRuntimePaths(meta *metadata) {
	fmt.Printf("paths:\n")
	fmt.Printf("  config: %s\n", meta.ConfigPath)
	fmt.Printf("  registration: %s\n", meta.RegistrationPath)
	fmt.Printf("  log: %s\n", meta.LogPath)
	fmt.Printf("  pid: %s\n", meta.PIDPath)
	if dbURI, err := getDatabaseURI(meta.ConfigPath); err == nil && dbURI != "" {
		fmt.Printf("  database.uri: %s\n", dbURI)
	}
}

func requiredInstanceArg(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("expected exactly one instance argument")
	}
	return args[0], nil
}

func expandPath(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[2:])
	}
	return filepath.Abs(p)
}
