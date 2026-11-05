// Package config loads per-instance node configuration for zealot serve.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// NodeConfig is the per-instance configuration consumed by `zealot serve`.
type NodeConfig struct {
	// Service is the logical name of this instance; used in metrics labels,
	// logs and DNS when generating manifests.
	Service string `yaml:"service"`
	// Listen is the address the HTTP server binds to (e.g. ":8080").
	Listen string `yaml:"listen"`
	// Endpoints map request paths to pipelines.
	Endpoints []Endpoint `yaml:"endpoints"`
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

// QueryStep queries a backing datastore. M2 will implement execution.
type QueryStep struct {
	Datastore string `yaml:"datastore"`
	Op        string `yaml:"op"`
	Key       string `yaml:"key"`
	SQL       string `yaml:"sql"`
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
	seen := map[string]bool{}
	for i, e := range c.Endpoints {
		if e.Path == "" {
			return fmt.Errorf("endpoints[%d]: path is required", i)
		}
		if seen[e.Path] {
			return fmt.Errorf("duplicate endpoint path %q", e.Path)
		}
		seen[e.Path] = true
		for j, s := range e.Pipeline {
			if s.Type() == "unknown" {
				return fmt.Errorf("endpoints[%d].pipeline[%d]: no step kind set", i, j)
			}
		}
	}
	return nil
}
