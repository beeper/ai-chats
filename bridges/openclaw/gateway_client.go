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
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	openClawProtocolVersion      = 3
	openClawGatewayClientID      = "gateway-client"
	openClawGatewayClientMode    = "backend"
	openClawGatewayDisplayName   = "ai-bridge openclaw"
	openClawGatewayDeviceFamily  = "bridge"
	openClawGatewayWSReadLimit   = 32 * 1024 * 1024
	openClawGatewayPingInterval  = 30 * time.Second
	openClawGatewayPingTimeout   = 10 * time.Second
	openClawMaxHistoryPageLimit  = 1000
	openClawDefaultRequestTimout = 30 * time.Second
)

type gatewayConnectConfig struct {
	URL         string
	Token       string
	Password    string
	DeviceToken string
}

type gatewayHelloFeatures struct {
	Methods []string `json:"methods,omitempty"`
	Events  []string `json:"events,omitempty"`
}

type gatewayHello struct {
	Type     string               `json:"type,omitempty"`
	Protocol int                  `json:"protocol,omitempty"`
	Server   map[string]any       `json:"server,omitempty"`
	Features gatewayHelloFeatures `json:"features,omitempty"`
	Auth     struct {
		DeviceToken string `json:"deviceToken,omitempty"`
	} `json:"auth,omitempty"`
}

type openClawGatewayCompatibilityReport struct {
	ServerVersion        string
	MissingMethods       []string
	MissingEvents        []string
	HistoryEndpointOK    bool
	HistoryEndpointCode  int
	HistoryEndpointError string
}

func (r openClawGatewayCompatibilityReport) Compatible() bool {
	return len(r.MissingMethods) == 0 && len(r.MissingEvents) == 0 && r.HistoryEndpointOK
}

type gatewaySessionRow struct {
	Key                string          `json:"key"`
	SpawnedBy          string          `json:"spawnedBy,omitempty"`
	Kind               string          `json:"kind"`
	Label              string          `json:"label,omitempty"`
	DisplayName        string          `json:"displayName,omitempty"`
	DerivedTitle       string          `json:"derivedTitle,omitempty"`
	LastMessagePreview string          `json:"lastMessagePreview,omitempty"`
	Channel            string          `json:"channel,omitempty"`
	Subject            string          `json:"subject,omitempty"`
	GroupChannel       string          `json:"groupChannel,omitempty"`
	Space              string          `json:"space,omitempty"`
	ChatType           string          `json:"chatType,omitempty"`
	Origin             json.RawMessage `json:"origin,omitempty"`
	UpdatedAt          int64           `json:"updatedAt,omitempty"`
	SessionID          string          `json:"sessionId,omitempty"`
	SystemSent         bool            `json:"systemSent,omitempty"`
	AbortedLastRun     bool            `json:"abortedLastRun,omitempty"`
	ThinkingLevel      string          `json:"thinkingLevel,omitempty"`
	FastMode           bool            `json:"fastMode,omitempty"`
	VerboseLevel       string          `json:"verboseLevel,omitempty"`
	ReasoningLevel     string          `json:"reasoningLevel,omitempty"`
	ElevatedLevel      string          `json:"elevatedLevel,omitempty"`
	SendPolicy         string          `json:"sendPolicy,omitempty"`
	InputTokens        int64           `json:"inputTokens,omitempty"`
	OutputTokens       int64           `json:"outputTokens,omitempty"`
	TotalTokens        int64           `json:"totalTokens,omitempty"`
	TotalTokensFresh   bool            `json:"totalTokensFresh,omitempty"`
	EstimatedCostUSD   float64         `json:"estimatedCostUsd,omitempty"`
	Status             string          `json:"status,omitempty"`
	StartedAt          int64           `json:"startedAt,omitempty"`
	EndedAt            int64           `json:"endedAt,omitempty"`
	RuntimeMs          int64           `json:"runtimeMs,omitempty"`
	ParentSessionKey   string          `json:"parentSessionKey,omitempty"`
	ChildSessions      []string        `json:"childSessions,omitempty"`
	ResponseUsage      string          `json:"responseUsage,omitempty"`
	ModelProvider      string          `json:"modelProvider,omitempty"`
	Model              string          `json:"model,omitempty"`
	ContextTokens      int64           `json:"contextTokens,omitempty"`
	DeliveryContext    map[string]any  `json:"deliveryContext,omitempty"`
	LastChannel        string          `json:"lastChannel,omitempty"`
	LastTo             string          `json:"lastTo,omitempty"`
	LastAccountID      string          `json:"lastAccountId,omitempty"`
}

