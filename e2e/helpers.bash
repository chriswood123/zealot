# Shared helpers for zealot e2e tests. Loaded with `load helpers`.
#
# Tests run the real example configs from examples/, with ports rewritten
# (sed) into a per-file temp dir so they never clash with a developer's own
# instances on 8080-8082.

REPO_ROOT="${BATS_TEST_DIRNAME}/.."
ZEALOT_BIN="${ZEALOT_BIN:-$REPO_ROOT/bin/zealot}"

# Ports used by the e2e suite (example files use 8080-8082).
E2E_FRONTEND_PORT=18080
E2E_BACKEND_PORT=18081
E2E_DATASTORES_PORT=18082

# render_example <example-rel-path> <out-file> [sed-expr...]
# Copies an example config into the file temp dir, applying sed expressions
# (typically port rewrites).
render_example() {
	local src="$REPO_ROOT/$1" dst="$2"
	shift 2
	mkdir -p "$BATS_FILE_TMPDIR"
	sed "$@" "$src" > "$dst"
}

# start_node <name> <config-file>
# Starts a zealot instance in the background; PID and log live in
# $BATS_FILE_TMPDIR. Fails the test if /healthz doesn't answer within 10s.
start_node() {
	local name="$1" config="$2" port="$3"
	[[ -x "$ZEALOT_BIN" ]] || fail "zealot binary not found at $ZEALOT_BIN (run: make build)"
	"$ZEALOT_BIN" serve --config "$config" >"$BATS_FILE_TMPDIR/$name.log" 2>&1 &
	echo $! >>"$BATS_FILE_TMPDIR/pids"
	wait_ready "$port" || {
		echo "--- $name log ---" >&2
		cat "$BATS_FILE_TMPDIR/$name.log" >&2
		fail "node $name on :$port did not become ready"
	}
}

wait_ready() {
	local port="$1"
	for _ in $(seq 1 100); do
		curl -sf "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 && return 0
		sleep 0.1
	done
	return 1
}

# stop_nodes — called from teardown_file; kills everything started via start_node.
stop_nodes() {
	[[ -f "$BATS_FILE_TMPDIR/pids" ]] || return 0
	# shellcheck disable=SC2046
	kill $(cat "$BATS_FILE_TMPDIR/pids") 2>/dev/null || true
}

# metrics_count <port> <metric-selector-grep-pattern>
# Returns the value of the first matching Prometheus sample.
metric_value() {
	local port="$1" pattern="$2"
	curl -s "http://127.0.0.1:$port/metrics" | grep -E "$pattern" | head -1 | awk '{print $2}'
}
