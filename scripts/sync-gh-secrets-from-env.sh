#!/usr/bin/env bash
set -euo pipefail

REPO="${1:-dwizi/agent-runtime}"
ENV_FILE="${2:-.env}"

if ! command -v gh >/dev/null 2>&1; then
  echo "error: gh CLI is not installed" >&2
  exit 1
fi

if [[ ! -f "$ENV_FILE" ]]; then
  echo "error: env file not found: $ENV_FILE" >&2
  exit 1
fi

if ! gh repo view "$REPO" >/dev/null 2>&1; then
  echo "error: repo not found or not accessible: $REPO" >&2
  exit 1
fi

updated=0

while IFS= read -r line; do
  [[ "$line" =~ ^AGENT_RUNTIME_[A-Z0-9_]+= ]] || continue

  key="${line%%=*}"
  value="${line#*=}"

  if [[ -z "$value" ]]; then
    continue
  fi

  gh secret set "$key" --repo "$REPO" --body "$value" >/dev/null
  echo "updated secret: $key"
  updated=$((updated + 1))
done < "$ENV_FILE"

echo "done: $updated secret(s) updated in $REPO"
