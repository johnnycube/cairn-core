package nats

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

func TestWorkerTemplateNamesMatchBootstrap(t *testing.T) {
	for got, want := range map[string]string{
		domain.WorkerJobStream:      StreamJobs,
		domain.WorkerWebhookStream:  StreamWebhooks,
		domain.WorkerPresenceBucket: KVWorkerPresence,
		domain.WorkerManifestBucket: KVWorkerManifests,
		domain.WorkerRateBucket:     KVRateLimits,
	} {
		if got != want {
			t.Errorf("template name %q != bootstrap name %q", got, want)
		}
	}
}

// newPermissionedWorker bootstraps a server as admin and connects a worker
// restricted to the standard template, as the auth-callout would issue it.
func newPermissionedWorker(t *testing.T) (admin, worker *Bus) {
	t.Helper()
	tpl := domain.StandardPermissionTemplate("strava", "w1-strava")
	s := startServer(t, &server.Options{Users: []*server.User{
		{Username: "admin", Password: "admin"},
		{Username: "worker", Password: "worker", Permissions: &server.Permissions{
			Publish:   &server.SubjectPermission{Allow: tpl.AllowPublish},
			Subscribe: &server.SubjectPermission{Allow: tpl.AllowSubscribe},
		}},
	}})
	connect := func(user string) *Bus {
		nc, err := nats.Connect(s.ClientURL(), nats.UserInfo(user, user), nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {}))
		if err != nil {
			t.Fatal(err)
		}
		b, err := NewBusFromConn(nc, user, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.Close)
		return b
	}
	admin = connect("admin")
	if err := admin.BootstrapStreams(context.Background()); err != nil {
		t.Fatal(err)
	}
	return admin, connect("worker")
}

func TestWorkerTemplateAllowsWorkerJetStreamUse(t *testing.T) {
	admin, worker := newPermissionedWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, cc := range []port.ConsumerConfig{
		{Stream: StreamJobs, Durable: "strava__fetch", Subject: "cairn.jobs.fetch_source.strava.>"},
		{Stream: StreamWebhooks, Durable: "strava__webhook_event", Subject: "cairn.webhooks.w1-strava.event"},
	} {
		ps, err := worker.Pull(ctx, cc)
		if err != nil {
			t.Fatalf("pull %s: %v", cc.Subject, err)
		}
		subj := cc.Subject
		if subj == "cairn.jobs.fetch_source.strava.>" {
			subj = "cairn.jobs.fetch_source.strava.a1"
		}
		if err := admin.Publish(ctx, subj, "", []byte("job")); err != nil {
			t.Fatal(err)
		}
		msgs, err := ps.Fetch(ctx, 1)
		if err != nil || len(msgs) != 1 {
			t.Fatalf("fetch %s: %v (%d msgs)", cc.Subject, err, len(msgs))
		}
		if err := msgs[0].InProgress(ctx); err != nil {
			t.Fatalf("in progress: %v", err)
		}
		if err := msgs[0].Ack(ctx); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}

	if err := worker.Publish(ctx, "cairn.results.fetch_source.strava", "r1", []byte("result")); err != nil {
		t.Fatalf("publish result: %v", err)
	}
	if err := worker.Publish(ctx, "cairn.workers.w1-strava.heartbeat", "", []byte("hb")); err != nil {
		t.Fatalf("core publish: %v", err)
	}

	for _, bucket := range []string{KVWorkerPresence, KVWorkerManifests, KVRateLimits} {
		kv, err := worker.KV(bucket)
		if err != nil {
			t.Fatalf("kv %s: %v", bucket, err)
		}
		if _, err := kv.Get(ctx, "strava"); !errors.Is(err, port.ErrKVKeyNotFound) {
			t.Fatalf("kv %s get: %v", bucket, err)
		}
		rev, err := kv.Put(ctx, "strava", []byte("v1"))
		if err != nil {
			t.Fatalf("kv %s put: %v", bucket, err)
		}
		if _, ok, err := kv.CompareAndSet(ctx, "strava", []byte("v2"), rev); err != nil || !ok {
			t.Fatalf("kv %s cas: %v %v", bucket, ok, err)
		}
	}
}

func TestWorkerTemplateDeniesForeignJetStreamUse(t *testing.T) {
	_, worker := newPermissionedWorker(t)
	// Denied requests surface as timeouts; keep each one short.
	short := func() context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		t.Cleanup(cancel)
		return ctx
	}

	if _, err := worker.Pull(short(), port.ConsumerConfig{
		Stream: StreamJobs, Durable: "steal", Subject: "cairn.jobs.fetch_source.garmin.>",
	}); err == nil {
		t.Error("consumer on another provider's jobs must be denied")
	}
	if _, err := worker.Pull(short(), port.ConsumerConfig{
		Stream: StreamResults, Durable: "steal", Subject: "cairn.results.>",
	}); err == nil {
		t.Error("consumer on the results stream must be denied")
	}
	if _, err := worker.JS().KeyValue(short(), KVBlobHandles); err == nil {
		t.Error("blob handle bucket must be denied")
	}
	if err := worker.JS().DeleteStream(short(), StreamJobs); err == nil {
		t.Error("stream delete must be denied")
	}
}
