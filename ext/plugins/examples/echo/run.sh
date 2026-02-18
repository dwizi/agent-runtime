#!/bin/sh
set -eu

# Reads request JSON from stdin and emits a minimal response JSON.
request="$(cat)"

echo "{\"message\":\"echo plugin received request (${#request} bytes)\"}"
