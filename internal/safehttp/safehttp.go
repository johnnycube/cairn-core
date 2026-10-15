// Package safehttp builds an http.Client hardened against SSRF: a dial-time
// guard rejects connections to loopback / private / link-local / metadata /
// unspecified / multicast / IANA special-purpose IPs (the actual resolved
// address, so DNS rebinding and redirect hops can't reach internal services). Used for every fetch/POST
// to a remote-supplied URL (webhooks, federation actor/inbox/object fetches).
package safehttp

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// NewClient returns a client whose dialer blocks internal addresses and caps
// redirects at 3.
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || IsBlockedAddr(ip.WithZone("")) {
				return fmt.Errorf("blocked address %s", host)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("stopped after 3 redirects")
			}
			return nil
		},
	}
}

// IsBlocked reports whether an IP must never be reached from a remote-URL fetch.
func IsBlocked(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	return !ok || IsBlockedAddr(addr)
}

// globalUnicast6 is the only IPv6 space IANA allocates for global unicast.
var globalUnicast6 = netip.MustParsePrefix("2000::/3")

// blockedPrefixes are IANA special-purpose ranges the netip predicates miss.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT, incl. 100.100.100.200 metadata
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, incl. broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 well-known
	netip.MustParsePrefix("64:ff9b:1::/48"),  // NAT64 local-use
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments, incl. Teredo
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4, embeds arbitrary IPv4
	netip.MustParsePrefix("3fff::/20"),       // documentation
	netip.MustParsePrefix("5f00::/16"),       // SRv6 SIDs
}

// IsBlockedAddr is IsBlocked for netip; IPv4-mapped IPv6 is judged as IPv4.
func IsBlockedAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return true
	}
	if addr.Is6() && !globalUnicast6.Contains(addr) {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
