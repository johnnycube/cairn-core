# Changelog

All notable changes to Cairn are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project aims to follow
[Semantic Versioning](https://semver.org/). Dates are ISO-8601.

## [Unreleased]

### Added
- Heatmap (`/heatmap`): every GPS track the filter matches, drawn as
  translucent lines so the routes you ride most burn brightest; click a track
  to open the activity. Backed by `GET /api/activities/heatmap`, which takes
  the same filter params as the feed and serves the tracks from the 30 s
  aggregates in one response (newest 5000 activities, flagged when truncated).
- The activity filter bar is now one shared component (`ActivityFilterBar`
  over an `ActivityFilter` model) used by both `/activities` and `/heatmap`,
  so the filter vocabulary is identical everywhere.
- Manage page: "Regenerate map snapshot" re-renders the static `map.png`
  (`POST /api/activities/{id}/map/regenerate`, owner-only) for when the track
  changed under the cache or a render failed.
- `scripts/dev-sample-data.py` seeds a dev instance with synthetic GPX
  activities (favourite loops + one-offs) through the normal upload path.

### Fixed
- Interactive maps were blank in production builds (no tiles, no route):
  MapLibre ≥6 resolves its web worker relative to `import.meta.url`, which is a
  hashed chunk path in the Vite bundle, so the worker 404'd and nothing was
  parsed. The worker is now pinned to a Vite-bundled asset. Dev mode was
  unaffected, which is why v0.2.4 shipped with it.
- `map.png` responses carry an ETag and a short private max-age instead of a
  day-long public one, so regenerated snapshots show up promptly.

## [0.2.4] — 2026-08-28

### Added
- Export options: `GET /api/activities/{id}/export` accepts
  `?exclude=gps,altitude,distance,speed,heart_rate,power,cadence,temperature,title`
  to strip data from the exported GPX/TCX/FIT file; the manage page exposes the
  same choices as checkboxes.

### Fixed
- Maps showed an "API KEY REQUIRED" watermark: CARTO started stamping its free
  Voyager raster tiles. The interactive map (activity page, privacy-zone picker)
  now defaults to OpenFreeMap's key-less Liberty vector style
  (`VITE_MAP_STYLE_URL` still overrides it). The server-rendered activity
  snapshot (`/api/activities/{id}/map.png`) fetches from the new
  `CAIRN_MAP_TILE_URL` (default: public OSM tiles; set `CAIRN_MAP_USER_AGENT`
  for policy-compliant identification) and its S3 cache key was bumped to v5 so
  watermarked snapshots are re-rendered.
- Scheduled reconcile re-imported the newest activity of every account on each
  tick: the worker window starts at watermark − 1 h, so the activity that *is*
  the watermark always fell inside it, and core never populated the workers'
  `known_ext_ids` skip-list. Core now sends the external IDs it already holds
  for the window; a lookup failure degrades to the old behaviour instead of
  blocking sync.

### Changed
- Dependencies: maplibre-gl 5 → 6.6, SvelteKit/Svelte/Vite/Paraglide minors,
  Go modules (AWS SDK, smithy-go, protobuf, x/crypto, nats.go), dev-stack NATS
  2.14.6 and Mailpit 1.31.

## [0.2.3] — 2026-07-29

### Added
- Activities page: year facet + year filter (sugar over the existing `from`/`to`
  range; the year list is deliberately unfiltered so it always offers every
  year with data) and proper paging instead of endless lists.

### Fixed
- Activities page no longer flickers when filters change.
- CI: images are signed keyless with cosign (Sigstore OIDC) and published with
  provenance attestations + SBOM; the Gitea mirror workflows were dropped.

## [0.2.2] — 2026-07-20

### Fixed
- GitHub Actions: the containerized test job now marks the checkout as a git
  `safe.directory`, fixing the "error obtaining VCS status" build failure that
  broke the v0.2.1 release pipeline. No code changes since 0.2.1 — this release
  exists to publish the images that release never produced.

## [0.2.1] — 2026-07-20

### Fixed
- NATS JetStream now binds an explicit `StorageClass` in the Kubernetes
  manifest. With no default StorageClass the claim stayed Pending and silently
  fell back to an `emptyDir`, losing all JetStream state (streams + KV) on every
  pod reschedule.
- Stale worker manifests are reaped once a worker has no live presence, and the
  `cairn_worker_manifests` KV bucket now carries a 15-minute TTL. Previously a
  decommissioned or replaced worker's manifest lingered indefinitely and
  replayed through the update-available scan on every server restart. Pairs with
  the Strava worker's periodic manifest re-publish ([cairn-provider-strava]
  ≥ v0.2.1); older workers are still cleaned up by the presence-driven reaper.

## [0.2.0] — 2026-07-16

**First public release.** A working, self-hosted multi-source activity
tracker: import → merge → analysis → web UI, plus a social layer and optional
federation. Single Go binary (API + embedded SPA) with separate provider
workers ([cairn-provider-strava], [cairn-provider-garmin]).

### Import & sources
- NATS/JetStream worker control plane + async job/result bus; provider workers
  fetch and push typed results that run through one ingest path.
