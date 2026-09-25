# Ledger Glass system tests

PR #34 runs two required suites against independently created **production Ledger
Compose stacks**. The full-app journeys are executable tests, not a checklist of
names or placeholders that return success.

## Native system topology

```text
Official Even Hub simulator 0.9.5: native WebView + SDK + LVGL
    -> production-built Ledger Glass on HTTPS :9443
    -> trusted HTTPS :8443
    -> transparent CI fault proxy (normally forwards exact MCP requests)
    -> production nginx / actual Ledger OAuth, MCP, admin and index services
    -> PostgreSQL + pgvector + production migrations

Playwright Chromium -> actual React owner console -> real OAuth approval
X11 keyboard events -> actual native phone editor (no DOM/state injection)
PulseAudio signal -> actual simulator microphone capture -> real WAV adapter
```

Fictional projects and entries are created through the actual owner API. SQL is
used only for independent observations of pending device codes and persistent
results; it never seeds credentials, approves devices, or injects retrieval hits.
The index service builds real chunks, and search uses actual PostgreSQL lexical
retrieval with semantic inference intentionally unavailable.

There is no mocked Ledger API, SQLite replacement, fake OAuth success, injected
access token, Playwright `route.fulfill`, or disabled TLS verification. Cross-origin
native WebView requests exercise the actual CORS rules.

### External boundaries substituted in blocking CI

The small Python provider container is **CI-only**, absent from production Compose:

- **Speech:** validates authenticated multipart requests and the actual PCM WAV
  encoding, then returns a fixed transcript. A generated audio signal is played
  through PulseAudio into the official simulator, not inserted into a fake SDK
  callback. This verifies transport/control/confirmation, **not speech accuracy**.
- **Nextcloud:** implements the external login/CalDAV protocol with two calendars
  and UTC, Bucharest, and all-day fixtures. Ledger's actual encryption, discovery,
  selection, CalDAV query, parsing, scope enforcement, and rendering still run.
  It is **not** an actual Nextcloud server or production account.
- **Fault proxy:** forwards actual MCP calls to production nginx. One explicitly
  armed append response is discarded only after the real server completed it.
  The database must contain one entry and Retry must return its original receipt.
  No write or success response is fabricated. The control endpoint requires a
  per-run random token and is bound to loopback on the runner.

These substitutions are reported separately from actual Ledger coverage.
Production speech quality and a real Nextcloud deployment remain acceptance
checks outside this deterministic CI suite.

## Executable full-app journeys

`full-app.mjs` extends the foundation journeys in `run.mjs`:

| Journey | Independent evidence |
| --- | --- |
| `capture.cancel` | Typed text reaches the native review UI; cancellation leaves PostgreSQL unchanged. |
| `capture.confirm` | Actual owner permission upgrade, native confirmation, real appended entry, source attribution, and returned receipt. |
| `capture.retry-idempotency` | Real successful response dropped; pending UI; retry returns the same entry ID; database count remains one. |
| `capture.review-selection` | Change project/type, read every long-text page without writing, then confirm the exact full body. |
| `capture.cancel-recording` | Native recording cancellation neither transcribes nor writes. |
| `capture.voice` | Signal enters the actual native audio path; the real server adapter sends valid non-silent WAV; transcript is reviewed and explicitly confirmed. |
| `recall.sources` | Real indexer and search retrieve the captured entry; exact source ID and timestamp match PostgreSQL. |
| `recall.empty` | A query with no matching source renders the actual empty state. |
| `recall.degraded` | Actual inference failure is exposed as lexical fallback without losing the source. |
| `brief.all-pages` | More than one page of actual entries, complete ordered enumeration, explicit page acknowledgement, and a frozen snapshot excluding a later write. |
| `brief.checkpoint-restart` | Real phone-WebView reload, same authorization identity, server checkpoint retained, and only the later unread entry returned. |
| `calendar.selected` | Owner connects/selects through real Ledger; explicit `calendar:read` upgrade; unselected calendar is not queried or rendered. |
| `calendar.empty` | Deselecting all calendars produces an empty view without querying their events. |
| `calendar.timezones` | Actual CalDAV parsing and native rendering of UTC/Bucharest equal instants and an unshifted all-day DATE. |

The foundation additionally exercises TLS/CORS and anonymous rejection, real
browser login/CSRF, first read-only approval, empty registry, real Now data,
first-item selection, 25-project pagination, external writes followed by Refresh,
menu restoration, forced process restart/re-approval, denied write scope, real MCP
container stop/restart, owner revocation, denied approval, and native shutdown.

The suite uses native gestures and post-bridge-acknowledgement observations plus
independent API/database assertions and decoded 576x288 framebuffer checks.
Input waits for actual contextual-menu close events; screenshots wait for a
settled framebuffer. The host's real `Browser` window receives X11 keyboard events
for the normal editor and documented reload shortcut. Production code has no
test-mode branch or fixture-only input API.

