package datastore

import (
	"context"
	"os"
	"testing"

	"github.com/chriswood123/zealot/internal/config"
)

// Integration test against a real Postgres (e.g. from docker-compose.yml).
// Skipped unless ZEALOT_TEST_PG_DSN is set:
//
//	ZEALOT_TEST_PG_DSN='postgres://zealot:zealot@localhost:5432/shop?sslmode=disable' \
//	  go test ./internal/datastore/ -run Postgres
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("ZEALOT_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("ZEALOT_TEST_PG_DSN not set")
	}
	ctx := context.Background()
	ds, err := Connect(ctx, "pg", config.Datastore{
		Driver: "postgres", DSN: dsn, MaxConns: 4,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer ds.Close()

	if err := ds.Exec(ctx, &config.QueryStep{SQL: "SELECT 1"}); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	// Multi-row result sets must be consumed without error.
	if err := ds.Exec(ctx, &config.QueryStep{SQL: "SELECT * FROM generate_series(1,100)"}); err != nil {
		t.Fatalf("generate_series: %v", err)
	}
	// SQL errors must surface.
	if err := ds.Exec(ctx, &config.QueryStep{SQL: "SELECT * FROM no_such_table"}); err == nil {
		t.Fatal("expected error for missing table")
	}
	// Missing SQL must surface.
	if err := ds.Exec(ctx, &config.QueryStep{}); err == nil {
		t.Fatal("expected error for empty sql")
	}
}
