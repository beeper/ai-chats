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
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
)

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
