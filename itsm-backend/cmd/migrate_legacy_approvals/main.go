package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/service"

	"go.uber.org/zap"
)

func main() {
	tenantID := flag.Int("tenant-id", 0, "只迁移指定租户（0 表示迁移所有租户）")
	dryRun := flag.Bool("dry-run", true, "只生成 BPMN XML 并打印，不真的部署/建绑定（默认开启，显式传 -dry-run=false 才会真的写库）")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	client, err := database.InitDatabaseWithRLS(&cfg.Database, &cfg.RLS, sugar)
	if err != nil {
		sugar.Fatalw("connect database", "error", err)
	}
	defer client.Close()

	ctx := tenantctx.SystemContext(
		context.Background(),
		"ops:migrate_legacy_approvals",
		"batch-migrate tenant-customized ApprovalWorkflow records to BPMN",
	)

	migrationSvc := service.NewLegacyApprovalMigrationService(client)

	if *tenantID > 0 {
		results, err := migrationSvc.MigrateAllForTenant(ctx, *tenantID, *dryRun)
		if err != nil {
			sugar.Fatalw("migration failed", "tenant_id", *tenantID, "error", err)
		}
		printResults(sugar, *tenantID, results, *dryRun)
		return
	}

	byTenant, err := migrationSvc.MigrateAllTenants(ctx, *dryRun)
	if err != nil {
		sugar.Fatalw("migration failed", "error", err)
	}
	for tid, results := range byTenant {
		printResults(sugar, tid, results, *dryRun)
	}
}

func printResults(sugar *zap.SugaredLogger, tenantID int, results []*service.LegacyApprovalMigrationResult, dryRun bool) {
	migrated, skipped, failed := 0, 0, 0
	for _, r := range results {
		switch {
		case r.Error != "":
			failed++
			sugar.Warnw("workflow migration failed", "tenant_id", tenantID, "workflow_id", r.WorkflowID, "error", r.Error)
		case r.Skipped:
			skipped++
		default:
			migrated++
		}
	}
	sugar.Infow("tenant migration summary",
		"tenant_id", tenantID, "dry_run", dryRun,
		"migrated", migrated, "skipped_already_migrated", skipped, "failed", failed,
	)
}
