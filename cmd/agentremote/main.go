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

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

var envDomains = map[string]string{
	"prod":    "beeper.com",
	"staging": "beeper-staging.com",
	"dev":     "beeper-dev.com",
	"local":   "beeper.localtest.me",
}

type metadata struct {
	Instance         string    `json:"instance"`
	BridgeType       string    `json:"bridge_type"`
	BeeperBridgeName string    `json:"beeper_bridge_name"`
	ConfigPath       string    `json:"config_path"`
	RegistrationPath string    `json:"registration_path"`
	LogPath          string    `json:"log_path"`
	PIDPath          string    `json:"pid_path"`
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
	case "__bridge":
		return cmdInternalBridge(os.Args[2:])
	case "login":
		return cmdLogin(os.Args[2:])
	case "logout":
		return cmdLogout(os.Args[2:])
	case "whoami":
		return cmdWhoami(os.Args[2:])
	case "profiles":
		return cmdProfiles(os.Args[2:])
	case "start":
		return cmdStart(os.Args[2:])
	case "run":
		return cmdRun(os.Args[2:])
	case "stop":
		return cmdStop(os.Args[2:])
	case "stop-all":
		return cmdStopAll(os.Args[2:])
	case "restart":
		return cmdRestart(os.Args[2:])
	case "status":
		return cmdStatus(os.Args[2:])
	case "logs":
		return cmdLogs(os.Args[2:])
	case "list":
		return cmdList()
	case "delete":
		return cmdDelete(os.Args[2:])
	case "version":
		return cmdVersion()
	case "help", "-h", "--help":
		return cmdHelp(os.Args[2:])
	default:
		return didYouMean(os.Args[1])
	}
}

var knownCommands = []string{
	"login", "logout", "whoami", "profiles",
	"start", "run", "stop", "stop-all", "restart",
	"status", "logs", "list", "delete", "version", "help",
}

var commandHelp = map[string]string{
	"login": `Log in to Beeper

Usage: agentremote login [flags]

Flags:
  --env       Beeper environment (prod|staging|dev|local) (default: prod)
  --profile   Profile name (default: "default")
  --email     Email address (will prompt if not provided)
  --code      Login code (will prompt if not provided)

Examples:
  agentremote login
  agentremote login --env staging --email user@example.com
`,
	"logout": `Clear stored credentials

Usage: agentremote logout [flags]

Flags:
  --profile   Profile name (default: "default")

Examples:
  agentremote logout
  agentremote logout --profile work
`,
	"whoami": `Show current user info

Usage: agentremote whoami [flags]

Flags:
  --profile   Profile name (default: "default")
  --output    Output format: text or json (default: text)
`,
	"profiles": `List all profiles

Usage: agentremote profiles [flags]

Flags:
  --output    Output format: text or json (default: text)
`,
	"start": `Start a bridge in the background

Usage: agentremote start <bridge> [flags]

Flags:
  --profile   Profile name (default: "default")
  --name      Instance name (for running multiple instances of the same bridge)
  --env       Override beeper env for this bridge

Examples:
  agentremote start ai
  agentremote start codex --name test
  agentremote start opencode --profile work
`,
	"run": `Run a bridge in the foreground

Usage: agentremote run <bridge> [flags]

Flags:
  --profile   Profile name (default: "default")
  --name      Instance name (for running multiple instances of the same bridge)
  --env       Override beeper env for this bridge

Examples:
  agentremote run ai
  agentremote run codex --name dev
`,
	"stop": `Stop a running bridge

Usage: agentremote stop <instance> [flags]

Flags:
  --profile   Profile name (default: "default")

Examples:
  agentremote stop ai
  agentremote stop codex-test
`,
	"stop-all": `Stop all running bridges

Usage: agentremote stop-all [flags]

Flags:
  --profile   Profile name (default: "default")
`,
	"restart": `Restart a bridge (stop + start)

Usage: agentremote restart <bridge> [flags]

Flags:
  --profile   Profile name (default: "default")
  --name      Instance name

Examples:
  agentremote restart ai
`,
	"status": `Show bridge status

Usage: agentremote status [instance...] [flags]

Shows local instance status and remote bridge state from the Beeper server.
If no instance names are given, shows all instances.

Flags:
  --profile     Profile name (default: "default")
  --no-remote   Skip fetching remote bridge state from server
  --output      Output format: text or json (default: text)

Examples:
  agentremote status
  agentremote status ai
  agentremote status --no-remote
`,
	"logs": `View bridge logs

Usage: agentremote logs <instance> [flags]

Flags:
  --profile   Profile name (default: "default")
  --follow    Follow log output (like tail -f)

Examples:
  agentremote logs ai
  agentremote logs ai --follow
`,
	"list": `List available bridge types

Usage: agentremote list
`,
	"delete": `Delete a bridge instance

Usage: agentremote delete <instance> [flags]

Flags:
  --profile   Profile name (default: "default")
  --remote    Also delete the remote bridge from Beeper

Examples:
  agentremote delete ai
  agentremote delete codex-test --remote
`,
	"version": `Show version info

Usage: agentremote version
`,
}

