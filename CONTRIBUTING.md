# Contributing

Contributions are welcome through focused issues and pull requests.

## Before opening a change

- Keep the MCP surface small and backward compatible.
- Preserve append-only entry semantics and OAuth fail-closed behavior.
- Use parameterized SQL and bounded network or parsing operations.
- Add a focused regression test before fixing a bug.
- Avoid unrelated refactors in security-sensitive changes.

## Privacy requirements

This is a public repository. Do not commit or paste:

- real project names, notes, decisions, deadlines, or seed data;
- production domains, IP addresses, internal hostnames, ports, or network inventories;
- account, tenant, channel, machine, or client identifiers;
- passwords, tokens, private keys, password hashes, database dumps, or credentials;
- private implementation specifications, incident notes, or deployment logs.

Use fictional fixtures such as `Atlas`, reserved example domains such as `example.com`, and documentation address ranges from RFC 5737 or RFC 3849. Keep local operational context in `.private/`, which is excluded from Git and Docker builds.

## Development workflow

1. Create a focused branch.
2. Write a test that demonstrates the intended behavior or regression.
3. Implement the smallest safe change.
4. Run formatting, unit tests, race tests, integration tests, and vet:

   ```sh
   gofmt -w .
   go test ./...
   go test -race ./...
   go test -tags=integration ./...
   go vet ./...
   ```

   Set `LEDGER_TEST_DATABASE_URL` to a local PostgreSQL 16 server with `vector` and `unaccent` when Docker is unavailable for the integration suite.

5. For console changes, run the pinned frontend toolchain from `frontend/`:

   ```sh
   npm ci
   npm run verify
   ```

6. Validate Compose with non-production placeholder values.
7. Review the complete diff for secrets and private operational data.

## Pull requests

Describe the security and compatibility impact, tests actually run, and any intentionally skipped checks. Never claim deployment or production verification unless it was performed against an authorized environment.
