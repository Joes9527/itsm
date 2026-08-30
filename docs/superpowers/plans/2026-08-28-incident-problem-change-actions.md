# Incident/Problem/Change Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Incident, Problem, and Change detail actions backend-computed, command-safe, and consumed by their existing professional detail controls.

**Architecture:** Keep domain state machines in their owning backend services. `actions` is a detail-only authorization and eligibility projection; all new or tightened command rules are enforced again by the write path. Incident-to-Problem conversion moves into the Problem domain as one Ent transaction that creates the WorkItem and Problem extension, relation, timeline event, and audit record atomically.

**Tech Stack:** Go, Gin, Ent, PostgreSQL/SQLite-compatible bootstrap preflight, Next.js App Router, TypeScript, Ant Design, Jest/React Testing Library.

**Spec:** `docs/superpowers/specs/2026-08-28-incident-problem-change-actions-design.md`

**Status:** Completed. Final review hardening and verification were completed on 2026-08-30.

## Global Constraints

- `itsm-backend` remains the authority for RBAC, tenant isolation, lifecycle rules, audit, and API contracts; frontend only renders its projection.
- Reuse `dto.ActionPermission` and `WorkItemActionState`; do not create an action-specific response shape.
- Compute `actions` only for single-record detail endpoints, never list endpoints.
- Reuse existing coarse permissions: `incident:write`, `problem:write`, `change:write`, and `change:approve`; do not add permission seed rows.
- A source Incident can have at most one live `investigated_by` relation; relation creation, WorkItem creation, Problem creation, IncidentEvent, and AuditLog must commit or roll back together.
- Ent schema is the sole DDL owner for the new partial index. Run the data-conflict preflight before `client.Schema.Create`; do not add a duplicate versioned migration.
- Do not retain `RootCauseAnalysisService.CreateProblemFromIncident`, `Problem<->Incident` conversion edges, or frontend status-state-machine checks that this feature supersedes.
- Do not implement Incident cancel, Change cancel/rollback, or Problem investigate/root-cause/solution forms.

---

## File Map

- `itsm-backend/service/action_actor.go`: shared action actor input used by Ticket and the three professional domains.
- `itsm-backend/service/ticket_authorization.go`: remove its local duplicate `ActionActor` definition.
- `itsm-backend/ent/schema/work_item_relation.go`: declare the partial uniqueness invariant for live Incident-to-Problem relations.
- `itsm-backend/internal/bootstrap/incident_problem_relation_migration.go`: inspect legacy relation conflicts before schema creation.
- `itsm-backend/internal/bootstrap/app.go`: invoke the preflight before `Schema.Create` and construct Problem conversion dependencies before Incident controller injection.
- `itsm-backend/handlers/problem/{repository.go,repository_impl.go,service.go,handler.go,conversion.go,authorization.go}`: atomic conversion use case, public response mapper, and Problem actions.
- `itsm-backend/service/{incident_service.go,incident_authorization.go}`: Incident detail snapshot/action projection and command-side assignment guard.
- `itsm-backend/controller/incident_controller.go`: inject the narrow conversion service and remove the RCA conversion call.
- `itsm-backend/router/router.go`: align Incident assignment route permission to `incident:write`.
- `itsm-backend/handlers/change/{authorization.go,service.go,handler.go}`: Change actions and command-side self-approval protection.
- `itsm-backend/dto/{incident_dto.go,problem_dto.go,change_dto.go}`: optional detail-only `Actions` response fields.
- `itsm-frontend/src/components/work-item/{WorkItemShell.tsx,WorkItemTypes.ts}`: opt-in generic action bar, defaulting to hidden.
- `itsm-frontend/src/lib/api/{incident-api.ts,problem-api.ts,change-api.ts}`: action response typing.
- `itsm-frontend/src/app/(main)/{incidents,problems,changes}/[id]/page.tsx`: pass API actions into `WorkItemShell`.
- `itsm-frontend/src/components/{incident/IncidentDetail.tsx,problem/ProblemDetail.tsx,change/ChangeDetail.tsx}`: consume context action state while keeping existing click handlers and modals.
- `itsm-frontend/src/lib/utils/workflow-state-machine.ts`: remove superseded Incident and Change logic only.

## Task 1: Add Shared Action Contract and Hide the Generic Action Bar by Default

**Files:**
- Create: `itsm-backend/service/action_actor.go`
- Modify: `itsm-backend/service/ticket_authorization.go`
- Modify: `itsm-frontend/src/components/work-item/WorkItemTypes.ts`
- Modify: `itsm-frontend/src/components/work-item/WorkItemShell.tsx`
- Create: `itsm-frontend/src/components/work-item/__tests__/WorkItemShell.test.tsx`

**Interfaces:**
- Produces: `service.ActionActor{Client *ent.Client, TenantID int, UserID int, Role string}`.
- Produces: `WorkItemShellProps.showActionBar?: boolean`, with `false` as the effective default.

