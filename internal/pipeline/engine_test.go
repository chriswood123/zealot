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
	"github.com/chriswood123/zealot/internal/datastore"
	"github.com/chriswood123/zealot/internal/reqid"
)

func newEngine() *Engine { return NewEngine("test", nil) }

func TestRenderTemplate(t *testing.T) {
	fixed := func(int) int { return 42 }
	got := renderTemplate("item:{rand:1000}:{rand:10}", fixed)
	if got != "item:42:42" {
		t.Fatalf("got %q", got)
	}
	if s := renderTemplate("plain", fixed); s != "plain" {
		t.Fatalf("got %q", s)
	}
}

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
	if string(res.Body) != "hi" {
		t.Fatalf("body %q, want %q", res.Body, "hi")
	}
	if res.PadTo != 1024 {
		t.Fatalf("PadTo %d, want 1024", res.PadTo)
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

// fakeStore records queries and can be programmed to fail.
type fakeStore struct {
	queries []*config.QueryStep
	err     error
}

func (f *fakeStore) Exec(_ context.Context, q *config.QueryStep) error {
	f.queries = append(f.queries, q)
	return f.err
}
func (f *fakeStore) Close() error { return nil }

func TestQueryStepExecutes(t *testing.T) {
	store := &fakeStore{}
	e := newEngine()
	e.randInt = func(int) int { return 7 }
	e.SetStores(map[string]datastore.Datastore{"pg": store})

	steps := []config.Step{{Query: &config.QueryStep{
		Datastore: "pg", SQL: "SELECT * FROM items WHERE id = {rand:1000}",
	}}}
	if _, err := runPipeline(e, steps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.queries) != 1 {
		t.Fatalf("store saw %d queries, want 1", len(store.queries))
	}
	if got := store.queries[0].SQL; got != "SELECT * FROM items WHERE id = 7" {
		t.Fatalf("sql %q, want {rand:1000} rendered to 7", got)
	}
}

func TestQueryStepUnknownDatastore(t *testing.T) {
	steps := []config.Step{{Query: &config.QueryStep{Datastore: "nope", Op: "GET"}}}
	_, err := runPipeline(newEngine(), steps)
	if err == nil || !strings.Contains(err.Error(), `datastore "nope" not configured`) {
		t.Fatalf("expected unknown-datastore error, got %v", err)
	}
}

func TestQueryStepRecordsDatastoreMetric(t *testing.T) {
	rec := &recordingRecorder{}
	e := NewEngine("svc", rec)
	e.SetStores(map[string]datastore.Datastore{"cache": &fakeStore{}})

	steps := []config.Step{{Query: &config.QueryStep{Datastore: "cache", Op: "GET", Key: "k"}}}
	if _, err := runPipeline(e, steps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.datastores) != 1 {
		t.Fatalf("recorded %d datastore observations, want 1", len(rec.datastores))
	}
	d := rec.datastores[0]
	if d.name != "cache" || d.op != "get" {
		t.Fatalf("unexpected datastore record %+v", d)
	}
}

func TestQueryStepStoreErrorFailsPipeline(t *testing.T) {
	e := newEngine()
	e.SetStores(map[string]datastore.Datastore{"pg": &fakeStore{err: errors.New("boom")}})
	steps := []config.Step{
		{Query: &config.QueryStep{Datastore: "pg", SQL: "SELECT 1"}},
		{Respond: &config.RespondStep{Status: 200}},
	}
	if _, err := runPipeline(e, steps); err == nil {
		t.Fatal("expected store error to fail the pipeline")
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
	steps      []stepRecord
	datastores []datastoreRecord
}

type stepRecord struct {
	endpoint, stepType, target string
	d                          time.Duration
}

type datastoreRecord struct {
	name, op string
	d        time.Duration
}

func (r *recordingRecorder) ObserveStep(endpoint, stepType, target string, d time.Duration) {
	r.steps = append(r.steps, stepRecord{endpoint, stepType, target, d})
}
func (r *recordingRecorder) IncUpstream(string, int) {}
func (r *recordingRecorder) ObserveDatastore(name, op string, d time.Duration) {
	r.datastores = append(r.datastores, datastoreRecord{name, op, d})
}

func Example_sampleTopology() {
	fmt.Println("pipelines run steps in order: delay -> call -> respond")
	// Output: pipelines run steps in order: delay -> call -> respond
}
