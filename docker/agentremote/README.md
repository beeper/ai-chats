# AgentRemote Manager Docker Image

The AgentRemote Manager container packages the `agentremote` CLI for Linux `amd64` and `arm64`.

The image stores CLI state under `/data` by setting `HOME=/data`, so mounting a host directory preserves profiles, auth, and bridge instance state.

## Usage

```sh
docker run --rm -it \
  -v "$(pwd):/data" \
  ghcr.io/beeper/agentremote:latest help
```

Run a bridge command with persisted state:

```sh
docker run --rm -it \
  -v "$(pwd):/data" \
  ghcr.io/beeper/agentremote:latest run ai
```
