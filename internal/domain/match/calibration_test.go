package match

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Calibration against REAL exported activity features (brief §7.5). Set
// CAIRN_MATCH_CORPUS to a JSON file produced by scripts/extract-match-corpus.sh
// (an array of {type,start_utc,distance_m,moving_s,elapsed_s,lat,lng}). The test
// skips when the var is unset so normal CI doesn't need a database.
//
// Two measurements on the operator's own data:
//
//   - PRECISION: every real activity is a DISTINCT workout. We pair each with
//     every other activity in a ±24h window, label them as two different
//     providers (so the ≤1-per-provider rule doesn't mask anything), and assert
//     the matcher does NOT cluster them. Any cluster is a false-positive
//     over-merge on real data — the brief's #1 risk (fusing two real workouts).
//
//   - RECALL: for each real activity we synthesize the second-provider copy an
//     auto-push would produce (start +2s, distance +0.3%, moving +1%) and assert
//     the matcher DOES cluster the pair.
type realFeature struct {
	Type      string   `json:"type"`
	StartUTC  int64    `json:"start_utc"`
	DistanceM float64  `json:"distance_m"`
	MovingS   int64    `json:"moving_s"`
	ElapsedS  int64    `json:"elapsed_s"`
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
}

func loadRealCorpus(t *testing.T) []realFeature {
	t.Helper()
	path := os.Getenv("CAIRN_MATCH_CORPUS")
	if path == "" {
		t.Skip("CAIRN_MATCH_CORPUS not set — skipping real-data calibration")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var fs []realFeature
	if err := json.Unmarshal(raw, &fs); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(fs) == 0 {
		t.Skip("empty corpus")
	}
	return fs
}

func (f realFeature) features(provider string) SourceFeatures {
	return SourceFeatures{
		Provider:   provider,
		SportClass: f.Type,
		StartUTC:   time.Unix(f.StartUTC, 0).UTC(),
		DistanceM:  f.DistanceM,
		MovingS:    f.MovingS,
		ElapsedS:   f.ElapsedS,
		StartLat:   f.Lat,
		StartLng:   f.Lng,
	}
}

func TestCalibration_PrecisionOnRealActivities(t *testing.T) {
	fs := loadRealCorpus(t)
	sort.Slice(fs, func(i, j int) bool { return fs[i].StartUTC < fs[j].StartUTC })
	opt := DefaultOptions()
	window := int64((24 * time.Hour).Seconds())

	pairs, falsePos := 0, 0
	var worst []struct {
		dtSec int64
		score float64
	}
	for i := 0; i < len(fs); i++ {
		for j := i + 1; j < len(fs); j++ {
			if fs[j].StartUTC-fs[i].StartUTC > window {
				break // sorted; no further j within the window
			}
			pairs++
			// Two DIFFERENT providers so the ≤1-per-provider rule doesn't mask
			// a genuine scoring false-positive.
			s, gated := Score(fs[i].features("garmin"), fs[j].features("strava"), opt.Weights)
			if !gated && s >= opt.EdgeThreshold {
				falsePos++
				worst = append(worst, struct {
					dtSec int64
					score float64
				}{fs[j].StartUTC - fs[i].StartUTC, s})
			}
		}
	}

	t.Logf("precision audit: %d activities, %d same-window pairs, %d would wrongly cluster",
		len(fs), pairs, falsePos)
	sort.Slice(worst, func(a, b int) bool { return worst[a].score > worst[b].score })
	for k := 0; k < len(worst) && k < 10; k++ {
		t.Logf("  false-positive: Δt=%ds score=%.3f", worst[k].dtSec, worst[k].score)
	}
	if pairs > 0 {
		t.Logf("precision = %.4f (1 - falsePos/pairs)", 1-float64(falsePos)/float64(pairs))
	}
	// The matcher must not wrongly fuse the operator's real distinct workouts.
	if falsePos > 0 {
		t.Errorf("%d real same-window activity pairs would be wrongly clustered — investigate weights/thresholds", falsePos)
	}
}

func TestCalibration_RecallOnSyntheticDuplicates(t *testing.T) {
	fs := loadRealCorpus(t)
	opt := DefaultOptions()

	clustered, total := 0, 0
	for _, f := range fs {
		total++
		a := f.features("garmin")
		// The second-provider copy an auto-push would produce.
		b := f.features("strava")
		b.StartUTC = b.StartUTC.Add(2 * time.Second)
		b.DistanceM = f.DistanceM * 1.003
		b.MovingS = int64(float64(f.MovingS) * 1.01)
		b.ElapsedS = int64(float64(f.ElapsedS) * 1.01)

		res := ClusterBucket([]Record{
			{ID: "a", SourceFeatures: a},
			{ID: "b", SourceFeatures: b},
		}, Constraints{}, opt)
		if len(res.Clusters) == 1 {
			clustered++
		}
	}
	recall := float64(clustered) / float64(total)
	t.Logf("recall on synthetic auto-push duplicates: %d/%d = %.4f", clustered, total, recall)
	// Realistic auto-push copies should almost always re-cluster.
	if recall < 0.98 {
		t.Errorf("recall %.4f below 0.98 — the matcher misses realistic duplicates", recall)
	}
}

// realSource is one source's match features (per-provider) from a real activity.
type realSource struct {
	Provider  string   `json:"provider"`
	Type      string   `json:"type"`
	StartUTC  int64    `json:"start_utc"`
	DistanceM float64  `json:"distance_m"`
	MovingS   int64    `json:"moving_s"`
	ElapsedS  int64    `json:"elapsed_s"`
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
}

func (s realSource) features() SourceFeatures {
	return SourceFeatures{
		Provider:   s.Provider,
		SportClass: s.Type,
		StartUTC:   time.Unix(s.StartUTC, 0).UTC(),
		DistanceM:  s.DistanceM,
		MovingS:    s.MovingS,
		ElapsedS:   s.ElapsedS,
		StartLat:   s.Lat,
		StartLng:   s.Lng,
	}
}

// TestCalibration_RealCrossProviderRecall measures recall on REAL cross-provider
// duplicates (not the synthetic +0.3% copy). CAIRN_MATCH_SAME_CORPUS is a JSON
// array of groups produced by `scripts/extract-match-corpus.sh --same-out`; each
// group is the set of distinct-provider sources the re-cluster engine already
// fused into one activity in production. Feeding each group back through the
// matcher must yield a single cluster — this exercises the matcher against the
// real provider feature divergence (start jitter, ±distance, pause/moving gaps).
func TestCalibration_RealCrossProviderRecall(t *testing.T) {
	path := os.Getenv("CAIRN_MATCH_SAME_CORPUS")
	if path == "" {
		t.Skip("CAIRN_MATCH_SAME_CORPUS not set — skipping real cross-provider recall")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read same-corpus: %v", err)
	}
	var groups [][]realSource
	if err := json.Unmarshal(raw, &groups); err != nil {
		t.Fatalf("parse same-corpus: %v", err)
	}
	if len(groups) == 0 {
		t.Skip("empty same-corpus — needs 2+ providers with overlapping workouts")
	}
	opt := DefaultOptions()

	clustered, total := 0, 0
	var misses []string
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		total++
		recs := make([]Record, len(g))
		provs := make([]string, len(g))
		for i, s := range g {
			recs[i] = Record{ID: fmt.Sprintf("%d", i), SourceFeatures: s.features()}
			provs[i] = s.Provider
		}
		if res := ClusterBucket(recs, Constraints{}, opt); len(res.Clusters) == 1 {
			clustered++
		} else {
			misses = append(misses, fmt.Sprintf("[%s]→%d clusters", strings.Join(provs, "+"), len(res.Clusters)))
		}
	}
	if total == 0 {
		t.Skip("no multi-source groups in same-corpus")
	}
	recall := float64(clustered) / float64(total)
	t.Logf("real cross-provider recall: %d/%d groups re-cluster = %.4f", clustered, total, recall)
	for k := 0; k < len(misses) && k < 10; k++ {
		t.Logf("  missed: %s", misses[k])
	}
	// Production fused these via the full bucket; re-clustering each in isolation
	// (fewer competing candidates) should reproduce a single cluster.
	if recall < 0.95 {
		t.Errorf("real cross-provider recall %.4f below 0.95 — the matcher fails to re-fuse confirmed duplicates", recall)
	}
}
