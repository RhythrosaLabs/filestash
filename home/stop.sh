#!/bin/sh
cd "$(dirname "$0")" && docker compose --profile ollama down
