# Development and releases

[← Back to Ledger](../README.md)

Run commands from the repository root. Use the Go toolchain selected in [`go.mod`](../go.mod), Node.js 24 for the frontend, and Docker for integration tests. See [Android build instructions](../android/README.md) for JDK and SDK requirements.

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
cmd/            CLI and four Go service entry points
internal/
  mcpserver/    MCP tools, resource, and prompt
  oauth/        OAuth 2.1 server, password hashing, rate limiting
  admin/        operator console API
  retrieval/    chunking, embeddings, reranking, reciprocal rank fusion
  store/        all SQL
  config/       environment and HTTP helpers
migrations/     embedded SQL migrations
frontend/       React + Vite operator console
android/        Kotlin + Jetpack Compose owner app
docs/           Client, hosting, and protocol guides
integration/    opt-in full-stack acceptance tests (build tag: stack)
```

## Publishing a client release

The `Release` workflow publishes Linux and Windows x64/ARM64 clients, a signed
Android APK, and `SHA256SUMS` when a stable `vMAJOR.MINOR.PATCH` tag is pushed on
a commit merged into `main`. Publication waits for the full CI suite, including
all client checks. After publication, native Linux and Windows runners install
and execute the published assets on both x64 and ARM64.

Every pull request and push to `main` tests:

| Client | GitHub Actions coverage |
| --- | --- |
| Linux CLI | Native x64 and ARM64, race tests, installer success and download failure checks |
| Windows CLI | Native x64 and ARM64, credential protection, updater, PowerShell and npm integration, installer checks |
| Windows under Wine | Windows x64 tests in an isolated Docker container with current Wine; PowerShell/npm remain covered on native Windows |
| Android | Unit tests, lint, APK builds, and HTTPS owner flows on Android 9 (API 28) and Android 16 (API 36) emulators |
| Web console | Lint, type checking, component tests, production build, and dependency audit |

The `client-checks` job fails if any client job fails or is skipped. Android
reports and Wine logs are retained as CI artifacts for 14 days. To reproduce
Wine checks locally with Go and Docker, run `./scripts/test-wine.sh`.

```sh
git switch main
git pull --ff-only
git tag -a v0.2.0 -m "Ledger v0.2.0"
git push origin v0.2.0
```

Use a new version number for later releases. Once the release workflow finishes,
the installer and `ledger update` pick up its binaries automatically. Pushing
`main` alone does not publish a binary. Server deployment remains separate from
CLI updates.
