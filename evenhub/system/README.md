# Ledger Glass system tests

This suite drives the **official native Even Hub simulator**, not a handwritten browser imitation of the glasses. The application talks to the production Ledger services over real HTTPS. The suite stays in PR #34.

## Topology

```text
Official simulator (SDK + native WebView + LVGL)
  -> built web app / packaged-file compatibility probe
  -> HTTPS :8443 -> CI TLS edge -> production nginx
  -> ledger-auth / ledger-mcp / ledger-admin / ledger-index
  -> PostgreSQL + pgvector + production migrations

Playwright Chromium -> production React owner console -> OAuth device approval
```

The CI edge serves the built client on a different HTTPS origin (:9443), so the native WebView must satisfy real CORS checks. No Vite proxy, `route.fulfill`, fake MCP responses, injected access tokens, bypassed owner login, TLS-verification disabling, or SQLite replacement is used.

Data is fictional and is written through the real owner API. PostgreSQL is read only to observe the pending user-visible device code and check persistence. The harness never creates credentials or approves a device in SQL. Approval is performed in the actual owner UI. Network failure is induced by stopping/restarting the actual MCP container.

## Executable journeys

The runner records an outcome for every planned case. If a prerequisite fails, dependent cases are **not_run**, not passed.

- HTTPS, unauthenticated rejection, API preflights, and exclusion of admin cookie endpoints from CORS.
- Real browser login, CSRF rejection, read-only permission review, and device-code approval.
- Empty registry, actual focus selection, native first-item/index-zero selection, and another project.
- Twenty-five projects across native list pages, including the last project.
- Another client appends a real database entry; Refresh must show it without changing projects.
- Context-menu open/dismiss with framebuffer preservation.
- Simulator cold restart with persisted credentials and no silent new registration.
- A separately approved real read-only MCP client cannot write; stored entries remain unchanged.
- Actual MCP service outage/error display and recovery.
- Owner revocation requires new approval; denied approval displays an error.
- Native exit-dialog rendering and uncaught-console-error checks.

Screen observations are emitted after bridge acknowledgement, without bodies, codes, or credentials. Tests combine observations with actual database/API results and decoded 576x288 framebuffer assertions. These are not substitute renderers.

## Required gates and evidence

`client-checks` includes the simulator matrix. Draft PRs exercise the implemented foundation. Moving a PR to Ready for review automatically enables `GLASS_REQUIRE_FULL_APP=true`; it fails until Capture, Recall, Brief and Calendar have executable passing journeys. A foundation pass must never be called full-app acceptance.

Each leg uploads native RGBA screenshots, owner-console screenshots, JUnit XML, JSON coverage/receipt reports, sanitised logs, dependency versions, tested commit, and the actual CI `.ehpk`. Failure evidence is uploaded with `if: always()`. Private keys, cookies, tokens, and simulator credential storage are excluded. Cleanup removes the deployment, volumes, temporary certificate trust entries and private runtime directory.

The `package` target currently probes direct `.ehpk` URL loading in the installed official simulator. If it returns a blank WebView, this is a **failure**, not a successful packaged-app test. Do not replace it with an invented package loader or claim package installation was tested just because `evenhub pack` succeeded. Inspect the pinned tooling and document supported alternatives explicitly.

## Fidelity boundaries

| Boundary | Tested | Not established |
| --- | --- | --- |
| Official simulator | Real SDK calls, native renderer, supported inputs/menu, WebView networking | Physical R1 source identity, BLE loss/timing, battery, optical readability |
| HTTPS/backend | Actual proxy/service/auth/database on disposable deployment | Production credentials, provider configuration, internet latency |
| Audio | Virtual silent input for native process initialisation | Microphone acoustics or STT accuracy; Capture is not implemented |
| Inference | Real service running with inference unavailable | Real embedding/reranking model; Recall UI is not implemented |
| Calendar | Existing backend is present | Connected Nextcloud and Calendar UI journey, not yet implemented |
| Restart | Persistence of the actual installed simulator | Android permissions, host eviction, physical phone locking/BLE reconnection |

Unsupported physical lifecycle/source events belong in separately labelled bridge-contract tests when their handlers exist. Mocks must stop at that boundary, not replace Ledger OAuth, storage writes, or search results. Real STT and Nextcloud tests should accompany those features in this same PR, with recorded audio/disposable providers rather than production accounts.

## Reproduce

Use a disposable Ubuntu 24.04 machine with Docker Compose and the prerequisites listed in `.github/workflows/glass-system.yml`. This script installs then removes a uniquely named temporary CI certificate in OS/NSS trust stores; do not run on production.

```sh
npm --prefix evenhub ci
npm --prefix evenhub/system ci
(cd evenhub/system && npx playwright install --with-deps chromium)
GLASS_TARGET=web bash evenhub/system/run.sh
GLASS_TARGET=package bash evenhub/system/run.sh
# Intentionally blocked until the complete app has executable journeys:
GLASS_TARGET=web GLASS_REQUIRE_FULL_APP=true bash evenhub/system/run.sh
```

No real credentials are needed. Ports bind to loopback, credentials are generated per run, and the compiled package points at the disposable local deployment, not the operator's live Ledger server.

Upstream: https://hub.evenrealities.com/docs/test/simulator
