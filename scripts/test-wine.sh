#!/usr/bin/env bash
# Isolated, offline Windows tests; no host credentials or Docker socket mounted.
set -euo pipefail
cd "$(dirname "$0")/.."
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$fixture/ledger-tests.exe" ./cmd/ledger
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X main.version=v0.0.0' -o "$fixture/ledger.exe" ./cmd/ledger
docker build -t ledger-wine-tests - < scripts/wine.Dockerfile
docker run --rm --init --network=none --cap-drop=ALL --security-opt=no-new-privileges \
  -v "$fixture:/checks:ro" ledger-wine-tests xvfb-run -a sh -eu -c '
    wine --version
    wineboot -u
    mkdir -p "$WINEPREFIX/drive_c/ledger-tests"
    cp /checks/*.exe "$WINEPREFIX/drive_c/ledger-tests/"
    wine "C:\\ledger-tests\\ledger-tests.exe" -test.v -test.timeout=3m
    test "$(wine "C:\\ledger-tests\\ledger.exe" version)" = v0.0.0
  '
