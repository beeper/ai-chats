package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

type toolLifecycle struct {
	oc     *AIClient
	portal *bridgev2.Portal
	state  *streamingState
}

func (oc *AIClient) toolLifecycle(portal *bridgev2.Portal, state *streamingState) toolLifecycle {
	return toolLifecycle{
		oc:     oc,
		portal: portal,
		state:  state,
	}
}

func (l toolLifecycle) ensureInputStart(ctx context.Context, tool *activeToolCall, providerExecuted bool, extra map[string]any) {
	if tool == nil {
		return
	}
	l.oc.writer(l.state, l.portal).Tools().EnsureInputStart(ctx, tool.callID, nil, bridgesdk.ToolInputOptions{
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
	l.oc.writer(l.state, l.portal).Tools().InputDelta(ctx, tool.callID, toolName, delta, providerExecuted)
}

func (l toolLifecycle) emitInput(ctx context.Context, tool *activeToolCall, toolName string, input any, providerExecuted bool) {
	if tool == nil {
		return
	}
	l.oc.writer(l.state, l.portal).Tools().Input(ctx, tool.callID, toolName, input, providerExecuted)
}

func (l toolLifecycle) emitInputError(ctx context.Context, tool *activeToolCall, toolName, rawInput, errText string, providerExecuted bool) {
	if tool == nil {
		return
	}
	l.oc.writer(l.state, l.portal).Tools().InputError(ctx, tool.callID, toolName, rawInput, errText, providerExecuted)
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
		l.oc.writer(l.state, l.portal).Tools().Denied(ctx, tool.callID)
	case ResultStatusError:
		l.oc.writer(l.state, l.portal).Tools().OutputError(ctx, tool.callID, opts.errorText, opts.providerExecuted)
	default:
		l.oc.writer(l.state, l.portal).Tools().Output(ctx, tool.callID, opts.output, bridgesdk.ToolOutputOptions{
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

func (l toolLifecycle) respondApproval(ctx context.Context, approvalID, toolCallID string, approved bool, reason string) {
	l.oc.writer(l.state, l.portal).Approvals().Respond(ctx, approvalID, toolCallID, approved, reason)
	if !approved {
		l.oc.writer(l.state, l.portal).Tools().Denied(ctx, toolCallID)
	}
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
