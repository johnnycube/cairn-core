// Package gear holds the gear use cases: user CRUD, merging two records,
// per-type defaults, provider imports and the derived totals. Attachment to
// activities is decided in usecase/activity (recompute); this package only
// maintains the gear side and the totals.
package gear

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/opencairn-org/cairn-core/internal/domain"
	"github.com/opencairn-org/cairn-core/internal/port"
)

// Service bundles the gear use cases; they share the same three dependencies.
type Service struct {
	gear  port.GearRepo
	tx    port.TxManager
	now   func() time.Time
	newID func() uuid.UUID
	// recompute re-derives one activity after a ref mapping changed. Nil
	// until SetRecompute; decisions then only move the ref.
	recompute func(ctx context.Context, id domain.ActivityID) error
	// notify tells the user a provider record awaits a decision. Nil until
	// SetNotifier; pending refs are then only listed.
	notify func(ctx context.Context, ref domain.GearExternalRef) error
}

// SetRecompute installs the activity re-derivation the mapping decisions use.
func (s *Service) SetRecompute(fn func(ctx context.Context, id domain.ActivityID) error) {
	s.recompute = fn
}

// SetNotifier installs the "decision needed" notification for new pending refs.
func (s *Service) SetNotifier(fn func(ctx context.Context, ref domain.GearExternalRef) error) {
	s.notify = fn
}

// NewService wires the use cases. now and newID default to time.Now and
// uuid.NewV7 when nil.
func NewService(gear port.GearRepo, tx port.TxManager, now func() time.Time, newID func() uuid.UUID) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if newID == nil {
		newID = func() uuid.UUID {
			id, err := uuid.NewV7()
			if err != nil {
				return uuid.New()
			}
			return id
		}
	}
	return &Service{gear: gear, tx: tx, now: now, newID: newID}
}

// ErrNotOwner is returned when the gear belongs to another user. Callers map
// it to not-found so gear ids of other users are not probeable.
var ErrNotOwner = errors.New("gear: not owned by caller")

// Get loads one gear owned by userID.
func (s *Service) Get(ctx context.Context, userID domain.UserID, id domain.GearID) (domain.Gear, error) {
	g, err := s.gear.Get(ctx, id)
	if err != nil {
		return domain.Gear{}, err
	}
	if g.UserID != userID {
		return domain.Gear{}, ErrNotOwner
	}
	return g, nil
}

// List returns the user's gear, retired included on request.
func (s *Service) List(ctx context.Context, userID domain.UserID, includeRetired bool) ([]domain.Gear, error) {
	return s.gear.ListForUser(ctx, userID, includeRetired)
}

// CreateInput is what a user supplies for new gear.
type CreateInput struct {
	Kind         domain.GearKind
	Name         string
	Brand        string
	Model        string
	Notes        string
	PurchaseDate *time.Time
}

// Create persists new user-managed gear.
func (s *Service) Create(ctx context.Context, userID domain.UserID, in CreateInput) (domain.Gear, error) {
	now := s.now()
	g := domain.Gear{
		ID:           domain.GearID(s.newID()),
		UserID:       userID,
		Kind:         in.Kind,
		Name:         strings.TrimSpace(in.Name),
		Brand:        strings.TrimSpace(in.Brand),
		Model:        strings.TrimSpace(in.Model),
		Notes:        in.Notes,
		PurchaseDate: in.PurchaseDate,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := g.Validate(); err != nil {
		return domain.Gear{}, err
	}
	if err := s.gear.Save(ctx, g); err != nil {
		return domain.Gear{}, err
	}
	return s.gear.Get(ctx, g.ID)
}

// Update loads the gear, applies the caller's edit and saves it. Retiring
// stamps RetiredAt; un-retiring clears it.
func (s *Service) Update(ctx context.Context, userID domain.UserID, id domain.GearID, apply func(*domain.Gear)) (domain.Gear, error) {
	g, err := s.Get(ctx, userID, id)
	if err != nil {
		return domain.Gear{}, err
	}
	wasRetired := g.Retired
	apply(&g)
	g.Name = strings.TrimSpace(g.Name)
	switch {
	case g.Retired && !wasRetired:
		now := s.now()
		g.RetiredAt = &now
	case !g.Retired && wasRetired:
		g.RetiredAt = nil
	}
	if err := g.Validate(); err != nil {
		return domain.Gear{}, err
	}
	if err := s.gear.Save(ctx, g); err != nil {
		return domain.Gear{}, err
	}
	return s.gear.Get(ctx, id)
}

// Delete removes the gear. Its activities keep no gear afterwards (the FK
// nulls them), refs and defaults cascade; totals of the rest are unaffected.
func (s *Service) Delete(ctx context.Context, userID domain.UserID, id domain.GearID) error {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return err
	}
	return s.gear.Delete(ctx, id)
}

