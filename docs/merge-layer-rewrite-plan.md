# Merge-Layer Rewrite — Implementation Plan

> Derived from the "Cairn — Architecture Handover" brief (multi-provider merge layer).
> Scope approved 2026-06-15: **full re-clustering rewrite**, **per-channel streams +
> alignment**, **health-data taxonomy in scope**. Plan-first: no code until approved.
>
> This plan converts the brief into phased, file-level work against the *current*
> codebase (verified, not assumed). It is the single source of truth for the rewrite;
> update it as phases land.

## STATUS — all phases landed (merged to `main`)

All 10 phases are implemented, each on its own commit, `go build/vet/test ./...`
+ `web npm run check` green throughout. Pure cores (match, reconcile,
streamalign, health, capability) are unit-tested.

**The legacy incremental-attach dedup has been REMOVED** (operator decision):
re-clustering is now the ONLY ingest path — there is no
`CAIRN_MATCHING_RECLUSTER_ENABLED` flag anymore. Ingest resolves source identity
+ persists; `ReclusterBucket` always derives grouping. The legacy heuristic/geo
repo queries, `IngestTolerances`, and the flag/config section are deleted. Gap-6
durability (a detached source must not re-attach on re-push) now lives in the
re-cluster engine: denied identities are forced to standalone singletons.

| Phase | Status | Key commit subject |
|---|---|---|
| 0 Golden corpus | ✅ | test(merge): phase 0 — golden corpus + merge golden master |
| 1 Capability manifest | ✅ | feat(capability): phase 1 |
| 2 Match features | ✅ | feat(match): phase 2 |
| 3 Matching engine | ✅ | feat(match): phase 3 — fuzzy scoring + constrained union-find |
| 4 Recluster + reconcile | ✅ | feat(match): phase 4 — recluster + stable-id reconciliation |
| 5 Manual overrides | ✅ | feat(merge): phase 5 — field pins, durable detach, join |
| 6 Priority cascade API | ✅ | feat(merge): phase 6 — priority cascade write API + instance defaults |
| 7 Provenance + computed | ✅ | feat(merge): phase 7 |
| 8 Per-channel streams | ✅ | feat(streams): phase 8 — per-channel merge + alignment |
| 9 Cross-provider PRs | ✅ | feat(records): phase 9 |
| 10 Health taxonomy | ✅ | feat(health): phase 10 |

Known deferrals (noted in the relevant commits), all non-blocking:
- Re-cluster DB orchestration is tested end-to-end via in-memory fakes
  (`internal/usecase/match/recluster_test.go`: merge-duplicate, keep-distinct,
  same-provider conflict, denylist-keeps-standalone) in addition to the
  pure-core unit tests.
- Per-channel merged stream is RENDERED on the full-screen /streams view (with
  per-channel provenance caption) for multi-source activities, computed on-demand
  (GET …/merged-stream). Still deferred: persisting it to a TimescaleDB hypertable
  (perf only — on-demand is cheap for a personal tracker) and using it on the
  detail page's combined chart + map (map needs GPS coordinates/track the merged
  view doesn't carry).
- Cross-provider PRs have a UI (/records). Best-efforts are still computed
  per-source then merged at query time — deliberately NOT re-keyed onto the
  aligned stream: the query-merge already yields the cross-provider best per
  metric, so the rework is high-churn for marginal accuracy.
- Confidence bands are surfaced: medium-confidence merges show in the /review
  queue (GET /api/review-queue) where the user confirms or splits them.
- Health (Phase 10) is build-ahead — no current worker emits HRV/Sleep/Weight.
- Matcher CALIBRATED against the operator's real data, now WITH a second provider
  connected (961 activities, 893 Garmin + 315 Strava sources). Three measurements
  (`internal/domain/match/calibration_test.go`):
  - **precision 1.0000** — 609 same-window distinct-activity pairs, 0 wrongly
    clustered (skips unless CAIRN_MATCH_CORPUS set).
  - **synthetic recall 1.0000** — 961/961 synthetic auto-push copies re-cluster.
  - **real cross-provider recall 1.0000** — 257/257 confirmed same-workout groups
    (activities the engine fused from Garmin+Strava in production) re-cluster when
    fed back through the matcher (CAIRN_MATCH_SAME_CORPUS, `--same-out`). This
    closes the earlier caveat (recall was synthetic-only when 0 multi-provider
    data existed). The only near-identical-start pairs the matcher keeps separate
    are correct: 5 Garmin multisport containers vs single Strava legs (different
    sport_class + distance) and 1 same-start ride with 33% distance divergence
    (one recording truncated). Default weights/thresholds hold; no tuning needed.
