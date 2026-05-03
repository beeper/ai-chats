package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3/responses"

	"github.com/beeper/ai-chats/pkg/shared/jsonutil"
	"github.com/beeper/ai-chats/sdk"
)

type toolLifecycle struct {
	state *streamingState
}

func newToolLifecycle(state *streamingState) toolLifecycle {
	return toolLifecycle{state: state}
}

func (l toolLifecycle) ensureInputStart(ctx context.Context, tool *activeToolCall, providerExecuted bool, extra map[string]any) {
	if tool == nil {
		return
	}
	l.state.writer().Tools().EnsureInputStart(ctx, tool.callID, nil, sdk.ToolInputOptions{
		ToolName:         tool.toolName,
		ProviderExecuted: providerExecuted,
		DisplayTitle:     toolDisplayTitle(tool.toolName),
		Extra:            extra,
	})
}

func (l toolLifecycle) appendInputDelta(ctx context.Context, tool *activeToolCall, toolName, delta string, providerExecuted bool) {
	if tool == nil {
		return
	}
	tool.input.WriteString(delta)
	l.state.writer().Tools().InputDelta(ctx, tool.callID, toolName, delta, providerExecuted)
}

func (l toolLifecycle) emitInput(ctx context.Context, tool *activeToolCall, toolName string, input any, providerExecuted bool) {
	if tool == nil {
		return
	}
	l.state.writer().Tools().Input(ctx, tool.callID, toolName, input, providerExecuted)
}

type toolFinalizeOptions struct {
	providerExecuted bool
	status           ToolStatus
	resultStatus     ResultStatus
	errorText        string
	output           any
	outputMap        map[string]any
	input            map[string]any
	streaming        bool
}

func (l toolLifecycle) finalize(ctx context.Context, tool *activeToolCall, opts toolFinalizeOptions) {
	if tool == nil {
		return
	}
	switch opts.resultStatus {
	case ResultStatusDenied:
		l.state.writer().Tools().Denied(ctx, tool.callID)
	case ResultStatusError:
		l.state.writer().Tools().OutputError(ctx, tool.callID, opts.errorText, opts.providerExecuted)
	default:
		l.state.writer().Tools().Output(ctx, tool.callID, opts.output, sdk.ToolOutputOptions{
			ProviderExecuted: opts.providerExecuted,
			Streaming:        opts.streaming,
		})
	}

	outputMap := opts.outputMap
	if outputMap == nil {
		outputMap = outputMapFromResult(opts.output, opts.errorText, opts.resultStatus)
	}
	recordToolCallResult(l.state, tool, opts.status, opts.resultStatus, opts.errorText, outputMap, opts.input)
}

func (l toolLifecycle) fail(ctx context.Context, tool *activeToolCall, providerExecuted bool, resultStatus ResultStatus, errorText string, input map[string]any) {
	l.finalize(ctx, tool, toolFinalizeOptions{
		providerExecuted: providerExecuted,
		status:           ToolStatusFailed,
		resultStatus:     resultStatus,
		errorText:        errorText,
		input:            input,
	})
}

func (l toolLifecycle) succeed(ctx context.Context, tool *activeToolCall, providerExecuted bool, output any, outputMap map[string]any, input map[string]any) {
	l.finalize(ctx, tool, toolFinalizeOptions{
		providerExecuted: providerExecuted,
		status:           ToolStatusCompleted,
		resultStatus:     ResultStatusSuccess,
		output:           output,
		outputMap:        outputMap,
		input:            input,
	})
}

func (l toolLifecycle) completeResult(
	ctx context.Context,
	tool *activeToolCall,
	providerExecuted bool,
	resultStatus ResultStatus,
	errorText string,
	output any,
	outputMap map[string]any,
	input map[string]any,
) {
	if resultStatus == ResultStatusSuccess {
		l.succeed(ctx, tool, providerExecuted, output, outputMap, input)
		return
	}
	l.fail(ctx, tool, providerExecuted, resultStatus, errorText, input)
}

func (l toolLifecycle) completeFromResponseItem(ctx context.Context, tool *activeToolCall, item responses.ResponseOutputItemUnion) {
	if tool == nil {
		return
	}
	result := responseOutputItemResultPayload(item)
	resultStatus := ResultStatusSuccess
	errorText := strings.TrimSpace(item.Error)
	statusText := strings.ToLower(strings.TrimSpace(item.Status))
	switch {
	case outputItemLooksDenied(item):
		resultStatus = ResultStatusDenied
	case statusText == "failed" || statusText == "incomplete" || errorText != "":
		if errorText == "" {
			errorText = fmt.Sprintf("%s failed", tool.toolName)
		}
		resultStatus = ResultStatusError
	}
	l.completeResult(
		ctx,
		tool,
		true,
		resultStatus,
		errorText,
		result,
		nil,
		parseToolInputPayload(tool.input.String()),
	)
}

func outputMapFromResult(result any, errorText string, resultStatus ResultStatus) map[string]any {
	switch resultStatus {
	case ResultStatusDenied:
		return map[string]any{"status": "denied"}
	case ResultStatusError:
		if strings.TrimSpace(errorText) != "" {
			return map[string]any{"error": errorText}
		}
	}
	if converted := jsonutil.ToMap(result); len(converted) > 0 {
		return converted
	}
	if result != nil {
		return map[string]any{"result": result}
	}
	return map[string]any{}
}
