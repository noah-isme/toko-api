package platform

// This package owns the cross-cutting commerce workflows that are not product
// catalog resources: returns/refunds, customer support, tenant onboarding and
// operational inventory views. The SQL is intentionally transactional and
// tenant-scoped at every boundary.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/payment"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type Handler struct {
	Pool      *pgxpool.Pool
	Providers map[string]payment.Provider
}

type returnInput struct {
	Reason string `json:"reason"`
	Notes  string `json:"notes"`
}

type statusInput struct {
	Status string `json:"status"`
}

type ticketInput struct {
	Subject string `json:"subject"`
	Message string `json:"message"`
	OrderID string `json:"orderId"`
}

type messageInput struct {
	Message string `json:"message"`
}

type tenantCreateInput struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type privacyPreferences struct {
	MarketingEmails   bool   `json:"marketingEmails"`
	OrderUpdates      bool   `json:"orderUpdates"`
	SecurityAlerts    bool   `json:"securityAlerts"`
	ProfileVisibility string `json:"profileVisibility"`
	DataProcessing    bool   `json:"dataProcessing"`
	AnalyticsTracking bool   `json:"analyticsTracking"`
}

func defaultPrivacyPreferences() privacyPreferences {
	return privacyPreferences{MarketingEmails: true, OrderUpdates: true, SecurityAlerts: true, ProfileVisibility: "private", DataProcessing: true, AnalyticsTracking: true}
}

func (h *Handler) PrivacyGet(w http.ResponseWriter, r *http.Request) {
	_, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	preferences := defaultPrivacyPreferences()
	err := h.Pool.QueryRow(r.Context(), `SELECT marketing_emails,order_updates,security_alerts,profile_visibility,data_processing,analytics_tracking FROM user_privacy_preferences WHERE user_id=$1`, userID).Scan(&preferences.MarketingEmails, &preferences.OrderUpdates, &preferences.SecurityAlerts, &preferences.ProfileVisibility, &preferences.DataProcessing, &preferences.AnalyticsTracking)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		common.JSONError(w, 500, "INTERNAL", "failed to load privacy preferences", nil)
		return
	}
	common.JSON(w, 200, map[string]any{"data": preferences})
}

func (h *Handler) PrivacyUpdate(w http.ResponseWriter, r *http.Request) {
	_, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	var preferences privacyPreferences
	if json.NewDecoder(r.Body).Decode(&preferences) != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid privacy preferences", nil)
		return
	}
	if preferences.ProfileVisibility != "public" && preferences.ProfileVisibility != "friends" && preferences.ProfileVisibility != "private" {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid profile visibility", nil)
		return
	}
	_, err := h.Pool.Exec(r.Context(), `INSERT INTO user_privacy_preferences(user_id,marketing_emails,order_updates,security_alerts,profile_visibility,data_processing,analytics_tracking,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,now()) ON CONFLICT(user_id) DO UPDATE SET marketing_emails=EXCLUDED.marketing_emails,order_updates=EXCLUDED.order_updates,security_alerts=EXCLUDED.security_alerts,profile_visibility=EXCLUDED.profile_visibility,data_processing=EXCLUDED.data_processing,analytics_tracking=EXCLUDED.analytics_tracking,updated_at=now()`, userID, preferences.MarketingEmails, preferences.OrderUpdates, preferences.SecurityAlerts, preferences.ProfileVisibility, preferences.DataProcessing, preferences.AnalyticsTracking)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to save privacy preferences", nil)
		return
	}
	common.JSON(w, 200, map[string]any{"data": preferences})
}

