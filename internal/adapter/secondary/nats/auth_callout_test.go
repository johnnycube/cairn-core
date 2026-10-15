package nats

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/johnnycube/cairn-core/internal/auth"
	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
	"github.com/johnnycube/cairn-core/internal/usecase/enrollment"
)

// fakeEnrollments is an in-memory port.WorkerEnrollmentRepo for the auth-callout tests.
type fakeEnrollments struct {
	port.WorkerEnrollmentRepo
	mu     sync.Mutex
	rows   []domain.WorkerEnrollment
	grants []domain.WorkerCredentialGrant
}

func (f *fakeEnrollments) FindEnrollmentByTokenHash(_ context.Context, hash []byte) (domain.WorkerEnrollment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.rows {
		if string(e.TokenHash) == string(hash) {
			return e, nil
		}
	}
	return domain.WorkerEnrollment{}, domain.ErrEnrollmentNotFound
}

func (f *fakeEnrollments) IncrementUses(_ context.Context, id domain.WorkerEnrollmentID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows[i].Uses++
			return f.rows[i].Uses, nil
		}
	}
	return 0, domain.ErrEnrollmentNotFound
}

func (f *fakeEnrollments) CreateGrant(_ context.Context, g domain.WorkerCredentialGrant) (domain.WorkerCredentialGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants = append(f.grants, g)
	return g, nil
}

func (f *fakeEnrollments) grantCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.grants)
}

type fakeSigningKeys struct{ key domain.SigningKey }

func (f *fakeSigningKeys) GetActive(context.Context, string) (domain.SigningKey, error) {
	if f.key.PublicKey == "" {
		return domain.SigningKey{}, domain.ErrNotFound
	}
	return f.key, nil
}

func (f *fakeSigningKeys) CreateKey(_ context.Context, k domain.SigningKey) (domain.SigningKey, error) {
	f.key = k
	return k, nil
}

func (f *fakeSigningKeys) DeactivateAll(context.Context, string, time.Time) error { return nil }

type noTx struct{}

func (noTx) InTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

// capturingIssuer records every user JWT the auth-callout mints.
type capturingIssuer struct {
	port.NATSCredentialIssuer
	mu   sync.Mutex
	jwts []string
}

func (c *capturingIssuer) IssueWorkerCredential(ctx context.Context, in port.IssueCredentialInput) (port.IssueCredentialOutput, error) {
	out, err := c.NATSCredentialIssuer.IssueWorkerCredential(ctx, in)
	if err == nil {
		c.mu.Lock()
		c.jwts = append(c.jwts, out.UserJWT)
		c.mu.Unlock()
	}
	return out, err
}

func (c *capturingIssuer) minted() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.jwts)
}

// calloutEnv is an embedded nats-server delegating client auth to core's auth-callout subscriber.
type calloutEnv struct {
	srv         *server.Server
	enrollments *fakeEnrollments
	issuer      *capturingIssuer
}

func newCalloutEnv(t *testing.T) *calloutEnv {
	t.Helper()
	ctx := context.Background()
	box, err := auth.NewSecretBoxFromMasterKey("auth-callout-test-master-key-material-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	keys := &fakeSigningKeys{}
	sk, err := BootstrapAccountKey(ctx, keys, box, nil)
	if err != nil {
		t.Fatal(err)
	}
	issuer := NewCredentialIssuer(keys, box)
	env := &calloutEnv{enrollments: &fakeEnrollments{}, issuer: &capturingIssuer{NATSCredentialIssuer: issuer}}

	authAcc, appAcc := server.NewAccount("AUTH"), server.NewAccount("APP")
	env.srv = startServer(t, &server.Options{
		Accounts: []*server.Account{authAcc, appAcc},
		Users:    []*server.User{{Username: "core", Password: "core", Account: authAcc}},
		AuthCallout: &server.AuthCallout{
			Issuer:    sk.PublicKey,
			Account:   "AUTH",
			AuthUsers: []string{"core"},
		},
	})
	nc, err := nats.Connect(env.srv.ClientURL(), nats.UserInfo("core", "core"))
	if err != nil {
		t.Fatal(err)
	}
	bus, err := NewBusFromConn(nc, "core", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)
	proc := enrollment.NewProcessAuthCallout(env.enrollments, env.issuer, noTx{}, nil, nil, time.Hour)
	sub, err := NewAuthCalloutSubscriber(bus, proc, issuer, nil).Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close(context.Background()) })
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return env
}

