package migration

import (
	"context"
	"fmt"
)

// PostSchemaMigrator records and applies the canonical post-schema migration stream.
type PostSchemaMigrator interface {
	EnsureMigrationsTable(context.Context) error
	RunMigrations(context.Context, []Migration) (int, error)
}

// CanonicalBootstrap contains the only supported ordering for a complete
// database bootstrap. Pre-schema preparation is optional; schema creation and
// post-schema migrations are required; seeding is optional.
type CanonicalBootstrap struct {
	Prepare      func(context.Context) error
	CreateSchema func(context.Context) error
	Migrator     PostSchemaMigrator
	Seed         func(context.Context) error
}

// RunPostSchemaMigrations applies the registered stream only after Ent schema
// creation has completed. It deliberately does not create schema resources.
func RunPostSchemaMigrations(ctx context.Context, migrator PostSchemaMigrator) error {
	if migrator == nil {
		return fmt.Errorf("migration runner is required")
	}
	if err := migrator.EnsureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("ensure migration ledger: %w", err)
	}
	if _, err := migrator.RunMigrations(ctx, PostSchemaMigrations()); err != nil {
		return fmt.Errorf("run post-schema migrations: %w", err)
	}
	return nil
}

// RunCanonicalBootstrap performs preparation, Ent schema creation, registered
// post-schema migrations, and optional seed in their authoritative order.
func RunCanonicalBootstrap(ctx context.Context, bootstrap CanonicalBootstrap) error {
	if bootstrap.CreateSchema == nil {
		return fmt.Errorf("schema creator is required")
	}
	if bootstrap.Migrator == nil {
		return fmt.Errorf("migration runner is required")
	}
	if bootstrap.Prepare != nil {
		if err := bootstrap.Prepare(ctx); err != nil {
			return fmt.Errorf("prepare pre-schema bootstrap: %w", err)
		}
	}
	if err := bootstrap.CreateSchema(ctx); err != nil {
		return fmt.Errorf("create schema resources: %w", err)
	}
	if err := RunPostSchemaMigrations(ctx, bootstrap.Migrator); err != nil {
		return err
	}
	if bootstrap.Seed != nil {
		if err := bootstrap.Seed(ctx); err != nil {
			return fmt.Errorf("seed bootstrap data: %w", err)
		}
	}
	return nil
}