func (row gatewaySessionRow) OriginString() string {
	if len(row.Origin) == 0 || string(row.Origin) == "null" {
		return ""
	}
	compact := make(map[string]any)
	if err := json.Unmarshal(row.Origin, &compact); err != nil {
		return ""
	}
	encoded, err := json.Marshal(compact)
	if err != nil {
		return ""
	}
	return string(encoded)
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

type gatewaySessionHistoryResponse struct {
	SessionKey string           `json:"sessionKey,omitempty"`
	Items      []map[string]any `json:"items"`
	Messages   []map[string]any `json:"messages"`
	NextCursor string           `json:"nextCursor,omitempty"`
	HasMore    bool             `json:"hasMore,omitempty"`
}

type gatewaySessionPreviewItem struct {
	Role string `json:"role,omitempty"`
	Text string `json:"text,omitempty"`
}

type gatewaySessionPreviewEntry struct {
	Key    string                      `json:"key,omitempty"`
	Status string                      `json:"status,omitempty"`
	Items  []gatewaySessionPreviewItem `json:"items,omitempty"`
}

type gatewaySessionsPreviewResponse struct {
	TS       int64                        `json:"ts,omitempty"`
	Previews []gatewaySessionPreviewEntry `json:"previews,omitempty"`
}

type gatewayResolveSessionResponse struct {
	OK  bool   `json:"ok,omitempty"`
	Key string `json:"key,omitempty"`
}

type gatewaySessionsPatchResponse struct {
	OK bool `json:"ok,omitempty"`
}

type gatewaySessionsResetResponse struct {
	OK bool `json:"ok,omitempty"`
}

type gatewaySessionsDeleteResponse struct {
	OK bool `json:"ok,omitempty"`
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

type gatewayApprovalListResponse struct {
	Approvals []gatewayApprovalRequestEvent `json:"approvals"`
}

type gatewayChatEvent struct {
	RunID        string         `json:"runId,omitempty"`
	SessionKey   string         `json:"sessionKey,omitempty"`
	Seq          int64          `json:"seq,omitempty"`
	TS           int64          `json:"ts,omitempty"`
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
	AgentID   string `json:"agentId"`
	Name      string `json:"name,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	Emoji     string `json:"emoji,omitempty"`
}

type gatewayAgentSummary struct {
	ID       string                `json:"id"`
	Name     string                `json:"name,omitempty"`
	Identity *gatewayAgentIdentity `json:"identity,omitempty"`
}

type gatewayAgentsListResponse struct {
	DefaultID string                `json:"defaultId,omitempty"`
	MainKey   string                `json:"mainKey,omitempty"`
	Scope     string                `json:"scope,omitempty"`
	Agents    []gatewayAgentSummary `json:"agents"`
}

type gatewayModelChoice struct {
	ID            string   `json:"id,omitempty"`
	Name          string   `json:"name,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	ContextWindow int64    `json:"contextWindow,omitempty"`
	Reasoning     bool     `json:"reasoning,omitempty"`
	Input         []string `json:"input,omitempty"`
}

type gatewayModelsListResponse struct {
	Models []gatewayModelChoice `json:"models"`
}

type gatewayToolCatalogProfile struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
}

type gatewayToolCatalogEntry struct {
	ID              string   `json:"id,omitempty"`
	Label           string   `json:"label,omitempty"`
	Description     string   `json:"description,omitempty"`
	Source          string   `json:"source,omitempty"`
	PluginID        string   `json:"pluginId,omitempty"`
	Optional        bool     `json:"optional,omitempty"`
	DefaultProfiles []string `json:"defaultProfiles,omitempty"`
}

type gatewayToolCatalogGroup struct {
	ID       string                    `json:"id,omitempty"`
	Label    string                    `json:"label,omitempty"`
	Source   string                    `json:"source,omitempty"`
	PluginID string                    `json:"pluginId,omitempty"`
	Tools    []gatewayToolCatalogEntry `json:"tools,omitempty"`
}

type gatewayToolsCatalogResponse struct {
	AgentID  string                      `json:"agentId,omitempty"`
	Profiles []gatewayToolCatalogProfile `json:"profiles,omitempty"`
	Groups   []gatewayToolCatalogGroup   `json:"groups,omitempty"`
}

type gatewayWaitRunResponse struct {
	RunID     string `json:"runId,omitempty"`
	Status    string `json:"status,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
	EndedAt   int64  `json:"endedAt,omitempty"`
	Error     string `json:"error,omitempty"`
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
		Code    string          `json:"code,omitempty"`
		Message string          `json:"message,omitempty"`
		Details json.RawMessage `json:"details,omitempty"`
	} `json:"error,omitempty"`
}

type gatewayErrorDetails struct {
	Code      string `json:"code,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type gatewayRPCError struct {
	Method     string
	Code       string
	Message    string
	DetailCode string
	RequestID  string
	Reason     string
}

func (e *gatewayRPCError) Error() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "gateway request failed"
	}
	if requestID := strings.TrimSpace(e.RequestID); requestID != "" {
		return fmt.Sprintf("%s (requestId: %s)", msg, requestID)
	}
	return msg
}

