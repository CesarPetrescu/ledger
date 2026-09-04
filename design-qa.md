# Design QA

## Comparison target

- Source visual truth: `/root/.codex/generated_images/01a06b70-6b25-77a2-89a2-c4a13552eff1/exec-fded5167-c3cd-4365-b9fe-d803d03a6ca7.png`
- Normalized source: `.private/design-qa/source-option-2-normalized.png`
- Mobile implementation: `.private/design-qa/search-mobile-pass-3.png`
- Desktop implementation: `.private/design-qa/search-desktop-pass-3.png`
- Viewports: `390 × 844` and `1440 × 1024` CSS px at 1× density
- State: authenticated Search route, populated query, Everything scope, collapsed filters, grouped ranked results, active Search navigation

## Full-view comparison evidence

- `.private/design-qa/compare-mobile-pass-3.png`
- The source and implementation are normalized to the same `390 × 844` viewport and placed side by side.

## Focused comparison evidence

- `.private/design-qa/compare-mobile-focus-pass-3.png`
- The top `390 × 430` region compares the command header, query, scope tabs, summary, headings, result rails, typography, and row density.

## Findings

- No open P0, P1, or P2 issues.
- [P3] The implementation bottom navigation is 72px tall rather than the mock's approximately 42px. This is intentional: real navigation targets remain at least 46px on touch devices.
- [P3] The generic second group reads “More results” instead of the mock's data-specific “More in Atlas,” because real results may span multiple projects.

## Required fidelity surfaces

- Typography: compact uppercase eyebrow, high-emphasis query, smaller metadata, and dense result hierarchy match the selected direction without clipping.
- Layout rhythm: edge-to-edge mobile command area, 48px scope bar, flat divider rows, colored result rails, and fixed bottom navigation match the source structure.
- Geometry: search surfaces and the broader admin/OAuth controls use square or 2px corners; no decorative card rounding remains.
- Color: dark navy command area, cobalt controls, green highlights, warm neutral canvas, and muted dividers match the target.
- Assets: the target has no raster assets; controls use the project's existing Tabler icon library.
- Copy: scopes, summary, result groups, excerpts, metadata, dates, and scores use Ledger's real data model.

## Primary interactions and console

- Chromium mobile: Everything, Projects, Entries, Filters, project selection, kind selection, and search submission exercised successfully.
- Mobile touch targets measured at 46–56px; horizontal overflow is `0px`.
- Chromium desktop: populated state captured at `1440 × 1024`; horizontal overflow is `0px`.
- Browser console and page errors: none at either viewport.
- Automated verification: 11 frontend test files / 43 tests, lint, typecheck, production build, Go admin/OAuth tests, and admin race test all pass.

## Comparison history

- Pass 1: query clipped on mobile; command header and scope area were too tall; result rows were too loose; source metadata added clutter.
- Fix: reduced the mobile command and row density, removed source/client identifiers, split result headlines from excerpts, and retained only useful project/date/score metadata.
- Pass 2: layout aligned, but the full query still clipped and the result typography remained larger than the selected source.
- Fix: tightened mobile letter sizing and row rhythm, matched the source's 12-result/3-project state, and preserved accessible navigation target sizes.
- Pass 3: combined full-view and focused comparisons show no remaining P0/P1/P2 mismatch; mobile and desktop browser checks pass.
- Pass 4: removed an over-specific generic list rule that suppressed the intended result-row gutter; the colored rail now has 18px of separation from all result text.

final result: passed

## Calendar addition

- Evidence: `.private/design-qa/calendar-mobile-pass-1.png`, `.private/design-qa/calendar-mobile-editor-pass-1.png`, and `.private/design-qa/calendar-desktop-pass-1.png`.
- Design-language comparison: `.private/design-qa/compare-calendar-language-pass-1.png` places the selected option and implementation at the same `390 × 844` viewport.
- Viewports: `390 × 844` and `1440 × 1024` CSS px at 1× density, using the selected flat, low-radius admin design language.
- State: authenticated, connected Nextcloud account, two selected calendars, populated agenda, and recurring-event editor.
- Chromium checks: event edit with ETag, calendar allowlist change, all-day control, and modal close all passed; horizontal overflow is `0px`; browser console and page errors are empty.
- Mobile touch targets measured at 46–56px. The final refinement keeps the calendar selector and Today action on one compact row.
- Automated verification: 12 frontend test files / 46 tests, lint, typecheck, and production build pass.

final result: passed
