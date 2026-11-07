# Zealot — Implementation Plan

**Date:** 2026-11-05 · **Status:** Approved plan, pre-implementation

A single, config-driven service for spinning up arbitrary microservices
topologies for demos and load testing. One binary plays many roles: each
instance is configured to consume (and be consumed by) other instances of
itself, plus backing services such as Postgres and Redis.

```
            ┌──────────┐
            │ frontend │──┐
            └──────────┘  │
                 │        ▼
          ┌──────┴───┐  ┌────┐    ┌──────────┐
          ▼          ▼  │api │───▶│ postgres │
        ┌────┐    ┌────┐└────┘    └──────────┘
        │auth│    │cart│  │
        └────┘    └────┘  ▼
                    │   ┌───────┐
                    └──▶│ redis │
                        └───────┘
```
Every box on the left is the same zealot binary with different YAML.

## Decisions

| Topic | Decision |
|---|---|
| Language | **Go** first; the feature set will eventually be replicated in other languages |
| Multi-language parity | Handled via a versioned behavior/metrics spec (SPEC.md) so future `zealot-py` / `zealot-node` ports behave identically |
| Deployment target | **Kubernetes manifests**, emitted by a generator |
| Backing services | **Postgres + Redis** initially |
| Configuration | **Topology file + generator** — one YAML describes the whole graph |
| Load generation | **k6 scripts only** — zealot is the system under test, k6 drives load |

## Repo layout (Go module)

```
zealot/
├── cmd/zealot/main.go        # CLI entrypoint
├── internal/
│   ├── config/               # per-instance node config loading
│   ├── pipeline/             # step engine + step implementations
│   ├── server/               # HTTP server + middleware
│   ├── metrics/              # Prometheus registry, spec-defined metric names
│   ├── topology/             # topology schema, parse, validate
│   └── generate/             # k8s manifest rendering
├── topologies/               # example topologies (e.g. shop-demo.yaml)
├── k6/                       # example load scripts
├── docs/SPEC.md              # behavior + metrics spec (for future language ports)
├── Dockerfile                # multi-stage → distroless, single static binary
└── Makefile
```

## The binary — three subcommands

| Command | Purpose |
|---|---|
| `zealot serve --config node.yaml` | Run one node: HTTP server executing config-defined pipelines |
| `zealot validate topology.yaml` | Validate a topology file (schema, references, cycles) |
| `zealot generate topology.yaml --out k8s/` | Render Kubernetes manifests |

## Pipeline model

Each instance defines HTTP endpoints; each endpoint maps to a **pipeline of
steps** executed on every request:

- `delay` — fixed or distribution-based latency (e.g. `normal(50ms, 10ms)`)
- `cpu` — burn CPU for N ms (hash iterations) to simulate real work
- `call` — HTTP call to upstream zealot instances or external services;
  supports **parallel fan-out** (`parallel: true`) and sequential chains
- `query` — hit a backing service (Postgres `SELECT`, Redis `GET/SET`)
  with connection pooling
- `fault` — inject errors/latency with a probability (resilience/chaos demos)
- `respond` — status code + configurable payload size

## Topology schema v1 (single source of truth)

```yaml
version: "1"
name: shop-demo
namespace: zealot-demo

datastores:
  postgres:
    image: postgres:16
    database: shop
    seedSQL: |                    # runs via postgres initdb, uses generate_series
      CREATE TABLE items (...);
      INSERT INTO items SELECT ... FROM generate_series(1,100000);
  redis:
    image: redis:7

services:
  frontend:
    replicas: 2
    resources: { cpu: 250m, memory: 128Mi }   # k8s requests/limits — throttle demos!
    endpoints:
      - path: /
        pipeline:
          - delay: { distribution: "normal(20ms,5ms)" }
          - call:  { targets: [api, cart], parallel: true }   # fan-out
          - respond: { status: 200, bodySize: 2kb }
  api:
    replicas: 3
    endpoints:
      - path: /
        pipeline:
          - query: { datastore: redis, op: GET, key: "item:{rand:1000}" }
          - query: { datastore: postgres, sql: "SELECT * FROM items WHERE id = $1" }
          - call:  { targets: [auth] }
          - respond: { status: 200 }
  auth:
    endpoints:
      - path: /
        pipeline:
          - cpu:   { duration: 10ms }         # hash iterations — shows up in CPU metrics
          - fault: { rate: 0.02, status: 500 } # 2% errors for resilience demos
          - respond: { status: 200 }

load:
  entrypoint: frontend      # k6 script targets this service
  k6: { vus: 50, duration: 5m }
```

Per-instance configs are rendered into ConfigMaps by the generator;
service names resolve via cluster DNS (`http://api.zealot-demo.svc.cluster.local`).

### Pipeline semantics (goes in SPEC.md)

