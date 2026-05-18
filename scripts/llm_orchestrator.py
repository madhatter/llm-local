#!/usr/bin/env python3
"""
Dynamic LLM orchestrator - switches between models by restarting llama-server
on a single shared port. Only one model loaded into VRAM at a time.
"""

import httpx
import subprocess
import signal
import sys
import time
from pathlib import Path

import yaml

from fastapi import FastAPI, Request, HTTPException
from starlette.responses import StreamingResponse

app = FastAPI()

SCRIPT_DIR = Path(__file__).parent.resolve()
PROJECT_DIR = SCRIPT_DIR.parent
YAML_FILE = PROJECT_DIR / "models.yaml"
OS_OVERLAY = PROJECT_DIR / "config" / f"{sys.platform}.yaml"

SHARED_PORT = 18888

CURRENT_MODEL: str | None = None
SERVER_PROCESS: subprocess.Popen | None = None


def load_merged_config() -> dict:
    """Load models.yaml and merge with OS-specific overlay."""
    import json

    base = yaml.safe_load(YAML_FILE.read_text())

    if OS_OVERLAY.exists():
        overlay = yaml.safe_load(OS_OVERLAY.read_text())
        # Merge overlay into base by model name
        for o_model in overlay.get("models", []):
            for b_model in base["models"]:
                if b_model["name"] == o_model["name"]:
                    for k, v in o_model.get("serve", {}).items():
                        b_model.setdefault("serve", {})
                        b_model["serve"][k] = v

    return base


def get_model_config(name: str) -> dict | None:
    """Find model config by name."""
    config = load_merged_config()
    for m in config.get("models", []):
        if m["name"] == name:
            return m
    return None


def find_model_file(local_dir: str) -> Path:
    """Find the .gguf file in local_dir."""
    expanded = Path(local_dir).expanduser()
    files = list(expanded.glob("*.gguf"))
    if not files:
        raise FileNotFoundError(f"No .gguf found in {expanded}")
    return files[0]


def build_server_cmd(model_cfg: dict) -> list[str]:
    """Build llama-server command from model config."""
    serve = model_cfg.get("serve", {})

    local_dir = model_cfg.get("local_dir", "")
    model_file = str(find_model_file(local_dir))

    args = [
        "--model",
        model_file,
        "--host",
        serve.get("host", "127.0.0.1"),
        "--port",
        str(SHARED_PORT),
        "-ngl",
        str(serve.get("n_gpu_layers", 99)),
        "--ctx-size",
        str(serve.get("ctx_size", 8192)),
        "-t",
        str(serve.get("threads", 8)),
        "--temp",
        str(serve.get("temp", 0.7)),
        "--top-p",
        str(serve.get("top_p", 0.95)),
        "--repeat-penalty",
        str(serve.get("repeat_penalty", 1.05)),
    ]

    top_k = serve.get("top_k")
    if top_k is not None:
        args.extend(["--top-k", str(top_k)])

    cache_k = serve.get("cache_type_k")
    if cache_k:
        args.extend(["--cache-type-k", str(cache_k)])

    cache_v = serve.get("cache_type_v")
    if cache_v:
        args.extend(["--cache-type-v", str(cache_v)])

    if serve.get("flash_attn"):
        args.extend(["--flash-attn", "on"])

    if serve.get("jinja"):
        args.append("--jinja")

    batch = serve.get("batch_size")
    if batch is not None:
        args.extend(["--batch-size", str(batch)])

    ubatch = serve.get("ubatch_size")
    if ubatch is not None:
        args.extend(["--ubatch-size", str(ubatch)])

    if serve.get("cont_batching"):
        args.append("--cont-batching")

    mmap = serve.get("mmap")
    if mmap is False:
        args.append("--no-mmap")
    elif mmap is True:
        args.append("--mmap")

    return ["llama-server", *args]


