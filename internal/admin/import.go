package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/security"
)

// CSV import of products and stock (POST /api/v1/admin/products/import).
//
// The import is all-or-nothing and runs in two phases inside one transaction:
// the plan phase resolves every row (identity, slugs, categories), locks the
// rows it will touch and computes the planned writes (the response rows[]);
// the apply phase runs only for dryRun=false with zero rejected rows. A dry run
// always rolls back. See docs/contracts/admin.md 6.27 for the full rules.

const (
	importMaxFileBytes = 5 << 20
	importMaxBodyBytes = importMaxFileBytes + 64<<10 // multipart envelope

	// ImportRoutePath is where cmd/api mounts ImportProductsCSV. It must match
	// the router; BodyLimitOverrides uses it to lift the global body cap.
	ImportRoutePath    = "/api/v1/admin/products/import"
	importMaxRows      = 5000
	importSlugAttempts = 20

	importTypeProducts = "products"
	importTypeStock    = "stock"

	importActionCreate        = "create"
	importActionCreateVariant = "create_variant"
	importActionUpdate        = "update"
	importActionUnchanged     = "unchanged"
	importActionReject        = "reject"
)

// BodyLimitOverrides lists the admin catalog routes whose payload cap differs
// from the global security.BodyLimit (SECURITY_BODY_LIMIT_BYTES, 1 MiB by
// default). The import accepts a 5 MiB file, so the global middleware must
// not buffer or reject it; readImportFile enforces the cap with a JSON
// PAYLOAD_TOO_LARGE error instead.
func BodyLimitOverrides() []security.BodyLimitOverride {
	return []security.BodyLimitOverride{
		{Method: http.MethodPost, Path: ImportRoutePath, Max: importMaxBodyBytes},
	}
}

// Row error codes reported in rows[].errors[].code.
const (
	importCodeMissing              = "MISSING"
	importCodeInvalidRow           = "INVALID_ROW"
	importCodeInvalidNumber        = "INVALID_NUMBER"
	importCodeInvalidUUID          = "INVALID_UUID"
	importCodeNegative             = "NEGATIVE"
	importCodeOutOfRange           = "OUT_OF_RANGE"
	importCodeInvalidStatus        = "INVALID_STATUS"
	importCodeInvalidJSON          = "INVALID_JSON"
	importCodeInvalidSlug          = "INVALID_SLUG"
	importCodeDuplicateSKU         = "DUPLICATE_SKU"
	importCodeDuplicateVariant     = "DUPLICATE_VARIANT"
	importCodeSKUTaken             = "SKU_TAKEN"
	importCodeSKUMismatch          = "SKU_MISMATCH"
	importCodeProductMismatch      = "PRODUCT_MISMATCH"
	importCodeSlugTaken            = "SLUG_TAKEN"
	importCodeUnknownSKU           = "UNKNOWN_SKU"
	importCodeUnknownVariant       = "UNKNOWN_VARIANT"
	importCodeUnknownProduct       = "UNKNOWN_PRODUCT"
	importCodeMissingForCreate     = "MISSING_FOR_CREATE"
	importCodeUnknownCategory      = "UNKNOWN_CATEGORY"
	importCodeUnknownBrand         = "UNKNOWN_BRAND"
	importCodeConflictingValues    = "CONFLICTING_PRODUCT_VALUES"
	importCodeNoVariant            = "NO_VARIANT"
	importCodeStaleStock           = "STALE_STOCK"
	importCodeStockBaselineMissing = "STOCK_BASELINE_REQUIRED"
)

// Header sets per import type. type=products reads the export's identity
// columns and ignores its derived columns, so an exported file round-trips.
var (
	importProductsRequired = []string{"sku"}
	importProductsOptional = []string{
		"variant_id", "product_id", "title", "slug", "status", "price", "compare_at", "variant_price",
		"stock", "stock_baseline", "low_stock_threshold", "category_slug", "brand_slug", "description", "attributes",
	}
	importProductsIgnored = []string{"in_stock", "stock_status", "created_at", "updated_at"}
	importStockRequired   = []string{"sku", "stock"}
	importStockOptional   = []string{"variant_id", "stock_baseline"}

	// Columns that describe the product (shared by every row of a product).
	importProductColumns = []string{"title", "slug", "status", "price", "compare_at", "low_stock_threshold", "category_slug", "brand_slug", "description"}
	// Columns that describe one variant.
	importVariantColumns = []string{"variant_price", "stock", "stock_baseline", "attributes"}
)

type importFieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type importChange struct {
	From any `json:"from"`
	To   any `json:"to"`
}

type importRowResult struct {
	Row       int                     `json:"row"`
	SKU       string                  `json:"sku"`
	VariantID string                  `json:"variantId,omitempty"`
	ProductID string                  `json:"productId,omitempty"`
	Slug      string                  `json:"slug,omitempty"`
	Action    string                  `json:"action"`
	Changes   map[string]importChange `json:"changes"`
	Errors    []importFieldError      `json:"errors"`
}

type importSummary struct {
	Total         int `json:"total"`
	Create        int `json:"create"`
	CreateVariant int `json:"createVariant"`
	Update        int `json:"update"`
	Unchanged     int `json:"unchanged"`
	Rejected      int `json:"rejected"`
}

type importResult struct {
	DryRun  bool              `json:"dryRun"`
	Type    string            `json:"type"`
	Summary importSummary     `json:"summary"`
	Rows    []importRowResult `json:"rows"`
}

// importInt is a parsed integer cell; set=false means the column is absent or
// the cell is empty.
type importInt struct {
	set   bool
	value int64
}

type importRow struct {
	res    *importRowResult
	fields map[string]string // present columns -> unescaped cell (identifiers trimmed)

	sku       string
	variantID pgtype.UUID
	productID pgtype.UUID

	price, compareAt, variantPrice, stock, stockBaseline, threshold importInt
	attributes                                                      []byte // canonical JSON object, nil when empty
	status                                                          string
	slug                                                            string // slugified slug cell

	kind      string // update | product | create_variant | create
	variant   *dbgen.AdminLockVariantsByIDsRow
	product   *importProduct
	group     *importGroup
	assignSKU bool

	// planned variant writes (update rows)
	newVariantPrice pgtype.Int8
	newStock        pgtype.Int4
	newAttributes   []byte
	newSKU          pgtype.Text
}

func (r *importRow) has(col string) bool { _, ok := r.fields[col]; return ok }

func (r *importRow) cell(col string) string { return r.fields[col] }

func (r *importRow) rejected() bool { return len(r.res.Errors) > 0 }

func (r *importRow) fail(field, code, message string) {
	r.res.Errors = append(r.res.Errors, importFieldError{Field: field, Code: code, Message: message})
}

