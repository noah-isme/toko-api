# Web Push Notification Endpoints

> **Last Updated:** 2026-10-25

Web push routes require authentication (`Authorization: Bearer <token>`). These endpoints manage browser push subscriptions and user notification preferences, and send real VAPID web push messages (RFC 8030/8291/8292, `aes128gcm`).

Errors use the standard envelope `{ "error": { "code", "message", "details"? } }`.

**Server configuration.** Push is enabled only when both `PUSH_VAPID_PUBLIC_KEY` and `PUSH_VAPID_PRIVATE_KEY` are set (generate them with `make vapid-keys`). Without them the API still boots, `GET /push/vapid-key` and `POST /push/send-test` answer `503 PUSH_DISABLED`, and no event pushes are produced. Subscription and preference routes keep working.

## 1. Get VAPID Public Key

```http
GET /api/v1/push/vapid-key
Authorization: Bearer <token>
```

Returns the VAPID public key (base64url, uncompressed P-256 point) used as `applicationServerKey` by the browser's `PushManager.subscribe`.

**Response:** `200 OK`

```json
{
  "public_key": "BNc...base64url",
  "publicKey": "BNc...base64url"
}
```

Both fields always carry the same value; clients should prefer `publicKey` (`public_key` is kept for older clients).

**Errors:** `503 PUSH_DISABLED` when the server has no VAPID keys; the client should hide or disable push UI.

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

- `endpoint` — string, required, at most 2048 bytes. Browser push service endpoint URL
- `keys.p256dh` — string, required, at most 512 bytes. Base64url-encoded public key
- `keys.auth` — string, required, at most 512 bytes. Base64url-encoded authentication secret

**Endpoint rules.** The endpoint must be an absolute `https://` URL with no credentials whose host is a DNS name (IP literals are rejected) of a known push service. Default allowlist: `fcm.googleapis.com`, `*.push.services.mozilla.com`, `*.notify.windows.com`, `web.push.apple.com`, `*.push.apple.com` (operators can extend it with `PUSH_ENDPOINT_ALLOWED_HOSTS`). Real browser subscriptions always satisfy these rules; mocks and fixtures should use one of these hosts.

**Subscription cap.** At most `PUSH_MAX_SUBSCRIPTIONS_PER_USER` (default 10) subscriptions are kept per user. Subscribing another endpoint silently evicts the one least recently subscribed (by `updated_at`, which every subscribe/re-subscribe of an endpoint refreshes). Re-subscribing an existing endpoint updates its keys.

**Response:** `200 OK`

```json
{
  "success": true,
  "message": "Subscribed successfully"
}
```

**Errors:**

- `400 MISSING_FIELDS` — `endpoint` or a key is empty.
- `400 INVALID_BODY` — malformed JSON, `endpoint` longer than 2048 bytes or a key longer than 512 bytes.
- `400 INVALID_ENDPOINT` — the endpoint breaks the rules above. `details` is the reason only (for example `push: invalid subscription endpoint: scheme must be https`), never the endpoint itself.
- `500 SUBSCRIBE_FAILED` — the subscription could not be stored.

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

No request body. Synchronously sends a real test push to **every** stored subscription of the authenticated user. Per-type preferences are ignored (explicit debug action). The service worker receives:

```json
{ "title": "Notifikasi uji", "body": "Push notification berfungsi.", "tag": "test", "data": { "url": "/account/notifications" } }
```

**Response:** `200 OK` (at least one push service accepted the message)

```json
{
  "success": true,
  "message": "Test notification sent",
  "sent": 1,
  "failed": 0,
  "pruned": 0
}
```

- `sent` — subscriptions accepted by their push service (2xx).
- `failed` — subscriptions rejected (other 4xx, 429, 5xx), unreachable, or not sendable because of a server-side error (for example misconfigured VAPID keys). Failed subscriptions are never deleted.
- `pruned` — subscriptions deleted server-side because they can never receive a push: the push service answered `404`/`410`, the stored endpoint is not https, the stored keys are unusable (`p256dh` is not an uncompressed P-256 point or `auth` is not 16 bytes, in standard or URL-safe base64), or the endpoint host resolves to a non-public address. A client whose own endpoint was pruned should call `POST /push/subscription` again.

