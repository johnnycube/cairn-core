#!/usr/bin/env bash
set -euo pipefail

# extract-match-corpus.sh — OPERATOR-RUN, NOT CI.
#
# Exports an ANONYMIZED matcher-calibration corpus from a live Cairn database,
# for the data-grounded calibration harness in internal/domain/match
# (calibration_test.go). See docs/merge-layer-rewrite-plan.md §0/§7.5.
#
# What it measures once fed to the harness
# (CAIRN_MATCH_CORPUS=<out> go test ./internal/domain/match -run Calibration):
#   - PRECISION: real activities are distinct workouts; the matcher must NOT
#     cluster any same-window pair (the brief's #1 risk: fusing two real
#     workouts). Stresses real back-to-back near-misses (bricks, stop/restart).
#   - RECALL: synthetic second-provider copies of each real activity must cluster.
#
# Ground truth quality scales with your data:
#   - PRE-rewrite schema (no activity_sources.sport_class): exports per-ACTIVITY
#     features from `activities`. Good for the precision audit + synthetic recall.
#   - POST-rewrite, with 2+ providers connected: extend the query to export
#     per-SOURCE rows incl. provider, and add a "same" set = activities carrying
#     sources from >=2 distinct providers (Garmin->Strava auto-push = free
#     confirmed-same labels). That gives true recall on real duplicates.
#
# ANONYMIZATION: titles/descriptions are never selected; coordinates are rounded
# to 2 decimals (~1 km); all start times are shifted by a fixed offset (only
# intra-pair deltas matter to the matcher, so this preserves calibration
# validity while leaking neither exact locations nor dates). Do NOT commit the
# output — it is local calibration input.

usage() {
  cat <<'EOF'
Usage: scripts/extract-match-corpus.sh [--out FILE]

  --out FILE   Output path (default: stdout). Feed it to the harness via
               CAIRN_MATCH_CORPUS=<file>.

Required environment:
  CAIRN_DATABASE_URL   Postgres DSN with read access to `activities`.
                       e.g. postgres://cairn:cairn@127.0.0.1:5432/cairn?sslmode=disable
EOF
}

OUT="-"
SAME_OUT=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --same-out) SAME_OUT="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 2 ;;
  esac
done

: "${CAIRN_DATABASE_URL:?set CAIRN_DATABASE_URL to your Postgres DSN}"

# A fixed, arbitrary epoch offset so exported start times don't reveal real
# dates. Intra-pair deltas (what the matcher scores on) are preserved.
TIME_SHIFT="${CAIRN_CORPUS_TIME_SHIFT:-1000000000}"

read -r -d '' SQL <<SQL || true
SELECT coalesce(json_agg(json_build_object(
  'type',       type,
  'start_utc',  extract(epoch from start_time)::bigint - ${TIME_SHIFT},
  'distance_m', coalesce(distance_m, 0),
  'moving_s',   coalesce(moving_duration_s, 0),
  'elapsed_s',  coalesce(elapsed_duration_s, 0),
  'lat',        round(start_lat::numeric, 2),
  'lng',        round(start_lng::numeric, 2)
)), '[]'::json)
FROM activities
WHERE deleted_at IS NULL;
SQL

# Real same-workout groups: per-SOURCE features for every activity carrying
# sources from >=2 distinct providers. Each group is the set the re-cluster
# engine already fused in production (one source per provider, earliest import)
# — true confirmed-same labels for the real cross-provider RECALL measurement
# (TestCalibration_RealCrossProviderRecall). Needs the post-rewrite per-source
# columns + 2+ connected providers; empty array otherwise.
read -r -d '' SAME_SQL <<SQL || true
WITH firstper AS (
  SELECT DISTINCT ON (activity_id, provider)
    activity_id, provider, sport_class, start_utc, distance_m, moving_s, elapsed_s, start_lat, start_lng
  FROM activity_sources
  WHERE status <> 'detached'
  ORDER BY activity_id, provider, imported_at
),
grp AS (
  SELECT activity_id, count(*) AS n, json_agg(json_build_object(
    'provider',   provider,
    'type',       sport_class,
    'start_utc',  extract(epoch from start_utc)::bigint - ${TIME_SHIFT},
    'distance_m', coalesce(distance_m, 0),
    'moving_s',   coalesce(moving_s, 0),
    'elapsed_s',  coalesce(elapsed_s, 0),
    'lat',        round(start_lat::numeric, 2),
    'lng',        round(start_lng::numeric, 2)
  )) AS sources
  FROM firstper GROUP BY activity_id
)
SELECT coalesce(json_agg(sources), '[]'::json) FROM grp WHERE n >= 2;
SQL

emit() { psql "$CAIRN_DATABASE_URL" -tAc "$SQL"; }
emit_same() { psql "$CAIRN_DATABASE_URL" -tAc "$SAME_SQL"; }

if [[ "$OUT" == "-" ]]; then
  emit
else
  emit > "$OUT"
  echo "wrote anonymized corpus to $OUT" >&2
  echo "calibrate: CAIRN_MATCH_CORPUS=$OUT go test ./internal/domain/match -run Calibration -v" >&2
fi

if [[ -n "$SAME_OUT" ]]; then
  emit_same > "$SAME_OUT"
  echo "wrote real same-workout groups to $SAME_OUT" >&2
  echo "calibrate recall: CAIRN_MATCH_SAME_CORPUS=$SAME_OUT go test ./internal/domain/match -run Calibration -v" >&2
fi
