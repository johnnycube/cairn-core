package safehttp

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsBlocked(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.8.9.10", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"fd00::1", true},
		{"169.254.169.254", true},
		{"fe80::1", true},
		{"224.0.0.1", true},
		{"ff02::1", true},
		{"ff01::1", true},
		{"0.0.0.0", true},
		{"::", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.0.0.1", true},
		{"0.1.2.3", true},
		{"100.64.0.1", true},
		{"100.100.100.200", true},
		{"100.127.255.254", true},
		{"192.0.0.8", true},
		{"192.0.2.1", true},
		{"192.88.99.1", true},
		{"198.18.0.1", true},
		{"198.19.255.255", true},
		{"198.51.100.7", true},
		{"203.0.113.9", true},
		{"239.255.255.250", true},
		{"240.0.0.1", true},
		{"255.255.255.255", true},
		{"::ffff:169.254.169.254", true},
		{"::ffff:100.100.100.200", true},
		{"::127.0.0.1", true},
		{"64:ff9b::7f00:1", true},
		{"64:ff9b::808:808", true},
		{"64:ff9b:1::a00:1", true},
		{"100::1", true},
		{"2001::1", true},
		{"2001:db8::1", true},
		{"2002:7f00:1::1", true},
		{"2002:a9fe:a9fe::1", true},
		{"3fff::1", true},
		{"5f00::1", true},
		{"fec0::1", true},
		{"ff05::2", true},
		{"8.8.8.8", false},
		{"100.63.255.255", false},
		{"100.128.0.1", false},
		{"198.20.0.1", false},
		{"::ffff:8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.32.0.1", false},
		{"2606:4700:4700::1111", false},
		{"2a00:1450:4001::200e", false},
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("bad test ip %q", tt.ip)
		}
		if got := IsBlocked(ip); got != tt.want {
			t.Errorf("IsBlocked(%s) = %v, want %v", tt.ip, got, tt.want)
		}
		if got := IsBlocked(ip.To16()); got != tt.want {
			t.Errorf("IsBlocked(%s as 16 bytes) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestIsBlockedInvalid(t *testing.T) {
	if !IsBlocked(nil) || !IsBlocked(net.IP{1, 2, 3}) {
		t.Fatal("invalid IP must be blocked")
	}
}

func TestNewClientRefusesLoopback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	c := NewClient(2 * time.Second)
	if c.Timeout != 2*time.Second {
		t.Errorf("timeout = %v", c.Timeout)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected loopback dial to be blocked")
	}
	if !strings.Contains(err.Error(), "blocked address 127.0.0.1") {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server was reached %d times", hits.Load())
	}
}

func TestNewClientRedirectCap(t *testing.T) {
	c := NewClient(time.Second)
	req := httptest.NewRequest(http.MethodGet, "https://example.org/", nil)
	for n := range 3 {
		if err := c.CheckRedirect(req, make([]*http.Request, n)); err != nil {
			t.Fatalf("redirect %d refused: %v", n, err)
		}
	}
	if err := c.CheckRedirect(req, make([]*http.Request, 3)); err == nil || !strings.Contains(err.Error(), "3 redirects") {
		t.Fatalf("4th redirect err = %v", err)
	}
}
