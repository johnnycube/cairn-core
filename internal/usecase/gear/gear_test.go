package gear

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/opencairn-org/cairn-core/internal/domain"
	"github.com/opencairn-org/cairn-core/internal/port"
)

// memGear is an in-memory port.GearRepo for the use-case tests.
type memGear struct {
	port.GearRepo
	gear     map[domain.GearID]domain.Gear
	refs     map[domain.GearRefKey]domain.GearExternalRef
	defaults map[domain.UserID]map[domain.ActivityType]domain.GearID
	totals   int

	activitiesByRef map[domain.GearRefKey][]domain.ActivityID
}

func newMemGear() *memGear {
	return &memGear{
		gear:     map[domain.GearID]domain.Gear{},
		refs:     map[domain.GearRefKey]domain.GearExternalRef{},
		defaults: map[domain.UserID]map[domain.ActivityType]domain.GearID{},
	}
}

func (m *memGear) Get(_ context.Context, id domain.GearID) (domain.Gear, error) {
	g, ok := m.gear[id]
	if !ok {
		return domain.Gear{}, domain.ErrNotFound
	}
	g.ExternalRefs = nil
	for _, r := range m.refs {
		if r.GearID != nil && *r.GearID == id {
			g.ExternalRefs = append(g.ExternalRefs, r)
		}
	}
	return g, nil
}

func (m *memGear) Save(_ context.Context, g domain.Gear) error {
	if err := g.Validate(); err != nil {
		return err
	}
	m.gear[g.ID] = g
	return nil
}

func (m *memGear) Delete(_ context.Context, id domain.GearID) error {
	if _, ok := m.gear[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.gear, id)
	for k, r := range m.refs {
		if r.GearID != nil && *r.GearID == id {
			delete(m.refs, k)
		}
	}
	return nil
}

func (m *memGear) UpsertExternalRef(_ context.Context, ref domain.GearExternalRef) error {
	m.refs[ref.Key()] = ref
	return nil
}

func (m *memGear) GetExternalRef(_ context.Context, key domain.GearRefKey) (domain.GearExternalRef, error) {
	r, ok := m.refs[key]
	if !ok {
		return domain.GearExternalRef{}, domain.ErrNotFound
	}
	return r, nil
}

func (m *memGear) ListPendingRefs(_ context.Context, userID domain.UserID) ([]domain.GearExternalRef, error) {
	var out []domain.GearExternalRef
	for _, r := range m.refs {
		if r.Pending() && r.UserID == userID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memGear) GetDefault(_ context.Context, userID domain.UserID, t domain.ActivityType) (domain.GearID, error) {
	id, ok := m.defaults[userID][t]
	if !ok {
		return domain.GearID{}, domain.ErrNotFound
	}
	return id, nil
}

func (m *memGear) SetDefault(_ context.Context, d domain.GearDefault) error {
	if m.defaults[d.UserID] == nil {
		m.defaults[d.UserID] = map[domain.ActivityType]domain.GearID{}
	}
	m.defaults[d.UserID][d.ActivityType] = d.GearID
	return nil
}

func (m *memGear) Repoint(_ context.Context, from, to domain.GearID) error {
	for k, r := range m.refs {
		if r.GearID != nil && *r.GearID == from {
			to := to
			r.GearID = &to
			m.refs[k] = r
		}
	}
	for _, d := range m.defaults {
		for t, id := range d {
			if id == from {
				d[t] = to
			}
		}
	}
	return nil
}

func (m *memGear) ListActivityIDsForRef(_ context.Context, key domain.GearRefKey) ([]domain.ActivityID, error) {
	return m.activitiesByRef[key], nil
}

func (m *memGear) RecomputeTotalsForUser(context.Context, domain.UserID) error {
	m.totals++
	return nil
}

type fakeTx struct{}

func (fakeTx) InTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }

