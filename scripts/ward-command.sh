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
  pin-cli-guard)
    ref="${1:-v0.122.0}"
    go get "forgejo.coilysiren.me/coilyco-flight-deck/cli-guard@${ref}"
    go mod tidy
    ;;
  *)
    echo "unknown Ward action: $action" >&2
    exit 2
    ;;
esac
