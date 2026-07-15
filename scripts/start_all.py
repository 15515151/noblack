from __future__ import annotations

import argparse
import json
import os
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WORKSPACE = ROOT.parent


def normalized_env() -> dict[str, str]:
    # Windows environment names are case-insensitive. Normalize duplicate
    # Path/PATH entries to avoid PowerShell Start-Process failures.
    merged: dict[str, tuple[str, str]] = {}
    for key, value in os.environ.items():
        merged[key.upper()] = (key, value)
    env = {original: value for original, value in merged.values()}

    local_runtime = ROOT / ".runtime"
    roots = [
        local_runtime,
        WORKSPACE / ".vendor-model",
        WORKSPACE / ".vendor",
        ROOT / "model_service" / "src",
    ]
    python_paths = [str(path) for path in roots if path.exists()]
    existing = env.get("PYTHONPATH", "")
    if existing:
        python_paths.append(existing)
    env["PYTHONPATH"] = os.pathsep.join(python_paths)
    env["PYTHONUTF8"] = "1"
    env["PYTHONIOENCODING"] = "utf-8"
    env["CUDA_VISIBLE_DEVICES"] = ""
    env["HF_HUB_OFFLINE"] = "1"
    env["TRANSFORMERS_OFFLINE"] = "1"
    env["TOKENIZERS_PARALLELISM"] = "false"
    # Never route loopback service traffic through an inherited proxy.
    for key in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
        env.pop(key, None)
    env["NO_PROXY"] = "127.0.0.1,localhost"
    return env


def request_json(url: str, payload: dict | None = None, timeout: float = 5.0) -> dict:
    data = None
    headers = {}
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json; charset=utf-8"
    request = urllib.request.Request(url, data=data, headers=headers, method="POST" if data else "GET")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(request, timeout=timeout) as response:
        return json.loads(response.read().decode("utf-8"))


def wait_health(url: str, process: subprocess.Popen, timeout: float) -> dict:
    deadline = time.monotonic() + timeout
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"process exited with code {process.returncode}")
        try:
            result = request_json(url, timeout=2)
            if result.get("ok") or result.get("code") == 200:
                return result
        except (OSError, urllib.error.URLError, json.JSONDecodeError) as exc:
            last_error = exc
        time.sleep(0.25)
    raise TimeoutError(f"health check timed out: {last_error}")


def terminate(process: subprocess.Popen | None) -> None:
    if process is None or process.poll() is not None:
        return
    if os.name == "nt":
        # go run spawns the compiled server as a child process. taskkill /T
        # ensures the complete process tree is stopped during tests/shutdown.
        subprocess.run(
            ["taskkill", "/PID", str(process.pid), "/T", "/F"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def main() -> int:
    parser = argparse.ArgumentParser(description="Run noblack Go server and two resident CPU models")
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument("--model-port", type=int, default=8091)
    parser.add_argument("--threads", type=int, default=2)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()

    env = normalized_env()
    env["NB_MODEL_HOST"] = "127.0.0.1"
    env["NB_MODEL_PORT"] = str(args.model_port)
    env["NB_MODEL_THREADS"] = str(args.threads)
    env.setdefault("NB_LITE_MODEL", str(ROOT / "models" / "lite-production-v1"))
    env.setdefault("NB_MACBERT_MODEL", str(ROOT / "models" / "macbert-production-v1"))
    env.setdefault("NB_MODEL_COMBINE_POLICY", "max")
    model_url = f"http://127.0.0.1:{args.model_port}"

    model_process: subprocess.Popen | None = None
    go_process: subprocess.Popen | None = None
    try:
        model_process = subprocess.Popen(
            [sys.executable, str(ROOT / "model_service" / "app.py")],
            cwd=ROOT,
            env=env,
        )
        health = wait_health(model_url + "/health", model_process, timeout=45)
        print(
            f"[runner] CPU models ready: {','.join(health['models'])}; "
            f"parallel={health['parallel']}",
            flush=True,
        )

        go_env = env.copy()
        go_env["NB_MODEL_SERVICE_URL"] = model_url
        go_cache = ROOT / ".gocache"
        go_tmp = ROOT / ".gotmp"
        go_cache.mkdir(exist_ok=True)
        go_tmp.mkdir(exist_ok=True)
        go_env["GOCACHE"] = str(go_cache)
        go_env["GOTMPDIR"] = str(go_tmp)
        go_process = subprocess.Popen(
            [
                "go",
                "run",
                "./cmd/server",
                "-addr",
                f":{args.port}",
                "-words",
                "./words.json",
                "-model-service-url",
                model_url,
            ],
            cwd=ROOT,
            env=go_env,
        )
        wait_health(f"http://127.0.0.1:{args.port}/health", go_process, timeout=45)

        if args.self_test:
            response = request_json(
                f"http://127.0.0.1:{args.port}/check",
                {"text": "晚上好"},
                timeout=30,
            )
            data = response.get("data", {})
            models = data.get("model_results", [])
            if (
                len(models) != 2
                or data.get("model_device") != "cpu"
                or not data.get("models_parallel")
                or not data.get("model_combine_policy")
            ):
                raise RuntimeError(f"incomplete dual-model response: {response}")
            print(
                json.dumps(
                    {
                        "ok": True,
                        "device": data["model_device"],
                        "parallel": data["models_parallel"],
                        "combine_policy": data.get("model_combine_policy"),
                        "models": [item["model"] for item in models],
                        "actions": {item["model"]: item["action"] for item in models},
                        "model_latency_ms": data.get("model_latency_ms"),
                    },
                    ensure_ascii=False,
                    indent=2,
                )
            )
            return 0

        print(f"[runner] open http://127.0.0.1:{args.port}", flush=True)
        while True:
            if go_process.poll() is not None:
                return go_process.returncode or 1
            if model_process.poll() is not None:
                raise RuntimeError(f"model service exited with code {model_process.returncode}")
            time.sleep(0.5)
    except KeyboardInterrupt:
        return 0
    finally:
        terminate(go_process)
        terminate(model_process)


if __name__ == "__main__":
    raise SystemExit(main())
