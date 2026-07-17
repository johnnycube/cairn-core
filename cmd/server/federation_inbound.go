package main

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/httpsig"
	"github.com/johnnycube/cairn-core/internal/safehttp"
)

// federationClient is the SSRF-guarded client for all remote actor/inbox fetches.
var federationClient = safehttp.NewClient(15 * time.Second)

// apActivity is the minimal envelope of an inbound ActivityPub activity.
type apActivity struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Actor  string          `json:"actor"`
	Object json.RawMessage `json:"object"`
}

// objectID returns the activity's object as a URL: a bare string, or the `id`
// of an embedded object.
func (a apActivity) objectID() string {
	var s string
	if json.Unmarshal(a.Object, &s) == nil && s != "" {
		return s
	}
	var o struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(a.Object, &o)
	return o.ID
}

// objectType returns the `type` of an embedded object (e.g. the inner Follow of
// an Undo), or "" when the object is a bare id string.
func (a apActivity) objectType() string {
	var o struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(a.Object, &o)
	return o.Type
}

// undoneObject returns the inner activity's object id when an Undo wraps an
// embedded activity of wantType (e.g. Follow → followee, Like → liked object),
// or "" otherwise.
func (a apActivity) undoneObject(wantType string) string {
	var o struct {
		Type   string          `json:"type"`
		Object json.RawMessage `json:"object"`
	}
	if json.Unmarshal(a.Object, &o) != nil || o.Type != wantType {
		return ""
	}
	var s string
	if json.Unmarshal(o.Object, &s) == nil && s != "" {
		return s
	}
	var inner struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(o.Object, &inner)
	return inner.ID
}

// undoneFollowTarget is the followee actor id of an embedded Follow inside an Undo.
func (a apActivity) undoneFollowTarget() string { return a.undoneObject("Follow") }

// domainBlocked reports whether host is on the instance defederation blocklist.
// Centralises the nil-guard + lookup used at every inbound/outbound edge.
func domainBlocked(ctx context.Context, app *App, host string) bool {
	if app.FederationBlocks == nil || host == "" {
		return false
	}
	blocked, _ := app.FederationBlocks.IsBlocked(ctx, host)
	return blocked
}

// localActivityIDFromAPURL extracts the local activity id from an object URL of
// the form .../activities/{uuid} (the shape our outbox/Create objects use). The
// URL's host must equal expectHost, so a foreign-domain URL that merely embeds a
// local UUID is rejected rather than resolved to a local activity.
func localActivityIDFromAPURL(raw, expectHost string) (domain.ActivityID, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, expectHost) {
		return domain.ActivityID{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "activities" {
			if id, err := domain.ParseUUID[domain.ActivityID](parts[i+1]); err == nil {
				return id, true
			}
		}
	}
	return domain.ActivityID{}, false
}

// apActor is the subset of a remote actor document we need.
type apActor struct {
	ID                string `json:"id"`
	Inbox             string `json:"inbox"`
	PreferredUsername string `json:"preferredUsername"`
	Endpoints         struct {
		SharedInbox string `json:"sharedInbox"`
	} `json:"endpoints"`
	PublicKey struct {
		PublicKeyPem string `json:"publicKeyPem"`
	} `json:"publicKey"`
}

