# Catalog Endpoints

## 2.1 List Categories

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

## 2.2 List Brands

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

## 2.3 List Products

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

## 2.4 Product Detail

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

## 2.5 Related Products

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
