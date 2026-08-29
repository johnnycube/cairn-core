#!/usr/bin/env python3
"""Seed a dev instance with synthetic GPX activities.

Generates plausible-looking rides/runs/hikes around a centre point — most of
them variations of a handful of "favourite" loops so the heatmap has hot
routes — and uploads them through the normal file-upload endpoint, i.e. the
same ingest path real workers use. Stdlib only.

    set -a; . ./dev.env; set +a
    python3 scripts/dev-sample-data.py --count 60

Credentials default to the bootstrap admin from dev.env
(CAIRN_INSTANCE_BOOTSTRAP_ADMIN_EMAIL / _PASSWORD). Coordinates are
synthetic (rural Lower Franconia), not anyone's home.
"""

import argparse
import datetime as dt
import http.cookiejar
import json
import math
import os
import random
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid

SPORTS = {
    # name → (weight, speed m/s, distance km range, start radius km, elevation amplitude m)
    "Ride": (0.5, 7.5, (25, 90), 6.0, 120),
    "Run": (0.35, 3.0, (5, 18), 2.5, 40),
    "Hike": (0.15, 1.3, (6, 16), 12.0, 250),
}


def offset(lat, lon, dx_m, dy_m):
    """Shift a WGS84 point by metres east (dx) / north (dy)."""
    dlat = dy_m / 111_320.0
    dlon = dx_m / (111_320.0 * math.cos(math.radians(lat)))
    return lat + dlat, lon + dlon


def loop(rng, lat0, lon0, dist_m, wiggle):
    """A closed random loop of roughly dist_m: walk out on a drifting heading,
    then steer back to the start. Returns [(lat, lon)] every ~50 m."""
    step = 50.0
    n = int(dist_m / step)
    pts = []
    x = y = 0.0
    heading = rng.uniform(0, 2 * math.pi)
    for i in range(n):
        if i < n * 0.45:
            heading += rng.gauss(0, wiggle)
        else:
            # Steer home, with some wobble so the return leg isn't a straight line.
            home = math.atan2(-y, -x)
            d = (home - heading + math.pi) % (2 * math.pi) - math.pi
            heading += 0.25 * d + rng.gauss(0, wiggle * 0.6)
        x += step * math.cos(heading)
        y += step * math.sin(heading)
        pts.append((x, y))
    pts.append((0.0, 0.0))
    return [offset(lat0, lon0, px, py) for px, py in pts]


def jitter(rng, track, sigma_m):
    """Re-ride a favourite loop: same shape, GPS-noise-level differences."""
    out = []
    for lat, lon in track:
        out.append(offset(lat, lon, rng.gauss(0, sigma_m), rng.gauss(0, sigma_m)))
    return out


def gpx(name, sport, start, track, speed, ele_amp, rng):
    lines = [
        '<?xml version="1.0" encoding="UTF-8"?>',
        '<gpx version="1.1" creator="cairn dev-sample-data" xmlns="http://www.topografix.com/GPX/1/1">',
        "  <trk>",
        f"    <name>{name}</name>",
        f"    <type>{sport}</type>",
        "    <trkseg>",
    ]
    t = start
    prev = None
    ele0 = rng.uniform(200, 400)
    phase = rng.uniform(0, 2 * math.pi)
    for i, (lat, lon) in enumerate(track):
        if prev is not None:
            d = haversine(prev[0], prev[1], lat, lon)
            v = max(0.3, speed * rng.gauss(1.0, 0.12))
            t += dt.timedelta(seconds=d / v)
        ele = ele0 + ele_amp * math.sin(phase + i / len(track) * 2 * math.pi) + rng.gauss(0, 1.5)
        lines.append(
            f'      <trkpt lat="{lat:.6f}" lon="{lon:.6f}"><ele>{ele:.1f}</ele>'
            f'<time>{t.strftime("%Y-%m-%dT%H:%M:%SZ")}</time></trkpt>'
        )
        prev = (lat, lon)
    lines += ["    </trkseg>", "  </trk>", "</gpx>", ""]
    return "\n".join(lines)


def haversine(lat1, lon1, lat2, lon2):
    r = 6_371_000.0
    p1, p2 = math.radians(lat1), math.radians(lat2)
    dp = p2 - p1
    dl = math.radians(lon2 - lon1)
    a = math.sin(dp / 2) ** 2 + math.cos(p1) * math.cos(p2) * math.sin(dl / 2) ** 2
    return 2 * r * math.asin(math.sqrt(a))


def login(opener, base, identifier, password):
    # Same form post the login page uses; the session cookie lands in the jar.
    req = urllib.request.Request(
        f"{base}/auth/password",
        data=urllib.parse.urlencode({"identifier": identifier, "password": password}).encode(),
        method="POST",
    )
    with opener.open(req) as resp:
        if resp.status != 200 or "error=" in resp.geturl():
            raise SystemExit(f"login failed: {resp.status} {resp.geturl()}")
    # Verify the session actually works rather than trusting the redirect.
    with opener.open(f"{base}/api/activities/feed?limit=1") as resp:
        if resp.status != 200:
            raise SystemExit(f"login failed: HTTP {resp.status} on feed")


