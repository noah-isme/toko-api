package campaign

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/cart"
)

const rfc3339Message = "startsAt and endsAt must be RFC 3339 timestamps with a timezone (e.g. 2026-12-01T00:00:00Z)"

// uid returns a deterministic UUID string for test fixtures.
func uid(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

func puid(t *testing.T, n int) pgtype.UUID {
	t.Helper()
	id, err := cart.ToUUID(uid(n))
	require.NoError(t, err)
	return id
}

func strp(s string) *string { return &s }
func i32p(v int32) *int32   { return &v }

func TestDecodeDates(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		wantNil bool
	}{
		{name: "zone-less datetime-local", raw: `"2026-10-25T00:00"`, wantErr: true},
		{name: "empty string", raw: `""`, wantErr: true},
		{name: "garbage", raw: `"garbage"`, wantErr: true},
		{name: "UTC with millis", raw: `"2026-10-25T00:00:00.000Z"`},
		{name: "offset", raw: `"2026-10-25T07:00:00+07:00"`},
		{name: "null", raw: `null`, wantNil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in campaignUpdateInput
			err := json.Unmarshal([]byte(`{"startsAt": `+tt.raw+`}`), &in)
			if tt.wantErr {
				require.Error(t, err)
				require.Equal(t, rfc3339Message, decodeError(err))
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				require.Nil(t, in.StartsAt)
				return
			}
			require.NotNil(t, in.StartsAt)
			require.True(t, in.StartsAt.Equal(time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)))
		})
	}

	t.Run("non-date decode errors stay generic", func(t *testing.T) {
		var in campaignUpdateInput
		err := json.Unmarshal([]byte(`{"name": 5}`), &in)
		require.Error(t, err)
		require.Equal(t, "invalid payload", decodeError(err))
	})

	t.Run("create input uses the same message", func(t *testing.T) {
		var in campaignInput
		err := json.Unmarshal([]byte(`{"startsAt": "2026-12-01T00:00"}`), &in)
		require.Error(t, err)
		require.Equal(t, rfc3339Message, decodeError(err))
		require.NoError(t, json.Unmarshal([]byte(`{"startsAt": "2026-12-01T00:00:00Z"}`), &in))
	})
}