func (r *importRow) change(field string, from, to any) {
	r.res.Changes[field] = importChange{From: from, To: to}
}

// importProduct is an existing product touched by the file.
type importProduct struct {
	locked  dbgen.AdminGetProductsByIDsForUpdateRow
	rows    []*importRow
	params  dbgen.AdminUpdateProductParams
	update  bool
	newSlug string
	price   int64 // effective price after this import (create_variant default)
	events  []catalog.ProductChange
}

// importGroup is a new product created by one or more rows.
type importGroup struct {
	key      string
	base     string
	explicit bool
	slug     string
	rows     []*importRow
}

// importQuerier is what the plan phase reads (all tenant-scoped).
type importQuerier interface {
	AdminFindVariantsForImport(ctx context.Context, arg dbgen.AdminFindVariantsForImportParams) ([]dbgen.AdminFindVariantsForImportRow, error)
	AdminExistingSKUsAnyTenant(ctx context.Context, skus []string) ([]string, error)
	AdminFindProductsForImport(ctx context.Context, arg dbgen.AdminFindProductsForImportParams) ([]dbgen.AdminFindProductsForImportRow, error)
	AdminSlugsTakenByOtherTenants(ctx context.Context, arg dbgen.AdminSlugsTakenByOtherTenantsParams) ([]string, error)
	AdminGetProductsByIDsForUpdate(ctx context.Context, arg dbgen.AdminGetProductsByIDsForUpdateParams) ([]dbgen.AdminGetProductsByIDsForUpdateRow, error)
	AdminLockVariantsByIDs(ctx context.Context, arg dbgen.AdminLockVariantsByIDsParams) ([]dbgen.AdminLockVariantsByIDsRow, error)
	AdminListCategories(ctx context.Context, tenantID pgtype.UUID) ([]dbgen.AdminListCategoriesRow, error)
	AdminListBrands(ctx context.Context, tenantID pgtype.UUID) ([]dbgen.AdminListBrandsRow, error)
}

type importPlan struct {
	typ      string
	tenantID pgtype.UUID
	columns  map[string]int
	rows     []*importRow
	products map[[16]byte]*importProduct
	order    []*importProduct
	groups   []*importGroup

	categoryBySlug map[string]pgtype.UUID
	categorySlugBy map[[16]byte]string
	brandBySlug    map[string]pgtype.UUID
	brandSlugBy    map[[16]byte]string
}

func importHeaders(typ string) (required, optional, ignored []string) {
	if typ == importTypeStock {
		return importStockRequired, importStockOptional, nil
	}
	return importProductsRequired, importProductsOptional, importProductsIgnored
}

// ImportProductsCSV handles POST /api/v1/admin/products/import.
func (h *CatalogHandler) ImportProductsCSV(w http.ResponseWriter, r *http.Request) {
	typ := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	if typ != importTypeProducts && typ != importTypeStock {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "type must be products or stock", nil)
		return
	}
	dryRun := true
	if raw := strings.TrimSpace(r.URL.Query().Get("dryRun")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "dryRun must be true or false", nil)
			return
		}
		dryRun = parsed
	}
	data, status, code, msg := readImportFile(w, r)
	if status != 0 {
		common.JSONError(w, status, code, msg, nil)
		return
	}
	header, records, _, err := parseCSVUpload(data, importMaxRows)
	if err != nil {
		writeCSVUploadError(w, err)
		return
	}
	required, optional, ignored := importHeaders(typ)
	columns, err := validateHeaders(header, required, optional, ignored)
	if err != nil {
		writeCSVUploadError(w, err)
		return
	}
	if h == nil || h.Pool == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "catalog import not configured", nil)
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	tx, err := beginCatalogTx(ctx, h.Pool)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to start import", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)

	plan, err := planImport(ctx, q, tenantID, typ, columns, records)
	if err != nil {
		writeTxError(w, err, "failed to plan import")
		return
	}
	result := plan.result(dryRun)
	if result.Summary.Rejected > 0 {
		_ = tx.Rollback(ctx)
		rejected := make([]importRowResult, 0, result.Summary.Rejected)
		for _, row := range result.Rows {
			if row.Action == importActionReject {
				rejected = append(rejected, row)
			}
		}
		metadata := map[string]any{"type": typ, "dryRun": dryRun, "summary": result.Summary, "rejected": rejected}
		if auditErr := h.recordCatalogAudit(context.WithoutCancel(ctx), h.auditStore(), r, "catalog.import.rejected", "catalog_import", http.StatusUnprocessableEntity, metadata); auditErr != nil {
			zerolog.Ctx(ctx).Error().Err(auditErr).Msg("catalog import: rejected audit row failed")
		}
		common.JSONError(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "one or more rows are invalid; nothing was changed", result)
		return
	}
	if dryRun || !plan.hasWrites(result.Summary) {
		// Dry runs and no-op imports change nothing (rollback releases the locks).
		_ = tx.Rollback(ctx)
		common.JSON(w, http.StatusOK, map[string]any{"data": result})
		return
	}
	events, slugs, err := plan.apply(ctx, q)
	if err != nil {
		writeTxError(w, err, "failed to apply import")
		return
	}
	applied := make([]importRowResult, 0, len(result.Rows))
	for _, row := range result.Rows {
		if row.Action != importActionUnchanged {
			applied = append(applied, row)
		}
	}
	metadata := map[string]any{"type": typ, "summary": result.Summary, "rows": applied}
	if err := h.recordCatalogAudit(ctx, q, r, "catalog.import.commit", "catalog_import", http.StatusOK, metadata); err != nil {
		writeTxError(w, err, "failed to write audit log")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeTxError(w, err, "failed to commit import")
		return
	}
	h.afterProductChanges(ctx, catalog.ChangeSourceImport, events, slugs...)
	common.JSON(w, http.StatusOK, map[string]any{"data": result})
}

// auditStore returns the non-transactional store for audit rows written after
// a rollback (rejected imports).
func (h *CatalogHandler) auditStore() dbgen.Querier { return h.Q }

