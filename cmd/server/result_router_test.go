package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/encoding/protojson"

	cairnv1 "github.com/opencairn-org/cairn-core/gen/proto/cairn/v1"
	workerv1 "github.com/opencairn-org/cairn-core/gen/proto/cairn/worker/v1"
	"github.com/opencairn-org/cairn-core/internal/adapter/secondary/inmem"
	"github.com/opencairn-org/cairn-core/internal/domain"
	"github.com/opencairn-org/cairn-core/internal/port"
	gearuc "github.com/opencairn-org/cairn-core/internal/usecase/gear"
)

// fakeQueue records MarkFailedByExternalID calls; all other
// ImportQueueRepo methods panic via the embedded nil interface.
type fakeQueue struct {
	port.ImportQueueRepo
	failedExtID  string
	failedReason string
}

func (f *fakeQueue) MarkFailedByExternalID(_ context.Context, _ domain.ExternalAccountID, _ domain.ImportItemType, externalID, reason string) error {
	f.failedExtID = externalID
	f.failedReason = reason
	return nil
}

// fakeBlobStore serves one object and records Get/Delete keys.
type fakeBlobStore struct {
	port.BlobStore
	objects map[string][]byte
	gotKey  string
	deleted []string
}

func (f *fakeBlobStore) Get(_ context.Context, key string) ([]byte, string, error) {
	f.gotKey = key
	data, ok := f.objects[key]
	if !ok {
		return nil, "", domain.ErrNotFound
	}
	return data, "application/json", nil
}

