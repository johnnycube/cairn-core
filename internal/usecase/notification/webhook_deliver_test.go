package notification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// fakeWebhookRepo implements the dispatch-relevant subset of
// port.WebhookEndpointRepo (ListEnabledForUser + RecordDeliveryResult); the
// CRUD methods are unused by the dispatcher and return zero values.
type fakeWebhookRepo struct {
	mu        sync.Mutex
	endpoints []domain.WebhookEndpoint
	results   []recorded
}

type recorded struct {
	id   domain.WebhookEndpointID
	code int
	err  string
}

func (f *fakeWebhookRepo) Create(context.Context, domain.WebhookEndpoint) (domain.WebhookEndpoint, error) {
	return domain.WebhookEndpoint{}, nil
}
func (f *fakeWebhookRepo) Update(context.Context, domain.WebhookEndpoint) error { return nil }
func (f *fakeWebhookRepo) RotateSecret(context.Context, domain.WebhookEndpointID, domain.UserID, string) error {
	return nil
}
func (f *fakeWebhookRepo) Delete(context.Context, domain.WebhookEndpointID, domain.UserID) error {
	return nil
}
func (f *fakeWebhookRepo) GetByID(context.Context, domain.WebhookEndpointID, domain.UserID) (domain.WebhookEndpoint, error) {
	return domain.WebhookEndpoint{}, nil
}
func (f *fakeWebhookRepo) ListForUser(context.Context, domain.UserID) ([]domain.WebhookEndpoint, error) {
	return f.endpoints, nil
}
func (f *fakeWebhookRepo) ListEnabledForUser(context.Context, domain.UserID) ([]domain.WebhookEndpoint, error) {
	return f.endpoints, nil
}
func (f *fakeWebhookRepo) RecordDeliveryResult(_ context.Context, id domain.WebhookEndpointID, code int, derr string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, recorded{id, code, derr})
	return nil
}

func webhookNotif(t domain.NotificationType, sev domain.NotificationSeverity) domain.Notification {
	nid, _ := uuid.NewV7()
	return domain.Notification{ID: domain.NotificationID(nid), UserID: domain.UserID(uuid.New()), Type: t, Severity: sev}
}

func deliverWithWebhooks(repo *fakeWebhookRepo) *DeliverNotifications {
	// email nil → only the webhook pass runs; users/prefs unused.
	uc := NewDeliverNotifications(nil, nil, nil, nil, repo, nil, nil, "http://x", nil)
	// Tests post to httptest servers on 127.0.0.1, which the production SSRF
	// dial-guard blocks. Swap in a plain client so the delivery logic is testable.
	uc.httpClient = &http.Client{}
	return uc
}

func TestSubscribes_TypeAndSeverityGating(t *testing.T) {
	ep := domain.WebhookEndpoint{Enabled: true, MinSeverity: domain.NotificationSeverityWarn}
	if !ep.Subscribes(domain.NotificationTypeWorkerOffline, domain.NotificationSeverityError) {
		t.Fatal("empty EventTypes + error >= warn should subscribe")
	}
	if ep.Subscribes(domain.NotificationTypeSegmentPersonalRecord, domain.NotificationSeverityInfo) {
		t.Fatal("info < warn must not subscribe")
	}
	ep.EventTypes = []domain.NotificationType{domain.NotificationTypeSegmentPersonalRecord}
	ep.MinSeverity = domain.NotificationSeverityInfo
	if ep.Subscribes(domain.NotificationTypeWorkerOffline, domain.NotificationSeverityInfo) {
		t.Fatal("type not in EventTypes must not subscribe")
	}
	if !ep.Subscribes(domain.NotificationTypeSegmentPersonalRecord, domain.NotificationSeverityInfo) {
		t.Fatal("matching type should subscribe")
	}
	ep.Enabled = false
	if ep.Subscribes(domain.NotificationTypeSegmentPersonalRecord, domain.NotificationSeverityInfo) {
		t.Fatal("disabled endpoint must not subscribe")
	}
}

func TestDeliverWebhook_SignsAndRecords(t *testing.T) {
	const secret = "whsec_test"
	var (
		gotBody []byte
		gotSig  string
		gotEvt  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Cairn-Signature")
		gotEvt = r.Header.Get("X-Cairn-Event")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &fakeWebhookRepo{endpoints: []domain.WebhookEndpoint{{
		ID: domain.WebhookEndpointID(uuid.New()), Enabled: true,
		URL: srv.URL, SigningSecret: secret, MinSeverity: domain.NotificationSeverityInfo,
	}}}
	deliverWithWebhooks(repo).Execute(context.Background(),
		[]domain.Notification{webhookNotif(domain.NotificationTypeSegmentPersonalRecord, domain.NotificationSeverityInfo)})

	if len(gotBody) == 0 {
		t.Fatal("endpoint received no body")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Fatalf("signature mismatch:\n got %s\nwant %s", gotSig, want)
	}
	if gotEvt != "segment_personal_record" {
		t.Fatalf("X-Cairn-Event = %q", gotEvt)
	}
	if len(repo.results) != 1 || repo.results[0].code != 200 || repo.results[0].err != "" {
		t.Fatalf("expected one 2xx success recorded, got %+v", repo.results)
	}
}

func TestDeliverWebhook_SeverityFilteredOut(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &fakeWebhookRepo{endpoints: []domain.WebhookEndpoint{{
		ID: domain.WebhookEndpointID(uuid.New()), Enabled: true,
		URL: srv.URL, SigningSecret: "x", MinSeverity: domain.NotificationSeverityError,
	}}}
	deliverWithWebhooks(repo).Execute(context.Background(),
		[]domain.Notification{webhookNotif(domain.NotificationTypeActivityImported, domain.NotificationSeverityInfo)})

	if hit {
		t.Fatal("info notification must not reach an error-min endpoint")
	}
	if len(repo.results) != 0 {
		t.Fatalf("no delivery should be recorded, got %+v", repo.results)
	}
}

func TestDeliverWebhook_Non2xxRecordsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	repo := &fakeWebhookRepo{endpoints: []domain.WebhookEndpoint{{
		ID: domain.WebhookEndpointID(uuid.New()), Enabled: true,
		URL: srv.URL, SigningSecret: "x", MinSeverity: domain.NotificationSeverityInfo,
	}}}
	deliverWithWebhooks(repo).Execute(context.Background(),
		[]domain.Notification{webhookNotif(domain.NotificationTypeActivityImported, domain.NotificationSeverityInfo)})

	if len(repo.results) != 1 || repo.results[0].code != 500 || repo.results[0].err == "" {
		t.Fatalf("expected one recorded failure with 500, got %+v", repo.results)
	}
}

func TestDeliverWebhook_NilRepoNoPanic(t *testing.T) {
	NewDeliverNotifications(nil, nil, nil, nil, nil, nil, nil, "http://x", nil).
		Execute(context.Background(), []domain.Notification{webhookNotif(domain.NotificationTypeActivityImported, domain.NotificationSeverityInfo)})
}

func TestIsBlockedWebhookIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", // loopback
		"10.0.0.5", "192.168.1.1", "172.16.0.1", // private
		"169.254.169.254", // link-local (cloud metadata)
		"0.0.0.0",         // unspecified
		"224.0.0.1",       // multicast
	}
	for _, s := range blocked {
		if !isBlockedWebhookIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34"} // public
	for _, s := range allowed {
		if isBlockedWebhookIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}
