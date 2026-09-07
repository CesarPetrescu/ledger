# Client installation and connection

[← Back to Ledger](../README.md)

[Windows](#windows-powershell) · [Linux / WSL](#linux-and-wsl) · [Commands](#everyday-ledger-commands) · [Troubleshooting](#connection-details-and-troubleshooting) · [Updates](#automatic-client-updates)

Install and connect the CLI for Codex on Windows or Linux. For the owner app, see [Ledger for Android](../android/README.md).

Install the client on the **machine where Codex runs**. Native Windows 10/11
and Linux (including WSL and remote Linux over SSH) support x64 and ARM64.
You need Codex CLI 0.152 or newer and the HTTPS address of a Ledger server.
The installers download and verify the latest release without Go, Git, Docker,
or administrator access.

## Windows (PowerShell)

Run this in PowerShell:

```powershell
curl.exe -fsSL https://raw.githubusercontent.com/CesarPetrescu/ledger/main/install.ps1 -o "$env:TEMP\ledger-install.ps1"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$env:TEMP\ledger-install.ps1"
```

The installer detects x64 or ARM64, verifies the GitHub release's SHA-256 digest,
and installs to `%LOCALAPPDATA%\Programs\Ledger`. It adds that directory to your
user PATH. Open a **new terminal**, then connect:

```powershell
ledger connect codex --server https://ledger.example.com --name "My Windows PC"
```

Approve the device code in your Ledger owner console, then start Codex. The
same `auth status`, `auth logout`, and `update` commands below work on Windows.
Setup supports standalone Codex and npm's `codex.cmd`, including paths with spaces.

For a portable installation, download the [x64 executable](https://github.com/CesarPetrescu/ledger/releases/latest/download/ledger_windows_amd64.exe)
or [ARM64 executable](https://github.com/CesarPetrescu/ledger/releases/latest/download/ledger_windows_arm64.exe)
and rename it to `ledger.exe`. Keep it in a writable directory so `ledger update`
can replace it. Published checksums are in `SHA256SUMS` on the same release.
Windows binaries are not Authenticode-signed.

The installer also accepts `-Server URL`, `-Name "My PC"`, `-InstallDir PATH`,
and `-NoPath`. `-NoPath` leaves your PATH unchanged.

## Linux and WSL

### 1. Copy and paste to install

```sh
curl -fsSL https://raw.githubusercontent.com/CesarPetrescu/ledger/main/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"
```

The second line makes `ledger` available in the current terminal. If it is not
already in your PATH, add that same export line to `~/.bashrc` (Bash) or
`~/.zshrc` (Zsh) for future terminals.

To inspect the installer first, download
[install.sh](https://raw.githubusercontent.com/CesarPetrescu/ledger/main/install.sh),
read it, and run `sh install.sh`. Set `LEDGER_INSTALL_DIR` when running it to use
a different installation directory.

### 2. Set your server URL and connect

Replace `https://ledger.example.com` with **your Ledger server's address**. Use
the base HTTPS URL, without `/mcp` or `/admin`. The machine name can be anything
recognizable to you; it is shown on the approval page.

```sh
ledger connect codex --server https://ledger.example.com --name "My laptop"
```

For installation and connection in one copyable command:

```sh
curl -fsSL https://raw.githubusercontent.com/CesarPetrescu/ledger/main/install.sh | sh -s -- --server https://ledger.example.com --name "My laptop"
```

The URL is saved in the local profile. You do not need to supply it each time you
start Codex or check the connection.

### 3. Approve in your browser

The terminal displays a URL such as `https://ledger.example.com/admin/connect`
and a code such as `ABCD-2345`. Open the URL on your phone or laptop, sign into
the operator console, enter the matching code, review access, and approve.
Wait for the terminal to say that Ledger is connected, then start Codex:

```sh
codex
```

## Everyday Ledger commands

| Command | What it does |
|---|---|
| `ledger auth status` | Shows the saved server, permissions, and access-token expiry without printing credentials. |
| `ledger version` | Shows the installed client version. |
| `ledger update` | Checks GitHub and installs a newer stable release now. |
| `ledger auth logout` | Revokes this machine's authorization and deletes its local credentials. |
| `ledger connect codex --server https://ledger.example.com` | Repairs Codex setup or starts a new approval if authorization expired or was revoked. |

To switch to another server, first rename the existing `ledger` MCP entry in
Codex's configuration, then give the new connection a separate profile:

```sh
ledger connect codex --server https://other-ledger.example.com --profile work --name "Work laptop"
ledger auth status --profile work
ledger auth logout --profile work
```

Each `connect` points Codex's `ledger` entry at the selected profile. The default
profile is `codex`; commands without `--profile` use that default.

## Connection details and troubleshooting

- **`ledger: command not found`:** run `export PATH="$HOME/.local/bin:$PATH"`, or
  call `~/.local/bin/ledger` directly.
- **Download returns 404:** check that a stable release has been published.
- **Approval page is missing:** deploy the current Ledger server and frontend;
  migration `0006_device_auth.sql` must be applied.
- **Code expired:** rerun the connection command and approve the new code.
- **Codex setup failed after approval:** fix the reported problem, then rerun the
  same connection command; the saved credentials are reused when valid.
- **The `ledger` entry points to another server:** rename that old entry in
  Codex's configuration before reconnecting. Unrelated MCP entries are preserved.

The CLI requests `ledger:read ledger:write`; calendar access is not requested.
These are Ledger-wide memory permissions, not access restricted to a single
project. Use `ledger connect codex` for Ledger login, not `codex mcp login ledger`,
which would introduce competing OAuth credentials.

Setup adds or updates `[mcp_servers.ledger]` in `$CODEX_HOME/config.toml` (default
`~/.codex/config.toml`). It preserves unrelated setting values, backs up the
original as `config.toml.ledger-backup`, and rewrites TOML formatting/comments.
It runs `codex mcp logout ledger` to clear stored
OAuth credentials (falling back to file storage when automatic mode has no
working OS keyring), removes conflicting bearer/Authorization settings, and sets
an absolute path to the helper. It refuses to replace an entry pointing to
another server or using stdio. If an OS keyring becomes available later, run
`codex mcp logout ledger` again to clear any old Ledger credentials there. Project-level settings can override this entry;
remove conflicting `.codex/config.toml` settings in projects where needed.

`--profile` selects a local credential file; Codex's `ledger` entry points to the
most recently configured profile. Repeating `connect` repairs setup and refreshes
existing credentials; revoked or expired credentials trigger a new approval.
`logout` revokes the token family on Ledger before deleting the local file. If
revocation fails, credentials stay available so logout can be retried. You can
also revoke the machine from the console's Agents page, including outstanding
device approvals.

Credentials are stored under `$XDG_CONFIG_HOME/ledger` (default
`~/.config/ledger`) on Linux, with a `0700` directory and `0600` files. On Windows
they live in `%APPDATA%\ledger`, with private ACLs granting access only to the
current Windows user and SYSTEM. Links and reparse points are rejected. Refresh and logout
use a file lock; rotated credentials are replaced atomically. The
`ledger auth headers` command is intended for Codex: its stdout contains a bearer
credential, so do not paste it into a chat or logs. Someone with shell access as
the same Unix user can access those credentials. A connection lost just after a
refresh is committed can require a new device approval; the CLI does not retry a
possibly consumed refresh token automatically.

## Automatic client updates

Release binaries automatically check for a newer stable GitHub release at most
once per day when used. The check runs in a detached process, including when
Codex invokes the helper, so downloads do not delay authentication. It downloads
only this repository's matching operating-system and architecture binary, verifies the SHA-256 digest and
size from GitHub's release metadata, checks the executable's version, and
replaces the installation. Windows renames the running executable to
`ledger.exe.old` first and restores it if replacement fails; one rollback copy
is retained until the next update. The update trusts this repository's GitHub
release publishing access. The installation directory must be writable by your
user; no sudo is invoked. The next invocation uses the new version.

```sh
ledger version
ledger update                         # check and install now
LEDGER_AUTO_UPDATE=0 ledger auth status # disable automatic checks for this invocation
```

Set `LEDGER_AUTO_UPDATE=0` in the environment that launches Codex to disable its
background update checks too. Update output is kept in
`~/.config/ledger/update.log` (or the XDG configuration directory), or
`%APPDATA%\ledger\update.log` on Windows. Codex contains Windows helper processes
in a job that may terminate their background checks; run `ledger update` in a
regular terminal to reliably update the Windows client. An unavailable
release or failed verification preserves the current binary. Development builds
report `dev` and skip automatic replacement; `ledger update` can switch one to a
published release.

## Build the client from source

For development on Linux:

```sh
git clone https://github.com/CesarPetrescu/ledger.git
cd ledger
mkdir -p "$HOME/.local/bin"
go build -trimpath -ldflags="-s -w" -o "$HOME/.local/bin/ledger" ./cmd/ledger
export PATH="$HOME/.local/bin:$PATH"
ledger connect codex --server https://ledger.example.com --name "My laptop"
```

On Windows, build and test from PowerShell:

```powershell
go test ./cmd/ledger
go build -trimpath -o ledger.exe ./cmd/ledger
.\ledger.exe connect codex --server https://ledger.example.com --name "My PC"
```

Go uses the toolchain selected in `go.mod`. Development builds report `dev` and
do not auto-update; `ledger update` can replace one with a published release.
