package payment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Handler exposes HTTP endpoints for payment intents and status polling.
type Handler struct {
	Svc               *Service
	Q                 *dbgen.Queries
	Pool              *pgxpool.Pool
	BankName          string
	BankAccountName   string
	BankAccountNumber string
	QRURL             string
}

type intentReq struct {
	OrderID string `json:"orderId"`
	Channel string `json:"channel"`
}

type intentResp struct {
	Provider    string     `json:"provider"`
	Token       string     `json:"token,omitempty"`
	RedirectURL string     `json:"redirectUrl,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
}

// Intent creates (or reuses) a payment intent for the authenticated user's order.
func (h *Handler) Intent(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Svc == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_NOT_CONFIGURED", "payment handler unavailable", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || strings.TrimSpace(userID) == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	var req intentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body", nil)
		return
	}
	req.OrderID = strings.TrimSpace(req.OrderID)
	if req.OrderID == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "orderId is required", nil)
		return
	}
	orderUUID, err := cart.ToUUID(req.OrderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid orderId", nil)
		return
	}
	userUUID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user", nil)
		return
	}
	order, err := h.Q.GetOrderByIDForUser(r.Context(), dbgen.GetOrderByIDForUserParams{ID: orderUUID, UserID: userUUID})
	if err != nil {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
		return
	}
	payment, err := h.Svc.CreateIntent(r.Context(), req.OrderID, order.PricingTotal, req.Channel, h.Svc.CallbackBaseURL)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status = http.StatusGatewayTimeout
		}
		common.JSONError(w, status, "INTENT_FAILED", err.Error(), nil)
		return
	}
	resp := intentResp{
		Provider:    payment.Provider.String,
		Token:       payment.IntentToken.String,
		RedirectURL: payment.RedirectUrl.String,
	}
	if payment.ExpiresAt.Valid {
		t := payment.ExpiresAt.Time
		resp.ExpiresAt = &t
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": resp})
}

// Status reports the consolidated payment status for an order belonging to the authenticated user.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Svc == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_NOT_CONFIGURED", "payment handler unavailable", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || strings.TrimSpace(userID) == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	orderID := strings.TrimSpace(chi.URLParam(r, "orderId"))
	if orderID == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "orderId is required", nil)
		return
	}
	orderUUID, err := cart.ToUUID(orderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid orderId", nil)
		return
	}
	userUUID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user", nil)
		return
	}
	if _, err := h.Q.GetOrderByIDForUser(r.Context(), dbgen.GetOrderByIDForUserParams{ID: orderUUID, UserID: userUUID}); err != nil {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
		return
	}
	status, err := h.Svc.ConsolidatedStatus(r.Context(), orderID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "STATUS_ERROR", err.Error(), nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]string{"status": status}})
}

// Instructions returns merchant-configured payment guidance for an order.
func (h *Handler) Instructions(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil || h.Pool == nil {
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_NOT_CONFIGURED", "payment handler unavailable", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	orderID, err := cart.ToUUID(chi.URLParam(r, "orderId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid orderId", nil)
		return
	}
	userUUID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, 401, "UNAUTHORIZED", "invalid user", nil)
		return
	}
	if _, err := h.Q.GetOrderByIDForUser(r.Context(), dbgen.GetOrderByIDForUserParams{ID: orderID, UserID: userUUID}); err != nil {
		common.JSONError(w, 404, "NOT_FOUND", "order not found", nil)
		return
	}
	var channel, provider string
	_ = h.Pool.QueryRow(r.Context(), `SELECT COALESCE(channel,''),COALESCE(provider,'') FROM payments WHERE order_id=$1 ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&channel, &provider)
	steps := []string{"Gunakan detail pembayaran di bawah ini dan bayar tepat sesuai total pesanan.", "Simpan bukti pembayaran setelah transaksi berhasil.", "Unggah bukti pembayaran di halaman ini agar tim kami dapat memverifikasinya."}
	if channel == "snap" {
		steps = []string{"Klik tombol Lanjut ke Pembayaran untuk memilih bank, e-wallet, atau kartu.", "Selesaikan pembayaran di halaman penyedia pembayaran.", "Jika membayar di luar halaman gateway, unggah bukti pembayaran di bawah ini."}
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"provider": provider, "channel": channel, "steps": steps, "bank": map[string]any{"name": h.BankName, "accountName": h.BankAccountName, "accountNumber": h.BankAccountNumber}, "qrUrl": h.QRURL}})
}

// UploadProof stores a payment proof privately against the authenticated order.
func (h *Handler) UploadProof(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil || h.Pool == nil {
		common.JSONError(w, 500, "PAYMENT_NOT_CONFIGURED", "payment handler unavailable", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, 401, "UNAUTHORIZED", "login required", nil)
		return
	}
	orderID, err := cart.ToUUID(chi.URLParam(r, "orderId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid orderId", nil)
		return
	}
	userUUID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, 401, "UNAUTHORIZED", "invalid user", nil)
		return
	}
	if _, err := h.Q.GetOrderByIDForUser(r.Context(), dbgen.GetOrderByIDForUserParams{ID: orderID, UserID: userUUID}); err != nil {
		common.JSONError(w, 404, "NOT_FOUND", "order not found", nil)
		return
	}
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "payment proof must be a file up to 5MB", nil)
		return
	}
	file, header, err := r.FormFile("proof")
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "proof file is required", nil)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 5<<20+1))
	if err != nil || len(content) > 5<<20 {
		common.JSONError(w, 400, "BAD_REQUEST", "payment proof must be a file up to 5MB", nil)
		return
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var proofID pgtype.UUID
	err = h.Pool.QueryRow(r.Context(), `INSERT INTO payment_proofs(order_id,user_id,filename,content_type,content) VALUES($1,$2,$3,$4,$5) RETURNING id`, orderID, userUUID, header.Filename, contentType, content).Scan(&proofID)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to store payment proof", nil)
		return
	}
	common.JSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": cart.UUIDString(proofID), "orderId": cart.UUIDString(orderID), "filename": header.Filename}})
}
