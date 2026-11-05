package datastore

import (
	"context"
	"fmt"

	"github.com/chriswood123/zealot/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgStore is a pooled Postgres connection.
type pgStore struct {
	pool *pgxpool.Pool
}

func newPostgres(ctx context.Context, cfg config.Datastore) (Datastore, error) {
	pc, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if cfg.MaxConns > 0 {
		pc.MaxConns = int32(cfg.MaxConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &pgStore{pool: pool}, nil
}

// Exec runs the SQL and consumes all rows, discarding values — like upstream
// call bodies, row content never flows into the response.
func (p *pgStore) Exec(ctx context.Context, q *config.QueryStep) error {
	if q.SQL == "" {
		return fmt.Errorf("postgres query: sql is required")
	}
	rows, err := p.pool.Query(ctx, q.SQL)
	if err != nil {
		return fmt.Errorf("postgres query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		_ = rows.RawValues()
	}
	return rows.Err()
}

func (p *pgStore) Close() error {
	p.pool.Close()
	return nil
}
