package order

func statusLabel(status string) string {
	switch status {
	case "pending_payment":
		return "Menunggu Pembayaran"
	case "paid":
		return "Dibayar"
	case "packed":
		return "Dikemas"
	case "shipped":
		return "Sedang Dikirim"
	case "out_for_delivery":
		return "Dalam Pengiriman"
	case "delivered":
		return "Selesai"
	case "cancelled":
		return "Dibatalkan"
	default:
		return status
	}
}

func paymentMethodLabel(method string) string {
	switch method {
	case "bank_transfer":
		return "Transfer Bank"
	case "virtual_account":
		return "Virtual Account"
	case "credit_card":
		return "Kartu Kredit"
	case "ewallet_gopay":
		return "GoPay"
	case "ewallet_ovo":
		return "OVO"
	case "ewallet_dana":
		return "DANA"
	default:
		return method
	}
}

func paymentStatusLabel(status string) string {
	switch status {
	case "PAID":
		return "paid"
	case "PENDING":
		return "pending"
	case "FAILED", "EXPIRED":
		return "failed"
	case "REFUNDED":
		return "failed"
	default:
		return "pending"
	}
}
