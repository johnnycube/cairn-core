package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGearKindForActivityType(t *testing.T) {
	cases := map[ActivityType]GearKind{
		ActivityTypeRide: GearKindBike,
		ActivityTypeRun:  GearKindShoe,
		ActivityTypeWalk: GearKindShoe,
		ActivityTypeHike: GearKindShoe,
		ActivityTypeSwim: GearKindOther,
		ActivityTypeSki:  GearKindOther,
	}
	for at, want := range cases {
		if got := GearKindForActivityType(at); got != want {
			t.Errorf("%s: got %s want %s", at, got, want)
		}
	}
}

func TestGearValidate(t *testing.T) {
	g := Gear{ID: GearID(uuid.New()), UserID: UserID(uuid.New()), Kind: GearKindBike, Name: "Canyon"}
	if err := g.Validate(); err != nil {
		t.Fatalf("valid gear rejected: %v", err)
	}
	g.Name = "  "
	if err := g.Validate(); !errors.Is(err, ErrGearMissingName) {
		t.Errorf("blank name: got %v", err)
	}
	g.Name = "x"
	g.Kind = "wheel"
	if err := g.Validate(); !errors.Is(err, ErrGearInvalidKind) {
		t.Errorf("bad kind: got %v", err)
	}
}

func gearTestSource(provider string, importedAt time.Time, gearExt string) ActivitySource {
	acct := ExternalAccountID(uuid.New())
	return ActivitySource{
		ID:                SourceID(uuid.New()),
		Provider:          provider,
		ExternalAccountID: &acct,
		ImportedAt:        importedAt,
		Parsed:            ActivitySourcePayload{GearExternalID: gearExt},
	}
}

func TestResolveGear(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	strava := gearTestSource("strava", t0, "b1")
	garmin := gearTestSource("garmin", t0.Add(time.Hour), "g1") // newer
	manual := gearTestSource("manual", t0.Add(2*time.Hour), "")
	bike, other := GearID(uuid.New()), GearID(uuid.New())
	sources := []ActivitySource{manual, strava, garmin}

	t.Run("policy order wins over recency", func(t *testing.T) {
		resolved := map[SourceID]GearID{strava.ID: bike, garmin.ID: other}
		policy := MergePolicy{DefaultPriority: []string{"strava", AnyProvider}}
		got, src, ok := ResolveGear(sources, resolved, policy, nil)
		if !ok || got != bike || src != strava.ID {
			t.Fatalf("got %v/%v/%v", got, src, ok)
		}
	})
	t.Run("unresolved higher-priority source is skipped", func(t *testing.T) {
		resolved := map[SourceID]GearID{garmin.ID: other}
		policy := MergePolicy{DefaultPriority: []string{"strava", AnyProvider}}
		got, src, ok := ResolveGear(sources, resolved, policy, nil)
		if !ok || got != other || src != garmin.ID {
			t.Fatalf("got %v/%v/%v", got, src, ok)
		}
	})
	t.Run("gear group override in policy", func(t *testing.T) {
		resolved := map[SourceID]GearID{strava.ID: bike, garmin.ID: other}
		policy := MergePolicy{
			DefaultPriority: []string{"strava", AnyProvider},
			Overrides:       map[FieldGroup][]string{FieldGroupGear: {"garmin"}},
		}
		got, _, ok := ResolveGear(sources, resolved, policy, nil)
		if !ok || got != other {
			t.Fatalf("got %v/%v", got, ok)
		}
	})
	t.Run("pin wins when resolved, ignored otherwise", func(t *testing.T) {
		resolved := map[SourceID]GearID{strava.ID: bike, garmin.ID: other}
		policy := MergePolicy{DefaultPriority: []string{"strava", AnyProvider}}
		pin := garmin.ID
		if got, _, _ := ResolveGear(sources, resolved, policy, &pin); got != other {
			t.Fatalf("pinned garmin: got %v", got)
		}
		pin = manual.ID
		if got, _, _ := ResolveGear(sources, resolved, policy, &pin); got != bike {
			t.Fatalf("pin on source without gear should fall back: got %v", got)
		}
	})
	t.Run("nothing resolved", func(t *testing.T) {
		if _, _, ok := ResolveGear(sources, nil, DefaultMergePolicyFor(ActivityTypeRide), nil); ok {
			t.Fatal("expected no gear")
		}
	})
	t.Run("default policy picks most recent", func(t *testing.T) {
		resolved := map[SourceID]GearID{strava.ID: bike, garmin.ID: other}
		got, _, ok := ResolveGear(sources, resolved, DefaultMergePolicyFor(ActivityTypeRide), nil)
		if !ok || got != other {
			t.Fatalf("got %v/%v", got, ok)
		}
	})
}

func TestGearRefKeyOf(t *testing.T) {
	s := gearTestSource("strava", time.Now(), "b1")
	k, ok := GearRefKeyOf(s)
	if !ok || k.ExternalID != "b1" || k.Provider != "strava" || k.ExternalAccountID != *s.ExternalAccountID {
		t.Fatalf("got %+v/%v", k, ok)
	}
	s.ExternalAccountID = nil
	if _, ok := GearRefKeyOf(s); ok {
		t.Fatal("provider-less source must not yield a key")
	}
}
