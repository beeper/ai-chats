package codexrpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type InitializeCapabilities struct {
	ExperimentalAPI           bool     `json:"experimentalApi,omitempty"`
	OptOutNotificationMethods []string `json:"optOutNotificationMethods,omitempty"`
}

type initializeParamsWire struct {
	ClientInfo   ClientInfo              `json:"clientInfo"`
	Capabilities *InitializeCapabilities `json:"capabilities,omitempty"`
}

type ProcessConfig struct {
	Command string
	Args    []string
	Env     []string
	// WebSocketURL enables websocket transport. When set, one JSON-RPC message
	// is sent per text frame, and stdio is ignored for RPC payloads.
	WebSocketURL string
	// OnStderr is called for each line of stderr output from the process.
	// If nil, stderr is silently discarded.
	OnStderr func(line string)
	// OnProcessExit is called when the process exits with its exit error (nil if exit code 0).
	OnProcessExit func(err error)
}

type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	wsConn *websocket.Conn
	wsCtx  context.Context
	wsStop context.CancelFunc

	writeMu sync.RWMutex
	writeCh chan writeReq

	nextID  atomic.Int64
	pending sync.Map // idKey -> chan Response

	notifMu   sync.RWMutex
	notifSubs []func(method string, params json.RawMessage)

	reqMu         sync.RWMutex
	requestRoutes map[string]func(ctx context.Context, req Request) (any, *RPCError)

	onStderr      func(line string)
	onProcessExit func(err error)

	closed         atomic.Bool
	failAllPending func() // drains and errors all pending RPC calls exactly once

	waitForProcess func() error // calls cmd.Wait() exactly once and caches the result
}

type writeReq struct {
	ctx  context.Context
	data []byte
	done chan error
}

func StartProcess(ctx context.Context, cfg ProcessConfig) (*Client, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("missing command")
	}
	args := slices.Clone(cfg.Args)
	cmd := exec.CommandContext(ctx, cfg.Command, args...)
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	wsURL := strings.TrimSpace(cfg.WebSocketURL)
	var wsConn *websocket.Conn
	var wsCtx context.Context
	var wsStop context.CancelFunc
	if wsURL != "" {
		dialCtx := ctx
		if dialCtx == nil {
			dialCtx = context.Background()
		}
		wsConn, err = dialWebSocketWithRetry(dialCtx, wsURL, 20*time.Second)
		if err != nil {
			_ = cmd.Process.Kill()
			return nil, err
		}
		wsConn.SetReadLimit(32 * 1024 * 1024)
		wsCtx, wsStop = context.WithCancel(context.Background())
	}

	c := &Client{
		cmd:           cmd,
		stdin:         stdin,
		stdout:        stdout,
		stderr:        stderr,
		wsConn:        wsConn,
		wsCtx:         wsCtx,
		wsStop:        wsStop,
		writeCh:       make(chan writeReq, 256),
		requestRoutes: make(map[string]func(ctx context.Context, req Request) (any, *RPCError)),
		onStderr:      cfg.OnStderr,
		onProcessExit: cfg.OnProcessExit,
	}
	c.failAllPending = sync.OnceFunc(func() {
		rpcErr := &RPCError{Code: -32000, Message: "rpc process closed"}
		c.pending.Range(func(key, value any) bool {
			ch, ok := value.(chan Response)
			if !ok || ch == nil {
				c.pending.Delete(key)
				return true
			}
			select {
			case ch <- Response{Error: rpcErr}:
			default:
			}
			c.pending.Delete(key)
			return true
		})
	})
	c.waitForProcess = sync.OnceValue(func() error {
		if c.cmd != nil {
			return c.cmd.Wait()
		}
		return nil
	})
	c.nextID.Store(1)
	writeCh := c.writeCh
	go c.writeLoop(writeCh)
	go c.readLoop()
	if c.wsConn != nil && c.stdout != nil {
		go c.drainStdout()
	}
	if c.stderr != nil {
		go c.drainStderr()
	}
	// Monitor process exit in a separate goroutine.
	go func() {
		waitErr := c.waitForProcess()
		if c.onProcessExit != nil {
			c.onProcessExit(waitErr)
		}
		c.failAllPending()
		_ = c.Close()
	}()
	return c, nil
}

func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	c.failAllPending()
	c.writeMu.Lock()
	if c.writeCh != nil {
		close(c.writeCh)
		c.writeCh = nil
	}
	c.writeMu.Unlock()
	if c.wsStop != nil {
		c.wsStop()
	}
	if c.wsConn != nil {
		_ = c.wsConn.Close(websocket.StatusNormalClosure, "closing")
	}
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	// Wait for process exit (uses sync.OnceValue to avoid double cmd.Wait())
	_ = c.waitForProcess()
	return nil
}

func (c *Client) OnNotification(fn func(method string, params json.RawMessage)) {
	if fn == nil {
		return
	}
	c.notifMu.Lock()
	c.notifSubs = append(c.notifSubs, fn)
	c.notifMu.Unlock()
}

