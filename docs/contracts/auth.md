# Authentication & User Endpoints

## 1.1 Register

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

## 1.2 Login

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

## 1.3 Refresh Token

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

## 1.4 Logout

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

## 1.5 Get Current User

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

## 1.6 Forgot Password

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

## 1.7 Reset Password

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

## 1.8 Verify Email

```http
POST /api/v1/auth/email/verify
Content-Type: application/json
```

**Request:**
```json
{
  "token": "verification-token-from-email"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "email verified",
    "user": {
      "id": "uuid-here",
      "name": "John Doe",
      "email": "john@example.com",
      "emailVerified": true,
      "createdAt": "2025-12-07T10:00:00Z",
      "updatedAt": "2025-12-08T09:00:00Z"
    }
  }
}
```

**Errors:** `400 INVALID_TOKEN` — token unknown, already used, or expired. Tokens
are single-use and valid for 72 hours.

---

## 1.9 Resend Verification Email

```http
POST /api/v1/auth/email/resend
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
    "message": "verification email sent"
  }
}
```

Always returns `200` — unknown and already-verified addresses are indistinguishable
from successful sends so the endpoint cannot be used to enumerate accounts.