- [x] **Step 1: Write failing WorkItemShell tests**

```tsx
it('does not render the generic action bar unless explicitly enabled', () => {
  render(<WorkItemShell {...props} professionalPanelSlot={<div>panel</div>} />);
  expect(screen.queryByRole('button', { name: /approve/i })).not.toBeInTheDocument();
});

it('renders the generic action bar when showActionBar is true', () => {
  render(<WorkItemShell {...props} showActionBar professionalPanelSlot={<div>panel</div>} />);
  expect(screen.getByRole('button', { name: /approve/i })).toBeInTheDocument();
});
```

- [x] **Step 2: Run the new frontend test to verify it fails**

Run: `cd itsm-frontend && npm test -- WorkItemShell.test.tsx --runInBand`

Expected: FAIL because `showActionBar` is not a prop and the action bar is unconditional.

- [x] **Step 3: Move ActionActor and make generic action bar opt-in**

```go
// itsm-backend/service/action_actor.go
package service

import "itsm-backend/ent"

type ActionActor struct {
	Client *ent.Client
	TenantID int
	UserID int
	Role string
}
```

```tsx
// WorkItemTypes.ts
showActionBar?: boolean;

// WorkItemShell.tsx
export function WorkItemShell({ showActionBar = false, ...props }: WorkItemShellProps) {
  // Preserve the existing provider; professional panels use context actions directly.
  {showActionBar && <WorkItemActionBar />}
}
```

Delete the local `ActionActor` declaration from `ticket_authorization.go`; keep all Ticket function signatures unchanged because the type remains in package `service`. Update the stale WorkItemShell comment that says professional panels should dispatch through the generic callback: these three panels keep their existing dedicated API calls because their actions require domain-specific payloads and modals.

- [x] **Step 4: Run focused checks**

Run: `cd itsm-backend && go test ./service -run 'Test.*(TicketAuthorization|Action)' -count=1`

Run: `cd itsm-frontend && npm test -- WorkItemShell.test.tsx --runInBand && npm run type-check`

Expected: focused backend test, shell test, and type check pass.

- [x] **Step 5: Commit the shared foundation**

```bash
git add itsm-backend/service/action_actor.go itsm-backend/service/ticket_authorization.go \
  itsm-frontend/src/components/work-item/WorkItemTypes.ts \
  itsm-frontend/src/components/work-item/WorkItemShell.tsx \
  itsm-frontend/src/components/work-item/__tests__/WorkItemShell.test.tsx
git commit -m "feat(workitem): make generic action bar opt-in"
```

## Task 2: Enforce the Incident-to-Problem Relation Invariant Before Schema Creation

**Files:**
- Modify: `itsm-backend/ent/schema/work_item_relation.go`
- Create: `itsm-backend/internal/bootstrap/incident_problem_relation_migration.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`
- Create: `itsm-backend/internal/bootstrap/incident_problem_relation_migration_test.go`
- Modify: `itsm-backend/ent/schema/work_item_relation_test.go`

**Interfaces:**
- Produces: `prepareIncidentProblemRelationMigration(ctx context.Context, db *sql.DB, logger *zap.SugaredLogger) error`.
- Produces: Ent partial unique index on `(tenant_id, source_work_item_id)` where `deleted_at IS NULL AND relation_type = 'investigated_by'`.

- [x] **Step 1: Write failing preflight and schema tests**

```go
func TestPrepareIncidentProblemRelationMigrationRejectsDuplicateLiveRelations(t *testing.T) {
  // Seed one tenant/source with two non-deleted investigated_by relations.
  err := prepareIncidentProblemRelationMigration(ctx, db, logger)
  require.Error(t, err)
  require.ErrorContains(t, err, "tenant_id=1")
  require.ErrorContains(t, err, "source_work_item_id=10")
}

func TestWorkItemRelationInvestigatedByUniquePerSource(t *testing.T) {
  // Same tenant and source, different targets: second live investigated_by insert must fail.
}
```

- [x] **Step 2: Run the focused tests to verify they fail**

Run: `cd itsm-backend && go test ./internal/bootstrap ./ent/schema -run 'Test(PrepareIncidentProblemRelation|WorkItemRelationInvestigatedBy)' -count=1`

Expected: FAIL because there is no preflight and the current unique index includes target WorkItem ID.

- [x] **Step 3: Implement preflight-before-DDL and the Ent-owned partial index**

```go
// app.go, inside cfg.Deployment.AutoMigrate and before client.Schema.Create
if err := prepareIncidentProblemRelationMigration(ctx, database.GetRawDB(), sugar); err != nil {
  return fmt.Errorf("prepare incident/problem relation migration: %w", err)
}
```

```go
// work_item_relation.go
index.Fields("tenant_id", "source_work_item_id").
  Unique().
  Annotations(entsql.IndexWhere("deleted_at IS NULL AND relation_type = 'investigated_by'"))
```