func (e *gatewayRPCError) IsPairingRequired() bool {
	if e == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(e.DetailCode), "PAIRING_REQUIRED") ||
		strings.EqualFold(strings.TrimSpace(e.Message), "pairing required") ||
		strings.Contains(strings.ToLower(e.Error()), "pairing required")
}

func newGatewayRPCError(method string, res gatewayResponseFrame) error {
	msg := method + " failed"
	code := ""
	detailCode := ""
	requestID := ""
	reason := ""
	if res.Error != nil {
		if strings.TrimSpace(res.Error.Message) != "" {
			msg = strings.TrimSpace(res.Error.Message)
		}
		code = strings.TrimSpace(res.Error.Code)
		if len(res.Error.Details) > 0 {
			var details gatewayErrorDetails
			if err := json.Unmarshal(res.Error.Details, &details); err == nil {
				detailCode = strings.TrimSpace(details.Code)
				requestID = strings.TrimSpace(details.RequestID)
				reason = strings.TrimSpace(details.Reason)
			}
		}
	}
	return &gatewayRPCError{
		Method:     method,
		Code:       code,
		Message:    msg,
		DetailCode: detailCode,
		RequestID:  requestID,
		Reason:     reason,
	}
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

	conn         *websocket.Conn
	events       chan gatewayEvent
	shutdownOnce sync.Once
	closeCh      chan struct{}
	readDone     chan struct{}
	readStarted  atomic.Bool
	lastErrMu    sync.Mutex
	lastErr      error
	helloMu      sync.RWMutex
	hello        *gatewayHello
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
	conn.SetReadLimit(openClawGatewayWSReadLimit)
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
		rpcErr := newGatewayRPCError("connect", *res)
		msg := rpcErr.Error()
		_ = conn.Close(websocket.StatusPolicyViolation, msg)
		return "", rpcErr
	}

	hello := parseGatewayHello(res.Payload)
	deviceToken := c.applyHelloPayload(res.Payload, hello)
	c.readStarted.Store(true)
	go c.readLoop()
	go c.pingLoop()
	return deviceToken, nil
}

