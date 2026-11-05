package server

import (
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
	return httptest.NewServer(New(cfg, log).Handler())
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
			{Respond: &config.RespondStep{Status: 200, Body: "hello", BodySize: "100"}},
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
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 100 || !strings.HasPrefix(string(body), "hello") {
		t.Fatalf("unexpected body length %d", len(body))
	}
	// Request ID echoed back.
	if resp.Header.Get(reqid.Header) == "" {
		t.Fatal("missing X-Request-ID response header")
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
