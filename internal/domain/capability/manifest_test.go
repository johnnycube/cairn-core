package capability

import "testing"

func TestManifest_Queries(t *testing.T) {
	m := Manifest{
		DataTypeActivity:      {Read: true, Backfill: true, Granularity: "per-activity"},
		DataTypeSegmentEffort: {Read: true, Backfill: true},
		DataTypeWeight:        {Read: true, Write: true, Granularity: "daily"},
	}

	if !m.CanRead(DataTypeActivity) || !m.SupportsBackfill(DataTypeActivity) {
		t.Error("Activity capability axes wrong")
	}
	if !m.CanWrite(DataTypeWeight) {
		t.Error("Weight should be writable")
	}
	if m.CanRead(DataTypeHRV) {
		t.Error("HRV absent should report not-readable")
	}
	if !m.AnyBackfill() {
		t.Error("AnyBackfill should be true")
	}

	readable := m.ReadableTypes()
	// Display order: Activity (activity cat) before SegmentEffort before Weight (timeseries).
	want := []DataType{DataTypeActivity, DataTypeSegmentEffort, DataTypeWeight}
	if len(readable) != len(want) {
		t.Fatalf("ReadableTypes = %v, want %v", readable, want)
	}
	for i := range want {
		if readable[i] != want[i] {
			t.Errorf("ReadableTypes[%d] = %s, want %s", i, readable[i], want[i])
		}
	}
}

func TestManifest_ValidateRejectsUnknown(t *testing.T) {
	m := Manifest{DataType("Bogus"): {Read: true}}
	if err := m.Validate(); err == nil {
		t.Fatal("expected validation error for unknown type")
	}
	good := Manifest{DataTypeActivity: {Read: true}}
	if err := good.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFromLegacy(t *testing.T) {
	m := FromLegacy(true, true, true, false, true)
	if !m.CanRead(DataTypeActivity) {
		t.Error("legacy activity mapping wrong")
	}
	if !m.CanRead(DataTypeLap) {
		t.Error("legacy should imply Lap readable when Activity produced")
	}
	if m.CanRead(DataTypePersonalBest) {
		t.Error("PersonalBest not produced in legacy input")
	}
	if err := m.Validate(); err != nil {
		t.Errorf("legacy manifest should validate: %v", err)
	}
}

func TestDataType_RegistryHelpers(t *testing.T) {
	if !DataTypeHRV.Valid() || DataType("nope").Valid() {
		t.Error("Valid wrong")
	}
	if DataTypeHRV.CategoryOf() != CategoryTimeSeries {
		t.Errorf("HRV category = %s", DataTypeHRV.CategoryOf())
	}
	if DataTypeActivity.Label() != "Activity" {
		t.Errorf("Activity label = %s", DataTypeActivity.Label())
	}
	if len(AllTypes()) < 10 {
		t.Errorf("AllTypes too small: %d", len(AllTypes()))
	}
}
