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
