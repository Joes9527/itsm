package bootstrap

import (
	"context"
	"fmt"
	"sync"
	"time"

	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/internal/workerhealth"
	"itsm-backend/service"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// KAFWorkerApplication owns the single KAF Outbox consumer process. It does
// not construct the HTTP router or start API-owned background jobs.
type KAFWorkerApplication struct {
	cfg          *config.Config
	logger       *zap.SugaredLogger
	dbClient     *ent.Client
	dispatcher   kafOutboxRunner
	healthRunner workerHealthRunner
	closeDB      func()
}

type workerHealthRunner interface {
	Run(context.Context) error
}

func NewKAFWorkerApplication() (*KAFWorkerApplication, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load worker configuration: %w", err)
	}
	if err := cfg.Execution.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Execution.Enabled("kaf_worker") {
		return nil, fmt.Errorf("KAF worker execution is disabled")
	}
	if err := ValidateWebStartupConfig(cfg); err != nil {
		return nil, fmt.Errorf("validate worker startup configuration: %w", err)
	}
	if err := config.ValidateKAFWorkerStartupConfig(cfg); err != nil {
		return nil, err
	}

	logger := initLogger(&cfg.Log).Sugar()
	clients, err := database.InitRuntimeDatabases(&cfg.Database, &cfg.RLS, logger)
	if err != nil {
		return nil, fmt.Errorf("connect worker database: %w", err)
	}
	client := clients.Tenant
	if cfg.Execution.Mode == "candidate" && cfg.RLS.Mode != "enforce" {
		_ = clients.Close()
		return nil, fmt.Errorf("candidate worker requires enforced tenant RLS")
	}
	admissionCtx, admissionCancel := context.WithTimeout(context.Background(), 10*time.Second)
	admissionErr := database.ValidateExecutionRuntime(admissionCtx, database.GetRawDB(), cfg.Execution)
	admissionCancel()
	if admissionErr != nil {
		_ = clients.Close()
		return nil, admissionErr
	}
	executionPolicy, err := database.NewExecutionPolicy(cfg.Execution)
	if err != nil {
		_ = clients.Close()
		return nil, err
	}
	metrics := service.NewKafOutboxMetrics()
	dispatcher, err := service.NewKafOutboxDispatcher(
		service.NewOutboxEventRepository(clients.System, executionPolicy),
		service.KafOutboxConfig{
			WebhookURL:    cfg.KAFOutbox.WebhookURL,
			WebhookSecret: cfg.KAFOutbox.WebhookSecret,
			BatchSize:     cfg.KAFOutbox.BatchSize,
			PollInterval:  cfg.KAFOutbox.PollInterval,
			MaxAttempts:   cfg.KAFOutbox.MaxAttempts,
		}, metrics,
	)
	if err != nil {
		clients.Close()
		return nil, fmt.Errorf("create KAF outbox dispatcher: %w", err)
	}
	healthRunner := workerhealth.New(fmt.Sprintf(":%d", cfg.KAFOutbox.HealthPort), func(ctx context.Context) error {
		db := clients.SystemDB
		if db == nil {
			return fmt.Errorf("database is unavailable")
		}
		return db.PingContext(ctx)
	}, promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))

	return &KAFWorkerApplication{
		cfg:          cfg,
		logger:       logger,
		dbClient:     client,
		dispatcher:   dispatcher,
		healthRunner: healthRunner,
		closeDB:      func() { clients.Close() },
	}, nil
}

func (app *KAFWorkerApplication) Run(ctx context.Context) error {
	if app == nil || app.dispatcher == nil || app.healthRunner == nil {
		return fmt.Errorf("KAF worker dispatcher is required")
	}
	if app.closeDB != nil {
		defer app.closeDB()
	}
	if app.logger != nil {
		defer app.logger.Sync()
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		app.dispatcher.Run(workerCtx)
	}()
	healthResult := make(chan error, 1)
	go func() {
		defer waitGroup.Done()
		healthResult <- app.healthRunner.Run(workerCtx)
	}()
	select {
	case <-ctx.Done():
	case err := <-healthResult:
		if err != nil {
			cancel()
			waitGroup.Wait()
			return fmt.Errorf("run KAF worker health server: %w", err)
		}
		cancel()
		waitGroup.Wait()
		return fmt.Errorf("KAF worker health server stopped unexpectedly")
	}
	cancel()
	waitGroup.Wait()
	return nil
}
