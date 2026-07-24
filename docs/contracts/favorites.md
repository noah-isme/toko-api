# Favorites Endpoints

All favorites endpoints require authentication.

## 8.1 List User Favorites

```http
GET /api/v1/favorites
Authorization: Bearer <token>
```

Returns the authenticated user's favorite products ordered by newest first.

**Response:** `200 OK`

```json
[
  {
    "product_id": "product-uuid",
    "product_name": "Samsung Galaxy S24",
    "product_slug": "samsung-galaxy-s24",
    "price": 12000000,
    "image_url": "https://cdn.toko.com/products/s24.jpg",
    "created_at": "2025-12-07T10:00:00Z"
  }
]
```

Note: the backend currently returns a raw array, not the standard `{ "data": ... }` envelope.

---

## 8.2 Toggle Favorite

```http
POST /api/v1/favorites
Content-Type: application/json
Authorization: Bearer <token>
```

Adds the product to favorites if it is not already favorited; otherwise removes it.

**Request:**

```json
{
  "productId": "product-uuid"
}
```

**Response:** `200 OK`

```json
{
  "favorited": true
}
```

- `favorited: true` — the product was added (or is now favorited)
- `favorited: false` — the product was removed (or is now not favorited)

---

## 8.3 Check Favorite Status

```http
GET /api/v1/favorites/{id}
Authorization: Bearer <token>
```

The `{id}` path parameter is the **product ID**.

**Response:** `200 OK`

```json
{
  "favorited": true
}
```