func newTestService(repo *memGear) *Service {
	t0 := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	return NewService(repo, fakeTx{}, func() time.Time { return t0 }, uuid.New)
}

// importAndDecide records a provider gear and immediately decides "new gear".
func importAndDecide(t *testing.T, svc *Service, user domain.UserID, key domain.GearRefKey, in domain.GearImport) domain.GearID {
	t.Helper()
	if _, err := svc.ImportFromProvider(context.Background(), user, key, in); err != nil {
		t.Fatalf("import: %v", err)
	}
	g, err := svc.CreateFromRef(context.Background(), user, key)
	if err != nil {
		t.Fatalf("create from ref: %v", err)
	}
	return g.ID
}

func TestImportFromProvider_PendingUntilDecided(t *testing.T) {
	repo := newMemGear()
	svc := newTestService(repo)
	user := domain.UserID(uuid.New())
	key := domain.GearRefKey{Provider: "strava", ExternalAccountID: domain.ExternalAccountID(uuid.New()), ExternalID: "b123"}
	km := 1500.0
	var notified []domain.GearExternalRef
	svc.SetNotifier(func(_ context.Context, ref domain.GearExternalRef) error {
		notified = append(notified, ref)
		return nil
	})

	in := domain.GearImport{Kind: domain.GearKindBike, Name: "Canyon", Brand: "Canyon", Model: "Grail", Primary: true, DistanceM: &km}
	ref, err := svc.ImportFromProvider(context.Background(), user, key, in)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !ref.Pending() || len(repo.gear) != 0 {
		t.Fatalf("unknown provider gear must stay pending, got ref=%+v gear=%d", ref, len(repo.gear))
	}
	if len(notified) != 1 || notified[0].ProviderName != "Canyon" || notified[0].UserID != user {
		t.Fatalf("decision notification = %+v", notified)
	}
	// Re-sent with the next activity: refreshed, no second notification.
	if _, err := svc.ImportFromProvider(context.Background(), user, key, in); err != nil {
		t.Fatal(err)
	}
	if len(notified) != 1 {
		t.Fatalf("re-import notified again: %d", len(notified))
	}
	if pending, _ := svc.ListPending(context.Background(), user); len(pending) != 1 {
		t.Fatalf("pending = %d", len(pending))
	}
	if _, err := repo.GetDefault(context.Background(), user, domain.ActivityTypeRide); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("defaults must not be seeded before a decision: %v", err)
	}

	// Decision: create gear from the record.
	g, err := svc.CreateFromRef(context.Background(), user, key)
	if err != nil {
		t.Fatalf("create from ref: %v", err)
	}
	if g.Name != "Canyon" || g.Kind != domain.GearKindBike || g.Brand != "Canyon" || len(g.ExternalRefs) != 1 || !g.ExternalRefs[0].Primary {
		t.Fatalf("created gear = %+v", g)
	}
	if d, err := repo.GetDefault(context.Background(), user, domain.ActivityTypeRide); err != nil || d != g.ID {
		t.Fatalf("ride default = %v/%v", d, err)
	}
	if pending, _ := svc.ListPending(context.Background(), user); len(pending) != 0 {
		t.Fatalf("still pending after decision")
	}
	if _, err := svc.CreateFromRef(context.Background(), user, key); !errors.Is(err, domain.ErrGearRefMapped) {
		t.Fatalf("second decision: %v", err)
	}

	// User renames; the provider re-sends with its own name and a retirement.
	g.Name = "Gravel bike"
	_ = repo.Save(context.Background(), g)
	if _, err := svc.ImportFromProvider(context.Background(), user, key, domain.GearImport{Kind: domain.GearKindBike, Name: "Canyon", Retired: true}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	g, _ = repo.Get(context.Background(), g.ID)
	if g.Name != "Gravel bike" {
		t.Errorf("user name clobbered: %q", g.Name)
	}
	if !g.Retired || g.RetiredAt == nil {
		t.Errorf("provider retirement not followed: %+v", g)
	}
	if len(repo.gear) != 1 {
		t.Errorf("duplicate gear created: %d", len(repo.gear))
	}
}