func (c *gatewayWSClient) Close() {
	c.shutdown(nil, websocket.StatusNormalClosure, "closing", true, true)
}

func (c *gatewayWSClient) CloseNow() {
	c.shutdown(nil, websocket.StatusNormalClosure, "closing", false, false)
	if c.conn != nil {
		_ = c.conn.CloseNow()
	}
}

func (c *gatewayWSClient) Events() <-chan gatewayEvent {
	return c.events
}

func (c *gatewayWSClient) LastError() error {
	c.lastErrMu.Lock()
	defer c.lastErrMu.Unlock()
	return c.lastErr
}

func (c *gatewayWSClient) Hello() *gatewayHello {
	c.helloMu.RLock()
	defer c.helloMu.RUnlock()
	if c.hello == nil {
		return nil
	}
	clone := *c.hello
	clone.Features.Methods = append([]string(nil), c.hello.Features.Methods...)
	clone.Features.Events = append([]string(nil), c.hello.Features.Events...)
	return &clone
}

func (c *gatewayWSClient) setLastError(err error) {
	c.lastErrMu.Lock()
	defer c.lastErrMu.Unlock()
	c.lastErr = err
}

func (c *gatewayWSClient) shutdown(err error, closeCode websocket.StatusCode, reason string, closeConn, waitRead bool) {
	c.shutdownOnce.Do(func() {
		c.setLastError(err)
		close(c.closeCh)
		if closeConn && c.conn != nil {
			_ = c.conn.Close(closeCode, reason)
		}
		if err == nil {
			err = errors.New("gateway connection closed")
		}
		c.failPending(err)
		if waitRead && c.readStarted.Load() {
			<-c.readDone
		}
		close(c.events)
	})
}

func (c *gatewayWSClient) ListSessions(ctx context.Context, limit int) ([]gatewaySessionRow, error) {
	params := map[string]any{
		"includeGlobal":  true,
		"includeUnknown": true,
	}
	if limit > 0 {
		params["limit"] = limit
	}
	var resp gatewaySessionsListResponse
	if err := c.Request(ctx, "sessions.list", params, &resp); err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

func (c *gatewayWSClient) SessionHistory(ctx context.Context, sessionKey string, limit int, cursor string) (*gatewaySessionHistoryResponse, error) {
	base, err := c.sessionHistoryURL(sessionKey, limit, cursor)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build session history request: %w", err)
	}
	return c.doSessionHistoryRequest(req)
}