func TestValidateUpdateInput(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	current := existingCampaign{
		Name: "Payday", Slug: "payday", Status: "DRAFT",
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(48 * time.Hour),
	}
	past := now.Add(-time.Hour)
	before := now.Add(30 * time.Minute)
	later := now.Add(72 * time.Hour)
	emptyItems := []updateItemInput{}

	tests := []struct {
		name    string
		in      campaignUpdateInput
		current existingCampaign
		want    *existingCampaign
		wantErr string
	}{
		{name: "no fields", in: campaignUpdateInput{}, wantErr: "no updatable fields provided"},
		{
			name: "status only ENDED",
			in:   campaignUpdateInput{Status: strp("ENDED")},
			want: &existingCampaign{Name: "Payday", Slug: "payday", Status: "ENDED", StartsAt: current.StartsAt, EndsAt: current.EndsAt},
		},
		{
			name: "status is normalized",
			in:   campaignUpdateInput{Status: strp(" active ")},
			want: &existingCampaign{Name: "Payday", Slug: "payday", Status: "ACTIVE", StartsAt: current.StartsAt, EndsAt: current.EndsAt},
		},
		{name: "empty status", in: campaignUpdateInput{Status: strp("")}, wantErr: "unsupported campaign status"},
		{name: "blank status", in: campaignUpdateInput{Status: strp(" ")}, wantErr: "unsupported campaign status"},
		{name: "unknown status", in: campaignUpdateInput{Status: strp("PAUSED")}, wantErr: "unsupported campaign status"},
		{name: "empty name", in: campaignUpdateInput{Name: strp("  ")}, wantErr: "name cannot be empty"},
		{name: "empty slug", in: campaignUpdateInput{Slug: strp("")}, wantErr: "slug cannot be empty"},
		{
			name: "name and slug trimmed",
			in:   campaignUpdateInput{Name: strp("  Mega  "), Slug: strp(" mega ")},
			want: &existingCampaign{Name: "Mega", Slug: "mega", Status: "DRAFT", StartsAt: current.StartsAt, EndsAt: current.EndsAt},
		},
		{name: "endsAt before merged startsAt", in: campaignUpdateInput{EndsAt: &before}, wantErr: "startsAt and endsAt must define a valid range"},
		{name: "startsAt after merged endsAt", in: campaignUpdateInput{StartsAt: &later}, wantErr: "startsAt and endsAt must define a valid range"},
		{
			name: "both dates merged",
			in:   campaignUpdateInput{StartsAt: &before, EndsAt: &later},
			want: &existingCampaign{Name: "Payday", Slug: "payday", Status: "DRAFT", StartsAt: before, EndsAt: later},
		},
		{
			name:    "scheduled with past endsAt",
			in:      campaignUpdateInput{Status: strp("SCHEDULED"), StartsAt: &[]time.Time{now.Add(-2 * time.Hour)}[0], EndsAt: &past},
			wantErr: "endsAt must be in the future for a scheduled or active campaign",
		},
		{
			name:    "active campaign stays active but endsAt moved to the past",
			current: existingCampaign{Name: "x", Slug: "x", Status: "ACTIVE", StartsAt: now.Add(-3 * time.Hour), EndsAt: now.Add(time.Hour)},
			in:      campaignUpdateInput{EndsAt: &past},
			wantErr: "endsAt must be in the future for a scheduled or active campaign",
		},
		{
			name:    "ended campaign with past dates stays editable",
			current: existingCampaign{Name: "x", Slug: "x", Status: "ENDED", StartsAt: now.Add(-3 * time.Hour), EndsAt: past},
			in:      campaignUpdateInput{Name: strp("y")},
			want:    &existingCampaign{Name: "y", Slug: "x", Status: "ENDED", StartsAt: now.Add(-3 * time.Hour), EndsAt: past},
		},
		{
			name: "items only counts as a field",
			in:   campaignUpdateInput{Items: &emptyItems},
			want: &current,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cur := current
			if tt.current.Name != "" {
				cur = tt.current
			}
			got, err := validateUpdateInput(tt.in, cur, now)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Equal(t, tt.wantErr, err.Error())
				var verr *validationError
				require.ErrorAs(t, err, &verr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, *tt.want, got)
		})
	}
}

// ids maps planned updates to their item fixture numbers for readable asserts.
func updateOrder(t *testing.T, plan itemPlan, names map[string]string) []string {
	t.Helper()
	out := make([]string, 0, len(plan.Update))
	for _, u := range plan.Update {
		out = append(out, names[cart.UUIDString(u.ItemID)])
	}
	return out
}

