# E2E pinning docs/SPEC.md v1 surface details: exact response bodies,
# request-ID format, frozen metric buckets/labels, error shape, padding
# math. These are the guarantees future language ports must reproduce.

load helpers

E2E_SPEC_PORT=18080

setup_file() {
	cat > "$BATS_FILE_TMPDIR/spec.yaml" <<EOF
service: spec
listen: ":$E2E_SPEC_PORT"
endpoints:
  - path: /
    pipeline:
      - respond: { status: 200, body: "hello" }
  - path: /padded
    pipeline:
      - respond: { status: 200, body: "x", bodySize: 2kb }
  - path: /dead
    pipeline:
      - call: { targets: ["127.0.0.1:1"] }   # transport error -> 502
EOF
	start_node spec "$BATS_FILE_TMPDIR/spec.yaml" "$E2E_SPEC_PORT"
}

teardown_file() {
	stop_nodes
}

@test "healthz returns the exact spec body" {
	run curl -s "http://127.0.0.1:$E2E_SPEC_PORT/healthz"
	[ "$output" = '{"status":"ok"}' ]
}

@test "generated request IDs are 32 lowercase hex, echoed in header and body" {
	run curl -s -D - "http://127.0.0.1:$E2E_SPEC_PORT/"
	hid=$(grep -i '^X-Request-Id:' <<<"$output" | awk '{print $2}' | tr -d '\r')
	bid=$(tail -1 <<<"$output" | jq -r .request_id)
	[ -n "$hid" ] && [ "$hid" = "$bid" ]
	[[ "$hid" =~ ^[0-9a-f]{32}$ ]]
}

@test "the success envelope carries exactly the spec fields, no padding" {
	run curl -s "http://127.0.0.1:$E2E_SPEC_PORT/"
	[ "$(jq -r 'keys | join(",")' <<<"$output")" = "body,request_id,service,status" ]
}

@test "bodySize pads the envelope to exactly the requested size" {
	run curl -s "http://127.0.0.1:$E2E_SPEC_PORT/padded"
	[ "${#output}" -eq 2048 ]
	[ "$(jq -r .body <<<"$output")" = "x" ]
	jq -e '.padding | length > 0' <<<"$output" >/dev/null
}

@test "a failed pipeline returns 502 with an error object, not the envelope" {
	run curl -s -w '\n%{http_code}' "http://127.0.0.1:$E2E_SPEC_PORT/dead"
	[ "$(tail -1 <<<"$output")" = "502" ]
	body=$(head -1 <<<"$output")
	[ "$(jq -r 'keys | join(",")' <<<"$body")" = "error" ]
	grep -q 'call' <<<"$body"
}

@test "an upstream 5xx does not fail the call step (SPEC §4)" {
	cat > "$BATS_FILE_TMPDIR/flaky.yaml" <<EOF
service: flaky
listen: ":18081"
endpoints:
  - path: /
    pipeline:
      - respond: { status: 200 }
      - fault: { rate: 1.0, status: 500 }
  - path: /via
    pipeline:
      - call: { targets: ["127.0.0.1:18081"] }   # default failFast
      - respond: { status: 200 }
EOF
	start_node flaky "$BATS_FILE_TMPDIR/flaky.yaml" 18081
	# Direct: the injected 500 sticks (fault after respond).
	run curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:18081/"
	[ "$output" = "500" ]
	# Via the call step: 200 — downstream statuses never propagate.
	run curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:18081/via"
	[ "$output" = "200" ]
	run metric_value 18081 'zealot_upstream_requests_total{service="flaky",status="500",target="127.0.0.1:18081"}'
	[ "$output" -ge 1 ]
}

@test "histograms expose the frozen buckets and label sets" {
	run curl -s "http://127.0.0.1:$E2E_SPEC_PORT/metrics"
	grep -q 'zealot_request_duration_seconds_bucket{endpoint="/",service="spec",le="0.001"}' <<<"$output"
	grep -q 'zealot_request_duration_seconds_bucket{endpoint="/",service="spec",le="0.025"}' <<<"$output"
	grep -q 'zealot_request_duration_seconds_bucket{endpoint="/",service="spec",le="+Inf"}' <<<"$output"
	grep -q 'zealot_step_duration_seconds_bucket{endpoint="/",service="spec",step_type="respond",target="",le="10"}' <<<"$output"
	grep -q 'zealot_build_info{lang="go",version=' <<<"$output"
}