func (c *gatewayWSClient) ProbeSessionHistory(ctx context.Context) openClawGatewayCompatibilityReport {
	base, err := c.sessionHistoryURL("agent:main:__beeper_probe__", 1, "")
	if err != nil {
		return openClawGatewayCompatibilityReport{
			HistoryEndpointError: err.Error(),
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return openClawGatewayCompatibilityReport{
			HistoryEndpointError: err.Error(),
		}
	}
	req.Header.Set("Accept", "application/json")
	history, statusCode, reqErr := c.doSessionHistoryRequestWithStatus(req)
	report := openClawGatewayCompatibilityReport{
		HistoryEndpointCode: statusCode,
	}
	if reqErr == nil {
		report.HistoryEndpointOK = true
		return report
	}
	report.HistoryEndpointError = reqErr.Error()
	if statusCode == http.StatusNotFound {
		var errPayload struct {
			Error struct {
				Type string `json:"type,omitempty"`
			} `json:"error,omitempty"`
		}
		if history != nil && len(history.Messages) == 0 {
			report.HistoryEndpointOK = true
			return report
		}
		_ = errPayload
	}
	return report
}

func (c *gatewayWSClient) ListPendingApprovals(ctx context.Context) ([]gatewayApprovalRequestEvent, error) {
	var resp gatewayApprovalListResponse
	if err := c.Request(ctx, "exec.approval.list", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return resp.Approvals, nil
}

func (c *gatewayWSClient) sessionHistoryURL(sessionKey string, limit int, cursor string) (*url.URL, error) {
	baseURL, err := normalizeGatewayHTTPURL(c.cfg.URL)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid gateway url: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/sessions/" + url.PathEscape(strings.TrimSpace(sessionKey)) + "/history"
	query := base.Query()
	if limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", min(limit, openClawMaxHistoryPageLimit)))
	}
	if trimmedCursor := strings.TrimSpace(cursor); trimmedCursor != "" {
		query.Set("cursor", trimmedCursor)
	}
	base.RawQuery = query.Encode()
	return base, nil
}

func (c *gatewayWSClient) doSessionHistoryRequest(req *http.Request) (*gatewaySessionHistoryResponse, error) {
	history, _, err := c.doSessionHistoryRequestWithStatus(req)
	return history, err
}

func (c *gatewayWSClient) doSessionHistoryRequestWithStatus(req *http.Request) (*gatewaySessionHistoryResponse, int, error) {
	if req == nil {
		return nil, 0, errors.New("session history request is required")
	}
	if authToken := c.httpBearerAuthToken(); authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	req.Header.Set("User-Agent", "ai-bridge/openclaw")

	resp, err := (&http.Client{Timeout: openClawDefaultRequestTimout}).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request session history: %w", err)
	}
	defer resp.Body.Close()

	var history gatewaySessionHistoryResponse
	if err = json.NewDecoder(resp.Body).Decode(&history); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode session history response: %w", err)
	}
	if len(history.Messages) == 0 && len(history.Items) > 0 {
		history.Messages = history.Items
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &history, resp.StatusCode, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		var errPayload struct {
			Error struct {
				Type    string `json:"type,omitempty"`
				Message string `json:"message,omitempty"`
			} `json:"error,omitempty"`
		}
		data, _ := json.Marshal(history)
		_ = json.Unmarshal(data, &errPayload)
		if strings.EqualFold(strings.TrimSpace(errPayload.Error.Type), "not_found") {
			return &history, resp.StatusCode, fmt.Errorf("session history request failed: not_found")
		}
	}
	if len(history.Messages) == 0 && len(history.Items) == 0 && !history.HasMore {
		return &history, resp.StatusCode, fmt.Errorf("session history request failed: http %d", resp.StatusCode)
	}
	return &history, resp.StatusCode, fmt.Errorf("session history request failed: http %d", resp.StatusCode)
}