func TestDecisions_DefaultNotOverwritten(t *testing.T) {
	repo := newMemGear()
	svc := newTestService(repo)
	user := domain.UserID(uuid.New())
	acct := domain.ExternalAccountID(uuid.New())

	first := importAndDecide(t, svc, user,
		domain.GearRefKey{Provider: "garmin", ExternalAccountID: acct, ExternalID: "1"},
		domain.GearImport{Kind: domain.GearKindShoe, Name: "Pegasus", DefaultFor: []domain.ActivityType{domain.ActivityTypeRun, domain.ActivityTypeWalk}})
	importAndDecide(t, svc, user,
		domain.GearRefKey{Provider: "garmin", ExternalAccountID: acct, ExternalID: "2"},
		domain.GearImport{Kind: domain.GearKindShoe, Name: "Vaporfly", Primary: true})

	if d, _ := repo.GetDefault(context.Background(), user, domain.ActivityTypeRun); d != first {
		t.Fatalf("existing run default overwritten by a later primary: %v != %v", d, first)
	}
	if d, _ := repo.GetDefault(context.Background(), user, domain.ActivityTypeWalk); d != first {
		t.Fatalf("walk default missing")
	}
}

func TestImportFromProvider_OtherUsersRefRejected(t *testing.T) {
	repo := newMemGear()
	svc := newTestService(repo)
	key := domain.GearRefKey{Provider: "strava", ExternalAccountID: domain.ExternalAccountID(uuid.New()), ExternalID: "b1"}
	owner := domain.UserID(uuid.New())
	if _, err := svc.ImportFromProvider(context.Background(), owner, key, domain.GearImport{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	other := domain.UserID(uuid.New())
	if _, err := svc.ImportFromProvider(context.Background(), other, key, domain.GearImport{Name: "x"}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner, got %v", err)
	}
	if _, err := svc.CreateFromRef(context.Background(), other, key); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("foreign decision: %v", err)
	}
}

func TestMerge(t *testing.T) {
	repo := newMemGear()
	svc := newTestService(repo)
	user := domain.UserID(uuid.New())
	acctS := domain.ExternalAccountID(uuid.New())
	acctG := domain.ExternalAccountID(uuid.New())
	strava := importAndDecide(t, svc, user,
		domain.GearRefKey{Provider: "strava", ExternalAccountID: acctS, ExternalID: "b1"},
		domain.GearImport{Kind: domain.GearKindBike, Name: "Canyon", Primary: true})
	garmin := importAndDecide(t, svc, user,
		domain.GearRefKey{Provider: "garmin", ExternalAccountID: acctG, ExternalID: "77"},
		domain.GearImport{Kind: domain.GearKindBike, Name: "Canyon Grail"})

	if _, err := svc.Merge(context.Background(), user, strava, strava); !errors.Is(err, domain.ErrGearSelfMerge) {
		t.Fatalf("self merge: %v", err)
	}
	if _, err := svc.Merge(context.Background(), domain.UserID(uuid.New()), garmin, strava); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("foreign merge: %v", err)
	}

	merged, err := svc.Merge(context.Background(), user, garmin, strava)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ID != strava || len(merged.ExternalRefs) != 2 {
		t.Fatalf("merged = %+v", merged)
	}
	if _, err := repo.Get(context.Background(), garmin); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("from still exists: %v", err)
	}
	if d, _ := repo.GetDefault(context.Background(), user, domain.ActivityTypeRide); d != strava {
		t.Fatalf("default not repointed: %v", d)
	}
	if repo.totals == 0 {
		t.Fatal("totals not recomputed after merge")
	}
}

