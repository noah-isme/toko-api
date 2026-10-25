package tenant

import (
	"strings"
	"time"
	// The runtime image (alpine, see Dockerfile) ships no zoneinfo, so embed
	// the tz database; without it every tenant would silently fall back to UTC.
	_ "time/tzdata"
)

// Location resolves a tenant's IANA timezone name (tenants.timezone). It
// returns time.UTC and false for an empty or unknown name so callers can log
// the fallback instead of failing the request.
func Location(name string) (*time.Location, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.UTC, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC, false
	}
	return loc, true
}
