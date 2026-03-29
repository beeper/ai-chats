package beeperauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/beeper/bridge-manager/api/beeperapi"
	"maunium.net/go/mautrix"
)

var envDomains = map[string]string{
	"prod":    "beeper.com",
	"staging": "beeper-staging.com",
	"dev":     "beeper-dev.com",
	"local":   "beeper.localtest.me",
}

type Config struct {
	Env      string `json:"env"`
	Domain   string `json:"domain"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

type Store struct {
	Path         string
	MissingError func() error
}

type LoginParams struct {
	Env               string
	Email             string
	Code              string
	DeviceDisplayName string
	Prompt            func(string) (string, error)
}

const loginAuth = "BEEPER-PRIVATE-API-PLEASE-DONT-USE"

var httpClient = &http.Client{Timeout: 30 * time.Second}

type loginCodeResponse struct {
	LoginToken          string                `json:"token"`
	LegacyLoginToken    string                `json:"login_token"`
	LeadToken           string                `json:"leadToken"`
	UsernameSuggestions []string              `json:"usernameSuggestions"`
	Whoami              *beeperapi.RespWhoami `json:"whoami"`
}

func (resp *loginCodeResponse) token() string {
	if resp == nil {
		return ""
	}
	if tok := strings.TrimSpace(resp.LoginToken); tok != "" {
		return tok
	}
	return strings.TrimSpace(resp.LegacyLoginToken)
}

func (resp *loginCodeResponse) needsSignup() bool {
	if resp == nil {
		return false
	}
	return strings.TrimSpace(resp.LeadToken) != "" || len(resp.UsernameSuggestions) > 0
}

func (resp *loginCodeResponse) signupError() error {
	if resp == nil || !resp.needsSignup() {
		return nil
	}
	if len(resp.UsernameSuggestions) > 0 {
		return fmt.Errorf("login code verified, but this account does not exist yet; finish registration in a Beeper client first (username suggestions: %s)", strings.Join(resp.UsernameSuggestions, ", "))
	}
	return fmt.Errorf("login code verified, but this account does not exist yet; finish registration in a Beeper client first")
}

func normalizeEmail(email string) string {
	return strings.TrimSpace(email)
}

func normalizeLoginCode(code string) string {
	return strings.Join(strings.Fields(code), "")
}

func DomainForEnv(env string) (string, error) {
	domain, ok := envDomains[env]
	if !ok {
		return "", fmt.Errorf("invalid env %q", env)
	}
	return domain, nil
}

func EnvNames() []string {
	return slices.Collect(maps.Keys(envDomains))
}

func Login(ctx context.Context, params LoginParams) (Config, error) {
	domain, err := DomainForEnv(params.Env)
	if err != nil {
		return Config{}, err
	}
	email := normalizeEmail(params.Email)
	if email == "" {
		if params.Prompt == nil {
			return Config{}, fmt.Errorf("email is required")
		}
		email, err = params.Prompt("Email: ")
		if err != nil {
			return Config{}, err
		}
		email = normalizeEmail(email)
	}
	if email == "" {
		return Config{}, fmt.Errorf("email is required")
	}

	start, err := beeperapi.StartLogin(domain)
	if err != nil {
		return Config{}, err
	}
	if err = sendLoginEmail(ctx, domain, start.RequestID, email); err != nil {
		return Config{}, err
	}

	code := normalizeLoginCode(params.Code)
	if code == "" {
		if params.Prompt == nil {
			return Config{}, fmt.Errorf("code is required")
		}
		code, err = params.Prompt("Code: ")
		if err != nil {
			return Config{}, err
		}
		code = normalizeLoginCode(code)
	}
	if code == "" {
		return Config{}, fmt.Errorf("code is required")
	}

	resp, err := sendLoginCode(ctx, domain, start.RequestID, code)
	if err != nil {
		return Config{}, err
	}
	if err := resp.signupError(); err != nil {
		return Config{}, err
	}
	matrixClient, err := mautrix.NewClient(fmt.Sprintf("https://matrix.%s", domain), "", "")
	if err != nil {
		return Config{}, fmt.Errorf("failed to create matrix client: %w", err)
	}
	loginCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	loginResp, err := matrixClient.Login(loginCtx, &mautrix.ReqLogin{
		Type:                     "org.matrix.login.jwt",
		Token:                    resp.token(),
		InitialDeviceDisplayName: params.DeviceDisplayName,
	})
	if err != nil {
		return Config{}, fmt.Errorf("matrix login failed: %w", err)
	}
	username := ""
	if resp.Whoami != nil {
		username = strings.TrimSpace(resp.Whoami.UserInfo.Username)
	}
	if username == "" {
		username = loginResp.UserID.Localpart()
	}
	return Config{
		Env:      params.Env,
		Domain:   domain,
		Username: username,
		Token:    loginResp.AccessToken,
	}, nil
}

func sendLoginEmail(ctx context.Context, domain, requestID, email string) error {
	reqBody := map[string]any{
		"request":     requestID,
		"email":       email,
		"supportsOTP": true,
	}
	req, err := newJSONRequest(ctx, http.MethodPost, fmt.Sprintf("https://api.%s/user/login/email", domain), loginAuth, reqBody)
	if err != nil {
		return err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		if body.Error != "" {
			return fmt.Errorf("server returned error (HTTP %d): %s", res.StatusCode, body.Error)
		}
		return fmt.Errorf("unexpected status code %d", res.StatusCode)
	}
	return nil
}

func sendLoginCode(ctx context.Context, domain, requestID, code string) (*loginCodeResponse, error) {
	reqBody := map[string]any{
		"request":  requestID,
		"response": code,
		"appType":  "desktop",
	}
	req, err := newJSONRequest(ctx, http.MethodPost, fmt.Sprintf("https://api.%s/user/login/response", domain), loginAuth, reqBody)
	if err != nil {
		return nil, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer res.Body.Close()
	var resp loginCodeResponse
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var body struct {
			Error   string `json:"error"`
			Retries int    `json:"retries"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		if res.StatusCode == http.StatusForbidden && body.Retries > 0 {
			return nil, fmt.Errorf("%w (%d retries left)", beeperapi.ErrInvalidLoginCode, body.Retries)
		}
		if body.Error != "" {
			return nil, fmt.Errorf("server returned error (HTTP %d): %s", res.StatusCode, body.Error)
		}
		return nil, fmt.Errorf("unexpected status code %d", res.StatusCode)
	}
	if err = json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}
	if resp.token() == "" && !resp.needsSignup() {
		return nil, fmt.Errorf("login response did not include a login token or lead token")
	}
	return &resp, nil
}

func newJSONRequest(ctx context.Context, method, requestURL, bearerToken string, body any) (*http.Request, error) {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(body); err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, &encoded)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", mautrix.DefaultUserAgent)
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	return req, nil
}
