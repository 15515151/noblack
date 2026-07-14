#!/bin/sh
set -eu

words_dir="$(dirname "$NB_WORDS")"
stats_dir=""
if [ -n "${NB_STATS:-}" ]; then
  stats_dir="$(dirname "$NB_STATS")"
fi

mkdir -p "$words_dir"
if [ -n "$stats_dir" ]; then
  mkdir -p "$stats_dir"
fi

if [ ! -f "$NB_WORDS" ]; then
  echo "[entrypoint] initializing word database: $NB_WORDS"
  cp /app/words.default.json "$NB_WORDS"
fi

check_write_dir() {
  dir="$1"
  test_file="$dir/.noblack-write-test-$$"
  if ! (: > "$test_file" && rm -f "$test_file"); then
    echo "[entrypoint] directory is not writable: $dir" >&2
    exit 1
  fi
}
check_write_dir "$words_dir"
if [ -n "$stats_dir" ] && [ "$stats_dir" != "$words_dir" ]; then
  check_write_dir "$stats_dir"
fi

# The model service is loopback-only. Both models are loaded once and remain in
# memory; each request runs Lite and MacBERT concurrently on CPU.
python /app/model_service/app.py &
model_pid=$!

cleanup() {
  kill "$model_pid" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

ready="false"
i=0
while [ "$i" -lt 120 ]; do
  if curl -fsS "http://${NB_MODEL_HOST:-127.0.0.1}:${NB_MODEL_PORT:-8091}/health" >/dev/null 2>&1; then
    ready="true"
    break
  fi
  if ! kill -0 "$model_pid" 2>/dev/null; then
    echo "[entrypoint] model service exited during startup" >&2
    exit 1
  fi
  i=$((i + 1))
  sleep 1
done
if [ "$ready" != "true" ]; then
  echo "[entrypoint] model service startup timed out" >&2
  exit 1
fi

echo "[entrypoint] dual CPU models are ready"

set -- -addr "$NB_ADDR" -words "$NB_WORDS" -watch="$NB_WATCH" \
  -model-service-url "$NB_MODEL_SERVICE_URL"
if [ -n "${NB_STATS:-}" ]; then
  set -- "$@" -stats-file "$NB_STATS"
fi
if [ -n "${NB_TOKEN:-}" ]; then
  set -- "$@" -token "$NB_TOKEN"
fi
if [ "${NB_CI:-false}" = "true" ]; then
  set -- "$@" -ci
fi

echo "[entrypoint] starting noblack on $NB_ADDR"
exec /app/noblack "$@"
