package topology

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "topology.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const minimalValid = `
version: "1"
name: demo
services:
  web:
    endpoints:
      - path: /
        pipeline:
          - respond: { status: 200 }
`

func load(t *testing.T, doc string) *Topology {
	t.Helper()
	top, err := Load(writeTemp(t, doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return top
}

func requireValid(t *testing.T, top *Topology) {
	t.Helper()
	if errs := top.Validate(); len(errs) > 0 {
		t.Fatalf("expected valid topology, got %v", errs)
	}
}

// requireInvalid asserts validation fails and one error mentions want.
func requireInvalid(t *testing.T, top *Topology, want string) {
	t.Helper()
	errs := top.Validate()
	if len(errs) == 0 {
		t.Fatalf("expected invalid topology (%q), got no errors", want)
	}
	for _, e := range errs {
		if strings.Contains(e.Error(), want) {
			return
		}
	}
	t.Fatalf("no error mentioning %q in %v", want, errs)
}

func TestMinimalValid(t *testing.T) {
	top := load(t, minimalValid)
	requireValid(t, top)
	// Defaults applied.
	if top.Namespace != "demo" {
		t.Fatalf("namespace %q, want defaulted to name", top.Namespace)
	}
	if top.Services["web"].Replicas != 1 {
		t.Fatalf("replicas %d, want defaulted to 1", top.Services["web"].Replicas)
	}
}

func TestShopDemoExampleValid(t *testing.T) {
	top, err := Load("../../topologies/shop-demo.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	requireValid(t, top)
	if top.Datastores["postgres"].Type != "postgres" {
		t.Fatalf("postgres type not inferred: %+v", top.Datastores["postgres"])
	}
	if top.Datastores["redis"].Image != "redis:7" {
		t.Fatalf("redis image not defaulted: %+v", top.Datastores["redis"])
	}
}

func TestVersion(t *testing.T) {
	requireInvalid(t, load(t, `
version: "2"
name: demo
services: { web: { endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }] } }
`), `version must be "1"`)
}

func TestNameRequired(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
services: { web: { endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }] } }
`), "name is required")
}

func TestDNS1123Names(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
services: { "Web_Frontend": { endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }] } }
`), "not a valid DNS-1123 name")
}

func TestNoServices(t *testing.T) {
	requireInvalid(t, load(t, `version: "1"`+"\nname: demo\n"), "at least one service")
}

func TestResourcesQuantities(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  web:
    resources: { cpu: "lots" }
    endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`), "resources.cpu")
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  web:
    resources: { memory: "plenty" }
    endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`), "resources.memory")
}

func TestDuplicateEndpointPath(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  web:
    endpoints:
      - { path: "/", pipeline: [{ respond: { status: 200 } }] }
      - { path: "/", pipeline: [{ respond: { status: 201 } }] }
`), "duplicate path")
}

func TestServiceDatastoreNameCollision(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
datastores:
  cache: { type: redis }
services:
  cache:
    endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`), "used by both a service and a datastore")
}

func TestCallTargetMustBeKnownService(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  web:
    endpoints:
      - { path: "/", pipeline: [{ call: { targets: [ghost] } }, { respond: { status: 200 } }] }
`), "not a service in this topology")
}

func TestCallTargetExternalURLOk(t *testing.T) {
	requireValid(t, load(t, `
version: "1"
name: demo
services:
  web:
    endpoints:
      - { path: "/", pipeline: [{ call: { targets: ["https://example.com/api"] } }, { respond: { status: 200 } }] }
`))
}

func TestCycleDetection(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  a:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [b] } }, { respond: { status: 200 } }] }]
  b:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [a] } }, { respond: { status: 200 } }] }]
`), "call cycle detected: a -> b -> a")
}

func TestSelfCycle(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  a:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [a] } }, { respond: { status: 200 } }] }]
`), "call cycle detected: a -> a")
}

func TestAcyclicDiamondOK(t *testing.T) {
	requireValid(t, load(t, `
version: "1"
name: demo
services:
  top:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [left, right], parallel: true } }, { respond: { status: 200 } }] }]
  left:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [bottom] } }, { respond: { status: 200 } }] }]
  right:
    endpoints: [{ path: "/", pipeline: [{ call: { targets: [bottom] } }, { respond: { status: 200 } }] }]
  bottom:
    endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`))
}

func TestQueryDatastoreReference(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  web:
    endpoints: [{ path: "/", pipeline: [{ query: { datastore: pg, sql: "SELECT 1" } }, { respond: { status: 200 } }] }]
`), "undefined datastore")
}

func TestQueryDriverMismatch(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
datastores:
  cache: { type: redis }
services:
  web:
    endpoints: [{ path: "/", pipeline: [{ query: { datastore: cache, sql: "SELECT 1" } }, { respond: { status: 200 } }] }]
`), "sql is not valid for redis")
}

func TestDatastoreTypeRequiredWhenNotInferable(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
datastores:
  main-db: {}
services:
  web:
    endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`), "type must be postgres or redis")
}

func TestInvalidStepFields(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  web:
    endpoints: [{ path: "/", pipeline: [{ delay: { duration: "soon" } }] }]
`), "delay")
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  web:
    endpoints: [{ path: "/", pipeline: [{ fault: { rate: 1.5, status: 500 } }] }]
`), "rate must be between 0 and 1")
}

func TestLoadEntrypoint(t *testing.T) {
	requireInvalid(t, load(t, `
version: "1"
name: demo
services:
  web:
    endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
load: { entrypoint: ghost, k6: { vus: 10, duration: 5m } }
`), "load.entrypoint")
}

func TestScalarAndMappingTargets(t *testing.T) {
	top := load(t, `
version: "1"
name: demo
services:
  a:
    endpoints:
      - path: /
        pipeline:
          - call:
              targets:
                - b                  # scalar shorthand
                - { url: b/path }    # mapping form with path
          - respond: { status: 200 }
  b:
    endpoints: [{ path: "/", pipeline: [{ respond: { status: 200 } }] }]
`)
	requireValid(t, top)
	targets := top.Services["a"].Endpoints[0].Pipeline[0].Call.Targets
	if targets[0].URL != "b" || targets[1].URL != "b/path" {
		t.Fatalf("unexpected targets %+v", targets)
	}
}
