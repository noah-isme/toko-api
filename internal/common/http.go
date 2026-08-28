package common

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the network peer address for the request.
//
// Forwarded headers are intentionally ignored. They are client-controlled
// unless a deployment has an explicitly configured, trusted proxy boundary;
// trusting them here would let callers spoof rate-limit and audit identities.
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
