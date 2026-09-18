// initialize_ticket_types repairs the product subtype catalog of an existing
// tenant. It does not run bootstrap, migrations, or the initialization DAG.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/pkg/seeder"

	"go.uber.org/zap"
)

type options struct {
	tenantID int
	actorID  int
	apply    bool
}

func parseOptions(args []string, output io.Writer) (options, error) {
	var result options
	flags := flag.NewFlagSet("initialize_ticket_types", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.IntVar(&result.tenantID, "tenant-id", 0, "existing target tenant ID")
	flags.IntVar(&result.actorID, "actor-id", 0, "existing active native administrator ID")
	flags.BoolVar(&result.apply, "apply", false, "commit missing defaults and audit; omitted means read-only plan")
	if err := flags.Parse(args); err != nil {
		return result, err
	}
	if flags.NArg() != 0 || result.tenantID <= 0 || result.actorID <= 0 {
		return result, fmt.Errorf("positive --tenant-id and --actor-id are required; positional arguments are unsupported")
	}
	return result, nil
}

func run(args []string, output io.Writer) error {
	options, err := parseOptions(args, os.Stderr)
	if err != nil {
		return err
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		return err
	}
	defer logger.Sync()
	client, err := database.InitDatabaseWithRLS(&cfg.Database, &cfg.RLS, logger.Sugar())
	if err != nil {
		return fmt.Errorf("connect target database: %w", err)
	}
	defer client.Close()
	ctx := tenantctx.WithTenantID(context.Background(), options.tenantID)
	result, err := seeder.NewSeeder(client, logger.Sugar(), cfg).InitializeTicketTypes(ctx, options.tenantID, options.actorID, options.apply)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(struct {
		Database string                                 `json:"database"`
		Schema   string                                 `json:"schema"`
		Result   *seeder.TicketTypeInitializationResult `json:"result"`
	}{cfg.Database.DBName, cfg.Database.Schema, result})
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