// fetchRemoteActor returns a cached or freshly-fetched remote actor plus its
// parsed public key (for signature verification). The fetch is SSRF-guarded.
func fetchRemoteActor(ctx context.Context, app *App, actorID string) (domain.FederatedActor, *rsa.PublicKey, error) {
	// Defederation: never dereference an actor on a blocked domain.
	if domainBlocked(ctx, app, hostOf(actorID)) {
		return domain.FederatedActor{}, nil, fmt.Errorf("domain blocked: %s", hostOf(actorID))
	}
	if a, err := app.FederationActors.Get(ctx, actorID); err == nil {
		if pub, perr := parsePublicKeyPEM(a.PublicKeyPEM); perr == nil {
			return a, pub, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, actorID, nil)
	if err != nil {
		return domain.FederatedActor{}, nil, err
	}
	req.Header.Set("Accept", "application/activity+json")
	resp, err := federationClient.Do(req)
	if err != nil {
		return domain.FederatedActor{}, nil, fmt.Errorf("fetch actor: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return domain.FederatedActor{}, nil, fmt.Errorf("fetch actor %s: status %d", actorID, resp.StatusCode)
	}
	var ra apActor
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ra); err != nil {
		return domain.FederatedActor{}, nil, fmt.Errorf("decode actor: %w", err)
	}
	if ra.Inbox == "" || ra.PublicKey.PublicKeyPem == "" {
		return domain.FederatedActor{}, nil, errors.New("actor missing inbox or public key")
	}
	a := domain.FederatedActor{
		ActorID: actorID, Inbox: ra.Inbox, SharedInbox: ra.Endpoints.SharedInbox,
		PublicKeyPEM: ra.PublicKey.PublicKeyPem, PreferredUsername: ra.PreferredUsername,
		Domain: hostOf(actorID),
	}
	pub, err := parsePublicKeyPEM(a.PublicKeyPEM)
	if err != nil {
		return domain.FederatedActor{}, nil, err
	}
	_ = app.FederationActors.Upsert(ctx, a)
	return a, pub, nil
}

func parsePublicKeyPEM(p string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(p))
	if block == nil {
		return nil, errors.New("invalid public key PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	rk, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}
	return rk, nil
}

// deliverActivity signs `activity` with the local user's key and POSTs it to a
// remote inbox, immediately. Used for the interactive Follow/Accept handshakes;
// activity Create fan-out goes through the durable queue instead.
func deliverActivity(ctx context.Context, app *App, fromUserID domain.UserID, fromActorID, inboxURL string, activity any) error {
	body, err := json.Marshal(activity)
	if err != nil {
		return err
	}
	code, err := deliverSignedBody(ctx, app, fromUserID, fromActorID, inboxURL, body)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("deliver to %s: status %d", inboxURL, code)
	}
	return nil
}

// deliverSignedBody signs the raw activity body with the user's key and POSTs it
// to inboxURL. Returns the HTTP status code (0 on transport error) so callers —
// notably the retry scheduler — can classify permanent vs transient failures.
func deliverSignedBody(ctx context.Context, app *App, fromUserID domain.UserID, fromActorID, inboxURL string, body []byte) (int, error) {
	priv, err := app.FederationKeys.GetPrivateKey(ctx, fromUserID)
	if err != nil {
		return 0, fmt.Errorf("load signing key: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inboxURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/activity+json")
	if err := httpsig.Sign(req, fromActorID+"#main-key", priv, body); err != nil {
		return 0, err
	}
	resp, err := federationClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("deliver: %w", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// feedItemFromObject parses a Create's object into a home-feed item. ok=false
// when the object isn't a usable activity (no id).
func feedItemFromObject(recipient domain.UserID, actorID string, obj json.RawMessage) (domain.FederatedFeedItem, bool) {
	var o struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Content   string          `json:"content"`
		URL       string          `json:"url"`
		Published string          `json:"published"`
		Image     json.RawMessage `json:"image"`
		Sport     struct {
			Discipline     string  `json:"discipline"`
			DistanceM      float64 `json:"distance_m"`
			MovingS        int     `json:"moving_s"`
			ElevationGainM float64 `json:"elevation_gain_m"`
		} `json:"sport:summary"`
	}
	if err := json.Unmarshal(obj, &o); err != nil || o.ID == "" {
		return domain.FederatedFeedItem{}, false
	}
	published := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339, o.Published); err == nil {
		published = t.UTC()
	}
	it := domain.FederatedFeedItem{
		RecipientID: recipient, ActorID: actorID, ActivityAPID: o.ID, Published: published,
		Name: o.Name, Summary: strings.TrimSpace(htmlTag.ReplaceAllString(o.Content, " ")),
		URL: o.URL, ImageURL: apImageURL(o.Image), Sport: o.Sport.Discipline,
	}
	if o.Sport.DistanceM > 0 {
		d := o.Sport.DistanceM
		it.DistanceM = &d
	}
	if o.Sport.MovingS > 0 {
		s := o.Sport.MovingS
		it.DurationS = &s
	}
	if o.Sport.ElevationGainM > 0 {
		e := o.Sport.ElevationGainM
		it.ElevationM = &e
	}
	return it, true
}

// remoteComment parses a Create's object as a reply Note. ok=false when it
// isn't a reply (no inReplyTo) or carries no usable content/id. inReplyTo is the
// AP URL of the activity being replied to; body is the HTML-stripped content.
func remoteComment(obj json.RawMessage) (noteID, inReplyTo, body string, ok bool) {
	var o struct {
		ID        string `json:"id"`
		InReplyTo string `json:"inReplyTo"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(obj, &o); err != nil || o.ID == "" || o.InReplyTo == "" {
		return "", "", "", false
	}
	body = strings.TrimSpace(htmlTag.ReplaceAllString(o.Content, " "))
	if body == "" {
		return "", "", "", false
	}
	return o.ID, o.InReplyTo, body, true
}

// apImageURL accepts either a string or an {url:""} object.
func apImageURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	var o struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.URL
}

func hostOf(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		return u.Host
	}
	return ""
}