A failed prerequisite marks dependent journeys `not_run`, never passed. Every
required journey must execute and pass; deleting a required name or filling its
result with a constant is not an acceptable fix. `client-checks` includes both
suites, and review-ready/main/release CI keeps the full-app gate enabled.

## Host-storage and protocol contract suite

Only `getLocalStorage` and `setLocalStorage` are doubled in this separate suite.
The actual `LedgerAuth`/`LedgerMCP` classes, HTTPS, OAuth, PostgreSQL and MCP remain
real. It tests restored-grant reuse, refresh rotation, same-device reauthorization,
explicit permission upgrades and denial without losing the prior read grant,
stateless write attribution/idempotent retry, and host persistence refusal.

The real native Capture journey exposed a protocol issue: legacy initialize-only
client identity was lost by the stateless Go transport between requests. The
application now uses the SDK's supported MCP 2026-07-28 negotiation, carrying
client identity on each request. CORS permits its standard `Mcp-Name` header.
A real TypeScript-to-Go write/retry test protects this behavior; the server's
client attribution validation was not removed or replaced with a fake name.

The observed simulator lost bridge storage after forced native-process termination.
That journey records the limitation and verifies actual owner re-approval, without
injecting credentials. A full host-data loss can create a new reader identity;
storage contracts do not establish native persistence or hardware encryption.
A same-process WebView reload can retain the native page; startup recovery uses
a separately acknowledged rebuild of a validated layout, never an ignored error.

## Backend and pure regressions

The Go integration suite uses actual PostgreSQL to test twelve concurrent retries,
conflicting-key rejection, independent client keys, snapshot pagination, rejection
of unfetched acknowledgements, reader isolation, and a late commit with a lower
entry identity. A separate commit-ordered change cursor prevents that late entry
from being skipped.

TypeScript regressions cover confirmed-intent persistence, cancelled/unconfirmed
writes, cross-identity replay rejection, retry-key preservation, storage refusal,
canonical PCM WAV, duration bounds, complete text pagination, and date formatting.
Provider-adapter tests use actual TLS/HTTP against an external contract fixture,
validate Romanian text preservation and multipart encoding, and reject redirects
and invalid recordings. Go MCP scope tests reject unauthorized new-tool access
before touching reader data or an external provider.

## Packaging and hardware boundary

Each run invokes the official packer, checks its EHPK output/magic and build-input
hashes, and requires rejection of an invalid package ID and missing entrypoint.
The compiled origin is the disposable local server, **not an operator release**.

Direct `.ehpk` URL loading produced a blank WebView on simulator 0.9.5. Runtime
journeys therefore use the exact production build supplied to the official
packer. The unsupported URL diagnostic remains manually reproducible using
`simulator_target=package` and fails when no real application starts; no invented
package loader substitutes for native installation.

The native shutdown request is independently observed in the simulator log; the
framebuffer clears rather than showing the physical OS confirmation dialog.
Blank output is allowed only in that labelled exit probe. Actual package
installation, OS confirmation/cancel, physical R1 identity/BLE timing, optics,
battery, Android permissions/eviction, and microphone acoustics remain hardware
acceptance items.

## Artifacts and privacy

Both suites upload JSON/JUnit results, dependency/revision receipts, sanitized
logs, official `.ehpk`, and package/build-input hashes. Native artifacts also
include untouched RGBA frames, black-composited readable previews, an HTML gallery,
owner approval screenshots, and external-provider contract receipts. Preview
conversion is not a design mock. Failure evidence uploads with `if: always()`.

Tokens, cookies, private keys, host credential storage and raw audio payloads are
excluded. Per-run credentials and fictional data are disposable, ports bind to
loopback, and no production account is used. Cleanup removes containers, volumes,
private runtime files, and temporary OS/NSS trust entries.

## Reproduce

Use a disposable Ubuntu 24.04 machine with Docker Compose and prerequisites from
`.github/workflows/glass-system.yml`. The script temporarily installs/removes a
uniquely named local certificate; do not run it on production.

```sh
npm --prefix evenhub ci
npm --prefix evenhub/system ci
(cd evenhub/system && npx playwright install --with-deps chromium)
GLASS_SUITE=native GLASS_TARGET=web GLASS_REQUIRE_FULL_APP=true bash evenhub/system/run.sh
GLASS_SUITE=host-contract GLASS_TARGET=web bash evenhub/system/run.sh
# Optional reproduction of the vendor's unsupported package-URL behavior:
GLASS_TARGET=package bash evenhub/system/run.sh
```

Official simulator reference: https://hub.evenrealities.com/docs/test/simulator
