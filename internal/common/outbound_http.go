package common

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrUnsafeAddress is returned (wrapped) when an outbound connection would go
// to a loopback, private, link-local, CGNAT, reserved, unspecified or multicast
// address, or to an IPv6 transition prefix (NAT64, 6to4, Teredo).
var ErrUnsafeAddress = errors.New("outbound: destination address not allowed")

// blockedPrefixes are non-public ranges the netip predicates do not cover,
// plus IPv6 transition prefixes that can wrap an internal IPv4 address.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),  // CGNAT shared address space
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved, incl. 255.255.255.255
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 well-known prefix
	netip.MustParsePrefix("64:ff9b:1::/48"), // NAT64 local-use prefix
	netip.MustParsePrefix("2002::/16"),      // 6to4 (embeds an IPv4 address)
	netip.MustParsePrefix("2001::/32"),      // Teredo (embeds an IPv4 address)
}

// NewOutboundHTTPClient returns an HTTP client for requests to user-supplied
// URLs (e.g. web push endpoints). Unless allowPrivate is set (tests against
// httptest servers only), every connection is checked after DNS resolution
// and refused when it targets an internal address, which also defeats DNS
// rebinding. Redirects are never followed: the redirect response itself is
// returned to the caller.
func NewOutboundHTTPClient(timeout time.Duration, allowPrivate bool) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	if !allowPrivate {
		dialer.Control = RejectUnsafeAddr
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               nil, // never route user-supplied URLs through an env proxy
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: timeout,
			MaxIdleConns:        16,
			IdleConnTimeout:     90 * time.Second,
			ForceAttemptHTTP2:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// RejectUnsafeAddr is a net.Dialer Control function that refuses connections
// to non-public addresses. The dialer passes the resolved IP, so a host name
// that resolves to an internal address is refused too.
func RejectUnsafeAddr(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnsafeAddress, address)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnsafeAddress, address)
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("%w: %s", ErrUnsafeAddress, address)
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(ip) {
			return fmt.Errorf("%w: %s", ErrUnsafeAddress, address)
		}
	}
	return nil
}
