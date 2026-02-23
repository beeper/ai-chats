package memory

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
)

const (
	DefaultFlushToolLoopTimeout = 2 * time.Minute
	DefaultFlushToolLoopTurns   = 6
)

type ModelToolCall struct {
	ID       string
	Name     string
	ArgsJSON string
}

type FlushToolLoopDeps struct {
	TimeoutMs int64
	MaxTurns  int

	NextTurn func(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion) (
		assistant openai.ChatCompletionMessageParamUnion,
		calls []ModelToolCall,
		done bool,
		err error,
	)
	ExecuteTool func(ctx context.Context, name string, argsJSON string) (string, error)
	OnToolError func(name string, err error)
}

func RunFlushToolLoop(
	ctx context.Context,
	model string,
	messages []openai.ChatCompletionMessageParamUnion,
	deps FlushToolLoopDeps,
) error {
	if deps.NextTurn == nil || deps.ExecuteTool == nil {
		return errors.New("memory flush unavailable")
	}
	timeout := DefaultFlushToolLoopTimeout
	if deps.TimeoutMs > 0 {
		timeout = time.Duration(deps.TimeoutMs) * time.Millisecond
	}
	maxTurns := DefaultFlushToolLoopTurns
	if deps.MaxTurns > 0 {
		maxTurns = deps.MaxTurns
	}
	flushCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	chat := append([]openai.ChatCompletionMessageParamUnion{}, messages...)
	for range maxTurns {
		assistant, calls, done, err := deps.NextTurn(flushCtx, model, chat)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		chat = append(chat, assistant)
		if len(calls) == 0 {
			return nil
		}
		for _, call := range calls {
			name := strings.TrimSpace(call.Name)
			args := call.ArgsJSON
			result := ""
			var execErr error
			if name == "" {
				execErr = errors.New("missing tool name")
			} else {
				result, execErr = deps.ExecuteTool(flushCtx, name, args)
			}
			if execErr != nil {
				if deps.OnToolError != nil {
					deps.OnToolError(name, execErr)
				}
				result = "Error: " + execErr.Error()
			}
			chat = append(chat, openai.ToolMessage(result, call.ID))
		}
	}
	return nil
}

func IsAllowedFlushTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "read", "write", "edit":
		return true
	default:
		return false
	}
}
