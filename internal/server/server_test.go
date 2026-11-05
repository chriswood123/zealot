package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chriswood123/zealot/internal/config"
	"github.com/chriswood123/zealot/internal/reqid"
)

func testServer(t *testing.T, cfg *config.NodeConfig) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(cfg, log)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return httptest.NewServer(srv.Handler())
}

func TestHealthz(t *testing.T) {
	cfg := &config.NodeConfig{
		Service: "svc", Listen: ":0",
		Endpoints: []config.Endpoint{{Path: "/", Pipeline: []config.Step{
			{Respond: &config.RespondStep{Status: 200}},
		}}},
	}
	srv := testServer(t, cfg)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("body %q", body)
	}
}

func TestEndpointPipeline(t *testing.T) {
	cfg := &config.NodeConfig{
		Service: "svc", Listen: ":0",
		Endpoints: []config.Endpoint{{Path: "/", Pipeline: []config.Step{
			{Delay: &config.DelayStep{Duration: "5ms"}},
			{Respond: &config.RespondStep{Status: 200, Body: "hello", BodySize: "200"}},
		}}},
	}
	srv := testServer(t, cfg)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q, want application/json", ct)
	}
	raw, _ := io.ReadAll(resp.Body)
	// bodySize: 200 -> envelope padded to exactly 200 bytes.
	if len(raw) != 200 {
		t.Fatalf("response length %d, want 200: %s", len(raw), raw)
	}
	var env struct {
		Service   string          `json:"service"`
		RequestID string          `json:"request_id"`
		Status    int             `json:"status"`
		Body      json.RawMessage `json:"body"`
		Padding   string          `json:"padding"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, raw)
	}
	if env.Service != "svc" || env.Status != 200 || env.RequestID == "" {
		t.Fatalf("unexpected envelope %+v", env)
	}
	var bodyStr string
	if err := json.Unmarshal(env.Body, &bodyStr); err != nil || bodyStr != "hello" {
		t.Fatalf("body field %s, want %q", env.Body, "hello")
	}
	// Request ID echoed back.
	if resp.Header.Get(reqid.Header) == "" {
		t.Fatal("missing X-Request-ID response header")
	}
}

func TestJSONBodyEmbeddedRaw(t *testing.T) {
	cfg := &config.NodeConfig{
		Service: "svc", Listen: ":0",
		Endpoints: []config.Endpoint{{Path: "/", Pipeline: []config.Step{
			{Respond: &config.RespondStep{Status: 200, Body: `{"ok":true,"n":3}`}},
		}}},
	}
	srv := testServer(t, cfg)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env struct {
		Body map[string]any `json:"body"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, raw)
	}
	if env.Body["ok"] != true || env.Body["n"] != float64(3) {
		t.Fatalf("body not embedded as raw JSON: %s", raw)
	}
	// A JSON body must not arrive double-quoted/escaped.
	if strings.Contains(string(raw), `\"ok\"`) {
		t.Fatalf("body was escaped instead of embedded: %s", raw)
	}
}

func TestRequestIDPassthrough(t *testing.T) {
	cfg := &config.NodeConfig{
		Service: "svc", Listen: ":0",
		Endpoints: []config.Endpoint{{Path: "/", Pipeline: []config.Step{
			{Respond: &config.RespondStep{Status: 200}},
		}}},
	}
	srv := testServer(t, cfg)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set(reqid.Header, "abc-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get(reqid.Header); got != "abc-123" {
		t.Fatalf("request ID %q, want %q", got, "abc-123")
	}
}

func TestMetricsEndpoint(t *testing.T) {
	cfg := &config.NodeConfig{
		Service: "svc", Listen: ":0",
		Endpoints: []config.Endpoint{{Path: "/", Pipeline: []config.Step{
			{Respond: &config.RespondStep{Status: 200}},
		}}},
	}
	srv := testServer(t, cfg)
	defer srv.Close()

	// Generate traffic first so counters exist.
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	mresp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer mresp.Body.Close()
	body, _ := io.ReadAll(mresp.Body)
	for _, want := range []string{
		`zealot_requests_total{endpoint="/",service="svc",status="200"} 1`,
		`zealot_request_duration_seconds_bucket`,
		`zealot_build_info{lang="go"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("metrics output missing %q\n---\n%s", want, body)
		}
	}
}
