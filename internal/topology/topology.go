// Package topology parses and validates zealot topology files: a single
// YAML document describing a whole microservices graph — services (each a
// zealot instance), their endpoints/pipelines, backing datastores, and the
// load entrypoint. It is the input to `zealot validate` and (M4)
// `zealot generate`.
package topology

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/chriswood123/zealot/internal/config"
)

// Version is the only topology schema version understood today.
const Version = "1"

// Topology is the parsed topology file. Validate applies defaults, so a
// validated Topology is also the generator's input contract.
type Topology struct {
	Version    string               `yaml:"version"`
	Name       string               `yaml:"name"`
	Namespace  string               `yaml:"namespace"`
	Datastores map[string]Datastore `yaml:"datastores"`
	Services   map[string]Service   `yaml:"services"`
	Load       *LoadSpec            `yaml:"load"`
}

// Datastore is a backing service deployed alongside the zealot services.
type Datastore struct {
	// Type is "postgres" or "redis". Optional when the map key already says
	// it (e.g. a datastore named "postgres").
	Type string `yaml:"type"`
	// Image overrides the container image (defaults per type).
	Image string `yaml:"image"`
	// Database is the Postgres database name (defaults to the datastore name).
	Database string `yaml:"database"`
	// SeedSQL runs via the Postgres container's initdb mechanism.
	SeedSQL string `yaml:"seedSQL"`
}

// Service is one zealot instance role in the graph.
type Service struct {
	// Replicas is the pod count; defaults to 1.
	Replicas  int               `yaml:"replicas"`
	Resources Resources         `yaml:"resources"`
	Endpoints []config.Endpoint `yaml:"endpoints"`
}

// Resources maps to Kubernetes requests=limits for the deployment.
type Resources struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

// LoadSpec describes how k6 should drive the topology (used by M5 demo kit).
type LoadSpec struct {
	// Entrypoint names the service k6 targets.
	Entrypoint string `yaml:"entrypoint"`
	K6         *K6    `yaml:"k6"`
}

// K6 holds k6 run parameters.
type K6 struct {
	VUs      int    `yaml:"vus"`
	Duration string `yaml:"duration"`
}

// Load parses a topology file. It does not validate; call Validate.
func Load(path string) (*Topology, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read topology: %w", err)
	}
	var t Topology
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse topology %s: %w", path, err)
	}
	return &t, nil
}

var (
	dns1123Re = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	cpuRe     = regexp.MustCompile(`^\d+(\.\d+)?m?$`)
	memoryRe  = regexp.MustCompile(`^\d+(\.\d+)?(Ki|Mi|Gi|Ti|k|M|G|T)?$`)
)

func validDNS1123(s string) bool {
	return len(s) <= 63 && dns1123Re.MatchString(s)
}

