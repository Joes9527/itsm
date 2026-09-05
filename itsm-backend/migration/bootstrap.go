package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// "ITSMMIGR" encoded as a signed-safe int64. Session advisory locks are
	// scoped by database, so one release operation can run per database.
	bootstrapAdvisoryLockKey int64 = 0x4954534d4d494752
	bootstrapUnlockTimeout         = 5 * time.Second
)

// BootstrapConnection is the connection-pinned SQL boundary used by every
// operation protected by the migration advisory lock. Both *sql.Conn and
// *sql.DB implement it, but PostgresAdvisoryLock always supplies *sql.Conn.
type BootstrapConnection interface {
	DBTX
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// BootstrapLock executes one complete bootstrap while a dedicated database
// connection owns the application advisory lock.
type BootstrapLock interface {
	WithLock(context.Context, func(BootstrapConnection) error) error
}

// PostgresAdvisoryLock owns the database/sql pool used to reserve one pinned
// connection for the lifetime of a fresh install or upgrade.
type PostgresAdvisoryLock struct {
	db *sql.DB
}

// NewPostgresAdvisoryLock constructs the production lock boundary.
func NewPostgresAdvisoryLock(db *sql.DB) (*PostgresAdvisoryLock, error) {
	if db == nil {
		return nil, fmt.Errorf("bootstrap database is required")
	}
	return &PostgresAdvisoryLock{db: db}, nil
}

// WithLock acquires and releases the fixed session advisory lock on the same
// dedicated PostgreSQL connection supplied to every protected phase.
func (lock *PostgresAdvisoryLock) WithLock(ctx context.Context, run func(BootstrapConnection) error) (resultErr error) {
	if lock == nil || lock.db == nil {
		return fmt.Errorf("bootstrap database is required")
	}
	if run == nil {
		return fmt.Errorf("bootstrap lock callback is required")
	}
	conn, err := lock.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve bootstrap connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close bootstrap connection: %w", closeErr))
		}
	}()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, bootstrapAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire bootstrap advisory lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bootstrapUnlockTimeout)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, bootstrapAdvisoryLockKey).Scan(&unlocked); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("release bootstrap advisory lock: %w", err))
			return
		}
		if !unlocked {
			resultErr = errors.Join(resultErr, fmt.Errorf("release bootstrap advisory lock: lock was not owned"))
		}
	}()
	return run(conn)
}

// BootstrapStep is one connection-pinned fresh-install phase.
type BootstrapStep func(context.Context, BootstrapConnection) error

// CurrentSchemaVerifier checks the complete release invariant set.
type CurrentSchemaVerifier func(context.Context, DBTX, ReleaseManifest) error

// SchemaStatePrivilegeApplier provisions the state table on the pinned
// connection before release promotion.
type SchemaStatePrivilegeApplier func(context.Context, BootstrapConnection, SchemaStateRoles) error

// SchemaStatePromoter publishes one verified release.
type SchemaStatePromoter func(context.Context, DBTX, ReleaseManifest) error

// FreshBootstrap defines the only supported fresh-install orchestration.
type FreshBootstrap struct {
	Lock            BootstrapLock
	Prepare         BootstrapStep
	CreateSchema    BootstrapStep
	ApplyBaseline   BootstrapStep
	VerifySchema    CurrentSchemaVerifier
	ApplyPrivileges SchemaStatePrivilegeApplier
	// VerifyProvisionedSchema reruns the complete target verifier after ACL
	// provisioning and immediately before authoritative state promotion.
	VerifyProvisionedSchema CurrentSchemaVerifier
	PromoteState            SchemaStatePromoter
	Seed                    BootstrapStep
	Release                 ReleaseManifest
	Roles                   SchemaStateRoles
}

// UpgradeBootstrap defines the immutable-history upgrade orchestration. The
// planner returns only unapplied migrations after validating all committed
// history. An empty result is therefore the verified restart path.
type UpgradeBootstrap struct {
	Lock                    BootstrapLock
	PlanForwardMigrations   func(context.Context, BootstrapConnection) ([]Migration, error)
	ApplyForwardMigrations  func(context.Context, BootstrapConnection, []Migration) error
	VerifySchema            CurrentSchemaVerifier
	ApplyPrivileges         SchemaStatePrivilegeApplier
	VerifyProvisionedSchema CurrentSchemaVerifier
	PromoteState            SchemaStatePromoter
	Release                 ReleaseManifest
	Roles                   SchemaStateRoles
}

