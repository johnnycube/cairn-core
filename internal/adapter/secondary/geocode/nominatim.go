// Package geocode implements port.Geocoder against Nominatim-compatible
// reverse-geocoding services (the OpenStreetMap public instance by default,
// or a self-hosted Nominatim via CAIRN_GEOCODER_URL).
//
// The adapter is built to respect the OSM Nominatim Usage Policy
// (https://operations.osmfoundation.org/policies/nominatim/):
//
//   - At most one request per second (a process-wide reserving limiter).
//   - A descriptive, identifying User-Agent (and an optional contact email,
//     passed both in the UA and as the `email=` query param).
//   - Results cached by coarse (rounded) coordinates so repeated/nearby
//     lookups never re-hit the service.
//
// Callers (the backfiller) can therefore loop over thousands of coordinates
// without implementing any throttling of their own.
package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/johnnycube/cairn-core/internal/config"
	"github.com/johnnycube/cairn-core/internal/port"
)

// Nominatim is a port.Geocoder backed by a Nominatim-compatible /reverse API.
type Nominatim struct {
	client    *http.Client
	baseURL   string
	userAgent string
	email     string
	limiter   *reservingLimiter

	mu    sync.Mutex
	cache map[string]port.Place
}

// New builds a Nominatim geocoder from config. The returned geocoder is safe
// for concurrent use; its rate limiter serialises all callers to the
// configured minimum interval.
func New(cfg config.GeocoderConfig) *Nominatim {
	ua := cfg.UserAgent
	if ua == "" {
		ua = "Cairn self-hosted activity tracker (https://github.com/johnnycube/cairn-core)"
	}
	if cfg.Email != "" {
		ua = ua + " - " + cfg.Email
	}
	interval := cfg.MinInterval
	if interval <= 0 {
		interval = time.Second
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Nominatim{
		client:    &http.Client{Timeout: timeout},
		baseURL:   strings.TrimRight(cfg.URL, "/"),
		userAgent: ua,
		email:     cfg.Email,
		limiter:   &reservingLimiter{interval: interval},
		cache:     make(map[string]port.Place),
	}
}

// cacheKey rounds coordinates to ~3 decimal places (~110 m) so nearby start
// points (and repeated lookups of the same activity) share a cached answer.
func cacheKey(lat, lon float64) string {
	return fmt.Sprintf("%.3f,%.3f", lat, lon)
}

// ReverseGeocode resolves a coordinate to a place name. A successful HTTP
// response with no usable place is cached and returned as an empty-Name Place
// (not an error) so the caller can mark the activity "attempted, no place".
// Transport/HTTP errors are returned as errors so the caller can retry later.
func (n *Nominatim) ReverseGeocode(ctx context.Context, lat, lon float64) (port.Place, error) {
	key := cacheKey(lat, lon)

	n.mu.Lock()
	if p, ok := n.cache[key]; ok {
		n.mu.Unlock()
		return p, nil
	}
	n.mu.Unlock()

	if err := n.limiter.wait(ctx); err != nil {
		return port.Place{}, err
	}

	q := url.Values{}
	q.Set("format", "jsonv2")
	q.Set("lat", fmt.Sprintf("%f", lat))
	q.Set("lon", fmt.Sprintf("%f", lon))
	q.Set("zoom", "10") // city/town level
	q.Set("addressdetails", "1")
	if n.email != "" {
		q.Set("email", n.email)
	}
	reqURL := n.baseURL + "/reverse?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return port.Place{}, fmt.Errorf("geocode: build request: %w", err)
	}
	req.Header.Set("User-Agent", n.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return port.Place{}, fmt.Errorf("geocode: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return port.Place{}, fmt.Errorf("geocode: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		Address map[string]string `json:"address"`
		Error   string            `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return port.Place{}, fmt.Errorf("geocode: decode: %w", err)
	}

	place := port.Place{
		Name:    pickPlaceName(body.Address),
		Country: body.Address["country"],
	}

	n.mu.Lock()
	n.cache[key] = place
	n.mu.Unlock()

	return place, nil
}

// pickPlaceName chooses the most specific populated-place name from a Nominatim
// address object, falling back to progressively coarser administrative levels.
func pickPlaceName(addr map[string]string) string {
	if addr == nil {
		return ""
	}
	for _, k := range []string{
		"city", "town", "village", "municipality", "hamlet",
		"suburb", "city_district", "county", "state",
	} {
		if v := strings.TrimSpace(addr[k]); v != "" {
			return v
		}
	}
	return ""
}

// reservingLimiter serialises callers to a minimum interval. Each caller
// reserves the next free slot, so N concurrent callers are spaced `interval`
// apart rather than all firing at once.
type reservingLimiter struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func (l *reservingLimiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	start := now
	if l.next.After(now) {
		start = l.next
	}
	l.next = start.Add(l.interval)
	l.mu.Unlock()

	wait := time.Until(start)
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Compile-time assertion that Nominatim satisfies the port.
var _ port.Geocoder = (*Nominatim)(nil)
