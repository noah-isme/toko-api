package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// fakeImportDB is an in-memory catalog for the plan phase. Products and
// variants belong to the caller's tenant; otherSKUs/otherSlugs belong to
// another tenant.
type fakeImportDB struct {
	products   map[[16]byte]*dbgen.AdminGetProductsByIDsForUpdateRow
	variants   map[[16]byte]*dbgen.AdminLockVariantsByIDsRow
	otherSKUs  map[string]bool
	otherSlugs map[string]bool
	categories []dbgen.AdminListCategoriesRow
}

func newFakeImportDB() *fakeImportDB {
	return &fakeImportDB{
		products:   map[[16]byte]*dbgen.AdminGetProductsByIDsForUpdateRow{},
		variants:   map[[16]byte]*dbgen.AdminLockVariantsByIDsRow{},
		otherSKUs:  map[string]bool{},
		otherSlugs: map[string]bool{},
	}
}

func (f *fakeImportDB) addProduct(slug, title string, price int64) pgtype.UUID {
	id := pgID()
	f.products[id.Bytes] = &dbgen.AdminGetProductsByIDsForUpdateRow{ID: id, Slug: slug, Title: title, Price: price, Status: "published"}
	return id
}

func (f *fakeImportDB) addVariant(productID pgtype.UUID, sku string, price int64, stock int32) pgtype.UUID {
	id := pgID()
	v := &dbgen.AdminLockVariantsByIDsRow{ID: id, ProductID: productID, Price: price, Stock: stock, Attributes: []byte(`{}`), Slug: f.products[productID.Bytes].Slug}
	if sku != "" {
		v.Sku = pgtype.Text{String: sku, Valid: true}
	}
	f.variants[id.Bytes] = v
	return id
}

func (f *fakeImportDB) AdminFindVariantsForImport(_ context.Context, arg dbgen.AdminFindVariantsForImportParams) ([]dbgen.AdminFindVariantsForImportRow, error) {
	var out []dbgen.AdminFindVariantsForImportRow
	for _, v := range f.variants {
		match := false
		for _, id := range arg.Ids {
			match = match || id == v.ID
		}
		for _, s := range arg.Skus {
			match = match || (v.Sku.Valid && v.Sku.String == s)
		}
		if match {
			out = append(out, dbgen.AdminFindVariantsForImportRow{ID: v.ID, ProductID: v.ProductID, Sku: v.Sku})
		}
	}
	return out, nil
}

func (f *fakeImportDB) AdminExistingSKUsAnyTenant(_ context.Context, skus []string) ([]string, error) {
	var out []string
	for _, s := range skus {
		if f.otherSKUs[s] {
			out = append(out, s)
			continue
		}
		for _, v := range f.variants {
			if v.Sku.Valid && v.Sku.String == s {
				out = append(out, s)
				break
			}
		}
	}
	return out, nil
}

func (f *fakeImportDB) AdminFindProductsForImport(_ context.Context, arg dbgen.AdminFindProductsForImportParams) ([]dbgen.AdminFindProductsForImportRow, error) {
	var out []dbgen.AdminFindProductsForImportRow
	for _, p := range f.products {
		match := false
		for _, id := range arg.Ids {
			match = match || id == p.ID
		}
		for _, s := range arg.Slugs {
			match = match || p.Slug == s
		}
		if match {
			out = append(out, dbgen.AdminFindProductsForImportRow{ID: p.ID, Slug: p.Slug})
		}
	}
	return out, nil
}

