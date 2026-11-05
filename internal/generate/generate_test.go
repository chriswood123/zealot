package generate

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/chriswood123/zealot/internal/config"
	"github.com/chriswood123/zealot/internal/topology"
)

var update = flag.Bool("update", false, "rewrite golden files")

func loadValid(t *testing.T, path string) *topology.Topology {
	t.Helper()
	top, err := topology.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if errs := top.Validate(); len(errs) > 0 {
		t.Fatalf("Validate: %v", errs)
	}
	return top
}

// TestRenderGolden renders topologies and compares every output file with
// the golden copy under testdata/golden/<case>/. Regenerate with:
//
//	go test ./internal/generate/ -update
func TestRenderGolden(t *testing.T) {
	cases := []struct {
		name     string
		topology string
		opts     Options
	}{
		{"shop-demo", "../../topologies/shop-demo.yaml", Options{Image: "zealot:latest"}},
		{"full-options", "testdata/full-options-topology.yaml", Options{
			Image:              "zealot:v1.2.3",
			NodePort:           true,
			PrometheusOperator: true,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			top := loadValid(t, tc.topology)
			files, err := Render(top, tc.opts)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			dir := filepath.Join("testdata", "golden", tc.name)
			if *update {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for _, f := range files {
					if err := os.WriteFile(filepath.Join(dir, f.Name), []byte(f.Content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read golden dir (run: go test ./internal/generate/ -update): %v", err)
			}
			var wantNames []string
			for _, e := range entries {
				wantNames = append(wantNames, e.Name())
			}
			sort.Strings(wantNames)
			var gotNames []string
			for _, f := range files {
				gotNames = append(gotNames, f.Name)
			}
			sort.Strings(gotNames)
			if strings.Join(wantNames, ",") != strings.Join(gotNames, ",") {
				t.Fatalf("file list mismatch (run with -update)\nwant: %v\ngot:  %v", wantNames, gotNames)
			}
			for _, f := range files {
				want, err := os.ReadFile(filepath.Join(dir, f.Name))
				if err != nil {
					t.Fatalf("read golden %s: %v", f.Name, err)
				}
				if string(want) != f.Content {
					t.Errorf("golden mismatch for %s (run with -update)\n--- want ---\n%s\n--- got ---\n%s",
						f.Name, want, f.Content)
				}
			}
		})
	}
}

func TestNodePortRequiresEntrypoint(t *testing.T) {
	top := loadValid(t, "testdata/no-load-topology.yaml")
	_, err := Render(top, Options{NodePort: true})
	if err == nil || !strings.Contains(err.Error(), "load.entrypoint") {
		t.Fatalf("expected load.entrypoint error, got %v", err)
	}
}

func TestNodeConfigRendersReferencedDatastoresOnly(t *testing.T) {
	top := loadValid(t, "../../topologies/shop-demo.yaml")
	g := &generator{top: top}

	api := g.nodeConfig("api", top.Services["api"])
	if got := api.Datastores["postgres"].DSN; got != "postgres://zealot:zealot@postgres:5432/shop?sslmode=disable" {
		t.Errorf("postgres DSN = %q", got)
	}
	if got := api.Datastores["redis"].Addr; got != "redis:6379" {
		t.Errorf("redis addr = %q", got)
	}
	if api.Listen != ":8080" || api.Service != "api" {
		t.Errorf("unexpected node config header: %+v", api)
	}

	frontend := g.nodeConfig("frontend", top.Services["frontend"])
	if len(frontend.Datastores) != 0 {
		t.Errorf("frontend references no datastores, got %v", frontend.Datastores)
	}
}

// Every rendered node config must round-trip through config.Load: it is the
// exact file the generated pods boot from.
func TestRenderedNodeConfigLoads(t *testing.T) {
	top := loadValid(t, "../../topologies/shop-demo.yaml")
	g := &generator{top: top}
	for _, name := range []string{"frontend", "api", "cart", "auth"} {
		data, err := yaml.Marshal(g.nodeConfig(name, top.Services[name]))
		if err != nil {
			t.Fatalf("marshal %q: %v", name, err)
		}
		p := filepath.Join(t.TempDir(), name+".yaml")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(p)
		if err != nil {
			t.Errorf("rendered node config for %q does not load: %v\n%s", name, err, data)
			continue
		}
		if cfg.Service != name {
			t.Errorf("service = %q, want %q", cfg.Service, name)
		}
	}
}

func TestWriteCreatesOutputDir(t *testing.T) {
	top := loadValid(t, "testdata/no-load-topology.yaml")
	dir := filepath.Join(t.TempDir(), "nested", "k8s")
	paths, err := Write(top, dir, Options{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	// namespace + one service
	if len(paths) != 2 {
		t.Fatalf("wrote %d files, want 2", len(paths))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}
