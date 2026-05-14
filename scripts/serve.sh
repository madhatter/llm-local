#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
YAML="$SCRIPT_DIR/../models.yaml"

if [[ $# -eq 0 ]]; then
    name=$(yq '.models[] | select(.default == true) | .name' "$YAML")
    echo "No model specified, using default: $name"
else
    name="$1"
fi

get() { yq ".models[] | select(.name == \"$name\") | .serve.$1" "$YAML"; }

local_dir=$(yq ".models[] | select(.name == \"$name\") | .local_dir" "$YAML")
local_dir="${local_dir/#\~/$HOME}"
model_file=$(find "$local_dir" -name "*.gguf" | head -1)

if [[ -z "$model_file" ]]; then
    echo "No .gguf file found in $local_dir – run download.sh first"
    exit 1
fi

args=(
    --model "$model_file"
    --host "$(get host)"
    --port "$(get port)"
    -ngl "$(get n_gpu_layers)"
    --ctx-size "$(get ctx_size)"
    -t "$(get threads)"
    --temp "$(get temp)"
    --top-p "$(get top_p)"
    --repeat-penalty "$(get repeat_penalty)"
)

top_k=$(get top_k)
[[ "$top_k" != "null" ]] && args+=(--top-k "$top_k")

echo "Starting: $name ($model_file)"
exec llama-server "${args[@]}"
