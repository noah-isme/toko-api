package dbgen

import (
	"strings"
	"testing"
)

func TestRecommendationQueriesOrderByComputedRatingAlias(t *testing.T) {
	tests := []struct {
		name             string
		query            string
		orderBy          string
		minOrderByCount  int
		tenantPredicate  string
		minTenantMatches int
	}{
		{
			name:             "frequently bought together",
			query:            getFrequentlyBoughtTogether,
			orderBy:          "ORDER BY opp.pair_count DESC, rating DESC",
			minOrderByCount:  1,
			tenantPredicate:  "WHERE opp.tenant_id = $1",
			minTenantMatches: 1,
		},
		{
			name:             "customers also viewed",
			query:            getCustomersAlsoViewed,
			orderBy:          "ORDER BY COUNT(DISTINCT upv.user_id) DESC, rating DESC",
			minOrderByCount:  1,
			tenantPredicate:  "WHERE upv.tenant_id = $1",
			minTenantMatches: 1,
		},
		{
			name:             "personalized recommendations",
			query:            getPersonalizedRecommendations,
			orderBy:          "ORDER BY rating DESC, p.created_at DESC",
			minOrderByCount:  3,
			tenantPredicate:  "WHERE p.tenant_id = $1",
			minTenantMatches: 3,
		},
		{
			name:             "trending products",
			query:            getTrendingProducts,
			orderBy:          "ORDER BY rating DESC, p.created_at DESC",
			minOrderByCount:  1,
			tenantPredicate:  "WHERE p.tenant_id = $1",
			minTenantMatches: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(tt.query, "p.rating") {
				t.Fatalf("query still references nonexistent products.rating column")
			}
			if count := strings.Count(tt.query, tt.orderBy); count < tt.minOrderByCount {
				t.Fatalf("expected at least %d occurrence(s) of %q, found %d", tt.minOrderByCount, tt.orderBy, count)
			}
			if count := strings.Count(tt.query, tt.tenantPredicate); count < tt.minTenantMatches {
				t.Fatalf("expected at least %d tenant predicate occurrence(s) of %q, found %d", tt.minTenantMatches, tt.tenantPredicate, count)
			}
		})
	}
}
