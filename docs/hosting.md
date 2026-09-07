# Self-hosting and operations

[← Back to Ledger](../README.md)

[Setup](#quick-start) · [Configuration](#configuration) · [Console](#operator-console) · [Commands](#commands) · [Backups](#backup-and-recovery)

Deploy Ledger, configure authentication, and manage the server. For a first deployment, follow [Quick start](#quick-start) in order. Keep [backup and recovery](#backup-and-recovery) handy before changing production data.

## Quick start

### Requirements

- Docker with Compose
- An HTTPS reverse proxy in front of it. The console sets `Secure` cookies and will not work over plain HTTP except on `localhost`.
- For semantic search, an OpenAI-compatible embeddings endpoint and a Jina/Cohere-style reranking endpoint. Set `LEDGER_INFER_URL` in Compose; if the endpoint is unavailable, search falls back to lexical-only mode.

For a native deployment instead of Compose you need Go 1.26+, Node.js 24, and PostgreSQL 16 with the `vector` and `unaccent` extensions. See [Configuration](#configuration).

### 1. Configure

```sh
git clone https://github.com/CesarPetrescu/ledger.git
cd ledger
cp .env.example .env
```

Replace every placeholder in `.env`, including `LEDGER_PUBLIC_URL`, `LEDGER_INFER_URL`, `LEDGER_TRUSTED_PROXY_CIDR`, the database password, and `LEDGER_CALENDAR_ENCRYPTION_KEY`. Use a separate random secret of at least 32 bytes for the calendar encryption key, even if calendar access is not enabled. Generate the password from URL-safe characters, because Compose interpolates it into a PostgreSQL URI:

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

For Codex, follow the [CLI connection guide](clients.md). For the owner app, follow the [Android guide](../android/README.md).

Give any MCP client the URL `<LEDGER_PUBLIC_URL>/mcp`. It will discover the OAuth metadata, register itself, and send you to the approval page. For Claude Code:

```sh
claude mcp add --transport http ledger https://ledger.example.com/mcp
```

`.env` and `.private/` are ignored by both Git and the Docker build context. Keep deployment-specific notes in `.private/`.

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
