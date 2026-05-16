#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
YAML="$SCRIPT_DIR/../models.yaml"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
OVERLAY="$SCRIPT_DIR/../config/${OS}.yaml"

# Initialize MERGED_CONFIG
MERGED_CONFIG="$YAML"

if [[ -f "$OVERLAY" ]]; then
    MERGED_CONFIG=$(mktemp)
    # Use yq to convert to JSON and jq to perform a robust merge by 'name' key
    jq -s '
      .[0] as $base | .[1] as $overlay |
      {
        models: (
          $base.models | map(
            . as $b | 
            ($overlay.models | map(select(.name == $b.name)) | first) as $o |
            if $o then ($b * $o) else $b end
          )
        )
      }
    ' <(yq -o=json "$YAML") <(yq -o=json "$OVERLAY") > "$MERGED_CONFIG"
    
    echo "Using merged config: $MERGED_CONFIG"
    trap 'rm -f "$MERGED_CONFIG"' EXIT
else
    echo "No overlay found for $OS, using base config."
fi

# Determine model name
if [[ $# -eq 0 ]]; then
    name=$(yq -r '.models[] | select(.default == true) | .name' "$MERGED_CONFIG")
    echo "No model specified, using default: $name"
else
    name="$1"
fi

# Helper function to get parameters from the merged config
get() {
    yq -r ".models[] | select(.name == \"$name\") | .serve.$1" "$MERGED_CONFIG"
}

# Find model file
local_dir=$(yq -r ".models[] | select(.name == \"$name\") | .local_dir" "$MERGED_CONFIG")
local_dir="${local_dir/#\~/$HOME}"
model_file=$(find "$local_dir" -name "*.gguf" | head -1)

if [[ -z "$model_file" ]]; then
    echo "No .gguf file found in $local_dir – run download.sh first"
    exit 1
fi

# Build arguments
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

cache_type_k=$(get cache_type_k)
[[ "$cache_type_k" != "null" ]] && args+=(--cache-type-k "$cache_type_k")

cache_type_v=$(get cache_type_v)
[[ "$cache_type_v" != "null" ]] && args+=(--cache-type-v "$cache_type_v")

flash_attn=$(get flash_attn)
[[ "$flash_attn" == "true" ]] && args+=(--flash-attn on)

jinja=$(get jinja)
[[ "$jinja" == "true" ]] && args+=(--jinja)

batch_size=$(get batch_size)
[[ "$batch_size" != "null" ]] && args+=(--batch-size "$batch_size")

ubatch_size=$(get ubatch_size)
[[ "$ubatch_size" != "null" ]] && args+=(--ubatch-size "$ubatch_size")

cont_batching=$(get cont_batching)
[[ "$cont_batching" == "true" ]] && args+=(--cont-batching)

mmap=$(get mmap)
[[ "$mmap" != "null" ]] && [[ "$mmap" == "false" ]] && args+=(--no-mmap)
[[ "$mmap" != "null" ]] && [[ "$mmap" == "true" ]] && args+=(--mmap)

echo "Starting: $name ($model_file)"
echo "Using command llama-server ${args[*]}"
exec llama-server "${args[@]}"