func (h *Handler) DataExport(w http.ResponseWriter, r *http.Request) {
	_, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	var profile map[string]any
	var id pgtype.UUID
	var name, email string
	var phone pgtype.Text
	var created time.Time
	if err := h.Pool.QueryRow(r.Context(), `SELECT id,name,email,phone,created_at FROM users WHERE id=$1`, userID).Scan(&id, &name, &email, &phone, &created); err != nil {
		common.JSONError(w, 404, "NOT_FOUND", "user not found", nil)
		return
	}
	profile = map[string]any{"id": cart.UUIDString(id), "name": name, "email": email, "phone": nullableText(phone), "createdAt": created}
	orders := make([]map[string]any, 0)
	rows, err := h.Pool.Query(r.Context(), `SELECT id,status::text,currency,pricing_total,created_at FROM orders WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var orderID pgtype.UUID
			var status, currency string
			var total int64
			var createdAt time.Time
			if rows.Scan(&orderID, &status, &currency, &total, &createdAt) == nil {
				orders = append(orders, map[string]any{"id": cart.UUIDString(orderID), "status": status, "currency": currency, "total": total, "createdAt": createdAt})
			}
		}
	}
	common.JSON(w, 200, map[string]any{"data": map[string]any{"profile": profile, "orders": orders, "exportedAt": time.Now().UTC()}})
}

func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	_, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to begin account deletion", nil)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	// Orders remain for accounting, but are detached and their delivery PII is removed.
	if _, err := tx.Exec(r.Context(), `UPDATE orders SET user_id=NULL,shipping_address=NULL,updated_at=now() WHERE user_id=$1`, userID); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to anonymize orders", nil)
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM users WHERE id=$1`, userID); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to delete account", nil)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to commit account deletion", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CreateTenant provisions a new store for the authenticated user and returns
// the tenant id that the client should send as X-Tenant-ID for subsequent
// requests.
func (h *Handler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	userIDString, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, 401, "UNAUTHORIZED", "authentication is required", nil)
		return
	}
	userID, err := cart.ToUUID(userIDString)
	if err != nil {
		common.JSONError(w, 401, "UNAUTHORIZED", "invalid user id", nil)
		return
	}
	var input tenantCreateInput
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	if input.Name == "" || !validTenantSlug(input.Slug) {
		common.JSONError(w, 400, "BAD_REQUEST", "name and a valid slug are required", nil)
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to begin tenant onboarding", nil)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var tenantID pgtype.UUID
	if err := tx.QueryRow(r.Context(), `INSERT INTO tenants(slug,name) VALUES($1,$2) RETURNING id`, input.Slug, input.Name).Scan(&tenantID); err != nil {
		common.JSONError(w, 409, "TENANT_EXISTS", "tenant slug is already in use", nil)
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'OWNER','ACTIVE')`, tenantID, userID); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to create tenant membership", nil)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to commit tenant onboarding", nil)
		return
	}
	w.Header().Set("X-Tenant-ID", cart.UUIDString(tenantID))
	common.JSON(w, 201, map[string]any{"data": map[string]any{"id": cart.UUIDString(tenantID), "slug": input.Slug, "name": input.Name, "role": "OWNER"}})
}

func validTenantSlug(value string) bool {
	if len(value) < 2 || len(value) > 63 {
		return false
	}
	for i, char := range []byte(value) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || (char == '-' && i > 0 && i < len(value)-1) {
			continue
		}
		return false
	}
	return true
}

func (h *Handler) ReturnCreate(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	orderID, err := cart.ToUUID(chi.URLParam(r, "orderId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	var input returnInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.Reason) == "" {
		common.JSONError(w, 400, "BAD_REQUEST", "reason is required", nil)
		return
	}
	var exists pgtype.UUID
	var orderStatus string
	if err := h.Pool.QueryRow(r.Context(), `SELECT id, status::text FROM orders WHERE id=$1 AND user_id=$2 AND tenant_id=$3`, orderID, userID, tenantID).Scan(&exists, &orderStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, 404, "NOT_FOUND", "order not found", nil)
		} else {
			common.JSONError(w, 500, "INTERNAL", "failed to load order", nil)
		}
		return
	}
	if orderStatus != "DELIVERED" && orderStatus != "PAID" && orderStatus != "SHIPPED" {
		common.JSONError(w, 409, "RETURN_NOT_ALLOWED", "order is not eligible for return", nil)
		return
	}
	var id pgtype.UUID
	var status string
	var created time.Time
	err = h.Pool.QueryRow(r.Context(), `INSERT INTO returns(tenant_id,order_id,user_id,reason,notes) VALUES($1,$2,$3,$4,NULLIF($5,'')) RETURNING id,status,created_at`, tenantID, orderID, userID, strings.TrimSpace(input.Reason), strings.TrimSpace(input.Notes)).Scan(&id, &status, &created)
	if err != nil {
		if strings.Contains(err.Error(), "idx_returns_one_open_per_order") {
			common.JSONError(w, 409, "RETURN_EXISTS", "an active return already exists for this order", nil)
			return
		}
		common.JSONError(w, 500, "INTERNAL", "failed to create return", nil)
		return
	}
	common.JSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": cart.UUIDString(id), "orderId": cart.UUIDString(orderID), "status": status, "reason": input.Reason, "createdAt": created}})
}

func (h *Handler) ReturnList(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT id,order_id,status,reason,notes,created_at,updated_at FROM returns WHERE tenant_id=$1 AND user_id=$2 ORDER BY created_at DESC`, tenantID, userID)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to list returns", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, orderID pgtype.UUID
		var status, reason string
		var notes pgtype.Text
		var created, updated time.Time
		if err := rows.Scan(&id, &orderID, &status, &reason, &notes, &created, &updated); err != nil {
			common.JSONError(w, 500, "INTERNAL", "failed to read returns", nil)
			return
		}
		items = append(items, map[string]any{"id": cart.UUIDString(id), "orderId": cart.UUIDString(orderID), "status": status, "reason": reason, "notes": nullableText(notes), "createdAt": created, "updatedAt": updated})
	}
	common.JSON(w, 200, map[string]any{"data": items})
}

