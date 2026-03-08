package openclaw

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	openClawProtocolVersion      = 3
	openClawGatewayClientID      = "ai-bridge-openclaw"
	openClawGatewayClientMode    = "operator"
	openClawGatewayDisplayName   = "ai-bridge openclaw"
	openClawGatewayDeviceFamily  = "bridge"
	openClawDefaultSessionLimit  = 1000
	openClawDefaultRequestTimout = 30 * time.Second
)

type gatewayConnectConfig struct {
	URL         string
	Token       string
	Password    string
	DeviceToken string
}

type gatewaySessionRow struct {
	Key                string         `json:"key"`
	Kind               string         `json:"kind"`
	Label              string         `json:"label,omitempty"`
	DisplayName        string         `json:"displayName,omitempty"`
	DerivedTitle       string         `json:"derivedTitle,omitempty"`
	LastMessagePreview string         `json:"lastMessagePreview,omitempty"`
	Channel            string         `json:"channel,omitempty"`
	Subject            string         `json:"subject,omitempty"`
	GroupChannel       string         `json:"groupChannel,omitempty"`
	Space              string         `json:"space,omitempty"`
	ChatType           string         `json:"chatType,omitempty"`
	Origin             string         `json:"origin,omitempty"`
	UpdatedAt          int64          `json:"updatedAt,omitempty"`
	SessionID          string         `json:"sessionId,omitempty"`
	SystemSent         bool           `json:"systemSent,omitempty"`
	AbortedLastRun     bool           `json:"abortedLastRun,omitempty"`
	ThinkingLevel      string         `json:"thinkingLevel,omitempty"`
	VerboseLevel       string         `json:"verboseLevel,omitempty"`
	ReasoningLevel     string         `json:"reasoningLevel,omitempty"`
	ElevatedLevel      string         `json:"elevatedLevel,omitempty"`
	SendPolicy         string         `json:"sendPolicy,omitempty"`
	InputTokens        int64          `json:"inputTokens,omitempty"`
	OutputTokens       int64          `json:"outputTokens,omitempty"`
	TotalTokens        int64          `json:"totalTokens,omitempty"`
	TotalTokensFresh   bool           `json:"totalTokensFresh,omitempty"`
	ResponseUsage      string         `json:"responseUsage,omitempty"`
	ModelProvider      string         `json:"modelProvider,omitempty"`
	Model              string         `json:"model,omitempty"`
	ContextTokens      int64          `json:"contextTokens,omitempty"`
	DeliveryContext    map[string]any `json:"deliveryContext,omitempty"`
	LastChannel        string         `json:"lastChannel,omitempty"`
	LastTo             string         `json:"lastTo,omitempty"`
	LastAccountID      string         `json:"lastAccountId,omitempty"`
}

type gatewaySessionsListResponse struct {
	Path     string              `json:"path,omitempty"`
	Sessions []gatewaySessionRow `json:"sessions"`
}

type gatewaySendResponse struct {
	RunID  string `json:"runId,omitempty"`
	Status string `json:"status,omitempty"`
}

type gatewayAbortResponse struct {
	OK      bool `json:"ok,omitempty"`
	Aborted bool `json:"aborted,omitempty"`
}

type gatewayHistoryResponse struct {
	SessionKey    string           `json:"sessionKey,omitempty"`
	SessionID     string           `json:"sessionId,omitempty"`
	ThinkingLevel string           `json:"thinkingLevel,omitempty"`
	VerboseLevel  string           `json:"verboseLevel,omitempty"`
	Messages      []map[string]any `json:"messages"`
}

type gatewayApprovalRequestEvent struct {
	ID          string         `json:"id"`
	Request     map[string]any `json:"request"`
	CreatedAtMs int64          `json:"createdAtMs,omitempty"`
	ExpiresAtMs int64          `json:"expiresAtMs,omitempty"`
}

type gatewayApprovalResolvedEvent struct {
	ID         string         `json:"id"`
	Decision   string         `json:"decision,omitempty"`
	ResolvedBy string         `json:"resolvedBy,omitempty"`
	TS         int64          `json:"ts,omitempty"`
	Request    map[string]any `json:"request"`
}

