package codexrpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func startHelper(t *testing.T, extraEnv ...string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	env := append([]string{"GO_WANT_CODEXRPC_HELPER=1"}, extraEnv...)
	c, err := StartProcess(ctx, ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexRPC_HelperProcess", "--"},
		Env:     env,
	})
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClient_CallResponseCorrelation(t *testing.T) {
	c := startHelper(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	var gotFast, gotSlow struct {
		OK   bool   `json:"ok"`
		Name string `json:"name"`
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := c.Call(ctx, "fast", map[string]any{"name": "fast"}, &gotFast); err != nil {
			t.Errorf("fast Call: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := c.Call(ctx, "slow", map[string]any{"name": "slow"}, &gotSlow); err != nil {
			t.Errorf("slow Call: %v", err)
		}
	}()
	wg.Wait()

	if !gotFast.OK || gotFast.Name != "fast" {
		t.Fatalf("unexpected fast result: %+v", gotFast)
	}
	if !gotSlow.OK || gotSlow.Name != "slow" {
		t.Fatalf("unexpected slow result: %+v", gotSlow)
	}
}

func TestClient_Initialize_IgnoresInvalidJSON(t *testing.T) {
	c := startHelper(t, "HELPER_WRITE_INVALID_JSON=1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	ua, err := c.Initialize(ctx, ClientInfo{Name: "t", Version: "0.0.0"}, false)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if strings.TrimSpace(ua) == "" {
		t.Fatalf("expected non-empty userAgent")
	}
}

func TestClient_PartialLineResponse(t *testing.T) {
	c := startHelper(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.Call(ctx, "partial", nil, &out); err != nil {
		t.Fatalf("Call(partial): %v", err)
	}
	if !out.OK {
		t.Fatalf("expected ok=true")
	}
}

func TestClient_ServerInitiatedRequestHandling(t *testing.T) {
	c := startHelper(t)

	c.HandleRequest("srv/request", func(ctx context.Context, req Request) (any, *RPCError) {
		var p struct {
			X int `json:"x"`
		}
		_ = json.Unmarshal(req.Params, &p)
		return map[string]any{"accepted": true, "x": p.X}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.Call(ctx, "trigger_server_request", nil, &out); err != nil {
		t.Fatalf("Call(trigger_server_request): %v", err)
	}
	if !out.OK {
		t.Fatalf("expected ok=true")
	}
}

func TestClient_ProcessExitFailsPendingCallWithoutDeadline(t *testing.T) {
	c := startHelper(t)

	done := make(chan error, 1)
	go func() {
		var out map[string]any
		done <- c.Call(context.Background(), "exit_without_response", nil, &out)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected error")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("call hung; expected pending call to be failed on process exit")
	}
}

func TestClient_CloseConcurrentWithWriteDoesNotPanic(t *testing.T) {
	c := startHelper(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Notify(ctx, "ping", map[string]any{"ok": true})
		}()
	}
	_ = c.Close()
	wg.Wait()
}

// Helper process: speaks JSONL over stdin/stdout.
//
// Protocol (minimal):
// - Requests: {"id":<num>,"method":<string>,"params":...}
// - Notifications: {"method":<string>,"params":...}
// - Responses: {"id":<num>,"result":...} (no "method")
func TestCodexRPC_HelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CODEXRPC_HELPER") != "1" {
		return
	}
	defer os.Exit(0)

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	if os.Getenv("HELPER_WRITE_INVALID_JSON") == "1" {
		_, _ = w.WriteString("not-json\n")
		_ = w.Flush()
	}

	encMu := sync.Mutex{}
	writeJSONL := func(v any, partial bool) {
		encMu.Lock()
		defer encMu.Unlock()
		b, _ := json.Marshal(v)
		if partial && len(b) > 2 {
			// Write in two chunks without newline in between to simulate partial pipe reads.
			mid := len(b) / 2
			_, _ = w.Write(b[:mid])
			_ = w.Flush()
			time.Sleep(10 * time.Millisecond)
			_, _ = w.Write(b[mid:])
			_, _ = w.WriteString("\n")
			_ = w.Flush()
			return
		}
		_, _ = w.Write(b)
		_, _ = w.WriteString("\n")
		_ = w.Flush()
	}

	r := bufio.NewReader(os.Stdin)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var probe map[string]json.RawMessage
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe["method"] == nil {
			// Response to a server-initiated request we sent.
			continue
		}

		var method string
		_ = json.Unmarshal(probe["method"], &method)
		if probe["id"] == nil {
			// Notification from client, ignore.
			continue
		}

		var id any
		_ = json.Unmarshal(probe["id"], &id)

		switch method {
		case "initialize":
			writeJSONL(map[string]any{"id": id, "result": map[string]any{"userAgent": "helper-agent/0.0.0"}}, false)
		case "fast":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(probe["params"], &p)
			writeJSONL(map[string]any{"id": id, "result": map[string]any{"ok": true, "name": p.Name}}, false)
		case "slow":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(probe["params"], &p)
			go func(id any, name string) {
				time.Sleep(80 * time.Millisecond)
				writeJSONL(map[string]any{"id": id, "result": map[string]any{"ok": true, "name": name}}, false)
			}(id, p.Name)
		case "partial":
			writeJSONL(map[string]any{"id": id, "result": map[string]any{"ok": true}}, true)
		case "trigger_server_request":
			// Emit a server-initiated request, then wait until the client responds, then finish.
			srvID := 999
			writeJSONL(map[string]any{"id": srvID, "method": "srv/request", "params": map[string]any{"x": 7}}, false)

			// Wait for response id=999.
			for {
				l2, err := r.ReadBytes('\n')
				if err != nil {
					return
				}
				l2 = bytes.TrimSpace(l2)
				if len(l2) == 0 {
					continue
				}
				var p2 map[string]json.RawMessage
				if err := json.Unmarshal(l2, &p2); err != nil {
					continue
				}
				if p2["id"] == nil || p2["method"] != nil {
					continue
				}
				var rid int
				_ = json.Unmarshal(p2["id"], &rid)
				if rid != srvID {
					continue
				}
				break
			}

			writeJSONL(map[string]any{"id": id, "result": map[string]any{"ok": true}}, false)
		case "exit_without_response":
			// Simulate a crash/exit before responding. The client should not hang forever.
			os.Exit(0)
		default:
			writeJSONL(map[string]any{"id": id, "result": map[string]any{"ok": true}}, false)
		}
	}
}
