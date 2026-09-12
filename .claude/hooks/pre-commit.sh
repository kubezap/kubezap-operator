#!/bin/bash
# Runs gofmt and go vet before any git commit Claude executes.
input=$(cat)
command=$(echo "$input" | jq -r '.command // ""')

if echo "$command" | grep -q 'git commit'; then
  echo "Running pre-commit checks..." >&2
  gofmt -w . && go vet $(go list ./... | grep -v /e2e)
fi
