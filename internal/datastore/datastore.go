// Package datastore provides pooled backing-service connections (Postgres,
// Redis) used by pipeline query steps. It is a leaf package: it depends only
// on config, so engine, server and tests can share it without import cycles.
package datastore

import (
	"context"
	"fmt"
	"strings"

	"github.com/chriswood123/zealot/internal/config"
)

// Datastore executes one query step against a backing service.
type Datastore interface {
	// Exec runs the query. Misses (e.g. Redis nil) are not errors — under
	// load a cache miss is normal behaviour and must not fail the pipeline.
	Exec(ctx context.Context, q *config.QueryStep) error
	Close() error
}

// Op returns the metric label for the query operation: the Redis command
// lowercased, or "sql" for Postgres queries.
func Op(q *config.QueryStep) string {
	if q.SQL != "" {
		return "sql"
	}
	return strings.ToLower(q.Op)
}

// Connect dials and pings the configured datastore. It fails fast: a node
// whose backing service is unreachable should not start serving.
func Connect(ctx context.Context, name string, cfg config.Datastore) (Datastore, error) {
	var (
		ds  Datastore
		err error
	)
	switch cfg.Driver {
	case "postgres":
		ds, err = newPostgres(ctx, cfg)
	case "redis":
		ds, err = newRedis(ctx, cfg)
	default:
		err = fmt.Errorf("unsupported driver %q", cfg.Driver)
	}
	if err != nil {
		return nil, fmt.Errorf("datastore %q (%s): %w", name, cfg.Driver, err)
	}
	return ds, nil
}
