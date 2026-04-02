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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := runMigrations(cfg.Database.DSN(), logger); err != nil {
		logger.Fatal("failed to run migrations", zap.Error(err))
	}

	wopiPool, err := connectDB(ctx, cfg.Database.DSN())
	if err != nil {
		logger.Fatal("failed to connect to WOPI database", zap.Error(err))
	}
	defer wopiPool.Close()

	nc, err := connectNATS(cfg.NATS.URL)
	if err != nil {
		logger.Fatal("failed to connect to NATS", zap.Error(err))
	}
	defer nc.Close()

	adapters := createAdapters(wopiPool, nc, cfg)
	services := createServices(adapters, cfg, logger)

	go services.cleanup.Start(ctx)

	handlers := createHandlers(services, wopiPool, nc, logger)
	router := wopihttp.NewRouter(wopihttp.RouterDeps{
		TokenSvc:         services.token,
		DiscoverySvc:     services.discovery,
		TokenHandler:     handlers.token,
		WOPIHandler:      handlers.wopi,
		HealthHandler:    handlers.health,
		DiscoveryHandler: handlers.discovery,
		ProofValidation:  cfg.ProofValidation,
		Logger:           logger,
	})

	logger.Info("all services initialized",
		zap.String("wopi_db", cfg.Database.Host),
		zap.String("nats", cfg.NATS.URL),
		zap.String("file_service", cfg.FileService.URL),
	)

	srv := newHTTPServer(cfg.ServerPort, router)
	go gracefulShutdown(srv, cancel, logger)

	logger.Info("starting WOPI service", zap.String("port", cfg.ServerPort))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatal("server error", zap.Error(err))
	}
}

// adapters holds all outbound adapter instances.
type adapters struct {
	tokenRepo    *postgres.TokenRepository
	lockRepo     *postgres.LockRepository
	sessionRepo  *postgres.SessionRepository
	authSvc      *natsadapter.AuthService
	fileSvc      *fileservice.FileClient
	discoveryCli *collabora.DiscoveryClient
}

// services holds all domain service instances.
type services struct {
	token     *service.TokenService
	wopi      *service.WOPIService
	discovery *service.DiscoveryService
	cleanup   *service.CleanupService
}

// httpHandlers holds all inbound HTTP handler instances.
type httpHandlers struct {
	token     *wopihttp.TokenHandler
	wopi      *wopihttp.WOPIHandler
	health    *wopihttp.HealthHandler
	discovery *wopihttp.DiscoveryHandler
}

func connectDB(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, dsn)
}

func connectNATS(url string) (*nats.Conn, error) {
	return nats.Connect(url)
}

func createAdapters(pool *pgxpool.Pool, nc *nats.Conn, cfg *config.Config) adapters {
	return adapters{
		tokenRepo:    postgres.NewTokenRepository(pool),
		lockRepo:     postgres.NewLockRepository(pool),
		sessionRepo:  postgres.NewSessionRepository(pool),
		authSvc:      natsadapter.NewAuthService(nc),
		fileSvc:      fileservice.NewFileClient(cfg.FileService.URL),
		discoveryCli: collabora.NewDiscoveryClient(cfg.CollaboraURL),
	}
}

func createServices(a adapters, cfg *config.Config, logger *zap.Logger) services {
	tokenSvc := service.NewTokenService(
		a.tokenRepo, a.fileSvc, a.authSvc, a.sessionRepo,
		cfg.TokenSecret, cfg.BaseURL, logger,
	)
	return services{
		token:     tokenSvc,
		wopi:      service.NewWOPIService(a.fileSvc, a.lockRepo, cfg.BaseURL, logger),
		discovery: service.NewDiscoveryService(a.discoveryCli, logger),
		cleanup:   service.NewCleanupService(a.tokenRepo, a.lockRepo, logger),
	}
}

func createHandlers(s services, pool *pgxpool.Pool, nc *nats.Conn, logger *zap.Logger) httpHandlers {
	return httpHandlers{
		token:     wopihttp.NewTokenHandler(s.token, logger),
		wopi:      wopihttp.NewWOPIHandler(s.wopi, logger),
		health:    wopihttp.NewHealthHandler(pool, nc, logger),
		discovery: wopihttp.NewDiscoveryHandler(s.discovery, logger),
	}
}

func newHTTPServer(port string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func gracefulShutdown(srv *http.Server, cancelApp context.CancelFunc, logger *zap.Logger) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("shutting down")
	cancelApp() // cancel app context → stops cleanup goroutine
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", zap.Error(err))
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

	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			logger.Warn("migrate source close error", zap.Error(srcErr))
		}
		if dbErr != nil {
			logger.Warn("migrate db close error", zap.Error(dbErr))
		}
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}

	logger.Info("migrations applied successfully")
	return nil
}
