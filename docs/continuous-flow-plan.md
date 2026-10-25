# Search → compare → download → track

Status: proposed implementation plan, 25 October 2026.

Keep Tork's compact rows, muted palette, keyboard navigation, and contextual
footer. Make the next action obvious without adding a dashboard or extra steps
to single-release downloads.

## Current foundation

- Results already merge by infohash, retain other source names, and use stable
  row identities across filtering, sorting, and streamed inserts.
- Browse, flat results, picks, and source groups have separate navigation paths.
  Batch actions must behave consistently across them.
- Space currently expands/collapses source groups. Left/right and h/l already
  support folding, so Space can become a consistent marking action.
- Downloads already show progress, speed, ETA, aggregate activity, and a selected
  item panel. Pause/resume, reveal, move, relink, and verification exist. The
  detail panel disappears below 20 body rows or 44 columns, and row activity can
  truncate speed/ETA. This phase should refine that foundation.
- Source management already has checks, retries, enable/disable, and an editor.
- Search retains some preferences within a session; a new search replaces the
  result model. Persisted state currently contains downloads only. The existing
  metadata cache stores torrent metainfo, not search results.
- Seeder counts are integers, and merging retains the maximum. Per-source
  observations and unknown counts need explicit representation.

## Phase 1 — Batch selection

### Interaction

- Space toggles the focused release. Use a small check marker beside its title.
  While typing in a search/filter field, Space continues to enter text.
- Keep Enter's existing single-release preview behavior. Make D download marked
  releases when any are marked; otherwise retain its direct-download behavior
  for the focused release. Update the contextual footer and help together.
- Show `3 selected · 1 hidden` when filters hide a marked release. D includes all
  marked releases, including hidden ones. Provide a clear-selection action,
  with the final key chosen against the complete keymap during implementation.
- A group header marks its current representative release, matching its existing
  download target. It never selects every alternative. Freeze that target when
  marked so later ranking changes cannot silently change the intended download.
- Left/right or h/l expand/collapse groups. Show the replacement in help.
- Retain marks through filters, sorting, streamed results, view changes, and a
  trip to Downloads. Scope marks to the current search; clear them when a new
  query is submitted. Cross-search baskets are outside this first increment.
- Batch submission starts whole-release downloads using the configured folder
  without opening a file picker for each release. Enter remains available for
  inspecting files and choosing a subset before downloading one release.
- Keep users in results after submission, show a compact queued/failed summary,
  and offer the existing Downloads shortcut. Successful marks clear; failed
  marks remain for retry. Indicate items currently being submitted.

### Implementation

- Add a shared marked-release collection separate from cursor selection. Prefer
  infohash identity; fall back to the provider result key for unresolved rows.
- Reconcile fallback identities when magnets resolve or sources merge. Read the
  latest merged result when submitting, retaining accumulated tracker data.
- Add a batch coordinator with bounded resolution/add concurrency, per-item
  results, and duplicate suppression against pending and existing downloads.
  Do not reuse the single outstanding resolver state for an entire batch.
- Keep commands asynchronous and state updates on the Bubble Tea update path.
  Correlate completion with batch/search identities so late responses cannot
  clear marks belonging to a newer search. Navigation must not cancel already
  submitted additions.
- Integrate common marking/submission logic with results, browse, picks, grouped
  rendering, and the central keymap. Preserve existing resume-state recording.

### Acceptance criteria

- Mark three releases, hide one with a filter, change sort/view, then submit:
  each unique release is added once, including the hidden one.
- Streaming inserts, changed group representatives, and cross-source merges do
  not move or duplicate marks.
- A failed resolution does not prevent successful items from being recorded;
  only failures remain marked. Repeated D while a batch runs does not double-add.
- A search change during submission cannot affect marks in the new search.
- Filter typing, group folding, single-item preview, and file selection still
  work. Mark indicators and counts fit supported small terminal sizes.

## Phase 2 — Downloads that complete the flow

### Interaction

