package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/beeper/bridge-manager/api/beeperapi"
	"github.com/beeper/bridge-manager/api/hungryapi"
	"gopkg.in/yaml.v3"
	"maunium.net/go/mautrix"
)

const (
	manifestPathDefault = "bridges.manifest.yml"
)

var envDomains = map[string]string{
	"prod":    "beeper.com",
	"staging": "beeper-staging.com",
	"dev":     "beeper-dev.com",
	"local":   "beeper.localtest.me",
}

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

type authConfig struct {
	Env      string `json:"env"`
	Domain   string `json:"domain"`
	Username string `json:"username"`
	Token    string `json:"token"`
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
	domain, ok := envDomains[*env]
	if !ok {
		return fmt.Errorf("invalid env %q", *env)
	}
	if *email == "" {
		v, err := promptLine("Email: ")
		if err != nil {
			return err
		}
		*email = v
	}
	if strings.TrimSpace(*email) == "" {
		return fmt.Errorf("email is required")
	}
	start, err := beeperapi.StartLogin(domain)
	if err != nil {
		return err
	}
	if err = beeperapi.SendLoginEmail(domain, start.RequestID, *email); err != nil {
		return err
	}
	if *code == "" {
		v, err := promptLine("Code: ")
		if err != nil {
			return err
		}
		*code = v
	}
	if strings.TrimSpace(*code) == "" {
		return fmt.Errorf("code is required")
	}
	resp, err := beeperapi.SendLoginCode(domain, start.RequestID, strings.TrimSpace(*code))
	if err != nil {
		return err
	}
	matrixClient, err := mautrix.NewClient(fmt.Sprintf("https://matrix.%s", domain), "", "")
	if err != nil {
		return fmt.Errorf("failed to create matrix client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	loginResp, err := matrixClient.Login(ctx, &mautrix.ReqLogin{
		Type:                     "org.matrix.login.jwt",
		Token:                    resp.LoginToken,
		InitialDeviceDisplayName: "ai-bridge-manager",
	})
	if err != nil {
		return fmt.Errorf("matrix login failed: %w", err)
	}
	username := ""
	if resp.Whoami != nil {
		username = resp.Whoami.UserInfo.Username
	}
	if username == "" {
		username = loginResp.UserID.Localpart()
	}
	cfg := authConfig{
		Env:      *env,
		Domain:   domain,
		Username: username,
		Token:    loginResp.AccessToken,
	}
	if err = saveAuthConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("logged in as @%s:%s\n", username, domain)
	return nil
}

func cmdLogout(args []string) error {
	_ = args
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
		_ = saveAuthConfig(cfg)
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
	mf, cfg, err := loadInstance(*manifestPath, instance)
	if err != nil {
		return err
	}
	_ = mf
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
	fmt.Printf("log: %s\n", meta.LogPath)
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
		domain, ok := envDomains[*env]
		if !ok {
			return fmt.Errorf("invalid env %q", *env)
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
			_ = saveAuthConfig(cfg)
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

type statePaths struct {
	Root             string
	ConfigPath       string
	RegistrationPath string
	LogPath          string
	PIDPath          string
	MetaPath         string
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
	if auth.Username == "" || auth.Username != who.UserInfo.Username {
		auth.Username = who.UserInfo.Username
		_ = saveAuthConfig(auth)
	}
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
	if err = patchConfigWithRegistration(meta.ConfigPath, &reg, hc.HomeserverURL.String(), cfg.BridgeType); err != nil {
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

func patchConfigWithRegistration(configPath string, reg any, homeserverURL, bridgeType string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	regMap := toMap(reg)
	setPath(doc, []string{"homeserver", "address"}, homeserverURL)
	setPath(doc, []string{"homeserver", "domain"}, "beeper.local")
	setPath(doc, []string{"appservice", "as_token"}, regMap["as_token"])
	setPath(doc, []string{"appservice", "hs_token"}, regMap["hs_token"])
	if v, ok := regMap["id"]; ok {
		setPath(doc, []string{"appservice", "id"}, v)
	}
	if v, ok := regMap["sender_localpart"]; ok {
		if s, ok2 := v.(string); ok2 {
			setPath(doc, []string{"appservice", "bot", "username"}, s)
		}
	}
	if bridgeType != "" {
		setPath(doc, []string{"network", "bridge_type"}, bridgeType)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0o600)
}

func applyConfigOverrides(configPath string, overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	for k, v := range overrides {
		parts := strings.Split(k, ".")
		setPath(doc, parts, v)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0o600)
}

func setPath(root map[string]any, parts []string, value any) {
	if len(parts) == 0 {
		return
	}
	cur := root
	for i := 0; i < len(parts)-1; i++ {
		key := parts[i]
		next, ok := cur[key]
		if !ok {
			nm := map[string]any{}
			cur[key] = nm
			cur = nm
			continue
		}
		nm, ok := next.(map[string]any)
		if !ok {
			nm = map[string]any{}
			cur[key] = nm
		}
		cur = nm
	}
	cur[parts[len(parts)-1]] = value
}

func startBridge(meta *metadata) error {
	if _, err := os.Stat(meta.BinaryPath); err != nil {
		return fmt.Errorf("binary not found: %w", err)
	}
	logFile, err := os.OpenFile(meta.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(meta.BinaryPath, "-c", meta.ConfigPath)
	cmd.Dir = filepath.Dir(meta.ConfigPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	pid := cmd.Process.Pid
	if err = os.WriteFile(meta.PIDPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		_ = logFile.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return err
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	return nil
}

func stopBridge(meta *metadata) (bool, error) {
	running, pid := processAliveFromPIDFile(meta.PIDPath)
	if !running {
		_ = os.Remove(meta.PIDPath)
		return false, nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	if err = proc.Signal(syscall.SIGTERM); err != nil {
		return false, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			_ = os.Remove(meta.PIDPath)
			return true, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err = proc.Signal(syscall.SIGKILL); err != nil {
		return false, err
	}
	_ = os.Remove(meta.PIDPath)
	return true, nil
}

func processAliveFromPIDFile(path string) (bool, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false, 0
	}
	return processAlive(pid), pid
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
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

func getAuthOrEnv() (authConfig, error) {
	if tok := os.Getenv("BEEPER_ACCESS_TOKEN"); tok != "" {
		env := os.Getenv("BEEPER_ENV")
		if env == "" {
			env = "prod"
		}
		domain, ok := envDomains[env]
		if !ok {
			return authConfig{}, fmt.Errorf("invalid BEEPER_ENV %q", env)
		}
		return authConfig{Env: env, Domain: domain, Username: os.Getenv("BEEPER_USERNAME"), Token: tok}, nil
	}
	return loadAuthConfig()
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
	data, err := os.ReadFile(path)
	if err != nil {
		return authConfig{}, fmt.Errorf("failed to read auth config (%s). run auth set-token or set BEEPER_ACCESS_TOKEN", path)
	}
	var cfg authConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		return authConfig{}, err
	}
	if cfg.Token == "" || cfg.Domain == "" {
		return authConfig{}, fmt.Errorf("invalid auth config at %s", path)
	}
	return cfg, nil
}

func saveAuthConfig(cfg authConfig) error {
	path, err := authConfigPath()
	if err != nil {
		return err
	}
	if cfg.Domain == "" {
		cfg.Domain = envDomains[cfg.Env]
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func promptLine(label string) (string, error) {
	fmt.Fprint(os.Stdout, label)
	r := bufio.NewReader(os.Stdin)
	s, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func toMap(v any) map[string]any {
	data, _ := json.Marshal(v)
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}
