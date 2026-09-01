# WorkItem NumberAllocator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Replace every tickets.ticket_number generator with one PostgreSQL-authoritative, tenant/month atomic NumberAllocator and make ticket numbers unique only inside their tenant.

**Architecture:** Add one tenant-scoped work_item_number_sequences Ent model and one repository/workitemnumber allocator. Allocation establishes the (tenant_id, period) row with ON CONFLICT DO NOTHING, then atomically increments last_value and uses the returned entity; callers that own an Ent transaction pass tx.Client(), so the counter and WorkItem write roll back together. Remove Redis ticket keys, Ticket-table max scans, timestamp generators, collision retries, and dead parallel ticket services.

**Tech Stack:** Go 1.25.12, Ent 0.14, PostgreSQL/lib/pq, SQLite, testify, registered Go migration catalog and versioned SQL scripts.

**Spec:** docs/superpowers/specs/2026-09-01-architecture-hardening-agent-platform-evolution-design.md

## Global Constraints

- Application baseline: f11290317499b958ba93d85689286fdccccfe697.
- tickets remains the WorkItem base table.
- Human numbers are TKT-YYYYMM-NNNNNN. YYYYMM comes from issuedAt.UTC(); each (tenant_id, YYYYMM) has an independent sequence.
- ticket_number is immutable and unique by (tenant_id, ticket_number). WorkItem ID/UUID remains the global identity.
- PostgreSQL is the only number authority. Redis availability must not affect WorkItem allocation; no fallback scans tickets.
- A transaction-owning caller passes tx.Client(); never open an independent counter transaction.
- SequenceService remains only for professional identifiers such as incident_number.
- This development environment has no production history. Do not add backfill compatibility, dual reads/writes, deprecated aliases, runtime fallback, or compatibility constructors.
- Retain versioned apply, empty-development reset and verification SQL.
- Do not touch docs/superpowers/specs/2026-09-01-architecture-hardening-agent-platform-evolution-design-review.md.
- `itsm-backend/service/ticket_service.go` is a deliberate merge-touch with P1-C: P1-A owns only allocator construction in `NewTicketServiceForTest`; P1-C owns only the BPMN `CancelProcess` call near the workflow methods. Implement in isolated worktrees and let the Wave 1A integration owner merge both edits; neither plan may rewrite the other's block.

## Ownership Contract

- owns: Ticket number schema/index, sequence schema, repository/workitemnumber, migration 020, every WorkItem-number caller, and apply/reset/verification scripts.
- depends_on: approved overall spec and current Ent generation.
- deletes: Redis sequence:ticket authority, max-ticket scans, public GenerateTicketNumber, collision retry loops, timestamp Feishu numbers, dead NumberGenerator, dead TicketCoreService and unused handlers/ticket aggregate.
- evidence: schema/migration tests, PostgreSQL concurrency/rollback tests, domain creation regressions, zero-match searches, scripts, full tests/build and diff checks.

## Frozen Interface

~~~go
package workitemnumber

type Allocator interface {
	Allocate(ctx context.Context, client *ent.Client, tenantID int, issuedAt time.Time) (string, error)
}

type PostgreSQLAllocator struct{}

func NewPostgreSQLAllocator() *PostgreSQLAllocator
func (a *PostgreSQLAllocator) Allocate(
	ctx context.Context,
	client *ent.Client,
	tenantID int,
	issuedAt time.Time,
) (string, error)
~~~

issuedAt is explicit so runtime code uses the WorkItem creation timestamp and tests are deterministic. The object is stateless; PostgreSQL owns correctness.

---

### Task 1: Tenant-scoped schema and versioned scripts

