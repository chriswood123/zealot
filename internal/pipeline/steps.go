package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/chriswood123/zealot/internal/config"
	"github.com/chriswood123/zealot/internal/reqid"
)

func defaultRandInt(n int) int { return rand.IntN(n) }

func (e *Engine) execDelay(ctx context.Context, s *config.DelayStep) error {
	d, err := delayDuration(s)
	if err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func delayDuration(s *config.DelayStep) (time.Duration, error) {
	if s.Distribution != "" {
		return config.SampleDistribution(s.Distribution)
	}
	if s.Duration == "" {
		return 0, fmt.Errorf("delay step: duration or distribution required")
	}
	return time.ParseDuration(s.Duration)
}

// execCPU burns CPU for d using SHA-256 iterations. It checks ctx roughly
// every millisecond so request cancellation stops the burn.
func (e *Engine) execCPU(ctx context.Context, s *config.CPUStep) error {
	if s.Duration == "" {
		return fmt.Errorf("cpu step: duration required")
	}
	d, err := time.ParseDuration(s.Duration)
	if err != nil {
		return fmt.Errorf("cpu step: %w", err)
	}
	deadline := time.Now().Add(d)
	seed := []byte("zealot-cpu-burn-seed")
	buf := make([]byte, 8)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		h := sha256.New()
		for i := 0; i < 128; i++ {
			binary.LittleEndian.PutUint64(buf, uint64(i))
			h.Write(seed)
			h.Write(buf)
		}
		seed = h.Sum(nil)
	}
	return nil
}

type callResult struct {
	target string
	status int
	err    error
}

func (e *Engine) execCall(ctx context.Context, s *config.CallStep, req *http.Request) error {
	if len(s.Targets) == 0 {
		return fmt.Errorf("call step: at least one target required")
	}
	results := e.doCalls(ctx, s.Targets, s.Parallel, req)
	var firstErr error
	for _, r := range results {
		status := r.status
		if r.err != nil {
			status = 0
			if firstErr == nil {
				firstErr = r.err
			}
		}
		e.rec.IncUpstream(r.target, status)
	}
	if firstErr != nil && !strings.EqualFold(s.OnError, "continue") {
		return failFastError{err: firstErr}
	}
	return nil
}

// doCall performs one upstream HTTP GET, propagating the request ID header.
func (e *Engine) doCall(ctx context.Context, t config.Target, req *http.Request) callResult {
	u := renderTemplate(t.URL, e.randInt)
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	up, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return callResult{target: t.URL, err: fmt.Errorf("build request: %w", err)}
	}
	if id := reqid.FromContext(ctx); id != "" {
		up.Header.Set(reqid.Header, id)
	}
	// Propagate trace headers if present (W3C + baggage).
	for _, h := range []string{"traceparent", "tracestate", "baggage"} {
		if v := req.Header.Get(h); v != "" {
			up.Header.Set(h, v)
		}
	}
	resp, err := e.client.Do(up)
	if err != nil {
		return callResult{target: t.URL, err: fmt.Errorf("call %s: %w", t.URL, err)}
	}
	defer resp.Body.Close()
	// Drain to allow connection reuse; upstream body is discarded by design.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return callResult{target: t.URL, status: resp.StatusCode}
}

func (e *Engine) execFault(s *config.FaultStep, res *Result) error {
	if s.Rate < 0 || s.Rate > 1 {
		return fmt.Errorf("fault step: rate must be between 0 and 1")
	}
	if rand.Float64() >= s.Rate {
		return nil
	}
	if s.Latency != "" {
		d, err := time.ParseDuration(s.Latency)
		if err != nil {
			return fmt.Errorf("fault step: %w", err)
		}
		time.Sleep(d)
	}
	if s.Status != 0 {
		res.Status = s.Status
	}
	return nil
}

func (e *Engine) execRespond(s *config.RespondStep, res *Result) error {
	if s.Status != 0 {
		res.Status = s.Status
	}
	res.Body = []byte(renderTemplate(s.Body, e.randInt))
	if s.BodySize != "" {
		n, err := config.ParseSize(s.BodySize)
		if err != nil {
			return fmt.Errorf("respond step: %w", err)
		}
		res.PadTo = n
	}
	return nil
}

// renderTemplate expands {rand:N} placeholders to a random integer in [0,N).
func renderTemplate(s string, randInt func(int) int) string {
	for {
		start := strings.Index(s, "{rand:")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			return s
		}
		end += start
		var n int
		if _, err := fmt.Sscanf(s[start+6:end], "%d", &n); err != nil || n <= 0 {
			return s
		}
		s = s[:start] + fmt.Sprint(randInt(n)) + s[end+1:]
	}
}
