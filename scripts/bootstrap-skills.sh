#!/usr/bin/env sh
set -eu

repo="${1:-vercel-labs/agent-skills}"
agent="${2:-codex}"

# Default to /data in containers so installed skills persist and are shared.
if [ -d "/data" ] && [ "${HOME:-/root}" = "/root" ]; then
  export HOME="/data"
fi

if command -v bunx >/dev/null 2>&1; then
  bunx --bun skills add "$repo" -g -a "$agent" -y
  exit 0
fi

npx --yes skills add "$repo" -g -a "$agent" -y