Follow `prepareCMDBModelMigration` for fresh-database behavior: when `work_item_relations` does not exist, log a debug skip and return nil. Query the duplicate groups first, then query their relation/target IDs so the returned error identifies tenant, source, target, and relation IDs. Do not add a `migration.RegisteredMigration`; `client.Schema.Create` creates the index only after the preflight succeeds.

- [x] **Step 4: Run focused tests and bootstrap package checks**

Run: `cd itsm-backend && go test ./internal/bootstrap ./ent/schema -run 'Test(PrepareIncidentProblemRelation|WorkItemRelation)' -count=1`

Expected: PASS, including fresh-table skip and duplicate-conflict diagnostics.

- [x] **Step 5: Commit the relation invariant**

```bash
git add itsm-backend/ent/schema/work_item_relation.go \
  itsm-backend/ent/schema/work_item_relation_test.go \
  itsm-backend/internal/bootstrap/incident_problem_relation_migration.go \
  itsm-backend/internal/bootstrap/incident_problem_relation_migration_test.go \
  itsm-backend/internal/bootstrap/app.go
git commit -m "feat(problem): enforce one live incident investigation relation"
```

## Task 3: Move Incident-to-Problem Conversion into One Problem-Domain Transaction

**Files:**
- Modify: `itsm-backend/handlers/problem/repository.go`
- Modify: `itsm-backend/handlers/problem/repository_impl.go`
- Modify: `itsm-backend/handlers/problem/service.go`
- Modify: `itsm-backend/handlers/problem/handler.go`
- Create: `itsm-backend/handlers/problem/conversion.go`
- Create: `itsm-backend/handlers/problem/conversion_test.go`
- Modify: `itsm-backend/controller/incident_controller.go`
- Modify: `itsm-backend/controller/incident_controller_test.go`
- Modify: `itsm-backend/service/root_cause_analysis_service.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`

**Interfaces:**
- Produces: `problem.ConversionService` with `CreateFromIncident(ctx context.Context, tenantID, incidentID, actorUserID int, req dto.ConvertIncidentToProblemRequest) (*problem.Problem, error)`.
- Produces: `problem.ToResponse(p *Problem) *dto.ProblemResponse`; `Handler.toDTO` delegates to it before mapping associations.
- Produces: repository-private `createInTx(ctx context.Context, tx *ent.Tx, p *Problem) (*Problem, error)`.
- Consumes: Task 2’s unique relation constraint and preflight.

- [x] **Step 1: Write failing conversion tests**

```go
func TestCreateFromIncidentCreatesWorkItemsRelationAndAuditAtomically(t *testing.T) {
  created, err := svc.CreateFromIncident(ctx, tenantID, incidentID, actorID, req)
  require.NoError(t, err)
  require.NotNil(t, created.WorkItemID)
  requireLiveInvestigatedBy(t, client, tenantID, incidentWorkItemID, *created.WorkItemID, actorID)
  requireIncidentConversionEvent(t, client, incidentID, tenantID, actorID, created.ID)
  requireConversionAudit(t, client, tenantID, actorID, incidentWorkItemID, *created.WorkItemID)
}

func TestCreateFromIncidentRollsBackWhenRelationAlreadyExists(t *testing.T) {
  // Arrange a live relation, call conversion, then assert Problem/WorkItem counts do not increase.
}

func TestConvertToProblemControllerUsesConversionServiceAndPublicMapper(t *testing.T) {
  // Fake ConversionService returns a domain Problem; response is dto.ProblemResponse, not ent.Problem.
}
```

Also add cases for cross-tenant Incident, closed Incident, missing source WorkItem, duplicate concurrent requests, and an injected failure in each of relation, IncidentEvent, and AuditLog writes. Each failure case must assert no target WorkItem, Problem, relation, IncidentEvent, or AuditLog remains.

- [x] **Step 2: Run the conversion tests to verify they fail**

Run: `cd itsm-backend && go test ./handlers/problem ./controller -run 'Test(CreateFromIncident|ConvertToProblem)' -count=1`

Expected: FAIL because conversion still uses RCA’s direct Ent write and `Create` owns an unshareable transaction.

- [x] **Step 3: Refactor Problem Create and implement conversion**

```go
func (r *EntRepository) Create(ctx context.Context, p *Problem) (*Problem, error) {
  tx, err := r.client.Tx(ctx)
  if err != nil { return nil, fmt.Errorf("start problem transaction: %w", err) }
  created, err := r.createInTx(ctx, tx, p)
  if err != nil { _ = tx.Rollback(); return nil, err }
  if err := tx.Commit(); err != nil { return nil, err }
  return created, nil
}
```

Move the current creator validation, WorkItem number generation, Ticket creation, Problem creation, and domain mapping into `createInTx`; it must never commit or roll back itself. In `CreateFromIncident`, open one transaction, tenant-scope read the Incident and its WorkItem, recheck closed/duplicate rules in that transaction, call `createInTx`, insert the `investigated_by` relation, then insert:

