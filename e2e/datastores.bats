# E2E for examples/node-datastores.yaml — query steps against the real
# docker-compose backing services. Skipped without docker.

load helpers

setup_file() {
	command -v docker >/dev/null 2>&1 || skip "docker not available"
	docker compose -f "$REPO_ROOT/docker-compose.yml" up -d --wait >&2
	render_example examples/node-datastores.yaml "$BATS_FILE_TMPDIR/node-datastores.yaml" \
		-e "s/:8082/:$E2E_DATASTORES_PORT/"
	start_node api "$BATS_FILE_TMPDIR/node-datastores.yaml" "$E2E_DATASTORES_PORT"
}

teardown_file() {
	stop_nodes
	# The compose stack is left running: it is the developer's local
	# environment, not owned by this test suite.
}

@test "/item runs redis GET then postgres SELECT" {
	run curl -s "http://127.0.0.1:$E2E_DATASTORES_PORT/item"
	[ "$status" -eq 0 ]
	[ "$(jq -r .service <<<"$output")" = "api" ]
	[ "$(jq -r .body.endpoint <<<"$output")" = "item" ]
}

@test "/cache-write performs a redis SET" {
	run curl -s "http://127.0.0.1:$E2E_DATASTORES_PORT/cache-write"
	[ "$status" -eq 0 ]
	[ "$(jq -r .body.written <<<"$output")" = "true" ]
}

@test "datastore metrics record postgres and redis operations" {
	curl -s "http://127.0.0.1:$E2E_DATASTORES_PORT/item" >/dev/null
	curl -s "http://127.0.0.1:$E2E_DATASTORES_PORT/cache-write" >/dev/null

	run metric_value "$E2E_DATASTORES_PORT" 'zealot_datastore_duration_seconds_count{datastore="postgres",op="sql",service="api"}'
	[ -n "$output" ] && [ "$output" -ge 1 ]

	run metric_value "$E2E_DATASTORES_PORT" 'zealot_datastore_duration_seconds_count{datastore="redis",op="get",service="api"}'
	[ -n "$output" ] && [ "$output" -ge 1 ]

	run metric_value "$E2E_DATASTORES_PORT" 'zealot_datastore_duration_seconds_count{datastore="redis",op="set",service="api"}'
	[ -n "$output" ] && [ "$output" -ge 1 ]
}

@test "node refuses to start with an unreachable datastore" {
	sed "s/localhost:6379/127.0.0.1:1/" "$BATS_FILE_TMPDIR/node-datastores.yaml" \
		> "$BATS_FILE_TMPDIR/dead.yaml"
	run "$ZEALOT_BIN" serve --config "$BATS_FILE_TMPDIR/dead.yaml"
	[ "$status" -eq 1 ]
	grep -q 'datastore "redis"' <<<"$output"
}
