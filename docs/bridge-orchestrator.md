# Bridge Orchestrator

`tools/bridges` is a thin wrapper around `agentremote`, which manages isolated bridgev2 instances for Beeper from this repo.

## Auth

Use one of:

- `./tools/bridges login --env prod` for the email and code flow
- `./tools/bridges auth set-token --token syt_... --env prod`
- Environment variables: `BEEPER_ACCESS_TOKEN`, optional `BEEPER_ENV`, `BEEPER_USERNAME`

## One-command startup

```bash
./tools/bridges up ai
```

This will:

1. Create instance state under `~/.config/agentremote/profiles/default/instances/<instance>/`
2. Generate config from the bridge binary with `-e` if needed
3. Ensure Beeper appservice registration and sync config tokens
4. Start the bridge process and write PID and log files

## Core commands

- `./tools/bridges list`
- `./tools/bridges login`
- `./tools/bridges logout`
- `./tools/bridges whoami [--output json]`
- `./tools/bridges profiles`
- `./tools/bridges up <bridge>`
- `./tools/bridges start <bridge>`
- `./tools/bridges run <bridge>`
- `./tools/bridges init <bridge>`
- `./tools/bridges register <bridge>`
- `./tools/bridges status [instance]`
- `./tools/bridges instances`
- `./tools/bridges logs <instance> [--follow]`
- `./tools/bridges down <instance>`
- `./tools/bridges stop <instance>`
- `./tools/bridges stop-all`
- `./tools/bridges restart <bridge>`
- `./tools/bridges delete [instance]`
- `./tools/bridges doctor`
- `./tools/bridges completion <bash|zsh|fish>`

Shortcut wrapper:

- `./run.sh ai|codex|opencode|openclaw`
  - checks login and prompts with `login` if needed
  - then runs the selected bridge instance