// Validate checks the topology and applies defaults (namespace, replicas,
// datastore type/image/database). It collects every problem rather than
// failing on the first, in deterministic order.
func (t *Topology) Validate() []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if t.Version != Version {
		add("version must be %q, got %q", Version, t.Version)
	}
	if t.Name == "" {
		add("name is required")
	} else if !validDNS1123(t.Name) {
		add("name %q is not a valid DNS-1123 name", t.Name)
	}
	if t.Namespace == "" {
		t.Namespace = t.Name
	} else if !validDNS1123(t.Namespace) {
		add("namespace %q is not a valid DNS-1123 name", t.Namespace)
	}

	// Datastores: resolve type, apply defaults, validate.
	dsDrivers := map[string]string{}
	for _, name := range sortedKeys(t.Datastores) {
		ds := t.Datastores[name]
		if !validDNS1123(name) {
			add("datastore %q is not a valid DNS-1123 name", name)
		}
		if ds.Type == "" && (name == "postgres" || name == "redis") {
			ds.Type = name
		}
		switch ds.Type {
		case "postgres":
			if ds.Image == "" {
				ds.Image = "postgres:16"
			}
			if ds.Database == "" {
				ds.Database = name
			}
		case "redis":
			if ds.Image == "" {
				ds.Image = "redis:7"
			}
		default:
			add("datastore %q: type must be postgres or redis (inferable from the name only for those exact names), got %q",
				name, ds.Type)
		}
		t.Datastores[name] = ds
		dsDrivers[name] = ds.Type
	}

	if len(t.Services) == 0 {
		add("at least one service is required")
	}

	// Services: validate shape, pipelines and build the call graph.
	graph := map[string][]string{}
	for _, name := range sortedKeys(t.Services) {
		svc := t.Services[name]
		if !validDNS1123(name) {
			add("service %q is not a valid DNS-1123 name", name)
		}
		if svc.Replicas == 0 {
			svc.Replicas = 1
		} else if svc.Replicas < 0 {
			add("service %q: replicas must be >= 0", name)
		}
		if svc.Resources.CPU != "" && !cpuRe.MatchString(svc.Resources.CPU) {
			add("service %q: resources.cpu %q is not a valid quantity (want e.g. 250m or 1)", name, svc.Resources.CPU)
		}
		if svc.Resources.Memory != "" && !memoryRe.MatchString(svc.Resources.Memory) {
			add("service %q: resources.memory %q is not a valid quantity (want e.g. 128Mi)", name, svc.Resources.Memory)
		}
		if len(svc.Endpoints) == 0 {
			add("service %q: at least one endpoint is required", name)
		}
		seenPaths := map[string]bool{}
		for i, ep := range svc.Endpoints {
			where := fmt.Sprintf("service %q endpoints[%d]", name, i)
			if !strings.HasPrefix(ep.Path, "/") {
				add("%s: path must start with /, got %q", where, ep.Path)
			}
			if seenPaths[ep.Path] {
				add("%s: duplicate path %q", where, ep.Path)
			}
			seenPaths[ep.Path] = true
			if err := config.ValidateSteps(ep.Pipeline, dsDrivers, where); err != nil {
				errs = append(errs, err)
			}
			// Collect intra-topology call edges for cycle detection.
			for _, step := range ep.Pipeline {
				if step.Call == nil {
					continue
				}
				for _, target := range step.Call.Targets {
					if strings.Contains(target.URL, "://") {
						continue // external URL: nothing to check
					}
					host := strings.SplitN(target.URL, "/", 2)[0]
					if _, ok := t.Services[host]; !ok {
						add("%s: call target %q is not a service in this topology (use a full URL for external targets)", where, target.URL)
						continue
					}
					graph[name] = append(graph[name], host)
				}
			}
		}
		t.Services[name] = svc
	}

	// Cycles in the call graph mean unbounded request recursion at runtime:
	// there is no depth limit, so reject them outright.
	if cycle := findCycle(graph); cycle != nil {
		add("call cycle detected: %s (cyclic calls recurse without a depth limit)", strings.Join(cycle, " -> "))
	}

	// Load block.
	if t.Load != nil {
		if _, ok := t.Services[t.Load.Entrypoint]; !ok {
			add("load.entrypoint %q is not a service in this topology", t.Load.Entrypoint)
		}
		if t.Load.K6 != nil {
			if t.Load.K6.VUs < 0 {
				add("load.k6.vus must be >= 0")
			}
			if t.Load.K6.Duration != "" {
				if _, err := time.ParseDuration(t.Load.K6.Duration); err != nil {
					add("load.k6.duration: %v", err)
				}
			}
		}
	}
	return errs
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findCycle returns one cycle in the graph as a node path (first == last),
// or nil if the graph is acyclic.
func findCycle(graph map[string][]string) []string {
	const (
		white = 0 // unvisited
		grey  = 1 // on the current DFS path
		black = 2 // fully explored
	)
	color := map[string]int{}
	var path []string

	var visit func(n string) []string
	visit = func(n string) []string {
		color[n] = grey
		path = append(path, n)
		for _, next := range graph[n] {
			switch color[next] {
			case grey:
				// Found a back edge: slice the path from the first
				// occurrence of next and close the loop.
				for i, p := range path {
					if p == next {
						return append(append([]string{}, path[i:]...), next)
					}
				}
			case white:
				if cycle := visit(next); cycle != nil {
					return cycle
				}
			}
		}
		path = path[:len(path)-1]
		color[n] = black
		return nil
	}

	nodes := sortedKeys(graph)
	for _, n := range nodes {
		if color[n] == white {
			if cycle := visit(n); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}
