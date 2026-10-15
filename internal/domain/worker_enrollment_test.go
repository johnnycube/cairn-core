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
		"$JS.API.CONSUMER.CREATE.CAIRN_JOBS.*.cairn.jobs.fetch_source.strava.>",
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
	if !slices.Contains(tpl.AllowSubscribe, "cairn.jobs.fetch_source.strava.>") ||
		slices.ContainsFunc(tpl.AllowSubscribe, func(s string) bool { return strings.HasPrefix(s, "$JS") }) {
		t.Errorf("subscribe allow-list = %v", tpl.AllowSubscribe)
	}
	if empty := StandardPermissionTemplate("", "x"); empty.AllowPublish != nil || empty.AllowSubscribe != nil {
		t.Errorf("no provider must grant nothing, got %+v", empty)
	}
}
