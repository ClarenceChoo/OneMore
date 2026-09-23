package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/clarencechoo/onemore/internal/core"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	mode := flag.String("mode", "api", "api or worker")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Fatal(err)
	}
	cfg.MaxConns = 20
	var db *pgxpool.Pool
	for i := 0; i < 30; i++ {
		db, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			err = db.Ping(ctx)
		}
		if err == nil {
			break
		}
		if db != nil {
			db.Close()
		}
		log.Printf("waiting for database: %v", err)
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if *mode == "api" {
		if err = core.ApplyMigrations(ctx, db); err != nil {
			log.Fatal(err)
		}
		// Older fixed-profile sessions should not bypass the new sign-in screen.
		if _, err = db.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE subject LIKE 'demo:%')`); err != nil {
			log.Fatal(err)
		}
	}
	var rdb *redis.Client
	if raw := os.Getenv("REDIS_URL"); raw != "" {
		opt, err := redis.ParseURL(raw)
		if err != nil {
			log.Fatal(err)
		}
		rdb = redis.NewClient(opt)
		defer rdb.Close()
	}
	store := &core.Store{DB: db, Redis: rdb}
	if *mode == "worker" {
		store.RunWorker(ctx)
		return
	}
	if *mode != "api" {
		log.Fatal("mode must be api or worker")
	}
	if os.Getenv("LOCAL_AUTH") != "true" && strings.TrimSpace(os.Getenv("OIDC_ISSUER")) == "" {
		log.Fatal("configure OIDC_ISSUER or enable LOCAL_AUTH for local use")
	}
	auth, err := core.NewAuth(ctx, store)
	if err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{Addr: addr, Handler: (&core.API{Store: store, Auth: auth}).Router(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("listening on %s", addr)
	if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
