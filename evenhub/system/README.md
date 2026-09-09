# Ledger Glass system tests

PR #34 adds two required suites. Each builds its own disposable **production Ledger Compose stack**, keeping authentication, database behaviour and rate limits real.

## Native system suite

```text
Official Even Hub simulator: native WebView + SDK + LVGL
    -> production-built Ledger Glass, HTTPS :9443
    -> HTTPS :8443 -> CI TLS edge -> production nginx
    -> real Ledger OAuth / MCP / admin services
    -> PostgreSQL + pgvector + production migrations

Playwright Chromium -> production React owner console -> real device approval
```

The index service is also running, but actual model retrieval and the unimplemented Recall UI are not claimed as covered. The native suite has no mocked HTTP replies, Vite proxy, injected access token, SQLite substitute, or TLS bypass. Fictional data is written through the actual owner API. SQL only observes user-visible pending codes and client counts, never creates credentials or approves a device. Outage testing stops/restarts the real MCP container.

Fourteen journeys exercise HTTPS/CORS and anonymous rejection; browser login/CSRF; read-only approval; empty registry; real NOW data; first-item selection; 25-project pagination; external entry writes followed by Refresh; menu preservation; forced restart recovery; denied writes; real service outage/recovery; revocation; denied approval; and receipt of the native exit request. Native input waits for the actual context-menu close event, and screenshots wait for settled framebuffers.

The exit probe independently requires the native process to log `ShutDownPageContainer` and the framebuffer to change. Simulator 0.9.5 clears the page rather than showing the physical OS confirmation dialog; blank output is allowed only for this explicitly labelled exit probe. Confirmation/cancellation remains unverified, not counted as a successful dialog test.

## Host-storage contract suite — one narrowly scoped double

The observed simulator 0.9.5 session was lost after forced process termination. The native test **does not inject a replacement token**: it verifies real re-approval and recovery and records `native_session_restored: false` in the platform observations. This does not establish what a physical Even App host will do.

A separate suite imports the real application `LedgerAuth` class. **Only `getLocalStorage` and `setLocalStorage` are doubled.** All login, device authorization, access/refresh issuance, rotation and revocation go through the real HTTPS server and PostgreSQL. Four cases cover restored grant reuse without new registration, real token rotation with coherent persistence, re-approval after owner revocation, and refusal to claim success when host persistence fails.

These are storage-boundary contract tests, not proof of native persistent storage or encryption. Each suite gets a separate real deployment so tests do not bypass or reset production rate limits.

## Packaging boundary

Every run builds an actual `.ehpk` with official CLI 0.1.14, checks EHPK magic/nonempty output, records manifest and SHA-256 hashes of package/build inputs, and requires the official packer to reject missing entrypoints and invalid package IDs without producing output.

**Native package installation is not claimed.** Direct EHPK-URL trials on simulator 0.9.5 produced a blank WebView; the installed CLI exports creation, not an unpack API. Runtime tests use the exact production build supplied to the official packer, not an invented package loader. The unsupported URL probe remains available through manual workflow input `simulator_target=package` and fails when no application starts. Actual Even App installation remains a hardware acceptance item.

## Required gates and missing features

`client-checks` includes both suites. Draft PRs test implemented flows. Review-ready PRs and main/release CI also require executable Capture, Recall, Brief and Calendar journeys. Those UIs are not implemented yet: missing journeys explicitly fail the full-app gate and are never replaced by success mocks. Hardware acceptance remains separate.

Failed prerequisites leave dependent native cases `not_run`, never passed. Reports identify the exact tested merge commit, actual component coverage, substitutions, and limitations. Contract-suite reports are separate from native framebuffer reports.

## Artifacts and privacy

Each run uploads JSON/JUnit reports, sanitised logs, version/commit receipts, package hashes and the actual CI package. The native suite adds raw 576x288 RGBA screenshots, black-composited previews, an HTML gallery, and owner-console screenshots. Preview conversion is not a design mock; raw frames are retained for alpha-channel assertions.

Failures upload evidence with `if: always()`. Credentials, cookies, private keys and simulator storage are excluded. Credentials are generated per run; exposed ports bind to loopback; production is never contacted. Cleanup removes containers, volumes, temporary OS/NSS certificate trust entries and private runtime data.

## Remaining hardware/provider limits

Physical R1 source identity, BLE timing/loss, optical readability, battery use, Android host permissions/eviction, OS exit-confirmation/cancellation, and actual Even App storage/package installation remain unverified here. Virtual silent audio only allows native simulator startup; it does not prove microphone or STT accuracy. Actual Nextcloud and speech/model providers should be tested with disposable instances/recorded audio when their app features are implemented in this same PR.

## Reproduce

Use a disposable Ubuntu 24.04 machine with Docker Compose and prerequisites from `.github/workflows/glass-system.yml`. The script temporarily installs and removes a uniquely named local certificate; do not run on production.

```sh
npm --prefix evenhub ci
npm --prefix evenhub/system ci
(cd evenhub/system && npx playwright install --with-deps chromium)
GLASS_SUITE=native GLASS_TARGET=web bash evenhub/system/run.sh
GLASS_SUITE=host-contract GLASS_TARGET=web bash evenhub/system/run.sh
# Complete-app gate (blocked until the missing journeys are implemented):
GLASS_REQUIRE_FULL_APP=true GLASS_TARGET=web bash evenhub/system/run.sh
# Optional vendor package-URL failure diagnostic:
GLASS_TARGET=package bash evenhub/system/run.sh
```

Official reference: https://hub.evenrealities.com/docs/test/simulator
