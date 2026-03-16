# AgentRemote

AgentRemote brings all your agents into one app.

Beeper becomes the universal remote for agents.

Connect agent runtimes to Beeper with full history, live streaming, tool approvals, and encrypted delivery.

Run the bridge next to your agent, then talk to it from Beeper on your phone or desktop.

## Why Use It

- Keep agents on your own machine, server, or private network
- Use Beeper instead of building a separate web UI
- Stream responses and approve tool calls in the same chat
- Reach your agents from anywhere Beeper runs

## Open Source Focus

This repository is centered on the self-hosted path.

That means:

- local developer machines
- homelabs
- office servers
- runtimes behind a firewall
- private deployments that still want a polished remote interface

There is a broader product direction around richer AI chats and more opinionated agent experiences. Open source here is focused on making the bridge layer for private deployments easy to run and hard to break.

## AgentRemote SDK

If you want to build your own bridge, start with the SDK in [`sdk/`](./sdk).

The SDK handles the Matrix and Beeper side of the bridge for you:

- bridge bootstrapping and registration
- room and conversation wrappers
- streaming turn lifecycle
- tool approval UI
- agent identity and capability metadata

The main entrypoint is `sdk.New(sdk.Config{...})`.

In practice, most custom bridges only need three things:

- an `sdk.Agent` that represents the remote assistant in Beeper
- an `OnConnect` hook that builds whatever runtime client you need
- an `OnMessage` hook that turns an incoming Beeper message into model output

### Minimal SDK Shape

This is the smallest useful shape of a bridge:

```go
bridge := sdk.New(sdk.Config{
	Name: "my-bridge",
	Agent: &sdk.Agent{
		ID:           "my-agent",
		Name:         "My Agent",
		Description:  "A custom agent exposed through Beeper",
		ModelKey:     "openai/gpt-5-mini",
		Capabilities: sdk.BaseAgentCapabilities(),
	},
	OnConnect: func(ctx context.Context, login *sdk.LoginInfo) (any, error) {
		return newRuntimeClient(), nil
	},
	OnMessage: func(session any, conv *sdk.Conversation, msg *sdk.Message, turn *sdk.Turn) error {
		turn.WriteText("hello from my bridge")
		turn.End("stop")
		return nil
	},
})

bridge.Run()
```

`turn` is the important piece here. You can write text and reasoning deltas into it, request approvals, attach sources/files, and then finalize the message with `turn.End(...)` or `turn.EndWithError(...)`.

### Simple OpenAI SDK Bridge

The example below is intentionally minimal. It uses the Go OpenAI SDK directly and lets AgentRemote handle the chat room, sender identity, and message lifecycle.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/beeper/agentremote/sdk"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func main() {
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}

	bridge := sdk.New(sdk.Config{
		Name:        "openai-simple",
		Description: "A minimal OpenAI-backed AgentRemote bridge",
		Agent: &sdk.Agent{
			ID:           "openai-simple-agent",
			Name:         "OpenAI Simple",
			Description:  "Minimal bridge example using openai-go",
			ModelKey:     "openai/gpt-4o-mini",
			Capabilities: sdk.BaseAgentCapabilities(),
		},
		OnConnect: func(ctx context.Context, login *sdk.LoginInfo) (any, error) {
			return openai.NewClient(option.WithAPIKey(os.Getenv("OPENAI_API_KEY"))), nil
		},
		OnMessage: func(session any, conv *sdk.Conversation, msg *sdk.Message, turn *sdk.Turn) error {
			client := session.(*openai.Client)

			resp, err := client.Chat.Completions.New(turn.Context(), openai.ChatCompletionNewParams{
				Model: "gpt-4o-mini",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.SystemMessage("You are a helpful assistant replying through Beeper."),
					openai.UserMessage(msg.Text),
				},
			})
			if err != nil {
				turn.EndWithError(err.Error())
				return err
			}
			if len(resp.Choices) == 0 {
				err := fmt.Errorf("openai returned no choices")
				turn.EndWithError(err.Error())
				return err
			}

			turn.WriteText(resp.Choices[0].Message.Content)
			turn.End(resp.Choices[0].FinishReason)
			return nil
		},
	})

	bridge.Run()
}
```

Useful details from that example:

- `OnConnect` returns the session object that will be passed back into every `OnMessage` call.
- `sdk.Message` already gives you the normalized incoming Beeper message text.
- `sdk.Turn` is where you stream or finalize the assistant reply.
- If you want live token streaming later, switch the OpenAI call to `client.Chat.Completions.NewStreaming(...)` or `client.Responses.NewStreaming(...)` and forward deltas with `turn.WriteText(...)`.

## Included Bridges

Each bridge has its own README with setup details and scope:

| Bridge | Purpose |
| --- | --- |
| `ai` | AI Chats bridge surface used by the project |
| [`codex`](./bridges/codex/README.md) | Connect the Codex CLI app-server to Beeper |
| [`openclaw`](./bridges/openclaw/README.md) | Connect a self-hosted OpenClaw gateway to Beeper |
| [`opencode`](./bridges/opencode/README.md) | Connect a self-hosted OpenCode server to Beeper |

## Quick Start

Log into Beeper and start a bridge:

```bash
./tools/bridges login --env prod
./tools/bridges run codex
```

Then open Beeper and use the connected bridge from chat.

For a local Beeper environment:

```bash
./tools/bridges login --env local
./tools/bridges whoami
./tools/bridges run codex
```

Configured instances live under `~/.config/agentremote/profiles/<profile>/instances/`:

- `ai`
- `codex`
- `openclaw`
- `opencode`

Run any of them directly:

```bash
./tools/bridges run ai
./tools/bridges run codex
./tools/bridges run openclaw
./tools/bridges run opencode
```

Or use the wrapper:

```bash
./run.sh ai
./run.sh codex
./run.sh openclaw
./run.sh opencode
```

## Bridge Manager

Common commands:

```bash
./tools/bridges list
./tools/bridges status
./tools/bridges logs codex --follow
./tools/bridges restart codex
./tools/bridges down codex
./tools/bridges whoami
```

Reset all local bridge state and registrations:

```bash
./tools/bridges delete --remote ai
./tools/bridges delete --remote codex
./tools/bridges delete --remote openclaw
./tools/bridges delete --remote opencode
./tools/bridges logout
```

## Docs

- [`docs/bridge-orchestrator.md`](./docs/bridge-orchestrator.md): local bridge management workflow
- [`docs/matrix-ai-matrix-spec-v1.md`](./docs/matrix-ai-matrix-spec-v1.md): Matrix transport profile for streaming, approvals, state, and AI payloads
- [`bridges/codex/README.md`](./bridges/codex/README.md): Codex bridge details
- [`bridges/openclaw/README.md`](./bridges/openclaw/README.md): OpenClaw bridge details
- [`bridges/opencode/README.md`](./bridges/opencode/README.md): OpenCode bridge details

## Status

Experimental and evolving quickly. The transport and bridge surfaces are real, but the project is still early.

## Build

Requires `libolm` for encryption support.

```bash
./build.sh
```

Or with Docker:

```bash
docker build -t agentremote .
```
