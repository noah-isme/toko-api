package invoice

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noah-isme/backend-toko/internal/money"
)

func fixtureDocument() Document {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	return Document{
		Number:      "INV-2026-000001",
		OrderNumber: "ORD-20261001-ABC123",
		IssuedAt:    time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC),
		OrderedAt:   time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC),
		Location:    jakarta,
		Store:       StoreInfo{Name: "Toko Kaos", Email: "halo@tokokaos.id", Phone: "+62 812 0000 0000"},
		Customer:    Customer{Name: "Budi Santoso", Email: "budi@example.com"},
		Address: Address{
			ReceiverName: "Budi Santoso", Phone: "08123456789", Line1: "Jl. Merdeka No. 1",
			City: "Bandung", Province: "Jawa Barat", PostalCode: "40111", Country: "ID",
		},
		Items: []Item{
			{Title: "Kaos Hitam", SKU: "KH-01", Qty: 5, UnitPrice: 249000, Subtotal: 1245000},
		},
		Currency:       "IDR",
		Subtotal:       1245000,
		Shipping:       5000,
		Total:          1250000,
		PaymentStatus:  "PAID",
		PaymentChannel: "bank_transfer",
		OrderStatus:    "PAID",
	}
}

// assertPDFStructure checks the file is a well-formed classic PDF: header,
// trailer, and a startxref offset that points at the xref table whose entry
// count matches the trailer /Size.
func assertPDFStructure(t *testing.T, pdf []byte) {
	t.Helper()
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.")) {
		t.Fatalf("missing PDF header: %q", pdf[:min(len(pdf), 16)])
	}
	if !bytes.HasSuffix(bytes.TrimRight(pdf, "\r\n"), []byte("%%EOF")) {
		t.Fatal("missing EOF trailer")
	}
	m := regexp.MustCompile(`startxref\s+(\d+)\s+%%EOF\s*$`).FindSubmatch(pdf)
	if m == nil {
		t.Fatal("missing startxref")
	}
	off, _ := strconv.Atoi(string(m[1]))
	if off <= 0 || off >= len(pdf) || !bytes.HasPrefix(pdf[off:], []byte("xref")) {
		t.Fatalf("startxref offset %d does not point at xref table", off)
	}
	size := regexp.MustCompile(`/Size (\d+)`).FindSubmatch(pdf)
	header := regexp.MustCompile(`xref\s+0 (\d+)`).FindSubmatch(pdf[off:])
	if size == nil || header == nil || string(size[1]) != string(header[1]) {
		t.Fatalf("xref/trailer size mismatch: %q vs %q", size, header)
	}
}

func TestRenderContainsInvoiceContent(t *testing.T) {
	doc := fixtureDocument()
	pdf, err := (&Renderer{Compress: false}).Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	assertPDFStructure(t, pdf)
	text := string(pdf)
	want := []string{
		"INV-2026-000001",
		"ORD-20261001-ABC123",
		"Kaos Hitam",
		"KH-01",
		"Budi Santoso",
		"budi@example.com",
		"Toko Kaos",
		"Jl. Merdeka No. 1",
		"Bandung, Jawa Barat, 40111",
		"Lunas",
		"bank_transfer",
		"02 Oktober 2026",
		"01 Oktober 2026",
		money.Format("IDR", 249000),
		money.Format("IDR", 1245000),
		money.Format("IDR", 5000),
		// The total string must be exactly money.Format of the stored whole
		// rupiah amount; a reintroduced /100 would print "Rp 12.500".
		money.Format("IDR", doc.Total),
		"Rp 1.250.000",
		"Rp 249.000",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("PDF missing %q", w)
		}
	}
	if strings.Contains(text, "Rp 12.500") {
		t.Error("PDF total looks divided by 100")
	}
}

func TestRenderCompressed(t *testing.T) {
	pdf, err := NewRenderer().Render(fixtureDocument())
	if err != nil {
		t.Fatal(err)
	}
	assertPDFStructure(t, pdf)
	if strings.Contains(string(pdf), "Kaos Hitam") {
		t.Error("compressed output should not contain plain text streams")
	}
}

func TestRenderManyItemsAndDiscount(t *testing.T) {
	doc := fixtureDocument()
	doc.VoucherCode = "HEMAT10"
	doc.Discount = 125000
	doc.Items = nil
	for i := 0; i < 60; i++ {
		doc.Items = append(doc.Items, Item{Title: strings.Repeat("Produk sangat panjang ", 6) + strconv.Itoa(i), Qty: 1, UnitPrice: 1000, Subtotal: 1000})
	}
	pdf, err := (&Renderer{}).Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	assertPDFStructure(t, pdf)
	text := string(pdf)
	if !strings.Contains(text, "HEMAT10") || !strings.Contains(text, "-Rp 125.000") {
		t.Error("discount line missing")
	}
	if !strings.Contains(text, "/Count 3") && !strings.Contains(text, "/Count 4") && !strings.Contains(text, "/Count 5") {
		t.Error("expected a multi-page document")
	}
}

func TestLabels(t *testing.T) {
	cases := map[[2]string]string{
		{"PAID", "PAID"}:          "Lunas",
		{"REFUNDED", "DELIVERED"}: "Dikembalikan",
		{"PENDING", "PAID"}:       "Menunggu pembayaran",
		{"FAILED", "PAID"}:        "Gagal",
		{"EXPIRED", "PAID"}:       "Kedaluwarsa",
		{"", "SHIPPED"}:           "Dikirim",
		{"", "OUT_FOR_DELIVERY"}:  "Dalam pengantaran",
		{"", ""}:                  "-",
	}
	for in, want := range cases {
		if got := paymentStatusLabel(in[0], in[1]); got != want {
			t.Errorf("paymentStatusLabel(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	if got := formatDate(time.Date(2026, 12, 31, 20, 0, 0, 0, time.UTC), time.FixedZone("WIB", 7*3600)); got != "01 Januari 2027" {
		t.Errorf("formatDate tz = %q", got)
	}
	if got := formatDate(time.Time{}, nil); got != "-" {
		t.Errorf("zero date = %q", got)
	}
}
