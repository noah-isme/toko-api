package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/noah-isme/backend-toko/internal/admin"
	"github.com/noah-isme/backend-toko/internal/analytics"
	"github.com/noah-isme/backend-toko/internal/audit"
	"github.com/noah-isme/backend-toko/internal/campaign"
	"github.com/noah-isme/backend-toko/internal/invoice"
	"github.com/noah-isme/backend-toko/internal/newsletter"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/order"
	"github.com/noah-isme/backend-toko/internal/payment"
	"github.com/noah-isme/backend-toko/internal/platform"
	"github.com/noah-isme/backend-toko/internal/queue"
	"github.com/noah-isme/backend-toko/internal/rbac"
	"github.com/noah-isme/backend-toko/internal/shipping"
	"github.com/noah-isme/backend-toko/internal/voucher"
)

// adminDeps carries the middlewares and handlers mounted under /admin and
// /analytics. A nil Audit (or AnalyticsAudit) means auditing is not installed
// on that group; a nil Idempotency leaves the refund route without the
// Idempotency-Key middleware.
type adminDeps struct {
	RequireAuth    func(http.Handler) http.Handler
	Membership     func(http.Handler) http.Handler
	PlatformAdmin  func(http.Handler) http.Handler
	Audit          func(http.Handler) http.Handler
	AnalyticsAudit func(http.Handler) http.Handler
	Idempotency    func(http.Handler) http.Handler

	Vouchers       *voucher.Handler
	Campaigns      *campaign.Handler
	Shipping       *shipping.Handler
	Orders         *order.AdminHandler
	Notify         *notify.AdminHandler
	Queue          *queue.AdminHandler
	AuditLogs      audit.Handler
	Platform       *platform.Handler
	Catalog        *admin.CatalogHandler
	AdminOrders    *admin.OrdersHandler
	AdminAnalytics *admin.AnalyticsHandler
	AdminReports   *admin.ReportsHandler
	Invoices       *invoice.Handler
	Payments       payment.Webhook
	Newsletter     *newsletter.Handler
	Analytics      *analytics.Handler
	Staff          *rbac.StaffHandler
}

