# zealot

A config-driven microservice simulator for demos and load testing. One binary
plays many roles: each instance is configured to consume (and be consumed by)
other instances of itself, and (from M2) backing services such as Postgres and
Redis.

Every node's behaviour — latency, CPU work, upstream calls, faults, response
shape — is defined entirely in YAML, so arbitrary service topologies can be
assembled from a single binary.

> Status: **M2 complete** (core node + Postgres/Redis query steps). See
> [PLAN.md](PLAN.md) for the full roadmap (topology files, Kubernetes
> generator, k6 demo kit).

## Build

```sh
make build        # or: go build -o bin/zealot ./cmd/zealot
```

Requires Go 1.26+.

## Run a node

```sh
./bin/zealot serve --config examples/node.yaml
```

Every instance runs from a **node config** describing who it is and what its
endpoints do:

```yaml
service: frontend        # logical name; used in metrics labels and logs
listen: ":8080"          # bind address

endpoints:
  - path: /
    pipeline:
      - delay: { distribution: "normal(20ms,5ms)" }
      - cpu: { duration: 5ms }
      - call:
          targets: [{ url: "backend" }, { url: "cart" }]
          parallel: true
          onError: continue          # failFast (default) | continue
      - fault: { rate: 0.02, status: 500 }
      - respond: { status: 200, body: "hello", bodySize: 2kb }
```

When a request hits an endpoint, its **pipeline steps run in order**:

