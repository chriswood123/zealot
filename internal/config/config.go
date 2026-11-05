// Package config loads per-instance node configuration for zealot serve.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// NodeConfig is the per-instance configuration consumed by `zealot serve`.
type NodeConfig struct {
	// Service is the logical name of this instance; used in metrics labels,
	// logs and DNS when generating manifests.
	Service string `yaml:"service"`
	// Listen is the address the HTTP server binds to (e.g. ":8080").
	Listen string `yaml:"listen"`
	// Datastores are named backing-service connections this node may query.
	Datastores map[string]Datastore `yaml:"datastores"`
	// Endpoints map request paths to pipelines.
	Endpoints []Endpoint `yaml:"endpoints"`
}

// Datastore configures one backing-service connection.
type Datastore struct {
	// Driver is "postgres" or "redis".
	Driver string `yaml:"driver"`
	// DSN is the Postgres connection string, e.g.
	// "postgres://user:pass@host:5432/db?sslmode=disable".
	DSN string `yaml:"dsn"`
	// Addr, Password and DB configure Redis ("host:6379").
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	// MaxConns caps the connection pool (Postgres; 0 = pgx default).
	MaxConns int `yaml:"maxConns"`
}

// Endpoint binds a request path to a pipeline.
type Endpoint struct {
	Path     string  `yaml:"path"`
	Pipeline []Step `yaml:"pipeline"`
}

// Step is a single pipeline action. Exactly one of the fields is set.
type Step struct {
	Delay   *DelayStep   `yaml:"delay,omitempty"`
	CPU     *CPUStep     `yaml:"cpu,omitempty"`
	Call    *CallStep    `yaml:"call,omitempty"`
	Query   *QueryStep   `yaml:"query,omitempty"`
	Fault   *FaultStep   `yaml:"fault,omitempty"`
	Respond *RespondStep `yaml:"respond,omitempty"`
}

// Type returns the step kind (used for metrics labels and logs).
func (s Step) Type() string {
	switch {
	case s.Delay != nil:
		return "delay"
	case s.CPU != nil:
		return "cpu"
	case s.Call != nil:
		return "call"
	case s.Query != nil:
		return "query"
	case s.Fault != nil:
		return "fault"
	case s.Respond != nil:
		return "respond"
	default:
		return "unknown"
	}
}

// DelayStep sleeps for a fixed duration or samples one from a distribution.
type DelayStep struct {
	// Duration is a fixed delay, e.g. "50ms".
	Duration string `yaml:"duration"`
	// Distribution samples a delay, e.g. "normal(50ms,5ms)".
	Distribution string `yaml:"distribution"`
}

// CPUStep burns CPU for the given duration via hash iterations.
type CPUStep struct {
	Duration string `yaml:"duration"`
}

// CallStep calls one or more upstream targets.
type CallStep struct {
	Targets  []Target `yaml:"targets"`
	Parallel bool     `yaml:"parallel"`
	// OnError is "failFast" (default) or "continue".
	OnError string `yaml:"onError"`
}

// Target is an upstream zealot instance or external HTTP service.
type Target struct {
	// URL is a full URL, or a bare service name resolved as http://<name>.
	URL string `yaml:"url"`
}

// UnmarshalYAML accepts either a bare string (shorthand for url) or the
// mapping form, so both `targets: [api]` and `targets: [{url: api}]` work.
func (t *Target) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		t.URL = value.Value
		return nil
	}
	type plain Target
	return value.Decode((*plain)(t))
}

// QueryStep queries a backing datastore declared in NodeConfig.Datastores.
type QueryStep struct {
	// Datastore names an entry in the node config's datastores map.
	Datastore string `yaml:"datastore"`
	// Op is the Redis command: GET, SET or DEL. Unused for Postgres.
	Op string `yaml:"op"`
	// Key is the Redis key; supports {rand:N} templates.
	Key string `yaml:"key"`
	// Value is the Redis SET payload; supports {rand:N} templates.
	Value string `yaml:"value"`
	// SQL is a Postgres query; supports {rand:N} templates.
	SQL string `yaml:"sql"`
}

// FaultStep injects errors or extra latency with a probability.
type FaultStep struct {
	Rate    float64 `yaml:"rate"`
	Status  int     `yaml:"status"`
	Latency string  `yaml:"latency"`
}

// RespondStep terminates the pipeline with a response.
type RespondStep struct {
	Status int    `yaml:"status"`
	Body   string `yaml:"body"`
	// BodySize pads the body to at least this size (e.g. "2kb").
	BodySize string `yaml:"bodySize"`
}

// Load reads and validates a node config file.
func Load(path string) (*NodeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg NodeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &cfg, nil
}

