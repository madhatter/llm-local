#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
YAML="$SCRIPT_DIR/../models.yaml"

download_model() {
    local name="$1"
    local repo include local_dir
    repo=$(yq ".models[] | select(.name == \"$name\") | .repo" "$YAML")
    include=$(yq ".models[] | select(.name == \"$name\") | .include" "$YAML")
    local_dir=$(yq ".models[] | select(.name == \"$name\") | .local_dir" "$YAML")
    local_dir="${local_dir/#\~/$HOME}"

    echo "Downloading $name from $repo ..."
    hf download "$repo" --include "$include" --local-dir "$local_dir"
}

if [[ $# -gt 0 ]]; then
    download_model "$1"
else
    count=$(yq '.models | length' "$YAML")
    for i in $(seq 0 $((count - 1))); do
        download_model "$(yq ".models[$i].name" "$YAML")"
    done
fi
