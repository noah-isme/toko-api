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
// to a loopback, private, link-local, CGNAT, unspecified or multicast address.
var ErrUnsafeAddress = errors.New("outbound: destination address not allowed")

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

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
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnatPrefix.Contains(ip) {
		return fmt.Errorf("%w: %s", ErrUnsafeAddress, address)
	}
	return nil
}
