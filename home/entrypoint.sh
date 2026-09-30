#!/bin/sh
# First start: write a Sensorium config with the AI assistant, MCP and the connections
# of this setup already enabled. Filestash then asks for an admin password.
set -e
CONFIG=/app/data/state/config/config.json
if [ ! -f "$CONFIG" ]; then
    mkdir -p "$(dirname "$CONFIG")"
    cat > "$CONFIG" <<JSON
{
  "general": { "name": "${APP_NAME:-Sensorium}" },
  "connections": [
    { "type": "local", "label": "This computer", "path": "/data/home/" },
    { "type": "rclone", "label": "Clouds (rclone)" },
    { "type": "imap", "label": "Email" },
    { "type": "caldav", "label": "Calendar" },
    { "type": "sftp", "label": "Other computer (SFTP)" },
    { "type": "webdav", "label": "Phone (WebDAV)" }
  ],
  "middleware": {
    "identity_provider": { "type": "passthrough", "params": "{\\"strategy\\":\\"password_only\\"}" },
    "attribute_mapping": {
      "related_backend": "This computer",
      "params": "{\\"This computer\\":{\\"type\\":\\"local\\",\\"password\\":\\"{{ .password }}\\",\\"path\\":\\"/data/home/\\"}}"
    }
  },
  "features": {
    "ai": {
      "enable": true,
      "base_url": "${AI_BASE_URL:-http://ollama:11434/v1}",
      "model": "${AI_MODEL:-qwen3:8b}",
      "api_key": "${AI_API_KEY:-}",
      "jev_api_key": "${JEV_API_KEY:-}"
    },
    "mcp": { "enable": true },
    "search": { "enable": true },
    "recent": {
      "enable": true,
      "enable_ai": true,
      "model_address": "${AI_BASE_URL:-http://ollama:11434/v1}/chat/completions",
      "model_name": "${AI_MODEL:-qwen3:8b}",
      "api_key": "${AI_API_KEY:-}"
    },
    "favourite": { "enable": true }
  }
}
JSON
    echo "==> wrote a starter config, open http://localhost:8334 to choose the admin password"
fi
exec /app/filestash