type gatewayChatEvent struct {
	RunID        string         `json:"runId,omitempty"`
	SessionKey   string         `json:"sessionKey,omitempty"`
	Seq          int64          `json:"seq,omitempty"`
	State        string         `json:"state,omitempty"`
	StopReason   string         `json:"stopReason,omitempty"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Usage        map[string]any `json:"usage"`
	Message      map[string]any `json:"message"`
}

type gatewayAgentEvent struct {
	RunID       string         `json:"runId,omitempty"`
	SourceRunID string         `json:"sourceRunId,omitempty"`
	SessionKey  string         `json:"sessionKey,omitempty"`
	Seq         int64          `json:"seq,omitempty"`
	Stream      string         `json:"stream,omitempty"`
	TS          int64          `json:"ts,omitempty"`
	Data        map[string]any `json:"data"`
}

type gatewayAgentIdentity struct {
	AgentID string `json:"agentId"`
	Name    string `json:"name,omitempty"`
	Avatar  string `json:"avatar,omitempty"`
	Emoji   string `json:"emoji,omitempty"`
}

type gatewayEvent struct {
	Name    string
	Payload json.RawMessage
}

type gatewayDeviceIdentity struct {
	Version    int    `json:"version"`
	DeviceID   string `json:"device_id"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	CreatedAt  int64  `json:"created_at_ms"`
}

