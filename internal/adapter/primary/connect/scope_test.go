package connect

import "testing"

func TestIsMutatingProcedure(t *testing.T) {
	reads := []string{
		"/cairn.v1.ActivityService/GetActivity",
		"/cairn.v1.ActivityService/ListActivities",
		"/cairn.v1.ActivityService/GetActivityStream",
		"/cairn.v1.SegmentService/SearchSegments",
		"/cairn.v1.MetricService/ExportMetrics",
	}
	for _, p := range reads {
		if isMutatingProcedure(p) {
			t.Errorf("expected read (non-mutating): %s", p)
		}
	}
	writes := []string{
		"/cairn.v1.ActivityService/UpdateActivity",
		"/cairn.v1.ActivityService/DeleteActivity",
		"/cairn.v1.ActivityService/CreateManualActivity",
		"/cairn.v1.EngagementService/PostComment",
	}
	for _, p := range writes {
		if !isMutatingProcedure(p) {
			t.Errorf("expected mutating (write): %s", p)
		}
	}
}

func TestHasAnyWriteScope(t *testing.T) {
	if !hasAnyWriteScope([]string{"activities:read", "social:write"}) {
		t.Fatal("should detect a write scope")
	}
	if hasAnyWriteScope([]string{"activities:read", "profile:read"}) {
		t.Fatal("read-only scopes must not count as write")
	}
	if hasAnyWriteScope(nil) {
		t.Fatal("nil scopes must not count as write")
	}
}