// Merge folds `from` into `to` (same user): refs, defaults and activity
// attachments move, `from` is deleted, totals of the user are rebuilt.
func (s *Service) Merge(ctx context.Context, userID domain.UserID, from, to domain.GearID) (domain.Gear, error) {
	if from == to {
		return domain.Gear{}, domain.ErrGearSelfMerge
	}
	if _, err := s.Get(ctx, userID, from); err != nil {
		return domain.Gear{}, err
	}
	if _, err := s.Get(ctx, userID, to); err != nil {
		return domain.Gear{}, err
	}
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.gear.Repoint(ctx, from, to); err != nil {
			return err
		}
		if err := s.gear.Delete(ctx, from); err != nil {
			return err
		}
		return s.gear.RecomputeTotalsForUser(ctx, userID)
	})
	if err != nil {
		return domain.Gear{}, fmt.Errorf("merge gear: %w", err)
	}
	return s.gear.Get(ctx, to)
}

// LinkRef decides "this is an alias": maps a provider gear record, pending
// or already mapped elsewhere, onto gear the user chose. Activities whose
// gear the merge derived from that record are re-derived, so they move with
// the mapping; user-set activities stay as they are.
func (s *Service) LinkRef(ctx context.Context, userID domain.UserID, id domain.GearID, key domain.GearRefKey) (domain.Gear, error) {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return domain.Gear{}, err
	}
	ref, err := s.ownedRef(ctx, userID, key)
	if err != nil {
		return domain.Gear{}, err
	}
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		return s.mapRef(ctx, userID, ref, id)
	})
	if err != nil {
		return domain.Gear{}, fmt.Errorf("link gear ref %s/%s: %w", key.Provider, key.ExternalID, err)
	}
	return s.gear.Get(ctx, id)
}

// rederiveForRef recomputes the merge-assigned activities that took their
// gear from the provider record, so they follow the new mapping.
func (s *Service) rederiveForRef(ctx context.Context, key domain.GearRefKey) error {
	if s.recompute == nil {
		return nil
	}
	ids, err := s.gear.ListActivityIDsForRef(ctx, key)
	if err != nil {
		return err
	}
	for _, aid := range ids {
		if err := s.recompute(ctx, aid); err != nil {
			return fmt.Errorf("re-derive activity %s: %w", aid, err)
		}
	}
	return nil
}

// SetDefault makes the gear the user's default for an activity type.
func (s *Service) SetDefault(ctx context.Context, userID domain.UserID, t domain.ActivityType, id domain.GearID) error {
	if !t.Valid() {
		return domain.ErrActivityInvalidType
	}
	if _, err := s.Get(ctx, userID, id); err != nil {
		return err
	}
	return s.gear.SetDefault(ctx, domain.GearDefault{UserID: userID, ActivityType: t, GearID: id})
}

// ClearDefault removes the user's default for an activity type.
func (s *Service) ClearDefault(ctx context.Context, userID domain.UserID, t domain.ActivityType) error {
	return s.gear.ClearDefault(ctx, userID, t)
}

// ListDefaults returns the user's per-type defaults.
func (s *Service) ListDefaults(ctx context.Context, userID domain.UserID) ([]domain.GearDefault, error) {
	return s.gear.ListDefaults(ctx, userID)
}

// ImportFromProvider records a provider's gear record. The domain gear is
// the golden record, so a record Cairn has not seen becomes a PENDING ref
// and the user is told to decide (create gear from it, map it onto existing
// gear, or leave it). A mapped ref only refreshes its description and
// follows a provider-side retirement; user edits to the gear itself are
// never clobbered. Returns the ref as stored.
func (s *Service) ImportFromProvider(ctx context.Context, userID domain.UserID, key domain.GearRefKey, in domain.GearImport) (domain.GearExternalRef, error) {
	var (
		out        domain.GearExternalRef
		newPending bool
	)
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		ref, err := s.gear.GetExternalRef(ctx, key)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			ref = domain.GearExternalRef{UserID: userID, Provider: key.Provider, ExternalAccountID: key.ExternalAccountID, ExternalID: key.ExternalID}
			newPending = true
		case err != nil:
			return err
		case ref.UserID != userID:
			return ErrNotOwner
		}
		ref.Describe(in, s.now())
		if ref.GearID != nil && in.Retired {
			if err := s.retireFromProvider(ctx, *ref.GearID); err != nil {
				return err
			}
		}
		if err := s.gear.UpsertExternalRef(ctx, ref); err != nil {
			return err
		}
		out = ref
		return nil
	})
	if err != nil {
		return domain.GearExternalRef{}, fmt.Errorf("import gear %s/%s: %w", key.Provider, key.ExternalID, err)
	}
	if newPending && s.notify != nil {
		if err := s.notify(ctx, out); err != nil {
			return out, fmt.Errorf("notify gear decision: %w", err)
		}
	}
	return out, nil
}

// retireFromProvider follows a provider-side retirement onto the mapped gear.
func (s *Service) retireFromProvider(ctx context.Context, id domain.GearID) error {
	g, err := s.gear.Get(ctx, id)
	if err != nil || g.Retired {
		return err
	}
	now := s.now()
	g.Retired, g.RetiredAt = true, &now
	return s.gear.Save(ctx, g)
}