// registerAdminRoutes mounts every /admin endpoint.
//
// Registration contract: every endpoint is an rbac.Guarded value, registered
// with Method (chi's Get/Post/... only accept an http.HandlerFunc):
//
//	a.Method(http.MethodGet, "/path", rbac.Require(rbac.PermX, handler))  // tenant-scoped resource
//	a.Method(http.MethodPost, "/path", rbac.RequireAll(handler, rbac.PermX, rbac.PermY)) // needs every listed permission
//	p.Method(http.MethodGet, "/path", rbac.RequirePlatform(handler))      // platform-global, inside the platform group
//
// rbac.RequireStaff at group level keeps customers out of anything registered
// another way, and TestAdminRoutesAreGuarded fails on any endpoint that is not
// an rbac.Guarded value or is missing from its expected-route table.
func registerAdminRoutes(a chi.Router, d adminDeps) {
	a.Use(d.RequireAuth)
	a.Use(d.Membership)
	if d.Audit != nil {
		// Installed before RequireStaff so every request from a tenant member
		// is recorded with its final status, including staff, platform-admin
		// and permission denials (customers probing /admin). Requests rejected
		// earlier (401, 403 TENANT_FORBIDDEN) have no tenant actor and are not
		// recorded here.
		a.Use(d.Audit)
	}
	a.Use(rbac.RequireStaff)

	const (
		get   = http.MethodGet
		post  = http.MethodPost
		put   = http.MethodPut
		patch = http.MethodPatch
		del   = http.MethodDelete
	)

	// Promotions.
	a.Method(post, "/vouchers", rbac.Require(rbac.PermPromotionsWrite, d.Vouchers.Create))
	a.Method(put, "/vouchers/{code}", rbac.Require(rbac.PermPromotionsWrite, d.Vouchers.Update))
	a.Method(post, "/vouchers/preview", rbac.Require(rbac.PermPromotionsRead, d.Vouchers.Preview))
	a.Method(get, "/vouchers", rbac.Require(rbac.PermPromotionsRead, d.AdminOrders.ListVouchers))
	a.Method(get, "/vouchers/stats", rbac.Require(rbac.PermPromotionsRead, d.AdminOrders.VoucherStats))
	a.Method(del, "/vouchers/{code}", rbac.Require(rbac.PermPromotionsWrite, d.AdminOrders.DeleteVoucher))
	a.Method(post, "/flash-sales", rbac.Require(rbac.PermPromotionsWrite, d.Campaigns.AdminCreate))
	a.Method(get, "/flash-sales", rbac.Require(rbac.PermPromotionsRead, d.Campaigns.AdminList))
	a.Method(get, "/flash-sales/{id}", rbac.Require(rbac.PermPromotionsRead, d.Campaigns.AdminGet))
	a.Method(patch, "/flash-sales/{id}", rbac.Require(rbac.PermPromotionsWrite, d.Campaigns.AdminUpdate))

	// Orders. "/orders/stats" and "/orders/export" are registered before
	// "/orders/{id}" so chi does not treat them as ids. The export file holds
	// customer names and emails, so it also needs customers:read.
	a.Method(get, "/orders", rbac.Require(rbac.PermOrdersRead, d.AdminOrders.ListOrders))
	a.Method(get, "/orders/stats", rbac.Require(rbac.PermOrdersRead, d.AdminOrders.OrderStats))
	a.Method(get, "/orders/export", rbac.RequireAll(d.AdminOrders.ExportOrdersCSV, rbac.PermOrdersRead, rbac.PermCustomersRead))
	a.Method(get, "/orders/{id}", rbac.Require(rbac.PermOrdersRead, d.AdminOrders.GetOrder))
	a.Method(get, "/orders/{id}/invoice", rbac.Require(rbac.PermOrdersRead, d.Invoices.AdminInvoice))
	a.Method(post, "/orders/{id}/shipment", rbac.Require(rbac.PermOrdersWrite, d.Shipping.AdminCreate))
	a.Method(patch, "/orders/{id}/status", rbac.Require(rbac.PermOrdersWrite, d.Orders.PatchStatus))
	// Confirming a manual transfer settles the payment (the order becomes
	// PAID), which PATCH /status can never do; it is a money operation, so it
	// takes the same permission as refunds (owner, admin, manager).
	a.Method(post, "/orders/{id}/confirm-payment", rbac.Require(rbac.PermRefundsWrite, d.Payments.AdminConfirmPayment))

	// Customers, inventory, returns and support.
	a.Method(get, "/customers", rbac.Require(rbac.PermCustomersRead, d.Platform.Customers))
	a.Method(get, "/newsletter/subscribers", rbac.Require(rbac.PermCustomersRead, d.Newsletter.AdminList))
	a.Method(get, "/inventory", rbac.Require(rbac.PermInventoryRead, d.Platform.InventoryList))
	a.Method(patch, "/inventory/{variantId}", rbac.Require(rbac.PermInventoryWrite, d.Platform.InventoryUpdate))
	a.Method(get, "/returns", rbac.Require(rbac.PermReturnsRead, d.Platform.AdminReturnList))
	a.Method(patch, "/returns/{returnId}", rbac.Require(rbac.PermReturnsWrite, d.Platform.AdminReturnStatus))
	refunds := a
	if d.Idempotency != nil {
		refunds = a.With(d.Idempotency)
	}
	refunds.Method(post, "/returns/{returnId}/refund", rbac.Require(rbac.PermRefundsWrite, d.Platform.AdminRefund))
	a.Method(get, "/support/tickets", rbac.Require(rbac.PermSupportRead, d.Platform.AdminTicketList))
	a.Method(get, "/support/tickets/{ticketId}/messages", rbac.Require(rbac.PermSupportRead, d.Platform.AdminTicketMessages))
	a.Method(patch, "/support/tickets/{ticketId}", rbac.Require(rbac.PermSupportWrite, d.Platform.AdminTicketStatus))
	a.Method(post, "/support/tickets/{ticketId}/messages", rbac.Require(rbac.PermSupportWrite, d.Platform.AdminTicketMessage))

	// Settings.
	a.Method(get, "/settings", rbac.Require(rbac.PermSettingsRead, d.Platform.SettingsGet))
	a.Method(patch, "/settings", rbac.Require(rbac.PermSettingsWrite, d.Platform.SettingsUpdate))
	a.Method(post, "/onboarding", rbac.Require(rbac.PermSettingsWrite, d.Platform.Onboarding))

	// Catalog management. The static catalog-ops paths are registered before
	// "/products/{id}" so chi does not treat them as ids. The CSV import can
	// create products and set stock, so it needs both write permissions.
	a.Method(get, "/products", rbac.Require(rbac.PermCatalogRead, d.Catalog.ListProducts))
	a.Method(post, "/products", rbac.Require(rbac.PermCatalogWrite, d.Catalog.CreateProduct))
	a.Method(post, "/products/bulk/price", rbac.Require(rbac.PermCatalogWrite, d.Catalog.BulkPrice))
	a.Method(post, "/products/bulk/stock", rbac.Require(rbac.PermInventoryWrite, d.Catalog.BulkStock))
	a.Method(post, "/products/bulk/status", rbac.Require(rbac.PermCatalogWrite, d.Catalog.BulkStatus))
	a.Method(get, "/products/export", rbac.Require(rbac.PermCatalogRead, d.Catalog.ExportProductsCSV))
	a.Method(post, "/products/import", rbac.RequireAll(d.Catalog.ImportProductsCSV, rbac.PermCatalogWrite, rbac.PermInventoryWrite))
	a.Method(get, "/products/{id}", rbac.Require(rbac.PermCatalogRead, d.Catalog.GetProduct))
	a.Method(patch, "/products/{id}", rbac.Require(rbac.PermCatalogWrite, d.Catalog.UpdateProduct))
	a.Method(del, "/products/{id}", rbac.Require(rbac.PermCatalogWrite, d.Catalog.DeleteProduct))
	a.Method(patch, "/products/{id}/stock", rbac.Require(rbac.PermInventoryWrite, d.Catalog.UpdateProductStock))

	a.Method(get, "/categories", rbac.Require(rbac.PermCatalogRead, d.Catalog.ListCategories))
	a.Method(post, "/categories", rbac.Require(rbac.PermCatalogWrite, d.Catalog.CreateCategory))
	a.Method(patch, "/categories/{id}", rbac.Require(rbac.PermCatalogWrite, d.Catalog.UpdateCategory))
	a.Method(del, "/categories/{id}", rbac.Require(rbac.PermCatalogWrite, d.Catalog.DeleteCategory))

	a.Method(get, "/brands", rbac.Require(rbac.PermCatalogRead, d.Catalog.ListBrands))
	a.Method(post, "/brands", rbac.Require(rbac.PermCatalogWrite, d.Catalog.CreateBrand))
	a.Method(patch, "/brands/{id}", rbac.Require(rbac.PermCatalogWrite, d.Catalog.UpdateBrand))
	a.Method(del, "/brands/{id}", rbac.Require(rbac.PermCatalogWrite, d.Catalog.DeleteBrand))

	// Tenant analytics and reports (format=csv streams a CSV attachment).
	a.Method(get, "/analytics/overview", rbac.Require(rbac.PermAnalyticsRead, d.AdminAnalytics.Overview))
	a.Method(get, "/reports/sales-by-category", rbac.Require(rbac.PermAnalyticsRead, d.AdminReports.SalesByCategory))
	a.Method(get, "/reports/sales-by-product", rbac.Require(rbac.PermAnalyticsRead, d.AdminReports.SalesByProduct))
	a.Method(get, "/reports/conversion", rbac.Require(rbac.PermAnalyticsRead, d.AdminReports.Conversion))
	a.Method(get, "/reports/customers", rbac.Require(rbac.PermAnalyticsRead, d.AdminReports.Customers))

	// Staff management.
	a.Method(get, "/roles", rbac.Require(rbac.PermStaffRead, d.Staff.Roles))
	a.Method(get, "/staff", rbac.Require(rbac.PermStaffRead, d.Staff.List))
	a.Method(post, "/staff", rbac.Require(rbac.PermStaffManage, d.Staff.Assign))
	a.Method(patch, "/staff/{userId}", rbac.Require(rbac.PermStaffManage, d.Staff.ChangeRole))
	a.Method(del, "/staff/{userId}", rbac.Require(rbac.PermStaffManage, d.Staff.Revoke))

	// Platform-global resources: not tenant-scoped, so no tenant role grants
	// them; they require the global users.roles "admin" exactly as before.
	a.Group(func(p chi.Router) {
		p.Use(d.PlatformAdmin)
		p.Method(post, "/webhooks", rbac.RequirePlatform(d.Notify.CreateEndpoint))
		p.Method(put, "/webhooks/{id}", rbac.RequirePlatform(d.Notify.UpdateEndpoint))
		p.Method(get, "/webhooks", rbac.RequirePlatform(d.Notify.ListEndpoints))
		p.Method(del, "/webhooks/{id}", rbac.RequirePlatform(d.Notify.DeleteEndpoint))
		p.Method(get, "/webhook-deliveries", rbac.RequirePlatform(d.Notify.ListDeliveries))
		p.Method(post, "/webhook-deliveries/{id}/replay", rbac.RequirePlatform(d.Notify.ReplayDelivery))
		p.Method(get, "/queue/dlq", rbac.RequirePlatform(d.Queue.ListDLQ))
		p.Method(post, "/queue/dlq/replay", rbac.RequirePlatform(d.Queue.ReplayDLQ))
		p.Method(get, "/queue/stats", rbac.RequirePlatform(d.Queue.Stats))
		p.Method(get, "/audit-logs", rbac.RequirePlatform(d.AuditLogs.List))
	})
}

// registerAnalyticsRoutes mounts /analytics. internal/analytics resolves the
// tenant from the request context (rec-api-analytics), so these routes are
// tenant-scoped and take analytics:read like /admin/analytics/overview.
func registerAnalyticsRoutes(an chi.Router, d adminDeps) {
	an.Use(d.RequireAuth, d.Membership)
	if d.AnalyticsAudit != nil {
		// Before RequireStaff, as on /admin, so denied probes are recorded.
		an.Use(d.AnalyticsAudit)
	}
	an.Use(rbac.RequireStaff)
	an.Method(http.MethodGet, "/sales", rbac.Require(rbac.PermAnalyticsRead, d.Analytics.Sales))
	an.Method(http.MethodGet, "/top-products", rbac.Require(rbac.PermAnalyticsRead, d.Analytics.TopProducts))
	an.Method(http.MethodGet, "/overview", rbac.Require(rbac.PermAnalyticsRead, d.Analytics.Overview))
}
