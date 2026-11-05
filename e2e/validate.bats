# E2E for `zealot validate` — topology schema, reference checking and cycle
# detection. No servers, no ports.

load helpers

setup_file() {
	[[ -x "$ZEALOT_BIN" ]] || { echo "zealot binary not found (run: make build)" >&2; return 1; }
	mkdir -p "$BATS_FILE_TMPDIR"
}

@test "the shipped shop-demo topology is valid" {
	run "$ZEALOT_BIN" validate "$REPO_ROOT/topologies/shop-demo.yaml"
	[ "$status" -eq 0 ]
	grep -q 'topology "shop-demo" valid: 4 services, 2 datastores' <<<"$output"
}

@test "a cyclic topology is rejected with the cycle path" {
	cat > "$BATS_FILE_TMPDIR/cycle.yaml" <<'EOF'
version: "1"
name: cycle-demo
services:
  a:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [b] } }, { respond: { status: 200 } }] }]
  b:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [a] } }, { respond: { status: 200 } }] }]
EOF
	run "$ZEALOT_BIN" validate "$BATS_FILE_TMPDIR/cycle.yaml"
	[ "$status" -eq 1 ]
	grep -q 'call cycle detected: a -> b -> a' <<<"$output"
}

@test "a call to an undefined service is rejected" {
	cat > "$BATS_FILE_TMPDIR/ghost.yaml" <<'EOF'
version: "1"
name: ghost-demo
services:
  web:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [ghost] } }, { respond: { status: 200 } }] }]
EOF
	run "$ZEALOT_BIN" validate "$BATS_FILE_TMPDIR/ghost.yaml"
	[ "$status" -eq 1 ]
	grep -q 'call target "ghost" is not a service' <<<"$output"
}

@test "multiple problems are reported in one run" {
	cat > "$BATS_FILE_TMPDIR/multi.yaml" <<'EOF'
version: "9"
name: "Bad_Name"
services:
  web:
    resources: { cpu: "lots" }
    endpoints: [{ path: "/", pipeline: [{ fault: { rate: 9, status: 500 } }] }]
EOF
	run "$ZEALOT_BIN" validate "$BATS_FILE_TMPDIR/multi.yaml"
	[ "$status" -eq 1 ]
	grep -q '4 validation error(s)' <<<"$output"
	grep -q 'version must be "1"' <<<"$output"
	grep -q 'not a valid DNS-1123 name' <<<"$output"
	grep -q 'resources.cpu' <<<"$output"
	grep -q 'rate must be between 0 and 1' <<<"$output"
}