- Steps run **in order**; a `call` with `parallel: true` fans out
  concurrently and waits for all targets
- Failure policy per step: `failFast` (default) or `continue`
- `X-Request-ID` propagated on every upstream call; every log line carries it
- `{rand:N}` template in keys/payloads for cache-hit-ratio control

## Generated Kubernetes output

- Namespace, one **Deployment + Service + ConfigMap** per zealot service
  (replicas + resources from topology)
- Postgres (Deployment + Service + PVC, initdb SQL via ConfigMap) and
  Redis (Deployment + Service)
- Optional `--prometheus-operator` flag emits **ServiceMonitors** for
  instant Grafana dashboards
- NodePort/ClusterIP option for the load entrypoint so k6 can reach it

## Observability spec (identical across future languages)

- `zealot_requests_total{service,endpoint,status}`
- `zealot_request_duration_seconds{service,endpoint}` (histogram)
- `zealot_step_duration_seconds{service,endpoint,step_type,target}`
- `zealot_upstream_requests_total{service,target,status}`
- `zealot_datastore_duration_seconds{service,datastore,op}`
- `zealot_build_info{version,lang="go"}`
- `/healthz` (liveness/readiness), JSON structured logs

## Testing

- Unit tests per pipeline step (`httptest` servers for `call`,
  `miniredis` for Redis, fake for Postgres via interface)
- Golden-file tests for generated manifests
- `docker run` smoke test of the built image

## Milestones

1. **M1 — Core node** ✅ (2026-11-05): config loading, HTTP server, pipeline engine
   (`delay`, `cpu`, `call`, `respond` + `fault` landed early), request-ID middleware,
   `/healthz`, `/metrics`, unit tests, two-instance smoke test verified
   (request-ID propagation, parallel fan-out, spec metrics)
2. **M2 — Datastores & chaos** ✅ (2026-11-05): `query` step (Postgres via
   pgxpool, Redis via go-redis, both pooled, startup fail-fast connect),
   `fault` step (landed in M1), `zealot_datastore_duration_seconds` live,
   docker-compose backing services (seeded 100k-row Postgres), miniredis unit
   tests + env-guarded Postgres integration test
3. **M3 — Topology** ✅ (2026-11-05): schema v1 (`internal/topology`),
   `zealot validate` with aggregated errors — DNS-1123 names, resource
   quantities, reference checks (call targets, query datastores,
   load entrypoint), deep step validation (shared with node configs via
   `config.ValidateSteps`), DFS cycle detection, scalar call-target
   shorthand; example `topologies/shop-demo.yaml`; unit + bats coverage
4. **M4 — Generator** ✅ (2026-11-05): `zealot generate` renders Namespace,
   per-service ConfigMap+Deployment+Service, Postgres (PVC + initdb
   ConfigMap) and Redis manifests; flags `--out`/`--image`/`--nodeport`
   (fixed 30080)/`--prometheus-operator` (ServiceMonitors); node configs
   baked into ConfigMaps carry only referenced datastores; service/datastore
   name-collision validation; golden-file tests (`go test
   ./internal/generate/ -update`); multi-stage distroless Dockerfile,
   `make docker` / `make docker-smoke`; bats e2e
5. **M5 — Demo kit** ✅ (2026-11-06): `k6/shop-demo.js` (drives the load
   entrypoint, mirrors `load.k6` vus/duration, stamps `X-Request-ID:
   k6-<vu>-<iteration>` for log tracing, tolerates auth's 2% injected
   faults); README Kubernetes walkthrough (build → generate → apply → k6 →
   observe); fixed shop-demo auth fault ordering (fault after respond so
   injected 500s stick); e2e sync + live-k6 smoke tests
6. **M6 — SPEC.md** ✅ (2026-11-06): `docs/SPEC.md` v1 frozen — node config
   schema, startup fail-fast, request-ID/routing/pipeline semantics, all six
   steps, templating/grammars, HTTP surface (envelope, padding math,
   healthz, 502 shape), metrics with exact labels + buckets, log fields.
   Spec-review conformance tests lock the subtle clauses: upstream 5xx ≠
   call failure (only transport errors fail; statuses never propagate),
   failFast attempts all targets, trace-header forwarding, exact
   envelope/padding/healthz bodies (`e2e/spec.bats` +
   `internal/pipeline` tests). M5 docs corrected to match (faults visible
   in metrics, not at the frontend). OTel span emission deferred (v1
   forwards trace headers only — SPEC §8).

## Open points (deferred)

- Whether `zealot generate` should also emit docker-compose later
  (the generator will be interface-based so it's cheap to add)
- OpenTelemetry span emission: deferred past M6. SPEC v1 freezes
  trace-header *forwarding* only (SPEC §8); span emission would be a
  spec minor/major version discussion.
