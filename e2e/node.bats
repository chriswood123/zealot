# E2E for examples/node.yaml — the single-node getting-started example.

load helpers

setup_file() {
	render_example examples/node.yaml "$BATS_FILE_TMPDIR/node.yaml" \
		-e "s/:8080/:$E2E_FRONTEND_PORT/"
	start_node frontend "$BATS_FILE_TMPDIR/node.yaml" "$E2E_FRONTEND_PORT"
}

teardown_file() {
	stop_nodes
}

@test "healthz reports ok" {
	run curl -s "http://127.0.0.1:$E2E_FRONTEND_PORT/healthz"
	[ "$status" -eq 0 ]
	[ "$(jq -r .status <<<"$output")" = "ok" ]
}

@test "/ returns the standard JSON envelope" {
	run curl -s "http://127.0.0.1:$E2E_FRONTEND_PORT/"
	[ "$status" -eq 0 ]
	[ "$(jq -r .service <<<"$output")" = "frontend" ]
	[ "$(jq -r .status <<<"$output")" = "200" ]
	[ "$(jq -r .body <<<"$output")" = "hello from frontend" ]
	[ -n "$(jq -r .request_id <<<"$output")" ]
}

@test "caller-provided X-Request-ID is propagated into the envelope and headers" {
	run curl -s -D - -H "X-Request-ID: e2e-node-42" "http://127.0.0.1:$E2E_FRONTEND_PORT/"
	[ "$status" -eq 0 ]
	grep -qi '^X-Request-Id: e2e-node-42' <<<"$output"
	[ "$(tail -1 <<<"$output" | jq -r .request_id)" = "e2e-node-42" ]
}

@test "/fast embeds a JSON body as JSON (not an escaped string)" {
	run curl -s "http://127.0.0.1:$E2E_FRONTEND_PORT/fast"
	[ "$status" -eq 0 ]
	[ "$(jq -r .body.ok <<<"$output")" = "true" ]
}

@test "metrics expose the SPEC v1 series" {
	run curl -s "http://127.0.0.1:$E2E_FRONTEND_PORT/metrics"
	[ "$status" -eq 0 ]
	grep -q 'zealot_requests_total{endpoint="/",service="frontend",status="200"}' <<<"$output"
	grep -q 'zealot_request_duration_seconds_bucket{endpoint="/",service="frontend"' <<<"$output"
	grep -q 'zealot_step_duration_seconds_count{endpoint="/",service="frontend",step_type="delay",target=""}' <<<"$output"
	grep -q 'zealot_build_info{lang="go"' <<<"$output"
}