- Migrations 00052–00056 are VERIFIED against a real TimescaleDB (pg17-ts2.27):
  all 56 apply on a fresh DB, and the 5 new ones roll back (down-to 51) and
  re-apply cleanly. Schema confirmed: health_samples is a hypertable, the
  activity_sources.activity_id FK is deferrable, the 7 match-feature columns +
  4 override tables + merge_defaults_json all present.

---

## 0. Where Cairn is today vs the brief (verified)

| Brief | Today | Action |
|---|---|---|
| Immutable archive, re-derivable | ✅ raw JSON → S3, `parse_blob` re-derive, versioned by worker pkg/version | keep; normalization stays worker-side (defensible) |
| Capability manifest per-type axes | ❌ flat bools + `producible_events` list | **rebuild** (Phase 1) |
| Data-type taxonomy incl. health | ⚠️ activity/lap/segment/effort/generic metric only | **extend** (Phase 1 + Phase 10) |
| 3-level priority cascade | ⚠️ 2-level (per-type + per-field-group), no write API | **extend + expose** (Phase 6) |
| Fuzzy scoring + union-find + bands | ❌ hard-threshold incremental attach | **rebuild** (Phase 3) |
| ≤1 source/provider/activity | ❌ multi-source-per-provider allowed | **enforce** (Phase 3) |
| Stable-ID reconciliation, split/merge | ❌ incremental, frozen IDs | **build** (Phase 4) |
| Golden-master tests | ❌ none | **build first** (Phase 0) |
| Scalar field merge | ✅ field-group merge | keep |
| Coupled/derived = computed | ⚠️ only TSS + EndTime | **formalize** (Phase 7) |
| Blocks atomic per source | ✅ | keep |
| Streams per-channel | ❌ single primary source | **rebuild** (Phase 8) |
| Stream alignment/resampling | ❌ none | **build** (Phase 8) |
| Provenance flat sidecar | ⚠️ flat but no `decidedBy`/timestamp | **enrich** (Phase 7) |
| PRs over merged view, cross-activity | ⚠️ best-efforts per-source, query-time merge | **rebuild** (Phase 9) |
| Cross-activity invalidation | ❌ per-changed-source only | **build** (Phase 9) |
| Manual field-source override | ❌ none | **build** (Phase 5) |
| Matching override must/cannot-link | ⚠️ detach only, not durable (Gap 6) | **build** (Phase 5) |

Current schema anchors: migrations end at **00051**; new ones start **00052**.
`instance_settings` exists (00013). Match features live in `activity_sources.parsed`
(jsonb); `activity_sources.activity_id` is `NOT NULL ... ON DELETE CASCADE`.

---

## 1. Target architecture

The invariant from the brief drives everything:

```
merged = derive(archived_raw_records, priority_rules, manual_overrides)
```

Concretely this means **decoupling the four concerns Cairn currently fuses in
`ingest.go`**:

```
   INGEST (per record)              DERIVE (per bucket, pure, re-runnable)
   ─────────────────────           ─────────────────────────────────────
   archive raw  ─────────►  source_records (immutable ref + match features)
   normalize (worker)                       │
   persist source record                    ▼
        │                          RECLUSTER bucket(user, sport, utc-day±margin)
        └── enqueue recluster ───►   = cluster(records, constraints)  [pure]
                                              │
                                              ▼
                                     RECONCILE logical-activity IDs (split/merge)
                                              │
                                              ▼
                                     MERGE per activity (fields/blocks/channels)
                                              │
                                              ▼
                                     ALIGN+RESAMPLE merged stream
                                              │
                                              ▼
                                     DERIVE values (PRs/best-efforts, cross-activity)
```

Hexagonal placement is unchanged: pure logic in `internal/domain/*`, orchestration
in `internal/usecase/*`, IO in `internal/adapter/secondary/postgres/*`, wiring in
`cmd/server/wire.go`. The matcher, reconciler, merger, and stream-aligner are all
**pure** (`domain`) so they're golden-master testable.

### New domain packages
- `internal/domain/match/` — features, decay signals, scoring, union-find clustering, confidence bands.
- `internal/domain/reconcile/` — cluster→logical-activity overlap mapping, split/merge resolution.
- `internal/domain/streamalign/` — finest-axis detection, clock-offset alignment, resampling, gap-capping.
- `internal/domain/capability/` — canonical data-type taxonomy + capability model.

