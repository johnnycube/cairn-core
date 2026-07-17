package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/encoding/protojson"

	workerv1 "github.com/johnnycube/cairn-core/gen/proto/cairn/worker/v1"
	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
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
