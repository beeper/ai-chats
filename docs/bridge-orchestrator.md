# AgentRemote CLI

`./tools/bridges` is the local entrypoint for `agentremote`.

It wraps:

```bash
go run -tags goolm ./cmd/agentremote ...
```

Set `AGENTREMOTE_CRYPTO_BACKEND=libolm` to opt back into the native `libolm` backend for source builds.

## Authentication

Use one of:

- `./tools/bridges login --env prod`
- `./tools/bridges auth set-token --token syt_... --env prod`
- `./tools/bridges whoami`

Profiles default to `default`.

## Bridge lifecycle

- `./tools/bridges list`
- `./tools/bridges run <bridge>`
- `./tools/bridges up <bridge>`
- `./tools/bridges start <bridge>`
- `./tools/bridges stop <instance>`
- `./tools/bridges down <instance>`
- `./tools/bridges restart <bridge>`
- `./tools/bridges delete [instance]`

`up` is an alias of `start`. `down` is an alias of `stop`.

## Inspection

- `./tools/bridges status [instance...]`
- `./tools/bridges instances`
- `./tools/bridges logs <instance> --follow`
- `./tools/bridges doctor`

## Setup helpers

- `./tools/bridges init <bridge>`
- `./tools/bridges register <bridge>`
- `./tools/bridges completion <bash|zsh|fish>`

## Quick examples

```bash
./tools/bridges login --env prod
./tools/bridges up codex --wait
./tools/bridges status codex
./tools/bridges logs codex --follow
```

Local instance data is stored under:

```text
~/.config/agentremote/profiles/<profile>/instances/<instance>/
```

Remote self-hosted bridge registrations are device-scoped per profile:

```text
sh-<device-id>-<bridge>
sh-<device-id>-<bridge>-<name>
```