func (f *fakeBlobStore) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestRouteJobResult_WorkerFailureFailsQueueItem(t *testing.T) {
	q := &fakeQueue{}
	app := &App{ImportQueue: q}

	body, err := protojson.Marshal(&workerv1.JobResult{
		WorkerName: "strava-fetcher",
		Error: &workerv1.WorkerError{
			Class:   workerv1.ErrorClass_ERROR_CLASS_INVALID_INPUT,
			Code:    "result_too_large",
			Message: "payload exceeds max object size",
		},
		FailedRef: &workerv1.ExternalRef{
			Provider:          "strava",
			ExternalAccountId: "3b241101-e2bb-4255-8caf-4136c566a962",
			ExternalId:        "1788498851",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = routeJobResult(context.Background(), app, testLogger(), port.Message{
		Subject: "cairn.results.fetch_source.strava",
		Body:    body,
	})
	if err != nil {
		t.Fatalf("worker-failure envelope should ack, got %v", err)
	}
	if q.failedExtID != "1788498851" {
		t.Fatalf("queue item not failed, got ext id %q", q.failedExtID)
	}
	if want := "worker: result_too_large: payload exceeds max object size"; q.failedReason != want {
		t.Fatalf("reason = %q; want %q", q.failedReason, want)
	}
}

func TestRouteJobResult_ClaimCheckResolvesAndDeletes(t *testing.T) {
	const key = "transfer/strava/abc.json"

	full, err := protojson.Marshal(&workerv1.JobResult{WorkerName: "strava-fetcher"})
	if err != nil {
		t.Fatal(err)
	}
	bs := &fakeBlobStore{objects: map[string][]byte{key: full}}
	app := &App{BlobStore: bs}

	envelope, err := protojson.Marshal(&workerv1.JobResult{
		WorkerName: "strava-fetcher",
		PayloadRef: &workerv1.PayloadRef{BlobId: key, ContentType: "application/json"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = routeJobResult(context.Background(), app, testLogger(), port.Message{
		Subject: "cairn.results.fetch_source.strava",
		Body:    envelope,
	})
	if err != nil {
		t.Fatalf("claim-checked result failed: %v", err)
	}
	if bs.gotKey != key {
		t.Fatalf("blob fetched from %q; want %q", bs.gotKey, key)
	}
	if len(bs.deleted) != 1 || bs.deleted[0] != key {
		t.Fatalf("object not deleted after ingest: %v", bs.deleted)
	}
}

func TestRouteJobResult_ClaimCheckMissingPayloadIsTerminal(t *testing.T) {
	bs := &fakeBlobStore{objects: map[string][]byte{}}
	app := &App{BlobStore: bs}

	envelope, err := protojson.Marshal(&workerv1.JobResult{
		PayloadRef: &workerv1.PayloadRef{BlobId: "transfer/strava/gone.json"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = routeJobResult(context.Background(), app, testLogger(), port.Message{
		Subject: "cairn.results.fetch_source.strava",
		Body:    envelope,
	})
	var term *port.TerminalError
	if !errors.As(err, &term) || term.Reason != "payload_missing" {
		t.Fatalf("want TerminalError payload_missing, got %v", err)
	}
	if len(bs.deleted) != 0 {
		t.Fatalf("must not delete on failure: %v", bs.deleted)
	}
}

func TestTransferKey(t *testing.T) {
	if got, want := transferKey("strava", "b-1"), "transfer/strava/b-1.json"; got != want {
		t.Fatalf("transferKey = %q; want %q", got, want)
	}
}

func TestIsDeterministicDBError(t *testing.T) {
	dup := &pgconn.PgError{Code: "23505"} // unique_violation
	if !isDeterministicDBError(fmt.Errorf("write stream: %w", dup)) {
		t.Error("unique violation must be terminal")
	}
	if !isDeterministicDBError(&pgconn.PgError{Code: "22003"}) { // numeric out of range
		t.Error("data exception must be terminal")
	}
	if isDeterministicDBError(&pgconn.PgError{Code: "57014"}) { // statement timeout
		t.Error("timeout must stay transient")
	}
	if isDeterministicDBError(errors.New("network blip")) {
		t.Error("non-pg errors must stay transient")
	}
}

func TestRouteJobResult_IgnoresUnknownFields(t *testing.T) {
	q := &fakeQueue{}
	app := &App{ImportQueue: q}
	// A worker on a newer additive contract: a field this core does not know.
	body := []byte(`{"workerName":"strava-fetcher","futureField":{"x":1}}`)
	err := routeJobResult(context.Background(), app, testLogger(), port.Message{
		Subject: "cairn.results.fetch_source.strava",
		Body:    body,
	})
	if err != nil {
		t.Fatalf("unknown field must be ignored, got %v", err)
	}
}

type passTx struct{}

func (passTx) InTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }

type fakeAccountLookup map[domain.ExternalAccountID]domain.ExternalAccount

func (f fakeAccountLookup) GetExternalAccount(_ context.Context, id domain.ExternalAccountID) (domain.ExternalAccount, error) {
	a, ok := f[id]
	if !ok {
		return domain.ExternalAccount{}, domain.ErrNotFound
	}
	return a, nil
}

func TestProcessWorkerEvent_GearUpsertsByRef(t *testing.T) {
	repo := inmem.NewGearRepo()
	userID, acctID := domain.UserID(uuid.New()), domain.ExternalAccountID(uuid.New())
	accounts := fakeAccountLookup{acctID: {ID: acctID, UserID: userID, Provider: "strava"}}
	app := &App{Gear: gearuc.NewService(repo, accounts, passTx{}, nil, nil, nil, nil)}
	user, acct := userID.String(), acctID.String()
	ev := &workerv1.WorkerEvent{
		Type: workerv1.WorkerEventType_WORKER_EVENT_TYPE_GEAR,
		Entity: &workerv1.WorkerEvent_Gear{Gear: &workerv1.ImportedGear{
			Ref:     &workerv1.ExternalRef{UserId: user, Provider: "strava", ExternalAccountId: acct, ExternalId: "b42"},
			Payload: &workerv1.GearImport{Kind: cairnv1.GearKind_GEAR_KIND_BIKE, Name: "Canyon", Primary: true},
		}},
	}
	for range 2 { // second delivery must not duplicate
		if err := processWorkerEvent(context.Background(), app, testLogger(), &workerv1.JobResult{}, &ingestBundle{}, ev); err != nil {
			t.Fatalf("gear event: %v", err)
		}
	}
	// Unknown provider gear is recorded as a pending ref, never as gear.
	pending, _ := repo.ListPendingRefs(context.Background(), userID)
	if repo.Count() != 0 || len(pending) != 1 {
		t.Fatalf("gear=%d pending=%d", repo.Count(), len(pending))
	}
	if r := pending[0]; r.ProviderName != "Canyon" || r.Kind != domain.GearKindBike || r.UserID != userID {
		t.Fatalf("ref = %+v", r)
	}

	// A ref whose user_id does not own the account is rejected, not stored.
	foreign := &workerv1.WorkerEvent{Type: workerv1.WorkerEventType_WORKER_EVENT_TYPE_GEAR,
		Entity: &workerv1.WorkerEvent_Gear{Gear: &workerv1.ImportedGear{
			Ref:     &workerv1.ExternalRef{UserId: uuid.NewString(), Provider: "strava", ExternalAccountId: acct, ExternalId: "b99"},
			Payload: &workerv1.GearImport{Name: "not yours"},
		}}}
	if err := processWorkerEvent(context.Background(), app, testLogger(), &workerv1.JobResult{}, &ingestBundle{}, foreign); err != nil {
		t.Fatalf("foreign gear event must be non-fatal: %v", err)
	}
	if pending, _ := repo.ListPendingRefs(context.Background(), userID); len(pending) != 1 {
		t.Fatalf("foreign ref stored: %d", len(pending))
	}

	// Bad ref: logged, never NAKs the message.
	bad := &workerv1.WorkerEvent{Type: workerv1.WorkerEventType_WORKER_EVENT_TYPE_GEAR,
		Entity: &workerv1.WorkerEvent_Gear{Gear: &workerv1.ImportedGear{Ref: &workerv1.ExternalRef{UserId: "nope", ExternalId: "x"}}}}
	if err := processWorkerEvent(context.Background(), app, testLogger(), &workerv1.JobResult{}, &ingestBundle{}, bad); err != nil {
		t.Fatalf("bad gear event must be non-fatal: %v", err)
	}
}