// Validate checks the config is usable.
func (c *NodeConfig) Validate() error {
	if c.Service == "" {
		return fmt.Errorf("service is required")
	}
	if c.Listen == "" {
		return fmt.Errorf("listen is required")
	}
	if len(c.Endpoints) == 0 {
		return fmt.Errorf("at least one endpoint is required")
	}
	for name, ds := range c.Datastores {
		if err := ds.validate(); err != nil {
			return fmt.Errorf("datastore %q: %w", name, err)
		}
	}
	drivers := make(map[string]string, len(c.Datastores))
	for name, ds := range c.Datastores {
		drivers[name] = ds.Driver
	}
	seen := map[string]bool{}
	for i, e := range c.Endpoints {
		if e.Path == "" {
			return fmt.Errorf("endpoints[%d]: path is required", i)
		}
		if seen[e.Path] {
			return fmt.Errorf("duplicate endpoint path %q", e.Path)
		}
		seen[e.Path] = true
		if err := ValidateSteps(e.Pipeline, drivers, fmt.Sprintf("endpoints[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSteps semantically validates pipeline steps: durations and sizes
// parse, rates are in range, required fields are present, and query steps
// reference a defined datastore with fields matching its driver. dsDrivers
// maps datastore name → driver ("postgres"/"redis"); where prefixes errors
// with their location.
func ValidateSteps(steps []Step, dsDrivers map[string]string, where string) error {
	for j, s := range steps {
		if err := validateStep(s, dsDrivers); err != nil {
			return fmt.Errorf("%s.pipeline[%d]: %w", where, j, err)
		}
	}
	return nil
}

func validateStep(s Step, dsDrivers map[string]string) error {
	switch {
	case s.Delay != nil:
		if (s.Delay.Duration == "") == (s.Delay.Distribution == "") {
			return fmt.Errorf("delay: exactly one of duration or distribution is required")
		}
		if s.Delay.Duration != "" {
			if _, err := time.ParseDuration(s.Delay.Duration); err != nil {
				return fmt.Errorf("delay: %w", err)
			}
		}
		return ValidateDistribution(s.Delay.Distribution)
	case s.CPU != nil:
		if s.CPU.Duration == "" {
			return fmt.Errorf("cpu: duration is required")
		}
		if _, err := time.ParseDuration(s.CPU.Duration); err != nil {
			return fmt.Errorf("cpu: %w", err)
		}
	case s.Call != nil:
		if len(s.Call.Targets) == 0 {
			return fmt.Errorf("call: at least one target is required")
		}
		for i, t := range s.Call.Targets {
			if t.URL == "" {
				return fmt.Errorf("call: target %d: url is required", i)
			}
		}
		if s.Call.OnError != "" && s.Call.OnError != "failFast" && s.Call.OnError != "continue" {
			return fmt.Errorf("call: onError must be failFast or continue, got %q", s.Call.OnError)
		}
	case s.Fault != nil:
		if s.Fault.Rate < 0 || s.Fault.Rate > 1 {
			return fmt.Errorf("fault: rate must be between 0 and 1")
		}
		if s.Fault.Latency != "" {
			if _, err := time.ParseDuration(s.Fault.Latency); err != nil {
				return fmt.Errorf("fault: %w", err)
			}
		}
		if s.Fault.Status != 0 && (s.Fault.Status < 100 || s.Fault.Status > 599) {
			return fmt.Errorf("fault: status must be 0 or 100-599, got %d", s.Fault.Status)
		}
	case s.Respond != nil:
		if s.Respond.Status != 0 && (s.Respond.Status < 100 || s.Respond.Status > 599) {
			return fmt.Errorf("respond: status must be 0 or 100-599, got %d", s.Respond.Status)
		}
		if s.Respond.BodySize != "" {
			if _, err := ParseSize(s.Respond.BodySize); err != nil {
				return fmt.Errorf("respond: %w", err)
			}
		}
	case s.Query != nil:
		if s.Query.Datastore == "" {
			return fmt.Errorf("query: datastore is required")
		}
		driver, ok := dsDrivers[s.Query.Datastore]
		if !ok {
			return fmt.Errorf("query: references undefined datastore %q", s.Query.Datastore)
		}
		switch driver {
		case "postgres":
			if s.Query.SQL == "" {
				return fmt.Errorf("query: sql is required for postgres datastore %q", s.Query.Datastore)
			}
		case "redis":
			if s.Query.SQL != "" {
				return fmt.Errorf("query: sql is not valid for redis datastore %q", s.Query.Datastore)
			}
			switch s.Query.Op {
			case "GET", "SET", "DEL":
			default:
				return fmt.Errorf("query: op must be GET, SET or DEL for redis, got %q", s.Query.Op)
			}
			if s.Query.Key == "" {
				return fmt.Errorf("query: key is required for redis %s", s.Query.Op)
			}
		}
	default:
		return fmt.Errorf("no step kind set")
	}
	return nil
}

func (d Datastore) validate() error {
	switch d.Driver {
	case "postgres":
		if d.DSN == "" {
			return fmt.Errorf("dsn is required for postgres")
		}
	case "redis":
		if d.Addr == "" {
			return fmt.Errorf("addr is required for redis")
		}
	default:
		return fmt.Errorf("driver must be %q or %q, got %q", "postgres", "redis", d.Driver)
	}
	return nil
}
