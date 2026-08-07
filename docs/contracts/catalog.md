# Catalog Endpoints

> **Last Updated:** 2026-08-07

## 2.1 List Categories

```http
GET /api/v1/categories
```

**Query Parameters:**

- `page` (integer): Page number (default: 1)
- `limit` (integer): Items per page (default: 20, max: 100)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "uuid",
      "name": "Electronics",
      "slug": "electronics",
      "parentId": null
    }
  ],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 10
  }
}
```

Returns all categories with parent linkage.

---

## 2.2 List Brands

```http
GET /api/v1/brands
```

**Query Parameters:**

- `page` (integer): Page number (default: 1)
- `limit` (integer): Items per page (default: 20, max: 100)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "uuid",
      "name": "Samsung",
      "slug": "samsung"
    }
  ],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 10
  }
}
```

Returns all brands sorted by name.

---

## 2.3 List Products

```http
GET /api/v1/products?q=&category=&brand=&minPrice=&maxPrice=&inStock=&sort=&page=1&limit=20
```

**Query Parameters:**

- `q` (string): Search query
- `category` (string): Category slug filter
- `brand` (string): Brand slug filter
- `minPrice` (integer): Minimum price filter
- `maxPrice` (integer): Maximum price filter
- `inStock` (boolean): Filter by stock availability
- `sort` (string): Sort order: `price:asc`, `price:desc`, `title:asc`, `title:desc`
- `page` (integer): Page number (default: 1)
- `limit` (integer): Items per page (default: 20, max: 100)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "uuid",
      "title": "Samsung Galaxy S24",
      "slug": "samsung-galaxy-s24",
      "price": 12000000,
      "compareAt": 13000000,
      "inStock": true,
      "stock": 42,
      "thumbnail": "https://cdn.toko.com/products/s24.jpg",
      "badges": ["New", "Best Seller"],
      "categoryId": "uuid",
      "categoryName": "Smartphones",
      "brandId": "uuid",
      "brandName": "Samsung",
      "rating": 4.8,
      "reviewCount": 128
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

- `X-Total-Count`: Total number of items

Returns filtered product list with pagination metadata.

---

## 2.4 Get Product Detail

```http
GET /api/v1/products/{slug}
```

**Path Parameters:**

- `slug` (string): Product slug

**Response:** `200 OK`

```json
{
  "data": {
    "id": "uuid",
    "title": "Samsung Galaxy S24",
    "slug": "samsung-galaxy-s24",
    "description": "Latest Samsung flagship...",
    "price": 12000000,
    "compareAt": 13000000,
    "inStock": true,
    "stock": 42,
    "thumbnail": "https://cdn.toko.com/products/s24.jpg",
    "badges": ["New", "Best Seller"],
    "variants": [
      {
        "id": "uuid",
        "sku": "S24-128-BLACK",
        "price": 12000000,
        "stock": 20,
        "attributes": { "color": "Black", "storage": "128GB" }
      }
    ],
    "images": [
      "https://cdn.toko.com/products/s24-1.jpg",
      "https://cdn.toko.com/products/s24-2.jpg"
    ],
    "specs": [
      { "key": "Display", "value": "6.2 inch Dynamic AMOLED 2X" },
      { "key": "Processor", "value": "Exynos 2400" }
    ],
    "brand": { "id": "uuid", "name": "Samsung", "slug": "samsung" },
    "categoryPath": ["electronics", "smartphones"]
  }
}
```

**Response:** `404 Not Found` — Product not found.

Returns full product detail including variants, images, specs, and metadata.

---

## 2.5 Get Related Products

```http
GET /api/v1/products/{slug}/related
```

**Path Parameters:**

- `slug` (string): Product slug

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "uuid",
      "title": "MacBook Air M2",
      "slug": "macbook-air-m2",
      "price": 18000000,
      "thumbnail": "https://cdn.toko.com/products/mba-m2.jpg",
      "rating": 4.8,
      "inStock": true
    }
  ]
}
```

Returns products from the same category as the specified product (excluding the product itself).

---

## 2.6 Personalized Recommendations

```http
GET /api/v1/recommendations/personalized?limit=10
Authorization: Bearer <access_token>
```

**Query Parameters:**

- `limit` (integer): Number of recommendations to return (default: 10, max: 50)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "uuid",
      "title": "Samsung Galaxy S24",
      "slug": "samsung-galaxy-s24",
      "price": 12000000,
      "thumbnail": "https://cdn.toko.com/products/s24.jpg",
      "rating": 4.8,
      "inStock": true
    }
  ]
}
```

**Response:** `401 Unauthorized` — Missing or invalid access token.

Returns personalized product recommendations for the authenticated user based on their browsing history (viewed categories and brands). Falls back to trending products for anonymous users.

---

## 2.7 Trending Products

```http
GET /api/v1/recommendations/trending?limit=10
```

**Query Parameters:**

- `limit` (integer): Number of trending products to return (default: 10, max: 50)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "uuid",
      "title": "iPhone 15 Pro",
      "slug": "iphone-15-pro",
      "price": 25000000,
      "thumbnail": "https://cdn.toko.com/products/iphone15pro.jpg",
      "rating": 4.9,
      "inStock": true
    }
  ]
}
```

Returns currently trending/popular products across the platform, sorted by rating and recency.

---

## 2.8 Frequently Bought Together

```http
GET /api/v1/products/{id}/frequently-bought-together
```

**Path Parameters:**

- `id` (string, UUID or slug): Product identifier (UUID or slug)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "uuid",
      "title": "Phone Case",
      "slug": "phone-case",
      "price": 150000,
      "thumbnail": "https://cdn.toko.com/products/case.jpg",
      "rating": 4.5,
      "inStock": true
    }
  ]
}
```

**Response:** `404 Not Found` — Product not found.

Returns products frequently bought together with the specified product, based on order history analysis. Accepts both UUID and slug as product identifier.

---

## 2.9 Customers Also Viewed

```http
GET /api/v1/products/{id}/also-viewed
```

**Path Parameters:**

- `id` (string, UUID or slug): Product identifier (UUID or slug)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "uuid",
      "title": "Galaxy Buds",
      "slug": "galaxy-buds",
      "price": 2000000,
      "thumbnail": "https://cdn.toko.com/products/buds.jpg",
      "rating": 4.7,
      "inStock": true
    }
  ]
}
```

**Response:** `404 Not Found` — Product not found.

Returns products other customers viewed after viewing the specified product, based on collaborative filtering of user view history. Accepts both UUID and slug as product identifier.