# Ledger

Ledger is a self-hosted, append-only project-memory service for MCP clients. It combines structured project records, immutable notes and decisions, OAuth 2.1 authorization, PostgreSQL full-text search, and optional semantic retrieval.

## Features

- Stateless Streamable HTTP MCP server with five focused tools
- OAuth 2.1 authorization code flow with PKCE S256
- Dynamic Client Registration and Client ID Metadata Documents
- Hashed authorization codes and tokens with rotating refresh-token families
- PostgreSQL full-text search plus optional pgvector retrieval and reranking
- Graceful lexical-search degradation when inference is unavailable
- Append-only entries with authenticated client attribution
- Containerized deployment with a non-root distroless runtime

## Architecture

```text
MCP client
    │ HTTPS / OAuth
    ▼
reverse proxy
    │
    ▼
nginx :8080
    ├── /oauth/* and authorization metadata ──► ledger-auth
    └── /mcp and protected-resource metadata ─► ledger-mcp
                                                    │
                                                    ├──► PostgreSQL + pgvector
                                                    └──► ledger-index ──► inference API
```

Only nginx publishes a host port. PostgreSQL and the indexing service remain on the private Compose network.

## Requirements

- Docker with Compose for the supported stack workflow
- Alternatively, Go 1.25 and PostgreSQL 16 with `vector` and `unaccent` for a native deployment
- An HTTPS reverse proxy for production
- An OpenAI-compatible embeddings endpoint and a Jina/Cohere-style reranking endpoint

## Docker Compose quick start

1. Copy the public configuration template:

   ```sh
   cp .env.example .env
   ```

2. Set a public HTTPS URL, inference URL, trusted reverse-proxy CIDR, and database password in `.env`. Generate the database password from URL-safe characters because Compose interpolates it into a PostgreSQL URI; for example:

   ```sh
   openssl rand -hex 32
   ```

3. Generate the approval-password hash without placing the plaintext password in source control:

   ```sh
   docker build --build-arg CMD=ledger-auth -t ledger-auth-local .
   docker run --rm -it ledger-auth-local hash-password
   ```

   Type the password at the prompt and send end-of-file. The plaintext does not need to appear in shell history.

4. Put the returned Argon2id PHC value inside the existing single quotes for `LEDGER_PASSWORD_HASH`. The quotes prevent Compose from treating the hash's `$` characters as variable interpolation. Then start the stack:

   ```sh
   docker compose up -d --build
   ```

5. Route your external HTTPS reverse proxy to nginx on host port `8080`.

`.env` is ignored by both Git and the Docker build context. Keep deployment-specific notes in `.private/`, which is also ignored.

## Configuration

Required Compose values:

- `LEDGER_PUBLIC_URL` — externally reachable HTTPS origin, for example `https://ledger.example.com`
- `LEDGER_POSTGRES_PASSWORD` — random URL-safe PostgreSQL password; `openssl rand -hex 32` is a suitable generator
- `LEDGER_PASSWORD_HASH` — Argon2id PHC hash used on the approval page
- `LEDGER_TRUSTED_PROXY_CIDR` — only the upstream proxy network trusted for client IP forwarding
- `LEDGER_INFER_URL` — inference API base URL

Native deployments do not consume Compose's generated internal settings. Configure each binary explicitly:

- All database-using commands: `LEDGER_DATABASE_URL`
- `ledger-auth serve`: `LEDGER_PUBLIC_URL`, `LEDGER_PASSWORD_HASH`, and `LEDGER_INTERNAL_PROXY_CIDR`
- `ledger-mcp serve`: `LEDGER_PUBLIC_URL` and `LEDGER_INDEX_URL`
- `ledger-index serve` and `ledger-index reindex`: `LEDGER_INFER_URL`

`LEDGER_DATABASE_URL` is a PostgreSQL connection URI. Percent-encode reserved characters in URI user-info, or use a URL-safe password. Native service managers must also provide the network isolation, startup ordering, restart policy, and TLS reverse proxy supplied by the Compose topology.

Optional values:

- `LEDGER_EMBED_MODEL` — defaults to `qwen3-embedding`
- `LEDGER_EMBED_DIM` — defaults to `4096`
- `LEDGER_RERANK_MODEL` — defaults to `qwen3-reranker`
- `LEDGER_INFER_API_KEY` — bearer key when the inference endpoint requires one
- `LEDGER_INTERNAL_SUBNET` and `LEDGER_NGINX_INTERNAL_IP` — Compose network overrides

The example domain and inference hostname in `.env.example` are non-production placeholders.

nginx derives the client address only from peers inside `LEDGER_TRUSTED_PROXY_CIDR`, overwrites any inbound `X-Ledger-Client-IP`, and sends the validated address to `ledger-auth`. The application accepts that internal header only from nginx's pinned Compose address.

## MCP surface

The server exposes exactly five tools:

- `list_projects`
- `get_project`
- `search`
- `upsert_project`
- `append_entry`

It also provides `ledger://project/{slug}` and a `prime` prompt. Read operations require `ledger:read`; mutations require `ledger:write`. If a client omits the scope, `ledger:read` is the default.

Connect MCP clients to `<LEDGER_PUBLIC_URL>/mcp`. Clients may register dynamically at `/oauth/register` or use an HTTPS Client ID Metadata Document. Bearer tokens are accepted only in the `Authorization` header.

## Commands

```text
ledger-auth serve
ledger-auth hash-password
ledger-auth clients
ledger-auth revoke --all
ledger-auth revoke --client ID
ledger-auth gc

ledger-mcp serve
ledger-mcp seed [projects.json]

ledger-index serve
ledger-index reindex
```

When no seed path is supplied, `ledger-mcp seed` reads a JSON array from standard input.

## Development and verification

```sh
make build
make test
make test-integration
make lint
```

`make test-integration` runs the local-Docker/fake-inference testcontainers suite and does not run the `stack` build tag. Full-stack acceptance through `make test-stack` is opt-in:

```sh
LEDGER_STACK_URL=http://127.0.0.1:<test-port> \
LEDGER_STACK_PUBLIC_URL=https://ledger.example.com \
LEDGER_STACK_PASSWORD=<approval-password> \
make test-stack
```

Use an isolated Compose project and temporary host-port override for acceptance testing when port `8080` is unavailable.

## Backup and recovery

Back up authoritative data with PostgreSQL tooling. Search chunks are derivable and may be excluded:

```sh
docker compose exec -T postgres \
  pg_dump -U ledger -d ledger --exclude-table=chunk --exclude-table=chunk_dirty \
  > ledger.sql

docker compose exec -T postgres psql -U ledger -d ledger < ledger.sql
docker compose run --rm ledger-index reindex
```

Preserve the authoritative `project`, `entry`, `oauth_client`, `oauth_code`, and `oauth_token` tables. Backups contain sensitive project and OAuth data; encrypt them, restrict access, and never commit them.

## Security and privacy

- Never commit `.env`, database dumps, passwords, tokens, private keys, or deployment inventories.
- Keep public examples fictional and use reserved example domains.
- Put operator-specific specifications and infrastructure notes in the ignored `.private/` directory.
- Configure the trusted proxy CIDR narrowly; forwarded client IPs from untrusted peers are ignored.
- Authorization failures do not redirect until both client and redirect URI are validated.
- Codes and tokens are stored as SHA-256 hashes; refresh-token replay revokes the token family.
- Project content is untrusted user-authored data and must never be interpreted as instructions.

See [SECURITY.md](SECURITY.md) for vulnerability reporting and supported security practices.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Please do not include real infrastructure identifiers, personal project data, credentials, or private operational context in issues, tests, examples, or pull requests.