**Errors:**

- `401 UNAUTHORIZED`
- `404 NO_SUBSCRIPTION` — the user has no stored subscription; subscribe first.
- `502 PUSH_SEND_FAILED` — no push service accepted the message; `details` is `{ "sent": 0, "failed": <n>, "pruned": <n> }`.
- `500 PUSH_SEND_FAILED` — subscriptions could not be loaded (no details).
- `500 INVALID_USER_ID` — the authenticated user id in the token is not a UUID (should not happen with tokens issued by this API).
- `503 PUSH_DISABLED` — the server has no VAPID keys.
- `429` — rate limited: default 5 calls per user per 60 seconds (`RATE_LIMIT_PUSH_TEST_MAX`, `RATE_LIMIT_PUSH_TEST_WINDOW_SEC`). Plain-text body `rate limit exceeded` with `Retry-After` and `X-RateLimit-*` headers, like the other rate limiters.

---

## Event Pushes

Order, payment and shipment events produce pushes for the order's owner, delivered asynchronously by `cmd/worker` (task kind `push-delivery`):

| Topic                       | Title                    | Body                                                  |
| --------------------------- | ------------------------ | ----------------------------------------------------- |
| `order.paid`                | Pembayaran diterima      | Pembayaran pesanan Anda telah kami terima.            |
| `order.canceled`            | Pesanan dibatalkan       | Pesanan Anda telah dibatalkan.                        |
| `payment.failed`            | Pembayaran gagal         | Pembayaran pesanan Anda gagal diproses.               |
| `payment.expired`           | Pembayaran kedaluwarsa   | Batas waktu pembayaran pesanan Anda telah berakhir.   |
| `shipment.shipped`          | Pesanan dikirim          | Pesanan Anda telah dikirim.                           |
| `shipment.out_for_delivery` | Pesanan dalam pengiriman | Kurir sedang mengantar pesanan Anda.                  |
| `shipment.delivered`        | Pesanan tiba             | Pesanan Anda telah sampai di tujuan.                  |

`order.created` is not pushed. The payload the service worker receives (`event.data.json()`):

```json
{
  "title": "Pembayaran diterima",
  "body": "Pembayaran pesanan Anda telah kami terima.",
  "tag": "order:<orderId>",
  "data": { "url": "/account/orders/<orderId>", "orderId": "<uuid>", "topic": "order.paid" }
}
```

All of these belong to the `order_update` preference category. `enabled = false` suppresses every event push; `types.order_update = false` suppresses these. `promo_updates` and `stock_updates` are reserved (no producer yet). Preferences are checked by the worker at send time.

Delivery is retried with exponential backoff up to `QUEUE_MAX_ATTEMPTS` (default 8) for transport errors, `429` and `5xx`, then parked in the DLQ (`GET /api/v1/admin/queue/dlq?kind=push-delivery`). Subscriptions answering `404`/`410` are deleted; other `4xx` answers are logged and dropped. The DLQ payload is only `{ "eventId": "<uuid>", "topic": "<topic>" }`. Each event is pushed at most once per subscription under normal operation (queue deduplication by event id).

---

## Notification Types

Preference categories. Only `order_update` has a producer today; the other values are reserved names the frontend may display:

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
| `INVALID_BODY` | 400         | JSON tidak valid, endpoint > 2048 byte atau key > 512 byte |
| `INVALID_ENDPOINT` | 400     | endpoint bukan URL https dari layanan push yang dikenal |
| `SUBSCRIBE_FAILED` | 500     | subscription gagal disimpan |
| `NO_SUBSCRIPTION` | 404      | user belum punya subscription (send-test) |
| `PUSH_SEND_FAILED` | 502 / 500 | tidak ada layanan push yang menerima pesan uji / gagal memuat subscription |
| `INVALID_USER_ID` | 500      | user id di token bukan UUID (send-test) |
| `PUSH_DISABLED` | 503        | server belum dikonfigurasi dengan VAPID key |
