package tenant

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type contextKey string

const tenantContextKey contextKey = "tenant.id"

// Resolver resolves tenant identifiers from HTTP requests using either headers or subdomains.
type Resolver struct {
	HeaderName    string
	RootDomain    string
	DefaultTenant string
}

// NewResolver returns a resolver configured with the provided header name, root domain, and default tenant slug.
// If headerName is empty, "X-Tenant-ID" is used.
func NewResolver(headerName, rootDomain, defaultTenant string) *Resolver {
	if headerName == "" {
		headerName = "X-Tenant-ID"
	}
	return &Resolver{
		HeaderName:    headerName,
		RootDomain:    strings.ToLower(strings.TrimSpace(rootDomain)),
		DefaultTenant: strings.TrimSpace(defaultTenant),
	}
}

// Middleware resolves the tenant from the request and injects it into the context passed downstream.
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	if r == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		tenantID := r.Resolve(req)
		if tenantID == "" {
			tenantID = r.DefaultTenant
		}
		if tenantID != "" {
			ctx := WithTenant(req.Context(), tenantID)
			req = req.WithContext(ctx)
		}
		next.ServeHTTP(w, req)
	})
}

// Resolve attempts to find the tenant identifier from the configured header or the request subdomain.
func (r *Resolver) Resolve(req *http.Request) string {
	if r == nil || req == nil {
		return ""
	}
	if tenantID := strings.TrimSpace(req.Header.Get(r.HeaderName)); tenantID != "" {
		return tenantID
	}

	host := hostWithoutPort(req.Host)
	if host == "" {
		return ""
	}
	subdomain := r.subdomainFromHost(host)
	return strings.TrimSpace(subdomain)
}

func (r *Resolver) subdomainFromHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}

	if r.RootDomain != "" {
		if host == r.RootDomain {
			return ""
		}
		suffix := "." + r.RootDomain
		if strings.HasSuffix(host, suffix) {
			host = strings.TrimSuffix(host, suffix)
		} else {
			return ""
		}
	}

	parts := strings.Split(host, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func hostWithoutPort(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return ""
	}
	if strings.HasPrefix(hostport, "[") {
		if idx := strings.Index(hostport, "]"); idx != -1 {
			host := hostport[1:idx]
			if host != "" {
				return host
			}
		}
	}
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	if idx := strings.Index(hostport, ":"); idx != -1 && strings.Count(hostport, ":") == 1 {
		return hostport[:idx]
	}
	return hostport
}

// WithTenant stores the tenant identifier inside the context.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, tenantContextKey, tenantID)
}

// FromContext extracts the tenant identifier from the context if available.
func FromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	tenantID, ok := ctx.Value(tenantContextKey).(string)
	if !ok {
		return "", false
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", false
	}
	return tenantID, true
}