// readImportFile reads the multipart field "file" into memory (never to disk)
// with a 5 MiB cap. It returns a non-zero status with an error code on failure.
func readImportFile(w http.ResponseWriter, r *http.Request) ([]byte, int, string, string) {
	r.Body = http.MaxBytesReader(w, r.Body, importMaxBodyBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, http.StatusBadRequest, "BAD_REQUEST", "expected multipart/form-data with a file field"
	}
	tooLarge := func(err error) bool {
		var maxErr *http.MaxBytesError
		return errors.As(err, &maxErr)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, http.StatusBadRequest, "BAD_REQUEST", "file field is required"
		}
		if err != nil {
			if tooLarge(err) {
				return nil, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "file exceeds 5 MiB"
			}
			return nil, http.StatusBadRequest, "BAD_REQUEST", "invalid multipart body"
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, importMaxFileBytes+1))
		_ = part.Close()
		if err != nil {
			if tooLarge(err) {
				return nil, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "file exceeds 5 MiB"
			}
			return nil, http.StatusBadRequest, "BAD_REQUEST", "cannot read file"
		}
		if len(data) > importMaxFileBytes {
			return nil, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "file exceeds 5 MiB"
		}
		return data, 0, "", ""
	}
}

func writeCSVUploadError(w http.ResponseWriter, err error) {
	var upErr *csvUploadError
	if errors.As(err, &upErr) {
		details := map[string]any{}
		if upErr.Row > 0 {
			details["row"] = upErr.Row
		}
		if upErr.Header != "" {
			details["header"] = upErr.Header
		}
		common.JSONError(w, http.StatusBadRequest, upErr.Code, upErr.Message, details)
		return
	}
	common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
}

// ---------------------------------------------------------------------------
// Plan phase

// planImport resolves and validates every row and computes the planned
// writes. It only reads (and row-locks) data; it never writes.
func planImport(ctx context.Context, q importQuerier, tenantID pgtype.UUID, typ string, columns map[string]int, records []csvRecord) (*importPlan, error) {
	p := &importPlan{typ: typ, tenantID: tenantID, columns: columns, products: map[[16]byte]*importProduct{}}
	for _, rec := range records {
		p.rows = append(p.rows, p.parseRow(rec))
	}
	p.checkDuplicateKeys()
	if err := p.resolveIdentity(ctx, q); err != nil {
		return nil, err
	}
	if err := p.lockTargets(ctx, q); err != nil {
		return nil, err
	}
	if typ == importTypeProducts {
		if err := p.loadTaxonomy(ctx, q); err != nil {
			return nil, err
		}
		if err := p.allocateSlugs(ctx, q); err != nil {
			return nil, err
		}
		p.checkProductConsistency()
		p.planProducts()
	}
	p.planVariants()
	return p, nil
}

func (p *importPlan) parseRow(rec csvRecord) *importRow {
	row := &importRow{
		res:    &importRowResult{Row: rec.Row, Changes: map[string]importChange{}, Errors: []importFieldError{}},
		fields: make(map[string]string, len(p.columns)),
	}
	if len(rec.Fields) != len(p.columns) {
		row.fail("", importCodeInvalidRow, fmt.Sprintf("expected %d cells, found %d", len(p.columns), len(rec.Fields)))
		return row
	}
	for col, i := range p.columns {
		value := csvUnescapeCell(rec.Fields[i])
		if col != "title" && col != "description" {
			value = strings.TrimSpace(value)
		}
		row.fields[col] = value
	}
	row.sku = row.cell("sku")
	row.res.SKU = row.sku
	if raw := row.cell("variant_id"); raw != "" {
		id, err := parsePGUUID(raw)
		if err != nil {
			row.fail("variant_id", importCodeInvalidUUID, "variant_id must be a UUID")
		} else {
			row.variantID = id
		}
	}
	if raw := row.cell("product_id"); raw != "" {
		id, err := parsePGUUID(raw)
		if err != nil {
			row.fail("product_id", importCodeInvalidUUID, "product_id must be a UUID")
		} else {
			row.productID = id
		}
	}
	row.price = parseImportInt(row, "price", math.MaxInt64)
	row.compareAt = parseImportInt(row, "compare_at", math.MaxInt64)
	row.variantPrice = parseImportInt(row, "variant_price", math.MaxInt64)
	row.stock = parseImportInt(row, "stock", math.MaxInt32)
	row.stockBaseline = parseImportInt(row, "stock_baseline", math.MaxInt32)
	row.threshold = parseImportInt(row, "low_stock_threshold", int64(catalog.MaxLowStockThreshold))
	if raw := strings.ToLower(row.cell("status")); raw != "" {
		parsed, err := parseProductStatus(raw)
		if err != nil {
			row.fail("status", importCodeInvalidStatus, "status must be draft, published or archived")
		} else {
			row.status = parsed.String
		}
	}
	if raw := row.cell("slug"); raw != "" {
		row.slug = slugify(raw)
		if row.slug == "" {
			row.fail("slug", importCodeInvalidSlug, "slug must contain letters or digits")
		}
	}
	if raw := row.cell("attributes"); raw != "" {
		canonical, err := canonicalAttributes(raw)
		if err != nil {
			row.fail("attributes", importCodeInvalidJSON, "attributes must be a JSON object")
		} else {
			row.attributes = canonical
		}
	}
	if p.typ == importTypeStock && !row.stock.set && !row.rejected() {
		row.fail("stock", importCodeMissing, "stock is required")
	}
	return row
}

// parseImportInt parses a non-negative integer cell. Empty or absent is unset.
func parseImportInt(row *importRow, col string, max int64) importInt {
	raw := row.cell(col)
	if raw == "" {
		return importInt{}
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		row.fail(col, importCodeInvalidNumber, fmt.Sprintf("%s must be a whole number", col))
		return importInt{}
	}
	if v < 0 {
		row.fail(col, importCodeNegative, fmt.Sprintf("%s must be zero or greater", col))
		return importInt{}
	}
	if v > max {
		row.fail(col, importCodeOutOfRange, fmt.Sprintf("%s must be at most %d", col, max))
		return importInt{}
	}
	return importInt{set: true, value: v}
}

func canonicalAttributes(raw string) ([]byte, error) {
	var obj map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return nil, errors.New("not an object")
	}
	if dec.More() {
		return nil, errors.New("trailing data")
	}
	return json.Marshal(obj)
}

// checkDuplicateKeys rejects repeated variant_id or sku values in the file.
func (p *importPlan) checkDuplicateKeys() {
	byVariant := map[[16]byte]int{}
	bySKU := map[string]int{}
	for _, row := range p.rows {
		if row.variantID.Valid {
			if first, dup := byVariant[row.variantID.Bytes]; dup {
				row.fail("variant_id", importCodeDuplicateVariant, fmt.Sprintf("variant_id also on row %d", first))
			} else {
				byVariant[row.variantID.Bytes] = row.res.Row
			}
		}
		if row.sku != "" {
			if first, dup := bySKU[row.sku]; dup {
				row.fail("sku", importCodeDuplicateSKU, fmt.Sprintf("sku also on row %d", first))
			} else {
				bySKU[row.sku] = row.res.Row
			}
		}
	}
}

