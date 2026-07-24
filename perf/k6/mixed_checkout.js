import http from 'k6/http';
import { sleep, check } from 'k6';

const base = __ENV.BASE_URL || 'http://localhost:8080';
const vus = Number(__ENV.VUS || 20);
const duration = __ENV.DURATION || '2m';

export const options = {
  vus,
  duration,
  thresholds: {
    http_req_duration: ['p(95)<350', 'p(99)<500'],
    http_req_failed: ['rate<0.02']
  },
  summaryTrendStats: ['avg', 'min', 'max', 'p(90)', 'p(95)', 'p(99)']
};

function json(r) {
  try {
    return r.json();
  } catch (e) {
    return {};
  }
}

export function setup() {
  const headers = { headers: { 'Content-Type': 'application/json' } };
  const email = `k6_${Date.now()}@test.com`;
  const password = 'Password123!';

  http.post(`${base}/api/v1/auth/register`, JSON.stringify({
    name: 'K6 Load Test',
    email,
    password
  }), headers);

  const loginRes = http.post(`${base}/api/v1/auth/login`, JSON.stringify({
    email,
    password
  }), headers);
  const token = json(loginRes).data?.accessToken;
  if (!token) {
    throw new Error('failed to login during setup');
  }

  const authHeaders = {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${token}`
    }
  };

  const addrRes = http.post(`${base}/api/v1/users/me/addresses`, JSON.stringify({
    label: 'Home',
    receiver_name: 'K6 User',
    phone: '08123456789',
    address_line1: 'Jl. Test No. 1',
    city: 'Jakarta',
    postal_code: '12345',
    country: 'Indonesia',
    province: 'DKI Jakarta',
    is_default: true
  }), authHeaders);
  const addressId = json(addrRes).data?.id;
  if (!addressId) {
    throw new Error('failed to create address during setup');
  }

  const prodRes = http.get(`${base}/api/v1/products?limit=1`);
  const productId = json(prodRes).data?.[0]?.id;
  if (!productId) {
    throw new Error('no products available for load test');
  }

  return { token, addressId, productId };
}

export default function (data) {
  const headers = {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${data.token}`
    }
  };

  const cartRes = http.post(`${base}/api/v1/carts`, '{}', headers);
  check(cartRes, {
    'cart created': (r) => r.status === 201 || r.status === 200
  });
  const cartId = json(cartRes).data?.cartId || json(cartRes).data?.id;
  if (!cartId) {
    sleep(0.5);
    return;
  }

  const addItem = http.post(
    `${base}/api/v1/carts/${cartId}/items`,
    JSON.stringify({ productId: data.productId, qty: 1 }),
    headers
  );
  check(addItem, {
    'item added': (r) => r.status === 200 || r.status === 201
  });

  const checkout = http.post(
    `${base}/api/v1/checkout`,
    JSON.stringify({
      cartId: cartId,
      shippingAddressId: data.addressId,
      shippingService: 'jne-reg',
      shippingCost: 15000,
      paymentMethod: 'bank_transfer'
    }),
    headers
  );
  check(checkout, {
    'checkout accepted': (r) => r.status === 201
  });
  sleep(0.5);
}
