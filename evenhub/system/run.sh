#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/../.."
ROOT="$PWD"
export GLASS_ARTIFACT_DIR="$ROOT/evenhub/system/artifacts"
export GLASS_RUNTIME_DIR="${RUNNER_TEMP:-/tmp}/ledger-glass-system-${GLASS_TARGET:-web}-$$"
export GLASS_CERT_DIR="$GLASS_RUNTIME_DIR/certs"
export GLASS_CA_NAME="ledger-glass-ci-${GLASS_TARGET:-web}-$$"
mkdir -p "$GLASS_ARTIFACT_DIR" "$GLASS_CERT_DIR"
chmod 700 "$GLASS_RUNTIME_DIR"
export COMPOSE_PROJECT_NAME="ledger-glass-system-${GLASS_TARGET:-web}"
export LEDGER_PUBLIC_URL=https://localhost:8443
export LEDGER_SERVER="$LEDGER_PUBLIC_URL"
export GLASS_PROVIDER_TOKEN="$(openssl rand -hex 24)"
export TZ=Europe/Bucharest
export LEDGER_POSTGRES_PASSWORD="$(openssl rand -hex 24)"
export GLASS_OWNER_PASSWORD="$(openssl rand -hex 24)"
export LEDGER_CALENDAR_ENCRYPTION_KEY="$(openssl rand -hex 32)"
export LEDGER_TRUSTED_PROXY_CIDR=10.251.250.0/24
export LEDGER_INFER_URL=http://127.0.0.1:1
export LEDGER_PASSWORD_HASH=build-only
export LEDGER_ADMIN_PASSWORD_HASH=build-only
DC=(docker compose -f docker-compose.yml -f evenhub/system/compose.yml)
cleanup() {
  code=$?
  trap - EXIT
  "${DC[@]}" logs --no-color > "$GLASS_RUNTIME_DIR/containers.log" 2>&1 || true
  node evenhub/system/redact.mjs "$GLASS_RUNTIME_DIR" "$GLASS_ARTIFACT_DIR" || true
  "${DC[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  certutil -D -d "sql:$HOME/.pki/nssdb" -n "$GLASS_CA_NAME" >/dev/null 2>&1 || true
  sudo rm -f "/usr/local/share/ca-certificates/$GLASS_CA_NAME.crt"
  sudo update-ca-certificates >/dev/null 2>&1 || true
  rm -rf "$GLASS_RUNTIME_DIR"
  exit "$code"
}
trap cleanup EXIT
"${DC[@]}" build > "$GLASS_RUNTIME_DIR/build.log" 2>&1 || { tail -80 "$GLASS_RUNTIME_DIR/build.log"; exit 1; }
export LEDGER_ADMIN_PASSWORD_HASH="$(printf '%s\n' "$GLASS_OWNER_PASSWORD" | "${DC[@]}" run --rm --no-deps -T ledger-auth hash-password)"
export LEDGER_PASSWORD_HASH="$LEDGER_ADMIN_PASSWORD_HASH"
openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 1 \
  -keyout "$GLASS_CERT_DIR/localhost.key" -out "$GLASS_CERT_DIR/localhost.crt" \
  -subj /CN=localhost -addext 'subjectAltName=DNS:localhost,DNS:glass-provider,IP:127.0.0.1' >/dev/null 2>&1
sudo install -m 644 "$GLASS_CERT_DIR/localhost.crt" "/usr/local/share/ca-certificates/$GLASS_CA_NAME.crt"
sudo update-ca-certificates >/dev/null
mkdir -p "$HOME/.pki/nssdb"
if [[ ! -f "$HOME/.pki/nssdb/cert9.db" ]]; then certutil -N -d "sql:$HOME/.pki/nssdb" --empty-password; fi
certutil -A -d "sql:$HOME/.pki/nssdb" -n "$GLASS_CA_NAME" -t C,, -i "$GLASS_CERT_DIR/localhost.crt"
export NODE_EXTRA_CA_CERTS="$GLASS_CERT_DIR/localhost.crt"
(cd evenhub && npm run pack) > "$GLASS_RUNTIME_DIR/package.log" 2>&1 || { cat "$GLASS_RUNTIME_DIR/package.log"; exit 1; }
node evenhub/system/validate-package.mjs
cp evenhub/ledger-glass.ehpk "$GLASS_ARTIFACT_DIR/ledger-glass-ci.ehpk"
git rev-parse HEAD > "$GLASS_ARTIFACT_DIR/tested-commit.txt"
docker compose version > "$GLASS_ARTIFACT_DIR/versions.txt"
node --version >> "$GLASS_ARTIFACT_DIR/versions.txt"
npm --prefix evenhub ls --depth=0 >> "$GLASS_ARTIFACT_DIR/versions.txt"
git archive --format=tar HEAD evenhub .github/workflows > "$GLASS_ARTIFACT_DIR/tested-source.tar"
"${DC[@]}" up -d > "$GLASS_RUNTIME_DIR/start.log" 2>&1 || { cat "$GLASS_RUNTIME_DIR/start.log"; exit 1; }
# Separate jobs use separate real deployments, preserving production rate limits.
if [[ "${GLASS_SUITE:-native}" == host-contract ]]; then
  healthy=0
  for attempt in $(seq 1 60); do
    status=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 3 "$LEDGER_PUBLIC_URL/admin/api/session" || true)
    if [[ "$status" == 401 ]]; then healthy=1; break; fi
    sleep 1
  done
  test "$healthy" -eq 1
  (cd evenhub && npx vitest run --config system/vitest.config.mjs)
else
  pulseaudio --start --exit-idle-time=-1
  pactl load-module module-null-sink sink_name=glassci >/dev/null
  pactl set-default-source glassci.monitor
  export LIBGL_ALWAYS_SOFTWARE=1
  export WEBKIT_DISABLE_DMABUF_RENDERER=1
  export GDK_BACKEND=x11
  xvfb-run -a -s '-screen 0 1280x1024x24' dbus-run-session -- node evenhub/system/run.mjs
fi