```go
tx.IncidentEvent.Create().
  SetIncidentID(incidentID).SetTenantID(tenantID).SetUserID(actorUserID).
  SetSource("incident_conversion").SetEventType("conversion").
  SetEventName("convert_to_problem").
  SetData(map[string]any{"problem_id": created.ID, "problem_work_item_id": *created.WorkItemID}).Save(ctx)

tx.AuditLog.Create().
  SetTenantID(tenantID).SetUserID(actorUserID).
  SetResource("incident").SetAction("convert_to_problem").
  SetPath("/api/v1/incidents/:id/convert-to-problem").SetMethod("POST").
  SetRequestBody(redactedConversionAuditJSON(...)).Save(ctx)
```

Define the narrow interface in `handlers/problem`, have `*problem.Service` implement it, and inject it into `IncidentController`. Move `problemRepo`, `problemServiceDomain`, and `problemHandler` construction above `NewIncidentController` in bootstrap. Keep `RootCauseAnalysisService` only for its remaining RCA endpoints; delete `CreateProblemFromIncident` and its controller call. Controller maps the returned domain object through `problem.ToResponse`, never through a copied mapper or `dto.ToProblemResponse`.

- [x] **Step 4: Run focused transaction and controller tests**

Run: `cd itsm-backend && go test ./handlers/problem ./controller -run 'Test(CreateFromIncident|ConvertToProblem|Problem.*Create)' -count=1`

Expected: PASS, including no-orphan assertions on each forced failure path.

- [x] **Step 5: Commit the atomic conversion path**

```bash
git add itsm-backend/handlers/problem itsm-backend/controller/incident_controller.go \
  itsm-backend/controller/incident_controller_test.go \
  itsm-backend/service/root_cause_analysis_service.go itsm-backend/internal/bootstrap/app.go
git commit -m "feat(problem): convert incidents through atomic work item creation"
```

## Task 4: Add Incident Actions, Command Guard, and Single-Snapshot Detail Response

**Files:**
- Create: `itsm-backend/service/incident_authorization.go`
- Create: `itsm-backend/service/incident_authorization_test.go`
- Modify: `itsm-backend/service/incident_service.go`
- Modify: `itsm-backend/service/incident_service_test.go`
- Modify: `itsm-backend/dto/incident_dto.go`
- Modify: `itsm-backend/controller/incident_controller.go`
- Modify: `itsm-backend/controller/incident_controller_test.go`
- Modify: `itsm-backend/router/router.go`
- Modify: `itsm-backend/router/router_test.go`

**Interfaces:**
- Produces: `BuildIncidentActions(actor service.ActionActor, incident *ent.Incident) map[string]dto.ActionPermission`.
- Produces: `GetIncidentWithActions(ctx context.Context, id int, actor ActionActor) (*dto.IncidentResponse, error)`.
- Consumes: Task 3’s `problem.ConversionService` for `convert_to_problem` command handling.

- [x] **Step 1: Write failing Incident action and contract tests**

```go
func TestBuildIncidentActionsMirrorsIncidentCommandRules(t *testing.T) {
  require.True(t, BuildIncidentActions(actor, inProgress)["resolve"].Allowed)
  require.False(t, BuildIncidentActions(actor, resolved)["resolve"].Allowed)
  require.True(t, BuildIncidentActions(actor, closed)["reopen"].Allowed)
  require.False(t, BuildIncidentActions(actor, closed)["assign"].Allowed)
}

func TestAssignIncidentRejectsResolvedAndClosed(t *testing.T) { /* direct service command */ }
func TestGetIncidentWithActionsUsesOneEntitySnapshot(t *testing.T) { /* mapper and actions receive same entity */ }
func TestIncidentDetailHasActionsButListDoesNot(t *testing.T) { /* HTTP JSON contract */ }
func TestAssignRouteUsesIncidentWritePermission(t *testing.T) { /* non-super-admin incident:write succeeds */ }
```

- [x] **Step 2: Run focused Incident tests to verify they fail**

Run: `cd itsm-backend && go test ./service ./controller ./router -run 'Test(BuildIncidentActions|AssignIncidentRejects|GetIncidentWithActions|IncidentDetailHasActions|AssignRoute)' -count=1`

Expected: FAIL because the action map, shared guard, and detail-only response method do not exist.

- [x] **Step 3: Implement the projection and reuse command predicates**

Add `Actions map[string]ActionPermission \`json:"actions,omitempty"\`` to `dto.IncidentResponse`. Extract `getIncidentEntity(ctx, id, tenantID)` from the current `GetIncident`; both `GetIncident` and `GetIncidentWithActions` call it exactly once. `GetIncidentWithActions` maps and evaluates `BuildIncidentActions` against that same `*ent.Incident`.

