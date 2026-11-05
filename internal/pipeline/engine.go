// Package pipeline executes the per-endpoint step pipelines that define a
// zealot node's behaviour.
package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/chriswood123/zealot/internal/config"
)

// Recorder observes step and upstream metrics. Implemented by the metrics
// package; nil-safe via NoopRecorder.
type Recorder interface {
	ObserveStep(endpoint, stepType, target string, d time.Duration)
	IncUpstream(target string, status int)
}

// NoopRecorder discards all observations.
type NoopRecorder struct{}

func (NoopRecorder) ObserveStep(string, string, string, time.Duration) {}
func (NoopRecorder) IncUpstream(string, int)                           {}

// Engine runs pipelines for one service instance.
type Engine struct {
	service string
	rec     Recorder
	client  *http.Client
	// randInt renders {rand:N} templates; injectable for tests.
	randInt func(n int) int
}

// NewEngine returns an Engine for the given service name.
func NewEngine(service string, rec Recorder) *Engine {
	if rec == nil {
		rec = NoopRecorder{}
	}
	return &Engine{
		service: service,
		rec:     rec,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		randInt: defaultRandInt,
	}
}

// Result is the outcome of executing a pipeline.
type Result struct {
	Status int
	Body   []byte
}

// Run executes the pipeline for a request. The request's context carries the
// request ID set by middleware. If a step fails with failFast policy, Run
// returns the error; the caller decides the HTTP status.
func (e *Engine) Run(ctx context.Context, endpointPath string, steps []config.Step, req *http.Request) (*Result, error) {
	res := &Result{Status: http.StatusOK}
	for i, step := range steps {
		start := time.Now()
		target := stepTarget(step)
		err := e.execStep(ctx, step, req, res)
		e.rec.ObserveStep(endpointPath, step.Type(), target, time.Since(start))
		if err != nil {
			return nil, fmt.Errorf("pipeline step %d (%s): %w", i, step.Type(), err)
		}
	}
	return res, nil
}

func stepTarget(s config.Step) string {
	switch {
	case s.Call != nil:
		names := make([]string, len(s.Call.Targets))
		for i, t := range s.Call.Targets {
			names[i] = t.URL
		}
		return joinNames(names)
	case s.Query != nil:
		return s.Query.Datastore
	default:
		return ""
	}
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}

func (e *Engine) execStep(ctx context.Context, step config.Step, req *http.Request, res *Result) error {
	switch {
	case step.Delay != nil:
		return e.execDelay(ctx, step.Delay)
	case step.CPU != nil:
		return e.execCPU(ctx, step.CPU)
	case step.Call != nil:
		return e.execCall(ctx, step.Call, req)
	case step.Fault != nil:
		return e.execFault(step.Fault, res)
	case step.Respond != nil:
		return e.execRespond(step.Respond, res)
	case step.Query != nil:
		return fmt.Errorf("query step not yet implemented (M2)")
	default:
		return fmt.Errorf("unknown step")
	}
}

// failFastError wraps an error from a call step; execCall applies policy.
type failFastError struct{ err error }

func (f failFastError) Error() string { return f.err.Error() }
func (f failFastError) Unwrap() error { return f.err }

func (e *Engine) doCalls(ctx context.Context, targets []config.Target, parallel bool, req *http.Request) []callResult {
	results := make([]callResult, len(targets))
	if parallel {
		var wg sync.WaitGroup
		for i, t := range targets {
			wg.Add(1)
			go func(i int, t config.Target) {
				defer wg.Done()
				results[i] = e.doCall(ctx, t, req)
			}(i, t)
		}
		wg.Wait()
		return results
	}
	for i, t := range targets {
		results[i] = e.doCall(ctx, t, req)
	}
	return results
}