- **Strava** worker (Go reference implementation) and **Garmin** worker
  (Python) — proving the model is provider- and language-agnostic. The
  normative provider contract is documented on the docs site
  (https://docs.opencairn.org/architecture/provider-contract), and
  `cmd/worker-conformance` verifies a running worker implements it (presence
  heartbeat, durable consumers, discover reply shape, reconcile round-trip).
- Worker results are **claim-checked through the blob store**: event-carrying
  results upload via presigned PUT and travel as a small envelope, so large
  activities (long rides, dense streams, many segment efforts) aren't capped
  by the NATS payload ceiling. Terminal worker failures and deterministic
  ingest errors fail the import-queue item immediately with the true reason.
- File upload (GPX/TCX/FIT) and **manual activity entry**, both fed through the
  same ingest → merge pipeline as worker imports.
- Resumable full-sync (diff-only-vs-redownload), reconciliation scheduler,
  per-account auto-import suspend, webhook ingress (generic forwarder).
- Raw-response archival to S3/MinIO + quota-free re-parse from the archive;
  archived-blob content-type + size recorded for the manage view.
- Stream ingest tolerates provider quirks such as duplicate sample timestamps
  (last sample wins, order preserved).

### Merge & editing
- Per-field-group multi-source merge with a user-configurable policy; identity
  dedup + fuzzy re-clustering (match + union-find) of same-workout sources.
- `preserveUserEdits` (title/description/tags/privacy/gear), per-field source
  pins, and a post-merge user **overlay** for classification (sport/discipline/
  flags/custom subtype) **and** summary metrics (distance/elevation/moving
  time) that survives re-import.
- **Source data from another activity** — derived source carrying selected
  field-groups + the donor's geo stream rebased in time, pinned via overrides.
- Full sport-type set; activity **attachments** (photos) as a first-class
  entity, Strava photos mirrored to S3, visibility-gated.

### Analysis
- Best-effort curves (power/HR/speed/pace/VAM, duration- and distance-windowed)
  with personal records.
- Segment matching (PostGIS bbox + corridor-walking) with per-user/per-instance
  leaderboard ranks.
- Training load (Banister CTL/ATL/TSB, per-user FTP/LTHR as-of each activity),
  time-in-zone (HR + power), per-activity TSS, laps.
- Activity export (GPX/TCX/FIT) generated from the merged stream.

### Web app (SvelteKit, embedded)
- Grouped sidebar navigation (main / Social / Account / Admin) with a mobile
  off-canvas drawer.
- Activities feed (facets/sort/pagination) with dynamic filters — date presets
  + custom range, tri-state classification flags (virtual/e-bike/commute/race),
  and numeric ranges (distance, duration, elevation gain, avg speed/HR/power)
  in the user's display units; rich activity detail (map ↔ stream cross-hair,
  elevation profile, zones, segment efforts, laps, photo gallery), full-screen
  map/stream pages, create/edit activity, per-activity manage page (provenance,
  re-fetch, re-parse-from-archive, detach, split, export).
- Segments landing, similar-routes + best-effort progression, analysis/stats,
  athlete physiology profile, connections, admin.

### Accounts, auth & social
- Password + WebAuthn/passkeys + OIDC; invites; personal access tokens; per-user
  provider OAuth configs (bring-your-own Strava app).
- Social layer: follow graph + following feed, public profiles, share links,
  kudos + comments, clubs, blocking, content reports + moderation queue,
  per-field visibility policy (audience × category) + privacy zones, per-user
  activity quotas, delegated-admin (moderator) role.
- Optional **ActivityPub federation** (off by default; per-user opt-in): remote
  follow, publishing public activities, federated kudos/comments, signed
  delivery queue, instance defederation, per-domain inbox rate-limiting.

### Notifications & ops
- In-app feed + email + signed outbound webhooks; per-type preferences,
  tz-aware quiet hours, per-event delivery audit.
- Prometheus `/metrics`, deep `/readyz`, HTTP rate limiting, master-key +
  NATS-account-key rotation, dead-letter capture/replay.

### Build & CI
- Single distroless prod image (`docker/Dockerfile.core`, embedded SPA) +
  worker images.
- Shared **build-base** toolchain image (Go + Node + buf + protoc + local
  plugins) so `buf generate` runs offline; the core/Strava images build
  `FROM build-base`.
- CI: per-image build + test workflows; images publish only from `main` and
  `v*` tags, MRs build/test as a gate without pushing.

## [0.1.0] — 2026-06-21

Initial development (internal pre-release iterations v0.1.0–v0.1.12).

[0.2.4]: https://github.com/johnnycube/cairn-core/releases/tag/v0.2.4
[0.2.3]: https://github.com/johnnycube/cairn-core/releases/tag/v0.2.3
[0.2.2]: https://github.com/johnnycube/cairn-core/releases/tag/v0.2.2
[0.2.1]: https://github.com/johnnycube/cairn-core/releases/tag/v0.2.1
[0.2.0]: https://github.com/johnnycube/cairn-core/releases/tag/v0.2.0
[cairn-provider-strava]: https://github.com/johnnycube/cairn-provider-strava
[cairn-provider-garmin]: https://github.com/johnnycube/cairn-provider-garmin
