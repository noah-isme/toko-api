# Tenant Operations

Tenant and admin operational contracts:

- `GET /api/v1/admin/customers`
- `GET/PATCH /api/v1/admin/inventory` and `/api/v1/admin/inventory/{variantId}`
- `GET/PATCH /api/v1/admin/settings`
- `POST /api/v1/admin/onboarding`
- `GET /api/v1/tenant`
- `POST /api/v1/onboarding`

All operations are tenant-scoped and require an authenticated tenant member; admin paths additionally require the `admin` role.
