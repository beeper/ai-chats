package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultUsername = "opencode"

// Client handles HTTP interactions with an OpenCode server.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
	httpSSE  *http.Client // no timeout – used for long-lived SSE streams
}

// APIError captures non-2xx responses from the OpenCode server.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("opencode api error (%d): %s", e.StatusCode, e.Body)
}

// NormalizeBaseURL ensures a base URL is valid and has no trailing slash.
func NormalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("base url is required")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid base url")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

// NewClient constructs an OpenCode client with basic auth if a password is provided.
func NewClient(baseURL, username, password string) (*Client, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	user := strings.TrimSpace(username)
	if user == "" {
		user = defaultUsername
	}
	return &Client{
		baseURL:  normalized,
		username: user,
		password: strings.TrimSpace(password),
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
		httpSSE: &http.Client{}, // no timeout for long-lived SSE streams
	}, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return req, nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	return decoder.Decode(out)
}

// ListSessions returns all sessions from the server.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/session", nil)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	if err := c.do(req, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

// CreateSession creates a new session with an optional title and directory.
func (c *Client) CreateSession(ctx context.Context, title, directory string) (*Session, error) {
	payload := map[string]any{}
	if strings.TrimSpace(title) != "" {
		payload["title"] = strings.TrimSpace(title)
	}
	if strings.TrimSpace(directory) != "" {
		payload["directory"] = strings.TrimSpace(directory)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/session", payload)
	if err != nil {
		return nil, err
	}
	var session Session
	if err := c.do(req, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// DeleteSession deletes an OpenCode session.
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session id is required")
	}
	req, err := c.newRequest(ctx, http.MethodDelete, "/session/"+url.PathEscape(sessionID), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// UpdateSessionTitle updates the title of an OpenCode session.
func (c *Client) UpdateSessionTitle(ctx context.Context, sessionID, title string) (*Session, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session id is required")
	}
	payload := map[string]any{
		"title": strings.TrimSpace(title),
	}
	path := fmt.Sprintf("/session/%s", url.PathEscape(sessionID))
	req, err := c.newRequest(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return nil, err
	}
	var session Session
	if err := c.do(req, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// GetMessage fetches a single message and its parts.
func (c *Client) GetMessage(ctx context.Context, sessionID, messageID string) (*MessageWithParts, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return nil, errors.New("session id and message id are required")
	}
	path := fmt.Sprintf("/session/%s/message/%s", url.PathEscape(sessionID), url.PathEscape(messageID))
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var msg MessageWithParts
	if err := c.do(req, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// ListMessages lists recent messages in a session.
func (c *Client) ListMessages(ctx context.Context, sessionID string, limit int) ([]MessageWithParts, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session id is required")
	}
	query := ""
	if limit > 0 {
		query = fmt.Sprintf("?limit=%d", limit)
	}
	path := fmt.Sprintf("/session/%s/message%s", url.PathEscape(sessionID), query)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var messages []MessageWithParts
	if err := c.do(req, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// SendMessageAsync sends a message to a session asynchronously. The server
// returns 204 immediately; the assistant response is delivered via SSE.
func (c *Client) SendMessageAsync(ctx context.Context, sessionID, messageID string, parts []PartInput) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session id is required")
	}
	if len(parts) == 0 {
		return errors.New("message parts are required")
	}
	payload := map[string]any{
		"parts": parts,
	}
	if strings.TrimSpace(messageID) != "" {
		payload["messageID"] = strings.TrimSpace(messageID)
	}
	path := fmt.Sprintf("/session/%s/prompt_async", url.PathEscape(sessionID))
	req, err := c.newRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// AbortSession aborts a running session.
func (c *Client) AbortSession(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session id is required")
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/session/"+url.PathEscape(sessionID)+"/abort", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) RespondPermission(ctx context.Context, sessionID, permissionID, response string) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(permissionID) == "" {
		return errors.New("session id and permission id are required")
	}
	payload := map[string]any{"response": strings.TrimSpace(response)}
	path := fmt.Sprintf("/session/%s/permissions/%s", url.PathEscape(sessionID), url.PathEscape(permissionID))
	req, err := c.newRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) RejectQuestion(ctx context.Context, requestID string) error {
	if strings.TrimSpace(requestID) == "" {
		return errors.New("question request id is required")
	}
	path := fmt.Sprintf("/question/%s/reject", url.PathEscape(requestID))
	req, err := c.newRequest(ctx, http.MethodPost, path, map[string]any{})
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// IsAuthError returns true if the error is an auth error.
func IsAuthError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden
	}
	return false
}
