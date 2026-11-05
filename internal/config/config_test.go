package config

import (
	"os"
	"path/filepath"
	"testing"
)

const validYAML = `
service: frontend
listen: ":8080"
endpoints:
  - path: /
    pipeline:
      - delay: { distribution: "normal(20ms,5ms)" }
      - call: { targets: [{ url: "api" }, { url: "cart" }], parallel: true }
      - respond: { status: 200, bodySize: 2kb }
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Service != "frontend" || cfg.Listen != ":8080" {
		t.Fatalf("unexpected config %+v", cfg)
	}
	if len(cfg.Endpoints) != 1 || len(cfg.Endpoints[0].Pipeline) != 3 {
		t.Fatalf("unexpected endpoints %+v", cfg.Endpoints)
	}
	call := cfg.Endpoints[0].Pipeline[1].Call
	if call == nil || !call.Parallel || len(call.Targets) != 2 {
		t.Fatalf("unexpected call step %+v", call)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := map[string]string{
		"missing service": `
listen: ":8080"
endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`,
		"missing listen": `
service: x
endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`,
		"no endpoints": `
service: x
listen: ":8080"
`,
		"duplicate path": `
service: x
listen: ":8080"
endpoints:
  - { path: "/", pipeline: [{ respond: { status: 200 } }] }
  - { path: "/", pipeline: [{ respond: { status: 200 } }] }
`,
		"empty step": `
service: x
listen: ":8080"
endpoints: [{ path: "/", pipeline: [{}] }]
`,
	}
	for name, yamlDoc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeTemp(t, yamlDoc)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDatastoreValidation(t *testing.T) {
	valid := `
service: x
listen: ":8080"
datastores:
  pg: { driver: postgres, dsn: "postgres://u:p@h/db" }
  cache: { driver: redis, addr: "h:6379" }
endpoints:
  - path: /
    pipeline:
      - query: { datastore: pg, sql: "SELECT 1" }
      - query: { datastore: cache, op: GET, key: "k" }
      - respond: { status: 200 }
`
	if _, err := Load(writeTemp(t, valid)); err != nil {
		t.Fatalf("valid datastores rejected: %v", err)
	}

	cases := map[string]string{
		"unknown driver": `
service: x
listen: ":8080"
datastores: { mongo: { driver: mongodb } }
endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`,
		"postgres missing dsn": `
service: x
listen: ":8080"
datastores: { pg: { driver: postgres } }
endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`,
		"redis missing addr": `
service: x
listen: ":8080"
datastores: { cache: { driver: redis } }
endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`,
		"query references undefined datastore": `
service: x
listen: ":8080"
datastores: { pg: { driver: postgres, dsn: "postgres://u:p@h/db" } }
endpoints:
  - path: /
    pipeline:
      - query: { datastore: cache, op: GET, key: "k" }
`,
	}
	for name, yamlDoc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeTemp(t, yamlDoc)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