- Preserve the existing overview and list. Prioritize name, state, progress,
  speed, and ETA; show the selected destination in a compact line even when the
  full detail panel cannot fit. Use the configured download directory while the
  final payload path is unknown.
- Render unknown total size/ETA as an em dash or a short status, never a
  misleading 0% or zero-second estimate. Explain metadata lookup, waiting for
  peers, paused, verifying, completed, and recoverable failure states.
- Make p's footer label follow the selected item: pause, resume, or retry. Keep
  r for relink, o for folder reveal, and Enter for open. Surface the latest
  failure beside its recovery action; avoid requiring users to infer failure
  from a paused state or an internal error string.
- Open the destination folder when the payload does not exist yet. Reveal the
  payload when it exists. Report handoff errors in Tork.
- Keep keyboard focus stable as progress changes and completed items move.

### Implementation and acceptance criteria

- Reuse engine snapshots and existing actions. Add structured recovery/failure
  information where needed rather than branching on rendered note strings.
- Verify that pause/resume/retry preserves destination, file exclusions,
  checksum settings, and completed bytes where resumption is supported.
- Test torrent metadata waiting, active transfers, paused transfers, direct
  download errors/checksum failures, missing files, verifying, and completion.
- Verify selected destination and useful activity at compact and normal sizes;
  rendering stays within terminal bounds and does not perform filesystem work.
- Exercise the complete sequence: search → mark → queue → Downloads → pause →
  resume → complete → reveal → back to the original filtered results.

## Phase 3 — Explain release quality and availability

- Retain individual source observations before deduplication: source, reported
  seeders/leechers, and observation time. Display them in release details.
- Represent unknown counts explicitly across providers, merging, sorting,
  filters, ranking, and rendering. Unknown availability must not become zero
  seeders or disappear under a filter intended to hide known zero-seeder rows.
- Explain ranking using the actual scoring inputs and contributions rather than
  only a numeric score. Label source-reported counts separately from live peers.
- Reuse metadata preview for file contents with loading/unavailable states;
  don't fetch metadata for every search result automatically.

## Phase 4 — Source states and recovery

- Align existing search and source-management states: working with matches,
  working but empty for this query, unavailable with a reason, and disabled.
- Retain distinctions for unchecked, checking, stale observations, and partial
  responses. An empty query response is not evidence of a broken source.
- Keep retry and add-source actions visible in relevant empty/error states.
  Reuse existing source checks and request generation guards.

## Phase 5 — Restore context and make back instant

- Persist category, sort, filters, display preferences, and bounded recent
  searches in versioned UI state, separate from transfer resume entries.
- Add a bounded recent-search result cache with timestamps. Restore cached
  results immediately and refresh without discarding cursor/filter context.
- Key cache entries by query and relevant source configuration; invalidate when
  sources change. Label stale counts, offer clear-history/cache controls, and
  recover gracefully from malformed cache files.
- Restore preferences across restarts. Keep transient batch marks session-only
  initially; do not silently persist pending user actions.

## Phase 6 — Category-aware grouping

- Movies/TV: preserve title/episode identity and resolution groups, then show
  source/codec variants within them. Do not merge different torrent identities
  merely because their labels match.
- Games/software: group with platform, version, and edition when supplied by
  sources or confidently parsed. Label unknown values; avoid guessing.
- Preserve marked-release identity and collapsed-group state through regrouping.

## Delivery order and verification

1. Shared selection model, identity reconciliation, and focused tests.
2. Space markers, group-key migration, selected/hidden counts, and batch queue.
3. Download visibility, state-aware recovery, and folder handoff refinements.
4. End-to-end keyboard walkthrough and targeted TUI/engine tests, followed by
   the repository's required checks before delivery.
5. Proceed through phases 3–6 as separate reviewable increments.

The working tree already contains substantial in-progress changes. Implement
against that current behavior, preserve unrelated edits, and inspect the final
diff by scope. This document is a plan; no application behavior changed with it.