Implement `CanEditIncident`, `CanResolveIncident`, `CanCloseIncident`, `CanReopenIncident`, `CanEscalateIncident`, `CanAssignIncident`, `CanMarkMajorIncident`, and `CanConvertToProblem` as specified. Keep DB failure in the relation lookup fail-closed. Extract and use `canAssignIncidentStatus` from both `CanAssignIncident` and `AssignIncident`; do not put lifecycle validation in the router.

```go
func (s *IncidentService) GetIncidentWithActions(ctx context.Context, id int, actor ActionActor) (*dto.IncidentResponse, error) {
  incident, err := s.getIncidentEntity(ctx, id, actor.TenantID)
  if err != nil { return nil, err }
  response := s.toIncidentResponse(incident)
  response.Actions = BuildIncidentActions(actor, incident)
  return response, nil
}
```

In `IncidentController.GetIncident`, require positive `user_id`, non-empty `role`, and positive tenant before calling the new method. Change only the assignment route middleware from `RequirePermission("incident", "assign")` to `RequirePermission("incident", "write")`.

- [x] **Step 4: Run focused Incident checks**

Run: `cd itsm-backend && go test ./service ./controller ./router -run 'Test(BuildIncidentActions|AssignIncident|GetIncidentWithActions|Incident.*Actions|AssignRoute)' -count=1`

Expected: PASS; direct write command and GET projection agree on assignment and conversion eligibility.

- [x] **Step 5: Commit Incident backend actions**

```bash
git add itsm-backend/service/incident_authorization.go itsm-backend/service/incident_authorization_test.go \
  itsm-backend/service/incident_service.go itsm-backend/service/incident_service_test.go \
  itsm-backend/dto/incident_dto.go itsm-backend/controller/incident_controller.go \
  itsm-backend/controller/incident_controller_test.go itsm-backend/router/router.go itsm-backend/router/router_test.go
git commit -m "feat(incident): project command-safe detail actions"
```

## Task 5: Consume Incident Actions in the Existing Incident Detail UI

**Files:**
- Modify: `itsm-frontend/src/lib/api/incident-api.ts`
- Modify: `itsm-frontend/src/app/(main)/incidents/[id]/page.tsx`
- Modify: `itsm-frontend/src/components/incident/IncidentDetail.tsx`
- Modify: `itsm-frontend/src/lib/utils/workflow-state-machine.ts`
- Create: `itsm-frontend/src/components/incident/__tests__/IncidentDetail.test.tsx`

**Interfaces:**
- Consumes: `Incident.actions?: Record<string, WorkItemActionState>` and `useWorkItemContext().actions`.
- Produces: Incident controls whose visibility/disabled state/reason derive solely from API actions.

- [x] **Step 1: Write failing UI tests**

```tsx
it('disables the resolve control with the backend reason', () => {
  renderWithWorkItemContext(<IncidentDetail incident={incident} />, {
    resolve: { allowed: false, reason: '只有处理中的事件可以解决' },
  });
  expect(screen.getByRole('button', { name: /解决/i })).toBeDisabled();
  expect(screen.getByText('只有处理中的事件可以解决')).toBeInTheDocument();
});

it('does not run a client transition pre-check before the existing resolve handler', async () => {
  // An allowed action calls the existing IncidentAPI handler directly.
});
```

- [x] **Step 2: Run the UI test to verify it fails**

Run: `cd itsm-frontend && npm test -- IncidentDetail.test.tsx --runInBand`

Expected: FAIL because the detail component has no WorkItem context consumer and still imports the client transition validator.

- [x] **Step 3: Wire typed API actions, page pass-through, and context controls**

```ts
// incident-api.ts
actions?: Record<string, WorkItemActionState>;

// page.tsx
<WorkItemShell actions={incident.actions ?? {}} showActionBar={false} onActionDispatch={async () => {}} ... />
```

In `IncidentDetail`, call `useWorkItemContext()` and replace all inline status gates for `edit`, `resolve`, `close`, `reopen`, `escalate`, `assign`, `mark_major_incident`, and `convert_to_problem` with the matching action entry. Preserve existing `handleEscalate`, `handleAssignClick`, `handleResolveClick`, `handleClose`, `handleConvertToProblem`, and `handleReopen` functions and their modals. Remove the `isValidIncidentTransition` call and delete only `INCIDENT_STATUS_TRANSITIONS` and `isValidIncidentTransition` from `workflow-state-machine.ts`; retain Ticket exports.

- [x] **Step 4: Run focused frontend checks**

Run: `cd itsm-frontend && npm test -- IncidentDetail.test.tsx workflow-state-machine.test.ts --runInBand && npm run type-check`

Expected: PASS, with no remaining Incident transition-validator import.

- [x] **Step 5: Commit Incident frontend consumption**

```bash
git add itsm-frontend/src/lib/api/incident-api.ts \
  'itsm-frontend/src/app/(main)/incidents/[id]/page.tsx' \
  itsm-frontend/src/components/incident/IncidentDetail.tsx \
  itsm-frontend/src/components/incident/__tests__/IncidentDetail.test.tsx \
  itsm-frontend/src/lib/utils/workflow-state-machine.ts
git commit -m "feat(incident): render detail controls from backend actions"
```

