// Reconcile the navigation of one existing tenant without running full seeding.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/pkg/seeder"
)

func main() {
	tenantID := flag.Int("tenant-id", 0, "existing tenant whose menus will be reconciled")
	requestedBy := flag.String("requested-by", "", "operator identity recorded in the audit log")
	scope := flag.String("scope", "", "required menu scope: workflow|catalog|approvals")
	flag.Parse()
	if *tenantID <= 0 || strings.TrimSpace(*requestedBy) == "" {
		fmt.Fprintln(os.Stderr, "-tenant-id must be positive and -requested-by is required")
		os.Exit(2)
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not load configuration")
		os.Exit(1)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not initialize logger")
		os.Exit(1)
	}
	defer logger.Sync()
	client, err := database.InitDatabaseWithRLS(&cfg.Database, &cfg.RLS, logger.Sugar())
	if err != nil {
		logger.Sugar().Fatalw("could not connect to database")
		return
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(tenantctx.WithTenantID(context.Background(), *tenantID), 30*time.Second)
	defer cancel()
	if err = seeder.NewSeeder(client, logger.Sugar(), cfg).ReconcileMenus(ctx, *tenantID, *requestedBy, *scope); err != nil {
		logger.Sugar().Fatalw("menu reconciliation failed", "tenant_id", *tenantID, "error", err)
	}
	logger.Sugar().Infow("menu reconciliation completed", "tenant_id", *tenantID)
}
