// zealot is a config-driven microservice simulator for demos and load
// testing. One binary plays many roles; each instance's behaviour is
// defined by a YAML node config.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/chriswood123/zealot/internal/config"
	"github.com/chriswood123/zealot/internal/generate"
	"github.com/chriswood123/zealot/internal/metrics"
	"github.com/chriswood123/zealot/internal/server"
	"github.com/chriswood123/zealot/internal/topology"
)

const usage = `zealot - config-driven microservice simulator

Usage:
  zealot serve --config <node.yaml>   Run one node
  zealot validate <topology.yaml>     Validate a topology file
  zealot generate <topology.yaml> [flags]   Render Kubernetes manifests
  zealot version                      Print version

Generate flags:
  --out <dir>              Output directory (default "k8s")
  --image <img>            zealot container image (default "zealot:latest")
  --nodeport               Expose the load entrypoint on NodePort 30080
  --prometheus-operator    Emit a ServiceMonitor per zealot service
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			fatal(err)
		}
	case "version":
		fmt.Println(metrics.Version)
	case "validate":
		if err := validate(os.Args[2:]); err != nil {
			fatal(err)
		}
	case "generate":
		if err := generateCmd(os.Args[2:]); err != nil {
			fatal(err)
		}
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func serve(args []string) error {
	var configPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-config", "-c":
			if i+1 >= len(args) {
				return fmt.Errorf("--config requires a path")
			}
			configPath = args[i+1]
			i++
		default:
			return fmt.Errorf("unknown serve argument %q", args[i])
		}
	}
	if configPath == "" {
		return fmt.Errorf("serve requires --config <node.yaml>")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	srv, err := server.New(cfg, log)
	if err != nil {
		return err
	}
	defer srv.Close()
	return srv.ListenAndServe()
}

func validate(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: zealot validate <topology.yaml>")
	}
	top, err := topology.Load(args[0])
	if err != nil {
		return err
	}
	if errs := top.Validate(); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "%s: %d validation error(s):\n", args[0], len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %v\n", e)
		}
		return fmt.Errorf("topology invalid")
	}
	fmt.Printf("topology %q valid: %d services, %d datastores, namespace %q\n",
		top.Name, len(top.Services), len(top.Datastores), top.Namespace)
	return nil
}

func generateCmd(args []string) error {
	var path, out, image string
	var nodeport, promOp bool
	for i := 0; i < len(args); i++ {
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", args[i])
			}
			i++
			return args[i], nil
		}
		switch args[i] {
		case "--out", "-o":
			v, err := value()
			if err != nil {
				return err
			}
			out = v
		case "--image":
			v, err := value()
			if err != nil {
				return err
			}
			image = v
		case "--nodeport":
			nodeport = true
		case "--prometheus-operator":
			promOp = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown generate flag %q", args[i])
			}
			if path != "" {
				return fmt.Errorf("generate takes exactly one topology file")
			}
			path = args[i]
		}
	}
	if path == "" {
		return fmt.Errorf("usage: zealot generate <topology.yaml> [--out k8s] [--image IMG] [--nodeport] [--prometheus-operator]")
	}
	if out == "" {
		out = "k8s"
	}
	top, err := topology.Load(path)
	if err != nil {
		return err
	}
	if errs := top.Validate(); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "%s: %d validation error(s):\n", path, len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %v\n", e)
		}
		return fmt.Errorf("topology invalid")
	}
	paths, err := generate.Write(top, out, generate.Options{
		Image:              image,
		NodePort:           nodeport,
		PrometheusOperator: promOp,
	})
	if err != nil {
		return err
	}
	for _, p := range paths {
		fmt.Println("wrote", p)
	}
	fmt.Printf("generated %d manifest files in %s (namespace %q)\n", len(paths), out, top.Namespace)
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "zealot: %v\n", err)
	os.Exit(1)
}