### New use-cases
- `internal/usecase/match/recluster.go` — `ReclusterBucket` (the new derive entry point).
- `internal/usecase/stream/buildmerged.go` — materialize the merged/aligned stream.
- `internal/usecase/health/` — ingest+merge for non-activity data types.

### New ports / tables (detail per phase)
- `match_constraints`, `source_match_denylist`, `field_source_overrides`,
  `activity_merged_streams`, `health_samples`, plus columns on `activity_sources`
  and a widened `activities.merge_provenance`.

---

## Phase 0 — Safety net (do first, no behavior change)

**Goal:** golden-master corpus + harness so the matcher/merge rewrite is provably
non-regressing. Brief §7.5.

- `internal/domain/match/testdata/` — fixture corpus of real dual-provider source
  records (Garmin↔Strava auto-push pairs = free ground-truth "same" labels, plus
  hand-labeled "different" pairs and indoor/no-GPS hard cases). **Needs you to
  export a handful of real dual-provider activities** (raw provider JSON) — or I
  synthesize representative fixtures from the existing live import.
- `internal/usecase/activity/golden_test.go` — pin current ingest/merge output as a
  golden file so Phase 3 can diff old-vs-new clustering decisions.
- `scripts/extract-match-corpus.sh` — pull raw blobs from S3 → anonymized fixtures.

**Verify:** `go test ./...` green; golden files committed.

---

## Phase 1 — Canonical taxonomy + capability manifest

**Goal:** brief §4. Provider-neutral data-type taxonomy + per-type capability axes.

- `internal/domain/capability/taxonomy.go` — canonical `DataType` enum:
  `Activity, Lap, SegmentEffort, PersonalBest, Segment, Gear, AthleteProfile,
  Steps, HRV, RestingHR, Sleep, Weight, WaterIntake, …`. (Note the brief's naming
  warning: `PersonalBest`, never "best effort" for delivery semantics.)
- `internal/domain/capability/manifest.go` — `Capability{Read, Write, Push, Backfill bool; Granularity string}`
  and `Manifest map[DataType]Capability`.
- **Proto** `api/proto/cairn/worker/v1/worker_service.proto` — replace the flat
  `supports_*` bools + `producible_events` on `ProviderManifest` with
  `map<string, DataTypeCapability> capabilities` (keep old fields as deprecated for
  one release to avoid breaking the live worker). `make proto`.
- `internal/workersdk/` — SDK helper to declare a `Manifest`; populate the presence
  heartbeat from it. `cmd/worker-strava/main.go` declares Strava's real manifest.
- `cmd/server/admin_api.go` + `WorkerOnboarding.svelte` — display per-type
  capabilities ("this provider gives you Activity, SegmentEffort, …").
- **Sync orchestration** reads capabilities to route which provider sources which
  type (replaces the implicit "everything is an activity" assumption).

**Migration:** none (manifest is KV/heartbeat).
**Verify:** worker advertises per-type manifest; admin shows it; build+vet+`npm run check`.

---

## Phase 2 — Normalization formalization + match-feature denormalization

**Goal:** make the canonical schema explicit and give the matcher cheap blocking
fields. Brief §4 (normalization layer), §6.

- `internal/domain/canonical.go` — document the canonical activity schema as the
  contract the worker maps onto (it already does via proto; this pins it + adds a
  `mappingVersion`). Versioning already exists via `source_worker_package/version`.
- **Migration 00052** `activity_source_match_features.sql` — add denormalized,
  indexed columns to `activity_sources`: `sport_class text`, `start_utc timestamptz`,
  `distance_m double precision`, `moving_s bigint`, `elapsed_s bigint`,
  `start_lat/start_lng double precision`. Backfill from `parsed`. Index
  `(user_id, sport_class, start_utc)` for blocking.
- `internal/adapter/secondary/postgres/activity_repo.go` — populate match-features
  on source insert/reimport; add `ListSourceRecordsInBucket(userID, sportClass, utcDay±margin)`.
- `internal/port/activity.go` — port method for the bucket query.

**Verify:** features backfilled; bucket query returns expected sets; golden tests still green.

---

## Phase 3 — Matching engine (fuzzy scoring + union-find + bands)

**Goal:** brief §7.1–7.3. The core rebuild. Replace hard-threshold incremental
attach with pure scoring + clustering.

