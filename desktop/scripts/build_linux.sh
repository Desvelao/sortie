#!/usr/bin/env bash
# Run inside the dev container (see Makefile). Usage: build_linux.sh [build|smoke|all]
set -euo pipefail
cd "$(dirname "$0")/.."
step=${1:-all}

checks() {
  test -z "$(gofmt -l .)" || { echo "gofmt issues:"; gofmt -l .; exit 1; }
  go vet ./...
  go test ./...
}
build() {
  mkdir -p release
  go build -trimpath -ldflags="-s -w -X main.version=$(tr -d '[:space:]' < ../VERSION)-dev" -o release/sortie ./cmd/sortie
  file release/sortie
  echo "--- dynamic dependencies:"; ldd release/sortie || true
  ./release/sortie --version
}
smoke() {
  xvfb-run -a ./release/sortie --smoke-test
  echo "smoke test OK"
}
case $step in
  build) checks; build ;;
  smoke) smoke ;;
  all) checks; build; smoke ;;
  *) echo "unknown step $step"; exit 2 ;;
esac
