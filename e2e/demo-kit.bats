# E2E for the M5 demo kit: the k6 script stays in sync with the topology,
# and (when k6 is installed) drives a live zealot node end to end.

load helpers

setup_file() {
	[[ -x "$ZEALOT_BIN" ]] || { echo "zealot binary not found (run: make build)" >&2; return 1; }
	mkdir -p "$BATS_FILE_TMPDIR"
}

teardown_file() {
	stop_nodes
}

@test "k6 script defaults match the topology load block" {
	grep -q 'vus: 50' "$REPO_ROOT/topologies/shop-demo.yaml"
	grep -q 'duration: 5m' "$REPO_ROOT/topologies/shop-demo.yaml"
	grep -q 'vus: 50' "$REPO_ROOT/k6/shop-demo.js"
	grep -q "duration: '5m'" "$REPO_ROOT/k6/shop-demo.js"
}

@test "k6 script runs against a live node and propagates request IDs" {
	command -v k6 >/dev/null || skip "k6 not installed"
	render_example examples/chaining/backend.yaml "$BATS_FILE_TMPDIR/backend.yaml" \
		-e "s/:8081/:$E2E_BACKEND_PORT/"
	render_example examples/chaining/frontend.yaml "$BATS_FILE_TMPDIR/frontend.yaml" \
		-e "s/:8081/:$E2E_BACKEND_PORT/" \
		-e "s/:8080/:$E2E_FRONTEND_PORT/"
	start_node backend "$BATS_FILE_TMPDIR/backend.yaml" "$E2E_BACKEND_PORT"
	start_node frontend "$BATS_FILE_TMPDIR/frontend.yaml" "$E2E_FRONTEND_PORT"

	run k6 run --vus 2 --duration 2s \
		-e ZEALOT_BASE_URL="http://127.0.0.1:$E2E_FRONTEND_PORT" \
		"$REPO_ROOT/k6/shop-demo.js"
	[ "$status" -eq 0 ]
	grep -q '✓ status is 200' <<<"$output"

	# Every upstream call carried the k6 request ID into the backend logs.
	run grep -c '"request_id":"k6-' "$BATS_FILE_TMPDIR/backend.log"
	[ "$output" -gt 0 ]
}