- `internal/domain/match/signal.go` — decay functions returning [0,1]:
  `startTime(Δt)` (≈1 at seconds, →0 over minutes), `distance(relDiff)`,
  `duration(relDiff)` (moving-vs-moving, never mixed), `sportCompat(a,b)` via a
  fuzzy compatibility map ("Run"/"Running"/"Trail Run"/"Workout"). Weighted combine.
  GPS start-coordinate / track similarity as **tiebreaker only**.
- `internal/domain/match/score.go` — `Score(a, b Features) (s float64, gated bool)`;
  sport-compat is gating, the rest weighted. Weights in one tunable struct
  (`DefaultWeights`) — calibratable against the Phase-0 corpus.
- `internal/domain/match/cluster.go` — blocking (caller passes a bucket) → pairwise
  score → edge threshold → **union-find** with deterministic tie-break (order by
  source-record ID) → **≤1-source-per-provider** hard constraint (same-provider
  collision in a component = alarm, not merge) → confidence band per cluster
  (`high`→auto, `medium`→flag, `low`→keep separate).
- `internal/domain/match/cluster_test.go` — golden-master against Phase-0 corpus;
  precision/recall report.
- **UTC everywhere** (brief's #1 silent bug): bucket by UTC day ± margin; all Δt in UTC.

**This phase produces clusters but does not yet rewrite the DB** — Phase 4 wires it.

**Verify:** `go test ./internal/domain/match/...`; precision/recall on corpus ≥ target.

---

## Phase 4 — Re-cluster use-case + identity reconciliation (split/merge)

**Goal:** brief §7.4, §12. Decouple ingest; make logical-activity assignment a
derived output with stable IDs.

- **Migration 00053** `reclustering.sql`:
  - relax `activity_sources.activity_id` from `ON DELETE CASCADE` to allow reassignment;
    add `match_confidence text`, `needs_review bool`.
  - `match_constraints (user_id, source_a, source_b, kind 'must_link'|'cannot_link', reason, created_at)`.
  - `activity_id_redirects (old_id, new_id, reason, created_at)` for merge-away IDs
    referenced by old URLs/notes.
- `internal/domain/reconcile/reconcile.go` — pure: given new clusters + existing
  source→activity map, assign each new cluster the existing activity ID it shares
  the most source records with; handle **split** (one old → N new: largest keeps
  ID, others get fresh IDs, overrides migrate to the side holding the pinned source)
  and **merge** (N old → one new: oldest/largest ID wins, losers → redirects +
  soft-delete, overrides union with conflict rule).
- `internal/usecase/match/recluster.go` — `ReclusterBucket(ctx, userID, bucketKey)`:
  load bucket records + constraints → `match.Cluster` → `reconcile.Reconcile` →
  persist reassignments/splits/merges → re-merge each affected activity → enqueue
  downstream derive (Phases 8/9) for affected activities. Pure core, deterministic,
  idempotent.
- **Rewire ingest** `internal/usecase/activity/ingest.go`: shrink to
  `IngestSourceRecord` = archive + normalize + persist source (+ match features),
  then `ReclusterBucket`. `handleReimport` becomes "replace source payload → recluster".
  The old 3-stage attach logic is deleted (its decisions are now the matcher's).
- `cmd/server/result_router.go` — call the new ingest path; `runFollowUps` keyed by
  the set of affected activities the recluster returns (not the single source).
- Soft-delete-on-empty-activity stays (recompute.go behavior preserved).

**Verify:** golden diff old→new clustering reviewed; split/merge unit-tested;
detach + re-ingest no longer silently reverts (leads into Phase 5); live re-import
of the existing 260-activity dataset re-clusters identically (or differences explained).

---

## Phase 5 — Manual overrides (durable inputs)

**Goal:** brief §13. Overrides are inputs, stored separately, survive every re-run.

- **Migration 00054** `manual_overrides.sql`:
  - `field_source_overrides (id, user_id, activity_id, field_key, source_id, decided_by 'manual', created_at)`
    — per field-group/channel source pin ("for this activity, distance from Strava").
  - `source_match_denylist (user_id, provider, external_account_id, external_id, reason, created_at)`
    — fixes **Gap 6**: a detached source that gets re-pushed is not silently re-attached.
- `internal/port` + `postgres` repos for both tables.
- **Cannot-link**: detach now writes a `match_constraints` cannot-link AND a denylist
  row; `match.Cluster` honors cannot-link as a hard split; ingest checks the denylist
  before re-attaching. Durable across re-ingest.
- **Must-link**: new endpoint `POST /api/activities/merge` (join two activities) →
  writes a must-link constraint → recluster → reconcile merges them. And
  `POST /api/activities/{id}/split` for the inverse.
- **Field-source override**: extend Connect `UpdateActivity` (or a new REST endpoint)
  to set `field_source_overrides`; `domain.Merge` consumes them as `decidedBy:manual`
  winners that the cascade cannot override; reconciliation migrates them on split/merge.
- UI: per-field "source badge" dropdown to pin a source (Phase 7 surfaces the badge);
  "these are/aren't the same" actions on the activity manage page.

**Verify:** detach survives re-ingest (denylist); join/split round-trip; pinned field
survives recompute + new import.

---

## Phase 6 — Priority cascade: write API + per-instance defaults

**Goal:** brief §5. Make the 2-level cascade user-configurable and add level 3.

- **Migration 00055** — add `merge_defaults_json` to `instance_settings` (the
  "future migration" already referenced in `internal/domain/merge.go`).
- `internal/port/user.go` — add `SetMergePolicy(userID, activityType, policy)`
  (today read-only).
- `cmd/server/` — REST: `GET/PUT /api/settings/merge-policy` (per-user global order +
  per-type overrides); admin `PUT /api/admin/instance/merge-defaults`.
- Cascade resolution order in `PolicyResolver`: per-instance default → per-user global →
  per-type override → per-field-group override → **per-instance/per-field manual
  override** (Phase 5) wins finest.
- UI: a merge-policy settings page (drag-order providers; per-type + per-field tweaks).

**Verify:** set Garmin>Strava for rides → re-merge picks Garmin power; per-field
override beats it.

---

## Phase 7 — Provenance enrichment + derived-fields-computed

**Goal:** brief §8.1, §10.

- **Migration 00056** — widen `activities.merge_provenance` from
  `map[fieldgroup]sourceid` to `map[fieldgroup]{source_id, decided_by, synced_at}`.
  Backfill `decided_by:"rule"`, `synced_at = merged_at`.
- `internal/domain/merge.go` — `MergeProvenance` becomes a richer type; `decidedBy`
  drives re-derivation (manual entries untouched on re-run — ties into Phase 5).
- `internal/domain/merge_engine.go` — formalize **coupled-field groups** (distance↔
  duration↔pace; the field-group model already half-does this) and mark **derived**
  fields (`avgPace`, `avgSpeed`, `grade`, `vam`, `TSS`) as `computed` — recompute from
  merged primaries instead of merging. Today only TSS/EndTime are computed.
- **Proto/convert** — provenance entries carry source + provider name + timestamp +
  decidedBy; resolve provider name at read.
- UI — per-field **source badge** + tooltip (origin provider + sync time + rule/manual).

**Verify:** badge shows correct provider+time; derived fields internally consistent
(no Garmin-distance/Strava-duration pace mismatch); manual provenance survives re-merge.

---

## Phase 8 — Per-channel stream merge + alignment/resampling

**Goal:** brief §8.3, §9. Replace single-primary-source streams with per-channel
best-source + a derived aligned stream.

- `internal/domain/streamalign/align.go` — pure:
  1. **finest axis** = source with smallest median Δt (not raw sample count);
  2. **clock-offset alignment** before resampling (shared start event or
     cross-correlation);
  3. **dynamic regular grid** from detected finest rate; resample all channels incl. anchor;
  4. **interpolation**: linear for continuous (elevation/HR/speed), **nearest/step**
     for discrete (lap markers, moving flag), **gap cap** (don't bridge pauses/dropouts
     → mark missing);
  5. per-channel winner via the cascade (elevation from Garmin, speed from Strava),
     with **coupled-channel groups** (latlng↔speed↔distance pulled together).
- **Migration 00057** `activity_merged_streams.sql` — TimescaleDB hypertable holding
  the derived aligned/merged stream keyed by `activity_id` (re-derivable, droppable).
  Per-channel provenance into the Phase-7 sidecar (channel keys: elevation/speed/
  heart_rate/power/cadence/latlng/temperature).
- `internal/usecase/stream/buildmerged.go` — materialize the merged stream at recompute;
  TimescaleDB CAggs run on top for downsampling (existing 5s/30s logic adapts to the
  merged hypertable).
- Read paths (activity detail map/charts, zones) read the merged stream; raw per-source
  streams remain in the archive untouched.

**Verify:** an activity with two GPS sources renders one aligned overlay; peaks line up
(offset alignment); pauses show gaps not fabricated data; per-channel badges correct.

---

## Phase 9 — Cross-provider PRs over merged view + cross-activity invalidation

**Goal:** brief §11, §12 (the headline feature) + §7.4 ripple.

- `internal/usecase/besteffort/` — `ComputeBestEffortsForActivity` reading the
  **merged aligned stream** (Phase 8), replacing `ComputeBestEffortsForSource`.
  Store best efforts keyed by `activity_id` (not source).
- **Migration 00058** — best-effort rows re-keyed to activity; add a
  `personal_records` concept (cross-activity rank per (user, metric, window), like
  segment ranks) so "true cross-provider PRs" are stored, not just UI-aggregated.
- **Cross-activity invalidation**: when an activity's merged best-efforts change (new
  source, re-cluster, override, rule change), recompute PR ranks across the affected
  (user, metric, window) set — not just the changed activity. Wire into the recluster
  cascade (Phase 4) and `runFollowUps`.
- Segment ranks already cross-activity per segment — fold into the same invalidation trigger.

**Verify:** a new Garmin source that beats a Strava 5k flips the stored PR; an unrelated
older activity's rank updates; progression page reflects merged-view bests.

---

## Phase 10 — Health-data taxonomy (HRV / Sleep / Weight / Steps / Water)

**Goal:** brief §4 taxonomy, §8 (atomic-per-source), §10 (provenance) for non-activity
data. These are daily time-series, so they **bypass the activity matcher** but follow
the same archive→normalize→merge→provenance discipline.

- **Migration 00059** `health_samples.sql` — TimescaleDB hypertable
  `(user_id, data_type, ts, source_id, value …)` + a derived merged view per
  `(user_id, data_type, day/interval)`.
- `internal/domain/health/` — canonical health types + per-type merge (atomic per
  source via the cascade; e.g. HRV from Whoop even if Garmin higher overall).
- `internal/usecase/health/ingest.go` + `merge.go` — archive raw → normalize →
  dedup by (type, day, source) → merge by cascade → provenance sidecar.
- **Proto/worker** — `producible_events`/manifest already extended in Phase 1; worker
  emits health events; result router routes by `DataType`.
- REST + UI — health dashboards (HRV/sleep/weight trends) with source badges.

**Note:** requires a provider that actually supplies these. Strava doesn't; this phase
is build-ahead unless a Garmin/Whoop/Apple worker exists. Flag at kickoff.

**Verify:** synthetic health import → merged daily series with correct per-type source winner.

---

## Cross-cutting

- **Determinism & idempotency** (invariant checklist): every pure stage sorts inputs
  by ID; re-running a bucket yields bit-identical output. A `recluster` is safe to
  replay.
- **Migrations**: 00052–00059 (8 new). None rewrite shipped federation/social tables.
- **Verification gates per phase**: `go build ./...`, `go vet ./...`, `go test ./...`,
  `cd web && npm run check`, plus the golden-master diff for Phases 3–4.
- **Rollout safety**: Phases 0–2 are additive (no behavior change). Phase 3–4 is the
  risky core — gated behind the golden corpus + a feature flag
  (`CAIRN_RECLUSTER_ENABLED`) so old and new paths can run side-by-side on the live
  dataset before cutover.
- **Out of scope** (brief §15): legal/retention, deletion policy beyond re-calc,
  OAuth/token refresh (done), ongoing weight tuning.

## Suggested order & rough size

| Phase | Theme | Size | Gates on |
|---|---|---|---|
| 0 | Golden corpus | S | — (needs sample data) |
| 1 | Capability manifest + taxonomy | M | — |
| 2 | Match features + normalization | S | 1 |
| 3 | Matching engine (pure) | L | 0, 2 |
| 4 | Recluster + reconciliation | L | 3 |
| 5 | Manual overrides | M | 4 |
| 6 | Priority cascade API/UI | M | — (parallel) |
| 7 | Provenance + computed fields | M | 6 |
| 8 | Per-channel streams + align | L | 7 |
| 9 | Cross-provider PRs + invalidation | L | 4, 8 |
| 10 | Health-data taxonomy | L | 1 (+ a provider) |

Phases 1, 6 can run parallel to the 3→4 critical path. 8→9 are the second large block.