def upload(opener, base, filename, body):
    boundary = "----cairn" + uuid.uuid4().hex
    payload = (
        f"--{boundary}\r\n"
        f'Content-Disposition: form-data; name="file"; filename="{filename}"\r\n'
        "Content-Type: application/gpx+xml\r\n\r\n"
    ).encode() + body.encode() + f"\r\n--{boundary}--\r\n".encode()
    req = urllib.request.Request(
        f"{base}/api/activities/upload",
        data=payload,
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
        method="POST",
    )
    try:
        with opener.open(req) as resp:
            return resp.status, json.loads(resp.read().decode() or "{}")
    except urllib.error.HTTPError as e:
        return e.code, {"error": e.read().decode(errors="replace")[:200]}


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base", default=os.environ.get("CAIRN_API_ORIGIN", "http://localhost:8090"))
    ap.add_argument("--email", default=os.environ.get("CAIRN_INSTANCE_BOOTSTRAP_ADMIN_EMAIL", ""))
    ap.add_argument("--password", default=os.environ.get("CAIRN_INSTANCE_BOOTSTRAP_ADMIN_PASSWORD", ""))
    ap.add_argument("--count", type=int, default=60)
    ap.add_argument("--seed", type=int, default=42)
    ap.add_argument("--center", default="50.10,10.20", help="lat,lon")
    ap.add_argument("--days", type=int, default=730, help="spread start dates over the last N days")
    ap.add_argument("--dry-run", action="store_true", help="write GPX files to ./tmp/sample-gpx instead of uploading")
    args = ap.parse_args()
    if not args.dry_run and not (args.email and args.password):
        sys.exit("need --email/--password (or source dev.env for the bootstrap admin)")

    rng = random.Random(args.seed)
    lat0, lon0 = (float(v) for v in args.center.split(","))

    # Favourite loops per sport: most activities re-ride one of these.
    favourites = {}
    for sport, (_, _, dist_km, radius_km, _) in SPORTS.items():
        favourites[sport] = []
        for _ in range(3):
            slat, slon = offset(lat0, lon0, rng.uniform(-radius_km, radius_km) * 1000, rng.uniform(-radius_km, radius_km) * 1000)
            d = rng.uniform(*dist_km) * 1000
            favourites[sport].append(loop(rng, slat, slon, d, wiggle=0.35))

    # Unique start slots so the fuzzy matcher never clusters two samples as one workout.
    slots = set()
    now = dt.datetime.now(dt.timezone.utc).replace(minute=0, second=0, microsecond=0)
    files = []
    names = {"Ride": ["Morning Ride", "Evening Loop", "Long Ride", "Coffee Ride"],
             "Run": ["Easy Run", "Tempo Run", "Lunch Run", "Long Run"],
             "Hike": ["Forest Hike", "Ridge Walk", "Sunday Hike"]}
    while len(files) < args.count:
        sport = rng.choices(list(SPORTS), weights=[w for w, *_ in SPORTS.values()])[0]
        _, speed, dist_km, radius_km, ele_amp = SPORTS[sport]
        slot = (rng.randrange(args.days), rng.choice([6, 7, 8, 9, 11, 12, 16, 17, 18]))
        if slot in slots:
            continue
        slots.add(slot)
        start = now - dt.timedelta(days=slot[0]) + dt.timedelta(hours=slot[1] - now.hour, minutes=rng.randrange(60))
        if rng.random() < 0.7:
            track = jitter(rng, rng.choice(favourites[sport]), sigma_m=6)
        else:
            slat, slon = offset(lat0, lon0, rng.uniform(-radius_km, radius_km) * 1000, rng.uniform(-radius_km, radius_km) * 1000)
            track = loop(rng, slat, slon, rng.uniform(*dist_km) * 1000, wiggle=0.35)
        name = rng.choice(names[sport])
        fname = f"sample-{start.strftime('%Y%m%d-%H%M')}-{sport.lower()}.gpx"
        files.append((fname, gpx(name, sport, start, track, speed, ele_amp, rng)))
    files.sort()

    if args.dry_run:
        out = os.path.join("tmp", "sample-gpx")
        os.makedirs(out, exist_ok=True)
        for fname, body in files:
            with open(os.path.join(out, fname), "w") as f:
                f.write(body)
        print(f"wrote {len(files)} files to {out}")
        return

    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    login(opener, args.base, args.email, args.password)
    ok = 0
    for i, (fname, body) in enumerate(files, 1):
        status, resp = upload(opener, args.base, fname, body)
        if status == 200:
            ok += 1
            print(f"[{i}/{len(files)}] {fname} → {resp.get('action')} {resp.get('activity_id')}")
        else:
            print(f"[{i}/{len(files)}] {fname} → HTTP {status} {resp}", file=sys.stderr)
    print(f"uploaded {ok}/{len(files)}")


if __name__ == "__main__":
    main()
