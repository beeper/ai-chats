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
	"path/filepath"
	"strings"
	"time"

	"github.com/beeper/bridge-manager/api/beeperapi"
	"maunium.net/go/mautrix"
)

type authConfig struct {
	Env      string `json:"env"`
	Domain   string `json:"domain"`
	Username string `json:"username"`
	Token    string `json:"token"`
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
	syncAuthUsername(&cfg, resp.UserInfo.Username)
	return nil
}

func cmdAuth(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("auth requires subcommand: set-token|whoami|show")
	}
	switch args[0] {
	case "set-token":
		return cmdAuthSetToken(args[1:])
	case "show":
		return cmdAuthShow()
	case "whoami":
		return cmdAuthWhoami()
	default:
		return fmt.Errorf("unknown auth subcommand %q", args[0])
	}
}

func cmdAuthSetToken(args []string) error {
	fs := flag.NewFlagSet("auth set-token", flag.ContinueOnError)
	token := fs.String("token", "", "beeper access token (syt_...)")
	env := fs.String("env", "prod", "beeper env")
	username := fs.String("username", "", "matrix username")
	if err := fs.Parse(args); err != nil {
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
}

func cmdAuthShow() error {
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
}

func cmdAuthWhoami() error {
	cfg, err := getAuthOrEnv()
	if err != nil {
		return err
	}
	resp, err := beeperapi.Whoami(cfg.Domain, cfg.Token)
	if err != nil {
		return err
	}
	fmt.Printf("@%s:%s (%s)\n", resp.UserInfo.Username, cfg.Domain, resp.UserInfo.Email)
	syncAuthUsername(&cfg, resp.UserInfo.Username)
	return nil
}

// syncAuthUsername updates the persisted auth config if the username changed.
func syncAuthUsername(cfg *authConfig, username string) {
	if cfg.Username == "" || cfg.Username != username {
		cfg.Username = username
		_ = saveAuthConfig(*cfg)
	}
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
