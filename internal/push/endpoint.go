package push

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// ErrInvalidEndpoint is returned when a subscription endpoint is not an https
// URL of an allowed push service.
var ErrInvalidEndpoint = errors.New("push: invalid subscription endpoint")

// ValidateEndpoint checks a subscription endpoint before it is stored. The
// server later POSTs to this URL, so it must be an absolute https URL without
// credentials whose host is a DNS name (no IP literals) on the allowlist.
// Allowlist entries match exactly, or as a suffix when they start with ".".
// The single entry "*" disables the host allowlist but keeps the other rules.
func ValidateEndpoint(raw string, allowedHosts []string) error {
	u, err := parseHTTPS(raw)
	if err != nil {
		return err
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials are not allowed", ErrInvalidEndpoint)
	}
	host := strings.ToLower(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil {
		return fmt.Errorf("%w: IP address hosts are not allowed", ErrInvalidEndpoint)
	}
	if len(allowedHosts) == 1 && allowedHosts[0] == "*" {
		return nil
	}
	for _, allowed := range allowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		if strings.HasPrefix(allowed, ".") {
			if strings.HasSuffix(host, allowed) && len(host) > len(allowed) {
				return nil
			}
			continue
		}
		if host == allowed {
			return nil
		}
	}
	return fmt.Errorf("%w: host is not a known push service", ErrInvalidEndpoint)
}

// SendableEndpoint is the send-time check for stored rows (including rows
// stored before validation existed): the endpoint must be an absolute https URL.
func SendableEndpoint(raw string) error {
	_, err := parseHTTPS(raw)
	return err
}

func parseHTTPS(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidEndpoint)
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return nil, fmt.Errorf("%w: not an absolute URL", ErrInvalidEndpoint)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme must be https", ErrInvalidEndpoint)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidEndpoint)
	}
	return u, nil
}

// endpointHost returns the lower-cased host of an endpoint for logs and
// redacted errors. The path and query carry the subscription token and must
// never be logged.
func endpointHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return "invalid-endpoint"
	}
	return strings.ToLower(u.Hostname())
}