func (h *Handler) ReturnGet(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	id, err := cart.ToUUID(chi.URLParam(r, "returnId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid return id", nil)
		return
	}
	var orderID pgtype.UUID
	var status, reason string
	var notes pgtype.Text
	var created, updated time.Time
	err = h.Pool.QueryRow(r.Context(), `SELECT order_id,status,reason,notes,created_at,updated_at FROM returns WHERE id=$1 AND tenant_id=$2 AND user_id=$3`, id, tenantID, userID).Scan(&orderID, &status, &reason, &notes, &created, &updated)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, 404, "NOT_FOUND", "return not found", nil)
		} else {
			common.JSONError(w, 500, "INTERNAL", "failed to load return", nil)
		}
		return
	}
	common.JSON(w, 200, map[string]any{"data": map[string]any{"id": cart.UUIDString(id), "orderId": cart.UUIDString(orderID), "status": status, "reason": reason, "notes": nullableText(notes), "createdAt": created, "updatedAt": updated}})
}

func (h *Handler) AdminReturnList(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT id,order_id,user_id,status,reason,notes,created_at,updated_at FROM returns WHERE tenant_id=$1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to list returns", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, orderID, userID pgtype.UUID
		var status, reason string
		var notes pgtype.Text
		var created, updated time.Time
		if err := rows.Scan(&id, &orderID, &userID, &status, &reason, &notes, &created, &updated); err != nil {
			common.JSONError(w, 500, "INTERNAL", "failed to read returns", nil)
			return
		}
		items = append(items, map[string]any{"id": cart.UUIDString(id), "orderId": cart.UUIDString(orderID), "userId": cart.UUIDString(userID), "status": status, "reason": reason, "notes": nullableText(notes), "createdAt": created, "updatedAt": updated})
	}
	common.JSON(w, 200, map[string]any{"data": items})
}

func (h *Handler) AdminReturnStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	id, err := cart.ToUUID(chi.URLParam(r, "returnId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid return id", nil)
		return
	}
	var input statusInput
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	status := strings.ToUpper(strings.TrimSpace(input.Status))
	allowed := map[string]bool{"APPROVED": true, "REJECTED": true, "RECEIVED": true, "CANCELLED": true}
	if !allowed[status] {
		common.JSONError(w, 400, "BAD_REQUEST", "unsupported return status", nil)
		return
	}
	var updated time.Time
	if err := h.Pool.QueryRow(r.Context(), `UPDATE returns SET status=$1,updated_at=now() WHERE id=$2 AND tenant_id=$3 RETURNING updated_at`, status, id, tenantID).Scan(&updated); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, 404, "NOT_FOUND", "return not found", nil)
		} else {
			common.JSONError(w, 500, "INTERNAL", "failed to update return", nil)
		}
		return
	}
	common.JSON(w, 200, map[string]any{"data": map[string]any{"id": cart.UUIDString(id), "status": status, "updatedAt": updated}})
}

