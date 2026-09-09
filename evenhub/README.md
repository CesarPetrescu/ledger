# Ledger Glass

Even Realities G2/R1 client for Ledger. The existing Ledger server remains the source of truth for project context, capture, recall, briefs, and calendar access.

## One app, one pull request

The complete app is delivered in [PR #34](https://github.com/CesarPetrescu/ledger/pull/34) on `feat/ledger-glass-foundation`. The earlier proposal to split Capture, Recall, Brief, and Calendar/Next into separate PRs is superseded by the owner's instruction on 2026-09-09.

Use separate commits and implementation checkpoints **inside this same branch and PR**, not separate feature PRs. Keep #34 in Draft until the full scope and acceptance evidence are complete. Hardware testing is a final acceptance requirement, not a reason to stop implementing the remaining features.

### Current implementation checkpoint

The code currently implements a **read-only foundation**, not the complete app:

- OAuth device-code pairing against an existing Ledger server.
- Requests only `ledger:read`; no project mutations are possible from this build.
- Stores the revocable OAuth session through the Even App bridge, not in the source bundle.
- Uses Ledger's MCP endpoint for `list_projects` and `get_project`.
- **NOW** chooses the highest-priority project (`focus` → `maintain` → `park`, then weekly hours).
- Project list and detail views with G2/R1 input handlers.
- Contextual menu actions: Now, Projects, Refresh, Reconnect.
- Double press opens the system exit confirmation.
- Phone WebView shows connection state and the device approval code.

The scope update does not itself implement the remaining features or change granted permissions. Build/test success at this checkpoint does not establish full-app or physical-hardware acceptance.

### Remaining work in PR #34

- [ ] **Foundation hardening:** cancellable pairing/network operations, timeout and reconnect recovery, permission/revocation tests, complete project pagination, stable screen restoration, and input/lifecycle regression tests.
- [ ] **Capture:** user-initiated G2 microphone capture, a configurable authenticated STT backend, project/type selection (`note`, `todo`, `decision`, `status`), transcript preview/correction, explicit confirmation, and append-only Ledger writes. Request write access only with a new explicit approval. Persist confirmed pending submissions and use server-side idempotency so retries cannot create duplicate entries. Never label an unacknowledged write as saved. Test Romanian, English, and mixed technical vocabulary.
- [ ] **Recall:** query entry/voice input, existing Ledger search, browsable source results with project and timestamp/reference, no-result and degraded-search states. An optional summary must remain grounded in retrieved entries; a generic chatbot is not required.
- [ ] **Brief:** complete, paginated changes since a durable checkpoint, per-surface read tracking, bounded cards, deduplication, and an AI-news reader using existing Ledger data. `last_entry_at` is only a change hint: latest-N project entries are not a complete changes feed. Advance a checkpoint only after the corresponding entries were fetched and acknowledged, not merely when the app opens. Keep glasses read state separate from existing digest-delivery checkpoints.
- [ ] **Calendar / Next:** upcoming events from Ledger's existing owner-selected Nextcloud calendars, explicit `calendar:read` authorization, and a combined next-event/current-project view. Handle timezone, all-day, unavailable-calendar, and empty states. Calendar write access is not required for this view.
- [ ] **Companion experience:** reuse Ledger Android/web for device approval and revocation; add the configuration and preferences needed by the above flows without introducing another mandatory APK or database. Keep admin credentials out of the Even Hub plugin.
- [ ] **Verification and delivery:** committed npm lockfile with `npm ci`, unit/integration and UI tests, simulator screenshots, complete existing CI regression gates, a packaged `.ehpk` for the intended deployment, and documented physical G2/R1 acceptance on the tested commit.

Media controls, health dashboards, notification mirroring, and a general-purpose chatbot are not part of this scope. Handoffs remain a secondary extension, not a prerequisite for the five core modes.

## Requirements for the current checkpoint

- Node.js 22 or newer.
- Even App 2.2.10 or newer.
- Even Hub SDK 0.0.15.
- An HTTPS Ledger deployment with the browser CORS support included by this Ledger change.
- Developer Mode in the Even Realities app for QR/local testing.

The packaged app is bound to one Ledger HTTPS origin because the Even Hub network permission must whitelist destinations before packaging. Do not commit an operator-specific Ledger URL to this public repository.

## Install

```sh
cd evenhub
npm install
```

## Run locally

Use your Ledger base URL without `/mcp` or another path:

```sh
export LEDGER_SERVER=https://ledger.example.com
npm run dev
```

In another terminal, point the Even Hub QR tool at the Vite server reachable from the phone:

```sh
npx evenhub qr --url http://YOUR-LAN-IP:5173
```

On first launch, the current checkpoint registers a device-code OAuth client and requests only `ledger:read`. Approve the displayed code from Ledger's owner UI/Android app. The refresh token remains revocable as a normal connected Ledger client. Capture and Calendar authorization upgrades are still unchecked work above, not available features of this checkpoint.

## Simulator

```sh
export LEDGER_SERVER=https://ledger.example.com
npm run dev
npm run simulate
```

The simulator is for layout and input logic. Before final acceptance, test QR sideloading and a packaged `.ehpk` on real G2 hardware; R1 event routing must also be checked on hardware. Label simulator screenshots as simulator evidence, not photographs of a physical G2 test.

## Verify and package

```sh
export LEDGER_SERVER=https://ledger.example.com
npm run verify
npm run pack
```

`npm run build` generates `app.json` from `LEDGER_SERVER`, ensuring the runtime server and Even Hub network whitelist are the same origin. `npm run pack` creates `ledger-glass.ehpk` using SDK 0.0.15.

A CI package built for `https://ledger.example.com` proves packaging only. It is not configured for an operator's live Ledger instance. Rebuild with the intended deployment origin for acceptance testing.

## Current interaction model

| Input | Result |
| --- | --- |
| Single press on NOW/detail | Open projects |
| Swipe in project list | Move selection (handled by the OS list) |
| Single press in project list | Open selected project |
| Double press | System exit confirmation |
| Contextual menu | Now / Projects / Refresh / Reconnect |

G2 and R1 expose the same basic gesture set. The current checkpoint intentionally does not assign hidden, device-specific actions. Update this table as the remaining modes are implemented and verified in the same PR.

## Security model

- The package contains no Ledger owner password and no static API token.
- Pairing uses Ledger's existing dynamic client registration + OAuth device-code flow.
- The current checkpoint requests only `ledger:read`; adding future Capture or Calendar features must not silently escalate an existing grant.
- OAuth and MCP fetches use `credentials: omit` and reject HTTP redirects.
- Ledger enables CORS only on the device/token registration endpoints needed by the WebView and on `/mcp`; the owner/admin cookie APIs remain outside that CORS surface.
- The configured server must be an HTTPS origin without embedded credentials, path, query, or fragment.
- Bridge-backed credential persistence is not a claim of hardware-backed encrypted storage; verify and document the host's storage guarantees before release.

If a device is lost, revoke the Ledger Glass client from Ledger just like any other connected client.

## Full-app acceptance checklist

Before marking PR #34 ready, complete the implementation checklist above and record evidence against the exact tested commit:

1. Pair from a clean install and approve the code from Ledger Android or the web console. Verify denial, expiry, cancellation, and revocation behavior.
2. Confirm a read-only grant cannot call `append_entry`. Verify Capture and Calendar require explicit authorization for their additional scopes.
3. Open NOW and compare the displayed state with actual Ledger data, including empty registries and long project histories.
4. Navigate every core mode with G2 and R1. Cover first-item index `0`, long lists, text pagination, contextual-menu open/close, and exit-confirmation cancellation.
5. Capture, review, and confirm a note; verify it appears once in Ledger with the correct project, kind, and attribution. Retry interrupted submissions without duplicates; rejected or unconfirmed speech must not be written.
6. Recall an entry and verify the displayed source against Ledger. Cover no matches, unavailable speech service, and lexical-only search fallback.
7. Fetch more changes than fit in a single API page. Verify Brief omits none, does not acknowledge unread pages, survives restart, and does not alter other digest readers' checkpoints.
8. Verify Calendar/Next against the selected calendars, including timezone boundaries, all-day events, empty results, missing permission, and provider failure.
9. Lock/unlock the phone, background/reopen the plugin, and disconnect/reconnect G2. Recover state without accidental writes, leaked microphone capture, or incorrect success messages.
10. Run all existing and new CI gates. Record simulator screenshots separately from physical G2/R1 results and test the actual `.ehpk` configured for the intended deployment, not only the Vite QR build.

Record failures and subsequent fixes in #34. Do not mark unchecked features complete based only on a roadmap, typecheck, or a fixture screenshot.
