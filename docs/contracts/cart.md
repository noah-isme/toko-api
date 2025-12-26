# Cart Endpoints

## 3.1 Create Cart (Guest)

```http
POST /api/v1/carts
Content-Type: application/json
```

**Request:**
```json
{
  "anonId": "optional-client-generated-uuid"
}
```

**Response:** `201 Created`
```json
{
  "data": {
    "cartId": "uuid",
    "anonId": "uuid",
    "voucher": null
  }
}
```

**Notes:**
- Jika `anonId` tidak diberikan, server akan generate baru
- Simpan `cartId` dan `anonId` di localStorage untuk guest checkout
- Cart expired setelah 7 hari tidak aktif

---

## 3.2 Get Cart

```http
GET /api/v1/carts/{cartId}
```

**Response:** `200 OK`
```json
{
  "data": {
    "id": "cart-uuid",
    "anonId": "anon-uuid",
    "voucher": "DISC20",
    "items": [
      {
        "id": "item-uuid",
        "productId": "product-uuid",
        "variantId": "variant-uuid",
        "title": "Samsung Galaxy S24",
        "slug": "samsung-galaxy-s24",
        "qty": 2,
        "unitPrice": 12000000,
        "subtotal": 24000000
      }
    ],
    "pricing": {
      "subtotal": 24000000,
      "discount": 4800000,
      "tax": 1920000,
      "shipping": 15000,
      "total": 21135000
    },
    "currency": "IDR"
  }
}
```

---

## 3.3 Add Item to Cart

```http
POST /api/v1/carts/{cartId}/items
Content-Type: application/json
Authorization: Bearer <token> (optional untuk guest)
```

**Request:**
```json
{
  "productId": "product-uuid",
  "variantId": "variant-uuid",
  "qty": 1
}
```

**Response:** `200 OK`
Returns updated cart (sama dengan Get Cart response)

**Error Cases:**
- `OUT_OF_STOCK`: Qty melebihi stock available
- `CART_EXPIRED`: Cart sudah expired
- `NOT_FOUND`: Product/variant tidak ditemukan

---

## 3.4 Update Cart Item Quantity

```http
PATCH /api/v1/carts/{cartId}/items/{itemId}
Content-Type: application/json
```

**Request:**
```json
{
  "qty": 3
}
```

**Response:** `200 OK`
Returns updated cart

---

## 3.5 Remove Cart Item

```http
DELETE /api/v1/carts/{cartId}/items/{itemId}
```

**Response:** `200 OK`
Returns updated cart

---

## 3.6 Apply Voucher

```http
POST /api/v1/carts/{cartId}/apply-voucher
Content-Type: application/json
```

**Request:**
```json
{
  "code": "DISC20"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "discount": 4800000
  }
}
```

**Error Cases:**
- `VOUCHER_INVALID`: Voucher tidak ditemukan, expired, atau sudah habis
- `VOUCHER_MIN_SPEND`: Subtotal tidak memenuhi minimum pembelian
- `VOUCHER_ALREADY_USED`: User sudah menggunakan voucher (jika ada limit per user)

---

## 3.7 Remove Voucher

```http
DELETE /api/v1/carts/{cartId}/voucher
```

**Response:** `200 OK`
```json
{
  "data": {
    "voucher": null
  }
}
```

---

## 3.8 Get Shipping Quote

```http
POST /api/v1/carts/{cartId}/quote/shipping
Content-Type: application/json
```

**Request:**
```json
{
  "destination": "Jakarta Selatan",
  "courier": "jne",
  "weightGram": 500
}
```

**Response:** `200 OK`
```json
{
  "data": [
    {
      "service": "REG",
      "description": "Regular Service",
      "cost": 15000,
      "etd": "2-3 days",
      "note": ""
    },
    {
      "service": "YES",
      "description": "Yakin Esok Sampai",
      "cost": 35000,
      "etd": "1 day",
      "note": ""
    }
  ]
}
```

**Supported Couriers:**
- `jne` - JNE
- `pos` - Pos Indonesia
- `tiki` - TIKI
- `sicepat` - SiCepat
- `jnt` - J&T Express

---

## 3.9 Get Tax Quote

```http
POST /api/v1/carts/{cartId}/quote/tax
```

**Response:** `200 OK`
```json
{
  "data": {
    "tax": 1920000
  }
}
```

**Note:** Tax rate = 10% (1000 basis points)

---

## 3.10 Merge Guest Cart to User Cart

```http
POST /api/v1/carts/merge
Content-Type: application/json
Authorization: Bearer <token>
```

**Request:**
```json
{
  "cartId": "guest-cart-uuid"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "cartId": "user-cart-uuid"
  }
}
```

**Notes:**
- Gunakan endpoint ini setelah user login
- Guest cart akan di-merge ke user cart
- Duplicate items akan di-increment quantity-nya