func TestPlanItemChanges(t *testing.T) {
	// Items 101..104, products 1..9.
	item := func(n, product int, sold int32) existingItem {
		return existingItem{ID: puid(t, n), ProductID: puid(t, product), SalePrice: 100, SoldCount: sold}
	}
	req := func(id, product int, price int64, limit *int32) updateItemInput {
		in := updateItemInput{ProductID: uid(product), SalePrice: price, StockLimit: limit}
		if id != 0 {
			in.ID = strp(uid(id))
		}
		return in
	}
	names := map[string]string{uid(101): "A", uid(102): "B", uid(103): "C", uid(104): "D"}

	t.Run("empty items rejected", func(t *testing.T) {
		_, err := planItemChanges([]existingItem{item(101, 1, 0)}, []updateItemInput{})
		require.EqualError(t, err, "at least one item is required")
	})

	validation := []struct {
		name     string
		existing []existingItem
		req      []updateItemInput
		wantErr  string
	}{
		{name: "invalid productId", req: []updateItemInput{{ProductID: "nope"}}, wantErr: "item productId is invalid"},
		{name: "invalid id", req: []updateItemInput{{ID: strp("nope"), ProductID: uid(1)}}, wantErr: "item id is invalid"},
		{name: "duplicate productId", req: []updateItemInput{req(0, 1, 10, nil), req(0, 1, 20, nil)}, wantErr: "duplicate productId in items"},
		{
			name:     "duplicate id",
			existing: []existingItem{item(101, 1, 0)},
			req:      []updateItemInput{req(101, 1, 10, nil), req(101, 2, 20, nil)},
			wantErr:  "duplicate item id in items",
		},
		{
			name:     "foreign id",
			existing: []existingItem{item(101, 1, 0)},
			req:      []updateItemInput{req(199, 1, 10, nil)},
			wantErr:  "item " + uid(199) + " does not belong to this campaign",
		},
		{name: "negative salePrice", req: []updateItemInput{req(0, 1, -1, nil)}, wantErr: "salePrice cannot be negative"},
		{name: "stockLimit zero", req: []updateItemInput{req(0, 1, 10, i32p(0))}, wantErr: "stockLimit must be at least 1 or null"},
		{name: "stockLimit negative", req: []updateItemInput{req(0, 1, 10, i32p(-3))}, wantErr: "stockLimit must be at least 1 or null"},
		{
			name:     "stockLimit below soldCount",
			existing: []existingItem{item(101, 1, 2)},
			req:      []updateItemInput{req(101, 1, 10, i32p(1))},
			wantErr:  "stockLimit cannot be below soldCount",
		},
		{
			name:     "product change with sales",
			existing: []existingItem{item(101, 1, 2)},
			req:      []updateItemInput{req(101, 2, 10, nil)},
			wantErr:  "cannot change the product of an item that already has sales",
		},
		{
			name:     "two-item swap",
			existing: []existingItem{item(101, 1, 0), item(102, 2, 0)},
			req:      []updateItemInput{req(101, 2, 10, nil), req(102, 1, 10, nil)},
			wantErr:  "swapping products between existing items in one request is not supported; remove one and add it again",
		},
		{
			name:     "three-item rotation",
			existing: []existingItem{item(101, 1, 0), item(102, 2, 0), item(103, 3, 0)},
			req:      []updateItemInput{req(101, 2, 10, nil), req(102, 3, 10, nil), req(103, 1, 10, nil)},
			wantErr:  "swapping products between existing items in one request is not supported; remove one and add it again",
		},
	}
	for _, tt := range validation {
		t.Run(tt.name, func(t *testing.T) {
			_, err := planItemChanges(tt.existing, tt.req)
			require.Error(t, err)
			require.Equal(t, tt.wantErr, err.Error())
			var verr *validationError
			require.ErrorAs(t, err, &verr)
			require.False(t, errors.Is(err, errItemHasSales))
		})
	}

	t.Run("stockLimit equal to soldCount and price change with sales allowed", func(t *testing.T) {
		plan, err := planItemChanges([]existingItem{item(101, 1, 2)}, []updateItemInput{req(101, 1, 50, i32p(2))})
		require.NoError(t, err)
		require.Len(t, plan.Update, 1)
		require.Equal(t, int64(50), plan.Update[0].SalePrice)
		require.Equal(t, int32(2), *plan.Update[0].StockLimit)
	})

	t.Run("delete with sales", func(t *testing.T) {
		_, err := planItemChanges([]existingItem{item(101, 1, 0), item(102, 2, 1)}, []updateItemInput{req(101, 1, 10, nil)})
		require.ErrorIs(t, err, errItemHasSales)
		var verr *validationError
		require.ErrorAs(t, err, &verr)
		require.Equal(t, uid(102), verr.ItemID)
		require.Equal(t, uid(102), verr.details()["itemId"])
	})

	t.Run("mixed reorder insert update delete", func(t *testing.T) {
		existing := []existingItem{item(101, 1, 0), item(102, 2, 3), item(103, 3, 0)}
		plan, err := planItemChanges(existing, []updateItemInput{
			req(0, 9, 70, nil),       // insert
			req(102, 2, 80, i32p(5)), // update with sales, same product
			req(101, 1, 90, nil),     // update, listed after
		})
		require.NoError(t, err)
		require.Len(t, plan.Delete, 1)
		require.Equal(t, puid(t, 103), plan.Delete[0].ID)
		require.Equal(t, []string{"B", "A"}, updateOrder(t, plan, names))
		require.Len(t, plan.Insert, 1)
		require.Equal(t, uid(9), plan.Insert[0].ProductID)
		require.Nil(t, plan.Insert[0].ID)
	})

	t.Run("product-move chain in both request orders", func(t *testing.T) {
		existing := []existingItem{item(101, 1, 0), item(102, 2, 0), item(103, 3, 0)}
		forward := []updateItemInput{req(101, 2, 10, nil), req(102, 3, 10, nil), req(103, 4, 10, nil)}
		reverse := []updateItemInput{forward[2], forward[1], forward[0]}
		for _, requested := range [][]updateItemInput{forward, reverse} {
			plan, err := planItemChanges(existing, requested)
			require.NoError(t, err)
			require.Equal(t, []string{"C", "B", "A"}, updateOrder(t, plan, names))
			require.Empty(t, plan.Delete)
			require.Empty(t, plan.Insert)
		}
	})

	t.Run("move onto product of a deleted item", func(t *testing.T) {
		existing := []existingItem{item(101, 1, 0), item(102, 2, 0)}
		plan, err := planItemChanges(existing, []updateItemInput{req(101, 2, 10, nil)})
		require.NoError(t, err)
		require.Len(t, plan.Delete, 1)
		require.Equal(t, puid(t, 102), plan.Delete[0].ID)
		require.Equal(t, []string{"A"}, updateOrder(t, plan, names))
		require.Equal(t, puid(t, 2), plan.Update[0].ProductID)
	})

	t.Run("insert onto product vacated by an update", func(t *testing.T) {
		existing := []existingItem{item(101, 1, 0)}
		plan, err := planItemChanges(existing, []updateItemInput{req(101, 2, 10, nil), req(0, 1, 10, nil)})
		require.NoError(t, err)
		require.Len(t, plan.Update, 1)
		require.Len(t, plan.Insert, 1)
	})
}

