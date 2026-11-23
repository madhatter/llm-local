#!/usr/bin/env bash
# Start llama-server with the given args on a spare port, wait until it is
# ready or fails, report load time, VRAM use and generation speed, then stop it.
# Does not touch the orchestrator; stop it first if its model occupies the GPU.

if [[ $# -lt 2 ]]; then
    cat <<EOF
Usage: $(basename "$0") <label> <llama-server args...>

Example:
  $(basename "$0") qwen36-ncm22 -m ~/models/qwen36/Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf \\
      -ngl 999 --n-cpu-moe 22 -c 65536 -fa on --load-mode none

Env: PORT (default 18099), TIMEOUT in seconds (default 600)
EOF
    exit 1
fi

label=$1
shift
port=${PORT:-18099}
timeout=${TIMEOUT:-600}
log=$(mktemp -t "vram-probe-$label.XXXXXX.log")

start=$(date +%s)
llama-server --host 127.0.0.1 --port "$port" --parallel 1 "$@" >"$log" 2>&1 &
pid=$!

status=timeout
for _ in $(seq 1 "$timeout"); do
    if ! kill -0 "$pid" 2>/dev/null; then status=exited; break; fi
    if curl -s -m 2 "http://127.0.0.1:$port/health" | grep -q '"ok"'; then status=ready; break; fi
    sleep 1
done
load_s=$(( $(date +%s) - start ))

used=$(nvidia-smi --query-compute-apps=pid,used_memory --format=csv,noheader,nounits \
    | awk -F', ' -v p="$pid" '$1==p{print $2}')
free=$(nvidia-smi --query-gpu=memory.free --format=csv,noheader,nounits)

tps=-
if [[ $status == ready ]]; then
    tps=$(curl -s -m 120 "http://127.0.0.1:$port/completion" \
        -d '{"prompt":"Write a Go function that reverses a slice of ints.","n_predict":200,"cache_prompt":false}' \
        | jq -r '.timings.predicted_per_second | floor')
fi

kill "$pid" 2>/dev/null
wait "$pid" 2>/dev/null

echo "[$label] status=$status load=${load_s}s proc_vram=${used:-?}MiB gpu_free_while_loaded=${free}MiB gen_tok_s=$tps"
grep -m2 -E 'out of memory' "$log"
echo "log: $log"