func (h *Handler) AdminRefund(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	returnID, err := cart.ToUUID(chi.URLParam(r, "returnId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid return id", nil)
		return
	}
	var orderID pgtype.UUID
	var status, provider string
	var amount int64
	err = h.Pool.QueryRow(r.Context(), `SELECT r.order_id,r.status::text,COALESCE(p.provider,'midtrans'),o.pricing_total FROM returns r JOIN orders o ON o.id=r.order_id LEFT JOIN payments p ON p.order_id=o.id AND p.status='PAID' WHERE r.id=$1 AND r.tenant_id=$2 ORDER BY p.created_at DESC LIMIT 1`, returnID, tenantID).Scan(&orderID, &status, &provider, &amount)
	if err != nil {
		common.JSONError(w, 404, "NOT_FOUND", "return not found", nil)
		return
	}
	if status != "APPROVED" && status != "RECEIVED" {
		common.JSONError(w, 409, "REFUND_NOT_ALLOWED", "return must be approved or received", nil)
		return
	}
	gateway, exists := h.Providers[strings.ToLower(provider)]
	if !exists {
		common.JSONError(w, 409, "REFUND_UNSUPPORTED", "payment provider does not support refunds", nil)
		return
	}
	refundGateway, ok := gateway.(payment.RefundProvider)
	if !ok {
		common.JSONError(w, 409, "REFUND_UNSUPPORTED", "payment provider does not support refunds", nil)
		return
	}
	providerRef, err := refundGateway.Refund(r.Context(), cart.UUIDString(orderID), amount, "customer return")
	if err != nil {
		common.JSONError(w, 502, "REFUND_FAILED", err.Error(), nil)
		return
	}
	var refundID pgtype.UUID
	if err := h.Pool.QueryRow(r.Context(), `INSERT INTO refunds(tenant_id,return_id,order_id,provider,provider_ref,amount,reason) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(return_id) DO UPDATE SET provider_ref=EXCLUDED.provider_ref RETURNING id`, tenantID, returnID, orderID, provider, providerRef, amount, "customer return").Scan(&refundID); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to record refund", nil)
		return
	}
	_, _ = h.Pool.Exec(r.Context(), `UPDATE returns SET status='REFUNDED',updated_at=now() WHERE id=$1 AND tenant_id=$2`, returnID, tenantID)
	_, _ = h.Pool.Exec(r.Context(), `UPDATE payments SET status='REFUNDED',updated_at=now() WHERE order_id=$1 AND status='PAID'`, orderID)
	common.JSON(w, 200, map[string]any{"data": map[string]any{"id": cart.UUIDString(refundID), "returnId": cart.UUIDString(returnID), "orderId": cart.UUIDString(orderID), "provider": provider, "providerRef": providerRef, "amount": amount, "status": "SUCCEEDED"}})
}

func (h *Handler) TicketCreate(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	var input ticketInput
	if json.NewDecoder(r.Body).Decode(&input) != nil || strings.TrimSpace(input.Subject) == "" || strings.TrimSpace(input.Message) == "" {
		common.JSONError(w, 400, "BAD_REQUEST", "subject and message are required", nil)
		return
	}
	var orderID any
	if input.OrderID != "" {
		parsed, err := cart.ToUUID(input.OrderID)
		if err != nil {
			common.JSONError(w, 400, "BAD_REQUEST", "invalid order id", nil)
			return
		}
		var ownedOrderID pgtype.UUID
		if err := h.Pool.QueryRow(r.Context(), `SELECT id FROM orders WHERE id=$1 AND tenant_id=$2 AND user_id=$3`, parsed, tenantID, userID).Scan(&ownedOrderID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				common.JSONError(w, 404, "NOT_FOUND", "order not found", nil)
			} else {
				common.JSONError(w, 500, "INTERNAL", "failed to validate order", nil)
			}
			return
		}
		orderID = ownedOrderID
	}
	var ticketID pgtype.UUID
	if err := h.Pool.QueryRow(r.Context(), `INSERT INTO support_tickets(tenant_id,user_id,order_id,subject) VALUES($1,$2,$3,$4) RETURNING id`, tenantID, userID, orderID, strings.TrimSpace(input.Subject)).Scan(&ticketID); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to create ticket", nil)
		return
	}
	if _, err := h.Pool.Exec(r.Context(), `INSERT INTO support_messages(ticket_id,author_id,body) VALUES($1,$2,$3)`, ticketID, userID, strings.TrimSpace(input.Message)); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to create ticket message", nil)
		return
	}
	common.JSON(w, 201, map[string]any{"data": map[string]any{"id": cart.UUIDString(ticketID), "subject": input.Subject, "status": "OPEN"}})
}