func (c *Client) HandleRequest(method string, fn func(ctx context.Context, req Request) (any, *RPCError)) {
	method = strings.TrimSpace(method)
	if method == "" || fn == nil {
		return
	}
	c.reqMu.Lock()
	c.requestRoutes[method] = fn
	c.reqMu.Unlock()
}

type InitializeOptions struct {
	ExperimentalAPI           bool
	OptOutNotificationMethods []string
}

func (c *Client) Initialize(ctx context.Context, info ClientInfo, experimental bool) (string, error) {
	return c.InitializeWithOptions(ctx, info, InitializeOptions{ExperimentalAPI: experimental})
}

func (c *Client) InitializeWithOptions(ctx context.Context, info ClientInfo, opts InitializeOptions) (string, error) {
	params := initializeParamsWire{
		ClientInfo: info,
	}
	if opts.ExperimentalAPI || len(opts.OptOutNotificationMethods) > 0 {
		params.Capabilities = &InitializeCapabilities{
			ExperimentalAPI: opts.ExperimentalAPI,
		}
		if len(opts.OptOutNotificationMethods) > 0 {
			params.Capabilities.OptOutNotificationMethods = slices.Clone(opts.OptOutNotificationMethods)
		}
	}
	var result struct {
		UserAgent string `json:"userAgent"`
	}
	if err := c.Call(ctx, "initialize", params, &result); err != nil {
		return "", err
	}
	// Followed by initialized notification.
	if err := c.Notify(ctx, "initialized", map[string]any{}); err != nil {
		return "", err
	}
	return result.UserAgent, nil
}

func (c *Client) Notify(ctx context.Context, method string, params any) error {
	msg := map[string]any{
		"method": method,
	}
	if params != nil {
		msg["params"] = params
	}
	return c.writeJSONL(ctx, msg)
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	const maxRetries = 5
	for attempt := 0; ; attempt++ {
		idNum := c.nextID.Add(1)
		idRaw, _ := json.Marshal(idNum)
		ch := make(chan Response, 1)
		c.pending.Store(idKey(idRaw), ch)

		req := map[string]any{
			"id":     idNum,
			"method": method,
		}
		if params != nil {
			req["params"] = params
		}
		if err := c.writeJSONL(ctx, req); err != nil {
			c.pending.Delete(idKey(idRaw))
			return err
		}

		var resp Response
		select {
		case resp = <-ch:
		case <-ctx.Done():
			c.pending.Delete(idKey(idRaw))
			return ctx.Err()
		}
		c.pending.Delete(idKey(idRaw))
		if resp.Error != nil {
			if c.wsConn != nil && shouldRetryServerOverloaded(resp.Error) && attempt < maxRetries {
				if err := waitRetryBackoff(ctx, attempt); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if out == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return errors.New("missing rpc result")
		}
		return json.Unmarshal(resp.Result, out)
	}
}

func (c *Client) writeJSONL(ctx context.Context, v any) (err error) {
	if c.closed.Load() {
		return errors.New("rpc client closed")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	req := writeReq{
		ctx:  ctx,
		data: data,
		done: make(chan error, 1),
	}

	c.writeMu.RLock()
	ch := c.writeCh
	if ch == nil {
		c.writeMu.RUnlock()
		return errors.New("rpc client closed")
	}
	select {
	case ch <- req:
		c.writeMu.RUnlock()
	case <-ctx.Done():
		c.writeMu.RUnlock()
		return ctx.Err()
	}
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		// Best-effort: if the write is stuck (e.g. child stopped reading),
		// close the process to unblock the writer goroutine.
		_ = c.Close()
		return ctx.Err()
	}
}

func (c *Client) writeLoop(ch <-chan writeReq) {
	for req := range ch {
		if c.closed.Load() {
			select {
			case req.done <- errors.New("rpc client closed"):
			default:
			}
			continue
		}
		// Perform the write in a goroutine so we can enforce context cancellation.
		writeDone := make(chan error, 1)
		go func(b []byte) {
			defer func() {
				if r := recover(); r != nil {
					select {
					case writeDone <- fmt.Errorf("rpc write panic: %v", r):
					default:
					}
				}
			}()
			if c.wsConn != nil {
				payload := bytes.TrimSpace(b)
				writeDone <- c.wsConn.Write(req.ctx, websocket.MessageText, payload)
				return
			}
			_, err := c.stdin.Write(b)
			writeDone <- err
		}(req.data)

		// If caller provided no deadline, enforce a conservative max write time.
		maxWrite := 30 * time.Second
		var timer *time.Timer
		if _, ok := req.ctx.Deadline(); !ok {
			timer = time.NewTimer(maxWrite)
		}

		var err error
		select {
		case err = <-writeDone:
		case <-req.ctx.Done():
			err = req.ctx.Err()
			_ = c.Close()
		case <-timerC(timer):
			err = errors.New("rpc write timed out")
			_ = c.Close()
		}
		if timer != nil {
			timer.Stop()
		}
		select {
		case req.done <- err:
		default:
		}
	}
}

func timerC(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func (c *Client) readLoop() {
	if c.wsConn != nil {
		for {
			_, payload, err := c.wsConn.Read(c.wsCtx)
			if err != nil {
				break
			}
			c.handleInboundJSON(payload)
		}
		c.failAllPending()
		_ = c.Close()
		return
	}

	sc := bufio.NewScanner(c.stdout)
	// Default token limit is 64K; Codex items/diffs can be bigger.
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 32*1024*1024)
	for sc.Scan() {
		c.handleInboundJSON(sc.Bytes())
	}
	c.failAllPending()
	_ = c.Close()
}

func (c *Client) handleInboundJSON(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(line, &probe); err != nil {
		slog.Warn("codexrpc: failed to parse JSON payload from process", "error", err)
		return
	}
	if _, hasMethod := probe["method"]; hasMethod {
		if _, hasID := probe["id"]; hasID {
			var req Request
			if err := json.Unmarshal(line, &req); err != nil {
				return
			}
			go c.handleServerRequest(req)
			return
		}
		var n Notification
		if err := json.Unmarshal(line, &n); err != nil {
			return
		}
		c.dispatchNotification(n.Method, n.Params)
		return
	}
	if _, hasID := probe["id"]; hasID {
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			slog.Warn("codexrpc: failed to parse response JSON", "error", err)
			return
		}
		if chAny, ok := c.pending.Load(idKey(resp.ID)); ok {
			ch := chAny.(chan Response)
			select {
			case ch <- resp:
			default:
				slog.Warn("codexrpc: dropped response (channel full)", "id", string(resp.ID))
			}
		}
	}
}

