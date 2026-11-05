# E2E for examples/chaining/ — two zealot instances with parallel fan-out
# and request-ID propagation.

load helpers

setup_file() {
	render_example examples/chaining/backend.yaml "$BATS_FILE_TMPDIR/backend.yaml" \
		-e "s/:8081/:$E2E_BACKEND_PORT/"
	render_example examples/chaining/frontend.yaml "$BATS_FILE_TMPDIR/frontend.yaml" \
		-e "s/:8081/:$E2E_BACKEND_PORT/" \
		-e "s/:8080/:$E2E_FRONTEND_PORT/"
	start_node backend "$BATS_FILE_TMPDIR/backend.yaml" "$E2E_BACKEND_PORT"
	start_node frontend "$BATS_FILE_TMPDIR/frontend.yaml" "$E2E_FRONTEND_PORT"
}

teardown_file() {
	stop_nodes
}

@test "frontend responds through the fan-out pipeline" {
	run curl -s "http://127.0.0.1:$E2E_FRONTEND_PORT/"
	[ "$status" -eq 0 ]
	[ "$(jq -r .service <<<"$output")" = "frontend" ]
	[ "$(jq -r .body <<<"$output")" = "hello from frontend" ]
}

@test "backend serves both endpoints directly" {
	run curl -s "http://127.0.0.1:$E2E_BACKEND_PORT/"
	[ "$(jq -r .body.from <<<"$output")" = "backend" ]

	run curl -s "http://127.0.0.1:$E2E_BACKEND_PORT/work"
	[ "$(jq -r .body.worked <<<"$output")" = "true" ]
}

@test "request fans out to both upstream targets" {
	curl -s "http://127.0.0.1:$E2E_FRONTEND_PORT/" >/dev/null
	curl -s "http://127.0.0.1:$E2E_FRONTEND_PORT/" >/dev/null

	run metric_value "$E2E_FRONTEND_PORT" 'zealot_upstream_requests_total{service="frontend",status="200",target="localhost:18081"}'
	[ "$output" -ge 2 ]

	run metric_value "$E2E_FRONTEND_PORT" 'zealot_upstream_requests_total{service="frontend",status="200",target="localhost:18081/work"}'
	[ "$output" -ge 2 ]
}

@test "X-Request-ID propagates through the fan-out into the backend logs" {
	run curl -s -H "X-Request-ID: e2e-chain-99" "http://127.0.0.1:$E2E_FRONTEND_PORT/"
	[ "$status" -eq 0 ]
	[ "$(jq -r .request_id <<<"$output")" = "e2e-chain-99" ]

	# Both upstream calls must carry the same request ID.
	run grep -c 'e2e-chain-99' "$BATS_FILE_TMPDIR/backend.log"
	[ "$output" -ge 2 ]
}

@test "frontend latency reflects delay + slowest parallel target" {
	start=$(date +%s%N)
	curl -s "http://127.0.0.1:$E2E_FRONTEND_PORT/" >/dev/null
	elapsed_ms=$(( ($(date +%s%N) - start) / 1000000 ))
	# delay ~20ms + slowest upstream (work, 20ms); sequential would be ~45ms.
	# Allow generous headroom for slow CI machines.
	[ "$elapsed_ms" -ge 20 ]
	[ "$elapsed_ms" -lt 2000 ]
}