func (h *Handler) TicketList(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT id,subject,status,priority,order_id,created_at,updated_at FROM support_tickets WHERE tenant_id=$1 AND user_id=$2 ORDER BY updated_at DESC`, tenantID, userID)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to list tickets", nil)
		return
	}
	defer rows.Close()
	h.writeTickets(w, rows)
}

func (h *Handler) TicketMessages(w http.ResponseWriter, r *http.Request) {
	h.ticketMessages(w, r, false)
}

func (h *Handler) AdminTicketList(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT id,subject,status,priority,order_id,created_at,updated_at FROM support_tickets WHERE tenant_id=$1 ORDER BY updated_at DESC`, tenantID)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to list tickets", nil)
		return
	}
	defer rows.Close()
	h.writeTickets(w, rows)
}

func (h *Handler) AdminTicketMessages(w http.ResponseWriter, r *http.Request) {
	h.ticketMessages(w, r, true)
}

func (h *Handler) ticketMessages(w http.ResponseWriter, r *http.Request, admin bool) {
	tenantID, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	ticketID, err := cart.ToUUID(chi.URLParam(r, "ticketId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid ticket id", nil)
		return
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT m.id,m.ticket_id,m.author_id,m.body,m.created_at,
		       CASE WHEN m.author_id=t.user_id THEN 'customer' ELSE 'agent' END
		FROM support_messages m
		JOIN support_tickets t ON t.id=m.ticket_id
		WHERE m.ticket_id=$1 AND t.tenant_id=$2 AND (t.user_id=$3 OR $4)
		ORDER BY m.created_at ASC, m.id ASC`, ticketID, tenantID, userID, admin)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to list ticket messages", nil)
		return
	}
	defer rows.Close()
	messages := make([]map[string]any, 0)
	for rows.Next() {
		var id, messageTicketID, authorID pgtype.UUID
		var body, authorType string
		var created time.Time
		if err := rows.Scan(&id, &messageTicketID, &authorID, &body, &created, &authorType); err != nil {
			common.JSONError(w, 500, "INTERNAL", "failed to read ticket messages", nil)
			return
		}
		messages = append(messages, map[string]any{
			"id":         cart.UUIDString(id),
			"ticketId":   cart.UUIDString(messageTicketID),
			"authorId":   cart.UUIDString(authorID),
			"authorType": authorType,
			"message":    body,
			"createdAt":  created,
		})
	}
	if err := rows.Err(); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to read ticket messages", nil)
		return
	}
	common.JSON(w, 200, map[string]any{"data": messages})
}

func (h *Handler) writeTickets(w http.ResponseWriter, rows pgx.Rows) {
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, orderID pgtype.UUID
		var subject, status, priority string
		var created, updated time.Time
		if err := rows.Scan(&id, &subject, &status, &priority, &orderID, &created, &updated); err != nil {
			common.JSONError(w, 500, "INTERNAL", "failed to read tickets", nil)
			return
		}
		item := map[string]any{"id": cart.UUIDString(id), "subject": subject, "status": status, "priority": priority, "createdAt": created, "updatedAt": updated}
		if orderID.Valid {
			item["orderId"] = cart.UUIDString(orderID)
		}
		items = append(items, item)
	}
	common.JSON(w, 200, map[string]any{"data": items})
}
func (h *Handler) TicketMessage(w http.ResponseWriter, r *http.Request) {
	h.ticketMessage(w, r, false)
}

func (h *Handler) AdminTicketMessage(w http.ResponseWriter, r *http.Request) {
	h.ticketMessage(w, r, true)
}

func (h *Handler) ticketMessage(w http.ResponseWriter, r *http.Request, admin bool) {
	tenantID, userID, ok := h.identity(w, r)
	if !ok {
		return
	}
	ticketID, err := cart.ToUUID(chi.URLParam(r, "ticketId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid ticket id", nil)
		return
	}
	var input messageInput
	if json.NewDecoder(r.Body).Decode(&input) != nil || strings.TrimSpace(input.Message) == "" {
		common.JSONError(w, 400, "BAD_REQUEST", "message is required", nil)
		return
	}
	var messageID pgtype.UUID
	if err := h.Pool.QueryRow(r.Context(), `INSERT INTO support_messages(ticket_id,author_id,body) SELECT $1,$2,$3 WHERE EXISTS(SELECT 1 FROM support_tickets WHERE id=$1 AND tenant_id=$4 AND (user_id=$2 OR $5)) RETURNING id`, ticketID, userID, strings.TrimSpace(input.Message), tenantID, admin).Scan(&messageID); err != nil {
		common.JSONError(w, 404, "NOT_FOUND", "ticket not found", nil)
		return
	}
	nextStatus := "PENDING_AGENT"
	if admin {
		nextStatus = "PENDING_CUSTOMER"
	}
	_, _ = h.Pool.Exec(r.Context(), `UPDATE support_tickets SET status=$1,updated_at=now() WHERE id=$2 AND tenant_id=$3`, nextStatus, ticketID, tenantID)
	common.JSON(w, 201, map[string]any{"data": map[string]any{"id": cart.UUIDString(messageID), "ticketId": cart.UUIDString(ticketID), "message": input.Message}})
}
func (h *Handler) AdminTicketStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	ticketID, err := cart.ToUUID(chi.URLParam(r, "ticketId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid ticket id", nil)
		return
	}
	var input statusInput
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	status := strings.ToUpper(strings.TrimSpace(input.Status))
	allowed := map[string]bool{"OPEN": true, "PENDING_CUSTOMER": true, "PENDING_AGENT": true, "RESOLVED": true, "CLOSED": true}
	if !allowed[status] {
		common.JSONError(w, 400, "BAD_REQUEST", "unsupported ticket status", nil)
		return
	}
	if _, err := h.Pool.Exec(r.Context(), `UPDATE support_tickets SET status=$1,updated_at=now() WHERE id=$2 AND tenant_id=$3`, status, ticketID, tenantID); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to update ticket", nil)
		return
	}
	common.JSON(w, 200, map[string]any{"data": map[string]any{"id": cart.UUIDString(ticketID), "status": status}})
}

func (h *Handler) InventoryList(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT v.id,v.product_id,v.sku,v.price,v.stock,p.title FROM product_variants v JOIN products p ON p.id=v.product_id WHERE p.tenant_id=$1 ORDER BY p.title,v.sku`, tenantID)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to list inventory", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, pid pgtype.UUID
		var sku pgtype.Text
		var price int64
		var stock int32
		var title string
		if err := rows.Scan(&id, &pid, &sku, &price, &stock, &title); err != nil {
			common.JSONError(w, 500, "INTERNAL", "failed to read inventory", nil)
			return
		}
		items = append(items, map[string]any{"variantId": cart.UUIDString(id), "productId": cart.UUIDString(pid), "sku": nullableText(sku), "productTitle": title, "price": price, "stock": stock})
	}
	common.JSON(w, 200, map[string]any{"data": items})
}
func (h *Handler) InventoryUpdate(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	id, err := cart.ToUUID(chi.URLParam(r, "variantId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid variant id", nil)
		return
	}
	var input struct {
		Stock *int32 `json:"stock"`
		Delta *int32 `json:"delta"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || (input.Stock == nil && input.Delta == nil) {
		common.JSONError(w, 400, "BAD_REQUEST", "stock or delta is required", nil)
		return
	}
	var stock int32
	var query string
	if input.Stock != nil {
		if *input.Stock < 0 {
			common.JSONError(w, 400, "BAD_REQUEST", "stock cannot be negative", nil)
			return
		}
		query = `UPDATE product_variants v SET stock=$1 FROM products p WHERE v.id=$2 AND p.id=v.product_id AND p.tenant_id=$3 RETURNING v.stock`
		err = h.Pool.QueryRow(r.Context(), query, *input.Stock, id, tenantID).Scan(&stock)
	} else {
		query = `UPDATE product_variants v SET stock=GREATEST(0,v.stock+$1) FROM products p WHERE v.id=$2 AND p.id=v.product_id AND p.tenant_id=$3 RETURNING v.stock`
		err = h.Pool.QueryRow(r.Context(), query, *input.Delta, id, tenantID).Scan(&stock)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, 404, "NOT_FOUND", "variant not found", nil)
		} else {
			common.JSONError(w, 500, "INTERNAL", "failed to update inventory", nil)
		}
		return
	}
	_, _ = h.Pool.Exec(r.Context(), `UPDATE products SET in_stock=EXISTS(SELECT 1 FROM product_variants WHERE product_id=products.id AND stock>0),updated_at=now() WHERE id=(SELECT product_id FROM product_variants WHERE id=$1)`, id)
	common.JSON(w, 200, map[string]any{"data": map[string]any{"variantId": cart.UUIDString(id), "stock": stock}})
}

func (h *Handler) Customers(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT DISTINCT u.id,u.name,u.email,u.created_at FROM users u JOIN orders o ON o.user_id=u.id WHERE o.tenant_id=$1 ORDER BY u.created_at DESC`, tenantID)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to list customers", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id pgtype.UUID
		var name, email string
		var created time.Time
		if err := rows.Scan(&id, &name, &email, &created); err != nil {
			common.JSONError(w, 500, "INTERNAL", "failed to read customers", nil)
			return
		}
		items = append(items, map[string]any{"id": cart.UUIDString(id), "name": name, "email": email, "createdAt": created})
	}
	common.JSON(w, 200, map[string]any{"data": items})
}

