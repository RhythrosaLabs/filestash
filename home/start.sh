#!/bin/sh
# Starts Sensorium. Safe to run again at any time.
set -e
cd "$(dirname "$0")"

if ! command -v docker >/dev/null 2>&1; then
    echo "Docker is needed: install Docker Desktop from https://www.docker.com/products/docker-desktop/ and run this again."
    exit 1
fi
if ! docker info >/dev/null 2>&1; then
    echo "Docker isn't running: open Docker Desktop, wait until it says 'running', then run this again."
    exit 1
fi

if [ ! -f .env ]; then
    cat > .env <<ENV
# folder of this computer shown in Sensorium as "This computer"
FILES_DIR="$HOME"
# local model used by the assistant (needs tool calling: qwen3, hermes3, llama3.1, ...)
AI_MODEL=qwen3:8b
# leave empty to use Ollama. For DeepSeek: AI_BASE_URL=https://api.deepseek.com/v1, AI_MODEL=deepseek-chat, AI_API_KEY=sk-...
AI_BASE_URL=
AI_API_KEY=
# optional, from https://typesafe.ai
JEV_API_KEY=
ENV
    echo "==> created home/.env (edit it to change the folder or the model)"
fi
set -a; . ./.env; set +a
mkdir -p rclone

PROFILE=""
if [ -z "$AI_BASE_URL" ]; then
    # the Ollama app is reachable from Docker Desktop on Mac, on Linux it only listens on localhost
    if [ "$(uname)" = "Darwin" ] && curl -s --max-time 2 http://localhost:11434/api/version >/dev/null 2>&1; then
        echo "==> using the Ollama app already running on this computer"
        AI_BASE_URL=http://host.docker.internal:11434/v1
        if command -v ollama >/dev/null 2>&1; then ollama pull "$AI_MODEL"; fi
    else
        echo "==> running Ollama in Docker (first run downloads the model, a few GB)"
        AI_BASE_URL=http://ollama:11434/v1
        PROFILE="--profile ollama"
    fi
fi
export AI_BASE_URL

echo "==> building and starting (the first build takes 5 to 15 minutes)"
docker compose $PROFILE up -d --build

printf "==> waiting for Sensorium"
i=0
until curl -s -o /dev/null http://localhost:8334/; do
    i=$((i + 1)); [ $i -gt 60 ] && { echo; echo "not up yet, check: docker compose logs sensorium"; exit 1; }
    printf "."; sleep 2
done
echo
echo "==> ready: http://localhost:8334"
echo "    first visit: choose an admin password, it's also the password of 'This computer' and 'Clouds (rclone)'"
echo "    add iCloud / Dropbox / Google Drive / OneDrive accounts: ./rclone-config.sh"
if command -v open >/dev/null 2>&1; then open http://localhost:8334; elif command -v xdg-open >/dev/null 2>&1; then xdg-open http://localhost:8334 >/dev/null 2>&1 || true; fi
