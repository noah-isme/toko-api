package invoice

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/go-pdf/fpdf"

	"github.com/noah-isme/backend-toko/internal/money"
)

const (
	pageMargin   = 15.0
	contentWidth = 210.0 - 2*pageMargin // A4 portrait
	lineHeight   = 5.0
)

// item table column widths (sum = contentWidth)
var colWidths = [...]float64{80, 30, 14, 28, 28}

// Renderer turns a Document into PDF bytes using the pure-Go fpdf library and
// its core Helvetica font. Text goes through the cp1252 translator, so
// characters outside Latin-1 degrade to '?'.
type Renderer struct {
	// Compress deflates content streams. Tests disable it to search the text.
	Compress bool
}

// NewRenderer returns a Renderer with compression enabled.
func NewRenderer() *Renderer {
	return &Renderer{Compress: true}
}

// Render produces the PDF for doc. A panic inside fpdf (e.g. from a font
// width lookup on unexpected input) is converted to an error so one bad
// field can never take down the request goroutine.
func (r *Renderer) Render(doc Document) (out []byte, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			out, err = nil, fmt.Errorf("invoice: render panic: %v", rec)
		}
	}()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(r == nil || r.Compress)
	pdf.SetMargins(pageMargin, pageMargin, pageMargin)
	pdf.SetAutoPageBreak(true, pageMargin+5)
	if !doc.IssuedAt.IsZero() {
		pdf.SetCreationDate(doc.IssuedAt)
		pdf.SetModificationDate(doc.IssuedAt)
	}
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetTitle(tr("Invoice "+doc.Number), false)
	pdf.SetAuthor(tr(doc.Store.Name), false)
	pdf.SetCreator("toko", false)
	pdf.AliasNbPages("{nb}")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-pageMargin)
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(0, 4, tr(fmt.Sprintf("%s - Halaman %d/{nb}", doc.Number, pdf.PageNo())), "", 0, "C", false, 0, "")
	})
	pdf.AddPage()

	cur := doc.Currency
	m := func(amount int64) string { return tr(money.Format(cur, amount)) }

	// Header: store block (left) and title + number (right).
	top := pdf.GetY()
	pdf.SetTextColor(20, 20, 20)
	pdf.SetFont("Helvetica", "B", 16)
	pdf.MultiCell(110, 7, tr(nonEmpty(doc.Store.Name, "Toko")), "", "L", false)
	pdf.SetFont("Helvetica", "", 9)
	for _, line := range []string{doc.Store.Description, doc.Store.Email, doc.Store.Phone} {
		if line != "" {
			pdf.MultiCell(110, 4.5, tr(line), "", "L", false)
		}
	}
	leftBottom := pdf.GetY()

	pdf.SetXY(pageMargin+110, top)
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(contentWidth-110, 9, "INVOICE", "", 2, "R", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetX(pageMargin + 110)
	pdf.CellFormat(contentWidth-110, 5, tr("No. "+doc.Number), "", 2, "R", false, 0, "")
	pdf.SetX(pageMargin + 110)
	pdf.CellFormat(contentWidth-110, 5, tr("Tanggal terbit: "+formatDate(doc.IssuedAt, doc.Location)), "", 2, "R", false, 0, "")
	if pdf.GetY() < leftBottom {
		pdf.SetY(leftBottom)
	}
	pdf.Ln(3)
	r.rule(pdf)

	// Billing (left) and order meta (right).
	top = pdf.GetY()
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(90, 6, "Ditagihkan kepada", "", 2, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	for _, line := range []string{doc.Customer.Name, doc.Customer.Email} {
		if line != "" {
			pdf.MultiCell(90, 4.5, tr(line), "", "L", false)
		}
	}
	if lines := addressLines(doc.Address); len(lines) > 0 {
		pdf.Ln(1)
		pdf.SetFont("Helvetica", "B", 9)
		pdf.CellFormat(90, 5, "Alamat pengiriman", "", 2, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 9)
		for _, line := range lines {
			pdf.MultiCell(90, 4.5, tr(line), "", "L", false)
		}
	}
	leftBottom = pdf.GetY()

	meta := [][2]string{
		{"No. Pesanan", nonEmpty(doc.OrderNumber, "-")},
		{"Tanggal pesanan", formatDate(doc.OrderedAt, doc.Location)},
		{"Status pembayaran", paymentStatusLabel(doc.PaymentStatus, doc.OrderStatus)},
		{"Metode pembayaran", nonEmpty(doc.PaymentChannel, "-")},
		{"Status pesanan", orderStatusLabel(doc.OrderStatus)},
	}
	pdf.SetXY(pageMargin+95, top)
	for _, kv := range meta {
		pdf.SetX(pageMargin + 95)
		pdf.SetFont("Helvetica", "", 9)
		pdf.CellFormat(38, 5.5, tr(kv[0]), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 9)
		pdf.CellFormat(contentWidth-95-38, 5.5, tr(kv[1]), "", 2, "R", false, 0, "")
	}
	if pdf.GetY() < leftBottom {
		pdf.SetY(leftBottom)
	}
	pdf.Ln(4)

	// Items table.
	headers := [...]string{"Produk", "SKU", "Qty", "Harga", "Subtotal"}
	aligns := [...]string{"L", "L", "R", "R", "R"}
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(240, 240, 240)
	pdf.SetX(pageMargin)
	for i, h := range headers {
		pdf.CellFormat(colWidths[i], 7, h, "B", 0, aligns[i], true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 9)
	for _, it := range doc.Items {
		cells := [...]string{
			tr(it.Title),
			tr(nonEmpty(it.SKU, "-")),
			strconv.FormatInt(int64(it.Qty), 10),
			m(it.UnitPrice),
			m(it.Subtotal),
		}
		// cells[0] is already cp1252 (single-byte), so count lines with the
		// byte-based SplitLines. SplitText decodes UTF-8 runes and would index
		// the 256-entry width table with U+FFFD for any byte above 0x7F.
		titleLines := pdf.SplitLines([]byte(cells[0]), colWidths[0]-2)
		rows := len(titleLines)
		if rows < 1 {
			rows = 1
		}
		height := float64(rows)*lineHeight + 2
		_, pageH := pdf.GetPageSize()
		if pdf.GetY()+height > pageH-pageMargin-5 {
			pdf.AddPage()
		}
		y := pdf.GetY()
		x := pageMargin
		pdf.SetXY(x, y+1)
		pdf.MultiCell(colWidths[0], lineHeight, cells[0], "", "L", false)
		x += colWidths[0]
		for i := 1; i < len(cells); i++ {
			pdf.SetXY(x, y+1)
			pdf.CellFormat(colWidths[i], lineHeight, cells[i], "", 0, aligns[i], false, 0, "")
			x += colWidths[i]
		}
		pdf.SetY(y + height)
		pdf.SetDrawColor(220, 220, 220)
		pdf.Line(pageMargin, pdf.GetY(), pageMargin+contentWidth, pdf.GetY())
	}
	pdf.Ln(3)

	// Totals.
	type totalRow struct {
		label string
		value string
		bold  bool
	}
	discountLabel := "Diskon"
	if doc.VoucherCode != "" {
		discountLabel = "Diskon (" + doc.VoucherCode + ")"
	}
	discount := m(0)
	if doc.Discount != 0 {
		discount = m(-abs(doc.Discount))
	}
	totals := []totalRow{
		{"Subtotal", m(doc.Subtotal), false},
		{discountLabel, discount, false},
		{"Pajak", m(doc.Tax), false},
		{"Ongkos kirim", m(doc.Shipping), false},
		{"Total", m(doc.Total), true},
	}
	labelW, valueW := 45.0, 40.0
	for _, row := range totals {
		style := ""
		if row.bold {
			style = "B"
			pdf.SetDrawColor(20, 20, 20)
			pdf.Line(pageMargin+contentWidth-labelW-valueW, pdf.GetY(), pageMargin+contentWidth, pdf.GetY())
		}
		pdf.SetFont("Helvetica", style, 10)
		pdf.SetX(pageMargin + contentWidth - labelW - valueW)
		pdf.CellFormat(labelW, 6.5, tr(row.label), "", 0, "L", false, 0, "")
		pdf.CellFormat(valueW, 6.5, row.value, "", 1, "R", false, 0, "")
	}

	pdf.Ln(8)
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(100, 100, 100)
	pdf.MultiCell(0, 4, tr("Dokumen ini dibuat secara elektronik dan sah tanpa tanda tangan. Terima kasih telah berbelanja."), "", "L", false)

	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("invoice: render: %w", err)
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("invoice: output: %w", err)
	}
	return buf.Bytes(), nil
}

func (r *Renderer) rule(pdf *fpdf.Fpdf) {
	pdf.SetDrawColor(200, 200, 200)
	pdf.Line(pageMargin, pdf.GetY(), pageMargin+contentWidth, pdf.GetY())
	pdf.Ln(4)
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
