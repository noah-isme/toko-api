# Dokumentasi API Contract - Toko API

**Version:** 0.2.0  
**Base URL:** `https://api.toko.com` (production) | `http://localhost:8080` (development)  
**Last Updated:** 2025-12-07

## 📋 Table of Contents

- [Authentication](#authentication)
- [Error Handling](#error-handling)
- [Pagination](#pagination)
- [API Endpoints](#api-endpoints)
  - [Authentication & User](#1-authentication--user)
  - [Catalog](#2-catalog)
  - [Cart](#3-cart)
  - [Checkout & Orders](#4-checkout--orders)
  - [User Addresses](#5-user-addresses)
  - [Admin](#6-admin-endpoints)

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

## API Endpoints

## 1. Authentication & User

### 1.1 Register

```http
POST /api/v1/auth/register
Content-Type: application/json
```

**Request:**
```json
{
  "name": "John Doe",
  "email": "john@example.com",
  "password": "SecurePass123!"
}
```

**Response:** `201 Created`
```json
{
  "data": {
    "user": {
      "id": "uuid-here",
      "name": "John Doe",
      "email": "john@example.com",
      "createdAt": "2025-12-07T10:00:00Z"
    },
    "accessToken": "eyJhbGc..."
  }
}
```

**Set-Cookie:** `refresh_token=...; HttpOnly; Secure; SameSite=Lax; Path=/api/v1/auth`

---

### 1.2 Login

```http
POST /api/v1/auth/login
Content-Type: application/json
```

**Request:**
```json
{
  "email": "john@example.com",
  "password": "SecurePass123!"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "user": {
      "id": "uuid-here",
      "name": "John Doe",
      "email": "john@example.com"
    },
    "accessToken": "eyJhbGc..."
  }
}
```

**Set-Cookie:** `refresh_token=...`

---

### 1.3 Refresh Token

```http
POST /api/v1/auth/refresh
Cookie: refresh_token=...
```

**Response:** `200 OK`
```json
{
  "data": {
    "accessToken": "eyJhbGc..."
  }
}
```

---

### 1.4 Logout

```http
POST /api/v1/auth/logout
Authorization: Bearer <token>
Cookie: refresh_token=...
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "Logged out successfully"
  }
}
```

**Set-Cookie:** `refresh_token=; Max-Age=0` (clears cookie)

---

### 1.5 Get Current User

```http
GET /api/v1/auth/me
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": {
    "id": "uuid-here",
    "name": "John Doe",
    "email": "john@example.com",
    "emailVerified": false,
    "createdAt": "2025-12-07T10:00:00Z"
  }
}
```

---

### 1.6 Forgot Password

```http
POST /api/v1/auth/password/forgot
Content-Type: application/json
```

**Request:**
```json
{
  "email": "john@example.com"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "Password reset email sent"
  }
}
```

---

### 1.7 Reset Password

```http
POST /api/v1/auth/password/reset
Content-Type: application/json
```

**Request:**
```json
{
  "token": "reset-token-from-email",
  "newPassword": "NewSecurePass123!"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "Password reset successfully"
  }
}
```

---

## 2. Catalog

### 2.1 List Categories

```http
GET /api/v1/categories
```

**Response:** `200 OK`
```json
{
  "data": [
    {
      "id": "uuid",
      "name": "Electronics",
      "slug": "electronics",
      "description": "Electronic devices and accessories",
      "imageUrl": "https://cdn.toko.com/categories/electronics.jpg"
    }
  ]
}
```

---

### 2.2 List Brands

```http
GET /api/v1/brands
```

**Response:** `200 OK`
```json
{
  "data": [
    {
      "id": "uuid",
      "name": "Samsung",
      "slug": "samsung",
      "logoUrl": "https://cdn.toko.com/brands/samsung.png"
    }
  ]
}
```

---

### 2.3 List Products

```http
GET /api/v1/products
```

**Query Parameters:**
- `q` (string): Search query
- `category` (string): Filter by category slug
- `brand` (string): Filter by brand slug
- `minPrice` (integer): Minimum price
- `maxPrice` (integer): Maximum price
- `inStock` (boolean): Filter available items only
- `sort` (enum): `price:asc`, `price:desc`, `title:asc`, `title:desc`
- `page` (integer): Page number (default: 1)
- `limit` (integer): Items per page (default: 20, max: 100)

**Example:**
```http
GET /api/v1/products?category=electronics&minPrice=100000&maxPrice=5000000&sort=price:asc&page=1&limit=20
```

**Response:** `200 OK`
```json
{
  "data": [
    {
      "id": "uuid",
      "title": "Samsung Galaxy S24",
      "slug": "samsung-galaxy-s24",
      "description": "Latest flagship smartphone",
      "price": 12000000,
      "originalPrice": 15000000,
      "discountPercent": 20,
      "currency": "IDR",
      "categoryId": "uuid",
      "categoryName": "Smartphones",
      "brandId": "uuid",
      "brandName": "Samsung",
      "imageUrl": "https://cdn.toko.com/products/s24.jpg",
      "images": [
        "https://cdn.toko.com/products/s24-1.jpg",
        "https://cdn.toko.com/products/s24-2.jpg"
      ],
      "stock": 50,
      "inStock": true,
      "rating": 4.8,
      "reviewCount": 125,
      "tags": ["flagship", "5g", "android"],
      "createdAt": "2025-01-01T00:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 150
  }
}
```

**Headers:**
```
X-Total-Count: 150
```

---

### 2.4 Product Detail

```http
GET /api/v1/products/{slug}
```

**Response:** `200 OK`
```json
{
  "data": {
    "id": "uuid",
    "title": "Samsung Galaxy S24",
    "slug": "samsung-galaxy-s24",
    "description": "Latest flagship smartphone with AI features...",
    "price": 12000000,
    "originalPrice": 15000000,
    "discountPercent": 20,
    "currency": "IDR",
    "category": {
      "id": "uuid",
      "name": "Smartphones",
      "slug": "smartphones"
    },
    "brand": {
      "id": "uuid",
      "name": "Samsung",
      "slug": "samsung"
    },
    "images": [
      {
        "url": "https://cdn.toko.com/products/s24-1.jpg",
        "alt": "Samsung Galaxy S24 front view",
        "isPrimary": true
      }
    ],
    "variants": [
      {
        "id": "uuid",
        "name": "8GB/128GB - Black",
        "sku": "S24-8-128-BLK",
        "price": 12000000,
        "stock": 25,
        "attributes": {
          "color": "Black",
          "storage": "128GB",
          "ram": "8GB"
        }
      }
    ],
    "specifications": {
      "Display": "6.2\" AMOLED",
      "Processor": "Snapdragon 8 Gen 3",
      "Camera": "50MP + 12MP + 10MP",
      "Battery": "4000mAh"
    },
    "stock": 50,
    "inStock": true,
    "weight": 167,
    "dimensions": "14.6 x 7.0 x 0.76 cm",
    "rating": 4.8,
    "reviewCount": 125,
    "tags": ["flagship", "5g", "android"],
    "createdAt": "2025-01-01T00:00:00Z",
    "updatedAt": "2025-12-01T00:00:00Z"
  }
}
```

---

### 2.5 Related Products

```http
GET /api/v1/products/{slug}/related
```

**Response:** `200 OK`
```json
{
  "data": [
    {
      "id": "uuid",
      "title": "Samsung Galaxy S24 Plus",
      "slug": "samsung-galaxy-s24-plus",
      "price": 14000000,
      "imageUrl": "https://cdn.toko.com/products/s24plus.jpg",
      "rating": 4.9,
      "inStock": true
    }
  ]
}
```

---

## 3. Cart

### 3.1 Create Cart (Guest)

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

### 3.2 Get Cart

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

### 3.3 Add Item to Cart

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

### 3.4 Update Cart Item Quantity

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

### 3.5 Remove Cart Item

```http
DELETE /api/v1/carts/{cartId}/items/{itemId}
```

**Response:** `200 OK`
Returns updated cart

---

### 3.6 Apply Voucher

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

### 3.7 Remove Voucher

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

### 3.8 Get Shipping Quote

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

### 3.9 Get Tax Quote

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

### 3.10 Merge Guest Cart to User Cart

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

---

## 4. Checkout & Orders

### 4.1 Create Order (Checkout)

```http
POST /api/v1/checkout
Content-Type: application/json
Authorization: Bearer <token>
```

**Request:**
```json
{
  "cartId": "cart-uuid",
  "shippingAddressId": "address-uuid",
  "shippingService": "jne-reg",
  "shippingCost": 15000,
  "paymentMethod": "bank_transfer",
  "notes": "Please call before delivery"
}
```

**Response:** `201 Created`
```json
{
  "data": {
    "orderId": "order-uuid",
    "orderNumber": "ORD-20251207-001",
    "status": "pending_payment",
    "total": 21135000,
    "currency": "IDR",
    "paymentMethod": "bank_transfer",
    "paymentUrl": "https://payment.gateway.com/pay/xxx",
    "paymentExpiry": "2025-12-08T10:00:00Z",
    "createdAt": "2025-12-07T10:00:00Z"
  }
}
```

**Payment Methods:**
- `bank_transfer` - Bank Transfer
- `virtual_account` - Virtual Account
- `credit_card` - Credit Card
- `ewallet_gopay` - GoPay
- `ewallet_ovo` - OVO
- `ewallet_dana` - DANA

**Order Status Flow:**
```
pending_payment → paid → processing → shipped → delivered
                   ↓
                cancelled
```

---

### 4.2 List User Orders

```http
GET /api/v1/orders?page=1&limit=20
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": [
    {
      "id": "order-uuid",
      "orderNumber": "ORD-20251207-001",
      "status": "shipped",
      "statusLabel": "Sedang Dikirim",
      "total": 21135000,
      "currency": "IDR",
      "itemCount": 2,
      "thumbnailUrl": "https://cdn.toko.com/products/s24.jpg",
      "paymentMethod": "bank_transfer",
      "createdAt": "2025-12-07T10:00:00Z",
      "updatedAt": "2025-12-07T12:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 5
  }
}
```

---

### 4.3 Get Order Detail

```http
GET /api/v1/orders/{orderId}
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": {
    "id": "order-uuid",
    "orderNumber": "ORD-20251207-001",
    "status": "shipped",
    "statusLabel": "Sedang Dikirim",
    "user": {
      "id": "user-uuid",
      "name": "John Doe",
      "email": "john@example.com"
    },
    "items": [
      {
        "id": "item-uuid",
        "productId": "product-uuid",
        "productTitle": "Samsung Galaxy S24",
        "productSlug": "samsung-galaxy-s24",
        "variantName": "8GB/128GB - Black",
        "qty": 2,
        "unitPrice": 12000000,
        "subtotal": 24000000,
        "imageUrl": "https://cdn.toko.com/products/s24.jpg"
      }
    ],
    "shippingAddress": {
      "receiverName": "John Doe",
      "phone": "+6281234567890",
      "addressLine1": "Jl. Sudirman No. 123",
      "addressLine2": "Apt 45B",
      "city": "Jakarta Selatan",
      "province": "DKI Jakarta",
      "postalCode": "12190",
      "country": "Indonesia"
    },
    "pricing": {
      "subtotal": 24000000,
      "discount": 4800000,
      "tax": 1920000,
      "shipping": 15000,
      "total": 21135000
    },
    "voucher": {
      "code": "DISC20",
      "discount": 4800000
    },
    "shipping": {
      "courier": "JNE",
      "service": "REG",
      "trackingNumber": "JP1234567890",
      "estimatedDelivery": "2-3 hari",
      "shippedAt": "2025-12-07T14:00:00Z"
    },
    "payment": {
      "method": "bank_transfer",
      "methodLabel": "Transfer Bank",
      "status": "paid",
      "paidAt": "2025-12-07T11:30:00Z",
      "paymentUrl": "https://payment.gateway.com/pay/xxx",
      "expiryAt": "2025-12-08T10:00:00Z"
    },
    "notes": "Please call before delivery",
    "currency": "IDR",
    "createdAt": "2025-12-07T10:00:00Z",
    "updatedAt": "2025-12-07T14:00:00Z",
    "statusHistory": [
      {
        "status": "pending_payment",
        "timestamp": "2025-12-07T10:00:00Z"
      },
      {
        "status": "paid",
        "timestamp": "2025-12-07T11:30:00Z"
      },
      {
        "status": "processing",
        "timestamp": "2025-12-07T13:00:00Z"
      },
      {
        "status": "shipped",
        "timestamp": "2025-12-07T14:00:00Z"
      }
    ]
  }
}
```

---

### 4.4 Cancel Order

```http
POST /api/v1/orders/{orderId}/cancel
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": {
    "orderId": "order-uuid",
    "status": "cancelled",
    "message": "Order cancelled successfully"
  }
}
```

**Notes:**
- Hanya bisa cancel order dengan status `pending_payment` atau `paid`
- Order yang sudah `processing`, `shipped`, atau `delivered` tidak bisa dicancel

---

### 4.5 Get Shipment Detail

```http
GET /api/v1/orders/{orderId}/shipment
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": {
    "orderId": "order-uuid",
    "trackingNumber": "JP1234567890",
    "courier": "JNE",
    "service": "REG",
    "status": "on_delivery",
    "statusLabel": "Dalam Pengiriman",
    "estimatedDelivery": "2-3 hari",
    "shippedAt": "2025-12-07T14:00:00Z",
    "tracking": [
      {
        "timestamp": "2025-12-07T14:00:00Z",
        "status": "picked_up",
        "location": "Jakarta Warehouse",
        "description": "Paket telah diambil kurir"
      },
      {
        "timestamp": "2025-12-07T16:30:00Z",
        "status": "in_transit",
        "location": "Jakarta Distribution Center",
        "description": "Paket dalam perjalanan"
      },
      {
        "timestamp": "2025-12-08T08:00:00Z",
        "status": "on_delivery",
        "location": "Jakarta Selatan",
        "description": "Paket sedang diantar ke alamat tujuan"
      }
    ]
  }
}
```

---

## 5. User Addresses

### 5.1 List Addresses

```http
GET /api/v1/users/me/addresses?page=1&limit=20
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": [
    {
      "id": "address-uuid",
      "label": "Home",
      "receiverName": "John Doe",
      "phone": "+6281234567890",
      "country": "Indonesia",
      "province": "DKI Jakarta",
      "city": "Jakarta Selatan",
      "postalCode": "12190",
      "addressLine1": "Jl. Sudirman No. 123",
      "addressLine2": "Apt 45B",
      "isDefault": true,
      "createdAt": "2025-11-01T00:00:00Z",
      "updatedAt": "2025-11-01T00:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 3
  }
}
```

---

### 5.2 Create Address

```http
POST /api/v1/users/me/addresses
Content-Type: application/json
Authorization: Bearer <token>
```

**Request:**
```json
{
  "label": "Office",
  "receiver_name": "John Doe",
  "phone": "+6281234567890",
  "country": "Indonesia",
  "province": "DKI Jakarta",
  "city": "Jakarta Pusat",
  "postal_code": "10110",
  "address_line1": "Jl. Thamrin No. 1",
  "address_line2": "Tower A, Floor 5",
  "is_default": false
}
```

**Response:** `201 Created`
```json
{
  "data": {
    "id": "new-address-uuid",
    "label": "Office",
    "receiverName": "John Doe",
    "phone": "+6281234567890",
    "country": "Indonesia",
    "province": "DKI Jakarta",
    "city": "Jakarta Pusat",
    "postalCode": "10110",
    "addressLine1": "Jl. Thamrin No. 1",
    "addressLine2": "Tower A, Floor 5",
    "isDefault": false,
    "createdAt": "2025-12-07T10:00:00Z",
    "updatedAt": "2025-12-07T10:00:00Z"
  }
}
```

**Validation Rules:**
- `receiver_name`: required, max 100 characters
- `phone`: required, format Indonesian phone number
- `address_line1`: required, max 255 characters
- `postal_code`: required, numeric, 5 digits

---

### 5.3 Update Address

```http
PATCH /api/v1/users/me/addresses/{addressId}
Content-Type: application/json
Authorization: Bearer <token>
```

**Request:** (semua field optional)
```json
{
  "label": "Home (Updated)",
  "is_default": true
}
```

**Response:** `200 OK`
Returns updated address object

---

### 5.4 Delete Address

```http
DELETE /api/v1/users/me/addresses/{addressId}
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "Address deleted successfully"
  }
}
```

**Notes:**
- Tidak bisa delete default address jika masih ada address lain
- Set address lain sebagai default terlebih dahulu

---

## 6. Admin Endpoints

### 6.1 Create Voucher

```http
POST /api/v1/admin/vouchers
Content-Type: application/json
Authorization: Bearer <admin_token>
```

**Request:**
```json
{
  "code": "DISC20",
  "type": "percentage",
  "value": 20,
  "minSpend": 100000,
  "maxDiscount": 50000,
  "usageLimit": 100,
  "perUserLimit": 1,
  "validFrom": "2025-12-01T00:00:00Z",
  "validUntil": "2025-12-31T23:59:59Z",
  "description": "20% discount up to 50k"
}
```

**Voucher Types:**
- `percentage`: Discount in percentage (value: 1-100)
- `fixed`: Fixed amount discount

**Response:** `201 Created`

---

### 6.2 Update Order Status

```http
PATCH /api/v1/admin/orders/{orderId}/status
Content-Type: application/json
Authorization: Bearer <admin_token>
```

**Request:**
```json
{
  "status": "processing"
}
```

**Valid Status Transitions:**
- `pending_payment` → `paid`, `cancelled`
- `paid` → `processing`, `cancelled`
- `processing` → `shipped`
- `shipped` → `delivered`

**Response:** `200 OK`

---

### 6.3 Create Shipment

```http
POST /api/v1/admin/orders/{orderId}/shipment
Content-Type: application/json
Authorization: Bearer <admin_token>
```

**Request:**
```json
{
  "courier": "jne",
  "service": "REG",
  "trackingNumber": "JP1234567890",
  "estimatedDelivery": "2-3 hari"
}
```

**Response:** `201 Created`

---

## 7. Health & Monitoring

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

---

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

## 8. Rate Limiting

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

---

## 9. Webhooks (untuk Payment Gateway)

### Payment Notification Webhook

```http
POST /api/v1/webhooks/payment/midtrans
Content-Type: application/json
X-Signature: <hmac-signature>
```

**Request dari Payment Gateway:**
```json
{
  "transaction_id": "xxx",
  "order_id": "ORD-20251207-001",
  "payment_type": "bank_transfer",
  "transaction_status": "settlement",
  "gross_amount": "21135000"
}
```

**Response:** `200 OK`

---

## 10. Testing & Development

### Test Credentials

**Test User:**
```
Email: test@example.com
Password: Test123!
```

**Test Admin:**
```
Email: admin@example.com
Password: Admin123!
```

### Test Payment (Sandbox)

**Test Card:**
```
Card Number: 4811 1111 1111 1114
CVV: 123
Expiry: 12/25
```

### Test Vouchers

```
DISC10 - 10% discount, min spend 50k
DISC20 - 20% discount, min spend 100k, max 50k
FREE50K - 50k fixed discount, min spend 200k
```

---

## 11. Best Practices untuk Frontend

### Authentication Flow

1. **Login/Register** → Simpan `accessToken` di memory (state management)
2. **Store** `refresh_token` automatically via HTTP-only cookie
3. **Auto-refresh** token sebelum expired (set interval 14 menit)
4. **Logout** → Clear token dari memory dan call logout endpoint

### Cart Management

1. **Guest User:**
   - Generate `anonId` di localStorage saat pertama kali
   - Create cart dengan `anonId`
   - Simpan `cartId` di localStorage
   
2. **Setelah Login:**
   - Call `/api/v1/carts/merge` dengan `cartId` dari localStorage
   - Replace `cartId` dengan user cart
   - Hapus `anonId` dari localStorage

### Error Handling

```typescript
try {
  const response = await api.post('/api/v1/carts/123/items', payload);
  // handle success
} catch (error) {
  if (error.response?.status === 401) {
    // Token expired, try refresh
    await refreshToken();
    // Retry request
  } else if (error.response?.data?.error?.code === 'OUT_OF_STOCK') {
    // Show out of stock message
    showError('Product is out of stock');
  } else {
    // Generic error
    showError(error.response?.data?.error?.message || 'Something went wrong');
  }
}
```

### Optimistic Updates

Untuk update quantity cart:
```typescript
// 1. Update UI immediately
updateCartItemQtyInState(itemId, newQty);

// 2. Call API
try {
  await api.patch(`/api/v1/carts/${cartId}/items/${itemId}`, { qty: newQty });
} catch (error) {
  // 3. Rollback on error
  revertCartItemQty(itemId);
  showError(error);
}
```

### Pagination

```typescript
const fetchProducts = async (page = 1, limit = 20) => {
  const response = await api.get('/api/v1/products', {
    params: { page, limit, category: 'electronics' }
  });
  
  return {
    products: response.data.data,
    totalPages: Math.ceil(response.data.pagination.totalItems / limit),
    currentPage: response.data.pagination.page
  };
};
```

---

## 12. Changelog

### Version 0.2.0 (2025-12-07)
- ✅ Complete API contract documentation
- ✅ Added error codes reference
- ✅ Added request/response examples
- ✅ Added best practices for frontend

### Version 0.1.0 (2025-11-01)
- Initial API release

---

## Support & Contact

**Backend Team:**
- Email: backend-team@toko.com
- Slack: #backend-support

**API Issues:**
- GitHub: https://github.com/noah-isme/backend-toko/issues

**Documentation Updates:**
- This document is auto-generated from OpenAPI spec
- Last sync: 2025-12-07
