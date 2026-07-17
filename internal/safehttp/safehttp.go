// Package safehttp builds an http.Client hardened against SSRF: a dial-time
// guard rejects connections to loopback / private / link-local / metadata /
// unspecified / multicast IPs (the actual resolved address, so DNS rebinding
// and redirect hops can't reach internal services). Used for every fetch/POST
// to a remote-supplied URL (webhooks, federation actor/inbox/object fetches).
package safehttp

import (
	"fmt"
	"net"
	"net/http"
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
			ip := net.ParseIP(host)
			if ip == nil || IsBlocked(ip) {
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
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}
