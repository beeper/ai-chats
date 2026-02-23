# Bridge Orchestrator

`tools/bridges` manages isolated bridgev2 instances for Beeper from this repo.

It supports bridge-manager-style top-level commands too: `login`, `logout`, `whoami`, `run`, `config`, `register`, `delete`.

## Auth

Use one of:

- `./tools/bridges login --env prod` (email+code flow)
- `./tools/bridges auth set-token --token syt_... --env prod`
- Environment variables: `BEEPER_ACCESS_TOKEN`, optional `BEEPER_ENV`, `BEEPER_USERNAME`

## One-command startup

```bash
./tools/bridges up ai
```

This will:

1. Create isolated instance state under `~/.local/share/ai-bridge-manager/instances/<instance>/`
2. Build the bridge via manifest `build_cmd`
3. Generate config from bridge binary (`-e`) if needed
4. Ensure Beeper appservice registration and sync config tokens
5. Start bridge process and write PID/log files

## Core commands

- `./tools/bridges list`
- `./tools/bridges login`
- `./tools/bridges logout`
- `./tools/bridges whoami [--raw]`
- `./tools/bridges run <instance>` (alias to `up`)
- `./tools/bridges config <instance> [--output ...]`
- `./tools/bridges init <instance>`
- `./tools/bridges register <instance>`
- `./tools/bridges up <instance>`
- `./tools/bridges down <instance>`
- `./tools/bridges restart <instance>`
- `./tools/bridges status [instance]`
- `./tools/bridges logs <instance> [--follow]`
- `./tools/bridges delete <instance> [--remote]`
- `./tools/bridges doctor`

Shortcut wrapper:

- `./run.sh ai|codex|opencode`
  - checks login and prompts with `login` if needed
  - then runs the selected bridge instance

## Manifest

Instances are configured in `bridges.manifest.yml`.

Key fields:

- `bridge_type`
- `repo_path`
- `build_cmd`
- `binary_path`
- `beeper_bridge_name`
- `config_overrides` (dot-path override map)