def stop_server():
    """Terminate current llama-server process."""
    global SERVER_PROCESS, CURRENT_MODEL

    if SERVER_PROCESS and SERVER_PROCESS.poll() is None:
        print(f"[{time.strftime('%H:%M:%S')}] Stopping server for '{CURRENT_MODEL}'...")
        SERVER_PROCESS.terminate()
        try:
            SERVER_PROCESS.wait(timeout=15)
        except subprocess.TimeoutExpired:
            SERVER_PROCESS.kill()
            SERVER_PROCESS.wait(timeout=5)
        print(f"[{time.strftime('%H:%M:%S')}] Server stopped, VRAM freed.")

    SERVER_PROCESS = None
    CURRENT_MODEL = None


def wait_for_server():
    """Wait until llama-server is ready (model loaded)."""
    for i in range(120):
        try:
            r = httpx.get(f"http://127.0.0.1:{SHARED_PORT}/health", timeout=2)
            if r.status_code == 200:
                return True
        except Exception:
            pass
        time.sleep(1)

    raise RuntimeError("Server did not become ready within 120s")


def start_server(model_name: str):
    """Start llama-server for the given model."""
    global SERVER_PROCESS, CURRENT_MODEL

    if CURRENT_MODEL == model_name and SERVER_PROCESS and SERVER_PROCESS.poll() is None:
        return  # Already running

    stop_server()

    model_cfg = get_model_config(model_name)
    if not model_cfg:
        raise HTTPException(400, f"Unknown model: {model_name}")

    cmd = build_server_cmd(model_cfg)
    print(f"[{time.strftime('%H:%M:%S')}] Starting server for '{model_name}'...")
    print(f"  CMD: {' '.join(cmd)}")

    SERVER_PROCESS = subprocess.Popen(
        cmd,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    CURRENT_MODEL = model_name

    wait_for_server()
    print(f"[{time.strftime('%H:%M:%S')}] Server for '{model_name}' ready.")


def resolve_model(path: str) -> str:
    """Extract model name from request path."""
    parts = path.split("/")
    for part in parts:
        if part and get_model_config(part):
            return part
    # Default fallback
    default_cfg = load_merged_config()
    for m in default_cfg.get("models", []):
        if m.get("default"):
            return m["name"]
    raise HTTPException(400, "No model specified in path. Use /{model_name}/v1/...")


@app.api_route("/{path:path}", methods=["GET", "POST"])
async def proxy(request: Request, path: str):
    """Route requests to the correct llama-server instance."""
    global CURRENT_MODEL

    target_model = resolve_model(path)

    # Switch model if needed
    if CURRENT_MODEL != target_model:
        start_server(target_model)

    # Strip model name prefix from path
    real_path = path.replace(target_model, "", 1).lstrip("/")

    upstream = f"http://127.0.0.1:{SHARED_PORT}"

    async with httpx.AsyncClient() as client:
        headers = {k: v for k, v in request.headers.items() if k.lower() != "host"}
        resp = await client.request(
            method=request.method,
            url=f"{upstream}/{real_path}",
            headers=headers,
            content=await request.body(),
            timeout=None,
            stream=True,
        )

    return StreamingResponse(
        resp.aiter_bytes(),
        status_code=resp.status_code,
        headers={
            k: v for k, v in resp.headers.items() if k.lower() != "transfer-encoding"
        },
    )


@app.get("/status")
async def status():
    """Return current server status."""
    return {
        "current_model": CURRENT_MODEL,
        "server_running": SERVER_PROCESS is not None and SERVER_PROCESS.poll() is None,
    }


@app.post("/switch/{model_name}")
async def switch(model_name: str):
    """Explicitly switch to a model without sending a request."""
    start_server(model_name)
    return {
        "current_model": CURRENT_MODEL,
        "message": f"Switched to '{model_name}'",
    }


def shutdown(signum, frame):
    print("\nShutting down orchestrator...")
    stop_server()
    sys.exit(0)


signal.signal(signal.SIGINT, shutdown)
signal.signal(signal.SIGTERM, shutdown)
