# Ledger Glass

A daily-use Even Realities G2/R1 frontend to the existing Ledger server. The app,
backend changes, companion controls, and verification are delivered together in
[PR #34](https://github.com/CesarPetrescu/ledger/pull/34).

Ledger is the source of truth. There is no second project database, embedded owner
password, static OAuth token, or provider API key in the plugin.

## Implemented modes

| Mode | Behavior |
| --- | --- |
| **Now / Projects** | Current focus, project browser with native list pagination, and project details. |
| **Capture** | User-initiated glasses microphone or phone text; choose project and note/todo/decision/status; review the full transcript across pages; explicitly confirm before saving. |
| **Recall** | Voice or typed search; real Ledger retrieval, lexical-fallback indication, and exact source-entry bodies with IDs and timestamps. |
| **Brief** | Complete commit-ordered entry changes, bounded pages, explicit page acknowledgement, and per-device server-side checkpoints. Existing AI-news entries appear as normal Ledger changes; no separate news collector. |
| **Next** | Seven days of events from owner-selected Nextcloud calendars, local time-zone formatting, all-day dates, and the current focus project. |

Capture and Recall use the **real** server API. Speech recognition is optional:
without a configured speech provider, typed Capture/Recall remain usable. There
is no general chatbot, health dashboard, media remote, or notification mirror.

## Permissions and safety

Initial pairing requests only `ledger:read`. Confirming the first Capture asks
for explicit owner approval of `ledger:write`; first opening Next asks for
`calendar:read`. The app never requests `calendar:write` and never silently
broadens an existing grant. Approval and revocation use Ledger's existing owner
web console or Android application.

Reauthorization preserves the registered device identity when its host storage
survives. This keeps retry receipts and Brief checkpoints associated with that
device. A completely new installation/identity gets an independent reader.

A confirmed capture is written to the phone host's outbox **before** making the
network write. Ledger stores an atomic `(client_id, idempotency_key)` receipt. If
the response disappears, Retry uses the same key and returns the original entry;
a different payload with the same key is rejected. “Saved” appears only after a
valid server acknowledgement. Confirmed pending captures cannot be edited or
automatically replayed under a different identity. Back keeps them pending;
Cancel discards only an unconfirmed draft.

Bridge storage is a host capability, **not a claim of hardware-backed encrypted
storage**. Losing that host's data may require re-pairing and manual reconciliation
of pending captures. No client can recover a lost local retry key by guessing.

## Requirements

- Node.js 22.12+ for local development; CI uses Node.js 24.
- Even App 2.2.10+, SDK 0.0.15, and an HTTPS Ledger server containing this PR's
  migration, MCP tools, transcription adapter, and browser CORS support.
- The client uses supported MCP 2026-07-28 per-request client identity for the
  stateless server; deploy the updated Ledger server before using this client.
- G2/R1 for physical acceptance; Developer Mode for local QR testing.
- Optional: an OpenAI-compatible HTTPS speech endpoint configured on the server.
- Optional: an owner-authorized Nextcloud connection with selected calendars.

The `.ehpk` is bound to a single HTTPS Ledger origin. The build produces its runtime
configuration and network whitelist from the same `LEDGER_SERVER` value. Do not
commit an operator-specific origin or credentials to this public repository.

## Build and run

```sh
cd evenhub
npm ci
export LEDGER_SERVER=https://ledger.example.com
npm run verify
npm run pack
```

The output is `evenhub/ledger-glass.ehpk`. Its origin is the one supplied above;
`ledger.example.com` is a placeholder, not a usable production deployment.

For QR development:

```sh
export LEDGER_SERVER=https://ledger.example.com
npm run dev
# In another terminal; the phone must reach this development host:
npx evenhub qr --url http://YOUR-LAN-IP:5173
```

For the native simulator:

```sh
export LEDGER_SERVER=https://ledger.example.com
npm run dev
npm run simulate
```

First launch displays a device approval code on the glasses and phone WebView.
Approve it in Ledger, then use the contextual menu to choose a mode.

## Phone companion controls

The Even Hub phone WebView contains the necessary editor and preferences; there
is no additional mandatory APK. The existing Ledger Android/web application
continues to own device approvals, revocation, and Nextcloud configuration.

- **Capture text / Recall query:** type or correct the text, then use the visible
  Review/search button or **Ctrl+Enter**. A Capture still needs glasses confirmation.
- **Record speech / Stop recording:** the same user-initiated recording path as the
  glasses menu; no background listening.
- **Cancel current action:** stops recording or transcription and cancels a pending
  authorization attempt. It does not delete a confirmed pending submission.
- **Preferences:** IANA display time zone (for example `Europe/Bucharest`) and
  speech language (Automatic/mixed, Romanian, or English).
- **Reload app:** reloads the WebView; also available as **Ctrl+Alt+R**. Confirmed
  pending captures are retained if the host storage survives, never auto-submitted.

The UI language is English. Romanian and mixed-language text are preserved; actual
speech accuracy depends on the chosen provider and needs a real-audio evaluation.

## G2/R1 interactions

| Input | Behavior |
| --- | --- |
| Contextual menu | Now, Projects, Refresh, Reconnect, Capture, Recall, Brief, Next. |
| Swipe in native list | Move the selection. |
| Single press | Open the selected item/action; on a multi-page document, advance through every page before its action menu. |
| Single press while recording | Stop recording and transcribe; never automatically save. |
| Double press in a daily mode | Cancel/back; preserve a confirmed pending capture. |
| Double press in foundation views | Request the system exit confirmation. Actual OS confirmation/cancel is a hardware acceptance item. |
| Brief “Mark page read / Next” | Explicitly acknowledge this fetched page, then advance within the frozen snapshot. |
| Brief “Back (keep unread)” | Leave without changing the acknowledgement checkpoint. |

Recording is bounded to 30 seconds of 16 kHz mono signed-16-bit PCM. Cancellation,
page hiding, and mode changes stop the microphone; transcription responses from
an interrupted operation are not applied to the current screen.

## Server-side speech configuration

Set these **only on the Ledger server**, not in the plugin:

```dotenv
LEDGER_STT_URL=https://speech.example.com/v1/audio/transcriptions
LEDGER_STT_MODEL=your-provider-model
LEDGER_STT_API_KEY=your-provider-key
```

The URL is the complete transcription endpoint, not just a base URL. The adapter
sends multipart WAV audio, `model`, optional `language`, and `response_format=json`;
it expects a JSON `text` field. Requests are bounded, redirects are rejected, and
provider errors are displayed without exposing its credentials. Only canonical
0.1–30-second 16 kHz mono PCM WAV is accepted; this is not a general file-upload
service. Each OAuth client is limited to ten transcription requests per minute.

Configure the endpoint for Romanian/English quality and privacy appropriate to
your deployment. A CI transcript fixture verifies the adapter contract, not the
accuracy of any speech model. No speech provider is contacted when using typed
Capture or Recall.

## Backend migration and compatibility

`0007_glass.sql` adds retry receipts, an entry-change feed, and device reader
checkpoints. It takes a write-blocking table lock during backfill/trigger setup;
back up the database and schedule deployment appropriately for a large history.
Existing entries remain unchanged and append-only. Existing `append_entry`
clients remain compatible because `idempotency_key` is optional.

Every writer, including the existing owner API, reaches the change-feed trigger.
A transaction advisory lock allocates change cursors in commit order, avoiding
the late-commit/lower-entry-ID gap that an `after_entry_id` feed could skip. This
briefly serializes entry insertion: appropriate for a personal project ledger,
not a claim of unlimited ingestion throughput. Snapshots exclude later entries;
those remain available on the next Brief open. Reading never acknowledges a page
by itself, and an acknowledgement cannot skip an unfetched range.

The new MCP tools are `get_entry`, `list_changes`, `ack_changes`, and
`transcribe_audio`; the client also uses existing search/calendar tools. Reader
state changes require the device's `ledger:read` grant but do not mutate project
history or another device's/digest's checkpoint.

## Verification and evidence

See [system/README.md](system/README.md) for exact topology, executable journeys,
provider substitutions, failure injection, and reproduction commands. The native
suite uses the official simulator, production Ledger containers, actual OAuth
approval, MCP, and PostgreSQL. It is not a mocked Ledger implementation.

CI artifacts record the tested commit, JSON/JUnit outcomes, actual native RGBA
screenshots and readable previews, package/build-input hashes, and sanitized logs.
Inspect the **latest PR head's** Checks; older green runs do not validate later
changes. The full-app gate remains enabled for a ready-for-review PR.

Official `.ehpk` production and invalid-manifest rejection are checked separately.
Simulator 0.9.5 did not install an `.ehpk` URL in the tested environment; its native
journeys use the exact production build supplied to the official packer. **Actual
Even App package installation is not established by these checks.**

### Physical acceptance before release

Test the actual package configured for the intended deployment on the final
revision: initial approval; all four modes using both G2 and R1; review/edit/cancel
and exactly-once Capture; real Romanian/English/mixed speech; source Recall; Brief
paging/reopen; selected-calendar/time-zone correctness; phone lock/background;
Bluetooth disconnect/reconnect; revoked credentials; and OS exit/cancel.

Record device/firmware versions and failures in PR #34. Native simulator success
does not prove physical BLE timing, optics, battery life, host storage guarantees,
Android eviction behavior, or production-provider accuracy. No production deploy,
merge, or release is performed by the tests.
