// zealot is a config-driven microservice simulator for demos and load
// testing. One binary plays many roles; each instance's behaviour is
// defined by a YAML node config.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/chriswood123/zealot/internal/config"
	"github.com/chriswood123/zealot/internal/metrics"
	"github.com/chriswood123/zealot/internal/server"
)

const usage = `zealot - config-driven microservice simulator

Usage:
  zealot serve --config <node.yaml>   Run one node
  zealot version                      Print version

Future milestones: validate, generate (see PLAN.md).
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
	case "validate", "generate":
		fatal(fmt.Errorf("%q not yet implemented (M3/M4, see PLAN.md)", os.Args[1]))
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
	return server.New(cfg, log).ListenAndServe()
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "zealot: %v\n", err)
	os.Exit(1)
}