// resolveIdentity classifies each row as update, product-only, create_variant
// or create, using unlocked tenant-scoped lookups.
func (p *importPlan) resolveIdentity(ctx context.Context, q importQuerier) error {
	var ids []pgtype.UUID
	var skus []string
	for _, row := range p.rows {
		if row.variantID.Valid {
			ids = append(ids, row.variantID)
		}
		if row.sku != "" {
			skus = append(skus, row.sku)
		}
	}
	byID := map[[16]byte]dbgen.AdminFindVariantsForImportRow{}
	bySKU := map[string]dbgen.AdminFindVariantsForImportRow{}
	if len(ids) > 0 || len(skus) > 0 {
		found, err := q.AdminFindVariantsForImport(ctx, dbgen.AdminFindVariantsForImportParams{Ids: nonNilUUIDs(ids), Skus: nonNilStrings(skus), TenantID: p.tenantID})
		if err != nil {
			return fmt.Errorf("resolve variants: %w", err)
		}
		for _, v := range found {
			byID[v.ID.Bytes] = v
			if v.Sku.Valid {
				bySKU[v.Sku.String] = v
			}
		}
	}

	var globalSKUs []string
	resolvedVariant := map[[16]byte]*importRow{}
	for _, row := range p.rows {
		if row.rejected() {
			continue
		}
		var hit *dbgen.AdminFindVariantsForImportRow
		switch {
		case row.variantID.Valid:
			v, ok := byID[row.variantID.Bytes]
			if !ok {
				row.fail("variant_id", importCodeUnknownVariant, "variant_id does not exist in this store")
				continue
			}
			hit = &v
			if row.sku != "" {
				switch {
				case v.Sku.Valid && v.Sku.String != row.sku:
					row.fail("sku", importCodeSKUMismatch, fmt.Sprintf("sku %q differs from the variant's sku %q (was it changed by the spreadsheet app?)", row.sku, v.Sku.String))
					continue
				case !v.Sku.Valid && p.typ == importTypeStock:
					row.fail("sku", importCodeSKUMismatch, "the variant has no sku; leave sku empty or assign one with type=products")
					continue
				case !v.Sku.Valid:
					row.assignSKU = true
					globalSKUs = append(globalSKUs, row.sku)
				}
			}
		case row.sku != "":
			if v, ok := bySKU[row.sku]; ok {
				hit = &v
				break
			}
			if p.typ == importTypeStock {
				row.fail("sku", importCodeUnknownSKU, "sku does not exist in this store")
				continue
			}
			globalSKUs = append(globalSKUs, row.sku)
			if row.productID.Valid {
				row.kind = importActionCreateVariant
			} else {
				row.kind = importActionCreate
			}
			continue
		case p.typ == importTypeProducts && row.productID.Valid:
			row.kind = "product"
			for _, col := range importVariantColumns {
				if row.cell(col) != "" {
					row.fail(col, importCodeNoVariant, fmt.Sprintf("%s needs a sku or variant_id on this row", col))
				}
			}
			continue
		default:
			msg := "sku or variant_id is required"
			if p.typ == importTypeProducts {
				msg = "sku, variant_id or product_id is required"
			}
			row.fail("sku", importCodeMissing, msg)
			continue
		}
		if row.productID.Valid && row.productID != hit.ProductID {
			row.fail("product_id", importCodeProductMismatch, "the variant belongs to a different product")
			continue
		}
		if first, dup := resolvedVariant[hit.ID.Bytes]; dup {
			row.fail("variant_id", importCodeDuplicateVariant, fmt.Sprintf("row targets the same variant as row %d", first.res.Row))
			continue
		}
		resolvedVariant[hit.ID.Bytes] = row
		row.kind = importActionUpdate
		row.variantID = hit.ID
		row.productID = hit.ProductID
	}

	if len(globalSKUs) > 0 {
		taken, err := q.AdminExistingSKUsAnyTenant(ctx, globalSKUs)
		if err != nil {
			return fmt.Errorf("check skus: %w", err)
		}
		takenSet := make(map[string]bool, len(taken))
		for _, s := range taken {
			takenSet[s] = true
		}
		for _, row := range p.rows {
			if row.rejected() || row.sku == "" || !takenSet[row.sku] {
				continue
			}
			if row.assignSKU || row.kind == importActionCreate || row.kind == importActionCreateVariant {
				row.fail("sku", importCodeSKUTaken, "sku is already used by another product")
			}
		}
	}
	return nil
}

