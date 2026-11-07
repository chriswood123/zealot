# Zealot behavior & metrics spec — v1

**Status:** frozen · **Version:** 1 · **Date:** 2026-11-06

This document is the single source of truth for zealot's observable behavior.
The Go implementation conforms to it, and future language ports
(`zealot-py`, `zealot-node`, ...) MUST reproduce it exactly, so that a node
config or generated manifest behaves identically regardless of the binary
serving it.

## Versioning policy

- v1 is **frozen**: existing names never change. No renaming or removing
  metric names, label names, envelope fields, header names, log field names,
  or config keys.
- **Additive changes only**: new metrics, new labels on new metrics, new
  optional config keys, new steps — each requires a spec minor-version note
  here. Anything breaking requires a new major spec version and a
  coordinated rollout across all ports.
- When this file and an implementation disagree, it is a bug — file it
  against the implementation (or the spec) explicitly; do not silently
  "improve" either.

Conformance target: the `serve` subcommand. `validate`/`generate` are
tooling conveniences; ports MAY implement them but are not required to for
SPEC compliance — they must be able to *serve* the node configs a
topology/generator produces.

## 1. Node configuration

A node is configured by a single YAML file:

```yaml
service: frontend            # required; logical name in metrics, logs, envelope
listen: ":8080"              # required; host:port (empty host = all interfaces)

datastores:                  # optional; named backing-service connections
  postgres:
    driver: postgres         # required: postgres | redis
    dsn: "postgres://user:pass@host:5432/db?sslmode=disable"
    maxConns: 20             # optional; pool cap (0/absent = driver default)
  redis:
    driver: redis
    addr: "host:6379"        # required for redis
    password: ""             # optional
    db: 0                    # optional

endpoints:                   # required; at least one; paths unique
  - path: /
    pipeline:
      - delay: { distribution: "normal(20ms,5ms)" }
      - respond: { status: 200, body: "hello", bodySize: 2kb }
```

Validation happens once at startup; any violation is fatal (non-zero exit,
nothing served): required fields present, unique endpoint paths, exactly one
step kind per step, per-step rules in §4, datastore drivers valid with their
required fields, and every `query` step referencing a declared datastore
with driver-appropriate fields (`sql` for postgres, `op`+`key` for redis).

**Startup fail-fast**: every configured datastore is connected and pinged
before the HTTP listener starts (10 s timeout each). An unreachable backing
service aborts startup with exit code 1 — a node never serves in a degraded
"will fail per-request" state.

## 2. Request lifecycle

### Routing

Routing follows Go `http.ServeMux` semantics:

- Reserved, method-restricted, exact: `GET /healthz` (§6.3) and
  `GET /metrics` (§7).
- Configured endpoint paths accept **any HTTP method**. A configured path
  is an exact match, **except** a path ending in `/` (notably `/` itself),
  which is a **prefix match** — a node with a `/` endpoint answers every
  path that no more specific pattern claims, and paths without a catch-all
  return `404`.
- Most specific pattern wins (`/work` beats `/`).

### Request ID

- Every request gets an ID: the incoming `X-Request-ID` header value if
  present (used verbatim, any string), otherwise generated as **32 lowercase
  hex characters** (16 random bytes).
- The ID is echoed in the response `X-Request-ID` header (including on
  `/healthz`, metrics and error responses), embedded in the response
  envelope, attached to every log line emitted while handling the request,
  and forwarded on every upstream `call`.

### Pipeline execution

- Steps execute **strictly in listed order** on every request.
- Steps share a mutable **result object**: `{ status: 200, body: <empty>,
  padTo: 0 }`. Steps may read/write it (see `fault`, `respond`).
- `respond` does **not** terminate the pipeline. A pipeline without a
  `respond` step returns `200` with an empty body. Later steps (including a
  second `respond`) may overwrite the result.
- **Step failure**: if a step returns an error, the pipeline aborts
  immediately and the client gets `502` with the error body (§6.4). Only
  `call` has a policy to proceed past failures (`onError: continue`); all
  other steps always abort.
- Client disconnect cancels the pipeline (context cancellation). `delay`
  and `cpu` steps observe cancellation and abort early; `fault` latency
  does not.

## 3. Templating and value grammars

### `{rand:N}`

In `respond.body`, `call` target URLs, and `query` `sql`/`key`/`value`, the
template `{rand:N}` expands to a random integer in `[0, N)` (N a positive
integer). Expansion happens **per request, per occurrence** — two
occurrences in one string draw independently. Invalid templates
(non-numeric or non-positive N) are left literally untouched. Draws are
uniform; the RNG need not be seeded deterministically.

