package catalog

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeSort(t *testing.T) {
	cases := map[string]string{
		"":                "",
		" price:ASC ":     "price:asc",
		"title:desc":      "title:desc",
		"popularity":      "popularity:desc",
		"popularity:desc": "popularity:desc",
		"popularity:asc":  "",
		"rating":          "rating:desc",
		"Rating:Desc":     "rating:desc",
		"newest":          "",
	}
	for in, want := range cases {
		require.Equal(t, want, normalizeSort(in), "normalizeSort(%q)", in)
	}
}

func TestListCacheKeySkipsFacetRequests(t *testing.T) {
	svc := &Service{cache: &Cache{}, defaultPage: 1, defaultLimit: 20}
	ctx := context.Background()

	_, ok := svc.listCacheKey(ctx, ListParams{Page: 1, Limit: 20})
	require.True(t, ok, "default listing is cacheable")

	_, ok = svc.listCacheKey(ctx, ListParams{Page: 1, Limit: 20, IncludeFacets: true})
	require.False(t, ok, "include=facets must bypass the cache")

	_, ok = svc.listCacheKey(ctx, ListParams{Page: 1, Limit: 20, Sort: "popularity:desc"})
	require.False(t, ok, "sorted listing is not cached")
}