## Task 6: Add Problem Actions and Consume Them in Problem Detail

**Files:**
- Create: `itsm-backend/handlers/problem/authorization.go`
- Create: `itsm-backend/handlers/problem/authorization_test.go`
- Modify: `itsm-backend/handlers/problem/handler.go`
- Modify: `itsm-backend/handlers/problem/handler_test.go`
- Modify: `itsm-backend/dto/problem_dto.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`
- Modify: `itsm-frontend/src/lib/api/problem-api.ts`
- Modify: `itsm-frontend/src/app/(main)/problems/[id]/page.tsx`
- Modify: `itsm-frontend/src/components/problem/ProblemDetail.tsx`
- Create: `itsm-frontend/src/components/problem/__tests__/ProblemDetail.test.tsx`

**Interfaces:**
- Produces: `problem.BuildProblemActions(actor service.ActionActor, p *problem.Problem) map[string]dto.ActionPermission`.
- Consumes: shared `ActionActor` from Task 1 and `Problem.actions` from the Problem detail API response.

- [x] **Step 1: Write failing backend and frontend tests**

```go
func TestBuildProblemActionsUsesCanonicalStatuses(t *testing.T) {
  require.True(t, CanStartInvestigation(actor, &Problem{Status: "open"}).Allowed)
  require.False(t, CanStartInvestigation(actor, &Problem{Status: "in_progress"}).Allowed)
  require.True(t, CanResolveProblem(actor, &Problem{Status: "investigating"}).Allowed)
  require.True(t, CanResolveProblem(actor, &Problem{Status: "identified"}).Allowed)
  require.True(t, CanResolveProblem(actor, &Problem{Status: "in_progress"}).Allowed)
}
```

```tsx
it('starts investigation with the canonical investigating status', async () => {
  renderWithWorkItemContext(<ProblemDetail problem={problem} />, {
    start_investigation: { allowed: true }, resolve: { allowed: false }, close: { allowed: false }, edit: { allowed: true },
  });
  await user.click(screen.getByRole('button', { name: /开始调查/i }));
  expect(updateProblem).toHaveBeenCalledWith(problem.id, expect.objectContaining({ status: ProblemStatus.INVESTIGATING }));
});
```

- [x] **Step 2: Run focused tests to verify they fail**

Run: `cd itsm-backend && go test ./handlers/problem -run 'Test(BuildProblemActions|Problem.*Actions)' -count=1`

Run: `cd itsm-frontend && npm test -- ProblemDetail.test.tsx --runInBand`

Expected: FAIL because the action builder, DTO field, client type, and context controls do not exist.

- [x] **Step 3: Implement detail projection and UI consumption**

Add `Actions` to `dto.ProblemResponse`, inject the existing Ent client into `problem.NewHandler`, and validate identity context (`tenant_id`, positive `user_id`, non-empty `role`) in `Handler.Get`. Build actions immediately after `h.toDTO(p)`; list paths remain unchanged. Implement `edit`, `start_investigation`, `resolve`, and `close` exactly as in the design, including legacy `in_progress` as a resolvable source and `resolved` as the sole closable source.

```go
func BuildProblemActions(actor service.ActionActor, p *Problem) map[string]dto.ActionPermission {
  return map[string]dto.ActionPermission{
    "edit": CanEditProblem(actor),
    "start_investigation": CanStartInvestigation(actor, p),
    "resolve": CanResolveProblem(actor, p),
    "close": CanCloseProblem(actor, p),
  }
}
```

Update bootstrap and every handler test fixture for the new `NewHandler(service, client)` constructor. In the page pass `problem.actions ?? {}` to the shell. In `ProblemDetail`, read context actions, rename the button/key to `start_investigation`, and call the existing update handler with `ProblemStatus.INVESTIGATING`; do not add the excluded dedicated form endpoints.

- [x] **Step 4: Run focused checks**

Run: `cd itsm-backend && go test ./handlers/problem -run 'Test(BuildProblemActions|Handler.*Get|Problem.*Actions)' -count=1`

Run: `cd itsm-frontend && npm test -- ProblemDetail.test.tsx --runInBand && npm run type-check`

Expected: PASS; the old `in_progress` target is absent from the new-button path.

- [x] **Step 5: Commit Problem actions**

```bash
git add itsm-backend/handlers/problem itsm-backend/dto/problem_dto.go itsm-backend/internal/bootstrap/app.go \
  itsm-frontend/src/lib/api/problem-api.ts 'itsm-frontend/src/app/(main)/problems/[id]/page.tsx' \
  itsm-frontend/src/components/problem/ProblemDetail.tsx \
  itsm-frontend/src/components/problem/__tests__/ProblemDetail.test.tsx
git commit -m "feat(problem): project and consume detail actions"
```

