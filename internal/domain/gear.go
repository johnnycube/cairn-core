package domain

import (
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Gear
//
// The bikes and shoes a user rides and runs in. Owner-only; attached to an
// activity either by the merge (from the winning source's provider gear id,
// or the user's per-type default for manual/uploaded activities) or by the
// user, tracked in Activity.GearSetBy. Totals are derived from live
// activities and maintained by the totals follow-up, never by callers.
// ---------------------------------------------------------------------------

// GearKind classifies gear coarsely so pickers can filter by activity type.
type GearKind string

const (
	GearKindBike  GearKind = "bike"
	GearKindShoe  GearKind = "shoe"
	GearKindOther GearKind = "other"
)

// Valid reports whether k is a known kind.
func (k GearKind) Valid() bool {
	switch k {
	case GearKindBike, GearKindShoe, GearKindOther:
		return true
	}
	return false
}

// GearKindForActivityType maps an activity type to the kind of gear its
// picker suggests. The mapping is advisory: assignment never enforces it.
func GearKindForActivityType(t ActivityType) GearKind {
	switch t {
	case ActivityTypeRide:
		return GearKindBike
	case ActivityTypeRun, ActivityTypeWalk, ActivityTypeHike:
		return GearKindShoe
	}
	return GearKindOther
}

// GearSetBy records who decided an activity's gear.
type GearSetBy string

const (
	// GearSetByMerge: derived by recompute (winning source or type default);
	// a later recompute may change it.
	GearSetByMerge GearSetBy = "merge"
	// GearSetByUser: chosen through UpdateActivity (including "no gear");
	// recompute never touches it.
	GearSetByUser GearSetBy = "user"
)

// Valid reports whether s is a known value.
func (s GearSetBy) Valid() bool {
	return s == GearSetByMerge || s == GearSetByUser
}

type Gear struct {
	ID           GearID
	UserID       UserID
	Kind         GearKind
	Name         string
	Brand        string
	Model        string
	Notes        string
	PurchaseDate *time.Time
	Retired      bool
	RetiredAt    *time.Time

	// Derived; maintained by the totals follow-up.
	TotalDistanceM float64
	TotalMoving    time.Duration
	ActivityCount  int64

	ExternalRefs []GearExternalRef

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate enforces the gear invariants before persisting.
func (g *Gear) Validate() error {
	if g.ID == (GearID{}) {
		return ErrGearMissingID
	}
	if g.UserID == (UserID{}) {
		return ErrGearMissingUserID
	}
	if !g.Kind.Valid() {
		return ErrGearInvalidKind
	}
	if strings.TrimSpace(g.Name) == "" {
		return ErrGearMissingName
	}
	return nil
}

// GearRefKey identifies a provider's gear record on one account. An external
// ref belongs to exactly one gear per key.
type GearRefKey struct {
	Provider          string
	ExternalAccountID ExternalAccountID
	ExternalID        string
}

// GearExternalRef is a provider's gear record. Mapped (GearID set) it is an
// alias of that Gear; pending (GearID nil) it waits for the user's decision:
// create gear from it, map it onto existing gear, or decide later. The
// provider's description is kept for that decision and as audit.
type GearExternalRef struct {
	GearID            *GearID
	UserID            UserID // owner of the external account
	Provider          string
	ExternalAccountID ExternalAccountID
	ExternalID        string
	ProviderName      string   // display name as the provider has it
	ProviderDistanceM *float64 // provider-reported odometer, audit only
	Primary           bool     // provider's default for this kind
	Kind              GearKind
	Brand             string
	Model             string
	Description       string
	Retired           bool
	DefaultFor        []ActivityType
	LastSeenAt        time.Time
}

// Key returns the ref's identity.
func (r GearExternalRef) Key() GearRefKey {
	return GearRefKey{Provider: r.Provider, ExternalAccountID: r.ExternalAccountID, ExternalID: r.ExternalID}
}

// Pending reports whether the ref still awaits the user's decision.
func (r GearExternalRef) Pending() bool { return r.GearID == nil }

// Import returns the provider's description as a GearImport.
func (r GearExternalRef) Import() GearImport {
	return GearImport{
		Kind: r.Kind, Name: r.ProviderName, Brand: r.Brand, Model: r.Model, Description: r.Description,
		Retired: r.Retired, Primary: r.Primary, DistanceM: r.ProviderDistanceM, DefaultFor: r.DefaultFor,
	}
}

// Describe copies the provider's description onto the ref, keeping identity
// and mapping.
func (r *GearExternalRef) Describe(in GearImport, seenAt time.Time) {
	r.ProviderName = in.Name
	r.ProviderDistanceM = in.DistanceM
	r.Primary = in.Primary
	r.Kind = in.Kind
	r.Brand = in.Brand
	r.Model = in.Model
	r.Description = in.Description
	r.Retired = in.Retired
	r.DefaultFor = in.DefaultFor
	r.LastSeenAt = seenAt
}

// GearDefault is the user's default gear for an activity type.
type GearDefault struct {
	UserID       UserID
	ActivityType ActivityType
	GearID       GearID
}

// GearImport is a provider's description of one gear record, as a worker
// reports it (cairn.worker.v1.GearImport).
type GearImport struct {
	Kind        GearKind
	Name        string
	Brand       string
	Model       string
	Description string
	Retired     bool
	Primary     bool
	DistanceM   *float64
	DefaultFor  []ActivityType
}

// GearRefKeyOf returns the lookup key for s's provider gear id, if the source
// carries one on an external account.
func GearRefKeyOf(s ActivitySource) (GearRefKey, bool) {
	if s.ExternalAccountID == nil || s.Parsed.GearExternalID == "" {
		return GearRefKey{}, false
	}
	return GearRefKey{Provider: s.Provider, ExternalAccountID: *s.ExternalAccountID, ExternalID: s.Parsed.GearExternalID}, true
}

// ResolveGear picks an activity's gear from its sources. resolved maps each
// source to the GearID its provider gear id resolved to (sources whose gear
// is unknown to Cairn are absent). A pinned source wins if it resolved;
// otherwise the policy's priority list for the gear group is walked with the
// merge cascade's semantics, and the first resolved source wins. Sources need
// not be sorted; ties within a provider go to the most recent import.
func ResolveGear(
	sources []ActivitySource,
	resolved map[SourceID]GearID,
	policy MergePolicy,
	pin *SourceID,
) (GearID, SourceID, bool) {
	if len(resolved) == 0 {
		return GearID{}, SourceID{}, false
	}
	ordered := make([]ActivitySource, len(sources))
	copy(ordered, sources)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].ImportedAt.After(ordered[j].ImportedAt)
	})
	has := func(s *ActivitySource) bool {
		_, ok := resolved[s.ID]
		return ok
	}
	if pin != nil {
		for i := range ordered {
			if ordered[i].ID == *pin && has(&ordered[i]) {
				return resolved[ordered[i].ID], ordered[i].ID, true
			}
		}
	}
	w, ok := pickWinnerBy(ordered, policy.PriorityFor(FieldGroupGear), has)
	if !ok {
		return GearID{}, SourceID{}, false
	}
	return resolved[w.ID], w.ID, true
}