func cmdHelp(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	cmd := args[0]
	if help, ok := commandHelp[cmd]; ok {
		fmt.Print(help)
		return nil
	}
	return didYouMean(cmd)
}

func didYouMean(input string) error {
	best := ""
	bestDist := 4 // only suggest if distance <= 3
	for _, cmd := range knownCommands {
		d := levenshtein(input, cmd)
		if d < bestDist {
			bestDist = d
			best = cmd
		}
	}
	if best != "" {
		return fmt.Errorf("unknown command %q. Did you mean %q?", input, best)
	}
	return fmt.Errorf("unknown command %q, run 'agentremote help' for usage", input)
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func printUsage() {
	fmt.Println("agentremote - unified AI bridge manager for Beeper")
	fmt.Println()
	fmt.Println("Usage: agentremote <command> [flags] [args]")
	fmt.Println()
	fmt.Println("Auth:")
	fmt.Println("  login       Log in to Beeper")
	fmt.Println("  logout      Clear stored credentials")
	fmt.Println("  whoami      Show current user info")
	fmt.Println("  profiles    List all profiles")
	fmt.Println()
	fmt.Println("Bridges:")
	fmt.Println("  start       Start a bridge in the background")
	fmt.Println("  run         Run a bridge in the foreground")
	fmt.Println("  stop        Stop a running bridge")
	fmt.Println("  stop-all    Stop all running bridges")
	fmt.Println("  restart     Restart a bridge")
	fmt.Println("  status      Show bridge status")
	fmt.Println("  logs        View bridge logs")
	fmt.Println("  list        List available bridge types")
	fmt.Println("  delete      Delete a bridge instance")
	fmt.Println()
	fmt.Println("Other:")
	fmt.Println("  version     Show version info")
	fmt.Println()
	fmt.Println("Global flags:")
	fmt.Println("  --profile   Profile name (default: \"default\")")
}

// ── Auth commands ──

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	env := fs.String("env", "prod", "beeper env (prod|staging|dev|local)")
	profile := fs.String("profile", defaultProfile, "profile name")
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
		InitialDeviceDisplayName: "agentremote",
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
	if err = saveAuthConfig(*profile, cfg); err != nil {
		return err
	}
	fmt.Printf("logged in as @%s:%s (profile: %s)\n", username, domain, *profile)
	return nil
}

