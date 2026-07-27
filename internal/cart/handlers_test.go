package cart

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// The cart response is assembled from map literals, so these guard the two
// pieces that are easy to get wrong: a missing product image must serialise as
// JSON null rather than an empty string, and a present one must round-trip.
func TestNullableTextSerialisesMissingImageAsNull(t *testing.T) {
	payload := map[string]any{"imageUrl": nullableText(pgtype.Text{})}

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `{"imageUrl":null}` {
		t.Fatalf("got %s, want {\"imageUrl\":null}", encoded)
	}
}

func TestNullableTextSerialisesPresentImage(t *testing.T) {
	const url = "https://cdn.example/thumb.jpg"
	payload := map[string]any{"imageUrl": nullableText(pgtype.Text{String: url, Valid: true})}

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded struct {
		ImageURL *string `json:"imageUrl"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ImageURL == nil || *decoded.ImageURL != url {
		t.Fatalf("imageUrl = %v, want %q", decoded.ImageURL, url)
	}
}

// An empty-but-valid text column must stay an empty string, not collapse to
// null: the two mean different things to the storefront.
func TestNullableTextKeepsEmptyStringDistinctFromNull(t *testing.T) {
	got := nullableText(pgtype.Text{String: "", Valid: true})
	if got == nil {
		t.Fatal("valid empty text became null")
	}
	if *got != "" {
		t.Fatalf("got %q, want empty string", *got)
	}
}
