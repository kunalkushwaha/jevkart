#!/usr/bin/env bash
# Ask jev a set of typed questions about some state.
#   usage: examples/ask.sh [payload.json]   (default: examples/example.json)
set -euo pipefail

cd "$(dirname "$0")/.."
[ -f .env ] || { echo "missing .env (see .env.example)" >&2; exit 1; }
set -a; . ./.env; set +a

payload="${1:-examples/example.json}"

curl -sS --fail-with-body --max-time 60 \
  -X POST https://api.typesafe.ai/v1/systemone \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  --data @"$payload" | jq .
