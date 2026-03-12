package opencode

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/managedruntime"
)

type managedOpenCodeProcess struct {
	managedruntime.Process
	url string
}

func (m *OpenCodeManager) spawnManagedProcess(ctx context.Context, cfg *OpenCodeInstance, workingDir string) (*managedOpenCodeProcess, error) {
	if cfg == nil {
		return nil, errors.New("managed opencode config is required")
	}
	binaryPath := strings.TrimSpace(cfg.BinaryPath)
	if binaryPath == "" {
		return nil, errors.New("managed opencode binary path is missing")
	}
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return nil, errors.New("managed opencode working directory is missing")
	}
	baseURL, err := managedruntime.AllocateLoopbackHTTPURL()
	if err != nil {
		return nil, err
	}
	client, err := api.NewClient(baseURL, "", "")
	if err != nil {
		return nil, err
	}
	port := strings.TrimPrefix(baseURL, "http://127.0.0.1:")
	cmd := exec.CommandContext(ctx, binaryPath, "serve", "--hostname", "127.0.0.1", "--port", port)
	cmd.Dir = workingDir
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			m.log().Debug().
				Str("instance", cfg.ID).
				Str("workdir", workingDir).
				Msg(scanner.Text())
		}
	}()
	dead := make(chan error, 1)
	go func() {
		dead <- cmd.Wait()
	}()
	readyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err = managedruntime.WaitForReady(readyCtx, 250*time.Millisecond, dead, func(checkCtx context.Context) error {
		_, checkErr := client.ListSessions(checkCtx)
		return checkErr
	})
	if err != nil {
		_ = cmd.Process.Kill()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("managed opencode did not become ready: %w", err)
		}
		return nil, err
	}
	return &managedOpenCodeProcess{Process: managedruntime.Process{Cmd: cmd}, url: baseURL}, nil
}
