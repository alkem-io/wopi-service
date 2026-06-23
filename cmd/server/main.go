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
	"github.com/alkem-io/wopi-service/internal/adapter/outbound/authbreaker"
	"github.com/alkem-io/wopi-service/internal/adapter/outbound/authhttp"
	"github.com/alkem-io/wopi-service/internal/adapter/outbound/collabora"
	"github.com/alkem-io/wopi-service/internal/adapter/outbound/fileservice"
	natsadapter "github.com/alkem-io/wopi-service/internal/adapter/outbound/nats"
	"github.com/alkem-io/wopi-service/internal/adapter/outbound/postgres"
	"github.com/alkem-io/wopi-service/internal/adapter/outbound/rabbitmq"
	"github.com/alkem-io/wopi-service/internal/config"
	"github.com/alkem-io/wopi-service/internal/domain/port"
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

	// Select auth transport: h2c preferred if AUTH_SERVICE_URL is set, NATS as fallback
	var rawAuthSvc port.AuthService
	var nc *nats.Conn
	switch {
	case cfg.AuthSvc.URL != "":
		rawAuthSvc = authhttp.NewAuthService(cfg.AuthSvc.URL)
		logger.Info("auth transport: h2c", zap.String("url", cfg.AuthSvc.URL))
	case cfg.NATS.URL != "":
		nc, err = connectNATS(cfg.NATS.URL)
		if err != nil {
			logger.Fatal("failed to connect to NATS", zap.Error(err))
		}
		defer nc.Close()
		rawAuthSvc = natsadapter.NewAuthService(nc)
		logger.Info("auth transport: NATS", zap.String("url", cfg.NATS.URL))
	default:
		logger.Fatal("either AUTH_SERVICE_URL or NATS_URL must be set")
	}

	// Wrap with circuit breaker (shared config for both transports)
	authSvc := authbreaker.Wrap(rawAuthSvc, authbreaker.BreakerConfig{
		FailureThreshold: cfg.AuthSvc.BreakerFailures,
		TimeoutSeconds:   cfg.AuthSvc.BreakerTimeoutSecs,
		HalfOpenMax:      cfg.AuthSvc.BreakerHalfOpenMax,
	})

	adapters := createAdapters(wopiPool, authSvc, cfg, logger)
	defer func() { _ = adapters.publisher.Close() }()
	services := createServices(adapters, cfg, logger)

	go services.cleanup.Start(ctx)
	go services.contribution.Start(ctx)

	// Prime discovery cache — needed for editor URL resolution and proof validation
	if _, err := services.discovery.GetDiscovery(ctx); err != nil {
		logger.Warn("failed to prime discovery cache at startup", zap.Error(err))
	}

	handlers := createHandlers(services, wopiPool, nc, logger)
	router := wopihttp.NewRouter(wopihttp.RouterDeps{
		TokenSvc:         services.token,
		DiscoverySvc:     services.discovery,
		TokenHandler:     handlers.token,
		WOPIHandler:      handlers.wopi,
		HealthHandler:    handlers.health,
		DiscoveryHandler: handlers.discovery,
		ContributionWnd:  services.contribution,
		ProofValidation:  cfg.ProofValidation,
		Logger:           logger,
	})

	logger.Info("all services initialized",
		zap.String("wopi_db", cfg.Database.Host),
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
	authSvc      port.AuthService
	fileSvc      *fileservice.FileClient
	discoveryCli *collabora.DiscoveryClient
	publisher    port.QueuePublisher
}

// services holds all domain service instances.
type services struct {
	token        *service.TokenService
	wopi         *service.WOPIService
	discovery    *service.DiscoveryService
	cleanup      *service.CleanupService
	contribution *service.ContributionWindow
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

func createAdapters(pool *pgxpool.Pool, authSvc port.AuthService, cfg *config.Config, logger *zap.Logger) adapters {
	return adapters{
		tokenRepo:    postgres.NewTokenRepository(pool),
		lockRepo:     postgres.NewLockRepository(pool),
		authSvc:      authSvc,
		fileSvc:      fileservice.NewFileClient(cfg.FileService.URL),
		discoveryCli: collabora.NewDiscoveryClient(cfg.CollaboraURL),
		publisher:    newPublisher(cfg, logger),
	}
}

// newPublisher returns a live RabbitMQ publisher when a broker URL is
// configured, or a no-op publisher otherwise (FR-009 — absent config is never
// a crash). The publisher targets the NestJS consumer queue.
func newPublisher(cfg *config.Config, logger *zap.Logger) port.QueuePublisher {
	if !cfg.RabbitMQ.IsConfigured() {
		logger.Info("contribution publisher: no broker configured, using no-op (FR-009)")
		return rabbitmq.NewNoopPublisher()
	}
	logger.Info("contribution publisher: RabbitMQ", zap.String("queue", service.ContributionQueue))
	return rabbitmq.NewPublisher(cfg.RabbitMQ.URL, service.ContributionQueue, logger)
}

func createServices(a adapters, cfg *config.Config, logger *zap.Logger) services {
	discoverySvc := service.NewDiscoveryService(a.discoveryCli, logger)
	tokenSvc := service.NewTokenService(
		a.tokenRepo, a.fileSvc, a.authSvc,
		discoverySvc, cfg.TokenSecret, cfg.BaseURL, cfg.CallbackURL, logger,
	)
	return services{
		token:        tokenSvc,
		wopi:         service.NewWOPIService(a.fileSvc, a.lockRepo, cfg.BaseURL, cfg.FrontendOrigin, cfg.MaxLockLifetime, logger),
		discovery:    discoverySvc,
		cleanup:      service.NewCleanupService(a.tokenRepo, a.lockRepo, logger),
		contribution: service.NewContributionWindow(a.publisher, cfg.ContributionWindow, logger),
	}
}

func createHandlers(s services, pool *pgxpool.Pool, nc *nats.Conn, logger *zap.Logger) httpHandlers {
	return httpHandlers{
		token:     wopihttp.NewTokenHandler(s.token, logger),
		wopi:      wopihttp.NewWOPIHandler(s.wopi, s.contribution, logger),
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
	cancelApp()
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
