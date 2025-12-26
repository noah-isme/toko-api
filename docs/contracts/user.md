# User Endpoints

## 5.1 List Addresses

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

## 5.2 Create Address

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

## 5.3 Update Address

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

## 5.4 Delete Address

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
