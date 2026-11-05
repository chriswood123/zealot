package datastore

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/chriswood123/zealot/internal/config"
)

func newTestRedis(t *testing.T) (Datastore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	ds, err := Connect(context.Background(), "test", config.Datastore{
		Driver: "redis",
		Addr:   mr.Addr(),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = ds.Close() })
	return ds, mr
}

func TestRedisSetGet(t *testing.T) {
	ds, mr := newTestRedis(t)
	ctx := context.Background()

	set := &config.QueryStep{Op: "SET", Key: "item:1", Value: "cached"}
	if err := ds.Exec(ctx, set); err != nil {
		t.Fatalf("SET: %v", err)
	}
	if got, err := mr.Get("item:1"); err != nil || got != "cached" {
		t.Fatalf("miniredis has %q (err %v), want %q", got, err, "cached")
	}

	get := &config.QueryStep{Op: "GET", Key: "item:1"}
	if err := ds.Exec(ctx, get); err != nil {
		t.Fatalf("GET existing: %v", err)
	}
}

func TestRedisGetMissIsNotAnError(t *testing.T) {
	ds, _ := newTestRedis(t)
	// Cache misses are normal under load and must not fail the pipeline.
	miss := &config.QueryStep{Op: "GET", Key: "never-set"}
	if err := ds.Exec(context.Background(), miss); err != nil {
		t.Fatalf("GET miss returned error: %v", err)
	}
}

func TestRedisDel(t *testing.T) {
	ds, mr := newTestRedis(t)
	mr.Set("k", "v")
	del := &config.QueryStep{Op: "DEL", Key: "k"}
	if err := ds.Exec(context.Background(), del); err != nil {
		t.Fatalf("DEL: %v", err)
	}
	if mr.Exists("k") {
		t.Fatal("key still exists after DEL")
	}
}

func TestRedisErrors(t *testing.T) {
	ds, _ := newTestRedis(t)
	ctx := context.Background()
	for _, q := range []*config.QueryStep{
		{Op: "GET"},                    // missing key
		{Op: "HGETALL", Key: "k"},      // unsupported op
		{Op: "", Key: "k", SQL: ""},    // no op
	} {
		if err := ds.Exec(ctx, q); err == nil {
			t.Fatalf("expected error for %+v", q)
		}
	}
}

func TestConnectBadRedisAddr(t *testing.T) {
	_, err := Connect(context.Background(), "bad", config.Datastore{
		Driver: "redis", Addr: "127.0.0.1:1",
	})
	if err == nil {
		t.Fatal("expected connect error")
	}
}

func TestOpLabel(t *testing.T) {
	if got := Op(&config.QueryStep{SQL: "SELECT 1"}); got != "sql" {
		t.Fatalf("Op() = %q, want sql", got)
	}
	if got := Op(&config.QueryStep{Op: "GET"}); got != "get" {
		t.Fatalf("Op() = %q, want get", got)
	}
}