type gatewayRequestFrame struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type gatewayResponseFrame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *struct {
		Code    string `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

type gatewayEventFrame struct {
	Type    string          `json:"type"`
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type gatewayWSClient struct {
	cfg gatewayConnectConfig

	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[string]chan gatewayResponseFrame

	conn      *websocket.Conn
	events    chan gatewayEvent
	closeOnce sync.Once
	closeCh   chan struct{}
	readDone  chan struct{}
}

func newGatewayWSClient(cfg gatewayConnectConfig) *gatewayWSClient {
	return &gatewayWSClient{
		cfg:      cfg,
		pending:  make(map[string]chan gatewayResponseFrame),
		events:   make(chan gatewayEvent, 256),
		closeCh:  make(chan struct{}),
		readDone: make(chan struct{}),
	}
}

func (c *gatewayWSClient) Connect(ctx context.Context) (string, error) {
	wsURL, err := normalizeGatewayWSURL(c.cfg.URL)
	if err != nil {
		return "", err
	}
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
		HTTPHeader:      http.Header{"User-Agent": []string{"ai-bridge/openclaw"}},
	})
	if err != nil {
		return "", fmt.Errorf("dial gateway websocket: %w", err)
	}
	c.conn = conn

	nonce, err := c.waitForConnectChallenge(ctx)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "connect challenge failed")
		return "", err
	}

	identity, err := loadOrCreateGatewayDeviceIdentity()
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "device identity failed")
		return "", err
	}

	connectReqID := uuid.NewString()
	connectPayload, err := c.buildConnectParams(identity, nonce)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "connect payload failed")
		return "", err
	}
	if err = c.writeJSON(ctx, gatewayRequestFrame{
		Type:   "req",
		ID:     connectReqID,
		Method: "connect",
		Params: connectPayload,
	}); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "connect write failed")
		return "", err
	}
	res, err := c.readResponseFrame(ctx)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "connect response failed")
		return "", err
	} else if !res.OK {
		msg := "gateway connect failed"
		if res.Error != nil && strings.TrimSpace(res.Error.Message) != "" {
			msg = strings.TrimSpace(res.Error.Message)
		}
		_ = conn.Close(websocket.StatusPolicyViolation, msg)
		return "", errors.New(msg)
	}

	deviceToken := parseHelloDeviceToken(res.Payload)
	go c.readLoop()
	return deviceToken, nil
}

func (c *gatewayWSClient) Close() {
	c.closeOnce.Do(func() {
		close(c.closeCh)
		if c.conn != nil {
			_ = c.conn.Close(websocket.StatusNormalClosure, "closing")
		}
		<-c.readDone
		close(c.events)
	})
}

func (c *gatewayWSClient) Events() <-chan gatewayEvent {
	return c.events
}

func (c *gatewayWSClient) ListSessions(ctx context.Context, limit int) ([]gatewaySessionRow, error) {
	if limit <= 0 {
		limit = openClawDefaultSessionLimit
	}
	var resp gatewaySessionsListResponse
	if err := c.Request(ctx, "sessions.list", map[string]any{
		"limit":          limit,
		"includeGlobal":  true,
		"includeUnknown": true,
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

func (c *gatewayWSClient) RecentHistory(ctx context.Context, sessionKey string, limit int) (*gatewayHistoryResponse, error) {
	if limit <= 0 {
		limit = openClawDefaultSessionLimit
	}
	var resp gatewayHistoryResponse
	if err := c.Request(ctx, "chat.history", map[string]any{
		"sessionKey": strings.TrimSpace(sessionKey),
		"limit":      limit,
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *gatewayWSClient) SendMessage(ctx context.Context, sessionKey, message string, attachments []map[string]any, thinking, verbose, idempotencyKey string) (*gatewaySendResponse, error) {
	params := map[string]any{
		"sessionKey":     strings.TrimSpace(sessionKey),
		"message":        message,
		"idempotencyKey": strings.TrimSpace(idempotencyKey),
	}
	if len(attachments) > 0 {
		params["attachments"] = attachments
	}
	if strings.TrimSpace(thinking) != "" {
		params["thinking"] = strings.TrimSpace(thinking)
	}
	if strings.TrimSpace(verbose) != "" {
		params["verbose"] = strings.TrimSpace(verbose)
	}
	var resp gatewaySendResponse
	if err := c.Request(ctx, "chat.send", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *gatewayWSClient) AbortRun(ctx context.Context, sessionKey, runID string) error {
	params := map[string]any{"sessionKey": strings.TrimSpace(sessionKey)}
	if strings.TrimSpace(runID) != "" {
		params["runId"] = strings.TrimSpace(runID)
	}
	var resp gatewayAbortResponse
	return c.Request(ctx, "chat.abort", params, &resp)
}

func (c *gatewayWSClient) ResolveApproval(ctx context.Context, approvalID, decision string) error {
	return c.Request(ctx, "exec.approval.resolve", map[string]any{
		"id":       strings.TrimSpace(approvalID),
		"decision": strings.TrimSpace(decision),
	}, nil)
}

func (c *gatewayWSClient) GetAgentIdentity(ctx context.Context, agentID, sessionKey string) (*gatewayAgentIdentity, error) {
	params := map[string]any{}
	if strings.TrimSpace(agentID) != "" {
		params["agentId"] = strings.TrimSpace(agentID)
	}
	if strings.TrimSpace(sessionKey) != "" {
		params["sessionKey"] = strings.TrimSpace(sessionKey)
	}
	if len(params) == 0 {
		return nil, errors.New("agent identity lookup requires agent id or session key")
	}
	var resp gatewayAgentIdentity
	if err := c.Request(ctx, "agent.identity.get", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *gatewayWSClient) Request(ctx context.Context, method string, params map[string]any, out any) error {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), openClawDefaultRequestTimout)
		defer cancel()
	}
	reqID := uuid.NewString()
	respCh := make(chan gatewayResponseFrame, 1)
	c.pendingMu.Lock()
	c.pending[reqID] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
	}()

	if err := c.writeJSON(ctx, gatewayRequestFrame{
		Type:   "req",
		ID:     reqID,
		Method: method,
		Params: params,
	}); err != nil {
		return err
	}

	select {
	case res := <-respCh:
		if !res.OK {
			msg := method + " failed"
			if res.Error != nil && strings.TrimSpace(res.Error.Message) != "" {
				msg = strings.TrimSpace(res.Error.Message)
			}
			return errors.New(msg)
		}
		if out == nil || len(res.Payload) == 0 {
			return nil
		}
		return json.Unmarshal(res.Payload, out)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closeCh:
		return errors.New("gateway connection closed")
	}
}

func (c *gatewayWSClient) writeJSON(ctx context.Context, value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errors.New("gateway connection is not established")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *gatewayWSClient) waitForConnectChallenge(ctx context.Context) (string, error) {
	for {
		frameType, data, err := c.conn.Read(ctx)
		if err != nil {
			return "", fmt.Errorf("read connect challenge: %w", err)
		}
		if frameType != websocket.MessageText {
			continue
		}
		var evt gatewayEventFrame
		if err = json.Unmarshal(data, &evt); err != nil {
			continue
		}
		if evt.Type != "event" || evt.Event != "connect.challenge" {
			continue
		}
		var payload struct {
			Nonce string `json:"nonce"`
		}
		if err = json.Unmarshal(evt.Payload, &payload); err != nil {
			return "", err
		}
		payload.Nonce = strings.TrimSpace(payload.Nonce)
		if payload.Nonce == "" {
			return "", errors.New("gateway connect challenge missing nonce")
		}
		return payload.Nonce, nil
	}
}

func (c *gatewayWSClient) readResponseFrame(ctx context.Context) (*gatewayResponseFrame, error) {
	for {
		frameType, data, err := c.conn.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		if frameType != websocket.MessageText {
			continue
		}
		var res gatewayResponseFrame
		if err = json.Unmarshal(data, &res); err != nil {
			continue
		}
		if res.Type == "res" {
			return &res, nil
		}
	}
}

func (c *gatewayWSClient) readLoop() {
	defer close(c.readDone)
	for {
		_, data, err := c.conn.Read(context.Background())
		if err != nil {
			c.failPending(err)
			return
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err = json.Unmarshal(data, &envelope); err != nil {
			continue
		}
		switch envelope.Type {
		case "res":
			var res gatewayResponseFrame
			if err = json.Unmarshal(data, &res); err != nil {
				continue
			}
			c.pendingMu.Lock()
			respCh := c.pending[res.ID]
			c.pendingMu.Unlock()
			if respCh != nil {
				select {
				case respCh <- res:
				default:
				}
			}
		case "event":
			var evt gatewayEventFrame
			if err = json.Unmarshal(data, &evt); err != nil {
				continue
			}
			select {
			case c.events <- gatewayEvent{Name: evt.Event, Payload: evt.Payload}:
			case <-c.closeCh:
				return
			default:
			}
		}
	}
}

func (c *gatewayWSClient) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		select {
		case ch <- gatewayResponseFrame{
			Type: "res",
			ID:   id,
			OK:   false,
			Error: &struct {
				Code    string `json:"code,omitempty"`
				Message string `json:"message,omitempty"`
			}{Message: err.Error()},
		}:
		default:
		}
	}
}

func (c *gatewayWSClient) buildConnectParams(identity *gatewayDeviceIdentity, nonce string) (map[string]any, error) {
	scopes := []string{"operator.read", "operator.write", "operator.approvals"}
	authToken := strings.TrimSpace(c.cfg.DeviceToken)
	if authToken == "" {
		authToken = strings.TrimSpace(c.cfg.Token)
	}
	params := map[string]any{
		"minProtocol": openClawProtocolVersion,
		"maxProtocol": openClawProtocolVersion,
		"client": map[string]any{
			"id":           openClawGatewayClientID,
			"displayName":  openClawGatewayDisplayName,
			"version":      "0.1.0",
			"platform":     runtime.GOOS,
			"mode":         openClawGatewayClientMode,
			"deviceFamily": openClawGatewayDeviceFamily,
		},
		"role":      "operator",
		"scopes":    scopes,
		"caps":      []string{},
		"locale":    "en-US",
		"userAgent": "ai-bridge/openclaw",
	}
	if authToken != "" {
		params["auth"] = map[string]any{"token": authToken}
	} else if strings.TrimSpace(c.cfg.Password) != "" {
		params["auth"] = map[string]any{"password": strings.TrimSpace(c.cfg.Password)}
	}
	signedAtMs := time.Now().UnixMilli()
	device, err := buildSignedGatewayDevice(identity, authToken, scopes, signedAtMs, nonce)
	if err != nil {
		return nil, err
	}
	params["device"] = device
	return params, nil
}

func normalizeGatewayWSURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid gateway url: %w", err)
	}
	switch parsed.Scheme {
	case "ws", "wss":
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported gateway url scheme %q", parsed.Scheme)
	}
	return parsed.String(), nil
}

func parseHelloDeviceToken(payload json.RawMessage) string {
	var hello struct {
		Auth struct {
			DeviceToken string `json:"deviceToken"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(payload, &hello); err != nil {
		return ""
	}
	return strings.TrimSpace(hello.Auth.DeviceToken)
}

