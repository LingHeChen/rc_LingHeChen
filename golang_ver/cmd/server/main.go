package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/LingHeChen/rc_LingHeChen/internal/api"
	pgqueue "github.com/LingHeChen/rc_LingHeChen/internal/queue/postgres"
	"github.com/LingHeChen/rc_LingHeChen/internal/vendor"
	"github.com/LingHeChen/rc_LingHeChen/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbURL := env("DATABASE_URL", "postgres://notify:notify@localhost:5432/notify?sslmode=disable")

	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		slog.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	if err := sqlDB.PingContext(ctx); err != nil {
		slog.Error("db ping failed", "err", err)
		os.Exit(1)
	}

	q := pgqueue.New(db)
	vs := vendor.NewStore(db)
	w := worker.New(q,
		worker.WithConcurrency(5),
		worker.WithPollInterval(3*time.Second),
		worker.WithHTTPTimeout(10*time.Second),
	)

	go w.Run(ctx)

	r := gin.New()
	r.Use(gin.Recovery())
	api.New(q, vs).Register(r)

	port := env("PORT", "8080")
	slog.Info("server starting", "port", port)
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.ListenAndServe() }()

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped", "err", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown failed", "err", err)
		}
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
