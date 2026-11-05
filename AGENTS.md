# AGENTS.md — zealot

Config-driven microservice simulator (Go 1.26, single binary). One binary
plays every role in a demo topology; behavior lives in YAML node configs, not
code branches. Sources of truth: `PLAN.md` (roadmap, milestones M1–M6) and
`README.md` (user-facing behavior). Update both when behavior changes.

## Commands

- `make build` / `make test` / `make vet` / `make run` (serves `examples/node.yaml`)
- Single package: `go test ./internal/pipeline/` — no lint/typecheck step beyond `go vet`
- First `go mod` operations may stall on slow downloads; module cache is warm now, retry rather than changing proxy config

## Hard constraints

- **Spec stability**: metric names/labels (`zealot_*`) and the JSON response
  envelope (`service`, `request_id`, `status`, `body`, `padding`) are SPEC v1
  and must stay identical for future language ports. Never rename; additive
  changes only.
- **Milestone gating**: `query` steps intentionally fail with "not yet
  implemented (M2)" and a test asserts this. Don't implement M2+ features
  (datastores, topology, generator) outside their milestone; mark milestones
  done in `PLAN.md` when landed.
- `validate`/`generate` subcommands are stubs that error by design until M3/M4.

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
  content never flows into the response.

## Operational gotchas

- **No config hot reload**: node config is read once at startup. When
  smoke-testing on `:8080`/`:8081`, kill stale instances first
  (`kill $(pgrep -x zealot)`) or you'll be served the old config and chase
  phantom bugs.
- Keep scratch configs/binaries for ad-hoc smoke tests under
  `/tmp/opencode/`, not the repo.