// ListPending returns the user's provider records awaiting a decision.
func (s *Service) ListPending(ctx context.Context, userID domain.UserID) ([]domain.GearExternalRef, error) {
	return s.gear.ListPendingRefs(ctx, userID)
}

// CreateFromRef decides "this is new gear": creates gear from the provider's
// description, maps the ref onto it, seeds defaults, re-derives the
// activities that reference the record and refreshes totals.
func (s *Service) CreateFromRef(ctx context.Context, userID domain.UserID, key domain.GearRefKey) (domain.Gear, error) {
	ref, err := s.ownedRef(ctx, userID, key)
	if err != nil {
		return domain.Gear{}, err
	}
	if !ref.Pending() {
		return domain.Gear{}, domain.ErrGearRefMapped
	}
	var id domain.GearID
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		var err error
		if id, err = s.createFromImport(ctx, userID, key, ref.Import()); err != nil {
			return err
		}
		return s.mapRef(ctx, userID, ref, id)
	})
	if err != nil {
		return domain.Gear{}, fmt.Errorf("create gear from %s/%s: %w", key.Provider, key.ExternalID, err)
	}
	return s.gear.Get(ctx, id)
}

// ownedRef loads a provider ref and checks it belongs to the user.
func (s *Service) ownedRef(ctx context.Context, userID domain.UserID, key domain.GearRefKey) (domain.GearExternalRef, error) {
	ref, err := s.gear.GetExternalRef(ctx, key)
	if err != nil {
		return domain.GearExternalRef{}, err
	}
	if ref.UserID != userID {
		return domain.GearExternalRef{}, ErrNotOwner
	}
	return ref, nil
}

// mapRef points the ref at gear, seeds the provider's defaults where the
// user has none, re-derives the record's activities and refreshes totals.
func (s *Service) mapRef(ctx context.Context, userID domain.UserID, ref domain.GearExternalRef, id domain.GearID) error {
	ref.GearID = &id
	if err := s.gear.UpsertExternalRef(ctx, ref); err != nil {
		return err
	}
	if !ref.Retired {
		if err := s.seedDefaults(ctx, userID, id, ref.Import()); err != nil {
			return err
		}
	}
	if err := s.rederiveForRef(ctx, ref.Key()); err != nil {
		return err
	}
	return s.gear.RecomputeTotalsForUser(ctx, userID)
}

func (s *Service) createFromImport(ctx context.Context, userID domain.UserID, key domain.GearRefKey, in domain.GearImport) (domain.GearID, error) {
	now := s.now()
	g := domain.Gear{
		ID:        domain.GearID(s.newID()),
		UserID:    userID,
		Kind:      in.Kind,
		Name:      strings.TrimSpace(in.Name),
		Brand:     strings.TrimSpace(in.Brand),
		Model:     strings.TrimSpace(in.Model),
		Notes:     in.Description,
		Retired:   in.Retired,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if !g.Kind.Valid() {
		g.Kind = domain.GearKindOther
	}
	if g.Name == "" {
		g.Name = key.Provider + " " + key.ExternalID
	}
	if g.Retired {
		g.RetiredAt = &now
	}
	if err := g.Validate(); err != nil {
		return domain.GearID{}, err
	}
	if err := s.gear.Save(ctx, g); err != nil {
		return domain.GearID{}, err
	}
	return g.ID, nil
}

// seedDefaults applies the provider's defaults where the user has none: the
// explicit per-type list, plus the kind's canonical type for a "primary" flag.
func (s *Service) seedDefaults(ctx context.Context, userID domain.UserID, id domain.GearID, in domain.GearImport) error {
	types := append([]domain.ActivityType(nil), in.DefaultFor...)
	if in.Primary {
		switch in.Kind {
		case domain.GearKindBike:
			types = append(types, domain.ActivityTypeRide)
		case domain.GearKindShoe:
			types = append(types, domain.ActivityTypeRun)
		}
	}
	for _, t := range types {
		if !t.Valid() {
			continue
		}
		_, err := s.gear.GetDefault(ctx, userID, t)
		if err == nil {
			continue
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err := s.gear.SetDefault(ctx, domain.GearDefault{UserID: userID, ActivityType: t, GearID: id}); err != nil {
			return err
		}
	}
	return nil
}

// RecomputeTotals rebuilds the user's gear totals from live activities.
func (s *Service) RecomputeTotals(ctx context.Context, userID domain.UserID) error {
	return s.gear.RecomputeTotalsForUser(ctx, userID)
}

// RebuildAllTotals rebuilds totals for every user who owns gear (admin op).
// Returns the number of users processed.
func (s *Service) RebuildAllTotals(ctx context.Context) (int, error) {
	users, err := s.gear.ListUserIDsWithGear(ctx)
	if err != nil {
		return 0, err
	}
	for _, u := range users {
		if err := s.gear.RecomputeTotalsForUser(ctx, u); err != nil {
			return 0, fmt.Errorf("user %s: %w", u, err)
		}
	}
	return len(users), nil
}
