package codexrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
