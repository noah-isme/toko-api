# Reviews Endpoints

All review routes are scoped to a product. The `{id}` path parameter is the product UUID.

## 7.1 List Product Reviews

```http
GET /api/v1/products/{id}/reviews?page=1&limit=10
```

Public endpoint. Returns reviews for the product ordered by newest first.

**Query parameters:**

- `page` — page number (default: 1)
- `limit` — page size (default: 10)

**Response:** `200 OK`

```json
[
  {
    "id": "review-uuid",
    "product_id": "product-uuid",
    "user_id": "user-uuid",
    "rating": 5,
    "comment": "Great product!",
    "created_at": "2025-12-07T10:00:00Z",
    "updated_at": "2025-12-07T10:00:00Z",
    "tenant_id": "tenant-uuid"
  }
]
```

Note: the backend currently returns a raw array, not the standard `{ "data": ... }` envelope.

---

## 7.2 Get Review Stats

```http
GET /api/v1/products/{id}/reviews/stats
```

Public endpoint. Returns aggregate rating statistics for the product.

**Response:** `200 OK`

```json
{
  "total_reviews": 42,
  "average_rating": 4.5,
  "count_5_star": 30,
  "count_4_star": 8,
  "count_3_star": 2,
  "count_2_star": 1,
  "count_1_star": 1
}
```

Note: the backend currently returns a plain object, not the standard `{ "data": ... }` envelope.

---

## 7.3 Create Review

```http
POST /api/v1/products/{id}/reviews
Content-Type: application/json
Authorization: Bearer <token>
```

Requires authentication. Creates a review for the specified product on behalf of the authenticated user.

**Request:**

```json
{
  "rating": 5,
  "comment": "Great product!"
}
```

- `rating` — integer, required, 1–5
- `comment` — string, optional

**Response:** `201 Created`

```json
{
  "id": "review-uuid",
  "product_id": "product-uuid",
  "user_id": "user-uuid",
  "rating": 5,
  "comment": "Great product!",
  "created_at": "2025-12-07T10:00:00Z",
  "updated_at": "2025-12-07T10:00:00Z",
  "tenant_id": "tenant-uuid"
}
```

Note: the backend currently returns a plain review object, not the standard `{ "data": ... }` envelope.

---

## 7.4 Delete Review

```http
DELETE /api/v1/products/{id}/reviews
Authorization: Bearer <token>
```

Requires authentication. The endpoint deletes the authenticated user's review for the product and returns `204 No Content`.
