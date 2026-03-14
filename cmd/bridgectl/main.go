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
	"gopkg.in/yaml.v3"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
	"github.com/beeper/agentremote/cmd/internal/cliutil"
	"github.com/beeper/agentremote/cmd/internal/selfhost"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

const (
	manifestPathDefault = "bridges.manifest.yml"
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

type authConfig = beeperauth.Config

type metadata = cliutil.Metadata

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		printUsage()
		return nil
	}
	switch os.Args[1] {
	case "login":
		return cmdLogin(os.Args[2:])
	case "logout":
		return cmdLogout(os.Args[2:])
	case "whoami":
		return cmdWhoami(os.Args[2:])
	case "up":
		return cmdUp(os.Args[2:])
	case "down":
		return cmdDown(os.Args[2:])
	case "restart":
		return cmdRestart(os.Args[2:])
	case "status":
		return cmdStatus(os.Args[2:])
	case "logs":
		return cmdLogs(os.Args[2:])
	case "init":
		return cmdInit(os.Args[2:])
	case "register":
		return cmdRegister(os.Args[2:])
	case "delete":
		return cmdDelete(os.Args[2:])
	case "list":
		return cmdList(os.Args[2:])
	case "doctor":
		return cmdDoctor(os.Args[2:])
	case "run":
		return cmdRun(os.Args[2:])
	case "auth":
		return cmdAuth(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

func printUsage() {
	fmt.Println("bridgectl - bridgev2 orchestrator")
	fmt.Println("commands: login logout whoami register delete up down run restart status logs init list doctor auth help")
}

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	env := fs.String("env", "prod", "beeper env")
	email := fs.String("email", "", "email address")
	code := fs.String("code", "", "login code")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := beeperauth.Login(context.Background(), beeperauth.LoginParams{
		Env:               *env,
		Email:             *email,
		Code:              *code,
		DeviceDisplayName: "ai-bridge-manager",
		Prompt:            bridgeutil.PromptLine,
	})
	if err != nil {
		return err
	}
	if err = saveAuthConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("logged in as @%s:%s\n", cfg.Username, cfg.Domain)
	return nil
}

func cmdLogout(_ []string) error {
	path, err := authConfigPath()
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Println("logged out")
	return nil
}

func cmdWhoami(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	raw := fs.Bool("raw", false, "print raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := getAuthOrEnv()
	if err != nil {
		return err
	}
	resp, err := beeperapi.Whoami(cfg.Domain, cfg.Token)
	if err != nil {
		return err
	}
	if *raw {
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("User ID: @%s:%s\n", resp.UserInfo.Username, cfg.Domain)
	fmt.Printf("Email: %s\n", resp.UserInfo.Email)
	fmt.Printf("Cluster: %s\n", resp.UserInfo.BridgeClusterID)
	fmt.Printf("Bridges: %d\n", len(resp.User.Bridges))
	if cfg.Username == "" || cfg.Username != resp.UserInfo.Username {
		cfg.Username = resp.UserInfo.Username
		if err := saveAuthConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to save auth config: %v\n", err)
		}
	}
	return nil
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
	running, pid := bridgeutil.ProcessAliveFromPIDFile(meta.PIDPath)
	if running {
		fmt.Printf("%s already running (pid %d)\n", instance, pid)
		return nil
	}
	if err = startBridgeProcess(meta); err != nil {
		return err
	}
	fmt.Printf("started %s\n", instance)
	cliutil.PrintRuntimePaths(meta)
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
	cliutil.PrintRuntimePaths(meta)
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
	stopped, err := bridgeutil.StopByPIDFile(meta.PIDPath)
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
		running, pid := bridgeutil.ProcessAliveFromPIDFile(meta.PIDPath)
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
	if _, err := bridgeutil.StopByPIDFile(meta.PIDPath); err != nil {
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
		repo, err := cliutil.ExpandPath(cfg.RepoPath)
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

func cmdAuth(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("auth requires subcommand: set-token|whoami|show")
	}
	switch args[0] {
	case "set-token":
		fs := flag.NewFlagSet("auth set-token", flag.ContinueOnError)
		token := fs.String("token", "", "beeper access token (syt_...)")
		env := fs.String("env", "prod", "beeper env")
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
		if err := saveAuthConfig(cfg); err != nil {
			return err
		}
		fmt.Println("auth config saved")
		return nil
	case "show":
		cfg, err := loadAuthConfig()
		if err != nil {
			return err
		}
		masked := cfg.Token
		if len(masked) > 8 {
			masked = masked[:4] + "..." + masked[len(masked)-4:]
		}
		fmt.Printf("env=%s domain=%s username=%s token=%s\n", cfg.Env, cfg.Domain, cfg.Username, masked)
		return nil
	case "whoami":
		cfg, err := getAuthOrEnv()
		if err != nil {
			return err
		}
		resp, err := beeperapi.Whoami(cfg.Domain, cfg.Token)
		if err != nil {
			return err
		}
		fmt.Printf("@%s:%s (%s)\n", resp.UserInfo.Username, cfg.Domain, resp.UserInfo.Email)
		if cfg.Username == "" || cfg.Username != resp.UserInfo.Username {
			cfg.Username = resp.UserInfo.Username
			if err := saveAuthConfig(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save auth config: %v\n", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown auth subcommand %q", args[0])
	}
}

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

type statePaths = cliutil.StatePaths

func instancePaths(instance string) (*statePaths, error) {
	stateRoot, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(stateRoot, ".local", "share", "ai-bridge-manager", "instances")
	return cliutil.BuildStatePaths(root, instance), nil
}

func ensureInstanceLayout(instance string) (*statePaths, error) {
	sp, err := instancePaths(instance)
	if err != nil {
		return nil, err
	}
	if err = cliutil.EnsureStateLayout(sp); err != nil {
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
	if err = bridgeutil.ApplyConfigOverrides(meta.ConfigPath, cfg.ConfigOverrides); err != nil {
		return nil, err
	}
	if err = cliutil.WriteMetadata(meta, sp.MetaPath); err != nil {
		return nil, err
	}
	return meta, nil
}

func readOrSynthesizeMetadata(instance string, cfg instanceConfig, sp *statePaths) (*metadata, error) {
	repo, err := cliutil.ExpandPath(cfg.RepoPath)
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
	m := metadata{UpdatedAt: time.Now().UTC()}
	if meta, err := cliutil.ReadMetadata(sp.MetaPath); err == nil {
		m = *meta
	}
	// Always refresh from current manifest so moving the checkout doesn't
	// strand an instance on stale absolute paths from an older clone.
	m.Instance = instance
	m.BridgeType = cfg.BridgeType
	m.RepoPath = repo
	m.BinaryPath = binPath
	m.ConfigPath = sp.ConfigPath
	m.RegistrationPath = sp.RegistrationPath
	m.LogPath = sp.LogPath
	m.PIDPath = sp.PIDPath
	m.BeeperBridgeName = cfg.BeeperBridgeName
	return &m, nil
}

func ensureBuilt(cfg instanceConfig) error {
	repo, err := cliutil.ExpandPath(cfg.RepoPath)
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
	return selfhost.EnsureRegistration(context.Background(), selfhost.RegistrationParams{
		Auth:             auth,
		SaveAuth:         saveAuthConfig,
		ConfigPath:       meta.ConfigPath,
		RegistrationPath: meta.RegistrationPath,
		BeeperBridgeName: meta.BeeperBridgeName,
		BridgeType:       cfg.BridgeType,
	})
}

func deleteRemoteBridge(name string) error {
	auth, err := getAuthOrEnv()
	if err != nil {
		return err
	}
	return selfhost.DeleteRemoteBridge(context.Background(), auth, saveAuthConfig, name)
}

func startBridgeProcess(meta *metadata) error {
	if _, err := os.Stat(meta.BinaryPath); err != nil {
		return fmt.Errorf("binary not found: %w", err)
	}
	return bridgeutil.StartBridgeFromConfig(meta.BinaryPath, []string{"-c", meta.ConfigPath}, meta.ConfigPath, meta.LogPath, meta.PIDPath)
}

func requiredInstanceArg(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("expected exactly one instance argument")
	}
	return args[0], nil
}

func getAuthOrEnv() (authConfig, error) {
	path, err := authConfigPath()
	if err != nil {
		return authConfig{}, err
	}
	return cliutil.ResolveAuth(path, missingAuthError)
}

func authConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ai-bridge-manager", "config.json"), nil
}

func loadAuthConfig() (authConfig, error) {
	path, err := authConfigPath()
	if err != nil {
		return authConfig{}, err
	}
	return cliutil.LoadAuth(path, missingAuthError)
}

func saveAuthConfig(cfg authConfig) error {
	path, err := authConfigPath()
	if err != nil {
		return err
	}
	return cliutil.SaveAuth(path, cfg)
}

func authStore() beeperauth.Store {
	path, err := authConfigPath()
	if err != nil {
		return beeperauth.Store{
			MissingError: func() error { return err },
		}
	}
	return cliutil.Store(path, missingAuthError)
}

func missingAuthError() error {
	path, err := authConfigPath()
	if err != nil {
		return err
	}
	return fmt.Errorf("failed to read auth config (%s). run auth set-token or set BEEPER_ACCESS_TOKEN", path)
}
