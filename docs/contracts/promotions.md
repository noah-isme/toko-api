# Promotions

Public storefront discovery endpoints are tenant-scoped by the API tenant resolver:

- `GET /api/v1/vouchers` — active vouchers with rule metadata. Cart checkout remains the eligibility authority.
- `GET /api/v1/flash-sales` — scheduled/active campaigns with server-calculated sale prices, timestamps, and stock.
- `POST /api/v1/admin/flash-sales` — create a campaign and its product items.
- `PATCH /api/v1/admin/flash-sales/{id}` — update campaign status.

Voucher administration remains available under `/api/v1/admin/vouchers`.