func (f *fakeImportDB) AdminSlugsTakenByOtherTenants(_ context.Context, arg dbgen.AdminSlugsTakenByOtherTenantsParams) ([]string, error) {
	var out []string
	for _, s := range arg.Slugs {
		if f.otherSlugs[s] {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeImportDB) AdminGetProductsByIDsForUpdate(_ context.Context, arg dbgen.AdminGetProductsByIDsForUpdateParams) ([]dbgen.AdminGetProductsByIDsForUpdateRow, error) {
	var out []dbgen.AdminGetProductsByIDsForUpdateRow
	for _, id := range arg.Ids {
		if p, ok := f.products[id.Bytes]; ok {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (f *fakeImportDB) AdminLockVariantsByIDs(_ context.Context, arg dbgen.AdminLockVariantsByIDsParams) ([]dbgen.AdminLockVariantsByIDsRow, error) {
	var out []dbgen.AdminLockVariantsByIDsRow
	for _, id := range arg.Ids {
		if v, ok := f.variants[id.Bytes]; ok {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (f *fakeImportDB) AdminListCategories(context.Context, pgtype.UUID) ([]dbgen.AdminListCategoriesRow, error) {
	return f.categories, nil
}

func (f *fakeImportDB) AdminListBrands(context.Context, pgtype.UUID) ([]dbgen.AdminListBrandsRow, error) {
	return nil, nil
}

// planCSV parses a CSV text and runs the plan phase against db.
func planCSV(t *testing.T, db *fakeImportDB, typ, data string) importResult {
	t.Helper()
	header, records, _, err := parseCSVUpload([]byte(data), importMaxRows)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	required, optional, ignored := importHeaders(typ)
	columns, err := validateHeaders(header, required, optional, ignored)
	if err != nil {
		t.Fatalf("headers: %v", err)
	}
	plan, err := planImport(context.Background(), db, pgID(), typ, columns, records)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan.result(true)
}

func idStr(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }

func errCodes(row importRowResult) string {
	codes := make([]string, 0, len(row.Errors))
	for _, e := range row.Errors {
		codes = append(codes, e.Code)
	}
	return strings.Join(codes, ",")
}

func TestImportPlanClassification(t *testing.T) {
	db := newFakeImportDB()
	shoe := db.addProduct("sepatu", "Sepatu", 10000)
	v1 := db.addVariant(shoe, "SH-1", 10000, 5)
	skuLess := db.addVariant(shoe, "", 10000, 2)
	bare := db.addProduct("tanpa-varian", "Tanpa Varian", 5000) // product without variants

	csvData := "sku,variant_id,product_id,title,price,variant_price,stock,stock_baseline\n" +
		"SH-1,,," + "Sepatu,10000,12000,5,5\n" + // update variant price
		"," + idStr(skuLess) + ",,Sepatu,10000,10000,2,2\n" + // SKU-less variant, unchanged
		",," + idStr(bare) + ",Tanpa Varian,5000,,,\n" + // product without variants: unchanged
		"SH-2,," + idStr(shoe) + ",Sepatu,10000,,3,\n" + // create_variant
		"NEW-1,,,Sandal Baru,20000,,7,\n" // create
	res := planCSV(t, db, importTypeProducts, csvData)
	want := []string{importActionUpdate, importActionUnchanged, importActionUnchanged, importActionCreateVariant, importActionCreate}
	for i, row := range res.Rows {
		if row.Action != want[i] {
			t.Errorf("row %d action %s want %s (errors %v)", row.Row, row.Action, want[i], row.Errors)
		}
	}
	if res.Rows[0].Changes["variant_price"].To != int64(12000) || res.Rows[0].VariantID != idStr(v1) {
		t.Errorf("update row %+v", res.Rows[0])
	}
	if res.Rows[3].Changes["variant_price"].To != int64(10000) || res.Rows[3].Changes["stock"].To != int64(3) {
		t.Errorf("create_variant must default to product price: %+v", res.Rows[3].Changes)
	}
	if res.Rows[4].Slug != "sandal-baru" || res.Rows[4].Changes["title"].To != "Sandal Baru" {
		t.Errorf("create row %+v", res.Rows[4])
	}
	if res.Summary != (importSummary{Total: 5, Create: 1, CreateVariant: 1, Update: 1, Unchanged: 2}) {
		t.Errorf("summary %+v", res.Summary)
	}
}

func TestImportPlanProductWithoutVariantsRejectsVariantColumns(t *testing.T) {
	db := newFakeImportDB()
	bare := db.addProduct("bare", "Bare", 5000)
	res := planCSV(t, db, importTypeProducts, "sku,product_id,title,stock\n,"+idStr(bare)+",Bare Renamed,4\n")
	if res.Rows[0].Action != importActionReject || errCodes(res.Rows[0]) != importCodeNoVariant {
		t.Fatalf("row %+v", res.Rows[0])
	}
	res = planCSV(t, db, importTypeProducts, "sku,product_id,title\n,"+idStr(bare)+",Bare Renamed\n")
	if res.Rows[0].Action != importActionUpdate || res.Rows[0].Changes["title"].To != "Bare Renamed" {
		t.Fatalf("product-level update %+v", res.Rows[0])
	}
}

func TestImportPlanStaleStock(t *testing.T) {
	db := newFakeImportDB()
	p := db.addProduct("kaos", "Kaos", 1000)
	db.addVariant(p, "K-1", 1000, 7) // exported at 10, 3 sold since

	// Untouched stock cell (file == baseline): no change despite 10 != 7.
	res := planCSV(t, db, importTypeProducts, "sku,price,stock,stock_baseline\nK-1,1200,10,10\n")
	if res.Rows[0].Action != importActionUpdate || res.Rows[0].Changes["stock"] != (importChange{}) {
		t.Fatalf("baseline-equal stock must be ignored: %+v", res.Rows[0])
	}
	// Edited stock while the live stock moved: STALE_STOCK.
	res = planCSV(t, db, importTypeProducts, "sku,stock,stock_baseline\nK-1,20,10\n")
	if errCodes(res.Rows[0]) != importCodeStaleStock {
		t.Fatalf("want STALE_STOCK: %+v", res.Rows[0])
	}
	// Edited stock while the live stock still equals the baseline: applied.
	res = planCSV(t, db, importTypeProducts, "sku,stock,stock_baseline\nK-1,20,7\n")
	if res.Rows[0].Changes["stock"].To != int64(20) {
		t.Fatalf("want stock change: %+v", res.Rows[0])
	}
	// No baseline in type=products: changing stock is refused.
	res = planCSV(t, db, importTypeProducts, "sku,stock\nK-1,20\n")
	if errCodes(res.Rows[0]) != importCodeStockBaselineMissing {
		t.Fatalf("want STOCK_BASELINE_REQUIRED: %+v", res.Rows[0])
	}
	// type=stock without baseline sets absolute counts; with baseline it is checked.
	res = planCSV(t, db, importTypeStock, "sku,stock\nK-1,20\n")
	if res.Rows[0].Changes["stock"].To != int64(20) {
		t.Fatalf("stock mode absolute: %+v", res.Rows[0])
	}
	res = planCSV(t, db, importTypeStock, "sku,stock,stock_baseline\nK-1,20,10\n")
	if errCodes(res.Rows[0]) != importCodeStaleStock {
		t.Fatalf("stock mode baseline: %+v", res.Rows[0])
	}
}

func TestImportPlanSlugGrouping(t *testing.T) {
	db := newFakeImportDB()
	db.otherSlugs["sepatu-lari"] = true
	db.otherSlugs["topi"] = true
	db.otherSlugs["jaket-2"] = true
	db.addProduct("jaket", "Jaket Lama", 1)

	res := planCSV(t, db, importTypeProducts, "sku,title,slug,price\n"+
		"A-1,Sepatu Lari,,100\n"+ // derived slug taken by other tenant -> -2
		"A-2,Sepatu Lari,,100\n"+ // same group
		"B-1,Topi,topi,50\n"+ // explicit slug taken -> SLUG_TAKEN
		"C-1,Kaos Baru,,10\n")
	if res.Rows[0].Slug != "sepatu-lari-2" || res.Rows[1].Slug != "sepatu-lari-2" || res.Rows[0].Action != importActionCreate || res.Rows[1].Action != importActionCreate {
		t.Fatalf("group rows %+v %+v", res.Rows[0], res.Rows[1])
	}
	if _, ok := res.Rows[1].Changes["title"]; ok {
		t.Errorf("product-level changes are reported on the first row only")
	}
	if errCodes(res.Rows[2]) != importCodeSlugTaken {
		t.Errorf("explicit taken slug %+v", res.Rows[2])
	}
	if res.Rows[3].Slug != "kaos-baru" {
		t.Errorf("slug %s", res.Rows[3].Slug)
	}

	// A derived slug equal to an existing tenant product becomes create_variant.
	res = planCSV(t, db, importTypeProducts, "sku,title,price\nJ-1,Jaket,1\n")
	if res.Rows[0].Action != importActionCreateVariant {
		t.Fatalf("want create_variant on existing slug, got %+v", res.Rows[0])
	}

	// Groups colliding with each other and with the DB get distinct slugs: the
	// explicit "topi-baru" keeps its slug, the derived one skips -2 (taken by
	// another tenant) and gets -3.
	db.otherSlugs["topi-baru-2"] = true
	res = planCSV(t, db, importTypeProducts, "sku,title,slug,price\nT-1,Topi Baru,,1\nT-2,Topi X,topi-baru,1\n")
	if res.Rows[0].Slug != "topi-baru-3" || res.Rows[1].Slug != "topi-baru" {
		t.Fatalf("slugs %q %q", res.Rows[0].Slug, res.Rows[1].Slug)
	}
}

func TestImportPlanErrors(t *testing.T) {
	db := newFakeImportDB()
	p := db.addProduct("tas", "Tas", 100)
	v := db.addVariant(p, "TAS-1", 100, 1)
	db.otherSKUs["OTHER-1"] = true
	unknown := pgID()

	cases := []struct {
		name string
		typ  string
		csv  string
		code string
	}{
		{"sku mismatch", importTypeProducts, "sku,variant_id\n1.23E+12," + idStr(v) + "\n", importCodeSKUMismatch},
		{"unknown variant", importTypeProducts, "sku,variant_id\n," + idStr(unknown) + "\n", importCodeUnknownVariant},
		{"missing identity", importTypeProducts, "sku,title\n,X\n", importCodeMissing},
		{"unknown product", importTypeProducts, "sku,product_id\nNEW-9," + idStr(unknown) + "\n", importCodeUnknownProduct},
		{"sku taken", importTypeProducts, "sku,title,price\nOTHER-1,X,1\n", importCodeSKUTaken},
		{"missing title", importTypeProducts, "sku,price\nNEW-2,10\n", importCodeMissingForCreate},
		{"missing price", importTypeProducts, "sku,title\nNEW-3,Baru\n", importCodeMissingForCreate},
		{"invalid number", importTypeProducts, "sku,price\nTAS-1,10.5\n", importCodeInvalidNumber},
		{"negative", importTypeProducts, "sku,variant_price\nTAS-1,-1\n", importCodeNegative},
		{"threshold range", importTypeProducts, "sku,low_stock_threshold\nTAS-1,1000001\n", importCodeOutOfRange},
		{"invalid status", importTypeProducts, "sku,status\nTAS-1,hidden\n", importCodeInvalidStatus},
		{"invalid json", importTypeProducts, "sku,attributes\nTAS-1,[1]\n", importCodeInvalidJSON},
		{"unknown category", importTypeProducts, "sku,category_slug\nTAS-1,nope\n", importCodeUnknownCategory},
		{"bad uuid", importTypeProducts, "sku,variant_id\n,xyz\n", importCodeInvalidUUID},
		{"wrong cell count", importTypeProducts, "sku,title\nTAS-1,a,b\n", importCodeInvalidRow},
		{"stock unknown sku", importTypeStock, "sku,stock\nNOPE,1\n", importCodeUnknownSKU},
		{"stock empty", importTypeStock, "sku,stock\nTAS-1,\n", importCodeMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := planCSV(t, db, tc.typ, tc.csv)
			if res.Rows[0].Action != importActionReject || errCodes(res.Rows[0]) != tc.code {
				t.Fatalf("row %+v, want %s", res.Rows[0], tc.code)
			}
			if len(res.Rows[0].Changes) != 0 {
				t.Fatalf("rejected rows carry no changes")
			}
		})
	}
}

func TestImportPlanDuplicatesAndConflicts(t *testing.T) {
	db := newFakeImportDB()
	p := db.addProduct("tas", "Tas", 100)
	v1 := db.addVariant(p, "TAS-1", 100, 1)
	db.addVariant(p, "TAS-2", 100, 1)

	res := planCSV(t, db, importTypeProducts, "sku,title\nTAS-1,Tas\nTAS-1,Tas\n")
	if errCodes(res.Rows[1]) != importCodeDuplicateSKU {
		t.Errorf("dup sku %+v", res.Rows[1])
	}
	res = planCSV(t, db, importTypeProducts, "sku,variant_id\n,"+idStr(v1)+"\nTAS-1,\n")
	if errCodes(res.Rows[1]) != importCodeDuplicateVariant {
		t.Errorf("same variant twice %+v", res.Rows[1])
	}
	res = planCSV(t, db, importTypeProducts, "sku,title\nTAS-1,Tas A\nTAS-2,Tas B\n")
	if errCodes(res.Rows[1]) != importCodeConflictingValues {
		t.Errorf("conflict %+v", res.Rows[1])
	}
}

func TestImportPlanUnescapesAndAssignsSKU(t *testing.T) {
	db := newFakeImportDB()
	p := db.addProduct("buku", "Buku", 100)
	skuLess := db.addVariant(p, "", 100, 1)
	res := planCSV(t, db, importTypeProducts, "sku,variant_id,title\nBK-1,"+idStr(skuLess)+",'=Buku Baru\n")
	row := res.Rows[0]
	if row.Action != importActionUpdate || row.Changes["title"].To != "=Buku Baru" || row.Changes["sku"].To != "BK-1" {
		t.Fatalf("row %+v", row)
	}
	// Empty stays distinct from zero.
	res = planCSV(t, db, importTypeStock, "sku,variant_id,stock\n,"+idStr(skuLess)+",0\n")
	if res.Rows[0].Changes["stock"].To != int64(0) {
		t.Fatalf("zero stock %+v", res.Rows[0])
	}
}

func TestSameCSVTextIgnoresCarriageReturns(t *testing.T) {
	cases := []struct {
		file, stored string
		same         bool
	}{
		{"a\nb", "a\r\nb", true}, // CRLF reads back as LF
		{"ab", "a\rb", true},     // a lone CR is dropped by encoding/csv
		{"a\nb", "a\nb", true},
		{"a b", "a\nb", false},
		{"ab", "a\nb", false},
	}
	for _, tc := range cases {
		if got := sameCSVText(tc.file, tc.stored); got != tc.same {
			t.Errorf("sameCSVText(%q, %q) = %v, want %v", tc.file, tc.stored, got, tc.same)
		}
	}
}
