# Ledger Glass

Read-only Even Realities G2/R1 client for Ledger. The glasses render a compact **NOW** view and project browser while the existing Ledger server remains the source of truth.

## Scope of v0.1

- OAuth 2.1 device-code pairing against an existing Ledger server.
- Requests only `ledger:read`; no project mutations are possible from this build.
- Stores the revocable OAuth session through the Even App bridge, not in the source bundle.
- Uses Ledger's MCP endpoint for `list_projects` and `get_project`.
- **NOW** chooses the highest-priority project (`focus` → `maintain` → `park`, then weekly hours).
- Project list and detail views usable from G2 temples or R1.
- Contextual menu actions: Now, Projects, Refresh, Reconnect.
- Double press opens the system exit confirmation.
- Phone WebView shows connection state and the device approval code.

Capture, semantic recall, briefs, calendars, and handoffs are deliberately outside this first PR.

## Requirements

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

On first launch, Ledger Glass registers a device-code OAuth client and requests only `ledger:read`. Approve the displayed code from Ledger's owner UI/Android app. The refresh token remains revocable as a normal connected Ledger client.

## Simulator

```sh
export LEDGER_SERVER=https://ledger.example.com
npm run dev
npm run simulate
```

The simulator is for layout and input logic. Before merging a release, test QR sideloading and a packaged `.ehpk` on real G2 hardware; R1 event routing must also be checked on hardware.

## Verify and package

```sh
export LEDGER_SERVER=https://ledger.example.com
npm run verify
npm run pack
```

`npm run build` generates `app.json` from `LEDGER_SERVER`, ensuring the runtime server and Even Hub network whitelist are the same origin. `npm run pack` creates `ledger-glass.ehpk` using SDK 0.0.15.

## Interaction model

| Input | Result |
| --- | --- |
| Single press on NOW/detail | Open projects |
| Swipe in project list | Move selection (handled by the OS list) |
| Single press in project list | Open selected project |
| Double press | System exit confirmation |
| Contextual menu | Now / Projects / Refresh / Reconnect |

G2 and R1 expose the same basic gesture set. The app intentionally does not assign hidden, device-specific actions in v0.1.

## Security model

- The package contains no Ledger owner password and no static API token.
- Pairing uses Ledger's existing dynamic client registration + OAuth device-code flow.
- v0.1 requests only `ledger:read`.
- OAuth and MCP fetches use `credentials: omit` and reject HTTP redirects.
- Ledger enables CORS only on the device/token registration endpoints needed by the WebView and on `/mcp`; the owner/admin cookie APIs remain outside that CORS surface.
- The configured server must be an HTTPS origin without embedded credentials, path, query, or fragment.

If a device is lost, revoke the Ledger Glass client from Ledger just like any other connected client.

## Hardware acceptance checklist

Before marking the PR ready:

1. Pair from a clean install and approve the code from Ledger Android or the web console.
2. Confirm the token has `ledger:read` and cannot call `append_entry`.
3. Open NOW and verify the actual current focus project and latest entry render correctly.
4. Open Projects, swipe with G2 and R1, and select the first item (protobuf index `0` must work).
5. Open/close the contextual menu repeatedly; it must not lose state or trigger destructive lifecycle behavior.
6. Double press and verify the system exit confirmation appears.
7. Revoke the client in Ledger and verify the next access requires a new approval.
8. Lock/unlock the phone and reopen the plugin; state must reconstruct from Ledger.
9. Build and sideload the real `.ehpk`, not only the Vite QR build.