func (h *Handler) Tenant(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	var id pgtype.UUID
	var slug, name, status string
	var settings []byte
	if err := h.Pool.QueryRow(r.Context(), `SELECT id,slug::text,name,status,metadata FROM tenants WHERE id=$1`, tenantID).Scan(&id, &slug, &name, &status, &settings); err != nil {
		common.JSONError(w, 404, "NOT_FOUND", "tenant not found", nil)
		return
	}
	var metadata any
	_ = json.Unmarshal(settings, &metadata)
	common.JSON(w, 200, map[string]any{"data": map[string]any{"id": cart.UUIDString(id), "slug": slug, "name": name, "status": status, "metadata": metadata}})
}
func (h *Handler) SettingsGet(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT key,value FROM tenant_settings WHERE tenant_id=$1 ORDER BY key`, tenantID)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to load settings", nil)
		return
	}
	defer rows.Close()
	settings := map[string]any{}
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			common.JSONError(w, 500, "INTERNAL", "failed to read settings", nil)
			return
		}
		var decoded any
		if json.Unmarshal(value, &decoded) == nil {
			settings[key] = decoded
		}
	}
	common.JSON(w, 200, map[string]any{"data": settings})
}
func (h *Handler) SettingsUpdate(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	var settings map[string]json.RawMessage
	if json.NewDecoder(r.Body).Decode(&settings) != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "settings must be an object", nil)
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to begin settings update", nil)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	for key, value := range settings {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO tenant_settings(tenant_id,key,value,updated_at) VALUES($1,$2,$3,now()) ON CONFLICT(tenant_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, tenantID, key, []byte(value)); err != nil {
			common.JSONError(w, 500, "INTERNAL", "failed to save settings", nil)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to commit settings", nil)
		return
	}
	h.SettingsGet(w, r)
}
func (h *Handler) Onboarding(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	var input map[string]json.RawMessage
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "onboarding payload must be an object", nil)
		return
	}
	if input == nil {
		input = map[string]json.RawMessage{}
	}
	input["completed"] = json.RawMessage(`true`)
	body, _ := json.Marshal(input)
	_, err := h.Pool.Exec(r.Context(), `INSERT INTO tenant_settings(tenant_id,key,value,updated_at) VALUES($1,'onboarding',$2,now()) ON CONFLICT(tenant_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, tenantID, body)
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to save onboarding", nil)
		return
	}
	common.JSON(w, 200, map[string]any{"data": map[string]any{"completed": true, "settings": input}})
}

func (h *Handler) identity(w http.ResponseWriter, r *http.Request) (pgtype.UUID, pgtype.UUID, bool) {
	if h == nil || h.Pool == nil {
		common.JSONError(w, 500, "INTERNAL", "platform service not configured", nil)
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	tenantID, hasTenant := tenant.FromContext(r.Context())
	userID, hasUser := common.UserID(r.Context())
	if !hasTenant || !hasUser {
		common.JSONError(w, 401, "UNAUTHORIZED", "authentication and tenant are required", nil)
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	tID, err := cart.ToUUID(tenantID)
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid tenant id", nil)
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	uID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, 401, "UNAUTHORIZED", "invalid user id", nil)
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	return tID, uID, true
}
func nullableText(value pgtype.Text) any {
	if value.Valid {
		return value.String
	}
	return nil
}
