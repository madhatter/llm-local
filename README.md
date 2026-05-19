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
