package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chriswood123/zealot/internal/config"
	"github.com/chriswood123/zealot/internal/reqid"
)

func newEngine() *Engine { return NewEngine("test", nil) }

func runPipeline(e *Engine, steps []config.Step) (*Result, error) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	return e.Run(context.Background(), "/", steps, req)
}

func TestDelayStep(t *testing.T) {
	steps := []config.Step{{Delay: &config.DelayStep{Duration: "30ms"}}}
	start := time.Now()
	if _, err := runPipeline(newEngine(), steps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("delay too short: %v", elapsed)
	}
}

func TestCPUStep(t *testing.T) {
	steps := []config.Step{{CPU: &config.CPUStep{Duration: "20ms"}}}
	start := time.Now()
	if _, err := runPipeline(newEngine(), steps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("cpu burn too short: %v", elapsed)
	}
}

func TestRespondStep(t *testing.T) {
	steps := []config.Step{{Respond: &config.RespondStep{
		Status: 201, Body: "hi", BodySize: "1kb",
	}}}
	res, err := runPipeline(newEngine(), steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != 201 {
		t.Fatalf("status %d, want 201", res.Status)
	}
	if len(res.Body) != 1024 {
		t.Fatalf("body length %d, want 1024", len(res.Body))
	}
	if !strings.HasPrefix(string(res.Body), "hi") {
		t.Fatalf("body %q does not start with %q", res.Body[:2], "hi")
	}
}

func TestCallStepSequential(t *testing.T) {
	var hitsA, hitsB atomic.Int32
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsA.Add(1)
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsB.Add(1)
	}))
	defer srvB.Close()

	steps := []config.Step{{Call: &config.CallStep{
		Targets: []config.Target{{URL: srvA.URL}, {URL: srvB.URL}},
	}}}
	if _, err := runPipeline(newEngine(), steps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hitsA.Load() != 1 || hitsB.Load() != 1 {
		t.Fatalf("hits A=%d B=%d, want 1 each", hitsA.Load(), hitsB.Load())
	}
}

func TestCallStepParallel(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
	}))
	defer slow.Close()

	targets := []config.Target{{URL: slow.URL}, {URL: slow.URL}, {URL: slow.URL}}
	steps := []config.Step{{Call: &config.CallStep{Targets: targets, Parallel: true}}}
	if _, err := runPipeline(newEngine(), steps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if maxInFlight.Load() < 2 {
		t.Fatalf("calls did not overlap (max concurrent %d)", maxInFlight.Load())
	}
}

func TestCallStepFailFast(t *testing.T) {
	// Unreachable target.
	steps := []config.Step{
		{Call: &config.CallStep{Targets: []config.Target{{URL: "127.0.0.1:1"}}}},
		{Respond: &config.RespondStep{Status: 200}},
	}
	_, err := runPipeline(newEngine(), steps)
	if err == nil {
		t.Fatal("expected failFast error")
	}
	var ff failFastError
	if !errors.As(err, &ff) {
		t.Fatalf("error %v is not failFastError", err)
	}
}

func TestCallStepOnErrorContinue(t *testing.T) {
	steps := []config.Step{
		{Call: &config.CallStep{
			Targets: []config.Target{{URL: "127.0.0.1:1"}},
			OnError: "continue",
		}},
		{Respond: &config.RespondStep{Status: 200, Body: "ok"}},
	}
	res, err := runPipeline(newEngine(), steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != 200 || string(res.Body) != "ok" {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestCallPropagatesRequestID(t *testing.T) {
	var gotID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get(reqid.Header)
	}))
	defer srv.Close()

	e := newEngine()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := reqid.WithContext(context.Background(), "test-request-id-123")
	_, err := e.Run(ctx, "/", []config.Step{
		{Call: &config.CallStep{Targets: []config.Target{{URL: srv.URL}}}},
	}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotID != "test-request-id-123" {
		t.Fatalf("upstream request ID %q, want %q", gotID, "test-request-id-123")
	}
}

func TestFaultStepAlways(t *testing.T) {
	steps := []config.Step{{Fault: &config.FaultStep{Rate: 1.0, Status: 500}}}
	res, err := runPipeline(newEngine(), steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != 500 {
		t.Fatalf("status %d, want 500", res.Status)
	}
}

func TestFaultStepNever(t *testing.T) {
	steps := []config.Step{{Fault: &config.FaultStep{Rate: 0.0, Status: 500}}}
	res, err := runPipeline(newEngine(), steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != 200 {
		t.Fatalf("status %d, want 200", res.Status)
	}
}

func TestQueryStepNotImplemented(t *testing.T) {
	steps := []config.Step{{Query: &config.QueryStep{Datastore: "redis", Op: "GET"}}}
	_, err := runPipeline(newEngine(), steps)
	if err == nil || !strings.Contains(err.Error(), "M2") {
		t.Fatalf("expected not-implemented error, got %v", err)
	}
}

func TestEngineRecordsStepMetrics(t *testing.T) {
	rec := &recordingRecorder{}
	e := NewEngine("svc", rec)
	steps := []config.Step{{Delay: &config.DelayStep{Duration: "1ms"}}}
	if _, err := runPipeline(e, steps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.steps) != 1 {
		t.Fatalf("recorded %d steps, want 1", len(rec.steps))
	}
	s := rec.steps[0]
	if s.endpoint != "/" || s.stepType != "delay" {
		t.Fatalf("unexpected step record %+v", s)
	}
}

type recordingRecorder struct {
	steps []stepRecord
}

type stepRecord struct {
	endpoint, stepType, target string
	d                          time.Duration
}

func (r *recordingRecorder) ObserveStep(endpoint, stepType, target string, d time.Duration) {
	r.steps = append(r.steps, stepRecord{endpoint, stepType, target, d})
}
func (r *recordingRecorder) IncUpstream(string, int) {}

func Example_sampleTopology() {
	fmt.Println("pipelines run steps in order: delay -> call -> respond")
	// Output: pipelines run steps in order: delay -> call -> respond
}