## Task 7: Add Change Actions, Command-Side Self-Approval Guard, and Detail UI Consumption

**Files:**
- Create: `itsm-backend/handlers/change/authorization.go`
- Create: `itsm-backend/handlers/change/authorization_test.go`
- Modify: `itsm-backend/handlers/change/service.go`
- Modify: `itsm-backend/handlers/change/service_test.go`
- Modify: `itsm-backend/handlers/change/handler.go`
- Modify: `itsm-backend/handlers/change/handler_test.go`
- Modify: `itsm-backend/dto/change_dto.go`
- Modify: `itsm-frontend/src/lib/api/change-api.ts`
- Modify: `itsm-frontend/src/app/(main)/changes/[id]/page.tsx`
- Modify: `itsm-frontend/src/components/change/ChangeDetail.tsx`
- Modify: `itsm-frontend/src/lib/utils/workflow-state-machine.ts`
- Create: `itsm-frontend/src/components/change/__tests__/ChangeDetail.test.tsx`

**Interfaces:**
- Produces: `change.BuildChangeActions(actor service.ActionActor, c *change.Change) map[string]dto.ActionPermission`.
- Produces: `canApproveChange(actorUserID int, c *Change) error`, shared by the read projection and `TransitionStatus` for approve/reject.

- [x] **Step 1: Write failing backend and UI tests**

```go
func TestCanStartImplementationIsTypeAware(t *testing.T) {
  require.False(t, CanStartImplementation(actor, &Change{Type: "normal", Status: "approved"}).Allowed)
  require.True(t, CanStartImplementation(actor, &Change{Type: "normal", Status: "scheduled"}).Allowed)
  require.True(t, CanStartImplementation(actor, &Change{Type: "standard", Status: "approved"}).Allowed)
  require.True(t, CanStartImplementation(actor, &Change{Type: "emergency", Status: "approved"}).Allowed)
  require.False(t, CanStartImplementation(actor, &Change{Type: "emergency", Status: "scheduled"}).Allowed)
}

func TestTransitionStatusRejectsSelfApprovalAndSelfRejection(t *testing.T) {
  // Direct approve and reject requests by CreatedBy both return the shared domain error.
}
```

```tsx
it('uses distinct backend approval and rejection states', () => {
  renderWithWorkItemContext(<ChangeDetail change={change} />, {
    approve: { allowed: false, reason: '不能审批自己提交的变更' },
    reject: { allowed: false, reason: '不能驳回自己提交的变更' },
  });
  expect(screen.getByRole('button', { name: /批准/i })).toBeDisabled();
  expect(screen.getByRole('button', { name: /驳回/i })).toBeDisabled();
});
```

- [x] **Step 2: Run focused tests to verify they fail**

Run: `cd itsm-backend && go test ./handlers/change -run 'Test(CanStartImplementation|TransitionStatusRejectsSelf|BuildChangeActions)' -count=1`

Run: `cd itsm-frontend && npm test -- ChangeDetail.test.tsx --runInBand`

Expected: FAIL because Change detail response has no action projection and direct approve/reject do not share a self-approval guard.

- [x] **Step 3: Implement Change authorization, command enforcement, and UI consumption**

Add `Actions` to `dto.ChangeResponse`. In `Handler.GetChange`, validate actor context, build an `ActionActor` using the service’s Ent client, and attach `BuildChangeActions` to the single-record DTO only. Implement submit, approve, reject, start implementation, and complete implementation as defined in the spec; do not query BPMN task liveness in `CanApproveChange`.

```go
if targetStatus == string(dto.ChangeStatusApproved) || targetStatus == string(dto.ChangeStatusRejected) {
  if err := canApproveChange(userID, current); err != nil {
    return nil, err
  }
  if err := s.completeChangeApprovalTask(ctx, tenantID, userID, id, targetStatus, comment); err != nil {
    return nil, err
  }
}
```

Keep the reject-comment requirement in request validation, not the `actions` map. Update API type/page pass-through. In `ChangeDetail`, replace `canApprove` and all status-based visibility predicates for submit/approve/reject/start/complete with the corresponding action entries, preserving existing handlers and modals. Delete only `CHANGE_STATUS_TRANSITIONS` from `workflow-state-machine.ts`; leave Ticket logic untouched.

- [x] **Step 4: Run focused checks**

Run: `cd itsm-backend && go test ./handlers/change -run 'Test(CanStartImplementation|TransitionStatusRejectsSelf|BuildChangeActions|Handler.*Get)' -count=1`

Run: `cd itsm-frontend && npm test -- ChangeDetail.test.tsx workflow-state-machine.test.ts --runInBand && npm run type-check`

Expected: PASS; normal/approved never exposes start implementation and direct self-approval/rejection fails.

- [x] **Step 5: Commit Change actions**

