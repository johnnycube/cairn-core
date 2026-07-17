//go:build integration

package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/auth"
	"github.com/johnnycube/cairn-core/internal/domain"
)

// seedUser inserts a throwaway user and returns its id, for FK-satisfying
// fixtures in the notification repo integration tests.
func seedUser(t *testing.T) (domain.UserID, *UserRepo) {
	t.Helper()
	ctx, pool := requirePool(t)
	repo := NewUserRepo(pool)
	id := domain.UserID(uuid.New())
	suffix := id.String()[:8]
	u := domain.User{
		ID: id, Username: "ntest-" + suffix, Email: "ntest-" + suffix + "@example.com",
		DisplayName: "N Test", Role: domain.UserRoleUser, Status: domain.UserStatusActive,
		Units: domain.UserUnitsMetric,
	}
	if err := repo.CreateUser(ctx, u, domain.Credentials{UserID: id, PasswordHash: "x"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id, repo
}

func TestWebhookEndpointRepo_Integration(t *testing.T) {
	ctx, pool := requirePool(t)
	userID, _ := seedUser(t)
	sb, err := auth.NewSecretBoxFromMasterKey("dGVzdC1tYXN0ZXIta2V5LTMyLWJ5dGVzLWxvbmctISE=")
	if err != nil {
		t.Fatalf("secretbox: %v", err)
	}
	repo := NewWebhookEndpointRepo(pool, sb)

	created, err := repo.Create(ctx, domain.WebhookEndpoint{
		UserID: userID, Name: "test", URL: "https://example.com/hook",
		SigningSecret: "whsec_topsecret", EventTypes: []domain.NotificationType{domain.NotificationTypeSegmentPersonalRecord},
		MinSeverity: domain.NotificationSeverityInfo, Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// GetByID decrypts the secret back to plaintext.
	got, err := repo.GetByID(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.SigningSecret != "whsec_topsecret" {
		t.Errorf("secret round-trip failed: got %q", got.SigningSecret)
	}
	if len(got.EventTypes) != 1 || got.EventTypes[0] != domain.NotificationTypeSegmentPersonalRecord {
		t.Errorf("event_types round-trip = %v", got.EventTypes)
	}

	// ListForUser must NOT leak the secret.
	list, err := repo.ListForUser(ctx, userID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListForUser: %v (n=%d)", err, len(list))
	}
	if list[0].SigningSecret != "" {
		t.Error("ListForUser leaked the signing secret")
	}

	// ListEnabledForUser decrypts (dispatch path).
	enabled, err := repo.ListEnabledForUser(ctx, userID)
	if err != nil || len(enabled) != 1 || enabled[0].SigningSecret != "whsec_topsecret" {
		t.Fatalf("ListEnabledForUser: %v n=%d", err, len(enabled))
	}

	// Telemetry: success resets failures; repeated failures auto-disable.
	now := time.Now().UTC()
	if err := repo.RecordDeliveryResult(ctx, created.ID, 200, "", now); err != nil {
		t.Fatalf("record success: %v", err)
	}
	for i := 0; i < domain.WebhookMaxConsecutiveFailures; i++ {
		if err := repo.RecordDeliveryResult(ctx, created.ID, 500, "boom", now); err != nil {
			t.Fatalf("record failure %d: %v", i, err)
		}
	}
	after, _ := repo.GetByID(ctx, created.ID, userID)
	if !after.AutoDisabled {
		t.Errorf("expected auto_disabled after %d failures, got %+v", domain.WebhookMaxConsecutiveFailures, after)
	}
	// Now auto-disabled → not in the enabled dispatch list.
	enabled, _ = repo.ListEnabledForUser(ctx, userID)
	if len(enabled) != 0 {
		t.Errorf("auto-disabled endpoint must not be in ListEnabledForUser, got %d", len(enabled))
	}

	// Re-enable via Update clears the auto-disable latch + failure count.
	after.Enabled = true
	if err := repo.Update(ctx, after); err != nil {
		t.Fatalf("Update re-enable: %v", err)
	}
	re, _ := repo.GetByID(ctx, created.ID, userID)
	if re.AutoDisabled || re.ConsecutiveFailures != 0 {
		t.Errorf("re-enable should clear auto_disabled + failures, got %+v", re)
	}
}

func TestQuietHoursRepo_Integration(t *testing.T) {
	ctx, pool := requirePool(t)
	userID, _ := seedUser(t)
	repo := NewQuietHoursRepo(pool)

	// Default (no row) → disabled, UTC.
	def, err := repo.Get(ctx, userID)
	if err != nil {
		t.Fatalf("Get default: %v", err)
	}
	if def.Enabled || def.TZ != "UTC" {
		t.Errorf("default quiet hours = %+v, want disabled/UTC", def)
	}

	// Upsert + round-trip.
	if err := repo.Upsert(ctx, domain.QuietHours{
		UserID: userID, Enabled: true, StartMinute: 1320, EndMinute: 420,
		DaysOfWeek: []int{1, 2, 3, 4, 5}, TZ: "Europe/Berlin",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(ctx, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Enabled || got.StartMinute != 1320 || got.EndMinute != 420 ||
		got.TZ != "Europe/Berlin" || len(got.DaysOfWeek) != 5 {
		t.Errorf("quiet hours round-trip = %+v", got)
	}
}

func TestNotificationDeliveryRepo_Integration(t *testing.T) {
	ctx, pool := requirePool(t)
	userID, _ := seedUser(t)
	notifRepo := NewNotificationRepo(pool)
	delivRepo := NewNotificationDeliveryRepo(pool)

	// A delivery references a notification_event by FK — create one first.
	nid := domain.NotificationID(uuid.New())
	notifs := []domain.Notification{{
		ID: nid, UserID: userID, Type: domain.NotificationTypeActivityImported,
		Severity: domain.NotificationSeverityInfo, TitleI18nKey: "t", BodyI18nKey: "b",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	if err := notifRepo.SaveNotifications(ctx, notifs); err != nil {
		t.Fatalf("SaveNotifications: %v", err)
	}
	// SaveNotifications writes the persisted id back.
	eventID := notifs[0].ID

	if err := delivRepo.Record(ctx, domain.NotificationDelivery{
		EventID: eventID, UserID: userID, Channel: domain.NotificationChannelEmail,
		EmailAddress: "x@y.z", Status: domain.DeliveryStatusSent,
	}); err != nil {
		t.Fatalf("Record sent: %v", err)
	}
	if err := delivRepo.Record(ctx, domain.NotificationDelivery{
		EventID: eventID, UserID: userID, Channel: domain.NotificationChannelEmail,
		Status: domain.DeliveryStatusSuppressedQuietHours,
	}); err != nil {
		t.Fatalf("Record suppressed: %v", err)
	}

	rows, err := delivRepo.ListForUser(ctx, userID, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 delivery rows, got %d", len(rows))
	}
	// Newest-first; both reference our event.
	for _, r := range rows {
		if r.EventID != eventID {
			t.Errorf("delivery event_id = %s, want %s", r.EventID, eventID)
		}
	}
}
