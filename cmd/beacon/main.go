package main

import (
	"context"
	"encoding/json"
	"github.com/atbakhru/beacon/internal/beacon"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	db, err := pgxpool.New(ctx, env("DATABASE_URL", "postgres://beacon:beacon@localhost:5432/beacon?sslmode=disable"))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if err = db.Ping(ctx); err != nil {
		panic(err)
	}
	sql, err := os.ReadFile(env("MIGRATIONS_FILE", "db/migrations/001_init.sql"))
	if err != nil {
		panic(err)
	}
	if _, err = db.Exec(ctx, string(sql)); err != nil {
		panic(err)
	}
	store := &beacon.Store{DB: db}
	demo := env("DEMO_MODE", "false") == "true"
	if demo {
		if err = store.Seed(ctx); err != nil {
			panic(err)
		}
	}
	ro, err := redis.ParseURL(env("REDIS_URL", "redis://localhost:6379"))
	if err != nil {
		panic(err)
	}
	rdb := redis.NewClient(ro)
	defer rdb.Close()
	brokers := strings.Split(env("KAFKA_BROKERS", "localhost:19092"), ",")
	for n := 0; n < 30; n++ {
		if err = beacon.EnsureTopics(ctx, brokers); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		panic(err)
	}
	keys := map[string]string{}
	if v := os.Getenv("API_KEYS_JSON"); v != "" {
		if err = json.Unmarshal([]byte(v), &keys); err != nil {
			panic(err)
		}
	}
	if !demo && len(keys) == 0 {
		panic("set API_KEYS_JSON or explicitly enable synthetic DEMO_MODE")
	}
	origins := map[string]bool{}
	for _, o := range strings.Split(env("ALLOWED_ORIGINS", "http://localhost:5173,http://localhost:8088"), ",") {
		origins[o] = true
	}
	api := &beacon.API{Store: store, Redis: rdb, Keys: keys, Origins: origins, EmbedURL: env("EMBED_URL", "http://localhost:8091"), Demo: demo}
	go store.Publish(ctx, brokers)
	go store.Consume(ctx, brokers, rdb)
	srv := &http.Server{Addr: env("LISTEN_ADDR", ":8080"), Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		slog.Info("beacon listening", "address", srv.Addr, "demo", demo)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}