// lockTargets resolves product ids, converts create groups whose slug already
// belongs to a tenant product into create_variant rows, then row-locks every
// existing product and variant the file touches (sorted by id) and re-reads
// their current values.
func (p *importPlan) lockTargets(ctx context.Context, q importQuerier) error {
	productIDs := map[[16]byte]pgtype.UUID{}
	var groupSlugs []string
	for _, row := range p.rows {
		if row.rejected() {
			continue
		}
		if row.productID.Valid {
			productIDs[row.productID.Bytes] = row.productID
		}
		if row.kind == importActionCreate {
			base := row.slug
			if base == "" {
				base = slugify(row.cell("title"))
			}
			if base != "" {
				groupSlugs = append(groupSlugs, base)
			}
		}
	}
	ids := make([]pgtype.UUID, 0, len(productIDs))
	for _, id := range productIDs {
		ids = append(ids, id)
	}
	tenantProducts := map[[16]byte]bool{}
	slugOwner := map[string]pgtype.UUID{}
	if len(ids) > 0 || len(groupSlugs) > 0 {
		found, err := q.AdminFindProductsForImport(ctx, dbgen.AdminFindProductsForImportParams{Ids: nonNilUUIDs(ids), Slugs: nonNilStrings(groupSlugs), TenantID: p.tenantID})
		if err != nil {
			return fmt.Errorf("resolve products: %w", err)
		}
		for _, prod := range found {
			tenantProducts[prod.ID.Bytes] = true
			slugOwner[prod.Slug] = prod.ID
		}
	}
	for _, row := range p.rows {
		if row.rejected() {
			continue
		}
		if (row.kind == importActionCreateVariant || row.kind == "product") && !tenantProducts[row.productID.Bytes] {
			row.fail("product_id", importCodeUnknownProduct, "product_id does not exist in this store")
			continue
		}
		if row.kind == importActionCreate {
			base := row.slug
			if base == "" {
				base = slugify(row.cell("title"))
			}
			if owner, ok := slugOwner[base]; ok && base != "" {
				// The slug already names a product of this store: add variants to it.
				row.kind = importActionCreateVariant
				row.productID = owner
			}
		}
	}

	lockIDs := make([]pgtype.UUID, 0, len(productIDs))
	for _, row := range p.rows {
		if !row.rejected() && row.productID.Valid {
			if _, ok := p.products[row.productID.Bytes]; !ok {
				p.products[row.productID.Bytes] = nil
				lockIDs = append(lockIDs, row.productID)
			}
		}
	}
	if len(lockIDs) > 0 {
		locked, err := q.AdminGetProductsByIDsForUpdate(ctx, dbgen.AdminGetProductsByIDsForUpdateParams{Ids: lockIDs, TenantID: p.tenantID})
		if err != nil {
			return fmt.Errorf("lock products: %w", err)
		}
		for _, prod := range locked {
			p.products[prod.ID.Bytes] = &importProduct{locked: prod, newSlug: prod.Slug, price: prod.Price}
		}
	}
	var variantIDs []pgtype.UUID
	for _, row := range p.rows {
		if row.rejected() || !row.productID.Valid {
			continue
		}
		prod := p.products[row.productID.Bytes]
		if prod == nil {
			// Deleted between resolution and lock.
			row.fail("product_id", importCodeUnknownProduct, "product no longer exists")
			continue
		}
		row.product = prod
		prod.rows = append(prod.rows, row)
		if row.kind == importActionUpdate {
			variantIDs = append(variantIDs, row.variantID)
		}
	}
	for _, id := range lockIDs {
		if prod := p.products[id.Bytes]; prod != nil {
			p.order = append(p.order, prod)
		}
	}
	if len(variantIDs) > 0 {
		locked, err := q.AdminLockVariantsByIDs(ctx, dbgen.AdminLockVariantsByIDsParams{Ids: variantIDs, TenantID: p.tenantID})
		if err != nil {
			return fmt.Errorf("lock variants: %w", err)
		}
		byID := make(map[[16]byte]*dbgen.AdminLockVariantsByIDsRow, len(locked))
		for i := range locked {
			byID[locked[i].ID.Bytes] = &locked[i]
		}
		for _, row := range p.rows {
			if row.rejected() || row.kind != importActionUpdate {
				continue
			}
			v := byID[row.variantID.Bytes]
			if v == nil {
				row.fail("variant_id", importCodeUnknownVariant, "variant no longer exists")
				continue
			}
			row.variant = v
		}
	}
	for _, row := range p.rows {
		if row.rejected() {
			continue
		}
		if row.variantID.Valid {
			row.res.VariantID = uuidString(row.variantID)
		}
		if row.product != nil {
			row.res.ProductID = uuidString(row.product.locked.ID)
			row.res.Slug = row.product.locked.Slug
		}
	}
	return nil
}

func (p *importPlan) loadTaxonomy(ctx context.Context, q importQuerier) error {
	if _, ok := p.columns["category_slug"]; ok {
		cats, err := q.AdminListCategories(ctx, p.tenantID)
		if err != nil {
			return fmt.Errorf("load categories: %w", err)
		}
		p.categoryBySlug = make(map[string]pgtype.UUID, len(cats))
		p.categorySlugBy = make(map[[16]byte]string, len(cats))
		for _, c := range cats {
			p.categoryBySlug[c.Slug] = c.ID
			p.categorySlugBy[c.ID.Bytes] = c.Slug
		}
	}
	if _, ok := p.columns["brand_slug"]; ok {
		brands, err := q.AdminListBrands(ctx, p.tenantID)
		if err != nil {
			return fmt.Errorf("load brands: %w", err)
		}
		p.brandBySlug = make(map[string]pgtype.UUID, len(brands))
		p.brandSlugBy = make(map[[16]byte]string, len(brands))
		for _, b := range brands {
			p.brandBySlug[b.Slug] = b.ID
			p.brandSlugBy[b.ID.Bytes] = b.Slug
		}
	}
	return nil
}

// allocateSlugs groups new-product rows by slug and picks a globally free slug
// for each group; it also validates slug changes on existing products.
func (p *importPlan) allocateSlugs(ctx context.Context, q importQuerier) error {
	byKey := map[string]*importGroup{}
	for _, row := range p.rows {
		if row.rejected() || row.kind != importActionCreate {
			continue
		}
		explicit := row.slug != ""
		base := row.slug
		if !explicit {
			base = slugify(row.cell("title"))
		}
		if base == "" {
			row.fail("title", importCodeMissingForCreate, "title is required to create a product")
			continue
		}
		key := base
		if !explicit {
			key = "~" + base // derived and explicit slugs form distinct groups
		}
		g := byKey[key]
		if g == nil {
			g = &importGroup{key: key, base: base, explicit: explicit}
			byKey[key] = g
			p.groups = append(p.groups, g)
		}
		g.rows = append(g.rows, row)
		row.group = g
	}

	// Candidate slugs to check: every group's base (+ suffixes for derived
	// slugs) and every slug change requested for an existing product.
	candidateSet := map[string]bool{}
	for _, g := range p.groups {
		candidateSet[g.base] = true
		if !g.explicit {
			for i := 2; i <= importSlugAttempts; i++ {
				candidateSet[fmt.Sprintf("%s-%d", g.base, i)] = true
			}
		}
	}
	for _, prod := range p.order {
		for _, row := range prod.rows {
			if !row.rejected() && row.slug != "" && row.slug != prod.locked.Slug {
				candidateSet[row.slug] = true
			}
		}
	}
	if len(candidateSet) == 0 {
		return nil
	}
	candidates := make([]string, 0, len(candidateSet))
	for s := range candidateSet {
		candidates = append(candidates, s)
	}
	sort.Strings(candidates)
	taken := map[string]bool{}
	other, err := q.AdminSlugsTakenByOtherTenants(ctx, dbgen.AdminSlugsTakenByOtherTenantsParams{Slugs: candidates, TenantID: p.tenantID})
	if err != nil {
		return fmt.Errorf("check slugs: %w", err)
	}
	for _, s := range other {
		taken[s] = true
	}
	own, err := q.AdminFindProductsForImport(ctx, dbgen.AdminFindProductsForImportParams{Ids: []pgtype.UUID{}, Slugs: candidates, TenantID: p.tenantID})
	if err != nil {
		return fmt.Errorf("check slugs: %w", err)
	}
	ownSlug := map[string]pgtype.UUID{}
	for _, prod := range own {
		ownSlug[prod.Slug] = prod.ID
	}
	reserved := map[string]bool{}

	// Slug changes on existing products first (they are explicit).
	for _, prod := range p.order {
		for _, row := range prod.rows {
			if row.rejected() || row.slug == "" || row.slug == prod.locked.Slug {
				continue
			}
			ownerID, ownedHere := ownSlug[row.slug]
			switch {
			case taken[row.slug], ownedHere && ownerID != prod.locked.ID:
				row.fail("slug", importCodeSlugTaken, fmt.Sprintf("slug %q is already used by another product", row.slug))
			case reserved[row.slug] && prod.newSlug != row.slug:
				row.fail("slug", importCodeSlugTaken, fmt.Sprintf("slug %q is used twice in this file", row.slug))
			default:
				reserved[row.slug] = true
				prod.newSlug = row.slug
			}
		}
	}
	// Explicit slugs are claimed before derived ones, so a derived slug never
	// steals the exact slug another group asked for.
	ordered := make([]*importGroup, 0, len(p.groups))
	for _, explicitPass := range []bool{true, false} {
		for _, g := range p.groups {
			if g.explicit == explicitPass {
				ordered = append(ordered, g)
			}
		}
	}
	for _, g := range ordered {
		var chosen string
		tries := []string{g.base}
		if !g.explicit {
			for i := 2; i <= importSlugAttempts; i++ {
				tries = append(tries, fmt.Sprintf("%s-%d", g.base, i))
			}
		}
		for _, s := range tries {
			if _, ownedHere := ownSlug[s]; ownedHere || taken[s] || reserved[s] {
				continue
			}
			chosen = s
			break
		}
		if chosen == "" {
			for _, row := range g.rows {
				row.fail("slug", importCodeSlugTaken, fmt.Sprintf("slug %q is already used by another product", g.base))
			}
			continue
		}
		reserved[chosen] = true
		g.slug = chosen
		for _, row := range g.rows {
			row.res.Slug = chosen
		}
	}
	return nil
}

