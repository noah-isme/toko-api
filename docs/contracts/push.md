# Web Push Notification Endpoints

Web push routes require authentication. These endpoints manage browser push subscriptions and user notification preferences.

## 1. Get VAPID Public Key

```http
GET /api/v1/push/vapid-key
Authorization: Bearer <token>
```

Returns the VAPID public key used by the browser's PushManager to create a subscription.

**Response:** `200 OK`

```json
{
  "public_key": "BKddtPeGhS0qV4m3qLqGvZqCqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQqQ"
}
```

---

## 2. Subscribe to Push Notifications

```http
POST /api/v1/push/subscription
Content-Type: application/json
Authorization: Bearer <token>
```

Registers a browser push subscription for the authenticated user.

**Request:**

```json
{
  "endpoint": "https://fcm.googleapis.com/fcm/send/...",
  "keys": {
    "p256dh": "BEx...base64",
    "auth": "abc...base64"
  }
}
```

- `endpoint` — string, required. Browser push service endpoint URL
- `keys.p256dh` — string, required. Base64-encoded public key
- `keys.auth` — string, required. Base64-encoded authentication secret

**Response:** `200 OK`

```json
{
  "success": true,
  "message": "Subscribed successfully"
}
```

---

## 3. Unsubscribe from Push Notifications

```http
DELETE /api/v1/push/subscription?endpoint=https://fcm.googleapis.com/...
Authorization: Bearer <token>
```

Removes a push subscription for the authenticated user.

**Query parameters:**

- `endpoint` — optional. Specific subscription endpoint to remove. Omit to remove all subscriptions for the user.

**Response:** `200 OK`

```json
{
  "success": true,
  "message": "Unsubscribed successfully"
}
```

---

## 4. Get Push Preferences

```http
GET /api/v1/push/preferences
Authorization: Bearer <token>
```

Returns the authenticated user's push notification preferences.

**Response:** `200 OK`

```json
{
  "enabled": true,
  "types": {
    "order_update": true,
    "promo_updates": true,
    "stock_updates": false
  }
}
```

If the user has no stored preferences, defaults are returned (all types enabled).

---

## 5. Update Push Preferences

```http
PATCH /api/v1/push/preferences
Content-Type: application/json
Authorization: Bearer <token>
```

Updates the authenticated user's push notification preferences.

**Request:**

```json
{
  "enabled": true,
  "types": {
    "order_update": true,
    "promo_updates": false,
    "stock_updates": true
  }
}
```

- `enabled` — boolean, optional. Master toggle for all push notifications
- `types.order_update` — boolean, optional. Order status changes
- `types.promo_updates` — boolean, optional. Promotional notifications
- `types.stock_updates` — boolean, optional. Back-in-stock alerts

All fields are optional; omitted fields retain their current values.

**Response:** `200 OK`

Returns the updated preferences object.

---

## 6. Send Test Notification (Debug)

```http
POST /api/v1/push/send-test
Authorization: Bearer <token>
```

Sends a test push notification to the user's active subscription. For debugging purposes.

**Response:** `200 OK`

```json
{
  "success": true,
  "message": "Test notification sent"
}
```

---

## Notification Types

The backend may send the following push notification types:

| Type             | Description                    |
| ---------------- | ------------------------------ |
| `order_update`   | Order status changed           |
| `price_drop`     | Price drop on watched product  |
| `flash_sale`     | Flash sale started             |
| `new_review`     | New review on purchased product|
| `qa_answered`    | Q&A question was answered      |
| `loyalty_reward` | Loyalty tier or points change  |
| `general`        | General announcements          |

---

## Error Codes

| Code           | HTTP Status | Description                    |
| -------------- | ----------- | ------------------------------ |
| `UNAUTHORIZED` | 401         | Token tidak valid atau expired |
| `MISSING_FIELDS`| 400        | endpoint atau keys tidak lengkap |
