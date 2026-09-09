<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/ledger-logo-white-transparent.png">
  <img src="assets/ledger-logo-transparent.png" alt="Ledger" width="220" height="220">
</picture>

**Project memory that carries across conversations.**

Save decisions, find past context, and hand work from one assistant to another.

[![CI](https://github.com/CesarPetrescu/ledger/actions/workflows/ci.yml/badge.svg)](https://github.com/CesarPetrescu/ledger/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/release/CesarPetrescu/ledger)](https://github.com/CesarPetrescu/ledger/releases/latest) [![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)

[Get started](#get-started) · [Install](#install-the-ledger-client) · [Self-host](#self-host-ledger) · [Documentation](#documentation)

</div>

## What is Ledger?

Ledger is a self-hosted [MCP](https://modelcontextprotocol.io) server that gives Codex, Claude, ChatGPT, and other MCP clients a shared project notebook. Your data lives in PostgreSQL on your own server.

- **Remember decisions.** Keep notes, todos, status updates, and an append-only project history.
- **Find past context.** Search across projects with full-text search and optional semantic search.
- **Hand off work.** Leave another assistant a thread with messages, attachments, and progress.
- **Stay in control.** Manage projects, connected clients, and optional Nextcloud calendars from the web console or Android app.

> “What did we decide about Atlas last week?”
>
> Your assistant searches Ledger and brings the decision into the current conversation.

## Get started

| I want to… | Start here |
| --- | --- |
| Connect Codex on Windows, Linux, or WSL | [Install the CLI](#install-the-ledger-client) |
| Connect Claude, ChatGPT, or another MCP client | [Use the MCP endpoint](#connect-another-assistant) |
| Manage Ledger from my phone | [Install the Android app](#android-app) |
| Develop/test Ledger on Even Realities G2/R1 | [Ledger Glass](#ledger-glass-g2r1) |
| Run my own Ledger server | [Self-hosting guide](docs/hosting.md#quick-start) |

Clients need an existing **HTTPS Ledger server**. Use its base address, such as `https://ledger.example.com`, for the CLI and Android app. MCP clients use the same address with `/mcp` appended.

## Install the Ledger client

Install on the machine where **Codex runs**. Windows 10/11 and Linux support x64 and ARM64, including WSL and remote Linux over SSH. You need Codex CLI **0.152 or newer**; the installer does not require Go, Docker, or administrator access.

### Windows

Run in PowerShell:

```powershell
curl.exe -fsSL https://raw.githubusercontent.com/CesarPetrescu/ledger/main/install.ps1 -o "$env:TEMP\ledger-install.ps1"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$env:TEMP\ledger-install.ps1"
```

Open a **new terminal** so the updated PATH takes effect.

### Linux and WSL

```sh
curl -fsSL https://raw.githubusercontent.com/CesarPetrescu/ledger/main/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"
```

If needed, add that export line to `~/.bashrc` or `~/.zshrc` for future terminals.

### Connect and approve

On either platform, replace the example address with your server's base HTTPS URL:

```sh
ledger connect codex --server https://ledger.example.com --name "My laptop"
```

Open the URL shown in the terminal, sign into the owner console, and approve the matching device code. Wait for the terminal to confirm the connection, then start `codex`.

| Command | Use it to… |
| --- | --- |
| `ledger auth status` | Check your connection |
| `ledger update` | Install the latest stable client |
| `ledger auth logout` | Revoke this machine's access |

The CLI checks for updates automatically. See the [client guide](docs/clients.md) for portable downloads, custom paths, profiles, update controls, and troubleshooting. Windows executables are not Authenticode-signed.

### Connect another assistant

Add this remote MCP URL to your client and complete its OAuth approval flow:

```text
https://ledger.example.com/mcp
```

For Claude Code:

```sh
claude mcp add --transport http ledger https://ledger.example.com/mcp
```

For Codex, use `ledger connect codex` above. See the [MCP reference](docs/reference.md#mcp-surface) for tools, permissions, and discovery.

### Android app

Download the signed **`ledger-android-vX.Y.Z.apk`** from [the latest release](https://github.com/CesarPetrescu/ledger/releases/latest). Requires Android 9 or newer.

Enter your server's base HTTPS URL and sign in with the **owner password** used by the web console. You can manage projects, search, handoffs, calendars, and device approvals. See [Android setup and builds](android/README.md).

### Ledger Glass (G2/R1)

`evenhub/` contains the Even Realities G2/R1 client. The first version is intentionally read-only: it pairs through Ledger's OAuth device flow, shows the current highest-priority project, and lets you browse project state without introducing a second data store.

The package is bound to one HTTPS Ledger origin at build time because Even Hub requires outbound network destinations to be declared in the app manifest. See [Ledger Glass setup, security, and hardware checks](evenhub/README.md).

## Self-host Ledger

The Docker Compose stack includes four Go services, the web console, and PostgreSQL. Put it behind an HTTPS reverse proxy.

1. Clone the repository and configure `.env` from [`.env.example`](.env.example).
2. Set separate OAuth-approval and owner-console password hashes, plus the database password and calendar encryption key.
3. Build and start the stack, then open `/admin/` on your server.

Follow the **[complete setup guide](docs/hosting.md#quick-start)** for commands and configuration. Semantic search uses an inference endpoint; if it is unavailable, full-text search still works.

## Documentation

| Guide | What's inside |
| --- | --- |
| [Client setup](docs/clients.md) | Installers, connection, profiles, troubleshooting, and updates |
| [Self-hosting](docs/hosting.md) | Deployment, configuration, console access, backups, and recovery |
| [How Ledger works](docs/reference.md) | Architecture, all 17 MCP tools, handoffs, calendars, and security model |
| [Android](android/README.md) | App usage, builds, signing, and session security |
| [Ledger Glass](evenhub/README.md) | Even Realities G2/R1 setup, pairing, interaction model, and hardware acceptance |
| [Development](docs/development.md) | Local checks, repository layout, CI coverage, and releases |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow. Keep private data and credentials out of commits and issues. Report vulnerabilities using [SECURITY.md](SECURITY.md).

## License

[GNU Affero General Public License v3.0](LICENSE).
