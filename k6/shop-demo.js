// k6 load script for the shop-demo topology (topologies/shop-demo.yaml).
//
// Drives the topology's load entrypoint (frontend). vus/duration mirror the
// topology's load.k6 block — keep them in sync, or override on the CLI:
//
//   k6 run --vus 100 --duration 10m k6/shop-demo.js
//
// Point ZEALOT_BASE_URL at wherever the frontend is reachable:
//
//   # Kubernetes, deployed with `zealot generate --nodeport` (port 30080)
//   k6 run -e ZEALOT_BASE_URL=http://localhost:30080 k6/shop-demo.js
//
//   # A local instance (make run, or examples/chaining)
//   k6 run -e ZEALOT_BASE_URL=http://localhost:8080 k6/shop-demo.js
import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';

export const options = {
  vus: 50, // load.k6.vus in topologies/shop-demo.yaml
  duration: '5m', // load.k6.duration
  thresholds: {
    // auth injects 2% 500s; with failFast call steps they surface as 502s
    // at the frontend. A small failure rate is the demo, not a regression.
    http_req_failed: ['rate<0.10'],
    http_req_duration: ['p(95)<1000'],
  },
};

const BASE_URL = __ENV.ZEALOT_BASE_URL || 'http://localhost:30080';

export default function () {
  const res = http.get(`${BASE_URL}/`, {
    headers: {
      // Propagated through every upstream call — grep the zealot JSON logs
      // for this ID to follow one request across the whole graph.
      'X-Request-ID': `k6-${exec.vu.idInTest}-${exec.vu.iterationInInstance}`,
    },
  });
  check(res, {
    'status is 200': (r) => r.status === 200,
    // Faulted requests return {"error": ...} instead of the envelope.
    'standard envelope': (r) =>
      r.status !== 200 || (r.json('service') === 'frontend' && !!r.json('request_id')),
  });
}
