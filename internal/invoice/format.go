package invoice

import (
	"fmt"
	"strings"
	"time"
)

var indonesianMonths = [...]string{
	"Januari", "Februari", "Maret", "April", "Mei", "Juni",
	"Juli", "Agustus", "September", "Oktober", "November", "Desember",
}

// formatDate renders t as "25 Oktober 2026" in loc (UTC when nil).
func formatDate(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "-"
	}
	if loc == nil {
		loc = time.UTC
	}
	t = t.In(loc)
	return fmt.Sprintf("%02d %s %d", t.Day(), indonesianMonths[t.Month()-1], t.Year())
}

// paymentStatusLabel maps the latest payment status to Indonesian copy,
// falling back to the order status when no payment row exists.
func paymentStatusLabel(paymentStatus, orderStatus string) string {
	switch strings.ToUpper(strings.TrimSpace(paymentStatus)) {
	case "PAID":
		return "Lunas"
	case "REFUNDED":
		return "Dikembalikan"
	case "PENDING":
		return "Menunggu pembayaran"
	case "FAILED":
		return "Gagal"
	case "EXPIRED":
		return "Kedaluwarsa"
	case "":
		return orderStatusLabel(orderStatus)
	default:
		return paymentStatus
	}
}

// orderStatusLabel maps order statuses to Indonesian copy.
func orderStatusLabel(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PENDING_PAYMENT":
		return "Menunggu pembayaran"
	case "PAID":
		return "Dibayar"
	case "PACKED":
		return "Dikemas"
	case "SHIPPED":
		return "Dikirim"
	case "OUT_FOR_DELIVERY":
		return "Dalam pengantaran"
	case "DELIVERED":
		return "Diterima"
	case "CANCELLED":
		return "Dibatalkan"
	case "":
		return "-"
	default:
		return status
	}
}

// addressLines renders the shipping address as display lines.
func addressLines(a Address) []string {
	lines := make([]string, 0, 5)
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			lines = append(lines, s)
		}
	}
	add(a.ReceiverName)
	add(a.Phone)
	add(a.Line1)
	add(a.Line2)
	add(joinNonEmpty(", ", a.City, a.Province, a.PostalCode))
	add(a.Country)
	return lines
}

func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
