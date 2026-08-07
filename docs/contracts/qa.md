# Product Q&A Endpoints

> **Last Updated:** 2026-08-07

All Q&A routes are scoped to a product. The `{id}` path parameter is the product UUID.

## 1. List Product Questions

```http
GET /api/v1/products/{id}/questions?page=1&limit=10&sort=recent
```

Public endpoint. Returns questions for the product ordered by newest first.

**Query parameters:**

- `page` — page number (default: 1)
- `limit` — page size (default: 10)
- `sort` — sort order: `recent` (default), `popular`, `unanswered`

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "question-uuid",
      "product_id": "product-uuid",
      "user_id": "user-uuid",
      "question": "Apakah produk ini tahan air?",
      "answer": "Ya, produk ini memiliki rating IP67.",
      "answered_by": "admin-uuid",
      "answered_at": "2025-12-08T10:00:00Z",
      "status": "answered",
      "helpful_count": 12,
      "created_at": "2025-12-07T10:00:00Z"
    }
  ],
  "meta": {
    "page": 1,
    "limit": 10,
    "total": 42,
    "total_pages": 5
  }
}
```

**Status values:** `pending` | `answered` | `rejected`

---

## 2. Ask a Question

```http
POST /api/v1/products/{id}/questions
Content-Type: application/json
Authorization: Bearer <token>
```

Requires authentication.

**Request:**

```json
{
  "question": "Apakah produk ini tersedia dalam warna merah?"
}
```

- `question` — string, required, 10–500 characters

**Response:** `201 Created`

```json
{
  "id": "question-uuid",
  "product_id": "product-uuid",
  "user_id": "user-uuid",
  "question": "Apakah produk ini tersedia dalam warna merah?",
  "answer": null,
  "answered_by": null,
  "answered_at": null,
  "status": "pending",
  "helpful_count": 0,
  "created_at": "2025-12-08T10:00:00Z"
}
```

---

## 3. Answer a Question (Admin)

```http
POST /api/v1/products/{id}/questions/{questionId}/answer
Content-Type: application/json
Authorization: Bearer <token>
```

Requires authentication and admin role.

**Request:**

```json
{
  "answer": "Ya, produk ini tersedia dalam warna merah, biru, dan hitam."
}
```

- `answer` — string, required, 10–1000 characters

**Response:** `200 OK`

Returns the updated `ProductQuestion` object with `status: "answered"`.

---

## 4. Vote on a Question

```http
POST /api/v1/products/{id}/questions/{questionId}/vote
Content-Type: application/json
Authorization: Bearer <token>
```

Requires authentication. Marks the question as helpful (upvote) or clears the vote.

**Request:**

```json
{
  "direction": "up"
}
```

- `direction` — `"up"` to upvote, `"clear"` to remove vote

**Response:** `200 OK`

```json
{
  "helpful_count": 13,
  "my_vote": "up"
}
```

---

## Error Codes

| Code                | HTTP Status | Description                      |
| ------------------- | ----------- | -------------------------------- |
| `UNAUTHORIZED`      | 401         | Token tidak valid atau expired   |
| `NOT_FOUND`         | 404         | Product atau question tidak ada  |
| `BAD_REQUEST`       | 400         | Pertanyaan/jawaban tidak valid   |
| `VALIDATION_ERROR`  | 422         | Validasi input gagal             |
