// Package main is the entry point for the Alkemio WOPI service.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	wopihttp "github.com/alkem-io/wopi-service/internal/adapter/inbound/http"
	"github.com/alkem-io/wopi-service/internal/adapter/outbound/collabora"
	"github.com/alkem-io/wopi-service/internal/adapter/outbound/fileservice"
	natsadapter "github.com/alkem-io/wopi-service/internal/adapter/outbound/nats"
	"github.com/alkem-io/wopi-service/internal/adapter/outbound/postgres"
	"github.com/alkem-io/wopi-service/internal/config"
	"github.com/alkem-io/wopi-service/internal/domain/service"
	"github.com/alkem-io/wopi-service/migrations"
)

func main() {
	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	ctx := context.Background()

	// Run migrations
	if err := runMigrations(cfg.Database.DSN(), logger); err != nil {
		logger.Fatal("failed to run migrations", zap.Error(err))
	}

	// Own database pool
	wopiPool, err := pgxpool.New(ctx, cfg.Database.DSN())
	if err != nil {
		logger.Fatal("failed to connect to WOPI database", zap.Error(err))
	}
	defer wopiPool.Close()

	// NATS connection
	nc, err := nats.Connect(cfg.NATS.URL)
	if err != nil {
		logger.Fatal("failed to connect to NATS", zap.Error(err))
	}
	defer nc.Close()

	// Outbound adapters
	tokenRepo := postgres.NewTokenRepository(wopiPool)
	lockRepo := postgres.NewLockRepository(wopiPool)
	sessionRepo := postgres.NewSessionRepository(wopiPool)
	authSvc := natsadapter.NewAuthService(nc)
	fileSvc := fileservice.NewFileClient(cfg.FileService.URL)
	discoveryCli := collabora.NewDiscoveryClient(cfg.CollaboraURL)

	// Domain services
	tokenSvc := service.NewTokenService(
		tokenRepo, fileSvc, authSvc, sessionRepo,
		cfg.TokenSecret, cfg.BaseURL, logger,
	)
	wopiSvc := service.NewWOPIService(fileSvc, lockRepo, cfg.BaseURL, logger)
	discoverySvc := service.NewDiscoveryService(discoveryCli, logger)

	// Cleanup service
	cleanupSvc := service.NewCleanupService(tokenRepo, lockRepo, logger)
	go cleanupSvc.Start(ctx)

	// Inbound HTTP handlers
	tokenHandler := wopihttp.NewTokenHandler(tokenSvc, logger)
	wopiHandler := wopihttp.NewWOPIHandler(wopiSvc, logger)
	healthHandler := wopihttp.NewHealthHandler(wopiPool, nc, logger)
	discoveryHandler := wopihttp.NewDiscoveryHandler(discoverySvc, logger)

	// Router
	router := wopihttp.NewRouter(tokenSvc, tokenHandler, wopiHandler, healthHandler, discoveryHandler)

	logger.Info("all services initialized",
		zap.String("wopi_db", cfg.Database.Host),
		zap.String("nats", cfg.NATS.URL),
		zap.String("file_service", cfg.FileService.URL),
	)

	srv := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown error", zap.Error(err))
		}
	}()

	logger.Info("starting WOPI service", zap.String("port", cfg.ServerPort))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatal("server error", zap.Error(err))
	}
}

func runMigrations(dsn string, logger *zap.Logger) error {
	d, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, dsn)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}

	logger.Info("migrations applied successfully")
	return nil
}