func cmdLogout(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
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
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
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
	if cfg.Username == "" || cfg.Username != resp.UserInfo.Username {
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
	fs := flag.NewFlagSet("profiles", flag.ContinueOnError)
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

func parseBridgeFlags(fs *flag.FlagSet) (*string, *string, *string) {
	profile := fs.String("profile", defaultProfile, "profile name")
	name := fs.String("name", "", "instance name (for running multiple instances of the same bridge)")
	env := fs.String("env", "", "override beeper env for this bridge")
	return profile, name, env
}

func resolveBridgeArgs(fs *flag.FlagSet) (bridgeType string, err error) {
	posArgs := fs.Args()
	if len(posArgs) != 1 {
		return "", fmt.Errorf("expected exactly one bridge type argument (available: ai, codex, opencode, openclaw)")
	}
	bridgeType = posArgs[0]
	if _, ok := bridgeRegistry[bridgeType]; !ok {
		return "", fmt.Errorf("unknown bridge type %q (available: ai, codex, opencode, openclaw)", bridgeType)
	}
	return bridgeType, nil
}

func cmdStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	profile, name, _ := parseBridgeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	bridgeType, err := resolveBridgeArgs(fs)
	if err != nil {
		return err
	}
	instName := instanceDirName(bridgeType, *name)
	beeperName := beeperBridgeName(bridgeType, *name)

	sp, err := ensureInstanceLayout(*profile, instName)
	if err != nil {
		return err
	}
	meta, err := ensureInitialized(*profile, instName, bridgeType, beeperName, sp)
	if err != nil {
		return err
	}
	if err = ensureRegistration(*profile, meta, bridgeType); err != nil {
		return err
	}
	running, pid := processAliveFromPIDFile(meta.PIDPath)
	if running {
		fmt.Printf("%s already running (pid %d)\n", instName, pid)
		return nil
	}
	if err = startBridge(meta, bridgeType); err != nil {
		return err
	}
	fmt.Printf("started %s\n", instName)
	printRuntimePaths(meta)
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	profile, name, _ := parseBridgeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	bridgeType, err := resolveBridgeArgs(fs)
	if err != nil {
		return err
	}
	instName := instanceDirName(bridgeType, *name)
	beeperName := beeperBridgeName(bridgeType, *name)

	sp, err := ensureInstanceLayout(*profile, instName)
	if err != nil {
		return err
	}
	meta, err := ensureInitialized(*profile, instName, bridgeType, beeperName, sp)
	if err != nil {
		return err
	}
	if err = ensureRegistration(*profile, meta, bridgeType); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find own executable: %w", err)
	}
	argv := []string{exe, "__bridge", bridgeType, "-c", meta.ConfigPath}
	fmt.Printf("running %s in foreground\n", instName)
	printRuntimePaths(meta)
	if err = os.Chdir(filepath.Dir(meta.ConfigPath)); err != nil {
		return fmt.Errorf("failed to chdir: %w", err)
	}
	return syscall.Exec(exe, argv, os.Environ())
}

func cmdStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	profile := fs.String("profile", defaultProfile, "profile name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	posArgs := fs.Args()
	if len(posArgs) != 1 {
		return fmt.Errorf("expected exactly one instance name argument")
	}
	instName := posArgs[0]

	sp, err := getInstancePaths(*profile, instName)
	if err != nil {
		return err
	}
	meta, err := readMetadata(sp)
	if err != nil {
		// If no metadata, try to stop by PID file directly
		stopped, stopErr := stopByPIDFile(sp.PIDPath)
		if stopErr != nil {
			return stopErr
		}
		if stopped {
			fmt.Printf("stopped %s\n", instName)
		} else {
			fmt.Printf("%s is not running\n", instName)
		}
		return nil
	}
	stopped, err := stopBridge(meta)
	if err != nil {
		return err
	}
	if stopped {
		fmt.Printf("stopped %s\n", instName)
	} else {
		fmt.Printf("%s is not running\n", instName)
	}
	return nil
}

func cmdStopAll(args []string) error {
	fs := flag.NewFlagSet("stop-all", flag.ContinueOnError)
	profile := fs.String("profile", defaultProfile, "profile name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	instances, err := listInstancesForProfile(*profile)
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		fmt.Println("no instances found")
		return nil
	}
	for _, inst := range instances {
		sp, err := getInstancePaths(*profile, inst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: error: %v\n", inst, err)
			continue
		}
		stopped, err := stopByPIDFile(sp.PIDPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: error stopping: %v\n", inst, err)
			continue
		}
		if stopped {
			fmt.Printf("stopped %s\n", inst)
		}
	}
	return nil
}

func cmdRestart(args []string) error {
	if err := cmdStop(args); err != nil {
		return err
	}
	return cmdStart(args)
}

