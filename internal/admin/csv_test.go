package admin

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSVSafeCell(t *testing.T) {
	cases := map[string]string{
		`=HYPERLINK("http://x","y")`: `'=HYPERLINK("http://x","y")`,
		"+1":                         "'+1",
		"-1":                         "'-1",
		"@SUM(A1)":                   "'@SUM(A1)",
		"\tx":                        "'\tx",
		"\rx":                        "'\rx",
		"plain":                      "plain",
		"":                           "",
		"a=b":                        "a=b",
		"'quoted":                    "'quoted",
		"Kopi Luwak ☕":               "Kopi Luwak ☕",
	}
	for in, want := range cases {
		if got := csvSafeCell(in); got != want {
			t.Errorf("csvSafeCell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCSVUnescapeCell(t *testing.T) {
	for _, original := range []string{`=HYPERLINK("http://x","y")`, "+1", "-1", "@SUM(A1)", "\tx", "\rx", "plain", "'quoted", "''=x", "'", ""} {
		if got := csvUnescapeCell(csvSafeCell(original)); got != original {
			t.Errorf("round trip of %q gave %q", original, got)
		}
	}
	// A quote before a non-trigger is user text and stays.
	if got := csvUnescapeCell("'abc"); got != "'abc" {
		t.Errorf("got %q", got)
	}
}

func TestCSVStreamWritesBOMHeaderAndCRLF(t *testing.T) {
	rec := httptest.NewRecorder()
	s, err := newCSVStream(rec, "products-20260101-000000.csv", []string{"sku", "title"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.write([]string{"A-1", csvSafeCell("=1+1")}); err != nil {
		t.Fatal(err)
	}
	if err := s.write([]string{"A,2", "Sepatu Olahraga – Ukuran 42\nbaris dua"}); err != nil {
		t.Fatal(err)
	}
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("content-type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="products-20260101-000000.csv"` {
		t.Errorf("content-disposition %q", cd)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, utf8BOM+"sku,title\r\n") {
		t.Fatalf("body prefix %q", body[:20])
	}
	header, records, delim, err := parseCSVUpload(rec.Body.Bytes(), 10)
	if err != nil || delim != ',' {
		t.Fatalf("parse: %v %q", err, delim)
	}
	if strings.Join(header, "|") != "sku|title" || len(records) != 2 {
		t.Fatalf("header %v records %v", header, records)
	}
	if got := csvUnescapeCell(records[0].Fields[1]); got != "=1+1" {
		t.Errorf("formula cell round trip %q", got)
	}
	if records[1].Fields[0] != "A,2" || records[1].Fields[1] != "Sepatu Olahraga – Ukuran 42\nbaris dua" {
		t.Errorf("quoted fields %q", records[1].Fields)
	}
}

func TestParseCSVUploadDelimiterAndBOM(t *testing.T) {
	data := []byte(utf8BOM + "sku;stock\r\nA-1;5\r\n;;\r\nB-2;0\r\n")
	header, records, delim, err := parseCSVUpload(data, 10)
	if err != nil {
		t.Fatal(err)
	}
	if delim != ';' || header[0] != "sku" {
		t.Fatalf("delim %q header %q", delim, header)
	}
	if len(records) != 2 || records[0].Row != 2 || records[1].Row != 4 {
		t.Fatalf("records %+v (blank row 3 must be skipped but counted)", records)
	}
	if records[1].Fields[1] != "0" {
		t.Errorf("zero must stay %q", records[1].Fields[1])
	}
}

func TestParseCSVUploadRejectsBadFiles(t *testing.T) {
	cases := map[string][]byte{
		"UNSUPPORTED_FILE": []byte("PK\x03\x04rest-of-zip"),
		"INVALID_ENCODING": []byte("sku\n\xff\xfe\n"),
		"EMPTY_FILE":       []byte(utf8BOM + "  \n"),
		"INVALID_CSV":      []byte("sku,title\nA,\"unterminated\n"),
	}
	for code, data := range cases {
		_, _, _, err := parseCSVUpload(data, 10)
		var upErr *csvUploadError
		if !errors.As(err, &upErr) || upErr.Code != code {
			t.Errorf("%s: got %v", code, err)
		}
	}
}

func TestParseCSVUploadRowLimit(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("sku,stock\n")
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&buf, "S-%d,1\n", i)
	}
	if _, records, _, err := parseCSVUpload(buf.Bytes(), 4); err != nil || len(records) != 4 {
		t.Fatalf("4 rows at limit 4: %v %d", err, len(records))
	}
	buf.WriteString("S-x,1\n")
	_, _, _, err := parseCSVUpload(buf.Bytes(), 4)
	var upErr *csvUploadError
	if !errors.As(err, &upErr) || upErr.Code != "TOO_MANY_ROWS" {
		t.Fatalf("got %v", err)
	}
}

func TestValidateHeaders(t *testing.T) {
	required, optional, ignored := []string{"sku"}, []string{"stock", "variant_id"}, []string{"created_at"}
	index, err := validateHeaders([]string{" Stock ", "SKU", "created_at"}, required, optional, ignored)
	if err != nil || index["sku"] != 1 || index["stock"] != 0 {
		t.Fatalf("index %v err %v", index, err)
	}
	cases := map[string][]string{
		"MISSING_HEADER":   {"stock"},
		"DUPLICATE_HEADER": {"sku", "stock", "Stock"},
		"INVALID_HEADER":   {"sku", "stok"}, // renamed column fails closed
	}
	for code, got := range cases {
		_, err := validateHeaders(got, required, optional, ignored)
		var upErr *csvUploadError
		if !errors.As(err, &upErr) || upErr.Code != code {
			t.Errorf("%s: got %v", code, err)
		}
	}
	if _, err := validateHeaders([]string{"sku", ""}, required, optional, ignored); err == nil {
		t.Error("empty header must fail")
	}
}

func TestExportRecordsEscapeTextNotNumbers(t *testing.T) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{csvSafeCell("-5 discount"), "-5"})
	w.Flush()
	if got := buf.String(); got != "'-5 discount,-5\n" {
		t.Errorf("got %q", got)
	}
}
