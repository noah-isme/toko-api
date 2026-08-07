# Loyalty Endpoints

> **Last Updated:** 2026-08-07

Loyalty routes require authentication. All endpoints are scoped to the authenticated user's own profile.

## 1. Get Loyalty Profile

```http
GET /api/v1/loyalty/profile
Authorization: Bearer <token>
```

Returns the authenticated user's loyalty profile including current points, tier, and progress toward the next tier.

**Response:** `200 OK`

```json
{
  "user_id": "user-uuid",
  "points": 3500,
  "tier": "silver",
  "tier_progress": 50,
  "lifetime_points": 8200,
  "joined_at": "2025-06-15T08:00:00Z",
  "next_tier_threshold": 5000,
  "next_tier_name": "gold"
}
```

**Tier values:** `bronze` | `silver` | `gold` | `platinum`

**Tier thresholds:**

| Tier       | Minimum Points | Multiplier |
| ---------- | -------------- | ---------- |
| Bronze     | 0              | 1.0x       |
| Silver     | 1,000          | 1.25x      |
| Gold       | 5,000          | 1.5x       |
| Platinum   | 20,000         | 2.0x       |

---

## 2. List Loyalty Transactions

```http
GET /api/v1/loyalty/transactions?page=1&limit=20&type=earned
Authorization: Bearer <token>
```

Returns a paginated list of point transactions for the authenticated user.

**Query parameters:**

- `page` — page number (default: 1)
- `limit` — page size (default: 10)
- `type` — filter by transaction type: `earned`, `redeemed`, `expired`, `adjusted`, `bonus`

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "txn-uuid",
      "user_id": "user-uuid",
      "type": "earned",
      "points": 150,
      "balance": 3500,
      "description": "Pembelian pesanan ORD-20251208-001",
      "reference_id": "order-uuid",
      "reference_type": "order",
      "created_at": "2025-12-08T10:00:00Z"
    }
  ],
  "meta": {
    "page": 1,
    "limit": 20,
    "total": 42,
    "total_pages": 3
  }
}
```

**Transaction types:** `earned` | `redeemed` | `expired` | `adjusted` | `bonus`

**Reference types:** `order` | `review` | `referral` | `promo` | `manual`

---

## 3. Redeem Reward

```http
POST /api/v1/loyalty/redeem
Content-Type: application/json
Authorization: Bearer <token>
```

Redeems a reward from the loyalty catalog using the user's points.

**Request:**

```json
{
  "reward_id": "voucher-10k"
}
```

- `reward_id` — string, required. ID of the reward from the active catalog.

**Response:** `200 OK`

```json
{
  "success": true,
  "message": "Reward redeemed successfully",
  "remaining_points": 3000
}
```

**Error responses:**

- `400 BAD_REQUEST` with code `INSUFFICIENT_POINTS` — user has fewer points than the reward costs
- `400 BAD_REQUEST` with code `INVALID_REWARD` — reward ID not found or inactive

---

## Error Codes

| Code                | HTTP Status | Description                      |
| ------------------- | ----------- | -------------------------------- |
| `UNAUTHORIZED`      | 401         | Token tidak valid atau expired   |
| `NOT_FOUND`         | 404         | Profil loyalty tidak ditemukan   |
| `INSUFFICIENT_POINTS`| 400        | Poin tidak cukup untuk redeem    |
| `INVALID_REWARD`    | 400         | Reward tidak valid atau nonaktif |
