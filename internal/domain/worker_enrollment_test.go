package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestStandardPermissionTemplateJetStream(t *testing.T) {
	tpl := StandardPermissionTemplate("strava", "w1-strava")
	for _, want := range []string{
		"$JS.API.STREAM.NAMES",
		"$JS.API.CONSUMER.CREATE.CAIRN_JOBS.*.cairn.jobs.fetch_source.strava",
		"$JS.API.CONSUMER.CREATE.CAIRN_JOBS.*.cairn.jobs.import_metrics.strava",
		"$JS.API.CONSUMER.DURABLE.CREATE.CAIRN_JOBS.*",
		"cairn.results.reconcile.strava.*",
		"cairn.results.import_metrics.strava",
		"cairn.jobs.fetch_source.strava",
		"cairn.creds.strava.get",
		"$JS.API.CONSUMER.CREATE.CAIRN_WEBHOOKS.*.cairn.webhooks.w1-strava.event",
		"$JS.API.CONSUMER.MSG.NEXT.CAIRN_JOBS.*",
		"$JS.ACK.CAIRN_JOBS.>",
		"$JS.API.STREAM.INFO.KV_cairn_rate_limits",
		"$KV.cairn_worker_presence.>",
	} {
		if !slices.Contains(tpl.AllowPublish, want) {
			t.Errorf("publish allow-list lacks %q", want)
		}
	}
	for _, s := range tpl.AllowPublish {
		if s == "$JS.API.>" || strings.Contains(s, "garmin") || strings.Contains(s, "STREAM.CREATE") ||
			strings.Contains(s, "DELETE") || strings.Contains(s, "CAIRN_RESULTS") {
			t.Errorf("over-broad publish grant %q", s)
		}
	}
	if !slices.Contains(tpl.AllowSubscribe, "cairn.jobs.fetch_source.strava") ||
		!slices.Contains(tpl.AllowSubscribe, "cairn.discover.strava") ||
		slices.ContainsFunc(tpl.AllowSubscribe, func(s string) bool { return strings.HasPrefix(s, "$JS") }) {
		t.Errorf("subscribe allow-list = %v", tpl.AllowSubscribe)
	}
	if empty := StandardPermissionTemplate("", "x"); empty.AllowPublish != nil || empty.AllowSubscribe != nil {
		t.Errorf("no provider must yield empty allow lists (refused by the issuer), got %+v", empty)
	}
}

func TestEnrollmentProviderRequirement(t *testing.T) {
	if !(WorkerEnrollment{}).NeedsReenroll() {
		t.Error("enrollment without provider must need re-enrollment")
	}
	if (WorkerEnrollment{Provider: "strava"}).NeedsReenroll() {
		t.Error("enrollment with provider must not need re-enrollment")
	}
	for s, want := range map[string]bool{
		"strava": true, "garmin": true, "fit_upload": true, "a-b": true, "polar": true, "_": true, "-": true, "9": true, strings.Repeat("a", 48): true,
		"": false, "Strava": false, "a.b": false, "a>": false, "a*": false, "polar.>": false, ">": false, "*": false, "a b": false, "a/b": false, "ä": false, strings.Repeat("a", 49): false,
	} {
		if got := ValidProviderID(s); got != want {
			t.Errorf("ValidProviderID(%q) = %v, want %v", s, got, want)
		}
	}
}
