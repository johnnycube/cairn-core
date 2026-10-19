package inmem

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/opencairn-org/cairn-core/internal/port"
)

func TestSubjectMatches(t *testing.T) {
	tests := []struct {
		pattern, subject string
		want             bool
	}{
		{"a.b.c", "a.b.c", true},
		{"a.b.c", "a.b.d", false},
		{"a.*.c", "a.x.c", true},
		{"a.*.c", "a.x.y.c", false},
		{"a.*", "a", false},
		{"a.>", "a.b", true},
		{"a.>", "a.b.c.d", true},
		{"a.>", "a", false},
		{">", "anything.at.all", true},
		{"a.b", "a.b.c", false},
		{"a.b.c", "a.b", false},
		{"*", "x", true},
		{"*", "x.y", false},
	}
	for _, tt := range tests {
		if got := subjectMatches(tt.pattern, tt.subject); got != tt.want {
			t.Errorf("subjectMatches(%q, %q) = %v, want %v", tt.pattern, tt.subject, got, tt.want)
		}
	}
}

func TestPublishSubscribe(t *testing.T) {
	ctx := context.Background()
	b := New()

	if _, err := b.Subscribe(ctx, port.ConsumerConfig{}, nil); err == nil {
		t.Fatal("expected error for empty subject")
	}

	var wild, exact []port.Message
	subW, err := b.Subscribe(ctx, port.ConsumerConfig{Subject: "jobs.>"}, func(_ context.Context, m port.Message) error {
		wild = append(wild, m)
		return errors.New("ignored")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Subscribe(ctx, port.ConsumerConfig{Subject: "jobs.import"}, func(_ context.Context, m port.Message) error {
		exact = append(exact, m)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := b.Publish(ctx, "jobs.import", "id-1", []byte("one")); err != nil {
		t.Fatal(err)
	}
	// duplicate msgID is dropped
	if err := b.Publish(ctx, "jobs.import", "id-1", []byte("dup")); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, "jobs.export", "", []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, "other", "", []byte("none")); err != nil {
		t.Fatal(err)
	}

	if len(exact) != 1 || string(exact[0].Body) != "one" || exact[0].Headers["Nats-Msg-Id"] != "id-1" {
		t.Fatalf("exact = %+v", exact)
	}
	if len(wild) != 2 || string(wild[1].Body) != "two" || wild[1].Subject != "jobs.export" {
		t.Fatalf("wild = %+v", wild)
	}
	if _, ok := wild[1].Headers["Nats-Msg-Id"]; ok {
		t.Fatal("no msg id header expected without msgID")
	}

	if err := subW.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, "jobs.export", "", []byte("three")); err != nil {
		t.Fatal(err)
	}
	if len(wild) != 2 {
		t.Fatalf("closed subscription still received: %d", len(wild))
	}
}

func TestPullUnsupported(t *testing.T) {
	if _, err := New().Pull(context.Background(), port.ConsumerConfig{}); err == nil {
		t.Fatal("expected Pull error")
	}
}

func TestRequestRespond(t *testing.T) {
	ctx := context.Background()
	b := New()

	if _, err := b.Request(ctx, "svc.echo", nil, time.Second); err == nil {
		t.Fatal("expected no-responder error")
	}

	sub, err := b.RespondTo(ctx, "svc.echo", func(rctx context.Context, body []byte) ([]byte, error) {
		if _, ok := rctx.Deadline(); !ok {
			return nil, errors.New("missing deadline")
		}
		return append([]byte("re:"), body...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Request(ctx, "svc.echo", []byte("hi"), time.Second)
	if err != nil || string(got) != "re:hi" {
		t.Fatalf("Request = %q, %v", got, err)
	}

	if err := sub.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "svc.echo", nil, time.Second); err == nil {
		t.Fatal("expected error after responder closed")
	}
}

func TestKV(t *testing.T) {
	ctx := context.Background()
	b := New()
	kv, err := b.KV("bucket")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := b.KV("bucket")
	if kv != again {
		t.Fatal("KV handle not reused per bucket")
	}
	other, _ := b.KV("other")
	if other == kv {
		t.Fatal("distinct buckets share a handle")
	}

	if _, err := kv.Get(ctx, "missing"); !errors.Is(err, port.ErrKVKeyNotFound) {
		t.Fatalf("Get missing err = %v", err)
	}

	val := []byte("v1")
	rev1, err := kv.Put(ctx, "k", val)
	if err != nil || rev1 != 1 {
		t.Fatalf("Put = %d, %v", rev1, err)
	}
	val[0] = 'X' // caller mutation must not leak
	e, err := kv.Get(ctx, "k")
	if err != nil || string(e.Value) != "v1" || e.Revision != 1 || e.Key != "k" || e.CreatedAt.IsZero() {
		t.Fatalf("Get = %+v, %v", e, err)
	}

	if _, ok, _ := kv.CompareAndSet(ctx, "k", []byte("v2"), 99); ok {
		t.Fatal("CAS with wrong revision succeeded")
	}
	rev2, ok, err := kv.CompareAndSet(ctx, "k", []byte("v2"), rev1)
	if err != nil || !ok || rev2 != 2 {
		t.Fatalf("CAS = %d, %v, %v", rev2, ok, err)
	}
	if e, _ := kv.Get(ctx, "k"); string(e.Value) != "v2" || e.Revision != 2 {
		t.Fatalf("after CAS = %+v", e)
	}

	if _, ok, _ := kv.CompareAndSet(ctx, "new", []byte("n"), 5); ok {
		t.Fatal("CAS create with non-zero rev succeeded")
	}
	rev3, ok, err := kv.CompareAndSet(ctx, "new", []byte("n"), 0)
	if err != nil || !ok || rev3 != 3 {
		t.Fatalf("CAS create = %d, %v, %v", rev3, ok, err)
	}

	keys, err := kv.Keys(ctx)
	slices.Sort(keys)
	if err != nil || !slices.Equal(keys, []string{"k", "new"}) {
		t.Fatalf("Keys = %v, %v", keys, err)
	}

	if err := kv.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Get(ctx, "k"); !errors.Is(err, port.ErrKVKeyNotFound) {
		t.Fatalf("Get after delete err = %v", err)
	}

	if _, err := kv.Watch(ctx, "k"); err == nil {
		t.Fatal("expected Watch error")
	}
}

func TestObjectStore(t *testing.T) {
	ctx := context.Background()
	b := New()
	os, err := b.ObjectStore("blobs")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := b.ObjectStore("blobs"); again != os {
		t.Fatal("ObjectStore handle not reused per bucket")
	}

	if _, _, err := os.Get(ctx, "nope"); err == nil {
		t.Fatal("expected not-found error")
	}

	data := []byte("payload")
	meta := port.ObjectMeta{ContentType: "text/plain", SizeBytes: 7}
	if err := os.Put(ctx, "obj", data, meta); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	got, gotMeta, err := os.Get(ctx, "obj")
	if err != nil || string(got) != "payload" || gotMeta.ContentType != "text/plain" || gotMeta.SizeBytes != 7 {
		t.Fatalf("Get = %q, %+v, %v", got, gotMeta, err)
	}
	got[0] = 'Y'
	if again, _, _ := os.Get(ctx, "obj"); string(again) != "payload" {
		t.Fatalf("returned slice aliases storage: %q", again)
	}

	if err := os.Delete(ctx, "obj"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := os.Get(ctx, "obj"); err == nil {
		t.Fatal("expected error after delete")
	}
}
