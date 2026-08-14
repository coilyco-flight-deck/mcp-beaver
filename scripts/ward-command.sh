#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
export GOPRIVATE=forgejo.coilysiren.me

action="${1:-}"
shift || true

case "$action" in
  build)
    go build ./...
    ;;
  test)
    go test ./...
    ;;
  vet)
    go vet ./...
    ;;
  tidy)
    go mod tidy
    ;;
  fmt)
    gofmt -w cmd internal
    ;;
  serve-example)
    go run ./cmd/ward-mcp serve examples/skillsmp.mcp.kdl --http "${1:-:18080}"
    ;;
  pin-umbra)
    ref="${1:-v0.122.0}"
    go get "forgejo.coilysiren.me/coilyco-flight-deck/umbra@${ref}"
    go mod tidy
    ;;
  *)
    echo "unknown Ward action: $action" >&2
    exit 2
    ;;
esac
