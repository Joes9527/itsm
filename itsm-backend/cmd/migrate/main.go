//go:build migrate
// +build migrate

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"itsm-backend/config"
	"itsm-backend/database"
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
	fresh := flag.Bool("fresh", false, "Development-only: bootstrap an explicitly confirmed empty target, apply post-schema migrations, and seed")
	seed := flag.Bool("seed", false, "Seed database with initial data")
	seedOnly := flag.Bool("seed-only", false, "Only seed data without running migrations")
	version := flag.Bool("version", false, "Show current database version")
	reset := flag.Bool("reset", false, "Rollback all migrations")
	prepareWorkItem := flag.Bool("prepare-workitem", false, "Apply controlled WorkItem preparation")
	retireWorkItem := flag.Bool("retire-workitem", false, "Apply authorized WorkItem retirement")
	evidenceFile := flag.String("evidence-file", "", "Reviewed evidence JSON for a controlled stage")
	flag.Parse()
	operations := 0
	for _, selected := range []bool{*up, *down, *status, *list, *fresh, *seed, *seedOnly, *version, *reset, *prepareWorkItem, *retireWorkItem} {
		if selected {
			operations++
		}
	}
	invalid := ""
	if operations == 0 && !*dryRun {
		invalid = "migration operation is required"
	}
	if operations > 1 {
		invalid = "migration operations are mutually exclusive"
	}
	if *rollbackVersion != "" && !*down {
		invalid = "-rollback-to requires -down"
	}
	if *evidenceFile != "" && !*prepareWorkItem && !*retireWorkItem {
		invalid = "-evidence-file requires a controlled stage"
	}
	if (*prepareWorkItem || *retireWorkItem) && !*dryRun && *evidenceFile == "" {
		if invalid == "" {
			invalid = "controlled stage requires -evidence-file"
		}
	}
	if *dryRun && operations > 0 && !*up && !*prepareWorkItem && !*retireWorkItem {
		invalid = "-dry-run requires up or one controlled stage"
	}
	if flag.NArg() != 0 {
		invalid = "unexpected positional arguments"
	}
	if invalid != "" {
		fmt.Fprintln(os.Stderr, invalid)
		os.Exit(2)
	}
	control, err := migration.LoadControlConfiguration()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Migration control configuration rejected:", err)
		os.Exit(2)
	}
	var evidence migration.MigrationEvidence
	if (*prepareWorkItem || *retireWorkItem) && !*dryRun {
		if control.DeploymentID == "" {
			fmt.Fprintln(os.Stderr, "controlled stage requires trusted configuration")
			os.Exit(2)
		}
		f, err := os.Open(*evidenceFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot read evidence file")
			os.Exit(2)
		}
		decoder := json.NewDecoder(io.LimitReader(f, 16<<20))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&evidence)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = fmt.Errorf("trailing evidence content")
			}
		}
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid evidence JSON")
			os.Exit(2)
		}
	}

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
	if *fresh {
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

	migrator := migration.NewMigrator(db, sugar, control)
	if err := migrator.InspectMigrationTarget(ctx); err != nil {
		log.Fatalf("Migration target admission failed: %v", err)
	}

	if *prepareWorkItem || *retireWorkItem {
		if *dryRun {
			var inventory any
			if *prepareWorkItem {
				inventory, err = migrator.InspectPreparation(ctx)
			} else {
				inventory, err = migrator.InspectRetirement(ctx)
			}
			if err != nil {
				log.Fatalf("Controlled inventory rejected: %v", err)
			}
			if err = json.NewEncoder(os.Stdout).Encode(inventory); err != nil {
				log.Fatal("cannot encode inventory")
			}
			return
		}
		if *prepareWorkItem {
			err = migrator.ApplyPreparation(ctx, evidence)
		} else {
			err = migrator.ApplyRetirement(ctx, evidence)
		}
		if err != nil {
			log.Fatalf("Controlled migration rejected: %v", err)
		}
		fmt.Println("Controlled migration committed")
		return
	}
	// Get available migrations
	available := getAvailableMigrations()

	if *seed {
		seedData(sugar)
		return
	}

	if *seedOnly {
		ctx := context.Background()
		// First ensure migrations table
		if err := migrator.EnsureMigrationsTable(ctx); err != nil {
			log.Fatalf("Failed to ensure migrations table: %v", err)
		}
		count, err := migrator.RunMigrations(ctx, available)
		if err != nil {
			log.Fatalf("Migration failed: %v", err)
		}
		fmt.Printf("Applied %d migration(s)\n", count)
		seedData(sugar)
		return
	}

	if *dryRun {
		ctx := context.Background()
		fmt.Println("=== Dry Run Mode - No changes will be made ===")
		fmt.Println()
		plan, err := migrator.Plan(ctx, migration.OpUp, nil)
		if err != nil {
			log.Fatalf("Dry run rejected: %v", err)
		}
		for _, manual := range plan.PendingManual {
			fmt.Printf("[%s] pending_manual\n", manual.Version)
		}
		for _, mig := range plan.Executable {
			sql, err := migrator.DryRun(ctx, mig)
			if err != nil {
				log.Fatalf("Dry run failed for %s: %v", mig.Version, err)
			}
			fmt.Printf("[%s] %s\n%s\n\n", mig.Version, mig.Description, sql)
		}
		return
	}

	if *status {
		showStatus(migrator, available)
		return
	}

	if *version {
		showVersion(migrator, getAvailableMigrations())
		return
	}

	if *reset {
		resetMigrations(migrator, getAvailableMigrations())
		return
	}

	if *list {
		listMigrations(getAvailableMigrations())
		return
	}

	if *up {
		runMigrations(migrator, available)
		return
	}

	if *down {
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
	return migration.PostSchemaMigrations()
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
	fmt.Println("=== Executable Migrations ===")
	if len(pending) == 0 {
		fmt.Println("  No pending migrations")
	} else {
		for _, m := range pending {
			fmt.Printf("  [%s] %s\n", m.Version, m.Description)
		}
	}
	plan, err := migrator.Plan(ctx, migration.OpUp, nil)
	if err != nil {
		log.Fatalf("Plan rejected: %v", err)
	}
	for _, manual := range plan.PendingManual {
		fmt.Printf("  [%s] pending_manual\n", manual.Version)
	}
}

func runMigrations(migrator *migration.Migrator, available []migration.Migration) {
	ctx := context.Background()
	if err := migrator.EnsureMigrationsTable(ctx); err != nil {
		log.Fatalf("Migration ledger admission failed: %v", err)
	}
	count, err := migrator.RunMigrations(ctx, available)
	if err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	fmt.Printf("Applied %d migration(s)\n", count)
	showStatus(migrator, available)
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

	// Stage dependency order is authoritative; P has a higher number than later ordinary SQL.
	applied = dependencyOrderedApplied(applied)
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

	applied = dependencyOrderedApplied(applied)
	targetIndex := -1
	for i, m := range applied {
		if m.Version == targetVersion {
			targetIndex = i
		}
	}
	if targetIndex < 0 {
		log.Fatalf("Rollback target is not applied in the active dependency order: %s", targetVersion)
	}
	var toRollback []migration.Migration
	for i := len(applied) - 1; i > targetIndex; i-- {
		toRollback = append(toRollback, applied[i])
	}

	if len(toRollback) == 0 {
		fmt.Printf("No migrations to rollback (already at version %s)\n", targetVersion)
		return
	}

	versions := make([]string, 0, len(toRollback))
	for _, m := range toRollback {
		versions = append(versions, m.Version)
	}
	if err := migrator.ReverseMigrations(ctx, migration.OpDown, versions); err != nil {
		log.Fatalf("Rollback plan rejected: %v", err)
	}
	fmt.Printf("Rolled back %d migration(s)\n", len(versions))
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

	db, err := database.InitDB(&cfg.Database)
	if err != nil {
		log.Fatalf("Seed target connection failed: %v", err)
	}
	defer db.Close()
	control, err := migration.LoadControlConfiguration()
	if err != nil {
		log.Fatalf("Migration control configuration rejected: %v", err)
	}
	migrator := migration.NewMigrator(db, sugar, control)
	if err := migrator.WithMigrationLock(context.Background(), func(ctx context.Context) error {
		if err := migrator.InspectRuntimeMigrations(ctx); err != nil {
			return err
		}
		return seeder.NewSeeder(client, sugar, cfg).SeedAll(ctx)
	}); err != nil {
		log.Fatalf("Seed admission or execution failed: %v", err)
	}
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

	// Inspect existence through postgres; creation is allowed only for a missing target.
	postgresDSN := fmt.Sprintf("host=%s port=%d user=%s dbname=postgres sslmode=%s password=%s",
		cfg.Database.Host, cfg.Database.Port, cfg.Database.User, cfg.Database.SSLMode, cfg.Database.Password)

	postgresDB, err := sql.Open("postgres", postgresDSN)
	if err != nil {
		log.Fatalf("Failed to connect to postgres: %v", err)
	}
	defer postgresDB.Close()

	var exists bool
	if err := postgresDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, cfg.Database.DBName).Scan(&exists); err != nil {
		log.Fatalf("Fresh target inspection failed: %v", err)
	}
	// Never DROP an existing target: that bypasses reverse dependencies and
	// recovery evidence. A genuinely empty existing target needs no recreation.
	if !exists {
		if _, err := postgresDB.Exec("CREATE DATABASE " + pq.QuoteIdentifier(cfg.Database.DBName)); err != nil {
			log.Fatalf("Failed to create fresh database: %v", err)
		}
	}

	postgresDB.Close()

	// Reconnect to new database
	db, err := database.InitDB(&cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to new database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	client, err := database.InitDatabase(&cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect for canonical bootstrap: %v", err)
	}
	defer client.Close()
	control, err := migration.LoadControlConfiguration()
	if err != nil {
		log.Fatalf("Migration control configuration rejected: %v", err)
	}
	migrator := migration.NewMigrator(db, sugar, control)
	if err := migrator.WithMigrationLock(ctx, func(ctx context.Context) error {
		if err := migrator.InspectEmptyMigrationTarget(ctx); err != nil {
			return err
		}
		return migration.RunCanonicalBootstrap(ctx, migration.CanonicalBootstrap{
			Prepare: func(ctx context.Context) error {
				return database.PrepareBootstrapInfrastructure(ctx, db)
			},
			CreateSchema: func(ctx context.Context) error { return client.Schema.Create(ctx) },
			Migrator:     migrator,
			Seed: func(ctx context.Context) error {
				return seeder.NewSeeder(client, sugar, cfg).SeedProduction(ctx)
			},
		})
	}); err != nil {
		log.Fatalf("Canonical fresh bootstrap failed: %v", err)
	}

	fmt.Println("Fresh reset completed successfully")
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
	fmt.Printf("Highest recorded version (not dependency readiness): %s\n", latest.Version)
	showStatus(migrator, available)
	fmt.Printf("Description: %s\n", latest.Description)
	fmt.Printf("Applied at: %s\n", latest.AppliedAt.Format("2006-01-02 15:04:05"))
}

func resetMigrations(migrator *migration.Migrator, available []migration.Migration) {
	ctx := context.Background()
	if err := migrator.ReverseMigrations(ctx, migration.OpReset, nil); err != nil {
		log.Fatalf("Reset plan rejected: %v", err)
	}
	fmt.Println("Reset completed successfully")
}

func dependencyOrderedApplied(applied []migration.Migration) []migration.Migration {
	byVersion := map[string]migration.Migration{}
	controlled := false
	for _, m := range applied {
		byVersion[m.Version] = m
		controlled = controlled || m.Version == migration.WorkItemPrepareVersion
	}
	if !controlled {
		ordered := append([]migration.Migration(nil), applied...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].Version < ordered[j].Version })
		return ordered
	}
	var ordered []migration.Migration
	for _, d := range migration.ControlledMigrationCatalog() {
		if m, ok := byVersion[d.Migration.Version]; ok {
			ordered = append(ordered, m)
		}
	}
	return ordered
}
