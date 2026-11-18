#!/usr/bin/env bash
# Tests with the race detector on all logic packages (run inside the dev container; make test-race).
set -euo pipefail
cd "$(dirname "$0")/.."

go test -race $(go list ./... | grep -v '/internal/ui$')
# Fyne's test driver runs fyne.Do callbacks inline on the calling background goroutine (the real
# driver queues them onto the UI thread), so -race reports artifacts there; run these without it.
go test ./internal/ui