// storeEnrollment writes a row directly, as a pre-v0.3.1 admin API call left it.
func (e *calloutEnv) storeEnrollment(name, provider string) string {
	token := "enroll-" + name + "-" + provider
	e.enrollments.rows = append(e.enrollments.rows, domain.WorkerEnrollment{
		ID:                domain.WorkerEnrollmentID(uuid.New()),
		TokenHash:         enrollment.HashEnrollmentToken(token),
		Name:              name,
		Provider:          provider,
		Version:           1,
		WorkerNamePattern: "*",
		ExpiresAt:         time.Now().Add(time.Hour),
	})
	return token
}

func (e *calloutEnv) connectWorker(name, token string) (*nats.Conn, error) {
	return nats.Connect(e.srv.ClientURL(), nats.Token(token), nats.Name(name),
		nats.NoReconnect(), nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {}))
}

// Reproduces the v0.3.0 bug: an enrollment without provider got a user JWT
// with empty allow lists, which NATS treats as unrestricted (cairn.tokens.>,
// cairn.creds.>, other providers' jobs).
func TestAuthCalloutRejectsEnrollmentWithoutProvider(t *testing.T) {
	env := newCalloutEnv(t)
	nc, err := env.connectWorker("w1", env.storeEnrollment("w1", ""))
	if err == nil {
		nc.Close()
		t.Error("enrollment without provider must not be admitted")
	}
	for _, tok := range env.issuer.minted() {
		uc, err := jwt.DecodeUserClaims(tok)
		if err != nil {
			t.Fatal(err)
		}
		t.Errorf("minted user JWT for an enrollment without provider: pub allow %v, sub allow %v (empty = unrestricted)",
			uc.Pub.Allow, uc.Sub.Allow)
	}
	if n := env.enrollments.grantCount(); n != 0 {
		t.Errorf("no credential grant may be recorded, got %d", n)
	}
}

func TestAuthCalloutIssuesScopedJWTForProvider(t *testing.T) {
	env := newCalloutEnv(t)
	if nc, err := env.connectWorker("w1", env.storeEnrollment("w1", "strava")); err == nil {
		nc.Close()
	}
	minted := env.issuer.minted()
	if len(minted) != 1 {
		t.Fatalf("want one minted JWT, got %d", len(minted))
	}
	uc, err := jwt.DecodeUserClaims(minted[0])
	if err != nil {
		t.Fatal(err)
	}
	if !uc.Sub.Allow.Contains("cairn.jobs.fetch_source.strava") {
		t.Errorf("sub allow lacks own job subject: %v", uc.Sub.Allow)
	}
	for _, subj := range []string{"cairn.tokens.>", "cairn.creds.>", "cairn.jobs.fetch_source.garmin"} {
		if uc.Sub.Allow.Contains(subj) || uc.Pub.Allow.Contains(subj) {
			t.Errorf("%s must not be granted", subj)
		}
	}
}

func TestIssueWorkerCredentialRefusesEmptyAllowLists(t *testing.T) {
	ctx := context.Background()
	box, err := auth.NewSecretBoxFromMasterKey("auth-callout-test-master-key-material-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	keys := &fakeSigningKeys{}
	if _, err := BootstrapAccountKey(ctx, keys, box, nil); err != nil {
		t.Fatal(err)
	}
	issuer := NewCredentialIssuer(keys, box)
	ukp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	upub, err := ukp.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	strava := domain.StandardPermissionTemplate("strava", "w1-strava")
	for name, perms := range map[string]domain.WorkerPermissionTemplate{
		"no provider":  domain.StandardPermissionTemplate("", "w1"),
		"no publish":   {AllowSubscribe: strava.AllowSubscribe},
		"no subscribe": {AllowPublish: strava.AllowPublish},
	} {
		_, err := issuer.IssueWorkerCredential(ctx, port.IssueCredentialInput{UserNKeyPublic: upub, Permissions: perms})
		var verr *port.IssueValidationError
		if !errors.As(err, &verr) || verr.Field != "Permissions" {
			t.Errorf("%s: want permissions validation error, got %v", name, err)
		}
	}
	if _, err := issuer.IssueWorkerCredential(ctx, port.IssueCredentialInput{UserNKeyPublic: upub, Permissions: strava}); err != nil {
		t.Errorf("standard template: %v", err)
	}
}
