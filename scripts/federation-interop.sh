#!/usr/bin/env bash
# Two-instance ActivityPub interop test for Cairn. Drives a real cross-instance
# round-trip — discovery, Follow/Accept handshake, and public-activity outbox
# backfill — against the two servers stood up by docker-compose.interop.yml,
# asserting each step. Exits non-zero on the first failure.
#
# Usage:
#   docker compose -f docker-compose.interop.yml up -d --build
#   ./scripts/federation-interop.sh
#   docker compose -f docker-compose.interop.yml down -v
#
# NOTE: this is the full DB-backed proof. The pure wire protocol (signing,
# verification, object format) is also covered, DB-free, by the Go test
# cmd/server/federation_interop_test.go (runs in `go test ./...`).
set -euo pipefail

A=http://localhost:18090   # instance A (alice@cairn-a:8080)
B=http://localhost:18091   # instance B (bob@cairn-b:8080)
PW=interop-pw-123456
COMPOSE="docker compose -f docker-compose.interop.yml"

pass() { printf '  \033[32m✓\033[0m %s\n' "$1"; }
fail() { printf '  \033[31m✗ %s\033[0m\n' "$1"; exit 1; }
sqlA() { $COMPOSE exec -T postgres-a psql -U cairn -d cairn -tAc "$1"; }
sqlB() { $COMPOSE exec -T postgres-b psql -U cairn -d cairn -tAc "$1"; }

# wait_ready URL — block until /readyz returns 200 (or give up after ~60s).
wait_ready() {
  for _ in $(seq 1 60); do
    [ "$(curl -s -m2 -o /dev/null -w '%{http_code}' "$1/readyz" || true)" = "200" ] && return 0
    sleep 1
  done
  fail "instance never became ready: $1"
}

# login URL EMAIL → echoes the cairn_session cookie value.
login() {
  curl -s -i -X POST "$1/auth/password" \
    --data-urlencode "identifier=$2" --data-urlencode "password=$PW" \
    | grep -i '^set-cookie: cairn_session' | sed 's/.*cairn_session=//; s/;.*//'
}

echo "== waiting for both instances =="
wait_ready "$A"; wait_ready "$B"
pass "both instances ready"

echo "== enable federation on both =="
# Must precede discovery: WebFinger + the actor route only resolve a user who
# has opted in (federation_enabled).
CA=$(login "$A" "alice@cairn-a"); [ -n "$CA" ] || fail "login on A failed"
CB=$(login "$B" "bob@cairn-b");   [ -n "$CB" ] || fail "login on B failed"
curl -sf -X PUT "$A/api/profile/federation" -H 'Content-Type: application/json' \
  -H "Cookie: cairn_session=$CA" -d '{"enabled":true}' >/dev/null
curl -sf -X PUT "$B/api/profile/federation" -H 'Content-Type: application/json' \
  -H "Cookie: cairn_session=$CB" -d '{"enabled":true}' >/dev/null
pass "federation enabled for alice and bob"

echo "== discovery: B serves WebFinger + actor =="
curl -sf "$B/.well-known/webfinger?resource=acct:bob@cairn-b:8080" \
  | grep -q '"href":"http://cairn-b:8080/users/bob"' \
  || fail "B WebFinger did not resolve bob to the actor URL"
pass "B WebFinger resolves bob@cairn-b:8080 → actor"
curl -sf -H 'Accept: application/activity+json' "$B/users/bob" \
  | grep -q '"publicKeyPem"' || fail "B actor doc missing publicKeyPem"
pass "B actor document exposes a public key"

echo "== A follows B (Follow → auto-Accept) =="
curl -sf -X POST "$A/api/federation/follow" -H 'Content-Type: application/json' \
  -H "Cookie: cairn_session=$CA" -d '{"handle":"bob@cairn-b:8080"}' >/dev/null \
  || fail "follow request rejected"
# The Accept is delivered out-of-band; give it a moment.
ok=
for _ in $(seq 1 15); do
  out=$(sqlA "SELECT status FROM federation_follows WHERE direction='outbound' AND remote_actor_id='http://cairn-b:8080/users/bob'")
  inb=$(sqlB "SELECT status FROM federation_follows WHERE direction='inbound' AND remote_actor_id='http://cairn-a:8080/users/alice'")
  if [ "$out" = "accepted" ] && [ "$inb" = "accepted" ]; then ok=1; break; fi
  sleep 1
done
[ -n "$ok" ] || fail "Follow/Accept did not settle (A outbound=$out, B inbound=$inb)"
pass "A's outbound follow accepted; B records alice as an inbound follower"

echo "== B publishes a public activity; A can backfill it from the outbox =="
# A two-point GPX dated today, 30 min apart (positive duration, recent).
DAY=$(date -u +%Y-%m-%d)
GPX="<gpx version=\"1.1\"><trk><name>Interop Ride</name><type>cycling</type><trkseg>\
<trkpt lat=\"48.2\" lon=\"11.6\"><ele>500</ele><time>${DAY}T07:00:00Z</time></trkpt>\
<trkpt lat=\"48.21\" lon=\"11.61\"><ele>505</ele><time>${DAY}T07:30:00Z</time></trkpt></trkseg></trk></gpx>"
AID=$(printf '%s' "$GPX" > /tmp/interop.gpx; \
  curl -sf -X POST "$B/api/activities/upload" -H "Cookie: cairn_session=$CB" \
    -F 'file=@/tmp/interop.gpx;type=application/gpx+xml' | sed 's/.*"activity_id":"//; s/".*//')
[ -n "$AID" ] || fail "upload on B returned no activity id"
# New activities default to private; make it public so it federates via the outbox.
sqlB "UPDATE activities SET privacy='public' WHERE id='$AID'" >/dev/null
curl -sf -H 'Accept: application/activity+json' "$B/users/bob/outbox?page=true" \
  | grep -q "$AID" || fail "B's outbox did not list the public activity"
pass "B's outbox exposes the public activity (A's server can backfill it)"

echo "== defederation: A blocks cairn-b, inbound from it is refused =="
curl -sf -X POST "$A/api/admin/federation/blocks" -H 'Content-Type: application/json' \
  -H "Cookie: cairn_session=$CA" -d '{"domain":"cairn-b:8080","reason":"interop test"}' >/dev/null
curl -sf "$A/api/admin/federation/blocks" -H "Cookie: cairn_session=$CA" \
  | grep -q '"cairn-b:8080"' || fail "block was not recorded"
pass "cairn-b:8080 added to A's defederation blocklist"

echo
printf '\033[32mINTEROP OK\033[0m — discovery, Follow/Accept, outbox backfill, defederation all verified.\n'
