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
	// until SetRecompute; LinkRef then only moves the ref.
	recompute func(ctx context.Context, id domain.ActivityID) error
}

// SetRecompute installs the activity re-derivation LinkRef uses.
func (s *Service) SetRecompute(fn func(ctx context.Context, id domain.ActivityID) error) {
	s.recompute = fn
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

// LinkRef maps a provider gear record onto gear the user chose: the golden
// record. The ref must already be known (imported) and belong to the user.
// Activities whose gear the merge derived from that record are re-derived,
// so they move with the mapping; user-set activities stay as they are.
func (s *Service) LinkRef(ctx context.Context, userID domain.UserID, id domain.GearID, key domain.GearRefKey) (domain.Gear, error) {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return domain.Gear{}, err
	}
	currentID, err := s.gear.FindByExternalRef(ctx, key)
	if err != nil {
		return domain.Gear{}, err
	}
	current, err := s.Get(ctx, userID, currentID)
	if err != nil {
		return domain.Gear{}, err
	}
	if currentID != id {
		if err := s.gear.UpsertExternalRef(ctx, movedRef(current, key, id)); err != nil {
			return domain.Gear{}, err
		}
	}
	if err := s.rederiveForRef(ctx, key); err != nil {
		return domain.Gear{}, err
	}
	if err := s.gear.RecomputeTotalsForUser(ctx, userID); err != nil {
		return domain.Gear{}, err
	}
	return s.gear.Get(ctx, id)
}

// movedRef returns from's ref for key with its gear id replaced.
func movedRef(from domain.Gear, key domain.GearRefKey, to domain.GearID) domain.GearExternalRef {
	for _, r := range from.ExternalRefs {
		if r.Key() == key {
			r.GearID = to
			return r
		}
	}
	return domain.GearExternalRef{GearID: to, Provider: key.Provider, ExternalAccountID: key.ExternalAccountID, ExternalID: key.ExternalID}
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

// ImportFromProvider upserts a provider's gear record. A new ref creates
// gear from the provider's description; an existing ref only refreshes the
// ref itself and follows a provider-side retirement (user edits to name,
// brand and model are never clobbered). Provider defaults seed the user's
// per-type defaults only where none is set.
func (s *Service) ImportFromProvider(ctx context.Context, userID domain.UserID, key domain.GearRefKey, in domain.GearImport) (domain.GearID, error) {
	var out domain.GearID
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		id, err := s.findOrCreateImported(ctx, userID, key, in)
		if err != nil {
			return err
		}
		if err := s.gear.UpsertExternalRef(ctx, domain.GearExternalRef{
			GearID:            id,
			Provider:          key.Provider,
			ExternalAccountID: key.ExternalAccountID,
			ExternalID:        key.ExternalID,
			ProviderName:      in.Name,
			ProviderDistanceM: in.DistanceM,
			Primary:           in.Primary,
			LastSeenAt:        s.now(),
		}); err != nil {
			return err
		}
		if !in.Retired {
			if err := s.seedDefaults(ctx, userID, id, in); err != nil {
				return err
			}
		}
		out = id
		return nil
	})
	if err != nil {
		return domain.GearID{}, fmt.Errorf("import gear %s/%s: %w", key.Provider, key.ExternalID, err)
	}
	return out, nil
}

// findOrCreateImported resolves the ref to existing gear (following a
// provider-side retirement) or creates gear from the import.
func (s *Service) findOrCreateImported(ctx context.Context, userID domain.UserID, key domain.GearRefKey, in domain.GearImport) (domain.GearID, error) {
	id, err := s.gear.FindByExternalRef(ctx, key)
	if errors.Is(err, domain.ErrNotFound) {
		return s.createFromImport(ctx, userID, key, in)
	}
	if err != nil {
		return domain.GearID{}, err
	}
	g, err := s.gear.Get(ctx, id)
	if err != nil {
		return domain.GearID{}, err
	}
	if g.UserID != userID {
		return domain.GearID{}, ErrNotOwner
	}
	if in.Retired && !g.Retired {
		now := s.now()
		g.Retired, g.RetiredAt = true, &now
		if err := s.gear.Save(ctx, g); err != nil {
			return domain.GearID{}, err
		}
	}
	return id, nil
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
