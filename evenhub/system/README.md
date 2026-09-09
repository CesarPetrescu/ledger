# Ledger Glass system tests

The required CI job drives the **official native Even Hub simulator** against the production Ledger Compose deployment. It stays in PR #34. It is not a handwritten imitation of the glasses.

## Real path under test

```text
Official simulator: native WebView + SDK + LVGL renderer
    -> production-built Ledger Glass, HTTPS :9443
    -> HTTPS :8443 -> CI TLS edge -> production nginx
    -> real ledger-auth / ledger-mcp / ledger-admin / ledger-index
    -> PostgreSQL + pgvector + production migrations

Playwright Chromium -> production React owner UI -> real OAuth device approval
```

Different HTTPS origins exercise actual WebView CORS. There is no Vite proxy, Playwright response interception, fake MCP response, injected access token, SQLite substitute, or disabled TLS verification. Fictional data is created through the real owner API. SQL only observes pending user-visible codes and client counts; it never inserts credentials or approves a device. Failure injection stops and restarts the actual MCP container.

## Coverage

Fourteen executable journeys cover TLS/CORS/auth rejection; actual owner login and CSRF; read-only device approval; empty registry; live project data; first-item selection; 25-project pagination; another client's entry followed by Refresh; context-menu round trips; cold restart; write-scope denial; real service outage/recovery; revocation; approval denial; and native exit-dialog rendering.

The harness waits for the native menu-close event rather than sending clicks into its closing animation. Screenshots wait for a settled framebuffer. Tests combine post-bridge-ACK observations, actual API/database receipts, decoded 576x288 alpha-channel assertions, and native gestures. A failed prerequisite leaves dependent journeys **not_run**, never passed.

Capture, Recall, Brief, and Calendar UI journeys are not implemented yet. They remain explicit missing coverage rather than success mocks. `client-checks` includes this suite; review-ready PRs and main/release CI enable the full-app gate, which fails until those journeys are implemented and pass. Physical acceptance is separate and cannot be proved by a CI checkbox.

## Packaging: supported checks versus installation

Every run builds an actual `.ehpk` with official CLI 0.1.14, validates its EHPK magic/nonempty output, records the manifest, SHA-256 of the package and every build input, and checks that the official packer rejects a missing entrypoint and invalid package ID without leaving output.

**Native EHPK installation is not claimed.** Direct EHPK-URL trials on simulator 0.9.5 produced a blank WebView, although the documentation's example mentions EHPK URLs. The installed wrapper passes the URL to the native executable; the official packer exports creation, not an unpack API. The required runtime test therefore uses the exact production build supplied to the official packer. It does not replace the package loader with an invented implementation.

The unsupported URL probe is retained for reproduction via manual workflow input `simulator_target=package` or `GLASS_TARGET=package`. It fails if no real application starts; it is not labelled a successful package test. Actual package installation remains a device acceptance item.

## Evidence and privacy

Artifacts contain raw native RGBA frames, black-composited previews and an HTML gallery, owner-console screenshots, JSON/JUnit outcomes, sanitised logs, dependency versions, tested commit, package/input checksums, and the actual CI package. Raw frames are preserved for alpha assertions; previews are a display conversion, not simulated designs.

Failures upload evidence with `if: always()`. Private keys, tokens, cookies, and simulator credential storage are excluded. Per-run random credentials, loopback ports and disposable data avoid production. Cleanup removes containers, volumes, temporary OS/NSS certificate trust entries and the private runtime directory.

## Explicit boundaries

| Component | Real in this suite | Still requires other tests |
| --- | --- | --- |
| Glasses platform | Official SDK, native renderer, supported gestures/menu | Physical R1 event-source identity, BLE timing/loss, optics, battery |
| Server | Production proxy, four Go services, PostgreSQL, migrations, OAuth, owner UI, MCP | Production environment and deployment configuration |
| Persistence | Cold restart of the actual simulator | Android permissions, host eviction, physical phone locking |
| Audio | Virtual silent input only for native startup | Actual microphone/acoustics and STT accuracy; Capture remains unimplemented |
| Inference/calendar | Real server components, inference deliberately unavailable | Recall UI, actual model, Calendar UI and connected Nextcloud provider |
| Packaging | Official packer and validated build inputs/output | Native EHPK loader/installation on Even App |

Future unsupported hardware events should use narrowly labelled bridge-contract tests, not fake the backend. Capture/Calendar should add recorded-audio/disposable-provider tests in this same PR. A production account or microphone recording is not needed for the current system suite.

## Reproduce

Use a disposable Ubuntu 24.04 machine with Docker Compose and the prerequisites in `.github/workflows/glass-system.yml`. The script temporarily installs then removes a uniquely named local CI certificate; do not run on production.

```sh
npm --prefix evenhub ci
npm --prefix evenhub/system ci
(cd evenhub/system && npx playwright install --with-deps chromium)
GLASS_TARGET=web bash evenhub/system/run.sh
# Strict complete-app gate (blocked until missing journeys are implemented):
GLASS_TARGET=web GLASS_REQUIRE_FULL_APP=true bash evenhub/system/run.sh
# Optional reproduction of the vendor package-URL compatibility failure:
GLASS_TARGET=package bash evenhub/system/run.sh
```

Official reference: https://hub.evenrealities.com/docs/test/simulator