### Durations

Go-style duration strings: decimal number + unit (`ns`, `us`/`µs`, `ms`,
`s`, `m`, `h`), optionally sequenced (`1h30m`) and fractional (`1.5s`).
Examples: `50ms`, `300ms`, `2s`, `5m`.

### Distributions (delay step)

- `normal(mean,stddev)` — Gaussian sample, clamped at 0. Whitespace inside
  the parentheses is ignored.
- `uniform(min,max)` — uniform sample in `[min,max]`; requires `max ≥ min`.
- A bare duration (e.g. `50ms`) is also accepted and is constant.

### Sizes (respond.bodySize)

`512`, `2kb`, `1.5mb`, case-insensitive, optional whitespace. Units are
binary: `kb` = 1024 B, `mb` = 1024² B, `gb` = 1024³ B; a bare number is
bytes. Fractional values truncate to whole bytes.

## 4. Steps

Every step kind contributes `step_type` to `zealot_step_duration_seconds`
(§7). Exactly one kind per step.

### `delay`

`{ duration: 50ms }` or `{ distribution: "normal(50ms,5ms)" }` — exactly
one required. Sleeps for the fixed or per-request sampled duration.
Context-aware: aborts early if the client disconnects.

### `cpu`

`{ duration: 10ms }` — required. Burns CPU for the configured **wall-clock**
duration by computing chained SHA-256 hashes in a tight loop on a **single
goroutine** (saturates ≈1 core; this is what triggers Kubernetes CPU
throttling). Checks for client cancellation between hash batches. The exact
batch size is an implementation detail; observable behavior is the wall
duration and single-core saturation.

### `call`

```yaml
- call:
    targets: [{ url: "api" }, { url: "https://example.com/x" }]
    parallel: true          # default false
    onError: continue       # default failFast
```

- Each target URL is `{rand:N}`-expanded, then prefixed with `http://` if it
  contains no `://`. Every call is an HTTP **GET** with no request body and
  a **30 s** total timeout.
- **Sequential** (default): targets called one after another in configured
  order. **Parallel** (`parallel: true`): all targets called concurrently;
  the step completes when the slowest returns.
- Response bodies are read (up to **1 MiB**) and **discarded** — downstream
  content never flows into the response.
