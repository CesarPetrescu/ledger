#!/usr/bin/env bash
# Run with an already booted emulator/device. Uses only fictional data.
set -euo pipefail
cd "$(dirname "$0")"
smoke_dir=$(mktemp -d)
fixture_pid=''
cleanup() {
  if [[ -n "$fixture_pid" ]]; then kill "$fixture_pid" 2>/dev/null || true; fi
  adb reverse --remove tcp:8443 >/dev/null 2>&1 || true
  rm -rf "$smoke_dir"
}
trap cleanup EXIT
umask 077
mkdir -p "$smoke_dir/res/xml" "$smoke_dir/res/raw"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=localhost' \
  -addext 'subjectAltName=DNS:localhost' -keyout "$smoke_dir/server.key" -out "$smoke_dir/res/raw/test_ca.pem" >/dev/null 2>&1
cat > "$smoke_dir/res/xml/network_security_config.xml" <<'XML'
<network-security-config>
  <base-config cleartextTrafficPermitted="false"><trust-anchors><certificates src="system" /></trust-anchors></base-config>
  <debug-overrides><trust-anchors><certificates src="@raw/test_ca" /></trust-anchors></debug-overrides>
</network-security-config>
XML
python3 test/server.py --cert "$smoke_dir/res/raw/test_ca.pem" --key "$smoke_dir/server.key" > "$smoke_dir/server.log" 2>&1 &
fixture_pid=$!
adb reverse tcp:8443 tcp:8443
./gradlew --no-daemon -PtestCaDir="$smoke_dir/res" \
  -Pandroid.testInstrumentationRunnerArguments.fixture=true connectedDebugAndroidTest