func (c *gatewayWSClient) PreviewSessions(ctx context.Context, keys []string, limit, maxChars int) (*gatewaySessionsPreviewResponse, error) {
	filtered := make([]string, 0, len(keys))
	for _, key := range keys {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return &gatewaySessionsPreviewResponse{}, nil
	}
	if limit <= 0 {
		limit = 6
	}
	if maxChars <= 0 {
		maxChars = 240
	}
	var resp gatewaySessionsPreviewResponse
	if err := c.Request(ctx, "sessions.preview", map[string]any{
		"keys":     filtered,
		"limit":    limit,
		"maxChars": maxChars,
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *gatewayWSClient) ResolveSessionKey(ctx context.Context, key string) (string, error) {
	var resp gatewayResolveSessionResponse
	if err := c.Request(ctx, "sessions.resolve", map[string]any{
		"key": strings.TrimSpace(key),
	}, &resp); err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Key), nil
}

func (c *gatewayWSClient) PatchSession(ctx context.Context, key string, patch map[string]any) error {
	var resp gatewaySessionsPatchResponse
	return c.Request(ctx, "sessions.patch", buildPatchSessionParams(key, patch), &resp)
}

func (c *gatewayWSClient) ResetSession(ctx context.Context, key string) error {
	var resp gatewaySessionsResetResponse
	return c.Request(ctx, "sessions.reset", map[string]any{
		"key": strings.TrimSpace(key),
	}, &resp)
}

func (c *gatewayWSClient) DeleteSession(ctx context.Context, key string, deleteTranscript bool) error {
	var resp gatewaySessionsDeleteResponse
	return c.Request(ctx, "sessions.delete", map[string]any{
		"key":              strings.TrimSpace(key),
		"deleteTranscript": deleteTranscript,
	}, &resp)
}

func (c *gatewayWSClient) ListAgents(ctx context.Context) (*gatewayAgentsListResponse, error) {
	var resp gatewayAgentsListResponse
	if err := c.Request(ctx, "agents.list", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Agents {
		resp.Agents[i].ID = strings.TrimSpace(resp.Agents[i].ID)
		resp.Agents[i].Name = strings.TrimSpace(resp.Agents[i].Name)
		resp.Agents[i].Identity = normalizeGatewayAgentIdentity(resp.Agents[i].Identity)
	}
	resp.DefaultID = strings.TrimSpace(resp.DefaultID)
	resp.MainKey = strings.TrimSpace(resp.MainKey)
	resp.Scope = strings.TrimSpace(resp.Scope)
	return &resp, nil
}

func (c *gatewayWSClient) ListModels(ctx context.Context) (*gatewayModelsListResponse, error) {
	var resp gatewayModelsListResponse
	if err := c.Request(ctx, "models.list", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Models {
		resp.Models[i].ID = strings.TrimSpace(resp.Models[i].ID)
		resp.Models[i].Name = strings.TrimSpace(resp.Models[i].Name)
		resp.Models[i].Provider = strings.TrimSpace(resp.Models[i].Provider)
		inputs := resp.Models[i].Input[:0]
		for _, modality := range resp.Models[i].Input {
			modality = strings.ToLower(strings.TrimSpace(modality))
			if modality != "" {
				inputs = append(inputs, modality)
			}
		}
		resp.Models[i].Input = inputs
	}
	return &resp, nil
}

func (c *gatewayWSClient) GetToolsCatalog(ctx context.Context, agentID string) (*gatewayToolsCatalogResponse, error) {
	params := map[string]any{}
	if trimmed := strings.TrimSpace(agentID); trimmed != "" {
		params["agentId"] = trimmed
	}
	var resp gatewayToolsCatalogResponse
	if err := c.Request(ctx, "tools.catalog", params, &resp); err != nil {
		return nil, err
	}
	resp.AgentID = strings.TrimSpace(resp.AgentID)
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
	return normalizeGatewayAgentIdentity(&resp), nil
}

func (c *gatewayWSClient) WaitForRun(ctx context.Context, runID string, timeout time.Duration) (*gatewayWaitRunResponse, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, errors.New("run id is required")
	}
	params := map[string]any{"runId": runID}
	if timeout > 0 {
		params["timeoutMs"] = timeout.Milliseconds()
	}
	var resp gatewayWaitRunResponse
	if err := c.Request(ctx, "agent.wait", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func normalizeGatewayAgentIdentity(identity *gatewayAgentIdentity) *gatewayAgentIdentity {
	if identity == nil {
		return nil
	}
	normalized := *identity
	normalized.AgentID = strings.TrimSpace(normalized.AgentID)
	normalized.Name = strings.TrimSpace(normalized.Name)
	normalized.Avatar = strings.TrimSpace(normalized.Avatar)
	normalized.AvatarURL = strings.TrimSpace(normalized.AvatarURL)
	normalized.Emoji = strings.TrimSpace(normalized.Emoji)
	if normalized.Avatar == "" {
		normalized.Avatar = normalized.AvatarURL
	}
	return &normalized
}

func (c *gatewayWSClient) Request(ctx context.Context, method string, params map[string]any, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, openClawDefaultRequestTimout)
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
			return newGatewayRPCError(method, res)
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
			c.shutdown(err, websocket.StatusAbnormalClosure, "read failed", false, false)
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
			}
		}
	}
}

