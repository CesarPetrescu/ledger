#!/bin/sh
# Install the latest stable Linux client; optionally connect with --server URL.
set -eu

main() {
    case "$(uname -s)" in
        Linux) ;;
        *) echo 'Ledger currently supports Linux (including Windows WSL).' >&2; exit 1 ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) asset=ledger_linux_amd64 ;;
        aarch64|arm64) asset=ledger_linux_arm64 ;;
        *) echo 'Ledger requires an amd64 or arm64 Linux machine.' >&2; exit 1 ;;
    esac
    for command in curl sha256sum awk mktemp install mv; do
        command -v "$command" >/dev/null 2>&1 || {
            echo "Install $command first, then rerun this installer." >&2
            exit 1
        }
    done

    install_dir=${LEDGER_INSTALL_DIR:-"$HOME/.local/bin"}
    download_dir=$(mktemp -d)
    staged_binary=
    trap 'rm -rf "$download_dir"; if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi' EXIT
    trap 'exit 1' HUP INT TERM
    release=https://github.com/CesarPetrescu/ledger/releases/latest/download

    echo "Downloading $asset…"
    if ! curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 10 --max-time 120 "$release/$asset" -o "$download_dir/$asset"; then
        echo 'Download failed. Check your connection and https://github.com/CesarPetrescu/ledger/releases for a published release.' >&2
        exit 1
    fi
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 10 --max-time 30 "$release/SHA256SUMS" -o "$download_dir/SHA256SUMS"
    (
        cd "$download_dir"
        awk -v asset="$asset" '$2 == asset { print }' SHA256SUMS | sha256sum --check --strict -
    ) || { echo 'Checksum verification failed; existing installation preserved.' >&2; exit 1; }

    mkdir -p "$install_dir"
    staged_binary=$(mktemp "$install_dir/.ledger.XXXXXX")
    install -m 0755 "$download_dir/$asset" "$staged_binary"
    mv -fT "$staged_binary" "$install_dir/ledger"
    staged_binary=
    printf '\nInstalled Ledger at %s/ledger\n' "$install_dir"
    printf 'To use ledger from this terminal, run:\n  export PATH="%s:$PATH"\n\n' "$install_dir"
    if [ "$#" -gt 0 ]; then
        "$install_dir/ledger" connect codex "$@"
    else
        printf 'Connect to your server:\n  "%s/ledger" connect codex --server https://ledger.example.com --name "My PC"\n' "$install_dir"
    fi
}

main "$@"