| Step | Purpose | Key fields |
|---|---|---|
| [`delay`](#delay) | Sleep; fixed or sampled from a distribution | `duration`, `distribution` |
| [`cpu`](#cpu) | Burn CPU (SHA-256 iterations) to simulate real work | `duration` |
| [`call`](#call) | HTTP GET upstream(s) with optional parallel fan-out | `targets`, `parallel`, `onError` |
| [`fault`](#fault) | Probabilistic error/latency injection | `rate`, `status`, `latency` |
| [`respond`](#respond) | Set the response status and body | `status`, `body`, `bodySize` |
| [`query`](#query) | Postgres/Redis queries via pooled connections | `datastore`, `op`, `key`, `value`, `sql` |

### Pipeline semantics

- Steps execute **in the order listed**. Every step's latency is recorded in
  `zealot_step_duration_seconds{service,endpoint,step_type,target}`.
- A step that fails aborts the pipeline (unless the step's own policy says
  otherwise); the client gets `502 Bad Gateway` with a JSON error body and the
  failure is logged with the request ID.
- `respond` does **not** stop the pipeline — it sets the status/body on a
  result object that later steps can still overwrite. A pipeline with no
  `respond` step returns `200` with an empty body.
- `{rand:N}` in `body`, `url`, and key fields expands to a random integer in
  `[0,N)` per request — useful for cache-key cardinality control.

### `delay`

Simulates processing or network latency by sleeping. Exactly one of
`duration` or `distribution` is required.

```yaml
- delay: { duration: 50ms }                          # fixed
- delay: { distribution: "normal(50ms,5ms)" }      # Gaussian, clamped at 0
- delay: { distribution: "uniform(10ms,100ms)" }   # uniform range
```

Distributions are sampled **per request**, so `normal(50ms,5ms)` yields a
realistic latency spread under load rather than a flat 50 ms. The sleep is
context-aware: if the client disconnects, the delay (and pipeline) aborts.

### `cpu`

Burns CPU for a fixed wall-clock duration using SHA-256 hashing, simulating
compute-bound work (serialization, crypto, image processing, ...).

```yaml
- cpu: { duration: 10ms }
```

The burn runs on a single goroutine, so it saturates roughly one core. Under
Kubernetes CPU limits this is what triggers throttling — pair it with a low
`resources.cpu` limit in the topology (M4) to demo CPU-starved services. The
loop checks for client cancellation between hash batches.

### `call`

Calls one or more upstream HTTP services — other zealot instances or anything
speaking HTTP.

```yaml
- call:
    targets:
      - { url: "api" }                       # bare name -> http://api
      - { url: "cart" }                      #        -> http://cart
      - { url: "https://example.com/x" }     # full URLs kept as-is
    parallel: true                           # default false = sequential
    onError: continue                        # default failFast
```

Semantics:

- **Sequential** (default): targets are called one after another; total step
  latency is the sum. **Parallel**: all targets are called concurrently and
  the step completes when the slowest returns.
- Every upstream call is an HTTP `GET` with a 30 s timeout; response bodies
  are drained (up to 1 MB) and discarded — downstream content does not flow
  back into the response.
- **Header propagation**: `X-Request-ID` (from context) and any incoming W3C
  `traceparent`, `tracestate`, `baggage` headers are forwarded, preserving
  trace continuity across the graph.
- **Error policy**: `failFast` (default) — the first failed target aborts the
  whole pipeline with a 502. `continue` — failures are recorded and the
  pipeline proceeds; useful for optional dependencies.
- Every attempt increments
  `zealot_upstream_requests_total{service,target,status}` (`status="0"` on
  transport errors), and the target list appears in the step-latency metric's
  `target` label.

### `fault`

Injects chaos with a given probability — errors, extra latency, or both.

```yaml
- fault: { rate: 0.02, status: 500 }            # 2% of requests become 500s
- fault: { rate: 0.1, latency: 300ms }          # 10% get +300ms
- fault: { rate: 0.05, status: 503, latency: 1s }
```

`rate` must be in `[0,1]` and is rolled per request. When the roll succeeds,
the optional `latency` sleep runs and the response status is overwritten with
`status` (if set). The pipeline continues afterwards, so a later `respond`
step can still override the injected status — put `fault` **after** `respond`
to make injected errors stick, or before it to model recoverable degradation.

### `respond`

Sets the HTTP response. Optional everywhere: a pipeline without it returns
`200` with an empty `body`.

```yaml
- respond: { status: 200, body: '{"ok":true}' }
- respond: { status: 200, body: "hello", bodySize: 2kb }
- respond: { status: 404 }
```

- `status` — HTTP status code; when omitted the current status (default 200,
  possibly set by an earlier `fault`) is kept.
- `body` — response payload; supports `{rand:N}` templates. A body that is
  itself valid JSON is embedded **as JSON** in the response envelope;
  anything else becomes a JSON string.
- `bodySize` — pads the serialized response to **at least** this size (`512`,
  `2kb`, `1.5mb`) via a JSON `padding` field, for simulating payload-heavy
  endpoints. If the envelope alone is already larger, nothing is added.

## Response format

Every endpoint responds `Content-Type: application/json` with a standard
envelope:

```json
{
  "service": "frontend",
  "request_id": "e5540990080781f26545074c8380df4f",
  "status": 200,
  "body": "hello from frontend"
}
```

- `service` — this instance's configured name
- `request_id` — the propagated `X-Request-ID`
- `status` — mirrors the HTTP status code
- `body` — your `respond` body (raw JSON if it parses, otherwise a string)
- `padding` — filler (`x` characters), present only when `bodySize` demands
  a larger response

Example with a JSON body (`body: '{"ok":true}'`):

```json
{"service":"frontend","request_id":"...","status":200,"body":{"ok":true}}
```

### `query`

Queries a backing service through a pooled connection. The target must be
declared in the node config's `datastores` block:

```yaml
datastores:
  postgres:
    driver: postgres
    dsn: "postgres://zealot:zealot@localhost:5432/shop?sslmode=disable"
    maxConns: 20          # pgx pool cap (0 = pgx default)
  redis:
    driver: redis
    addr: "localhost:6379"
    # password: ""
    # db: 0
```

Postgres — arbitrary SQL, rows consumed and discarded:

```yaml
- query: { datastore: postgres, sql: "SELECT * FROM items WHERE id = {rand:1000} + 1" }
```

Redis — `GET`, `SET` or `DEL`:

```yaml
- query: { datastore: redis, op: GET, key: "item:{rand:1000}" }
- query: { datastore: redis, op: SET, key: "item:{rand:1000}", value: '{"cached":true}' }
```

Semantics:

- **Startup fail-fast**: datastores are connected and pinged when the node
  boots; an unreachable backing service stops the node from starting (exit 1)
  instead of failing per-request under load.
- **Cache misses are not errors**: a Redis `GET` on a missing key succeeds
  silently — misses are normal behaviour in a load test.
- **Query errors fail the pipeline** (SQL error, unknown op, missing key
  field): the client gets a 502 and the error is logged with the request ID.
- `sql`, `key` and `value` all support `{rand:N}` templates, e.g. point
  lookups across a 100k-row table with `id = {rand:1000} + 1`.
- Every query records
  `zealot_datastore_duration_seconds{service,datastore,op}` (`op` is `sql`
  or the lowercased Redis command) plus the standard per-step metric.

## Local backing services (docker compose)

`docker-compose.yml` provides seeded Postgres and Redis for local testing:

```sh
docker compose up -d    # postgres:16 (100k seeded items) + redis:7
./bin/zealot serve --config examples/node-datastores.yaml
curl localhost:8082/item          # redis GET miss -> postgres lookup
curl localhost:8082/cache-write   # redis SET
```

Postgres is seeded on first start from `examples/db/init.sql` (items table,
100k rows). Reset with `docker compose down -v`.

## Chaining instances

Run several nodes and wire them via `call` targets. Example:

```sh
# terminal 1
./bin/zealot serve --config backend.yaml     # service: backend, listen: :8081
# terminal 2
./bin/zealot serve --config frontend.yaml    # calls localhost:8081
```

Every `call` propagates `X-Request-ID` (incoming value honoured, otherwise
generated) plus any W3C `traceparent`/`tracestate`/`baggage` headers, so a
request can be followed through the graph in the JSON access logs:

```json
{"level":"INFO","msg":"request","service":"backend","request_id":"demo-123",...}
```

## Endpoints every instance serves

| Path | Purpose |
|---|---|
| *(your configured paths)* | Run the endpoint's pipeline |
| `GET /healthz` | Liveness/readiness probe |
| `GET /metrics` | Prometheus metrics (spec-defined names) |

## Metrics

All names and labels are part of the spec and will stay identical across
future language ports:

- `zealot_requests_total{service,endpoint,status}`
- `zealot_request_duration_seconds{service,endpoint}` (histogram)
- `zealot_step_duration_seconds{service,endpoint,step_type,target}` (histogram)
- `zealot_upstream_requests_total{service,target,status}`
- `zealot_datastore_duration_seconds{service,datastore,op}` (histogram; M2)
- `zealot_build_info{version,lang="go"}`

## CLI

```
zealot serve --config <node.yaml>   Run one node
zealot version                      Print version
zealot validate / generate          Coming in M3/M4 (see PLAN.md)
```

## Development

```sh
make test     # go test ./... (unit; miniredis, no docker needed)
make vet      # go vet ./...
make run      # build + serve examples/node.yaml
```

Postgres integration test (skipped unless the env var is set; needs
`docker compose up -d`):

```sh
ZEALOT_TEST_PG_DSN='postgres://zealot:zealot@localhost:5432/shop?sslmode=disable' \
  go test ./internal/datastore/ -run Postgres
```