func (c *gatewayWSClient) pingLoop() {
	ticker := time.NewTicker(openClawGatewayPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.closeCh:
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(context.Background(), openClawGatewayPingTimeout)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				c.shutdown(err, websocket.StatusGoingAway, "ping failed", true, false)
				return
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
				Code    string          `json:"code,omitempty"`
				Message string          `json:"message,omitempty"`
				Details json.RawMessage `json:"details,omitempty"`
			}{Message: err.Error()},
		}:
		default:
		}
	}
}

func (c *gatewayWSClient) buildConnectParams(identity *gatewayDeviceIdentity, nonce string) (map[string]any, error) {
	scopes := []string{"operator.read", "operator.write", "operator.approvals"}
	sharedToken := strings.TrimSpace(c.cfg.Token)
	deviceToken := strings.TrimSpace(c.cfg.DeviceToken)
	authToken := sharedToken
	if authToken == "" {
		authToken = deviceToken
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
		"role":        "operator",
		"scopes":      scopes,
		"caps":        []string{},
		"commands":    []string{},
		"permissions": map[string]bool{},
		"locale":      "en-US",
		"userAgent":   "ai-bridge/openclaw",
	}
	if authToken != "" {
		auth := map[string]any{"token": authToken}
		if deviceToken != "" {
			auth["deviceToken"] = deviceToken
		}
		params["auth"] = auth
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

func normalizeGatewayHTTPURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid gateway url: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https":
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	default:
		return "", fmt.Errorf("unsupported gateway url scheme %q", parsed.Scheme)
	}
	return parsed.String(), nil
}

func (c *gatewayWSClient) httpBearerAuthToken() string {
	if token := strings.TrimSpace(c.cfg.Token); token != "" {
		return token
	}
	if deviceToken := strings.TrimSpace(c.cfg.DeviceToken); deviceToken != "" {
		return deviceToken
	}
	return strings.TrimSpace(c.cfg.Password)
}

func buildPatchSessionParams(key string, patch map[string]any) map[string]any {
	params := make(map[string]any, len(patch)+1)
	params["key"] = strings.TrimSpace(key)
	for patchKey, patchValue := range patch {
		if patchKey == "key" {
			continue
		}
		params[patchKey] = patchValue
	}
	return params
}

func (c *gatewayWSClient) applyHelloPayload(payload json.RawMessage, hello *gatewayHello) string {
	if hello == nil {
		hello = parseGatewayHello(payload)
	}
	c.helloMu.Lock()
	c.hello = hello
	c.helloMu.Unlock()
	deviceToken := parseHelloDeviceToken(payload)
	if deviceToken != "" {
		c.cfg.DeviceToken = deviceToken
	}
	return deviceToken
}

func parseHelloDeviceToken(payload json.RawMessage) string {
	hello := parseGatewayHello(payload)
	if hello == nil {
		return ""
	}
	return strings.TrimSpace(hello.Auth.DeviceToken)
}

func parseGatewayHello(payload json.RawMessage) *gatewayHello {
	var hello struct {
		Type     string               `json:"type,omitempty"`
		Protocol int                  `json:"protocol,omitempty"`
		Server   map[string]any       `json:"server,omitempty"`
		Features gatewayHelloFeatures `json:"features,omitempty"`
		Auth     struct {
			DeviceToken string `json:"deviceToken"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(payload, &hello); err != nil {
		return nil
	}
	return &gatewayHello{
		Type:     strings.TrimSpace(hello.Type),
		Protocol: hello.Protocol,
		Server:   hello.Server,
		Features: gatewayHelloFeatures{Methods: append([]string(nil), hello.Features.Methods...), Events: append([]string(nil), hello.Features.Events...)},
		Auth: struct {
			DeviceToken string `json:"deviceToken,omitempty"`
		}{DeviceToken: strings.TrimSpace(hello.Auth.DeviceToken)},
	}
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
