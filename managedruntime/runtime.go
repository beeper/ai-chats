package managedruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"time"
)

func AllocateLoopbackURL(scheme string) (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate loopback %s listener: %w", scheme, err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	if !ok || addr == nil || addr.Port == 0 {
		return "", fmt.Errorf("allocate loopback %s listener: missing TCP port", scheme)
	}
	return fmt.Sprintf("%s://127.0.0.1:%d", scheme, addr.Port), nil
}

func AllocateLoopbackHTTPURL() (string, error) {
	return AllocateLoopbackURL("http")
}

func AllocateLoopbackWebSocketURL() (string, error) {
	return AllocateLoopbackURL("ws")
}

type Process struct {
	Cmd *exec.Cmd
}

func (p *Process) Close() error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}
	_ = p.Cmd.Process.Kill()
	_, _ = p.Cmd.Process.Wait()
	return nil
}

func WaitForReady(ctx context.Context, pollEvery time.Duration, dead <-chan error, check func(context.Context) error) error {
	if check == nil {
		return errors.New("readiness check is required")
	}
	if pollEvery <= 0 {
		pollEvery = 250 * time.Millisecond
	}
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		if err := check(ctx); err == nil {
			return nil
		}
		select {
		case waitErr := <-dead:
			if waitErr == nil {
				waitErr = errors.New("process exited before becoming ready")
			}
			return waitErr
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