func TestValidateInputRejectsDuplicateProduct(t *testing.T) {
	base := campaignInput{
		Name: "Payday", Slug: "payday",
		StartsAt: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC),
	}
	tests := []struct {
		name    string
		items   []itemInput
		wantErr string
	}{
		{name: "distinct products", items: []itemInput{{ProductID: uid(1), SalePrice: 10}, {ProductID: uid(2), SalePrice: 10}}},
		{name: "duplicate product", items: []itemInput{{ProductID: uid(1), SalePrice: 10}, {ProductID: uid(1), SalePrice: 20}}, wantErr: "duplicate productId in items"},
		{name: "duplicate product differing in case", items: []itemInput{{ProductID: uid(1)}, {ProductID: strings.ToUpper(uid(1))}}, wantErr: "duplicate productId in items"},
		{name: "no items", items: nil, wantErr: "at least one item is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			in.Items = tt.items
			err := validateInput(in)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestCheckItemPrices(t *testing.T) {
	prices := map[[16]byte]int64{puid(t, 1).Bytes: 1000}
	tests := []struct {
		name        string
		item        productPrice
		wantErr     string
		wantDetails map[string]any
	}{
		{name: "below price", item: productPrice{ProductID: puid(t, 1), SalePrice: 999}},
		{
			name:        "equal to price",
			item:        productPrice{ItemID: puid(t, 101), ProductID: puid(t, 1), SalePrice: 1000},
			wantErr:     "salePrice must be lower than the product price",
			wantDetails: map[string]any{"field": "salePrice", "itemId": uid(101), "productId": uid(1), "salePrice": int64(1000), "productPrice": int64(1000)},
		},
		{
			name:        "product of another tenant or missing",
			item:        productPrice{ProductID: puid(t, 2), SalePrice: 1},
			wantErr:     "item productId is invalid",
			wantDetails: map[string]any{"field": "productId", "productId": uid(2)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkItemPrices([]productPrice{tt.item}, prices)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			var verr *validationError
			require.ErrorAs(t, err, &verr)
			require.Equal(t, tt.wantErr, verr.Message)
			require.Equal(t, tt.wantDetails, verr.details())
		})
	}
}

func TestWriteCampaignError(t *testing.T) {
	rawPG := "duplicate key value violates unique constraint \"secret_internal\""
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{name: "not found", err: errCampaignNotFound, wantStatus: 404, wantCode: "NOT_FOUND", wantMsg: "campaign not found"},
		{name: "validation", err: &validationError{Message: "duplicate productId in items"}, wantStatus: 400, wantCode: "BAD_REQUEST", wantMsg: "duplicate productId in items"},
		{name: "item has sales", err: &validationError{Message: "item has sales and cannot be removed", ItemID: uid(101), err: errItemHasSales}, wantStatus: 409, wantCode: "CONFLICT", wantMsg: "item has sales and cannot be removed"},
		{name: "slug taken", err: fmt.Errorf("updating campaign: %w", &pgconn.PgError{Code: "23505", ConstraintName: "flash_sale_campaigns_tenant_id_slug_key", Message: rawPG}), wantStatus: 409, wantCode: "CONFLICT", wantMsg: "campaign slug already exists"},
		{name: "product duplicated", err: &pgconn.PgError{Code: "23505", ConstraintName: "flash_sale_items_campaign_id_product_id_key", Message: rawPG}, wantStatus: 409, wantCode: "CONFLICT", wantMsg: "product is already in this campaign"},
		{name: "reservation fk", err: &pgconn.PgError{Code: "23503", ConstraintName: "flash_sale_order_items_flash_sale_item_id_fkey", Message: rawPG}, wantStatus: 409, wantCode: "CONFLICT", wantMsg: "item has active reservations"},
		{name: "check violation", err: &pgconn.PgError{Code: "23514", ConstraintName: "flash_sale_items_stock_limit_check", Message: rawPG}, wantStatus: 400, wantCode: "BAD_REQUEST", wantMsg: "campaign violates a constraint"},
		{name: "lock timeout", err: &pgconn.PgError{Code: "55P03", Message: rawPG}, wantStatus: 409, wantCode: "CONFLICT", wantMsg: "campaign is busy, retry"},
		{name: "deadlock", err: &pgconn.PgError{Code: "40P01", Message: rawPG}, wantStatus: 409, wantCode: "CONFLICT", wantMsg: "campaign is busy, retry"},
		{name: "other unique", err: &pgconn.PgError{Code: "23505", ConstraintName: "something_else", Message: rawPG}, wantStatus: 500, wantCode: "INTERNAL", wantMsg: "failed to update campaign"},
		{name: "plain error", err: errors.New(rawPG), wantStatus: 500, wantCode: "INTERNAL", wantMsg: "failed to update campaign"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeCampaignError(rec, tt.err, "failed to update campaign")
			require.Equal(t, tt.wantStatus, rec.Code)
			require.NotContains(t, rec.Body.String(), "secret_internal")
			var body struct {
				Error struct {
					Code    string         `json:"code"`
					Message string         `json:"message"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Equal(t, tt.wantCode, body.Error.Code)
			require.Equal(t, tt.wantMsg, body.Error.Message)
			if tt.name == "item has sales" {
				require.Equal(t, uid(101), body.Error.Details["itemId"])
			}
		})
	}
}

func TestAdminUpdateWithoutPool(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/flash-sales/"+uid(1), strings.NewReader(`{"status":"ACTIVE"}`))
	(&Handler{}).AdminUpdate(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
