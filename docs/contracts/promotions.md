# Promotions

> **Last Updated:** 2026-08-07

Public storefront discovery endpoints are tenant-scoped by the API tenant resolver:

- `GET /api/v1/vouchers` — active vouchers with rule metadata. Cart checkout remains the eligibility authority.
- `GET /api/v1/flash-sales` — scheduled/active campaigns with server-calculated sale prices, timestamps, and stock.
- Flash-sale quota is reserved atomically when checkout creates an order, committed when payment succeeds, and released when payment expires or the order is cancelled.
- `POST /api/v1/admin/flash-sales` — create a campaign and its product items.
- `GET /api/v1/admin/flash-sales` — list all campaigns with pagination.
- `GET /api/v1/admin/flash-sales/{id}` — get a single campaign with items.
- `PATCH /api/v1/admin/flash-sales/{id}` — update campaign status, dates, and items.

Voucher administration remains available under `/api/v1/admin/vouchers`.