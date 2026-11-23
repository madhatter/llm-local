# llm-local

Local LLM setup using llama.cpp with NVIDIA CUDA.

## Dependencies

- `llama.cpp-cuda` (AUR: `yay -S llama.cpp-cuda`)
- `yq` (AUR: `yay -S go-yq`)
- `huggingface-hub` (AUR: `yay -S python-huggingface-hub`)
- `jq` (`pacman -S jq`), used by `serve.sh` to merge the OS overlay
- `go` (`pacman -S go`), to build the orchestrator

On macOS the same tools are available via Homebrew.

## Usage

Download a specific model:

    ./scripts/download.sh qwen35-9b

Download all models:

    ./scripts/download.sh

Start the default model:

    ./scripts/serve.sh

Start a specific model:

    ./scripts/serve.sh qwen3-coder-30b

## Models

Models are defined in `models.yaml` with their HuggingFace source and server parameters.
The `~/models/` directory is not tracked in git.

### OS overlays

`config/linux.yaml` and `config/darwin.yaml` override `serve` settings per OS,
matched by model `name` (e.g. no CPU MoE offloading on Apple Silicon with
unified memory). Both `serve.sh` and the orchestrator pick the overlay based on
the current OS. The orchestrator reads them only at startup, so restart it
after changes:

    systemctl --user restart llm-orchestrator

### Running alongside the desktop (Linux)

The GPU (16 GB) is shared with X and the desktop, which use about 2 GB. The
MoE models therefore keep part of their expert layers on the CPU
(`n_cpu_moe`) so they stay around 12-13 GB of VRAM and leave headroom.
The dense `qwen38-27b` does not fit next to the desktop with a useful
context size and is meant for headless use.

To measure VRAM use, load time and generation speed of a setting without
touching the orchestrator:

    ./scripts/vram_probe.sh <label> <llama-server args...>

Run it without arguments for an example.

For gaming, GameMode stops the orchestrator while a game runs
(`~/.config/gamemode.ini`, games started with `gamemoderun %command%`):

```ini
[custom]
start=systemctl --user stop llm-orchestrator.service
end=systemctl --user start llm-orchestrator.service
```

## Orchestrator

Minimal orchestrator written in Go to manage multiple LLM instances on limited
VRAM and route requests to the appropriate model based on the request url.

### Build

    cd scripts
    go build -o llm_orchestrator .

Restart the service afterwards to pick up the new binary.

### Use

The model is selected by the URL path: `http://<host>:8082/<model>/v1`, where
`<model>` is the `name` from `models.yaml`. Only one model is loaded at a time;
requesting another one swaps it, which takes a while for large models.

## opencode

- `config/opencode_orchestrator.json`: providers for all models via the
  orchestrator (port 8082). This is the one in daily use.
- `config/opencode.json`: older setup with one fixed port per model
  (`serve.sh`).

The file only holds the providers and the default model. It is merged into the
global opencode config via `OPENCODE_CONFIG`, e.g. in `.zshrc`:

    export OPENCODE_CONFIG="$HOME/code/llm-local/config/opencode_orchestrator.json"

Keep the default model on one that fits next to the desktop, since new
sessions start with it.

## Systemd Services

The `systemd/` directory holds user-level systemd units for this setup.
Install with:

    cp systemd/*.service ~/.config/systemd/user/
    systemctl --user daemon-reload
    systemctl --user enable --now llm-orchestrator

Enable lingering so user services start at boot without a login session:

    sudo loginctl enable-linger $USER

### llm-orchestrator.service

Runs the Go orchestrator (`scripts/llm_orchestrator`) as a user service.
It lazily starts `llama-server` for whichever model is requested via
`:8082/<model>/v1`, so no model is loaded until needed and VRAM stays
free otherwise. Switch models at runtime with:

    curl -X POST localhost:8082/switch/<model-name>
    curl localhost:8082/status

Logs: `journalctl --user -u llm-orchestrator`

## Docker Services

The `docker/` directory contains supporting services that run alongside the
LLM servers: `open-webui` (chat UI), `mcpo` (MCP-to-OpenAPI proxy for
Context7), and `searxng` (web search).

Start all services:

    cd docker
    docker compose up -d

Start a single service:

    cd docker
    docker compose up -d searxng

### SearXNG (web search)

Self-hosted meta-search engine. Used as an MCP web-search backend for
opencode, avoiding third-party search API quotas.

Config lives in `docker/searxng/settings.yml`. Two settings are required for
MCP/API clients (e.g. `mcp-searxng`) to get usable results, since SearXNG
defaults to HTML-only output for browser use:

```yaml
search:
  formats:
    - html
    - json

server:
  limiter: false
```

Once running, it's reachable at `http://<host>:8888`. Point an MCP client at
it via the `mcp` block in `config/opencode.json` / `config/opencode_orchestrator.json`:

```json
"mcp": {
  "searxng": {
    "type": "local",
    "command": ["npx", "-y", "mcp-searxng"],
    "enabled": true,
    "environment": {
      "SEARXNG_URL": "http://<host>:8888"
    }
  }
}
```

Use the host's LAN IP for `SEARXNG_URL` when opencode runs on a different
machine than the Docker host.
