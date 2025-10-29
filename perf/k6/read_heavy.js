import http from 'k6/http';
import { sleep, check } from 'k6';

const base = __ENV.BASE_URL || 'http://localhost:8080';
const vus = Number(__ENV.VUS || 50);
const duration = __ENV.DURATION || '2m';
const p95Budget = __ENV.HTTP_P95 || '250';
const p99Budget = __ENV.HTTP_P99 || '400';

export const options = {
  vus,
  duration,
  thresholds: {
    http_req_duration: [
      `p(95)<${p95Budget}`,
      `p(99)<${p99Budget}`
    ],
    http_req_failed: ['rate<0.01']
  },
  summaryTrendStats: ['avg', 'min', 'max', 'p(90)', 'p(95)', 'p(99)']
};

export default function () {
  const list = http.get(`${base}/api/v1/products?page=1&limit=20&sort=price:asc`);
  check(list, {
    'list status ok': (r) => r.status === 200
  });
  const detail = http.get(`${base}/api/v1/products/sample-slug`);
  check(detail, {
    'detail status valid': (r) => r.status === 200 || r.status === 404
  });
  sleep(0.2);
}
