import http from 'k6/http';
import { check } from 'k6';

const base = __ENV.BASE_URL || 'http://localhost:8080';
const vus = Number(__ENV.VUS || 30);
const duration = __ENV.DURATION || '60s';

export const options = {
  vus,
  duration,
  thresholds: {
    http_req_duration: ['p(99)<800'],
    http_req_failed: ['rate<0.01']
  },
  summaryTrendStats: ['avg', 'min', 'max', 'p(95)', 'p(99)']
};

export default function () {
  const resp = http.post(`${base}/api/v1/webhooks/shipping/mock?orderId=OID123&tracking=TRACK&status=delivered`);
  check(resp, {
    'webhook accepted': (r) => r.status === 202 || r.status === 200 || r.status === 204
  });
}
