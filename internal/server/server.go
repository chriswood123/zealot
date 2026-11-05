// Package server wires config endpoints to the pipeline engine behind
// request-ID, logging and metrics middleware.
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/chriswood123/zealot/internal/config"
	"github.com/chriswood123/zealot/internal/metrics"
	"github.com/chriswood123/zealot/internal/pipeline"
	"github.com/chriswood123/zealot/internal/reqid"
)

// Server is a zealot node HTTP server.
type Server struct {
	cfg     *config.NodeConfig
	engine  *pipeline.Engine
	metrics *metrics.Metrics
	log     *slog.Logger
}

// New builds a Server from a node config.
func New(cfg *config.NodeConfig, log *slog.Logger) *Server {
	m := metrics.New(cfg.Service)
	return &Server{
		cfg:     cfg,
		engine:  pipeline.NewEngine(cfg.Service, m),
		metrics: m,
		log:     log.With("service", cfg.Service),
	}
}

// Handler returns the root handler with all middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /metrics", s.metrics.Handler())
	for _, ep := range s.cfg.Endpoints {
		ep := ep
		mux.HandleFunc(ep.Path, func(w http.ResponseWriter, r *http.Request) {
			s.handleEndpoint(w, r, ep)
		})
	}
	return reqid.Middleware(s.loggingMiddleware(mux))
}

// ListenAndServe runs the HTTP server.
func (s *Server) ListenAndServe() error {
	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.log.Info("listening", "addr", s.cfg.Listen)
	return srv.ListenAndServe()
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ok"}`)
}

func (s *Server) handleEndpoint(w http.ResponseWriter, r *http.Request, ep config.Endpoint) {
	start := time.Now()
	res, err := s.engine.Run(r.Context(), ep.Path, ep.Pipeline, r)
	if err != nil {
		s.log.Error("pipeline failed",
			"endpoint", ep.Path,
			"request_id", reqid.FromContext(r.Context()),
			"error", err,
		)
		status := http.StatusBadGateway
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
		s.metrics.ObserveRequest(ep.Path, status, time.Since(start))
		return
	}
	body := s.envelope(r, res)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.Status)
	_, _ = w.Write(body)
	s.metrics.ObserveRequest(ep.Path, res.Status, time.Since(start))
}

// envelope is the standard JSON response format for every zealot endpoint.
type envelope struct {
	Service   string          `json:"service"`
	RequestID string          `json:"request_id"`
	Status    int             `json:"status"`
	Body      json.RawMessage `json:"body,omitempty"`
	Padding   string          `json:"padding,omitempty"`
}

// envelope builds the standard JSON response. A body that is itself valid
// JSON is embedded as-is; anything else becomes a JSON string. If res.PadTo
// exceeds the envelope size, a padding field expands it to exactly PadTo
// bytes (never truncates).
func (s *Server) envelope(r *http.Request, res *pipeline.Result) []byte {
	env := envelope{
		Service:   s.cfg.Service,
		RequestID: reqid.FromContext(r.Context()),
		Status:    res.Status,
	}
	if len(res.Body) > 0 {
		if json.Valid(res.Body) {
			env.Body = json.RawMessage(res.Body)
		} else {
			// Marshal to get a properly quoted JSON string.
			b, _ := json.Marshal(string(res.Body))
			env.Body = b
		}
	}
	out, _ := json.Marshal(env)
	if res.PadTo > 0 {
		const fieldOverhead = len(`,"padding":""`)
		if deficit := res.PadTo - len(out) - fieldOverhead; deficit > 0 {
			env.Padding = strings.Repeat("x", deficit)
			out, _ = json.Marshal(env)
		}
	}
	return out
}

// statusRecorder captures the status code written by downstream handlers for
// logging. Endpoints handled by the engine record their own metrics, so this
// is only used for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request",
			"request_id", reqid.FromContext(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
