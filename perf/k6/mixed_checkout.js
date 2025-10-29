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

export default function () {
  const headers = { headers: { 'Content-Type': 'application/json' } };
  const cartRes = http.post(`${base}/api/v1/carts`, '{}', headers);
  check(cartRes, {
    'cart created': (r) => r.status === 201 || r.status === 200
  });
  const cartBody = json(cartRes);
  const cartId = cartBody.data?.id || cartBody.id;
  if (!cartId) {
    sleep(0.5);
    return;
  }
  const addItem = http.post(
    `${base}/api/v1/carts/${cartId}/items`,
    JSON.stringify({ variantId: 'variant-sample', qty: 1 }),
    headers
  );
  check(addItem, {
    'item added': (r) => r.status === 200 || r.status === 201
  });
  const checkout = http.post(
    `${base}/api/v1/checkout`,
    JSON.stringify({ cartId: cartId, paymentChannel: 'midtrans' }),
    headers
  );
  check(checkout, {
    'checkout accepted': (r) => r.status === 200 || r.status === 202 || r.status === 201
  });
  sleep(0.5);
}
