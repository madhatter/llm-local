# llm-local

Local LLM setup using llama.cpp with NVIDIA CUDA.

## Dependencies

- `llama.cpp-cuda` (AUR: `yay -S llama.cpp-cuda`)
- `yq` (AUR: `yay -S go-yq`)
- `huggingface-hub` (AUR: `yay -S python-huggingface-hub`)

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

## Orchestrator

Minimal orchestator written in Go to manage multiple LLM instances on limited
VRAM and route requests to the appropriate model based on the request url.

### Build
```
cd scripts
go mod init llm_orchestrator
go mod tidy
go build -o llm_orchestrator .
./llm_orchestrator &
```

### Use
Have a look at the `config/opencode_orchestrator.json` file for an example
configuration. The `model_name` field should match the model name in `models.yaml`.

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
