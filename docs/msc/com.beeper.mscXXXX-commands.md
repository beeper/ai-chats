# MSC: AI Command Profile

Status: implemented for the AI bridge in this repo.

## Transport

Room state:

- `org.matrix.msc4391.command_description`

Structured invocation:

- `org.matrix.msc4391.command` inside `m.room.message`

When both structured data and plain text are present, the structured command wins.

## Built-in user-facing commands

The AI bridge currently publishes these stable user-facing commands:

| Command | Meaning |
| --- | --- |
| `new` | Create a new chat of the same type, optionally targeting an agent |
| `status` | Show current session status |
| `reset` | Start a new session or thread in the current room |
| `stop` | Abort the active run and clear the pending queue |

Integration modules may register more commands at runtime. Those are also broadcast through MSC4391 when available.

## Fallback

Clients without MSC4391 support can still send plain-text commands using the room command prefix.

The default command prefix is `!ai`.