**Files:**
- Create: itsm-backend/ent/schema/work_item_number_sequence.go
- Create: itsm-backend/ent/schema/work_item_number_sequence_test.go
- Create: itsm-backend/migrations/20260901_work_item_number_allocator.sql
- Create: itsm-backend/migrations/20260901_work_item_number_allocator_dev_reset.sql
- Create: itsm-backend/migrations/20260901_work_item_number_allocator_verify.sql
- Modify: itsm-backend/ent/schema/ticket.go:37-42,184-198
- Modify: itsm-backend/ent/generate.go:3
- Modify: itsm-backend/migration/migrations.go:41-127 and GetMigrationSQL
- Modify: itsm-backend/migration/migrator_test.go
- Modify: itsm-backend/internal/bootstrap/post_schema_migrations_test.go:30-51
- Generated: itsm-backend/ent/client.go, config.go, context.go, ent.go, mutation.go, tx.go, runtime/runtime.go, hook/hook.go, predicate/predicate.go, migrate/schema.go, workitemnumbersequence.go, workitemnumbersequence_create.go, workitemnumbersequence_delete.go, workitemnumbersequence_query.go, workitemnumbersequence_update.go and workitemnumbersequence/*.go

**Interfaces:**
- Consumes: Ent schema and migration registry conventions.
- Produces: WorkItemNumberSequence and 020_work_item_number_allocator.

- [ ] **Step 1: Write failing descriptor tests**

~~~go
func TestTicketNumberIsImmutableAndTenantScopedUnique(t *testing.T) {
	var immutable, fieldUnique, composite bool
	for _, f := range (Ticket{}).Fields() {
		if f.Descriptor().Name == "ticket_number" {
			immutable = f.Descriptor().Immutable
			fieldUnique = f.Descriptor().Unique
		}
	}
	for _, idx := range (Ticket{}).Indexes() {
		d := idx.Descriptor()
		if reflect.DeepEqual(d.Fields, []string{"tenant_id", "ticket_number"}) {
			composite = d.Unique
		}
		require.False(t, d.Unique && reflect.DeepEqual(d.Fields, []string{"ticket_number"}))
	}
	require.True(t, immutable)
	require.False(t, fieldUnique)
	require.True(t, composite)
}

func TestWorkItemNumberSequenceUsesOneTenantPeriodRow(t *testing.T) {
	var unique bool
	for _, idx := range (WorkItemNumberSequence{}).Indexes() {
		d := idx.Descriptor()
		unique = unique || d.Unique &&
			reflect.DeepEqual(d.Fields, []string{"tenant_id", "period"})
	}
	require.True(t, unique)
}
~~~

- [ ] **Step 2: Verify they fail**

~~~bash
cd itsm-backend
go test ./ent/schema -run 'TestTicketNumberIsImmutableAndTenantScopedUnique|TestWorkItemNumberSequenceUsesOneTenantPeriodRow' -count=1
~~~

Expected: FAIL because Ticket is globally unique/mutable and the sequence schema is absent.

- [ ] **Step 3: Define the schema**

~~~go
type WorkItemNumberSequence struct{ ent.Schema }

func (WorkItemNumberSequence) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Immutable().Positive(),
		field.String("period").Immutable().MinLen(6).MaxLen(6),
		field.Int64("last_value").Default(0).NonNegative(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (WorkItemNumberSequence) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "period").Unique()}
}
~~~

In Ticket, replace Unique().NotEmpty() with Immutable().NotEmpty(), and replace the single-field unique index with index.Fields("tenant_id", "ticket_number").Unique().

Change ent/generate.go to:

~~~go
//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert ./schema
~~~

ON CONFLICT is mandatory: a caught unique violation aborts PostgreSQL transactions and cannot be retried inside the same transaction.

- [ ] **Step 4: Register migration 020 and write the apply script**

RegisteredMigrations metadata:

~~~go
{
	Version:     "020_work_item_number_allocator",
	Description: "Create tenant/month WorkItem number sequences and replace global ticket_number uniqueness with tenant-scoped uniqueness",
	RollbackSQL: workItemNumberAllocatorEmptyDevelopmentRollbackSQL,
},
~~~

Define the rollback constant with an empty-tickets guard, drop sequence table/composite index, and recreate ticket_ticket_number. GetMigrationSQL and the apply file contain:

~~~sql
CREATE TABLE IF NOT EXISTS work_item_number_sequences (
    id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    period VARCHAR(6) NOT NULL,
    last_value BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT work_item_number_sequences_period_check
        CHECK (period ~ '^[0-9]{6}$'),
    CONSTRAINT work_item_number_sequences_last_value_check
        CHECK (last_value BETWEEN 0 AND 999999)
);
CREATE UNIQUE INDEX IF NOT EXISTS workitemnumbersequence_tenant_id_period
    ON work_item_number_sequences (tenant_id, period);
DROP INDEX IF EXISTS ticket_ticket_number;
ALTER TABLE tickets DROP CONSTRAINT IF EXISTS ticket_ticket_number;
ALTER TABLE tickets DROP CONSTRAINT IF EXISTS tickets_ticket_number_key;
CREATE UNIQUE INDEX IF NOT EXISTS ticket_tenant_id_ticket_number
    ON tickets (tenant_id, ticket_number);
~~~

No data backfill is added.

The rollback constant wraps this exact development-only SQL (the guard prevents silently reintroducing global uniqueness over populated tenant data):

~~~sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM tickets LIMIT 1) THEN
        RAISE EXCEPTION
            'rollback requires an empty tickets table; use only after development reset';
    END IF;
END $$;
DROP INDEX IF EXISTS ticket_tenant_id_ticket_number;
CREATE UNIQUE INDEX IF NOT EXISTS ticket_ticket_number
    ON tickets (ticket_number);
DROP TABLE IF EXISTS work_item_number_sequences;
~~~

- [ ] **Step 5: Write reset and verification SQL**

Reset:

~~~sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM tickets LIMIT 1) THEN
        RAISE EXCEPTION
            'reset requires an empty tickets table; run development reset first';
    END IF;
END $$;
TRUNCATE TABLE work_item_number_sequences RESTART IDENTITY;
~~~

Verification raises if the sequence table, composite unique index or checks are missing; if duplicate (tenant_id, ticket_number) exists; or if period/value is invalid. It must exit non-zero on invalid state.

- [ ] **Step 6: Generate and inspect**

~~~bash
cd itsm-backend
go generate ./ent
gofmt -w ent/schema/ticket.go ent/schema/work_item_number_sequence.go ent/schema/work_item_number_sequence_test.go
git diff -- ent/schema ent/migrate/schema.go ent/workitemnumbersequence.go ent/workitemnumbersequence ent/client.go ent/tx.go ent/mutation.go
~~~

Expected: the new entity appears in Client/Tx; create builders have OnConflict; Ticket update builders lose SetTicketNumber.

- [ ] **Step 7: Update registry tests and run**

Assert version 020, table/index/check SQL, rollback guard, PostSchemaMigrations length 14 and version 020 at index 13.

~~~bash
cd itsm-backend
go test ./ent/schema ./migration ./internal/bootstrap -run 'TicketNumber|WorkItemNumber|Migration|PostSchema' -count=1
~~~

Expected: PASS.

- [ ] **Step 8: Commit**

~~~bash
git add itsm-backend/ent itsm-backend/migration itsm-backend/migrations/20260901_work_item_number_allocator*.sql itsm-backend/internal/bootstrap/post_schema_migrations_test.go
git commit -m "feat(workitem): add tenant scoped number sequence schema"
~~~

---

### Task 2: Atomic NumberAllocator

**Files:**
- Create: itsm-backend/repository/workitemnumber/allocator.go
- Create: itsm-backend/repository/workitemnumber/allocator_test.go
- Create: itsm-backend/repository/workitemnumber/allocator_integration_test.go

**Interfaces:**
- Consumes: Task 1 builders.
- Produces: frozen Allocator and NewPostgreSQLAllocator.

- [ ] **Step 1: Write failing unit tests**

Cover sequential allocation, independent tenants, UTC month boundary, nil client, non-positive tenant, zero time and transaction rollback.

~~~go
issuedAt := time.Date(2026, 9, 1, 0, 30, 0, 0,
	time.FixedZone("UTC+8", 8*60*60))
n1, err := allocator.Allocate(ctx, client, tenantA.ID, issuedAt)
require.NoError(t, err)
require.Equal(t, "TKT-202608-000001", n1)
n2, err := allocator.Allocate(ctx, client, tenantA.ID, issuedAt)
require.NoError(t, err)
require.Equal(t, "TKT-202608-000002", n2)
n3, err := allocator.Allocate(ctx, client, tenantB.ID, issuedAt)
require.NoError(t, err)
require.Equal(t, "TKT-202608-000001", n3)
~~~

For rollback, allocate through tx.Client(), roll back, allocate in a new transaction and expect 000001 again.

- [ ] **Step 2: Verify failure**

~~~bash
cd itsm-backend
go test ./repository/workitemnumber -count=1
~~~

Expected: FAIL because the package is absent.

- [ ] **Step 3: Implement without fallback**

~~~go
func (a *PostgreSQLAllocator) Allocate(
	ctx context.Context, client *ent.Client, tenantID int, issuedAt time.Time,
) (string, error) {
	if client == nil {
		return "", errors.New("work item number allocator requires an Ent client")
	}
	if tenantID <= 0 {
		return "", errors.New("work item number allocator requires a positive tenant id")
	}
	if issuedAt.IsZero() {
		return "", errors.New("work item number allocator requires issuedAt")
	}
	issuedAt = issuedAt.UTC()
	period := issuedAt.Format("200601")
	err := client.WorkItemNumberSequence.Create().
		SetTenantID(tenantID).SetPeriod(period).SetLastValue(0).
		OnConflictColumns(
			workitemnumbersequence.FieldTenantID,
			workitemnumbersequence.FieldPeriod,
		).Ignore().Exec(ctx)
	if err != nil {
		return "", fmt.Errorf("ensure work item number sequence: %w", err)
	}
	row, err := client.WorkItemNumberSequence.Query().Where(
		workitemnumbersequence.TenantID(tenantID),
		workitemnumbersequence.Period(period),
	).Only(ctx)
	if err != nil {
		return "", fmt.Errorf("load work item number sequence: %w", err)
	}
	row, err = client.WorkItemNumberSequence.UpdateOneID(row.ID).
		AddLastValue(1).Save(ctx)
	if err != nil {
		return "", fmt.Errorf("increment work item number sequence: %w", err)
	}
	return fmt.Sprintf("TKT-%s-%06d", period, row.LastValue), nil
}
~~~

The database check is the terminal exhaustion guard. Do not catch it to invent another number.

- [ ] **Step 4: Run fast tests**

~~~bash
cd itsm-backend
gofmt -w repository/workitemnumber
go test ./repository/workitemnumber -run TestPostgreSQLAllocator -count=1
~~~

Expected: PASS.

- [ ] **Step 5: Add PostgreSQL concurrency tests**

Use //go:build integration and require ITSM_TEST_DB with require.NotEmpty. Create a UUID schema and connect with search_path in the DSN. Launch 128 Ent transactions, 64 per tenant, for one month. Each calls Allocate with tx.Client() and commits.

Assert each tenant receives exactly 000001..000064 and each counter row ends at 64. Add a test that allocates and creates a Ticket in one transaction, rolls back, then proves both Ticket and increment disappeared.

- [ ] **Step 6: Run race tests**

~~~bash
cd itsm-backend
test -n "$ITSM_TEST_DB"
go test -tags=integration -race ./repository/workitemnumber -run 'TestPostgreSQLAllocator_ConcurrentTenantMonthlyAllocation|TestPostgreSQLAllocator_RollsBackWithWorkItem' -count=1
~~~

Expected: PASS, zero skips.

- [ ] **Step 7: Commit**

~~~bash
git add itsm-backend/repository/workitemnumber
git commit -m "feat(workitem): add atomic postgres number allocator"
~~~

---

### Task 3: Ticket and Requested Item integration

**Files:**
- Modify: itsm-backend/repository/ticket/repository.go:10-39
- Modify: itsm-backend/repository/ticket/repository_impl.go:1-140,513-651
- Modify: itsm-backend/repository/ticket/repository_test.go
- Modify: itsm-backend/service/ticket_service.go:90-105
- Modify: itsm-backend/service/ticket_service_test.go
- Modify: itsm-backend/handlers/service_request/service.go:15-55,220-270
- Modify: itsm-backend/handlers/service_request/handler_test.go
- Modify: itsm-backend/handlers/service_request/kaf_delegation_sslvpn_e2e_test.go
- Modify: itsm-backend/handlers/service_request/regression_test.go
- Modify: itsm-backend/handlers/service_request/service_test.go
- Modify: itsm-backend/internal/bootstrap/app.go

**Interfaces:**
- Consumes: Allocator.Allocate.
- Produces: `ticket.NewEntRepository(client, logger, allocator)`, `service_request.NewService(repo, scRepo, cmdbRepo, entClient, allocator, logger, ticketSvc, chainResolver, incidentSvc)`, and `Repository` without `GenerateTicketNumber`.

The Service Request constructor becomes:

~~~go
func NewService(
	repo Repository,
	scRepo service_catalog.Repository,
	cmdbRepo cmdb.Repository,
	entClient *ent.Client,
	allocator workitemnumber.Allocator,
	logger *zap.SugaredLogger,
	ticketSvc TicketServiceInterface,
	chainResolver *service.ApprovalChainResolver,
	incidentSvc IncidentCreator,
) *Service
~~~

- [ ] **Step 1: Replace generator tests**

Delete stub SequenceProvider, typed-nil fallback, collision-retry and public GenerateTicketNumber tests. Add first-Ticket-per-tenant equality and same-tenant duplicate rejection. Add a Requested Item test observing one committed counter row, Ticket and extension.

- [ ] **Step 2: Verify failure**

~~~bash
cd itsm-backend
go test ./repository/ticket ./handlers/service_request -run 'Number|Create' -count=1
~~~

Expected: FAIL.

- [ ] **Step 3: Require allocator in Ticket repository**

~~~go
type EntRepository struct {
	*base.EntRepository
	logger          *zap.SugaredLogger
	numberAllocator workitemnumber.Allocator
}
func NewEntRepository(
	client *ent.Client, logger *zap.SugaredLogger,
	allocator workitemnumber.Allocator,
) *EntRepository {
	if allocator == nil {
		panic("work item number allocator is required")
	}
	return &EntRepository{
		EntRepository: base.NewEntRepository(client),
		logger: logger, numberAllocator: allocator,
	}
}
~~~

Create captures issuedAt := time.Now().UTC(), allocates once on r.Client(), uses issuedAt for Ticket timestamps and saves once.

Delete SequenceProvider, adapter, SetSequenceService, reflection handling, rawDB, SetRawDB, creation retry, GenerateTicketNumber, Redis/DB fallback helpers and _uniqueFallbackSuffix. Remove GenerateTicketNumber from Repository.

- [ ] **Step 4: Preserve Requested Item transactionality**

Add allocator to service_request.Service and NewService. Construct:

~~~go
workItemRepo := ticket.NewEntRepository(
	tx.Client(), s.logger, s.numberAllocator,
)
~~~

Create the single application allocator in `internal/bootstrap/app.go` in this task and pass that same instance to the Ticket repository and Service Request service. Tasks 4 and 5 extend this existing wiring to the remaining domains; they must not instantiate parallel allocators.

- [ ] **Step 5: Update fixtures and run**

All fixtures explicitly pass NewPostgreSQLAllocator; no variadic/default constructor.

~~~bash
cd itsm-backend
gofmt -w repository/ticket service/ticket_service.go handlers/service_request
go test ./repository/ticket ./handlers/service_request ./service -run 'Ticket.*Create|ServiceRequest.*Create' -count=1
~~~

Expected: PASS.

- [ ] **Step 6: Commit**

~~~bash
git add itsm-backend/repository/ticket itsm-backend/service/ticket_service.go itsm-backend/service/ticket_service_test.go itsm-backend/handlers/service_request itsm-backend/internal/bootstrap/app.go
git commit -m "refactor(workitem): route ticket creation through number allocator"
~~~

---

### Task 4: Incident, Problem and Change integration

**Files:**
- Modify: itsm-backend/service/incident_service.go:20-55,80-185,1084-1150
- Modify: itsm-backend/service/incident_service_test.go
- Modify: itsm-backend/service/incident_rule_engine.go
- Modify: itsm-backend/controller/incident_controller_test.go
- Modify: itsm-backend/router/router_test.go
- Modify: itsm-backend/service/bpmn_platform_tenant_test.go
- Modify: itsm-backend/service/bpmn_task_service_engine_scope_test.go
- Modify: itsm-backend/handlers/service_request/kaf_delegation_sslvpn_e2e_test.go
- Modify: itsm-backend/handlers/problem/repository_impl.go:1-45,300-415
- Modify: itsm-backend/handlers/problem/conversion_test.go
- Modify: itsm-backend/handlers/problem/handler_test.go
- Modify: itsm-backend/handlers/problem/investigation_test.go
- Modify: itsm-backend/handlers/problem/known_error_test.go
- Modify: itsm-backend/handlers/problem/service_test.go
- Modify: itsm-backend/handlers/change/repository_impl.go:1-40,300-435
- Modify: itsm-backend/handlers/change/change_bpmn_e2e_test.go
- Modify: itsm-backend/handlers/change/change_regression_test.go
- Modify: itsm-backend/handlers/change/service_bpmn_test.go
- Modify: itsm-backend/handlers/change/service_test.go
- Modify: itsm-backend/internal/bootstrap/app.go
- Modify: itsm-backend/internal/container/container.go

**Interfaces:**
- Produces: NewIncidentService(client, logger, allocator), problem.NewEntRepository(client, allocator), change.NewEntRepository(client, rawDB, allocator).

- [ ] **Step 1: Write new contracts**

For each domain create two records in one tenant and expect suffixes 000001/000002. Rewrite the Problem cross-tenant test to expect equal human numbers while both tenant rows persist. Delete fake Redis WorkItem-number tests; keep incident_number Redis tests.

- [ ] **Step 2: Verify failure**

~~~bash
cd itsm-backend
go test ./service ./handlers/problem ./handlers/change -run 'Incident.*WorkItem|Problem.*WorkItem|Change.*WorkItem|CrossTenantSameMonth' -count=1
~~~

Expected: FAIL.

- [ ] **Step 3: Integrate Incident inside its transaction**

Require allocator in NewIncidentService. Keep SequenceService only for incident_number. After tx starts, allocate on tx.Client() with issuedAt UTC and use the same timestamp on Ticket. Delete generateWorkItemTicketNumber only.

- [ ] **Step 4: Integrate Problem**

Replace SequenceProvider/sequenceService with Allocator. Delete SetSequenceService, Redis Ticket key and DB scan. Allocate after transaction start.

- [ ] **Step 5: Integrate Change**

Add allocator to its constructor. Keep rawDB because other Change operations use it. Delete generateWorkItemTicketNumber and allocate on tx.Client().

- [ ] **Step 6: Run**

~~~bash
cd itsm-backend
gofmt -w service/incident_service.go handlers/problem handlers/change
go test ./service ./handlers/problem ./handlers/change -run 'Incident|Problem|Change' -count=1
~~~

Expected: PASS.

- [ ] **Step 7: Commit**

~~~bash
git add itsm-backend/service/incident_service.go itsm-backend/service/incident_service_test.go itsm-backend/service/incident_rule_engine.go itsm-backend/controller/incident_controller_test.go itsm-backend/router/router_test.go itsm-backend/service/bpmn_platform_tenant_test.go itsm-backend/service/bpmn_task_service_engine_scope_test.go itsm-backend/handlers/service_request/kaf_delegation_sslvpn_e2e_test.go itsm-backend/handlers/problem/repository_impl.go itsm-backend/handlers/problem/conversion_test.go itsm-backend/handlers/problem/handler_test.go itsm-backend/handlers/problem/investigation_test.go itsm-backend/handlers/problem/known_error_test.go itsm-backend/handlers/problem/service_test.go itsm-backend/handlers/change/repository_impl.go itsm-backend/handlers/change/change_bpmn_e2e_test.go itsm-backend/handlers/change/change_regression_test.go itsm-backend/handlers/change/service_bpmn_test.go itsm-backend/handlers/change/service_test.go itsm-backend/internal/bootstrap/app.go itsm-backend/internal/container/container.go
git commit -m "refactor(workitem): unify professional domain numbering"
~~~

---

### Task 5: Remaining generators, bootstrap and obsolete paths

**Files:**
- Modify: itsm-backend/service/feishu_sync_service.go:15-30,200-235
- Create: itsm-backend/service/feishu_sync_service_number_test.go
- Modify: itsm-backend/internal/bootstrap/app.go:260-360,535-650
- Modify: itsm-backend/internal/bootstrap/email_msgraph_wiring_test.go
- Modify: itsm-backend/internal/container/container.go:1-110,150-235
- Modify: itsm-backend/service/README-sequence-service.md
- Delete: itsm-backend/service/number_generator.go
- Delete: itsm-backend/service/ticket_core_service.go and ticket_core_service_test.go
- Modify: itsm-backend/service/ticket_service_table_test.go; remove TicketCore suites, retain TicketLifecycle suites
- Delete: itsm-backend/handlers/ticket/aggregate.go
- Modify: itsm-backend/connector/builtin/feishu/connector.go:337-455; delete unused SyncFeishuTaskToTicket
- Delete: itsm-backend/cmd/backfill_incident_work_item/main.go and main_test.go
- Delete: itsm-backend/cmd/backfill_problem_work_item/main.go and main_test.go
- Delete: itsm-backend/cmd/backfill_change_work_item/main.go and main_test.go
- Modify: itsm-backend/cmd/backfill_incident_comments/main.go and main_test.go; remove the deleted Incident backfill prerequisite and treat a missing WorkItem as an invalid development record
- Modify: itsm-backend/handlers/change/service_bpmn_test.go; update the obsolete backfill prerequisite assertion/comment
- Modify: itsm-backend/service/incident_service.go
- Modify: itsm-backend/handlers/problem/entity.go
- Modify: itsm-backend/handlers/problem/repository_impl.go
- Modify: itsm-backend/handlers/change/entity.go
- Modify: itsm-backend/handlers/change/repository_impl.go
- Modify: itsm-backend/handlers/change/service.go
- Modify: itsm-backend/dto/change_dto.go
- Modify: itsm-backend/dto/problem_dto.go
- Modify: itsm-frontend/src/lib/api/incident-api.ts
- Modify: itsm-frontend/src/lib/api/problem-api.ts
- Modify: itsm-frontend/src/lib/api/change-api.ts
- Modify: itsm-frontend/src/app/(main)/incidents/[id]/page.tsx
- Modify: itsm-frontend/src/app/(main)/problems/[id]/page.tsx
- Modify: itsm-frontend/src/app/(main)/changes/[id]/page.tsx

**Interfaces:**
- Produces: one bootstrap-owned allocator and zero legacy WorkItem generators/history bridges.

- [ ] **Step 1: Add failing Feishu test**

Construct FeishuSyncService with allocator, sync two unmapped tasks and assert canonical sequence plus atomic FeishuTicketSync mapping.

~~~bash
cd itsm-backend
go test ./service -run TestFeishuSyncService_UsesWorkItemNumberAllocator -count=1
~~~

Expected: FAIL on TKT-FS timestamp generation.

- [ ] **Step 2: Integrate FeishuSyncService**

Change constructor to NewFeishuSyncService(client, logger, allocator). Allocate against existing tx.Client() with one issuedAt. Delete unused connector SyncFeishuTaskToTicket rather than persisting domain data in an adapter.

- [ ] **Step 3: Wire one bootstrap allocator**

~~~go
incidentService := service.NewIncidentService(client, sugar, numberAllocator)
ticketRepoImpl := repository_ticket.NewEntRepository(client, sugar, numberAllocator)
problemRepo := problem.NewEntRepository(client, numberAllocator)
changeRepo := change.NewEntRepository(client, database.GetRawDB(), numberAllocator)
feishuSyncService := service.NewFeishuSyncService(client, sugar, numberAllocator)
~~~

Reuse the bootstrap-owned `numberAllocator` created in Task 3; do not construct another instance. It is already passed into Ticket and Service Request. Extend the same wiring to Feishu here (Incident, Problem, and Change were added in Task 4). Remove Ticket/Problem SetSequenceService and Ticket SetRawDB; keep Incident sequence injection. Mirror explicit wiring in internal/container and delete queryMaxTicketSeqFromDB.

- [ ] **Step 4: Prove and delete dead implementations**

~~~bash
cd itsm-backend
rg -n 'NewTicketCoreService|TicketDomainService|GenerateTicketNumberGlobal|SyncFeishuTaskToTicket' --glob '*.go'
~~~

Baseline must show TicketCore test-only, TicketDomain confined to aggregate.go, global generator without callers and connector method without callers. Delete them. Retain TicketLifecycle tests from the mixed table file.

- [ ] **Step 5: Delete obsolete WorkItem backfill CLIs**

There is no history. Delete the three commands instead of creating a new migration-time numbering API. Replace “run cmd/backfill_*_work_item” runtime errors with fail-closed WorkItem invariant errors. Do not add a replacement backfill.

Remove the same deleted-command guidance from active frontend API/page source. Historical reports and superseded plans remain historical evidence; do not rewrite them.

- [ ] **Step 6: Narrow SequenceService docs**

Remove sequence:ticket examples/claims. Document only professional identifiers such as INC-YYYYMM-NNNNNN; repository/workitemnumber owns tickets.ticket_number.

- [ ] **Step 7: Test and build**

~~~bash
cd itsm-backend
gofmt -w internal/bootstrap/app.go internal/container/container.go service/feishu_sync_service.go connector/builtin/feishu/connector.go service/ticket_service_table_test.go
go test ./internal/bootstrap ./internal/container ./service ./connector/builtin/feishu -run 'Ticket|Incident|Problem|Change|ServiceRequest|Feishu|Sequence' -count=1
go build ./...
~~~

Expected: PASS.

- [ ] **Step 8: Commit**

~~~bash
git add -A -- itsm-backend/service/feishu_sync_service.go itsm-backend/service/feishu_sync_service_number_test.go itsm-backend/internal/bootstrap/app.go itsm-backend/internal/bootstrap/email_msgraph_wiring_test.go itsm-backend/internal/container/container.go itsm-backend/service/README-sequence-service.md itsm-backend/service/number_generator.go itsm-backend/service/ticket_core_service.go itsm-backend/service/ticket_core_service_test.go itsm-backend/service/ticket_service_table_test.go itsm-backend/handlers/ticket/aggregate.go itsm-backend/connector/builtin/feishu/connector.go itsm-backend/cmd/backfill_incident_work_item itsm-backend/cmd/backfill_problem_work_item itsm-backend/cmd/backfill_change_work_item itsm-backend/cmd/backfill_incident_comments itsm-backend/handlers/change/service_bpmn_test.go itsm-backend/service/incident_service.go itsm-backend/handlers/problem/entity.go itsm-backend/handlers/problem/repository_impl.go itsm-backend/handlers/change/entity.go itsm-backend/handlers/change/repository_impl.go itsm-backend/handlers/change/service.go itsm-backend/dto/change_dto.go itsm-backend/dto/problem_dto.go itsm-frontend/src/lib/api/incident-api.ts itsm-frontend/src/lib/api/problem-api.ts itsm-frontend/src/lib/api/change-api.ts 'itsm-frontend/src/app/(main)/incidents/[id]/page.tsx' 'itsm-frontend/src/app/(main)/problems/[id]/page.tsx' 'itsm-frontend/src/app/(main)/changes/[id]/page.tsx'
git commit -m "refactor(workitem): delete parallel ticket number generators"
~~~

---

### Task 6: P1-A release gate and evidence

**Files:**
- Modify only if commands change: docs/dev-commands-reference.md
- Create: docs/reports/2026-09-01-workitem-number-allocator-verification.md

- [ ] **Step 1: Prove old paths are zero**

~~~bash
cd itsm-backend
test -z "$(rg -n 'sequence:ticket|generateWorkItemTicketNumber|generateBackfillTicketNumber|GenerateTicketNumberGlobal|func .*GenerateTicketNumber|TKT-FS-|TK-%d-%s' --glob '*.go' --glob '*.md' . || true)"
test -z "$(rg -n 'NewTicketCoreService|TicketDomainService' --glob '*.go' . || true)"
test -z "$(rg -n 'backfill_(incident|problem|change)_work_item' service handlers dto ../itsm-frontend/src || true)"
~~~

Expected: both exit zero without exclusions.

- [ ] **Step 2: Audit tenant-scoped lookups**

~~~bash
cd itsm-backend
rg -n 'TicketNumber\(|TicketNumberEQ|TicketNumberIn|GetByNumber\(' --glob '*.go' --glob '!*_test.go' .
~~~

Each Ticket lookup by human number must include tenant ID in the same query/service contract. Add a focused regression for any violation; never restore global uniqueness.

- [ ] **Step 3: Apply and verify on isolated PostgreSQL**

Never use shared 192.168.31.66.

~~~bash
cd itsm-backend
test -n "$ITSM_TEST_DB"
go run -tags migrate ./cmd/migrate -up
psql "$ITSM_TEST_DB" -v ON_ERROR_STOP=1 -f migrations/20260901_work_item_number_allocator_verify.sql
~~~

Expected: migration 020 has a checksum and verification exits zero.

- [ ] **Step 4: Verify reset behavior**

Run reset twice on the empty isolated database; both pass. Insert one valid Ticket fixture and run reset; it must fail with “reset requires an empty tickets table”. Recreate the disposable database afterward.

- [ ] **Step 5: Run full matrix**

~~~bash
cd itsm-backend
go test ./ent/schema ./migration ./repository/workitemnumber ./repository/ticket ./handlers/service_request ./handlers/problem ./handlers/change ./service ./internal/bootstrap ./internal/container -count=1
test -n "$ITSM_TEST_DB"
go test -tags=integration -race ./repository/workitemnumber -count=1
go test ./... -count=1
go build ./...
git diff --check
~~~

Expected: all exit zero and PostgreSQL tests have zero skips.

- [ ] **Step 6: Record evidence**

Report baseline/final commits, migration checksum and script exit codes, test pass/fail/skip counts, concurrent sequence results, old-path searches, and successful Ticket/Problem/Change/Requested Item WorkItem-number allocation with Redis unavailable. For Incident, distinguish the PostgreSQL WorkItem number from the retained professional incident_number Redis-with-DB-fallback path. Confirm only incident_number still uses SequenceService and identify P1-B dependencies without compatibility behavior.

- [ ] **Step 7: Commit evidence**

~~~bash
git add docs/reports/2026-09-01-workitem-number-allocator-verification.md docs/dev-commands-reference.md
git commit -m "docs(workitem): record number allocator verification"
~~~

## Exit Checklist

- [ ] ticket_number is immutable and unique only by tenant and number.
- [ ] One sequence row exists per tenant and UTC month.
- [ ] Ticket, Incident, Problem, Change, Requested Item and Feishu inbound use one allocator.
- [ ] Transaction callers pass tx.Client() and rollback tests prove rollback.
- [ ] Redis ticket keys, max scans, timestamps and collision retries have zero call sites.
- [ ] Dead TicketCore/DDD/global generators and old WorkItem backfills are deleted.
- [ ] Apply/reset/verification scripts execute on isolated PostgreSQL.
- [ ] PostgreSQL concurrency/race tests run with zero skips.
- [ ] Full tests/build and git diff --check pass.
- [ ] Verification report gives P1-B the exact interface and commit.