func (c *Client) handleServerRequest(req Request) {
	method := strings.TrimSpace(req.Method)
	if method == "" {
		return
	}
	c.reqMu.RLock()
	handler := c.requestRoutes[method]
	c.reqMu.RUnlock()

	if handler == nil {
		_ = c.writeResponse(context.Background(), req.ID, nil, &RPCError{
			Code:    -32601,
			Message: "Method not found",
		})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	result, rpcErr := handler(ctx, req)
	_ = c.writeResponse(context.Background(), req.ID, result, rpcErr)
}

func (c *Client) writeResponse(ctx context.Context, id json.RawMessage, result any, rpcErr *RPCError) error {
	if len(id) == 0 {
		return nil
	}
	msg := map[string]any{
		"id": id,
	}
	if rpcErr != nil {
		msg["error"] = rpcErr
	} else {
		msg["result"] = result
	}
	return c.writeJSONL(ctx, msg)
}

func (c *Client) dispatchNotification(method string, params json.RawMessage) {
	c.notifMu.RLock()
	subs := slices.Clone(c.notifSubs)
	c.notifMu.RUnlock()
	for _, fn := range subs {
		fn(method, params)
	}
}

func (c *Client) drainStderr() {
	r := bufio.NewReader(c.stderr)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		if line != "" && c.onStderr != nil {
			c.onStderr(line)
		}
	}
}

func (c *Client) drainStdout() {
	if c.stdout == nil {
		return
	}
	_, _ = io.Copy(io.Discard, c.stdout)
}

func shouldRetryServerOverloaded(rpcErr *RPCError) bool {
	if rpcErr == nil {
		return false
	}
	if rpcErr.Code != -32001 {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(rpcErr.Message))
	return strings.Contains(msg, "overloaded") || strings.Contains(msg, "retry later")
}

func waitRetryBackoff(ctx context.Context, attempt int) error {
	backoff := min(100*time.Millisecond<<attempt, 3*time.Second)
	jitter := time.Duration(rand.Int63n(int64(250 * time.Millisecond)))
	timer := time.NewTimer(backoff + jitter)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func dialWebSocketWithRetry(ctx context.Context, wsURL string, maxWait time.Duration) (*websocket.Conn, error) {
	if strings.TrimSpace(wsURL) == "" {
		return nil, errors.New("missing websocket url")
	}
	dialCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		dialCtx, cancel = context.WithTimeout(ctx, maxWait)
	}
	defer cancel()

	backoff := 50 * time.Millisecond
	var lastErr error
	for {
		conn, _, err := websocket.Dial(dialCtx, wsURL, nil)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		timer := time.NewTimer(backoff + time.Duration(rand.Int63n(int64(100*time.Millisecond))))
		select {
		case <-timer.C:
		case <-dialCtx.Done():
			timer.Stop()
			return nil, fmt.Errorf("websocket dial failed: %w", firstErr(dialCtx.Err(), lastErr))
		}
		timer.Stop()
		backoff = min(backoff*2, 1*time.Second)
	}
}

func firstErr(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}
