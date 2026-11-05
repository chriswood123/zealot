# E2E for `zealot generate` — Kubernetes manifest rendering. No cluster,
# no ports: assertions are on the emitted files.

load helpers

setup_file() {
	[[ -x "$ZEALOT_BIN" ]] || { echo "zealot binary not found (run: make build)" >&2; return 1; }
	mkdir -p "$BATS_FILE_TMPDIR"
}

@test "generate renders one file per resource group" {
	run "$ZEALOT_BIN" generate "$REPO_ROOT/topologies/shop-demo.yaml" --out "$BATS_FILE_TMPDIR/k8s"
	[ "$status" -eq 0 ]
	grep -q 'generated 7 manifest files' <<<"$output"
	# namespace + 2 datastores + 4 services
	for f in 00-namespace api auth cart frontend postgres redis; do
		[ -f "$BATS_FILE_TMPDIR/k8s/$f.yaml" ]
	done
}

@test "service files carry the rendered node config, deployment and service" {
	api="$BATS_FILE_TMPDIR/k8s/api.yaml"
	grep -q '^kind: ConfigMap$' "$api"
	grep -q '^kind: Deployment$' "$api"
	grep -q '^kind: Service$' "$api"
	# The baked-in node config points at the cluster Services.
	grep -q 'dsn: postgres://zealot:zealot@postgres:5432/shop' "$api"
	grep -q 'addr: redis:6379' "$api"
	# ClusterIP by default; probes and config mount are wired.
	grep -q 'path: /healthz' "$api"
	grep -q '/etc/zealot/node.yaml' "$api"
	! grep -q 'NodePort' "$api"
}

@test "postgres gets a PVC, initdb seed SQL and matching credentials" {
	pg="$BATS_FILE_TMPDIR/k8s/postgres.yaml"
	grep -q '^kind: PersistentVolumeClaim$' "$pg"
	grep -q 'name: postgres-initdb' "$pg"
	grep -q 'CREATE TABLE IF NOT EXISTS items' "$pg"
	grep -q 'name: POSTGRES_DB' "$pg"
	grep -q 'mountPath: /docker-entrypoint-initdb.d' "$pg"
}

@test "--nodeport exposes the load entrypoint on 30080" {
	run "$ZEALOT_BIN" generate "$REPO_ROOT/topologies/shop-demo.yaml" \
		--out "$BATS_FILE_TMPDIR/k8s-np" --nodeport
	[ "$status" -eq 0 ]
	grep -q 'type: NodePort' "$BATS_FILE_TMPDIR/k8s-np/frontend.yaml"
	grep -q 'nodePort: 30080' "$BATS_FILE_TMPDIR/k8s-np/frontend.yaml"
	# Non-entrypoint services stay ClusterIP.
	! grep -q 'NodePort' "$BATS_FILE_TMPDIR/k8s-np/cart.yaml"
}

@test "--prometheus-operator emits a ServiceMonitor per zealot service" {
	run "$ZEALOT_BIN" generate "$REPO_ROOT/topologies/shop-demo.yaml" \
		--out "$BATS_FILE_TMPDIR/k8s-sm" --prometheus-operator
	[ "$status" -eq 0 ]
	for f in api auth cart frontend; do
		grep -q '^kind: ServiceMonitor$' "$BATS_FILE_TMPDIR/k8s-sm/$f.yaml"
	done
	# Datastores are not scraped.
	! grep -q 'ServiceMonitor' "$BATS_FILE_TMPDIR/k8s-sm/postgres.yaml"
}

@test "--nodeport without a load entrypoint is a clear error" {
	cat > "$BATS_FILE_TMPDIR/no-load.yaml" <<'EOF'
version: "1"
name: no-load
services:
  web:
    endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
EOF
	run "$ZEALOT_BIN" generate "$BATS_FILE_TMPDIR/no-load.yaml" \
		--out "$BATS_FILE_TMPDIR/k8s-noload" --nodeport
	[ "$status" -eq 1 ]
	grep -q 'load.entrypoint' <<<"$output"
}

@test "an invalid topology is rejected before rendering" {
	cat > "$BATS_FILE_TMPDIR/bad.yaml" <<'EOF'
version: "1"
name: bad
services:
  web:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [ghost] } }] }]
EOF
	run "$ZEALOT_BIN" generate "$BATS_FILE_TMPDIR/bad.yaml" --out "$BATS_FILE_TMPDIR/k8s-bad"
	[ "$status" -eq 1 ]
	grep -q 'validation error(s)' <<<"$output"
	[ ! -e "$BATS_FILE_TMPDIR/k8s-bad/web.yaml" ]
}

@test "generate requires a topology file" {
	run "$ZEALOT_BIN" generate
	[ "$status" -eq 1 ]
	grep -q 'usage: zealot generate' <<<"$output"
}