// checkProductConsistency requires every row of one product (existing or new)
// to carry identical product-level cells.
func (p *importPlan) checkProductConsistency() {
	check := func(rows []*importRow) {
		var first *importRow
		for _, row := range rows {
			if row.rejected() {
				continue
			}
			if first == nil {
				first = row
				continue
			}
			for _, col := range importProductColumns {
				if row.has(col) && row.cell(col) != first.cell(col) {
					row.fail(col, importCodeConflictingValues, fmt.Sprintf("%s differs from row %d of the same product", col, first.res.Row))
				}
			}
		}
	}
	for _, prod := range p.order {
		check(prod.rows)
	}
	for _, g := range p.groups {
		check(g.rows)
	}
}

func firstValid(rows []*importRow) *importRow {
	for _, row := range rows {
		if !row.rejected() {
			return row
		}
	}
	return nil
}

// sameCSVText compares a file cell with a stored value the way a CSV round trip
// sees them: the export writes CRLF records and encoding/csv turns a CRLF
// inside a quoted field into LF and drops a lone CR, so stored text with CR
// characters must not look edited when the file is re-imported unchanged.
func sameCSVText(file, stored string) bool {
	return strings.ReplaceAll(file, "\r", "") == strings.ReplaceAll(stored, "\r", "")
}

// planProducts computes product-level changes for existing products and the
// product fields of new groups. Changes are reported on the first row of the
// product.
func (p *importPlan) planProducts() {
	for _, prod := range p.order {
		row := firstValid(prod.rows)
		if row == nil {
			continue
		}
		cur := prod.locked
		params := dbgen.AdminUpdateProductParams{ID: cur.ID, TenantID: p.tenantID}
		if title := row.cell("title"); row.has("title") && strings.TrimSpace(title) != "" && !sameCSVText(title, cur.Title) {
			params.Title = pgtype.Text{String: title, Valid: true}
			row.change("title", cur.Title, title)
		}
		if prod.newSlug != cur.Slug {
			params.Slug = pgtype.Text{String: prod.newSlug, Valid: true}
			row.change("slug", cur.Slug, prod.newSlug)
		}
		if row.status != "" && row.status != cur.Status {
			params.Status = pgtype.Text{String: row.status, Valid: true}
			row.change("status", cur.Status, row.status)
			prod.events = append(prod.events, catalog.ProductChange{ProductID: cur.ID, Slug: prod.newSlug, Field: catalog.FieldStatus, Old: cur.Status, New: row.status})
		}
		if row.price.set && row.price.value != cur.Price {
			params.Price = pgtype.Int8{Int64: row.price.value, Valid: true}
			prod.price = row.price.value
			row.change("price", cur.Price, row.price.value)
			prod.events = append(prod.events, catalog.ProductChange{ProductID: cur.ID, Slug: prod.newSlug, Field: catalog.FieldPrice, Old: cur.Price, New: row.price.value})
		}
		if row.has("compare_at") {
			next := pgtype.Int8{Int64: row.compareAt.value, Valid: row.compareAt.set}
			if !int8Equal(next, cur.CompareAt) {
				params.SetCompareAt = true
				params.CompareAt = next
				row.change("compare_at", nullableInt64(cur.CompareAt), nullableInt64(next))
				prod.events = append(prod.events, catalog.ProductChange{ProductID: cur.ID, Slug: prod.newSlug, Field: catalog.FieldCompareAt, Old: nullableInt64(cur.CompareAt), New: nullableInt64(next)})
			}
		}
		if row.has("low_stock_threshold") {
			next := pgtype.Int4{Int32: int32(row.threshold.value), Valid: row.threshold.set}
			if !int4Equal(next, cur.LowStockThreshold) {
				params.SetLowStockThreshold = true
				params.LowStockThreshold = next
				row.change("low_stock_threshold", nullableInt32(cur.LowStockThreshold), nullableInt32(next))
			}
		}
		if row.has("category_slug") {
			if next, ok := p.resolveTaxonomy(row, "category_slug", p.categoryBySlug, importCodeUnknownCategory); ok && next != cur.CategoryID {
				params.SetCategory = true
				params.CategoryID = next
				row.change("category_slug", taxonomySlug(p.categorySlugBy, cur.CategoryID), taxonomySlug(p.categorySlugBy, next))
			}
		}
		if row.has("brand_slug") {
			if next, ok := p.resolveTaxonomy(row, "brand_slug", p.brandBySlug, importCodeUnknownBrand); ok && next != cur.BrandID {
				params.SetBrand = true
				params.BrandID = next
				row.change("brand_slug", taxonomySlug(p.brandSlugBy, cur.BrandID), taxonomySlug(p.brandSlugBy, next))
			}
		}
		if row.has("description") {
			// NULL, "" and blank are equivalent; a blank cell clears the description.
			next, current := row.cell("description"), cur.Description.String
			if strings.TrimSpace(next) == "" {
				next = ""
			}
			if strings.TrimSpace(current) == "" {
				current = ""
			}
			if !sameCSVText(next, current) {
				params.SetDescription = true
				params.Description = text(next)
				row.change("description", nullableString(cur.Description), nullableString(params.Description))
			}
		}
		prod.params = params
		prod.update = len(row.res.Changes) > 0
	}
	for _, g := range p.groups {
		row := firstValid(g.rows)
		if row == nil || g.slug == "" {
			continue
		}
		if strings.TrimSpace(row.cell("title")) == "" {
			for _, r := range g.rows {
				r.fail("title", importCodeMissingForCreate, "title is required to create a product")
			}
			continue
		}
		if !row.price.set {
			for _, r := range g.rows {
				r.fail("price", importCodeMissingForCreate, "price is required to create a product")
			}
			continue
		}
		status := row.status
		if status == "" {
			status = productStatusPublished
		}
		row.change("title", nil, row.cell("title"))
		row.change("slug", nil, g.slug)
		row.change("status", nil, status)
		row.change("price", nil, row.price.value)
		if row.compareAt.set {
			row.change("compare_at", nil, row.compareAt.value)
		}
		if row.threshold.set {
			row.change("low_stock_threshold", nil, row.threshold.value)
		}
		if row.cell("category_slug") != "" {
			if _, ok := p.resolveTaxonomy(row, "category_slug", p.categoryBySlug, importCodeUnknownCategory); ok {
				row.change("category_slug", nil, row.cell("category_slug"))
			}
		}
		if row.cell("brand_slug") != "" {
			if _, ok := p.resolveTaxonomy(row, "brand_slug", p.brandBySlug, importCodeUnknownBrand); ok {
				row.change("brand_slug", nil, row.cell("brand_slug"))
			}
		}
		if strings.TrimSpace(row.cell("description")) != "" {
			row.change("description", nil, row.cell("description"))
		}
	}
}

