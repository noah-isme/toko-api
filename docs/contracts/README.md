# Documentation API Contract - Toko API

**Version:** 0.2.0  
**Base URL:** `https://api.toko.com` (production) | `http://localhost:8080` (development)  
**Last Updated:** 2025-12-07

## 📋 Table of Contents

- [Authentication](auth.md)
- [Catalog](catalog.md)
- [Cart](cart.md)
- [Checkout](checkout.md)
- [Orders](orders.md)
- [User Addresses](user.md)
- [Admin](admin.md)
- [Webhooks](webhooks.md)
- [Testing & Development](testing.md)
- [Frontend Guides](guides.md)

---

## Authentication

### Bearer Token
Gunakan token JWT di header untuk endpoint yang memerlukan autentikasi:

```http
Authorization: Bearer <access_token>
```

### Refresh Token
Refresh token disimpan dalam **HTTP-only cookie** dengan nama `refresh_token`.

**Access Token TTL:** 15 menit  
**Refresh Token TTL:** 30 hari

---

## Error Handling

Semua error response menggunakan format standar:

```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human readable error message",
    "details": {}
  }
}
```

### Common Error Codes

| Code | HTTP Status | Description |
|------|------------|-------------|
| `UNAUTHORIZED` | 401 | Token tidak valid atau expired |
| `FORBIDDEN` | 403 | Tidak memiliki akses ke resource |
| `NOT_FOUND` | 404 | Resource tidak ditemukan |
| `BAD_REQUEST` | 400 | Request payload tidak valid |
| `VALIDATION_ERROR` | 422 | Validasi input gagal |
| `INTERNAL` | 500 | Server error |
| `UNAVAILABLE` | 503 | Service sementara tidak tersedia |
| `CART_EXPIRED` | 404 | Cart sudah expired |
| `OUT_OF_STOCK` | 400 | Produk tidak tersedia |
| `VOUCHER_INVALID` | 400 | Voucher tidak valid atau expired |
| `VOUCHER_MIN_SPEND` | 400 | Tidak memenuhi minimum belanja |

---

## Pagination

Request dengan pagination menggunakan query parameters:

```
?page=1&limit=20
```

Response:

```json
{
  "data": [...],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 150
  }
}
```

**Limits:**
- Default: `20` items per page
- Maximum: `100` items per page

---

## Health & Monitoring

### 7.1 Liveness Probe

```http
GET /health/live
```

**Response:** `200 OK`
```json
{
  "status": "ok"
}
```

### 7.2 Readiness Probe

```http
GET /health/ready
```

**Response:** `200 OK` (service ready) atau `503 Service Unavailable`
```json
{
  "status": "ready",
  "checks": {
    "database": "ok",
    "redis": "ok"
  }
}
```

---

## Rate Limiting

**Rate Limits:**
- **Public endpoints:** 100 requests per minute per IP
- **Authenticated endpoints:** 500 requests per minute per user
- **Admin endpoints:** 1000 requests per minute per admin

**Headers:**
```
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 95
X-RateLimit-Reset: 1733600000
```

**Error Response (429 Too Many Requests):**
```json
{
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Too many requests, please try again later",
    "details": {
      "retryAfter": 60
    }
  }
}
```
