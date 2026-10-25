package admin

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// CSV conventions shared by the catalog/orders exports and the catalog import
// (see docs/contracts/admin.md 6.25-6.27):
//   - export: UTF-8 with a BOM (Excel with Indonesian regional settings garbles
//     BOM-less UTF-8), comma delimiter, CRLF line endings, RFC 4180 quoting via
//     encoding/csv;
//   - import: optional BOM, comma or semicolon delimiter (sniffed on the header
//     line), either line ending;
//   - formula injection: text cells whose first character is = + - @ TAB or CR
//     are prefixed with a single quote on export, and that one quote is removed
//     again on import so a round trip stores the original text. Numeric columns
//     are written raw and never escaped.

const utf8BOM = "\xEF\xBB\xBF"

// csvFormulaTriggers are the leading characters spreadsheet apps evaluate.
func isFormulaTrigger(r rune) bool {
	switch r {
	case '=', '+', '-', '@', '\t', '\r':
		return true
	}
	return false
}

// csvSafeCell neutralises a text cell for export (formula-injection safety).
func csvSafeCell(s string) string {
	if s == "" {
		return s
	}
	first, _ := utf8.DecodeRuneInString(s)
	if isFormulaTrigger(first) {
		return "'" + s
	}
	return s
}

// csvUnescapeCell reverses csvSafeCell: a leading quote followed by a formula
// trigger loses the quote. Any other text is returned unchanged.
func csvUnescapeCell(s string) string {
	if len(s) < 2 || s[0] != '\'' {
		return s
	}
	next, _ := utf8.DecodeRuneInString(s[1:])
	if isFormulaTrigger(next) {
		return s[1:]
	}
	return s
}

// csvStream writes a streamed CSV download.
type csvStream struct {
	w       http.ResponseWriter
	csv     *csv.Writer
	flusher http.Flusher
}

// newCSVStream sets the download headers, writes the BOM and the header row.
// Nothing is sent to the client until the first Flush.
func newCSVStream(w http.ResponseWriter, filename string, columns []string) (*csvStream, error) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, utf8BOM); err != nil {
		return nil, err
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	s := &csvStream{w: w, csv: cw}
	if f, ok := w.(http.Flusher); ok {
		s.flusher = f
	}
	if err := cw.Write(columns); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *csvStream) write(record []string) error { return s.csv.Write(record) }

// flush pushes the buffered rows to the client (once per batch).
func (s *csvStream) flush() error {
	s.csv.Flush()
	if err := s.csv.Error(); err != nil {
		return err
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

// csvUploadError is a file-level parsing failure (400 before any DB work).
type csvUploadError struct {
	Code    string
	Message string
	Row     int    // 1-based record number, 0 when not row-specific
	Header  string // offending header, for header errors
}

func (e *csvUploadError) Error() string { return e.Message }

// csvRecord is one data record with its 1-based row number (header = row 1).
type csvRecord struct {
	Row    int
	Fields []string
}

// parseCSVUpload parses an uploaded CSV held in memory. It strips a BOM,
// rejects non-UTF-8 and zip (xlsx) content, sniffs the delimiter from the
// header line, and returns the header plus every non-blank data record.
// Records consisting only of empty cells are skipped but still counted, so the
// reported row numbers match the spreadsheet rows. More than maxRows data
// records is an error.
func parseCSVUpload(data []byte, maxRows int) ([]string, []csvRecord, rune, error) {
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return nil, nil, 0, &csvUploadError{Code: "UNSUPPORTED_FILE", Message: "only CSV files are accepted (save the sheet as CSV UTF-8)"}
	}
	data = bytes.TrimPrefix(data, []byte(utf8BOM))
	if !utf8.Valid(data) {
		return nil, nil, 0, &csvUploadError{Code: "INVALID_ENCODING", Message: "file must be UTF-8 encoded (save as CSV UTF-8)"}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, 0, &csvUploadError{Code: "EMPTY_FILE", Message: "file is empty"}
	}
	delimiter := sniffDelimiter(data)
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = false

	header, err := reader.Read()
	if err != nil {
		return nil, nil, 0, &csvUploadError{Code: "INVALID_CSV", Message: "cannot read header row: " + csvErrMessage(err), Row: 1}
	}
	var records []csvRecord
	row := 1
	dataRows := 0
	for {
		fields, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		row++
		if err != nil {
			return nil, nil, 0, &csvUploadError{Code: "INVALID_CSV", Message: fmt.Sprintf("row %d: %s", row, csvErrMessage(err)), Row: row}
		}
		if allEmpty(fields) {
			continue
		}
		dataRows++
		if dataRows > maxRows {
			return nil, nil, 0, &csvUploadError{Code: "TOO_MANY_ROWS", Message: fmt.Sprintf("file has more than %d data rows", maxRows)}
		}
		records = append(records, csvRecord{Row: row, Fields: fields})
	}
	return header, records, delimiter, nil
}

func csvErrMessage(err error) string {
	var parseErr *csv.ParseError
	if errors.As(err, &parseErr) {
		return parseErr.Err.Error()
	}
	return err.Error()
}

// sniffDelimiter picks ';' when the header line has semicolons and no commas
// (Excel with a comma decimal separator, e.g. Indonesian regional settings).
func sniffDelimiter(data []byte) rune {
	line, _ := bufio.NewReader(bytes.NewReader(data)).ReadString('\n')
	if strings.Contains(line, ";") && !strings.Contains(line, ",") {
		return ';'
	}
	return ','
}

func allEmpty(fields []string) bool {
	for _, f := range fields {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// validateHeaders maps each accepted column to its index. Headers are
// compared lower-cased and trimmed, in any order. Unknown, duplicated or
// missing required headers fail closed.
func validateHeaders(got []string, required, optional, ignored []string) (map[string]int, error) {
	allowed := make(map[string]bool, len(required)+len(optional)+len(ignored))
	for _, list := range [][]string{required, optional, ignored} {
		for _, name := range list {
			allowed[name] = true
		}
	}
	index := make(map[string]int, len(got))
	for i, raw := range got {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			return nil, &csvUploadError{Code: "INVALID_HEADER", Message: fmt.Sprintf("header column %d is empty", i+1), Row: 1}
		}
		if !allowed[name] {
			return nil, &csvUploadError{Code: "INVALID_HEADER", Message: fmt.Sprintf("unknown column %q", name), Row: 1, Header: name}
		}
		if _, dup := index[name]; dup {
			return nil, &csvUploadError{Code: "DUPLICATE_HEADER", Message: fmt.Sprintf("column %q appears more than once", name), Row: 1, Header: name}
		}
		index[name] = i
	}
	for _, name := range required {
		if _, ok := index[name]; !ok {
			return nil, &csvUploadError{Code: "MISSING_HEADER", Message: fmt.Sprintf("required column %q is missing", name), Row: 1, Header: name}
		}
	}
	return index, nil
}

// Export column lists (order is part of the API contract).
var (
	productExportColumns = []string{
		"sku", "product_id", "variant_id", "title", "slug", "status", "price", "compare_at",
		"variant_price", "stock", "stock_baseline", "low_stock_threshold", "in_stock", "stock_status",
		"category_slug", "brand_slug", "description", "attributes", "created_at", "updated_at",
	}
	orderExportColumns = []string{
		"order_number", "order_id", "status", "payment_status", "customer_name", "customer_email",
		"currency", "subtotal", "discount", "tax", "shipping", "total", "voucher_code", "items_count",
		"courier", "tracking_number", "created_at", "updated_at",
	}
)
