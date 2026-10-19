package geocode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencairn-org/cairn-core/internal/config"
	"github.com/opencairn-org/cairn-core/internal/port"
)

func newTestGeocoder(t *testing.T, h http.HandlerFunc, cfg config.GeocoderConfig) (*Nominatim, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg.URL = srv.URL + "/"
	if cfg.MinInterval == 0 {
		cfg.MinInterval = time.Microsecond
	}
	return New(cfg), &hits
}

func TestNewDefaults(t *testing.T) {
	n := New(config.GeocoderConfig{URL: "https://geo.example/"})
	if n.baseURL != "https://geo.example" {
		t.Errorf("baseURL = %q", n.baseURL)
	}
	if !strings.HasPrefix(n.userAgent, "Cairn ") || n.email != "" {
		t.Errorf("userAgent = %q email = %q", n.userAgent, n.email)
	}
	if n.limiter.interval != time.Second || n.client.Timeout != 10*time.Second {
		t.Errorf("interval = %v timeout = %v", n.limiter.interval, n.client.Timeout)
	}

	n = New(config.GeocoderConfig{UserAgent: "ua", Email: "a@b.c", MinInterval: time.Minute, Timeout: time.Second})
	if n.userAgent != "ua - a@b.c" || n.email != "a@b.c" {
		t.Errorf("userAgent = %q email = %q", n.userAgent, n.email)
	}
	if n.limiter.interval != time.Minute || n.client.Timeout != time.Second {
		t.Errorf("interval = %v timeout = %v", n.limiter.interval, n.client.Timeout)
	}
}

func TestReverseGeocodeSuccessAndCache(t *testing.T) {
	n, hits := newTestGeocoder(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path != "/reverse":
			http.Error(w, "path "+r.URL.Path, http.StatusNotFound)
		case q.Get("format") != "jsonv2" || q.Get("zoom") != "10" || q.Get("addressdetails") != "1":
			http.Error(w, "bad query", http.StatusBadRequest)
		case q.Get("lat") != "48.137100" || q.Get("lon") != "11.575400" || q.Get("email") != "ops@example.org":
			http.Error(w, "bad coords/email", http.StatusBadRequest)
		case r.Header.Get("User-Agent") != "test-ua - ops@example.org" || r.Header.Get("Accept") != "application/json":
			http.Error(w, "bad headers", http.StatusBadRequest)
		default:
			_, _ = w.Write([]byte(`{"address":{"town":" ","city":"München","country":"Deutschland"}}`))
		}
	}, config.GeocoderConfig{UserAgent: "test-ua", Email: "ops@example.org"})

	ctx := context.Background()
	got, err := n.ReverseGeocode(ctx, 48.1371, 11.5754)
	if err != nil {
		t.Fatal(err)
	}
	if got != (port.Place{Name: "München", Country: "Deutschland"}) {
		t.Fatalf("place = %+v", got)
	}
	// rounds to the same 3-decimal cache key, so no second request
	again, err := n.ReverseGeocode(ctx, 48.13712, 11.57541)
	if err != nil || again != got {
		t.Fatalf("cached = %+v, %v", again, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}

func TestReverseGeocodeNoPlaceIsCached(t *testing.T) {
	n, hits := newTestGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":"Unable to geocode"}`))
	}, config.GeocoderConfig{})

	for range 2 {
		got, err := n.ReverseGeocode(context.Background(), 0, 0)
		if !errors.Is(err, port.ErrNoPlace) || got.Name != "" {
			t.Fatalf("got %+v, %v; want ErrNoPlace", got, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}

func TestReverseGeocodeAddressWithoutPlace(t *testing.T) {
	n, _ := newTestGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"address":{"country":"Atlantis"}}`))
	}, config.GeocoderConfig{})
	got, err := n.ReverseGeocode(context.Background(), 1, 2)
	if !errors.Is(err, port.ErrNoPlace) || got.Country != "Atlantis" {
		t.Fatalf("got %+v, %v; want ErrNoPlace", got, err)
	}
}

func TestReverseGeocodeErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantMsg string
	}{
		{"rate limited", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}, "unexpected status 429"},
		{"server error", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}, "unexpected status 500"},
		{"bad json", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{not json`))
		}, "geocode: decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, hits := newTestGeocoder(t, tt.handler, config.GeocoderConfig{})
			for range 2 {
				_, err := n.ReverseGeocode(context.Background(), 1, 2)
				if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
					t.Fatalf("err = %v, want %q", err, tt.wantMsg)
				}
			}
			// failures must not be cached
			if hits.Load() != 2 {
				t.Fatalf("hits = %d, want 2", hits.Load())
			}
		})
	}
}

func TestReverseGeocodeTransportAndBuildErrors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	n := New(config.GeocoderConfig{URL: url, MinInterval: time.Microsecond})
	if _, err := n.ReverseGeocode(context.Background(), 1, 2); err == nil || !strings.Contains(err.Error(), "geocode: request") {
		t.Fatalf("err = %v", err)
	}

	n = New(config.GeocoderConfig{URL: "http://bad host", MinInterval: time.Microsecond})
	if _, err := n.ReverseGeocode(context.Background(), 1, 2); err == nil || !strings.Contains(err.Error(), "geocode: build request") {
		t.Fatalf("err = %v", err)
	}
}

func TestReverseGeocodeLimiterHonoursContext(t *testing.T) {
	n, hits := newTestGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"address":{"village":"A"}}`))
	}, config.GeocoderConfig{MinInterval: time.Hour})

	if _, err := n.ReverseGeocode(context.Background(), 1, 1); err != nil {
		t.Fatal(err)
	}
	// next slot is an hour away; a cancelled context must return immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.ReverseGeocode(ctx, 2, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}

func TestReservingLimiterSpacesCalls(t *testing.T) {
	l := &reservingLimiter{interval: 3 * time.Millisecond}
	start := time.Now()
	for range 3 {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// first call is free, the next two wait one interval each
	if elapsed := time.Since(start); elapsed < 6*time.Millisecond {
		t.Fatalf("elapsed %v, want >= 6ms", elapsed)
	}
}

func TestPickPlaceName(t *testing.T) {
	tests := []struct {
		addr map[string]string
		want string
	}{
		{nil, ""},
		{map[string]string{}, ""},
		{map[string]string{"country": "X"}, ""},
		{map[string]string{"state": "Bayern", "county": "Landkreis"}, "Landkreis"},
		{map[string]string{"hamlet": "H", "village": "V"}, "V"},
		{map[string]string{"city": "  ", "suburb": " S "}, "S"},
		{map[string]string{"city": "C", "town": "T"}, "C"},
	}
	for _, tt := range tests {
		if got := pickPlaceName(tt.addr); got != tt.want {
			t.Errorf("pickPlaceName(%v) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}

func TestCacheKey(t *testing.T) {
	if got := cacheKey(48.13712, -11.57549); got != "48.137,-11.575" {
		t.Fatalf("cacheKey = %q", got)
	}
}
