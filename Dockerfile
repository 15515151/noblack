# syntax=docker/dockerfile:1

# ============ Go build stage ============
FROM golang:1.25.12-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/noblack ./cmd/server

# ============ CPU model runtime ============
# Debian slim is used because official PyTorch CPU wheels are glibc-based.
FROM python:3.13-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    TOKENIZERS_PARALLELISM=false \
    CUDA_VISIBLE_DEVICES=""

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY model_service/requirements.txt /tmp/model-requirements.txt
RUN python -m pip install --no-cache-dir \
      --index-url https://download.pytorch.org/whl/cpu \
      torch==2.13.0+cpu \
    && python -m pip install --no-cache-dir -r /tmp/model-requirements.txt

COPY --from=go-build /out/noblack /app/noblack
COPY words.json /app/words.default.json
COPY docker-entrypoint.sh /app/entrypoint.sh
COPY model_service /app/model_service
COPY models /app/models

RUN chmod +x /app/entrypoint.sh \
    && mkdir -p /data

ENV NB_ADDR=":8080" \
    NB_WORDS="/data/words.json" \
    NB_STATS="/data/stats.json" \
    NB_TOKEN="" \
    NB_CI="false" \
    NB_WATCH="true" \
    NB_MODEL_HOST="127.0.0.1" \
    NB_MODEL_PORT="8091" \
    NB_MODEL_SERVICE_URL="http://127.0.0.1:8091" \
    NB_MODEL_THREADS="2" \
    NB_MODEL_PASS_THRESHOLD="0.15" \
    NB_MODEL_BLOCK_THRESHOLD="0.5" \
    NB_MODEL_COMBINE_POLICY="max"

USER root
EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=5 \
    CMD curl -fsS http://127.0.0.1:8080/health >/dev/null || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]

