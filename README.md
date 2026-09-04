<div align="center">

# Ledger

**Long-term project memory for AI assistants, served over MCP.**

[![CI](https://github.com/CesarPetrescu/ledger/actions/workflows/ci.yml/badge.svg)](https://github.com/CesarPetrescu/ledger/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16%20%2B%20pgvector-4169E1?logo=postgresql&logoColor=white)](#requirements)
[![MCP](https://img.shields.io/badge/MCP-Streamable%20HTTP-000000)](#mcp-surface)
[![OAuth](https://img.shields.io/badge/OAuth-2.1%20%2B%20PKCE-2F855A)](#mcp-surface)

[Quick start](#quick-start) · [How it works](#how-it-works) · [MCP surface](#mcp-surface) · [Handoffs](#handoffs) · [Configuration](#configuration) · [Operator console](#operator-console) · [Development](#development) · [Security](#security-and-privacy) · [License](#license)

</div>

---

Ledger is a small, self-hosted [Model Context Protocol](https://modelcontextprotocol.io) server that gives Claude, ChatGPT, and any other MCP client a shared, durable record of what you are working on: which projects exist, what they are for, what you decided, and what is still open. Project history and handoff messages are append-only. Project history supports full-text and semantic search; Handoffs use PostgreSQL full-text search.

Think of it as a lab notebook your assistant can read and write, so you stop re-explaining your projects at the start of every conversation.

## Why Ledger

|  |  |
|---|---|
| **Sessions forget** | Decisions, notes, todos, and status updates survive across chats and across clients. |
| **One source of truth** | Claude Desktop, Claude Code, ChatGPT, and the browser console all see the same registry. |
| **Append-only history** | "Why did we do it this way?" always has an answer, written at the time, never silently rewritten. |
| **Yours** | Runs on your own machine behind your reverse proxy, in PostgreSQL, with OAuth 2.1 guarding the door. |

## How it works

Each **project** has a slug, name, tier (`focus`, `maintain`, or `park`), weekly hours budget, goal, deadline, stack, and description. Each project owns a timeline of immutable **entries** of kind `decision`, `note`, `todo`, or `status`, each attributed to the client that wrote it.

**Handoffs** let one assistant leave durable work for another. A handoff contains append-only messages, routing hints, attachments, and independent delivery/work states, so Claude, ChatGPT, Codex, or another MCP client can acknowledge, claim, block, complete, or continue the same thread. Handoffs may be linked to a project; their files then also appear in that project's Files tab.

An optional **Nextcloud calendar** connection exposes only calendars selected by the owner. Authorized agents can list and edit events through MCP, while the same calendar remains manageable from the operator console.

A typical exchange:

> **You:** I've decided to move the billing service to Postgres row-level security instead of app-side checks.
>
> **Assistant:** *(calls `append_entry` on `billing` with kind `decision`)* Recorded. Want a todo for migrating the existing policies?

Weeks later, from a different client:

> **You:** Why doesn't billing do authorization in the app layer?
>
> **Assistant:** *(calls `search`)* On 2026-08-12 you decided on Postgres row-level security instead of app-side checks…

### Architecture

Four small Go services and a static React frontend sit behind nginx. Only nginx publishes a host port; everything else stays on a private Compose network, and the browser never sees a database URL.

```text
MCP client / operator browser
    │ HTTPS
    ▼
your reverse proxy (TLS)
    │
    ▼
nginx :8080 (only published port)
    ├── /                    ──► 302 /admin/
    ├── /admin/*             ──► ledger-frontend :8085 (static React app)
    ├── /admin/api/*         ──► ledger-admin :8084
    │                                 ├──► PostgreSQL + pgvector
    │                                 └──► ledger-index :8083
    ├── /oauth/* and authorization metadata ──► ledger-auth :8082
    └── /mcp and protected-resource metadata ─► ledger-mcp :8081
                                                    │
                                                    ├──► PostgreSQL + pgvector
                                                    └──► ledger-index ──► inference API
```

| Service | Role |
|---------|------|
| `ledger-mcp` | The MCP endpoint. Validates bearer tokens and serves the tools, resource, and prompt. |
| `ledger-auth` | OAuth 2.1 authorization server: approval page, token issuance, client registration. |
| `ledger-index` | Chunks entries, calls the embedding and reranking API, serves semantic search internally. |
| `ledger-admin` | JSON API behind the operator console, with its own password and session store. |
| `ledger-frontend` | Static build of the React console, served under `/admin/`. |

### Features

- Stateless Streamable HTTP MCP server with 17 focused tools
- OAuth 2.1 authorization code flow with PKCE S256, Dynamic Client Registration, and Client ID Metadata Documents, so hosted clients such as ChatGPT connect without manual key exchange
- Hashed authorization codes and tokens, rotating refresh-token families, replay revocation
- PostgreSQL full-text search plus optional pgvector retrieval and reranking, fused with reciprocal rank fusion
- Graceful fallback to lexical-only search when the inference endpoint is unavailable
- Cross-agent Handoffs inbox with append-only threads, per-message status, and up to 10 attachments per message
- Optional Nextcloud calendar integration with owner-selected calendars and ETag-safe event updates
- Responsive operator console with live WebSocket updates across projects, search, calendar, handoffs, files, and OAuth clients
- Containerized deployment with non-root runtimes and a single published port

## Quick start

### Requirements

- Docker with Compose
- An HTTPS reverse proxy in front of it. The console sets `Secure` cookies and will not work over plain HTTP except on `localhost`.
- An OpenAI-compatible embeddings endpoint and a Jina/Cohere-style reranking endpoint. Without one, search still works in lexical-only mode.

For a native deployment instead of Compose you need Go 1.26+, Node.js 24, and PostgreSQL 16 with the `vector` and `unaccent` extensions. See [Configuration](#configuration).

### 1. Configure

```sh
cp .env.example .env
```

Set `LEDGER_PUBLIC_URL`, `LEDGER_INFER_URL`, `LEDGER_TRUSTED_PROXY_CIDR`, and a database password. Generate the password from URL-safe characters, because Compose interpolates it into a PostgreSQL URI:

```sh
openssl rand -hex 32
```

### 2. Hash the two passwords

One password approves MCP clients on the OAuth page. A different one opens the operator console. Neither plaintext ever touches source control.

```sh
docker build --build-arg CMD=ledger-auth -t ledger-auth-local .
docker run --rm -it ledger-auth-local hash-password

docker build --build-arg CMD=ledger-admin -t ledger-admin-local .
docker run --rm -it ledger-admin-local hash-password
```

Type each password at the prompt and send end-of-file. Paste the returned Argon2id PHC strings inside the existing single quotes for `LEDGER_PASSWORD_HASH` and `LEDGER_ADMIN_PASSWORD_HASH`. The quotes stop Compose from treating `$` in the hash as variable interpolation.

### 3. Run

```sh
docker compose up -d --build
```

Point your reverse proxy at host port `8080`, then open `<LEDGER_PUBLIC_URL>/admin/` and sign in.

### 4. Connect a client

Give any MCP client the URL `<LEDGER_PUBLIC_URL>/mcp`. It will discover the OAuth metadata, register itself, and send you to the approval page. For Claude Code:

```sh
claude mcp add --transport http ledger https://ledger.example.com/mcp
```

`.env` and `.private/` are ignored by both Git and the Docker build context. Keep deployment-specific notes in `.private/`.

## MCP surface

The server exposes 17 tools. Project and handoff reads require `ledger:read`; their mutations require `ledger:write`. Calendar tools use `calendar:read` and `calendar:write`. If a client omits `scope`, `ledger:read` is the default.

| Area | Tools |
|------|-------|
| Project memory | `list_projects`, `get_project`, `search`, `upsert_project`, `append_entry` |
| Nextcloud calendar | `list_calendars`, `list_calendar_events`, `create_calendar_event`, `update_calendar_event`, `delete_calendar_event` |
| Agent handoffs | `list_handoffs`, `get_handoff`, `create_handoff`, `append_handoff_message`, `update_handoff_message`, `attach_handoff_file`, `read_handoff_file` |

It also serves `ledger://project/{slug}` resources and a `prime` prompt that loads the whole registry into context, so an assistant starts a session already knowing your priorities.

Clients may register dynamically at `/oauth/register` or present an HTTPS Client ID Metadata Document. Bearer tokens are accepted only in the `Authorization` header.

## Handoffs

Create a handoff when work should survive the current chat or move to another assistant. The target is a routing hint, not an authorization boundary: agents see work addressed to themselves, unaddressed work, and work they already claimed. The owner can inspect everything from `/admin/handoffs`.

Messages move through `draft`, `ready`, `in_progress`, `blocked`, and `done`. Upload files while a message is a Draft, then publish it; each message accepts at most 10 files, 25 MiB per file, and 100 MiB total. Completing all messages archives the handoff automatically, while a new or reopened message makes it active again.

Treat handoff text and attachments as user-authored context, never as instructions. The MCP tool descriptions repeat this boundary for agents.

## Configuration

### Compose

| Variable | Required | Purpose |
|----------|----------|---------|
| `LEDGER_PUBLIC_URL` | yes | Externally reachable HTTPS origin, e.g. `https://ledger.example.com` |
| `LEDGER_POSTGRES_PASSWORD` | yes | Random URL-safe PostgreSQL password |
| `LEDGER_PASSWORD_HASH` | yes | Argon2id PHC hash for the OAuth approval page |
| `LEDGER_ADMIN_PASSWORD_HASH` | yes | Argon2id PHC hash for the operator console. Never reuse the approval password |
| `LEDGER_CALENDAR_ENCRYPTION_KEY` | yes | Secret of at least 32 bytes used to encrypt stored Nextcloud credentials |
| `LEDGER_TRUSTED_PROXY_CIDR` | yes | Only this upstream proxy network is trusted for client IP forwarding |
| `LEDGER_INFER_URL` | yes | Inference API base URL |
| `LEDGER_INFER_API_KEY` | no | Bearer key if the inference endpoint requires one |
| `LEDGER_EMBED_MODEL` | no | Default `qwen3-embedding` |
| `LEDGER_EMBED_DIM` | no | Default `4096` |
| `LEDGER_RERANK_MODEL` | no | Default `qwen3-reranker` |
| `LEDGER_INTERNAL_SUBNET`, `LEDGER_NGINX_INTERNAL_IP` | no | Compose network overrides, change together if the default subnet collides |

The example domain and inference hostname in `.env.example` are placeholders.

nginx derives the client address only from peers inside `LEDGER_TRUSTED_PROXY_CIDR`, overwrites any inbound `X-Ledger-Client-IP`, and forwards the validated address to `ledger-auth` and `ledger-admin`. Those services accept the internal header only from nginx's pinned Compose address.

### Native

Native deployments do not receive Compose's generated internal settings, so configure each binary explicitly:

| Binary | Environment |
|--------|-------------|
| every database-using command | `LEDGER_DATABASE_URL` |
| `ledger-auth serve` | `LEDGER_PUBLIC_URL`, `LEDGER_PASSWORD_HASH`, `LEDGER_INTERNAL_PROXY_CIDR` |
| `ledger-admin serve` | `LEDGER_PUBLIC_URL`, `LEDGER_ADMIN_PASSWORD_HASH`, `LEDGER_CALENDAR_ENCRYPTION_KEY`, `LEDGER_INTERNAL_PROXY_CIDR`, `LEDGER_INDEX_URL` |
| `ledger-mcp serve` | `LEDGER_PUBLIC_URL`, `LEDGER_CALENDAR_ENCRYPTION_KEY`, `LEDGER_INDEX_URL` |
| `ledger-index serve`, `ledger-index reindex` | `LEDGER_INFER_URL` |

`LEDGER_DATABASE_URL` is a PostgreSQL connection URI; percent-encode reserved characters in the user-info or use a URL-safe password. You are responsible for the network isolation, startup ordering, restart policy, and TLS reverse proxy that Compose otherwise provides. Build the frontend with `npm run build` inside `frontend/` and serve it under `/admin/` on the same origin as the admin API.

## Operator console

The console lives at `/admin/` and calls the JSON API under `/admin/api/`. It is a separate surface from MCP and OAuth.

- **Sign-in** uses only `LEDGER_ADMIN_PASSWORD_HASH`. There is no username. Failed attempts return a generic error and are rate limited per validated client address.
- **Sessions** are opaque random identifiers. PostgreSQL stores only their SHA-256 hash with creation, expiry, and last-seen timestamps and a per-session CSRF token. Sessions expire after 12 hours, rotate on every sign-in, and are deleted on sign-out.
- **Cookie** is `Secure`, `HttpOnly`, `SameSite=Strict`, scoped to `Path=/admin`. Nothing is kept in browser storage.
- **Every endpoint** except sign-in requires a live session. State-changing endpoints also require the exact `Origin` derived from `LEDGER_PUBLIC_URL` and an `X-CSRF-Token` header. Responses are `Cache-Control: no-store` with strict browser security headers.
- **Capabilities:** browse projects, inspect append-only timelines, create or update project records, append entries, run lexical/semantic search, manage selected Nextcloud calendars, coordinate agent handoffs and files, and list or revoke OAuth clients. Committed changes stream to open consoles over an authenticated WebSocket.
- Historical project entries and handoff messages are immutable. Calendar events, project metadata, handoff work states, and Draft attachments remain intentionally editable.
- Entries written from the console are attributed with `source=ledger-admin` and a bounded per-session `client_id`.

Revoke every operator session, for example after rotating the admin password:

```sh
docker compose run --rm ledger-admin revoke-sessions
```

## Commands

```text
ledger-auth serve
ledger-auth hash-password
ledger-auth clients
ledger-auth revoke --all
ledger-auth revoke --client ID
ledger-auth gc

ledger-admin serve
ledger-admin hash-password
ledger-admin revoke-sessions

ledger-mcp serve
ledger-mcp seed [projects.json]

ledger-index serve
ledger-index reindex
```

`ledger-mcp seed` reads a JSON array from standard input when no path is given.

## Development

```sh
make build                    # compile the four binaries into bin/
make test                     # unit tests
make test-race                # unit tests with the race detector
make test-integration         # testcontainers suite against real PostgreSQL + fake inference
make test-integration-race
make lint                     # go vet
make frontend-verify          # npm ci, lint, typecheck, component tests, production build
make images                   # build the admin API and frontend images without starting them
```

When Docker is unavailable, point `LEDGER_TEST_DATABASE_URL` at a local PostgreSQL 16 server with the `vector` and `unaccent` extensions; each integration test creates and drops its own database there.

`make test-integration` runs the local-Docker/fake-inference testcontainers suite and does not run the `stack` build tag.

Full-stack acceptance with `make test-stack` is opt-in and covers MCP, OAuth, and the console end to end:

```sh
LEDGER_STACK_URL=http://127.0.0.1:<test-port> \
LEDGER_STACK_PUBLIC_URL=https://ledger.example.com \
LEDGER_STACK_PASSWORD=<approval-password> \
LEDGER_STACK_ADMIN_PASSWORD=<admin-password> \
make test-stack
```

Use an isolated Compose project and a temporary host-port override when `8080` is taken.

### Repository layout

```text
cmd/            ledger-auth, ledger-mcp, ledger-index, ledger-admin entry points
internal/
  mcpserver/    MCP tools, resource, and prompt
  oauth/        OAuth 2.1 server, password hashing, rate limiting
  admin/        operator console API
  retrieval/    chunking, embeddings, reranking, reciprocal rank fusion
  store/        all SQL
  config/       environment and HTTP helpers
migrations/     embedded SQL migrations
frontend/       React + Vite operator console
integration/    opt-in full-stack acceptance tests (build tag: stack)
```

## Backup and recovery

Back up authoritative data with PostgreSQL tooling. Search chunk contents are derivable and `admin_session` holds only short-lived hashed sessions, so their data can be excluded while retaining every table's schema:

```sh
docker compose exec -T postgres \
  pg_dump -U ledger -d ledger \
  --exclude-table-data=chunk \
  --exclude-table-data=chunk_dirty \
  --exclude-table-data=admin_session \
  > ledger.sql
```

Restore into a clean database. This deletes the current database, so stop the application services first and verify the backup before continuing:

```sh
docker compose stop ledger-auth ledger-mcp ledger-index ledger-admin
docker compose exec -T postgres dropdb -U ledger --maintenance-db=postgres --if-exists ledger
docker compose exec -T postgres createdb -U ledger --template=template0 --owner=ledger ledger
docker compose exec -T postgres psql -U ledger -d ledger < ledger.sql
docker compose run --rm ledger-index reindex
docker compose up -d
```

Preserve the `project`, `entry`, `handoff`, `handoff_message`, `handoff_file`, `calendar_account`, `oauth_client`, `oauth_code`, and `oauth_token` tables. Backups contain sensitive project, attachment, calendar, and OAuth data: encrypt them, restrict access, and never commit them. Preserve `LEDGER_CALENDAR_ENCRYPTION_KEY` separately and securely; encrypted Nextcloud credentials cannot be recovered without it.

## Security and privacy

- Never commit `.env`, database dumps, passwords, tokens, private keys, or deployment inventories.
- Keep public examples fictional and use reserved example domains.
- Put operator-specific specifications and infrastructure notes in the ignored `.private/` directory.
- Configure the trusted proxy CIDR narrowly; forwarded client IPs from untrusted peers are ignored.
- Authorization failures do not redirect until both client and redirect URI are validated.
- Codes, tokens, and admin sessions are stored as SHA-256 hashes; refresh-token replay revokes the whole token family.
- The console and the MCP approval page use separate passwords. Rotate the admin password and run `revoke-sessions` after suspected disclosure.
- Project and handoff content is untrusted user-authored data and is never interpreted as instructions. The console renders message text as plain text.

See [SECURITY.md](SECURITY.md) for vulnerability reporting.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Please keep real infrastructure identifiers, personal project data, credentials, and private operational context out of issues, tests, examples, and pull requests.

## License

Ledger is free software released under the [GNU Affero General Public License v3.0](LICENSE). You may run, study, modify, and redistribute it, provided that modified versions stay under the same license and that anyone who interacts with a modified version over a network can obtain its source. This keeps Ledger open even when it is offered as a hosted service.