// resolveTaxonomy maps a category/brand slug cell to an id; empty clears.
func (p *importPlan) resolveTaxonomy(row *importRow, col string, bySlug map[string]pgtype.UUID, code string) (pgtype.UUID, bool) {
	slug := row.cell(col)
	if slug == "" {
		return pgtype.UUID{}, true
	}
	id, ok := bySlug[slug]
	if !ok {
		row.fail(col, code, fmt.Sprintf("%s %q does not exist in this store", col, slug))
		return pgtype.UUID{}, false
	}
	return id, true
}

func taxonomySlug(bySlug map[[16]byte]string, id pgtype.UUID) any {
	if !id.Valid {
		return nil
	}
	if s, ok := bySlug[id.Bytes]; ok {
		return s
	}
	return uuidString(id)
}

// planVariants computes variant changes for update rows and the variant values
// of create/create_variant rows, then sets each row's action.
func (p *importPlan) planVariants() {
	for _, row := range p.rows {
		if row.rejected() {
			continue
		}
		switch row.kind {
		case importActionUpdate:
			p.planVariantUpdate(row)
		case importActionCreate, importActionCreateVariant:
			price := row.variantPrice.value
			if !row.variantPrice.set {
				switch {
				case row.product != nil:
					price = row.product.price
				default:
					price = row.price.value
				}
			}
			row.change("sku", nil, row.sku)
			row.change("variant_price", nil, price)
			row.change("stock", nil, row.stock.value)
			attrs := row.attributes
			if attrs == nil {
				attrs = []byte(`{}`)
			}
			row.change("attributes", nil, json.RawMessage(attrs))
			row.newVariantPrice = pgtype.Int8{Int64: price, Valid: true}
			row.newStock = pgtype.Int4{Int32: int32(row.stock.value), Valid: true}
			row.newAttributes = attrs
		}
	}
	for _, row := range p.rows {
		switch {
		case row.rejected():
			row.res.Action = importActionReject
			row.res.Changes = map[string]importChange{}
		case row.kind == importActionCreate:
			row.res.Action = importActionCreate
		case row.kind == importActionCreateVariant:
			row.res.Action = importActionCreateVariant
		case len(row.res.Changes) > 0:
			row.res.Action = importActionUpdate
		default:
			row.res.Action = importActionUnchanged
		}
	}
}

func (p *importPlan) planVariantUpdate(row *importRow) {
	v := row.variant
	if v == nil {
		return
	}
	if row.assignSKU {
		row.newSKU = pgtype.Text{String: row.sku, Valid: true}
		row.change("sku", nil, row.sku)
	}
	if row.variantPrice.set && row.variantPrice.value != v.Price {
		row.newVariantPrice = pgtype.Int8{Int64: row.variantPrice.value, Valid: true}
		row.change("variant_price", v.Price, row.variantPrice.value)
	}
	if row.stock.set {
		current := int64(v.Stock)
		next, ok := p.resolveStock(row, current)
		if ok && next != current {
			row.newStock = pgtype.Int4{Int32: int32(next), Valid: true}
			row.change("stock", current, next)
		}
	}
	if row.attributes != nil {
		current, err := canonicalAttributes(string(v.Attributes))
		if err != nil || !bytes.Equal(current, row.attributes) {
			row.newAttributes = row.attributes
			row.change("attributes", json.RawMessage(nonEmptyJSON(v.Attributes)), json.RawMessage(row.attributes))
		}
	}
}

// resolveStock applies the stale-stock rule to an existing variant. With a
// stock_baseline (the stock at export time): file == baseline means the cell
// was not edited (no change, whatever the live stock is now); an edited cell
// applies only while the live stock still equals the baseline, otherwise the
// row is rejected as STALE_STOCK so sales made since the export are never
// overwritten. Without a baseline, type=stock sets the absolute count (that is
// its purpose) and type=products refuses to change stock.
func (p *importPlan) resolveStock(row *importRow, current int64) (int64, bool) {
	file := row.stock.value
	if row.stockBaseline.set {
		baseline := row.stockBaseline.value
		switch {
		case file == baseline, file == current:
			return current, true
		case current == baseline:
			return file, true
		default:
			row.fail("stock", importCodeStaleStock, fmt.Sprintf("stock changed from %d to %d since the export; export again or use a delta", baseline, current))
			return current, false
		}
	}
	if p.typ == importTypeStock || file == current {
		return file, true
	}
	row.fail("stock", importCodeStockBaselineMissing, "changing the stock of an existing variant needs the stock_baseline column from an export; use type=stock to set absolute counts")
	return current, false
}

func nonEmptyJSON(raw []byte) []byte {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte(`{}`)
	}
	return raw
}

