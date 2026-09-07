#!/bin/sh
# Exercise the real installer offline with a native client binary.
set -eu
installer=$(cd "$(dirname "$0")/.." && pwd)/install.sh
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
cp "${1:?Pass a native Ledger binary}" "$fixture/client"
export LEDGER_INSTALL_FIXTURE="$fixture" LEDGER_INSTALL_DIR="$fixture/Atlas client with spaces"
mkdir "$fixture/bin"
case "$(uname -m)" in
    x86_64|amd64) asset=ledger_linux_amd64 ;;
    aarch64|arm64) asset=ledger_linux_arm64 ;;
    *) exit 1 ;;
esac
digest=$(sha256sum "$fixture/client" | awk '{print $1}')
printf '%s  %s\n' "$digest" "$asset" > "$fixture/SHA256SUMS"
cat > "$fixture/bin/curl" <<'EOF'
#!/bin/sh
set -eu
url=
destination=
while [ "$#" -gt 0 ]; do
    case "$1" in
        https://*) url=$1 ;;
        -o) shift; destination=$1 ;;
    esac
    shift
done
case "$url" in
    https://github.com/CesarPetrescu/ledger/releases/latest/download/SHA256SUMS)
        cp "$LEDGER_INSTALL_FIXTURE/SHA256SUMS" "$destination" ;;
    https://github.com/CesarPetrescu/ledger/releases/latest/download/ledger_linux_*)
        if [ -f "$LEDGER_INSTALL_FIXTURE/fail" ]; then exit 22; fi
        if [ -f "$LEDGER_INSTALL_FIXTURE/corrupt" ]; then printf corrupt > "$destination"
        else cp "$LEDGER_INSTALL_FIXTURE/client" "$destination"; fi ;;
    *) echo "Unexpected installer request" >&2; exit 1 ;;
esac
EOF
chmod +x "$fixture/bin/curl"
export PATH="$fixture/bin:$PATH"
expected=$("$fixture/client" version)
sh "$installer"
test "$("$LEDGER_INSTALL_DIR/ledger" version)" = "$expected"
printf 'old installation\n' > "$LEDGER_INSTALL_DIR/ledger"
sh "$installer"
cmp "$fixture/client" "$LEDGER_INSTALL_DIR/ledger"
for failure in corrupt fail missing-checksum; do
    case "$failure" in
        missing-checksum) printf '%s  unrelated\n' "$digest" > "$fixture/SHA256SUMS" ;;
        *) touch "$fixture/$failure" ;;
    esac
    if sh "$installer"; then echo "Installer accepted $failure" >&2; exit 1; fi
    cmp "$fixture/client" "$LEDGER_INSTALL_DIR/ledger"
    rm -f "$fixture/corrupt" "$fixture/fail"
done
echo 'Installer passed: install, replace, and preservation after corrupt, failed, or unverifiable downloads.'