func loadOrCreateGatewayDeviceIdentity() (*gatewayDeviceIdentity, error) {
	path, err := gatewayDeviceIdentityPath()
	if err != nil {
		return nil, err
	}
	if data, readErr := os.ReadFile(path); readErr == nil {
		var existing gatewayDeviceIdentity
		if jsonErr := json.Unmarshal(data, &existing); jsonErr == nil {
			existing.DeviceID = strings.TrimSpace(existing.DeviceID)
			if existing.DeviceID != "" && existing.PublicKey != "" && existing.PrivateKey != "" {
				return &existing, nil
			}
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate gateway device identity: %w", err)
	}
	sum := sha256.Sum256(pub)
	identity := &gatewayDeviceIdentity{
		Version:    1,
		DeviceID:   hex.EncodeToString(sum[:]),
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
		CreatedAt:  time.Now().UnixMilli(),
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return nil, err
	}
	return identity, nil
}

func gatewayDeviceIdentityPath() (string, error) {
	stateDir := strings.TrimSpace(os.Getenv("OPENCLAW_STATE_DIR"))
	if stateDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		stateDir = filepath.Join(home, ".openclaw")
	}
	return filepath.Join(stateDir, "identity", "device.json"), nil
}

func buildSignedGatewayDevice(identity *gatewayDeviceIdentity, authToken string, scopes []string, signedAtMs int64, nonce string) (map[string]any, error) {
	pub, err := base64.StdEncoding.DecodeString(identity.PublicKey)
	if err != nil {
		return nil, err
	}
	priv, err := base64.StdEncoding.DecodeString(identity.PrivateKey)
	if err != nil {
		return nil, err
	}
	payload := strings.Join([]string{
		"v3",
		identity.DeviceID,
		openClawGatewayClientID,
		openClawGatewayClientMode,
		"operator",
		strings.Join(scopes, ","),
		fmt.Sprintf("%d", signedAtMs),
		authToken,
		nonce,
		strings.ToLower(runtime.GOOS),
		openClawGatewayDeviceFamily,
	}, "|")
	signature := ed25519.Sign(ed25519.PrivateKey(priv), []byte(payload))
	return map[string]any{
		"id":        identity.DeviceID,
		"publicKey": base64URLEncode(pub),
		"signature": base64URLEncode(signature),
		"signedAt":  signedAtMs,
		"nonce":     nonce,
	}, nil
}

func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