func TestLinkRef_MovesRefAndRederives(t *testing.T) {
	repo := newMemGear()
	svc := newTestService(repo)
	user := domain.UserID(uuid.New())
	acct := domain.ExternalAccountID(uuid.New())
	key := domain.GearRefKey{Provider: "garmin", ExternalAccountID: acct, ExternalID: "77"}

	golden, _ := svc.Create(context.Background(), user, CreateInput{Kind: domain.GearKindBike, Name: "Bike A"})
	// Pending record from Garmin: the user decides it is an alias of Bike A.
	if _, err := svc.ImportFromProvider(context.Background(), user, key, domain.GearImport{Kind: domain.GearKindBike, Name: "Bike B", Primary: true}); err != nil {
		t.Fatal(err)
	}
	act := domain.ActivityID(uuid.New())
	repo.activitiesByRef = map[domain.GearRefKey][]domain.ActivityID{key: {act}}
	var recomputed []domain.ActivityID
	svc.SetRecompute(func(_ context.Context, id domain.ActivityID) error {
		recomputed = append(recomputed, id)
		return nil
	})

	g, err := svc.LinkRef(context.Background(), user, golden.ID, key)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if len(g.ExternalRefs) != 1 || g.ExternalRefs[0].ExternalID != "77" || g.ExternalRefs[0].ProviderName != "Bike B" {
		t.Fatalf("ref not moved with its fields: %+v", g.ExternalRefs)
	}
	if got, _ := repo.GetExternalRef(context.Background(), key); got.GearID == nil || *got.GearID != golden.ID {
		t.Fatalf("lookup still resolves to %v", got.GearID)
	}
	if len(repo.gear) != 1 {
		t.Fatalf("mapping created gear: %d", len(repo.gear))
	}
	if d, _ := repo.GetDefault(context.Background(), user, domain.ActivityTypeRide); d != golden.ID {
		t.Fatalf("primary flag did not seed the ride default on mapping: %v", d)
	}
	if pending, _ := svc.ListPending(context.Background(), user); len(pending) != 0 {
		t.Fatalf("mapped ref still pending")
	}
	if len(recomputed) != 1 || recomputed[0] != act {
		t.Fatalf("recomputed = %v", recomputed)
	}
	if repo.totals == 0 {
		t.Fatal("totals not refreshed")
	}

	// Unknown ref and foreign gear are not-found class errors.
	if _, err := svc.LinkRef(context.Background(), user, golden.ID, domain.GearRefKey{Provider: "strava", ExternalAccountID: acct, ExternalID: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown ref: %v", err)
	}
	if _, err := svc.LinkRef(context.Background(), domain.UserID(uuid.New()), golden.ID, key); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("foreign user: %v", err)
	}
}

func TestUpdate_RetireStampsTime(t *testing.T) {
	repo := newMemGear()
	svc := newTestService(repo)
	user := domain.UserID(uuid.New())
	g, err := svc.Create(context.Background(), user, CreateInput{Kind: domain.GearKindShoe, Name: " Pegasus "})
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "Pegasus" {
		t.Fatalf("name not trimmed: %q", g.Name)
	}
	g, err = svc.Update(context.Background(), user, g.ID, func(g *domain.Gear) { g.Retired = true })
	if err != nil || !g.Retired || g.RetiredAt == nil {
		t.Fatalf("retire: %+v %v", g, err)
	}
	g, _ = svc.Update(context.Background(), user, g.ID, func(g *domain.Gear) { g.Retired = false })
	if g.RetiredAt != nil {
		t.Fatalf("un-retire kept RetiredAt")
	}
	if _, err := svc.Update(context.Background(), user, g.ID, func(g *domain.Gear) { g.Name = "" }); !errors.Is(err, domain.ErrGearMissingName) {
		t.Fatalf("blank name accepted: %v", err)
	}
	if _, err := svc.Create(context.Background(), user, CreateInput{Kind: "wheel", Name: "x"}); !errors.Is(err, domain.ErrGearInvalidKind) {
		t.Fatalf("bad kind accepted: %v", err)
	}
}
