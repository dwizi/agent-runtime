#!/usr/bin/env bash
set -euo pipefail

docs_files=()
while IFS= read -r file; do
  docs_files+=("$file")
done < <(find docs -type f -name '*.md' | sort)

files=(README.md CONTRIBUTING.md SECURITY.md CODE_OF_CONDUCT.md CHANGELOG.md SUPPORT.md)
files+=("${docs_files[@]}")

if command -v markdownlint-cli2 >/dev/null 2>&1; then
  markdownlint-cli2 "${files[@]}"
else
  echo "markdownlint-cli2 not found; skipping markdown lint" >&2
fi

if command -v lychee >/dev/null 2>&1; then
  lychee --config .github/lychee.toml "${files[@]}"
else
  echo "lychee not found; skipping link check" >&2
fi
