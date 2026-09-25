# README screenshots

These are real application captures, not generated mockups. All visible project
records are fictional. The PNGs are stored in this repository so the root README
does not depend on expiring Actions downloads or an operator's private server.

| Asset | What is shown | Capture environment |
| --- | --- | --- |
| `website.png` | Project browser and Atlas project overview | Production React client in Chromium, backed by the disposable real Ledger HTTPS services and PostgreSQL |
| `android.png` | Authenticated Overview, focus project and recent entry | Actual Android application on the API 36 emulator, using the existing HTTPS test-data provider |
| `even-simulator.png` | Ledger Glass NOW and its focus project | Official Even Hub native simulator and real Ledger services; original 576 × 288 framebuffer composited onto black by the test harness |

The glasses picture is **simulator output, not a photograph through G2 lenses**.
The Android image is an emulator capture, not a physical-phone test. Source
captures are copied byte-for-byte: no UI is redrawn, text replaced, or features
invented. The three clients use separate fictional test datasets.

## Provenance

[`provenance.json`](provenance.json) records the capture run, exact source commit,
artifact names, pixel dimensions, file sizes, and SHA-256 hashes. Initial captures
come from [CI #161](https://github.com/CesarPetrescu/ledger/actions/runs/34451859800),
head `b6cef305bd95300c8ea70c0905e5f7a089ead4bf`. The two source jobs
(`glass-system / system (native)` and `android (36)`) passed. The overall source
run was not green because the separate API-28 text-focus test failed; screenshot
provenance does not substitute for the final PR's complete CI result.

Only these three PNGs and their metadata are imported. Runtime logs, tokens,
cookies, private keys, provider configuration, native binaries and font files are
not published with the screenshots.

## Refresh the captures

For the website and glasses, run the existing native system suite as documented
in [`evenhub/system/README.md`](../../evenhub/system/README.md). After all native
journeys pass, `evenhub/system/readme-web.mjs` creates fictional Atlas/Home lab/
Learning notes projects through the actual owner API and screenshots the real
project browser. It does not intercept backend replies or rewrite the DOM.

For Android, run [`android/smoke.sh`](../../android/smoke.sh) on a booted emulator.
`ReadmeScreenshotTest` waits for the authenticated Overview, saves a real
`screencap` frame to shell-owned `/data/local/tmp`, and decodes the image to
verify the export. The frame survives Gradle APK cleanup and is copied into
`app/build/reports/readme/android-overview.png`. All existing instrumentation
tests remain enabled.

Download the `glass-system-native-web` and `android-reports-api-36` artifacts from
the same inspected CI run. Confirm that both source jobs passed, inspect the
images for readability and accidental private information, then import them:

```sh
python3 scripts/readme-screenshots.py import \
  --native /path/to/extracted-native-artifact \
  --android /path/to/extracted-android-artifact \
  --run-id YOUR_RUN_ID \
  --head-sha FULL_PR_HEAD_SHA
python3 scripts/readme-screenshots.py check
python3 -m unittest discover -s scripts -p 'test_readme_screenshots.py' -v
```

Commit the three PNGs and `provenance.json` together. The read-only **README
screenshots** workflow validates PNG structure/pixel payloads, metadata,
checksums, and all three relative README image references. Review UI changes
manually when refreshing pictures; matching hashes alone do not prove that an
image represents the latest application version.
