# AGENTS.md — zealot

Config-driven microservice simulator (Go 1.26, single binary). One binary
plays every role in a demo topology; behavior lives in YAML node configs, not
code branches. Sources of truth: `PLAN.md` (roadmap, milestones M1–M6) and
`README.md` (user-facing behavior). Update both when behavior changes.

## Commands

- `make build` / `make test` / `make e2e` / `make vet` / `make run` (serves `examples/node.yaml`)
- Single package: `go test ./internal/pipeline/` — no lint/typecheck step beyond `go vet`
- `make e2e` runs bats tests in `e2e/` against the real example configs;
  bats-core is vendored into `e2e/.bats` (gitignored) on first run. E2E ports
  are 18080–18082 — never use 8080-range inside the suite.
- First `go mod` operations may stall on slow downloads; module cache is warm now, retry rather than changing proxy config

## Hard constraints

- **Spec stability**: metric names/labels (`zealot_*`) and the JSON response
  envelope (`service`, `request_id`, `status`, `body`, `padding`) are SPEC v1
  and must stay identical for future language ports. Never rename; additive
  changes only.
- **Milestone gating**: don't implement ahead-of-milestone features
  (generator, demo kit) without the user asking; mark milestones done in
  `PLAN.md` when landed. M1–M3 are complete.
- `generate` subcommand is a stub that errors by design until M4.

## Architecture notes (non-obvious)

- `internal/reqid` exists to break an import cycle: `server` → `pipeline` and
  `pipeline` → request-ID helpers. Put shared low-level helpers in new leaf
  packages, not in `server`.
- `respond` doesn't terminate the pipeline; it writes a `Result` later steps
  can overwrite. `fault` ordering relative to `respond` is therefore
  meaningful (documented in README).
- `bodySize` padding happens in `server.envelope` (JSON `padding` field),
  **not** in the `respond` step — the step only sets `Result.PadTo`. Padding
  must keep responses valid JSON.
- Upstream `call` bodies are drained and discarded by design; downstream
  content never flows into the response. Same for Postgres query rows.
- `internal/datastore` is a leaf package (depends only on `config`) so
  `pipeline`, `server` and tests share it without cycles. Datastore
  connections fail fast at startup (`server.New` returns error) — nodes
  refuse to serve with an unreachable backing service.
- Redis `GET` misses return nil by design (miss ≠ failure under load).
- Duration/size parsers (`SampleDistribution`, `ParseSize`) live in `config`,
  not `pipeline`: topology validation needs them and `pipeline` already
  depends on `config`. `config.ValidateSteps` is the shared deep step
  validator used by both node configs and topologies.
- Topology call targets accept scalars (`targets: [api]`) via custom
  `Target.UnmarshalYAML` — keep both forms working. Cycles are hard errors
  (no runtime depth limit exists).

## Operational gotchas

- **No config hot reload**: node config is read once at startup. When
  smoke-testing on `:8080`/`:8081`, kill stale instances first
  (`kill $(pgrep -x zealot)`) or you'll be served the old config and chase
  phantom bugs.
- Keep scratch configs/binaries for ad-hoc smoke tests under
  `/tmp/opencode/`, not the repo.
- Postgres integration test is env-guarded (skipped by default):
  `ZEALOT_TEST_PG_DSN=... go test ./internal/datastore/ -run Postgres`;
  backing services come from `docker compose up -d`.