// RunFreshBootstrap creates the current schema directly. It never invokes the
// published upgrade stream and never records synthetic schema_migrations rows.
func RunFreshBootstrap(ctx context.Context, bootstrap FreshBootstrap) error {
	if bootstrap.Lock == nil || bootstrap.Prepare == nil || bootstrap.CreateSchema == nil ||
		bootstrap.ApplyBaseline == nil || bootstrap.VerifySchema == nil ||
		bootstrap.ApplyPrivileges == nil || bootstrap.VerifyProvisionedSchema == nil ||
		bootstrap.PromoteState == nil || bootstrap.Seed == nil {
		return fmt.Errorf("fresh bootstrap dependencies are required")
	}
	if err := ValidateCurrentReleaseArtifact(bootstrap.Release); err != nil {
		return fmt.Errorf("fresh bootstrap release artifact is invalid: %w", err)
	}
	if err := validateSchemaStateRoles(bootstrap.Roles); err != nil {
		return fmt.Errorf("fresh bootstrap schema state roles are invalid: %w", err)
	}
	roleContext, err := WithSchemaStateRoles(ctx, bootstrap.Roles)
	if err != nil {
		return fmt.Errorf("fresh bootstrap schema state roles are invalid: %w", err)
	}

	return bootstrap.Lock.WithLock(roleContext, func(conn BootstrapConnection) error {
		if err := bootstrap.Prepare(roleContext, conn); err != nil {
			return fmt.Errorf("prepare fresh bootstrap: %w", err)
		}
		if err := bootstrap.CreateSchema(roleContext, conn); err != nil {
			return fmt.Errorf("create current Ent schema: %w", err)
		}
		if err := bootstrap.ApplyBaseline(roleContext, conn); err != nil {
			return fmt.Errorf("apply current baseline: %w", err)
		}
		if err := bootstrap.VerifySchema(roleContext, conn, bootstrap.Release); err != nil {
			return fmt.Errorf("verify current schema: %w", err)
		}
		if err := bootstrap.ApplyPrivileges(roleContext, conn, bootstrap.Roles); err != nil {
			return fmt.Errorf("provision schema state privileges: %w", err)
		}
		if err := bootstrap.VerifyProvisionedSchema(roleContext, conn, bootstrap.Release); err != nil {
			return fmt.Errorf("verify provisioned current schema: %w", err)
		}
		if err := bootstrap.PromoteState(roleContext, conn, bootstrap.Release); err != nil {
			return fmt.Errorf("promote schema state: %w", err)
		}
		if err := bootstrap.Seed(roleContext, conn); err != nil {
			return fmt.Errorf("seed fresh bootstrap: %w", err)
		}
		return nil
	})
}

// RunUpgrade validates committed lineage, applies only verified pending
// migrations, rechecks the final schema and privileges, and then promotes the
// release marker. A committed migration is never replayed after interruption.
func RunUpgrade(ctx context.Context, bootstrap UpgradeBootstrap) error {
	if bootstrap.Lock == nil || bootstrap.PlanForwardMigrations == nil ||
		bootstrap.ApplyForwardMigrations == nil || bootstrap.VerifySchema == nil ||
		bootstrap.ApplyPrivileges == nil || bootstrap.VerifyProvisionedSchema == nil ||
		bootstrap.PromoteState == nil {
		return fmt.Errorf("upgrade bootstrap dependencies are required")
	}
	if err := ValidateCurrentReleaseArtifact(bootstrap.Release); err != nil {
		return fmt.Errorf("upgrade release artifact is invalid: %w", err)
	}
	if err := validateSchemaStateRoles(bootstrap.Roles); err != nil {
		return fmt.Errorf("upgrade schema state roles are invalid: %w", err)
	}
	roleContext, err := WithSchemaStateRoles(ctx, bootstrap.Roles)
	if err != nil {
		return fmt.Errorf("upgrade schema state roles are invalid: %w", err)
	}

	return bootstrap.Lock.WithLock(roleContext, func(conn BootstrapConnection) error {
		pending, err := bootstrap.PlanForwardMigrations(roleContext, conn)
		if err != nil {
			return fmt.Errorf("validate migration lineage and history: %w", err)
		}
		if len(pending) > 0 {
			if err := bootstrap.ApplyForwardMigrations(roleContext, conn, append([]Migration(nil), pending...)); err != nil {
				return fmt.Errorf("apply forward migrations: %w", err)
			}
		}
		if err := bootstrap.VerifySchema(roleContext, conn, bootstrap.Release); err != nil {
			return fmt.Errorf("verify current schema: %w", err)
		}
		if err := bootstrap.ApplyPrivileges(roleContext, conn, bootstrap.Roles); err != nil {
			return fmt.Errorf("provision schema state privileges: %w", err)
		}
		if err := bootstrap.VerifyProvisionedSchema(roleContext, conn, bootstrap.Release); err != nil {
			return fmt.Errorf("verify provisioned current schema: %w", err)
		}
		if err := bootstrap.PromoteState(roleContext, conn, bootstrap.Release); err != nil {
			return fmt.Errorf("promote schema state: %w", err)
		}
		return nil
	})
}

// PostSchemaMigrator records and applies the canonical post-schema migration stream.
type PostSchemaMigrator interface {
	EnsureMigrationsTable(context.Context) error
	RunMigrations(context.Context, []Migration) (int, error)
}

// RunPostSchemaMigrations remains the common forward-stream primitive used by
// tests and non-bootstrap migration tooling. Fresh bootstrap never calls it.
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