```bash
git add itsm-backend/handlers/change itsm-backend/dto/change_dto.go \
  itsm-frontend/src/lib/api/change-api.ts 'itsm-frontend/src/app/(main)/changes/[id]/page.tsx' \
  itsm-frontend/src/components/change/ChangeDetail.tsx \
  itsm-frontend/src/components/change/__tests__/ChangeDetail.test.tsx \
  itsm-frontend/src/lib/utils/workflow-state-machine.ts
git commit -m "feat(change): enforce and render detail actions"
```

## Task 8: Run Cross-Domain Regression and Contract Verification

**Files:**
- Modify only if a regression exposes a defect in Tasks 1-7.

**Interfaces:**
- Verifies: all three detail contracts include `actions`; list contracts omit them; old duplicate creation and frontend state-machine paths are absent.

- [x] **Step 1: Add or complete HTTP contract coverage**

```go
func TestDetailActionsArePresentOnlyOnDetailEndpoints(t *testing.T) {
  // GET /incidents/:id, /problems/:id, /changes/:id contain actions.
  // GET /incidents, /problems, /changes omit actions.
}
```

Use response JSON assertions rather than only Go DTO assertions so tags and handler wiring are covered.

- [x] **Step 2: Run backend regression suite**

Run: `cd itsm-backend && go test ./service ./handlers/problem ./handlers/change ./controller ./router ./internal/bootstrap ./ent/schema -count=1`

Expected: PASS.

- [x] **Step 3: Run frontend regression suite**

Run: `cd itsm-frontend && npm test -- --runInBand`

Run: `cd itsm-frontend && npm run type-check && npm run lint:check`

Expected: focused feature suites, type check, and lint exit 0. The repository-wide Jest run may retain the documented pre-existing baseline of 49 failures in `template-api.test.ts` and `sla-api.test.ts`; no additional suite or test may fail.

- [x] **Step 4: Run repository-wide static checks and inspect the final diff**

Run: `cd itsm-backend && go vet ./... && go build ./...`

Run: `git diff --check`

Run: `rg -n 'CreateProblemFromIncident|INCIDENT_STATUS_TRANSITIONS|CHANGE_STATUS_TRANSITIONS|RequirePermission\("incident", "assign"\)' itsm-backend itsm-frontend`

Expected: static checks pass; the final search has no production references (test fixture names may be renamed or removed); diff check is clean.

- [x] **Step 5: Commit any regression-only fixes**

```bash
git add -A
git commit -m "test: verify incident problem change action contracts"
```

Only create this commit when Step 1 or verification uncovered and fixed an actual regression; otherwise do not create an empty commit.

## Task 9: Apply Final Review Hardening

**Final public contract:** HTTP `actions` keys are camelCase. Internal Incident conversion event and audit
values remain `convert_to_problem` because they are domain identifiers rather than JSON field names.

- [x] Resolve the selected MSP customer tenant for every Change and Problem customer-domain handler; update
  direct-handler fixtures to install the same `TenantContext` supplied by production middleware.
- [x] Reject `cancelled` Incident conversion, major-incident escalation, and assignment on both read and write
  paths using the shared final-status predicate.
- [x] Soft-delete live `investigated_by` relations in the same transaction as Problem soft deletion, and prove
  that the source Incident can be converted again.
- [x] Make Change start-implementation projection call the canonical status transition function, including
  standard/emergency `draft -> in_progress` paths.
- [x] Publish camelCase keys: `markMajorIncident`, `convertToProblem`, `startInvestigation`,
  `submitForApproval`, `startImplementation`, and `completeImplementation`; update all frontend consumers and
  contract tests atomically.
- [x] Move `investigated_by` to `common.WorkItemRelationInvestigatedBy` without introducing an upward
  dependency from `service` to `handlers/problem`.
- [x] Extract the repeated detail button behavior into `WorkItemActionButton` and retain professional handlers,
  modals, loading state, and reason presentation.

**Verification evidence:**

- `cd itsm-backend && go test ./... -count=1` — pass.
- Five focused frontend suites (`WorkItemActionButton`, `WorkItemActionBar`, Incident, Problem, Change) —
  27 tests pass.
- `cd itsm-frontend && npm run type-check` — pass.
- `cd itsm-frontend && npm run lint:check` — 0 errors; 3 unrelated pre-existing warnings.
- `cd itsm-frontend && npm run build` — pass.
- `git diff --check` — pass.

## Plan Self-Review

- Spec coverage: Tasks 1-7 cover shared actor/action shell, preflight/index ownership, atomic conversion/audit/bootstrap, Incident/Problem/Change command and read-side rules, frontend context consumption, and removal of superseded paths. Task 8 validates detail-vs-list contracts and cross-domain regressions.
- Scope: excluded cancellation/rollback and Problem dedicated-form work have no implementation task.
- Type consistency: all domain action builders consume `service.ActionActor`; `ProblemConversionService` returns `*problem.Problem` and the controller maps it through `problem.ToResponse`; frontend receives `Record<string, WorkItemActionState>`.
- Placeholder scan: no unresolved implementation placeholders remain.
