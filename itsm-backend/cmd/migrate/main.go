//go:build migrate
// +build migrate

package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/internal/initialization"
	"itsm-backend/migration"
	"itsm-backend/pkg/seeder"

	"github.com/lib/pq"
	"go.uber.org/zap"
)

var databaseNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var lookupFreshHostIPs = net.LookupIP

func validateDatabaseName(name string) error {
	if !databaseNamePattern.MatchString(name) {
		return fmt.Errorf("invalid database name %q: must start with a letter or underscore and contain only letters, numbers, and underscores", name)
	}
	return nil
}

func main() {
	// Parse command line flags
	up := flag.Bool("up", false, "Apply all pending migrations")
	down := flag.Bool("down", false, "Rollback the last migration")
	status := flag.Bool("status", false, "Show migration status")
	list := flag.Bool("list", false, "List all available migrations")
	rollbackVersion := flag.String("rollback-to", "", "Rollback to a specific version")
	dryRun := flag.Bool("dry-run", false, "Show SQL without executing")
	fresh := flag.Bool("fresh", false, "Development-only: recreate the explicitly confirmed database, create Ent schema, apply the current baseline, and seed")
	seed := flag.Bool("seed", false, "Seed database with initial data")
	seedOnly := flag.Bool("seed-only", false, "Only seed data without running migrations")
	version := flag.Bool("version", false, "Show current database version")
	reset := flag.Bool("reset", false, "Development-only: recreate and bootstrap the explicitly confirmed database")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	sugar := logger.Sugar()

	ctx := context.Background()
	if *fresh || *reset {
		freshDatabase(cfg, sugar)
		return
	}

	// -up applies only registered post-schema migrations to a database whose
	// Ent schema is already managed by the deployment/bootstrap policy.
	db, err := database.InitDB(&cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Get available migrations
	available := getAvailableMigrations()

	if *seed {
		seedData(sugar)
		return
	}

	if *seedOnly {
		count, err := runUpgrade(ctx, db, sugar)
		if err != nil {
			log.Fatalf("Upgrade failed: %v", err)
		}
		fmt.Printf("Applied %d migration(s)\n", count)
		seedData(sugar)
		return
	}

	if *dryRun {
		migrator := migration.NewMigrator(db, sugar)
		fmt.Println("=== Dry Run Mode - No changes will be made ===")
		fmt.Println()
		for _, mig := range available {
			sql, err := migrator.DryRun(ctx, mig)
			if err != nil {
				log.Fatalf("Dry run failed for %s: %v", mig.Version, err)
			}
			fmt.Printf("[%s] %s\n%s\n\n", mig.Version, mig.Description, sql)
		}
		return
	}

	if *status {
		migrator := migration.NewMigrator(db, sugar)
		showStatus(migrator, available)
		return
	}

	if *version {
		migrator := migration.NewMigrator(db, sugar)
		showVersion(migrator, getAvailableMigrations())
		return
	}

	if *list {
		listMigrations(getAvailableMigrations())
		return
	}

	if *up {
		count, err := runUpgrade(ctx, db, sugar)
		if err != nil {
			log.Fatalf("Upgrade failed: %v", err)
		}
		fmt.Printf("Applied %d migration(s)\n", count)
		return
	}

	if *down {
		migrator := migration.NewMigrator(db, sugar)
		if *rollbackVersion != "" {
			rollbackToVersion(migrator, available, *rollbackVersion)
		} else {
			rollbackLast(migrator, available)
		}
		return
	}

	// No command specified, show help
	fmt.Println("Migration CLI for ITSM Backend")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  go run -tags migrate cmd/migrate/main.go -up              Apply pending post-schema migrations to an Ent-schema-ready database")
	fmt.Println("  go run -tags migrate cmd/migrate/main.go -down            Rollback the last migration")
	fmt.Println("  go run -tags migrate cmd/migrate/main.go -rollback-to v2  Rollback to version v2")
	fmt.Println("  go run -tags migrate cmd/migrate/main.go -status         Show migration status")
}

func getAvailableMigrations() []migration.Migration {
	migrations := make([]migration.Migration, len(migration.RegisteredMigrations))
	copy(migrations, migration.RegisteredMigrations)
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations
}

func showStatus(migrator *migration.Migrator, available []migration.Migration) {
	ctx := context.Background()
	applied, pending, err := migrator.Status(ctx, available)
	if err != nil {
		log.Fatalf("Failed to get migration status: %v", err)
	}

	fmt.Println("=== Applied Migrations ===")
	if len(applied) == 0 {
		fmt.Println("  No migrations applied")
	} else {
		for _, m := range applied {
			fmt.Printf("  [%s] %s (applied: %s)\n", m.Version, m.Description, m.AppliedAt.Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Println("")
	fmt.Println("=== Pending Migrations ===")
	if len(pending) == 0 {
		fmt.Println("  No pending migrations")
	} else {
		for _, m := range pending {
			fmt.Printf("  [%s] %s\n", m.Version, m.Description)
		}
	}
}

func runUpgrade(ctx context.Context, db *sql.DB, sugar *zap.SugaredLogger) (int, error) {
	roles, err := migration.LoadSchemaStateRoles(os.Getenv)
	if err != nil {
		return 0, fmt.Errorf("load schema state roles: %w", err)
	}
	lock, err := migration.NewPostgresAdvisoryLock(db)
	if err != nil {
		return 0, err
	}
	applied := 0
	err = migration.RunUpgrade(ctx, migration.UpgradeBootstrap{
		Lock: lock,
		PlanForwardMigrations: func(ctx context.Context, conn migration.BootstrapConnection) ([]migration.Migration, error) {
			return migration.NewMigratorOnConnection(conn, sugar).PlanCurrentUpgrade(ctx, migration.CurrentRelease())
		},
		ApplyForwardMigrations: func(ctx context.Context, conn migration.BootstrapConnection, pending []migration.Migration) error {
			migrator := migration.NewMigratorOnConnection(conn, sugar)
			for _, item := range pending {
				if err := migrator.ApplyMigration(ctx, item); err != nil {
					return err
				}
				applied++
			}
			return nil
		},
		VerifySchema:    migration.VerifyCurrentSchema,
		ApplyPrivileges: migration.ApplySchemaStatePrivilegesOnConnection,
		PromoteState:    migration.PromoteSchemaState,
		Release:         migration.CurrentRelease(),
		Roles:           roles,
	})
	return applied, err
}

func rollbackLast(migrator *migration.Migrator, available []migration.Migration) {
	ctx := context.Background()
	applied, _, err := migrator.Status(ctx, available)
	if err != nil {
		log.Fatalf("Failed to get migration status: %v", err)
	}

	if len(applied) == 0 {
		fmt.Println("No migrations to rollback")
		return
	}

	// Get the last applied migration
	last := applied[len(applied)-1]
	if last.RollbackSQL == "" {
		log.Fatalf("Migration %s has no rollback SQL defined", last.Version)
	}

	if err := migrator.RollbackMigration(ctx, last); err != nil {
		log.Fatalf("Rollback failed: %v", err)
	}
	fmt.Printf("Rolled back migration: %s\n", last.Version)
}

func rollbackToVersion(migrator *migration.Migrator, available []migration.Migration, targetVersion string) {
	ctx := context.Background()
	applied, _, err := migrator.Status(ctx, available)
	if err != nil {
		log.Fatalf("Failed to get migration status: %v", err)
	}

	// Find migrations to rollback (all applied after target version)
	var toRollback []migration.Migration
	for i := len(applied) - 1; i >= 0; i-- {
		if applied[i].Version <= targetVersion {
			break
		}
		toRollback = append(toRollback, applied[i])
	}

	if len(toRollback) == 0 {
		fmt.Printf("No migrations to rollback (already at version %s)\n", targetVersion)
		return
	}

	for _, m := range toRollback {
		if m.RollbackSQL == "" {
			log.Fatalf("Migration %s has no rollback SQL defined", m.Version)
		}
		if err := migrator.RollbackMigration(ctx, m); err != nil {
			log.Fatalf("Rollback failed at %s: %v", m.Version, err)
		}
		fmt.Printf("Rolled back migration: %s\n", m.Version)
	}
}

func seedData(sugar *zap.SugaredLogger) {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	client, err := database.InitDatabase(&cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer client.Close()

	seederInstance := seeder.NewSeeder(client, sugar, cfg)
	seederInstance.SeedAll(context.Background())
	fmt.Println("Seed completed successfully")
}

func validateFreshTarget(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("fresh bootstrap configuration is required")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Deployment.Mode))
	if mode != "development" && mode != "dev" && mode != "test" && mode != "local" {
		return fmt.Errorf("-fresh is development-only; deployment mode %q is not allowed", cfg.Deployment.Mode)
	}
	if os.Getenv("ITSM_ALLOW_DESTRUCTIVE_FRESH") != "true" {
		return fmt.Errorf("-fresh requires ITSM_ALLOW_DESTRUCTIVE_FRESH=true")
	}
	host, err := normalizeFreshHost(cfg.Database.Host)
	if err != nil {
		return err
	}
	databaseName := strings.TrimSpace(cfg.Database.DBName)
	if cfg.Database.Port < 1 || cfg.Database.Port > 65535 {
		return fmt.Errorf("invalid fresh database port %d", cfg.Database.Port)
	}
	if err := validateDatabaseName(databaseName); err != nil {
		return fmt.Errorf("invalid fresh database target: %w", err)
	}
	if isSystemDatabase(databaseName) {
		return fmt.Errorf("-fresh refuses system database %q", databaseName)
	}
	if err := rejectSharedFreshHost(host); err != nil {
		return err
	}
	if os.Getenv("ITSM_FRESH_DATABASE") != databaseName {
		return fmt.Errorf("-fresh requires ITSM_FRESH_DATABASE to equal the exact configured database %q", cfg.Database.DBName)
	}
	if strings.TrimSpace(os.Getenv("ITSM_FRESH_HOST")) != host {
		return fmt.Errorf("-fresh requires ITSM_FRESH_HOST to equal the exact configured host %q", host)
	}
	if strings.TrimSpace(os.Getenv("ITSM_FRESH_PORT")) != strconv.Itoa(cfg.Database.Port) {
		return fmt.Errorf("-fresh requires ITSM_FRESH_PORT to equal the exact configured port %d", cfg.Database.Port)
	}
	return nil
}

func normalizeFreshHost(value string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if host == "" {
		return "", fmt.Errorf("-fresh configured host is required")
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return address.Unmap().String(), nil
	}
	return host, nil
}

func rejectSharedFreshHost(host string) error {
	const forbidden = "192.168.31.66"
	if parsed, err := netip.ParseAddr(host); err == nil {
		if parsed.Unmap().String() == forbidden {
			return fmt.Errorf("-fresh refuses shared host %q", host)
		}
		return nil
	}
	addresses, err := lookupFreshHostIPs(host)
	if err != nil {
		return fmt.Errorf("-fresh cannot resolve configured host %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("-fresh cannot resolve configured host %q", host)
	}
	for _, address := range addresses {
		parsed, ok := netip.AddrFromSlice(address)
		if !ok {
			return fmt.Errorf("-fresh received invalid address resolving configured host %q", host)
		}
		if parsed.Unmap().String() == forbidden {
			return fmt.Errorf("-fresh refuses shared host %q resolved as %q", host, parsed.Unmap())
		}
	}
	return nil
}

func isSystemDatabase(name string) bool {
	switch strings.ToLower(name) {
	case "postgres", "template0", "template1":
		return true
	default:
		return false
	}
}

func freshDatabase(cfg *config.Config, sugar *zap.SugaredLogger) {
	if err := validateFreshTarget(cfg); err != nil {
		log.Fatalf("Refusing fresh bootstrap: %v", err)
	}
	normalized := *cfg
	normalized.Database = cfg.Database
	normalizedHost, _ := normalizeFreshHost(cfg.Database.Host)
	normalized.Database.Host = normalizedHost
	normalized.Database.DBName = strings.TrimSpace(cfg.Database.DBName)
	cfg = &normalized

	// Connect to postgres to drop/create database
	postgresDSN := fmt.Sprintf("host=%s port=%d user=%s dbname=postgres sslmode=%s password=%s",
		cfg.Database.Host, cfg.Database.Port, cfg.Database.User, cfg.Database.SSLMode, cfg.Database.Password)

	postgresDB, err := sql.Open("postgres", postgresDSN)
	if err != nil {
		log.Fatalf("Failed to connect to postgres: %v", err)
	}
	defer postgresDB.Close()

	fmt.Printf("Dropping database %s...\n", cfg.Database.DBName)
	target := pq.QuoteIdentifier(cfg.Database.DBName)
	_, err = postgresDB.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s", target))
	if err != nil {
		log.Fatalf("Failed to drop database: %v", err)
	}

	fmt.Printf("Creating database %s...\n", cfg.Database.DBName)
	_, err = postgresDB.Exec(fmt.Sprintf("CREATE DATABASE %s", target))
	if err != nil {
		log.Fatalf("Failed to create database: %v", err)
	}

	postgresDB.Close()

	// Reconnect to new database
	db, err := database.InitDB(&cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to new database: %v", err)
	}
	defer db.Close()

	roles, err := migration.LoadSchemaStateRoles(os.Getenv)
	if err != nil {
		log.Fatalf("Failed to load schema state roles: %v", err)
	}
	lock, err := migration.NewPostgresAdvisoryLock(db)
	if err != nil {
		log.Fatalf("Failed to configure fresh bootstrap lock: %v", err)
	}
	ctx := context.Background()
	if err := migration.RunFreshBootstrap(ctx, migration.FreshBootstrap{
		Lock:    lock,
		Prepare: migration.PrepareCurrentInfrastructure,
		CreateSchema: func(ctx context.Context, conn migration.BootstrapConnection) error {
			client, err := migration.NewEntClientOnConnection(conn)
			if err != nil {
				return err
			}
			defer client.Close()
			database.RegisterSoftDeleteInterceptors(client)
			return client.Schema.Create(ctx)
		},
		ApplyBaseline:   migration.ApplyCurrentBaseline,
		VerifySchema:    migration.VerifyCurrentSchema,
		ApplyPrivileges: migration.ApplySchemaStatePrivilegesOnConnection,
		PromoteState:    migration.PromoteSchemaState,
		Seed: func(ctx context.Context, conn migration.BootstrapConnection) error {
			return seedFreshDatabase(ctx, conn, cfg, sugar)
		},
		Release: migration.CurrentRelease(),
		Roles:   roles,
	}); err != nil {
		log.Fatalf("Fresh bootstrap failed: %v", err)
	}

	fmt.Println("Fresh reset completed successfully")
}

func seedFreshDatabase(
	ctx context.Context,
	conn migration.BootstrapConnection,
	cfg *config.Config,
	sugar *zap.SugaredLogger,
) error {
	client, err := migration.NewEntClientOnConnection(conn)
	if err != nil {
		return err
	}
	defer client.Close()
	database.RegisterSoftDeleteInterceptors(client)
	components, err := seeder.ProductionInitializers(seeder.NewSeeder(client, sugar, cfg))
	if err != nil {
		return fmt.Errorf("create production initializers: %w", err)
	}
	store, err := initialization.NewSQLStoreOnConnection(conn)
	if err != nil {
		return fmt.Errorf("create initialization store: %w", err)
	}
	engine, err := initialization.NewEngine(store, components, 30*time.Second)
	if err != nil {
		return fmt.Errorf("create initialization engine: %w", err)
	}
	executorID, err := os.Hostname()
	if err != nil {
		executorID = "migration-cli"
	}
	executorID, err = initialization.NewExecutorID(executorID)
	if err != nil {
		return fmt.Errorf("create initialization executor id: %w", err)
	}
	releaseVersion := strings.TrimSpace(os.Getenv("ITSM_RELEASE_VERSION"))
	if releaseVersion == "" {
		releaseVersion = "unversioned"
	}
	_, err = engine.Apply(ctx, initialization.Request{
		Scope:          initialization.Scope{Type: "platform", ID: 0},
		TargetVersion:  seeder.CurrentTenantTemplateVersion,
		ReleaseVersion: releaseVersion,
		RequestedBy:    "migration-cli",
		ExecutorID:     executorID,
	})
	return err
}

func listMigrations(available []migration.Migration) {
	fmt.Println("=== Available Migrations ===")
	for _, mig := range available {
		fmt.Printf("  [%s] %s\n", mig.Version, mig.Description)
		if mig.RollbackSQL != "" {
			fmt.Printf("       ↳ rollback: YES\n")
		} else {
			fmt.Printf("       ↳ rollback: NO\n")
		}
	}
}

func showVersion(migrator *migration.Migrator, available []migration.Migration) {
	ctx := context.Background()
	applied, _, err := migrator.Status(ctx, available)
	if err != nil {
		log.Fatalf("Failed to get status: %v", err)
	}

	if len(applied) == 0 {
		fmt.Println("No migrations applied")
		return
	}

	latest := applied[len(applied)-1]
	fmt.Printf("Current version: %s\n", latest.Version)
	fmt.Printf("Description: %s\n", latest.Description)
	fmt.Printf("Applied at: %s\n", latest.AppliedAt.Format("2006-01-02 15:04:05"))
}
