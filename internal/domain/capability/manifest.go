package capability

import "sort"

// Capability declares what a worker can do with one DataType. It is the
// schema-stable surface — "which data can be transmitted" — and deliberately
// carries NO delivery-mode/ownership flag: whether an instance delivers via
// webhook push is a per-deployment property advertised separately in the
// presence heartbeat (the `webhooks` flag), not part of the manifest.
type Capability struct {
	Read        bool   `json:"read"`               // fetch from the provider
	Write       bool   `json:"write"`              // push back to the provider
	Backfill    bool   `json:"backfill"`           // can import history
	Granularity string `json:"granularity,omitempty"` // e.g. "1s", "daily"
}

// Manifest is a worker's per-data-type capability declaration.
type Manifest map[DataType]Capability

// CanRead reports whether the worker can read dt.
func (m Manifest) CanRead(dt DataType) bool { return m[dt].Read }

// CanWrite reports whether the worker can write dt.
func (m Manifest) CanWrite(dt DataType) bool { return m[dt].Write }

// SupportsBackfill reports whether the worker can backfill dt.
func (m Manifest) SupportsBackfill(dt DataType) bool { return m[dt].Backfill }

// ReadableTypes returns, in canonical display order, the data types the worker
// can read — the basis for the "this provider gives you …" display.
func (m Manifest) ReadableTypes() []DataType {
	var out []DataType
	for _, s := range registry {
		if m[s.Type].Read {
			out = append(out, s.Type)
		}
	}
	return out
}

// AnyBackfill reports whether the worker can backfill any data type.
func (m Manifest) AnyBackfill() bool {
	for _, c := range m {
		if c.Backfill {
			return true
		}
	}
	return false
}

// Validate rejects unknown data types (a typo should fail loudly).
func (m Manifest) Validate() error {
	var bad []string
	for dt := range m {
		if !dt.Valid() {
			bad = append(bad, string(dt))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return &UnknownTypesError{Types: bad}
}

// UnknownTypesError reports manifest data types outside the taxonomy.
type UnknownTypesError struct{ Types []string }

func (e *UnknownTypesError) Error() string {
	return "capability manifest references unknown data types: " + joinComma(e.Types)
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

// FromLegacy builds a Manifest from the old coarse flags for workers that don't
// declare a full one.
func FromLegacy(producesActivity, producesSegment, producesSegmentEffort, producesPersonalBest bool, supportsBackfill bool) Manifest {
	m := Manifest{}
	if producesActivity {
		m[DataTypeActivity] = Capability{Read: true, Backfill: supportsBackfill}
		m[DataTypeLap] = Capability{Read: true, Backfill: supportsBackfill}
	}
	if producesSegment {
		m[DataTypeSegment] = Capability{Read: true, Backfill: supportsBackfill}
	}
	if producesSegmentEffort {
		m[DataTypeSegmentEffort] = Capability{Read: true, Backfill: supportsBackfill}
	}
	if producesPersonalBest {
		m[DataTypePersonalBest] = Capability{Read: true, Backfill: supportsBackfill}
	}
	return m
}