type bridgeStatus struct {
	Name        string        `json:"name"`
	State       string        `json:"state,omitempty"`
	SelfHosted  bool          `json:"self_hosted,omitempty"`
	Local       *localStatus  `json:"local,omitempty"`
	Logins      []loginStatus `json:"logins,omitempty"`
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
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	profile := fs.String("profile", defaultProfile, "profile name")
	noRemote := fs.Bool("no-remote", false, "skip fetching remote bridge state from server")
	output := fs.String("output", "text", "output format (text|json)")
	if err := fs.Parse(args); err != nil {
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
			seen["sh-"+inst] = true
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
		if cut, ok := strings.CutPrefix(inst, "sh-"); ok {
			localName = cut
		} else {
			remoteName = "sh-" + inst
		}

		rb, hasRemote := remoteBridges[remoteName]
		hasLocal := localSet[localName]

		bs := bridgeStatus{Name: remoteName}
		if hasRemote {
			bs.State = string(rb.BridgeState.StateEvent)
			bs.SelfHosted = rb.BridgeState.IsSelfHosted
		}

		if hasLocal {
			sp, err := getInstancePaths(*profile, localName)
			if err == nil {
				running, pid := processAliveFromPIDFile(sp.PIDPath)
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
				selfHosted = " (self-hosted)"
			}
			fmt.Printf("  %s: %s%s\n", bs.Name, bs.State, selfHosted)
		} else if bs.Local != nil {
			fmt.Printf("  %s:\n", bs.Name)
		} else {
			fmt.Printf("  %s: unknown\n", bs.Name)
		}

		if bs.Local != nil {
			if bs.Local.Running {
				fmt.Printf("    local: running (pid %d)\n", bs.Local.PID)
			} else {
				fmt.Printf("    local: stopped\n")
			}
			fmt.Printf("    config: %s\n", bs.Local.ConfigPath)
		}

		if len(bs.Logins) > 0 {
			fmt.Printf("    logins:\n")
			for _, l := range bs.Logins {
				name := ""
				if l.RemoteName != "" {
					name = fmt.Sprintf(" (%s)", l.RemoteName)
				}
				fmt.Printf("      - %s: %s%s\n", l.RemoteID, l.State, name)
			}
		}
	}
	return nil
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	profile := fs.String("profile", defaultProfile, "profile name")
	follow := fs.Bool("follow", false, "follow logs")
	fs.BoolVar(follow, "f", false, "follow logs (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	posArgs := fs.Args()
	if len(posArgs) != 1 {
		return fmt.Errorf("expected exactly one instance name argument")
	}
	instName := posArgs[0]

	sp, err := getInstancePaths(*profile, instName)
	if err != nil {
		return err
	}
	if *follow {
		cmd := exec.Command("tail", "-f", sp.LogPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	f, err := os.Open(sp.LogPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

func cmdList() error {
	fmt.Println("Available bridge types:")
	for name, def := range bridgeRegistry {
		fmt.Printf("  %-10s %s\n", name, def.Description)
	}
	return nil
}

func cmdDelete(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	profile := fs.String("profile", defaultProfile, "profile name")
	remote := fs.Bool("remote", false, "also delete remote beeper bridge")
	if err := fs.Parse(args); err != nil {
		return err
	}
	posArgs := fs.Args()
	if len(posArgs) != 1 {
		return fmt.Errorf("expected exactly one instance name argument")
	}
	instName := posArgs[0]

	sp, err := getInstancePaths(*profile, instName)
	if err != nil {
		return err
	}
	// Stop if running
	if _, err := stopByPIDFile(sp.PIDPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to stop: %v\n", err)
	}
	if *remote {
		meta, readErr := readMetadata(sp)
		if readErr == nil {
			if err := deleteRemoteBridge(*profile, meta.BeeperBridgeName); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to delete remote bridge: %v\n", err)
			}
		}
	}
	if err := os.RemoveAll(sp.Root); err != nil {
		return err
	}
	fmt.Printf("deleted %s\n", instName)
	return nil
}

func cmdVersion() error {
	fmt.Printf("agentremote %s\n", Tag)
	fmt.Printf("commit: %s\n", Commit)
	fmt.Printf("built: %s\n", BuildTime)
	return nil
}

// ── Instance management helpers ──

func ensureInitialized(_, instName, bridgeType, beeperName string, sp *instancePaths) (*metadata, error) {
	meta, err := readOrSynthesizeMetadata(instName, bridgeType, beeperName, sp)
	if err != nil {
		return nil, err
	}
	if _, err = os.Stat(meta.ConfigPath); errors.Is(err, os.ErrNotExist) {
		if err = generateExampleConfig(meta); err != nil {
			return nil, err
		}
	}
	def := bridgeRegistry[bridgeType]
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
	if err = applyConfigOverrides(meta.ConfigPath, overrides); err != nil {
		return nil, err
	}
	if err = writeMetadata(meta, sp.MetaPath); err != nil {
		return nil, err
	}
	return meta, nil
}

func readOrSynthesizeMetadata(instName, bridgeType, beeperName string, sp *instancePaths) (*metadata, error) {
	if data, err := os.ReadFile(sp.MetaPath); err == nil {
		var m metadata
		if err = json.Unmarshal(data, &m); err == nil {
			m.Instance = instName
			m.BridgeType = bridgeType
			m.BeeperBridgeName = beeperName
			m.ConfigPath = sp.ConfigPath
			m.RegistrationPath = sp.RegistrationPath
			m.LogPath = sp.LogPath
			m.PIDPath = sp.PIDPath
			return &m, nil
		}
	}
	return &metadata{
		Instance:         instName,
		BridgeType:       bridgeType,
		BeeperBridgeName: beeperName,
		ConfigPath:       sp.ConfigPath,
		RegistrationPath: sp.RegistrationPath,
		LogPath:          sp.LogPath,
		PIDPath:          sp.PIDPath,
		UpdatedAt:        time.Now().UTC(),
	}, nil
}

func readMetadata(sp *instancePaths) (*metadata, error) {
	data, err := os.ReadFile(sp.MetaPath)
	if err != nil {
		return nil, err
	}
	var m metadata
	if err = json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func writeMetadata(meta *metadata, path string) error {
	meta.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
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

func ensureRegistration(profile string, meta *metadata, bridgeType string) error {
	auth, err := getAuthOrEnv(profile)
	if err != nil {
		return err
	}
	who, err := beeperapi.Whoami(auth.Domain, auth.Token)
	if err != nil {
		return fmt.Errorf("whoami failed: %w", err)
	}
	if auth.Username == "" || auth.Username != who.UserInfo.Username {
		auth.Username = who.UserInfo.Username
		if err := saveAuthConfig(profile, auth); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to save auth config: %v\n", err)
		}
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
	userID := fmt.Sprintf("@%s:%s", auth.Username, auth.Domain)
	if err = patchConfigWithRegistration(meta.ConfigPath, &reg, hc.HomeserverURL.String(), meta.BeeperBridgeName, bridgeType, auth.Domain, reg.AppToken, userID, auth.Token, who.User.AsmuxData.LoginToken); err != nil {
		return err
	}

	state := beeperapi.ReqPostBridgeState{
		StateEvent:   "STARTING",
		Reason:       "SELF_HOST_REGISTERED",
		IsSelfHosted: true,
		BridgeType:   bridgeType,
	}
	if err := beeperapi.PostBridgeState(auth.Domain, auth.Username, meta.BeeperBridgeName, reg.AppToken, state); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to post bridge state: %v\n", err)
	}
	return nil
}

func deleteRemoteBridge(profile, beeperName string) error {
	auth, err := getAuthOrEnv(profile)
	if err != nil {
		return err
	}
	if auth.Username == "" {
		who, werr := beeperapi.Whoami(auth.Domain, auth.Token)
		if werr == nil {
			auth.Username = who.UserInfo.Username
			if err := saveAuthConfig(profile, auth); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save auth config: %v\n", err)
			}
		}
	}
	if auth.Username != "" {
		hc := hungryapi.NewClient(auth.Domain, auth.Username, auth.Token)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := hc.DeleteAppService(ctx, beeperName); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to delete appservice: %v\n", err)
		}
		cancel()
	}
	if err = beeperapi.DeleteBridge(auth.Domain, beeperName, auth.Token); err != nil {
		return fmt.Errorf("failed to delete bridge in beeper api: %w", err)
	}
	return nil
}

// ── Process lifecycle ──

func startBridge(meta *metadata, bridgeType string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find own executable: %w", err)
	}
	logFile, err := os.OpenFile(meta.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "__bridge", bridgeType, "-c", meta.ConfigPath)
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
	return stopByPIDFile(meta.PIDPath)
}

func stopByPIDFile(pidPath string) (bool, error) {
	running, pid := processAliveFromPIDFile(pidPath)
	if !running {
		_ = os.Remove(pidPath)
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
			_ = os.Remove(pidPath)
			return true, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err = proc.Signal(syscall.SIGKILL); err != nil {
		return false, err
	}
	_ = os.Remove(pidPath)
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

// ── Config helpers ──

func patchConfigWithRegistration(configPath string, reg any, homeserverURL, bridgeName, bridgeType, beeperDomain, asToken, userID, matrixToken, provisioningSecret string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	regMap := jsonutil.ToMap(reg)

	setPath(doc, []string{"homeserver", "address"}, homeserverURL)
	setPath(doc, []string{"homeserver", "domain"}, "beeper.local")
	setPath(doc, []string{"homeserver", "software"}, "hungry")
	setPath(doc, []string{"homeserver", "async_media"}, true)
	setPath(doc, []string{"homeserver", "websocket"}, true)
	setPath(doc, []string{"homeserver", "ping_interval_seconds"}, 180)

	setPath(doc, []string{"appservice", "address"}, "irrelevant")
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
	setPath(doc, []string{"appservice", "username_template"}, fmt.Sprintf("%s_{{.}}", bridgeName))

	setPath(doc, []string{"bridge", "personal_filtering_spaces"}, true)
	setPath(doc, []string{"bridge", "private_chat_portal_meta"}, false)
	setPath(doc, []string{"bridge", "split_portals"}, true)
	setPath(doc, []string{"bridge", "bridge_status_notices"}, "none")
	setPath(doc, []string{"bridge", "cross_room_replies"}, true)
	setPath(doc, []string{"bridge", "cleanup_on_logout", "enabled"}, true)
	setPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "private"}, "delete")
	setPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "relayed"}, "delete")
	setPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "shared_no_users"}, "delete")
	setPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "shared_has_users"}, "delete")
	setPath(doc, []string{"bridge", "permissions", userID}, "admin")

	setPath(doc, []string{"database", "type"}, "sqlite3-fk-wal")
	setPath(doc, []string{"database", "uri"}, "file:ai.db?_txlock=immediate")

	setPath(doc, []string{"matrix", "message_status_events"}, true)
	setPath(doc, []string{"matrix", "message_error_notices"}, false)
	setPath(doc, []string{"matrix", "sync_direct_chat_list"}, false)
	setPath(doc, []string{"matrix", "federate_rooms"}, false)

	if provisioningSecret != "" {
		setPath(doc, []string{"provisioning", "shared_secret"}, provisioningSecret)
	}
	setPath(doc, []string{"provisioning", "allow_matrix_auth"}, true)
	setPath(doc, []string{"provisioning", "debug_endpoints"}, true)

	setPath(doc, []string{"network", "beeper", "user_mxid"}, userID)
	setPath(doc, []string{"network", "beeper", "base_url"}, homeserverURL)
	setPath(doc, []string{"network", "beeper", "token"}, matrixToken)

	setPath(doc, []string{"double_puppet", "servers", beeperDomain}, homeserverURL)
	setPath(doc, []string{"double_puppet", "secrets", beeperDomain}, "as_token:"+asToken)
	setPath(doc, []string{"double_puppet", "allow_discovery"}, false)

	setPath(doc, []string{"backfill", "enabled"}, true)
	setPath(doc, []string{"backfill", "queue", "enabled"}, true)
	setPath(doc, []string{"backfill", "queue", "batch_size"}, 50)
	setPath(doc, []string{"backfill", "queue", "max_batches"}, 0)

	setPath(doc, []string{"encryption", "allow"}, true)
	setPath(doc, []string{"encryption", "default"}, true)
	setPath(doc, []string{"encryption", "require"}, true)
	setPath(doc, []string{"encryption", "appservice"}, true)
	setPath(doc, []string{"encryption", "allow_key_sharing"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "delete_outbound_on_ack"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "ratchet_on_decrypt"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "delete_fully_used_on_decrypt"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "delete_prev_on_new_session"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "delete_on_device_delete"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "periodically_delete_expired"}, true)
	setPath(doc, []string{"encryption", "verification_levels", "receive"}, "cross-signed-tofu")
	setPath(doc, []string{"encryption", "verification_levels", "send"}, "cross-signed-tofu")
	setPath(doc, []string{"encryption", "verification_levels", "share"}, "cross-signed-tofu")
	setPath(doc, []string{"encryption", "rotation", "enable_custom"}, true)
	setPath(doc, []string{"encryption", "rotation", "milliseconds"}, 2592000000)
	setPath(doc, []string{"encryption", "rotation", "messages"}, 10000)
	setPath(doc, []string{"encryption", "rotation", "disable_device_change_key_rotation"}, true)

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
	for i := range len(parts) - 1 {
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

func printRuntimePaths(meta *metadata) {
	fmt.Printf("paths:\n")
	fmt.Printf("  config: %s\n", meta.ConfigPath)
	fmt.Printf("  registration: %s\n", meta.RegistrationPath)
	fmt.Printf("  log: %s\n", meta.LogPath)
	fmt.Printf("  pid: %s\n", meta.PIDPath)
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