- **Forwarded headers**: `X-Request-ID` (the current request's ID) plus
  `traceparent`, `tracestate` and `baggage` when present on the inbound
  request. No other inbound headers are forwarded.
- **Failure definition**: a target fails only on **transport-level errors**
  — connection refused, DNS failure, timeout, invalid URL. **Any received
  HTTP status, including 5xx, is a successful call.** Downstream statuses do
  not propagate into the result.
- **Error policy** (evaluated after *all* targets have been attempted —
  `failFast` never short-circuits the target list):
  - `failFast` (default): if any target failed, the step fails → pipeline
    aborts → client gets `502`.
  - `continue`: failures are recorded in metrics and the pipeline proceeds.

### `query`

`{ datastore: <name>, sql: "..." }` for Postgres, or
`{ datastore: <name>, op: GET|SET|DEL, key: "...", value: "..." }` for
Redis. `sql`, `key` and `value` support `{rand:N}`.

- Postgres: executes the SQL (text protocol), consumes all rows and
  **discards them**; row content never reaches the response. SQL errors
  fail the pipeline.
- Redis `GET`: returns the value nowhere (discarded); a **miss is not an
  error** — it succeeds silently. `SET`: sets key to `value`, no expiry.
  `DEL`: deletes key. Any other `op` fails the pipeline.
- Every query records `zealot_datastore_duration_seconds` (§7).

### `fault`

`{ rate: 0.02, status: 500, latency: 300ms }` — `rate` required in `[0,1]`;
rolled per request (triggered when a uniform draw in `[0,1)` is `< rate`).
On trigger: sleeps `latency` (if set; **not** cancellation-aware) and sets
the result status to `status` (if set, 100–599). The pipeline continues;
a later `respond` with an explicit status overwrites the injected status —
put `fault` **after** `respond` to make injected errors stick.

### `respond`

`{ status: 200, body: "...", bodySize: 2kb }` — all fields optional.

- `status`: HTTP status 100–599; `0`/absent keeps the current result status
  (default 200, or whatever an earlier step set).
- `body`: payload string, `{rand:N}`-expanded. If the expanded body is
  itself valid JSON it is embedded **as JSON** in the envelope; otherwise as
  a JSON string.
- `bodySize`: pads the serialized envelope to **at least** this many bytes
  (see §6.2). If the envelope is already larger, nothing is added.

## 5. Metrics

Prometheus text exposition on `GET /metrics`. Names and labels are frozen.
`service` is always the node's configured service name.

| Name | Type | Labels |
|---|---|---|
| `zealot_requests_total` | counter | `service`, `endpoint`, `status` |
| `zealot_request_duration_seconds` | histogram | `service`, `endpoint` |
| `zealot_step_duration_seconds` | histogram | `service`, `endpoint`, `step_type`, `target` |
| `zealot_upstream_requests_total` | counter | `service`, `target`, `status` |
| `zealot_datastore_duration_seconds` | histogram | `service`, `datastore`, `op` |
| `zealot_build_info` | gauge (always 1) | `version`, `lang` |

Label semantics:

- `endpoint` — the configured path (e.g. `/`), not a pattern.
- `status` — decimal HTTP status. For `zealot_upstream_requests_total`,
  `0` on transport failure (see §4 `call`), else the upstream's status.
- `step_type` — one of `delay`, `cpu`, `call`, `query`, `fault`, `respond`.
- `target` (step metric) — `""` except: `call` → the raw configured target
  URLs joined with `,` in order (before `{rand:N}` expansion or `http://`
  prefixing); `query` → the datastore name.
- `target` (upstream counter) — the raw configured target URL of that one
  call, same normalization as above.
- `datastore` / `op` — datastore name; `sql` for Postgres, else the
  lowercased Redis command (`get`, `set`, `del`).
- `lang` — the implementation language (`go` for the reference binary).
- `version` — build version string (`dev` unless stamped at build time).

All three histograms use identical bucket boundaries (seconds):
`0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10`.

Recording rules:

- `zealot_requests_total` + `zealot_request_duration_seconds` are recorded
  once per completed request to a **configured endpoint** (failures
  included, status `502`). `/healthz` and `/metrics` are not counted.
- `zealot_step_duration_seconds` is recorded for every executed step,
  including the step that failed.
- `zealot_upstream_requests_total` is recorded per attempted target,
  success or failure.
- `zealot_datastore_duration_seconds` is recorded per executed query.

## 6. HTTP surface

### 6.1 Success envelope

Configured endpoints respond `Content-Type: application/json`:

```json
{"service":"frontend","request_id":"e554...4f","status":200,"body":"hello","padding":"xxx"}
```

- `service`, `request_id` — always present (strings).
- `status` — integer mirroring the HTTP status.
- `body` — the `respond` body: raw JSON if the (expanded) body parses as
  JSON, otherwise a JSON string. **Omitted entirely** when empty.
- `padding` — **omitted unless** `bodySize` demanded a larger response.

### 6.2 Padding

When `bodySize` is set and the serialized envelope without padding is
smaller, a `padding` field of `x` characters is added so the total
serialized response is **exactly** `bodySize` bytes (`deficit = bodySize -
len(envelope) - len(',"padding":""')`; padding added only when
`deficit > 0`). No trailing newline is ever added.

### 6.3 `GET /healthz`

`200`, `Content-Type: application/json`, body exactly `{"status":"ok"}`.
Used for both liveness and readiness (datastore health is guaranteed by
startup fail-fast, §1).

### 6.4 Errors

A failed pipeline returns `502 Bad Gateway`,
`Content-Type: application/json`, body `{"error":"<message>"}` (not the
standard envelope). The failure is logged at ERROR with the request ID
(§7). Paths matched by no pattern (§2) get `404`.

## 7. Logging

JSON structured logs, one object per line, on **stdout** (slog-compatible
field names). Every line emitted while handling a request carries
`request_id` and `service`. Frozen fields:

- Access log — `{"level":"INFO","msg":"request","service","request_id",
  "method","path","status","duration_ms"}` (`duration_ms` integer ms).
- Failure log — `{"level":"ERROR","msg":"pipeline failed","service",
  "request_id","endpoint","error"}`.
- Startup — `{"level":"INFO","msg":"listening","service","addr"}` and one
  `{"level":"INFO","msg":"datastore connected","service","name","driver"}`
  per datastore.

Timestamps and additional fields are implementation details.

## 8. Out of scope for v1

- **OpenTelemetry span emission** — v1 only *forwards* `traceparent`,
  `tracestate` and `baggage` headers (§4 `call`); nodes emit no spans.
  Candidate for a future spec version.
- docker-compose generation, TLS termination, non-GET upstream calls,
  per-endpoint methods, config hot-reload (config is read once at startup).