func (p *importPlan) result(dryRun bool) importResult {
	res := importResult{DryRun: dryRun, Type: p.typ, Rows: make([]importRowResult, 0, len(p.rows))}
	for _, row := range p.rows {
		switch row.res.Action {
		case importActionCreate:
			res.Summary.Create++
		case importActionCreateVariant:
			res.Summary.CreateVariant++
		case importActionUpdate:
			res.Summary.Update++
		case importActionUnchanged:
			res.Summary.Unchanged++
		case importActionReject:
			res.Summary.Rejected++
		}
		res.Rows = append(res.Rows, *row.res)
	}
	res.Summary.Total = len(p.rows)
	return res
}

func (p *importPlan) hasWrites(s importSummary) bool {
	return s.Create+s.CreateVariant+s.Update > 0
}

// ---------------------------------------------------------------------------
// Apply phase

// importWriter is what the apply phase writes through (the transaction's
// queries).
type importWriter interface {
	AdminCreateProduct(ctx context.Context, arg dbgen.AdminCreateProductParams) (pgtype.UUID, error)
	AdminUpdateProduct(ctx context.Context, arg dbgen.AdminUpdateProductParams) (pgtype.UUID, error)
	AdminInsertProductVariant(ctx context.Context, arg dbgen.AdminInsertProductVariantParams) (pgtype.UUID, error)
	AdminUpdateProductVariant(ctx context.Context, arg dbgen.AdminUpdateProductVariantParams) (dbgen.ProductVariant, error)
	AdminRecomputeProductStockFlag(ctx context.Context, arg dbgen.AdminRecomputeProductStockFlagParams) (bool, error)
}

// apply executes the planned writes. It returns the product.* changes to emit
// after commit and every slug whose cache entries must be invalidated.
func (p *importPlan) apply(ctx context.Context, q importWriter) ([]catalog.ProductChange, []string, error) {
	var events []catalog.ProductChange
	var slugs []string
	recompute := map[[16]byte]pgtype.UUID{}

	for _, prod := range p.order {
		if prod.update {
			if _, err := q.AdminUpdateProduct(ctx, prod.params); err != nil {
				return nil, nil, fmt.Errorf("update product: %w", err)
			}
			events = append(events, prod.events...)
			slugs = append(slugs, prod.locked.Slug, prod.newSlug)
		}
	}
	for _, g := range p.groups {
		row := firstValid(g.rows)
		if row == nil {
			continue
		}
		status := row.status
		if status == "" {
			status = productStatusPublished
		}
		params := dbgen.AdminCreateProductParams{
			Title:             row.cell("title"),
			Slug:              g.slug,
			Price:             row.price.value,
			CompareAt:         pgtype.Int8{Int64: row.compareAt.value, Valid: row.compareAt.set},
			InStock:           false,
			Badges:            []string{},
			Description:       text(row.cell("description")),
			TenantID:          p.tenantID,
			Status:            status,
			LowStockThreshold: pgtype.Int4{Int32: int32(row.threshold.value), Valid: row.threshold.set},
		}
		if id, ok := p.categoryBySlug[row.cell("category_slug")]; ok {
			params.CategoryID = id
		}
		if id, ok := p.brandBySlug[row.cell("brand_slug")]; ok {
			params.BrandID = id
		}
		productID, err := q.AdminCreateProduct(ctx, params)
		if err != nil {
			return nil, nil, fmt.Errorf("create product: %w", err)
		}
		slugs = append(slugs, g.slug)
		for _, r := range g.rows {
			if _, err := q.AdminInsertProductVariant(ctx, dbgen.AdminInsertProductVariantParams{
				ProductID: productID, Sku: pgtype.Text{String: r.sku, Valid: true},
				Price: r.newVariantPrice.Int64, Stock: r.newStock.Int32, Attributes: r.newAttributes, TenantID: p.tenantID,
			}); err != nil {
				return nil, nil, fmt.Errorf("create variant: %w", err)
			}
			r.res.ProductID = uuidString(productID)
		}
		recompute[productID.Bytes] = productID
	}
	for _, row := range p.rows {
		if row.res.Action == importActionReject || row.product == nil {
			continue
		}
		prod := row.product
		switch row.kind {
		case importActionCreateVariant:
			if _, err := q.AdminInsertProductVariant(ctx, dbgen.AdminInsertProductVariantParams{
				ProductID: prod.locked.ID, Sku: pgtype.Text{String: row.sku, Valid: true},
				Price: row.newVariantPrice.Int64, Stock: row.newStock.Int32, Attributes: row.newAttributes, TenantID: p.tenantID,
			}); err != nil {
				return nil, nil, fmt.Errorf("create variant: %w", err)
			}
			recompute[prod.locked.ID.Bytes] = prod.locked.ID
			slugs = append(slugs, prod.newSlug)
		case importActionUpdate:
			v := row.variant
			if v == nil || (!row.newSKU.Valid && !row.newVariantPrice.Valid && !row.newStock.Valid && row.newAttributes == nil) {
				continue
			}
			if _, err := q.AdminUpdateProductVariant(ctx, dbgen.AdminUpdateProductVariantParams{
				Sku: row.newSKU, Price: row.newVariantPrice, Stock: row.newStock, Attributes: row.newAttributes,
				ID: v.ID, ProductID: v.ProductID, TenantID: p.tenantID,
			}); err != nil {
				return nil, nil, fmt.Errorf("update variant: %w", err)
			}
			slugs = append(slugs, prod.newSlug)
			if row.newVariantPrice.Valid {
				events = append(events, catalog.ProductChange{ProductID: v.ProductID, VariantID: v.ID, Slug: prod.newSlug, SKU: firstNonEmpty(v.Sku.String, row.sku), Field: catalog.FieldPrice, Old: v.Price, New: row.newVariantPrice.Int64})
			}
			if row.newStock.Valid {
				events = append(events, catalog.ProductChange{ProductID: v.ProductID, VariantID: v.ID, Slug: prod.newSlug, SKU: firstNonEmpty(v.Sku.String, row.sku), Field: catalog.FieldStock, Old: v.Stock, New: row.newStock.Int32})
				recompute[v.ProductID.Bytes] = v.ProductID
			}
		}
	}
	ids := make([]pgtype.UUID, 0, len(recompute))
	for _, id := range recompute {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i].Bytes[:], ids[j].Bytes[:]) < 0 })
	for _, id := range ids {
		if _, err := q.AdminRecomputeProductStockFlag(ctx, dbgen.AdminRecomputeProductStockFlagParams{ID: id, TenantID: p.tenantID}); err != nil {
			return nil, nil, fmt.Errorf("recompute stock flag: %w", err)
		}
	}
	return events, slugs, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func nonNilUUIDs(ids []pgtype.UUID) []pgtype.UUID {
	if ids == nil {
		return []pgtype.UUID{}
	}
	return ids
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
