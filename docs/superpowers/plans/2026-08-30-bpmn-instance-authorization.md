# BPMN Instance Authorization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce tenant-safe, participant-aware authorization for every active BPMN process-instance and task API, with elevated access from existing RBAC permissions and transactional audit records for mutations.

**Architecture:** The controller converts authenticated Gin state and existing RBAC decisions into a trusted `BPMNAccessScope` stored in `context.Context`. A single service-layer resolver matches initiator, assignee, candidate users, roles, and groups. Mutations authorize first, then write state and `ProcessAuditLog` through one Ent transaction.

**Tech Stack:** Go 1.24+, Gin, Ent, SQLite enttest, testify, PostgreSQL-compatible query semantics.

**Spec:** `docs/superpowers/specs/2026-08-30-architecture-assessment-remediation-execution-plan-design.md` (Wave 0 / S0), with detailed input from `docs/superpowers/specs/2026-08-25-bpmn-task-instance-authorization-design.md`.

## Global Constraints

- Record the actual execution HEAD before starting; this plan was written after design commits `79d9117e` and `301ddb1d`.
- Reuse `process_instance:read`, `process_instance:update`, `task:read`, and `task:update`; do not add roles or a parallel permission store.
- Preserve the existing route contract: `/bpmn/*` supports participant-aware access, while `/workflow/*` remains an RBAC-protected compatibility alias. Both paths must still pass the same service-layer policy; do not duplicate authorization logic in handlers.
- Tenant ID, user ID, role, and elevated scope come only from authenticated context.
- Missing actor or tenant context fails closed on HTTP-facing services.
- CSV candidate matching is token-exact after trimming; user ID `1` must never match `11`.
- Cross-tenant access returns not-found or forbidden without exposing target data.
- Every mutation writes `ProcessAuditLog` in the same Ent transaction as its state change.
- Preserve KAF async-task authorization by task type, delegated status, actor role, and tenant.
- Do not redesign BPMN schemas or add normalized candidate tables in this security fix.
- Use TDD and commit each completed task separately.

---

## File Structure

| File | Responsibility |
|---|---|
| `itsm-backend/service/bpmn_access_scope.go` | Trusted access-scope value and context helpers. |
| `itsm-backend/service/bpmn_participation.go` | The only actor/task/process participation resolver. |
| `itsm-backend/service/bpmn_authorization_test.go` | Ent fixture and service-level security regression tests. |
| `itsm-backend/service/bpmn_process_engine.go` | Apply scope to process/task reads and mutations. |
| `itsm-backend/service/bpmn_audit_service.go` | Transaction-compatible mutation audit methods. |
| `itsm-backend/controller/bpmn_workflow_controller.go` | Build trusted scope and map structured errors to HTTP. |
| `itsm-backend/controller/bpmn_workflow_authorization_test.go` | Real HTTP authorization matrix. |

### Task 1: Trusted BPMN Access Scope

**Files:**
- Create: `itsm-backend/service/bpmn_access_scope.go`
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:34-50`
- Modify: `itsm-backend/controller/bpmn_workflow_controller_test.go`
- Create: `itsm-backend/controller/bpmn_workflow_authorization_test.go`

**Interfaces:**
- Produces: `service.BPMNAccessScope`.
- Produces: `service.WithBPMNAccessScope(context.Context, BPMNAccessScope) context.Context`.
- Produces: `service.BPMNAccessScopeFromContext(context.Context) (BPMNAccessScope, error)`.
- Consumes: `middleware.HasResourcePermission(*ent.Client, role, resource, action, tenantID)`.

- [ ] **Step 1: Write the failing context tests**

```go
func TestGetBPMNTenantContextBuildsTrustedScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:bpmn_scope?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bpmn/tasks", nil)
	c.Set("tenant_id", 42)
	c.Set("user_id", 7)
	c.Set("role", "super_admin")
	c.Set("client", client)

	workflowCtx, tenantID, ok := getBPMNTenantContext(c)
	require.True(t, ok)
	require.Equal(t, 42, tenantID)
	scope, err := service.BPMNAccessScopeFromContext(workflowCtx)
	require.NoError(t, err)
	assert.Equal(t, 7, scope.UserID)
	assert.True(t, scope.CanReadAllInstances)
	assert.True(t, scope.CanUpdateAllInstances)
	assert.True(t, scope.CanReadAllTasks)
	assert.True(t, scope.CanUpdateAllTasks)
}

func TestGetBPMNTenantContextRejectsMissingTenantOrActor(t *testing.T) {
	for _, input := range []struct{ tenantID, userID int }{{0, 7}, {42, 0}} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bpmn/tasks", nil)
		c.Set("tenant_id", input.tenantID)
		c.Set("user_id", input.userID)
		_, _, ok := getBPMNTenantContext(c)
		assert.False(t, ok)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd itsm-backend && go test ./controller -run 'TestGetBPMNTenantContext' -v`

Expected: FAIL because `BPMNAccessScope` does not exist and zero tenant/user currently passes too far.

- [ ] **Step 3: Implement the scope and controller construction**

```go
type BPMNAccessScope struct {
	UserID, TenantID      int
	CanReadAllInstances   bool
	CanUpdateAllInstances bool
	CanReadAllTasks       bool
	CanUpdateAllTasks     bool
}

type bpmnAccessScopeContextKey struct{}

func WithBPMNAccessScope(ctx context.Context, scope BPMNAccessScope) context.Context {
	return context.WithValue(ctx, bpmnAccessScopeContextKey{}, scope)
}

func BPMNAccessScopeFromContext(ctx context.Context) (BPMNAccessScope, error) {
	scope, ok := ctx.Value(bpmnAccessScopeContextKey{}).(BPMNAccessScope)
	if !ok || scope.UserID <= 0 || scope.TenantID <= 0 {
		return BPMNAccessScope{}, common.NewForbiddenError("缺少 BPMN 实例授权上下文")
	}
	return scope, nil
}
```

Update `getBPMNTenantContext` to reject non-positive tenant/user IDs, read `role` and `client` from Gin, compute the four flags through `HasResourcePermission`, set existing BPMN tenant/user keys, then call `WithBPMNAccessScope`. Do not accept scope values from request data.

Update the existing controller fake setup to set `tenant_id`, `user_id`, `role`, and `client`; use `super_admin` only where the old test is not exercising authorization behavior. Do not make production code fall back to elevated access to preserve an under-specified test.

- [ ] **Step 4: Run the focused tests**

Run: `cd itsm-backend && go test ./controller -run 'TestGetBPMNTenantContext' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn_access_scope.go itsm-backend/controller/bpmn_workflow_controller.go itsm-backend/controller/bpmn_workflow_controller_test.go itsm-backend/controller/bpmn_workflow_authorization_test.go
git commit -m "feat(bpmn): propagate trusted access scope"
```

### Task 2: Single Participation Resolver

**Files:**
- Create: `itsm-backend/service/bpmn_participation.go`
- Create: `itsm-backend/service/bpmn_authorization_test.go`
- Modify: `itsm-backend/service/bpmn_process_engine.go:101-136`

**Interfaces:**
- Produces: unexported `bpmnParticipationResolver`.
- Produces: `resolveActor(ctx context.Context, scope BPMNAccessScope) (*bpmnActorIdentity, error)`.
- Produces: `matchesTask(*ent.ProcessTask, *bpmnActorIdentity) bool`.
- Produces: `participatingInstanceIDs(context.Context, *bpmnActorIdentity) ([]int, error)`.
- Test helper: `(*bpmnAuthorizationFixture).scopedCtx(canReadInstances, canUpdateInstances, canReadTasks, canUpdateTasks bool) context.Context`; it always uses the fixture actor and tenant and delegates to `WithBPMNAccessScope`.

- [ ] **Step 1: Write exact-match, role, group, and tenant tests**

```go
func TestParticipationResolverMatchesExactIdentityAndGroups(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	actor, err := f.resolver.resolveActor(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID})
	require.NoError(t, err)

	assert.True(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, Assignee: strconv.Itoa(f.actor.ID)}, actor))
	assert.True(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateUsers: f.actor.Username + ", someone"}, actor))
	assert.True(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateGroups: "network_eng,vpn-operators"}, actor))
	assert.False(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateUsers: "1,11"}, actor))
	assert.False(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.otherTenant.ID, Assignee: strconv.Itoa(f.actor.ID)}, actor))
}
```

The fixture creates two tenants, an active actor, an additional `network_eng` role edge, and a `vpn-operators` group membership.

- [ ] **Step 2: Run the tests to verify failure**

Run: `cd itsm-backend && go test ./service -run 'TestParticipationResolver' -v`

Expected: FAIL because the resolver does not exist.

- [ ] **Step 3: Implement exact token matching**

```go
type bpmnActorIdentity struct {
	UserID, TenantID int
	UserTokens       map[string]struct{}
	GroupTokens      map[string]struct{}
}

type bpmnParticipationResolver struct {
	client        *ent.Client
	groupResolver *bpmn.GroupResolver
}

func csvTokens(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if token := strings.ToLower(strings.TrimSpace(part)); token != "" {
			out = append(out, token)
		}
	}
	return out
}
```

`resolveActor` queries an active user by both ID and tenant; user tokens are decimal ID, username, and email. Group tokens include primary role, all additional role codes, and `GroupResolver` names. `matchesTask` checks tenant first and then exact token membership. `participatingInstanceIDs` uses tenant-scoped broad DB predicates only as a prefilter, exact-filters every row, and deduplicates process instance IDs. Wire one resolver into both process and task services.

- [ ] **Step 4: Run resolver and existing group tests**

Run: `cd itsm-backend && go test ./service ./service/bpmn -run 'TestParticipationResolver|TestGroupResolver|TestResolveRoleCandidates' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn_participation.go itsm-backend/service/bpmn_authorization_test.go itsm-backend/service/bpmn_process_engine.go
git commit -m "feat(bpmn): centralize participant resolution"
```

### Task 3: Persist Initiator and Scope Process-Instance Reads

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:223-310`
- Modify: `itsm-backend/service/bpmn_process_engine.go:2180-2240`
- Modify: `itsm-backend/service/bpmn_process_engine.go:2535-2551`
- Test: `itsm-backend/service/bpmn_authorization_test.go`

**Interfaces:**
- Consumes: scope and participation resolver.
- Produces: `resolveProcessInitiator(context.Context, map[string]interface{}) string`.
- Produces: private `loadProcessInstance(ctx, instanceKey, tenantID)` for trusted tenant-scoped persistence lookup.
- Preserves: existing service method signatures.

- [ ] **Step 1: Write initiator and scoped-read tests**

```go
func TestStartProcessPersistsAuthenticatedInitiator(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := context.WithValue(f.userCtx, bpmn.BPMNUserIDContextKey, f.actor.ID)
	instance, err := f.engine.StartProcess(ctx, f.definition.Key, "ticket-1", "ticket", 1, map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(f.actor.ID), instance.Initiator)
}

func TestListProcessInstancesScopesParticipantAndElevatedReader(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	visible, _ := f.seedParticipantAndNonParticipantInstances()
rows, total, err := f.engine.ProcessInstanceService().ListProcessInstances(f.scopedCtx(false, false, false, false), &ListProcessInstancesRequest{TenantID: f.tenant.ID, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	assert.Equal(t, visible.ID, rows[0].ID)
_, elevatedTotal, err := f.engine.ProcessInstanceService().ListProcessInstances(f.scopedCtx(true, false, false, false), &ListProcessInstancesRequest{TenantID: f.tenant.ID, Page: 1, PageSize: 20})
	require.NoError(t, err)
	assert.Equal(t, 2, elevatedTotal)
}
```

Use the same fixture to add these named assertions:

```text
TestStartProcessUsesTrustedRequesterFallback: requester_id=actor.ID -> Initiator="<actor ID>"
TestGetProcessInstanceAuthorization: participant=success, outsider=forbidden, other tenant=not found
TestGetProcessInstanceVariablesAuthorization: same matrix as the parent instance
TestGetProcessInstanceHistoryAuthorization: same matrix as the parent instance
TestListApprovalDecisionsAuthorization: same matrix as the parent instance
TestInstanceStatisticsAuthorization: participant=forbidden, CanReadAllInstances=success, forged tenant ignored
```

Statistics require `CanReadAllInstances`; participant scope is insufficient because aggregate results cannot be safely reduced to one process without changing their contract. In the forged-tenant case, seed one instance in each tenant and assert the returned total is exactly the caller tenant's total.

- [ ] **Step 2: Run tests to verify current leakage**

Run: `cd itsm-backend && go test ./service -run 'TestStartProcessPersists|TestListProcessInstancesScopes|TestGetProcessInstanceAuthorization|TestListApprovalDecisionsAuthorization|TestInstanceStatisticsAuthorization' -v`

Expected: FAIL because initiator is not written, reads only enforce tenant predicates, and statistics accept caller-controlled scope without requiring aggregate-read permission.

- [ ] **Step 3: Implement initiator and instance predicates**

Prefer authenticated BPMN user ID; otherwise use positive trusted `requester_id`/`requesterId`; otherwise write `system`. For non-elevated lists, add initiator-or-participating-instance predicates before count and pagination. `GetProcessInstance`, variables/history, and approval history use the private tenant-scoped loader, then allow elevated read or exact participation. Reject a request tenant that differs from scope tenant. `GetInstanceStatistics` requires `CanReadAllInstances` and always replaces `req.TenantID` with the trusted scope tenant. `ListProcessInstances` and `GetInstanceStats` controllers must pass `workflowCtx`, not the raw Gin context, into the service.

```go
query = query.Where(processinstance.Or(
	processinstance.Initiator(strconv.Itoa(scope.UserID)),
	processinstance.IDIn(participatingIDs...),
))
```

When the ID slice is empty, use only the initiator predicate.

- [ ] **Step 4: Run process-instance tests**

Run: `cd itsm-backend && go test ./service -run 'TestStartProcessPersists|TestListProcessInstancesScopes|TestGetProcessInstanceAuthorization|TestListApprovalDecisionsAuthorization|TestGetProcessInstanceHistory|TestInstanceStatisticsAuthorization' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_authorization_test.go
git commit -m "fix(bpmn): scope process instance reads"
```

### Task 4: Scope Task Reads and Filter Overrides

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:2367-2551`
- Modify: `itsm-backend/service/bpmn_process_engine.go:2667-2687`
- Modify: `itsm-backend/service/bpmn_process_engine.go:2926-2978`
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:609-685`
- Modify: `itsm-backend/handlers/change/service_bpmn_test.go`
- Modify: `itsm-backend/service/bpmn_process_engine_ext_test.go`
- Modify: `itsm-backend/service/bpmn_approval_gateway_variable_test.go`
- Modify: `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go`
- Modify: `itsm-backend/service/bpmn_usertask_callback_test.go`
- Modify: `itsm-backend/cmd/backfill_change_work_item/main_test.go`
- Test: `itsm-backend/service/bpmn_authorization_test.go`
- Test: `itsm-backend/controller/bpmn_workflow_authorization_test.go`

**Interfaces:**
- Consumes: shared scope and participation resolver.
- Produces: private `loadTaskByKey(ctx, taskID, tenantID)` and `loadTaskByID(ctx, id, tenantID)` persistence helpers; public reads wrap these loaders with read authorization.
- Preserves: current task read method signatures.
- Produces: participant-safe pagination totals after exact filtering.

- [ ] **Step 1: Write failing task read tests**

```go
func TestListUserTasksForcesCallerScopeWithoutTaskRead(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	mine, _ := f.seedMineAndOtherTasks()
	req := &ListUserTasksRequest{TenantID: f.tenant.ID, UserID: f.otherActor.ID, Assignee: strconv.Itoa(f.otherActor.ID), Page: 1, PageSize: 20}
rows, total, err := f.engine.TaskService().ListUserTasks(f.scopedCtx(false, false, false, false), req)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, mine.TaskID, rows[0].TaskID)
}

func TestGetTaskRejectsSameTenantNonParticipant(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedTaskForOtherActor()
_, err := f.engine.TaskService().GetTask(f.scopedCtx(false, false, false, false), task.TaskID)
	assertAppErrorCode(t, err, common.ErrCodeForbidden)
}
```

Add these named cases with the fixture's real Ent rows:

```text
TestGetTaskRejectsCrossTenant: not found; response/error contains no target tenant data
TestGetTaskAllowsElevatedReader: CanReadAllTasks=true -> success
TestTaskCandidateMatchingDoesNotMatchSubstring: actor 1 cannot read candidate_users="11"
TestGetTaskVariablesAuthorization: unauthorized parent -> forbidden before variables query
TestGetCounterSignStatusAuthorization: unauthorized parent -> forbidden
TestTaskStatisticsAuthorization: participant=forbidden, task reader=success, forged tenant ignored
TestListUserTasksHTTPRejectsFilterOverride: ?userId=<other> returns only caller tasks
```

Statistics require `CanReadAllTasks`; participant scope is insufficient because assignee breakdown exposes other actors. In the forged-tenant case, assert `TotalTasks` and every assignee breakdown entry come only from the trusted tenant.

- [ ] **Step 2: Run tests to verify current overreach**

Run: `cd itsm-backend && go test ./service ./controller -run 'TestListUserTasksForces|TestGetTaskRejects|TestListUserTasksHTTP|TestGetCounterSignStatusAuthorization|TestTaskStatisticsAuthorization' -v`

Expected: FAIL because request filters override caller scope and task reads lack participation checks.

- [ ] **Step 3: Implement the read policy**

All persistence loaders require trusted tenant but perform no policy decision. Public `GetTask`/`GetTaskByID` call those loaders and then allow `CanReadAllTasks` or exact participation. Mutations in Task 6 call the private loaders and apply update policy, so `task:update` does not accidentally require `task:read`. Non-elevated `ListUserTasks` ignores user/assignee/candidate filters, exact-filters the caller's tasks, then computes total and pagination; elevated readers retain filters within their tenant. `GetTaskVariables` and `GetCounterSignStatus` authorize the parent task, and every child query includes trusted tenant. `GetTaskStatistics` requires `CanReadAllTasks` and replaces the request tenant with the trusted tenant. Remove controller-owned “my tasks” defaulting, and make `GetTaskStats` pass `workflowCtx` rather than Gin context.

Update every listed direct test caller of `ListUserTasks` to attach an explicit `BPMNAccessScope`: use the fixture actor for participant assertions and `CanReadAllTasks: true` only for tests that intentionally inspect all generated tasks. This is test-context completion, not a production bypass.

- [ ] **Step 4: Run read tests**

Run: `cd itsm-backend && go test ./service ./controller -run 'TestListUserTasksForces|TestGetTaskRejects|TestListUserTasksHTTP|TestGetCounterSignStatusAuthorization|TestTaskStatisticsAuthorization' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_authorization_test.go itsm-backend/controller/bpmn_workflow_controller.go itsm-backend/controller/bpmn_workflow_authorization_test.go itsm-backend/handlers/change/service_bpmn_test.go itsm-backend/service/bpmn_process_engine_ext_test.go itsm-backend/service/bpmn_approval_gateway_variable_test.go itsm-backend/service/bpmn_process_engine_approval_assignment_test.go itsm-backend/service/bpmn_usertask_callback_test.go itsm-backend/cmd/backfill_change_work_item/main_test.go
git commit -m "fix(bpmn): enforce participant task reads"
```

### Task 5: Transactional Process-Instance Mutations

**Files:**
- Modify: `itsm-backend/service/bpmn_audit_service.go:84-126`
- Modify: `itsm-backend/service/bpmn_process_engine.go:1643-1796`
- Modify: `itsm-backend/service/bpmn_process_engine.go:2257-2273`
- Test: `itsm-backend/service/bpmn_authorization_test.go`

**Interfaces:**
- Produces: `(*BPMNAuditService).ForClient(*ent.Client) *BPMNAuditService`.
- Consumes: `BPMNAccessScope.CanUpdateAllInstances` from `process_instance:update`.
- Preserves: `SuspendProcess`, `ResumeProcess`, `TerminateProcess`, and `SetProcessInstanceVariables` signatures.

- [ ] **Step 1: Write authorization, audit, and rollback tests**

```go
func TestProcessInstanceMutationsRequireUpdatePermission(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.seedRunningInstance(f.actor.ID)
	mutations := map[string]func(context.Context) error{
		"suspend": func(ctx context.Context) error { return f.engine.SuspendProcess(ctx, instance.ProcessInstanceID, "maintenance") },
		"resume": func(ctx context.Context) error { return f.engine.ResumeProcess(ctx, instance.ProcessInstanceID) },
		"terminate": func(ctx context.Context) error { return f.engine.TerminateProcess(ctx, instance.ProcessInstanceID, "cancelled") },
		"variables": func(ctx context.Context) error {
			return f.engine.ProcessInstanceService().SetProcessInstanceVariables(ctx, instance.ProcessInstanceID, map[string]interface{}{"safe": true})
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			assertAppErrorCode(t, mutate(f.scopedCtx(false, false, false, false)), common.ErrCodeForbidden)
		})
	}
}
```

Implement `TestProcessInstanceMutationAuditRollback` with an Ent mutation hook that returns `errors.New("forced audit failure")` only for `*ent.ProcessAuditLogMutation`. Snapshot instance status/variables and child task statuses before the call, assert the mutation returns that error, then re-query and compare every snapshot field. Implement `TestProcessInstanceMutationAuditMetadata` and assert the committed row's actor ID, tenant ID, action, reason, and before/after metadata against the request.

- [ ] **Step 2: Run tests to verify non-atomic behavior**

Run: `cd itsm-backend && go test ./service -run 'TestProcessInstanceMutationsRequire|TestProcessInstanceMutationRollsBack|TestProcessInstanceMutationAudit' -v`

Expected: FAIL because writes only tenant-filter, audits are warning-only, and actor extraction uses `ctx.Value("user")`.

- [ ] **Step 3: Implement permission gates and transaction-bound audit**

```go
func (s *BPMNAuditService) ForClient(client *ent.Client) *BPMNAuditService {
	return &BPMNAuditService{client: client, logger: s.logger}
}
```

Each mutation loads scope, requires `CanUpdateAllInstances`, uses the private tenant-scoped instance loader rather than the public read-authorized method, starts an Ent transaction, performs all state/task updates through `tx.Client()`, writes audit through `auditService.ForClient(tx.Client())`, and commits last. Audit failure must roll back rather than log-and-continue. Resolve actor name from `scope.UserID`. `TerminateProcess` cancels active tasks and writes audit in the same transaction.

- [ ] **Step 4: Run mutation and lifecycle tests**

Run: `cd itsm-backend && go test ./service -run 'TestProcessInstanceMutation|TestSuspend|TestResume|TestTerminate|TestSetProcessInstanceVariables' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn_audit_service.go itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_authorization_test.go
git commit -m "fix(bpmn): audit instance mutations atomically"
```

### Task 6: Transactional Task Mutations and Counter-Sign

**Files:**
- Modify: `itsm-backend/service/bpmn_audit_service.go`
- Modify: `itsm-backend/service/bpmn_process_engine.go:2553-2687`
- Modify: `itsm-backend/service/bpmn_process_engine.go:2859-3135`
- Test: `itsm-backend/service/bpmn_authorization_test.go`

**Interfaces:**
- Produces: audit actions `task_cancelled`, `task_variables_changed`, and `counter_sign_created` through existing `AuditContext`.
- Consumes: `CanUpdateAllTasks` or exact participation.
- Preserves: task mutation and counter-sign public signatures.

- [ ] **Step 1: Write the failing mutation matrix**

```go
func TestTaskMutationsRequireParticipantOrTaskUpdate(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedTaskForOtherActor()
	mutations := map[string]func(context.Context) error{
		"assign": func(ctx context.Context) error { return f.engine.TaskService().AssignTask(ctx, task.TaskID, strconv.Itoa(f.actor.ID)) },
		"cancel": func(ctx context.Context) error { return f.engine.TaskService().CancelTask(ctx, task.TaskID, "invalid") },
		"variables": func(ctx context.Context) error { return f.engine.TaskService().SetTaskVariables(ctx, task.TaskID, map[string]interface{}{"x": 1}) },
		"counter-sign": func(ctx context.Context) error {
			_, err := f.engine.TaskService().CreateCounterSignTasks(ctx, task.TaskID, &CounterSignRequest{Approvers: []string{strconv.Itoa(f.actor.ID)}, ApprovalType: "parallel", Threshold: 1})
			return err
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			assertAppErrorCode(t, mutate(f.scopedCtx(false, false, false, false)), common.ErrCodeForbidden)
		})
	}
}
```

Implement the remaining matrix as named tests so each failure is isolated:

```text
TestTaskParticipantCanMutateOwnTask
TestTaskUpdaterCanMutateOtherTask
TestTaskMutationRejectsCrossTenant
TestTaskMutationAuditRollback
TestCounterSignCreatesChildrenParentAndAuditAtomically
TestVoteWritesDecisionAndCompletesTaskAtomically
```

For both rollback tests, fail `ProcessAuditLog.Create` through an Ent hook and assert no task, parent, child, or approval-decision row changed. Keep the existing Claim/Complete/Vote and KAF delegated authorization tests in the focused command to prevent regressions.

- [ ] **Step 2: Run tests to verify the gaps**

Run: `cd itsm-backend && go test ./service -run 'TestTaskMutationsRequire|TestTaskMutationAuditRollback|TestCounterSignAtomic|Test.*Kaf.*Authorize' -v`

Expected: FAIL because assign/cancel/variables/counter-sign lack a shared policy and several queries omit tenant.

- [ ] **Step 3: Implement one mutation gate**

```go
func (s *bpmnTaskService) authorizeTaskUpdate(ctx context.Context, task *ent.ProcessTask) (BPMNAccessScope, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return BPMNAccessScope{}, err
	}
	if task.TenantID != scope.TenantID {
		return BPMNAccessScope{}, common.NewNotFoundError("process task")
	}
	if scope.CanUpdateAllTasks {
		return scope, nil
	}
	actor, err := s.participation.resolveActor(ctx, scope)
	if err != nil || !s.participation.matchesTask(task, actor) {
		return BPMNAccessScope{}, common.NewForbiddenError("无权操作该流程任务")
	}
	return scope, nil
}
```

Async handlers continue through `authorizeKafAutomationActor`. Every ordinary mutation uses the private tenant-scoped task loader, applies update authorization, then writes state and audit in one transaction. Resolve new assignees in the same tenant. Counter-sign creates all children, updates parent, and audits atomically; remove warning-only parent updates. `Vote` tenant-scopes task/parent/subtask queries and commits decision plus completion atomically.

- [ ] **Step 4: Run mutation, approval, and KAF tests**

Run: `cd itsm-backend && go test ./service -run 'TestTaskMutationsRequire|TestTaskMutationAuditRollback|TestCounterSignAtomic|TestVote|Test.*Kaf|TestCompleteTask|TestClaimTask' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn_audit_service.go itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_authorization_test.go
git commit -m "fix(bpmn): authorize and audit task mutations"
```

### Task 7: HTTP Error Semantics and Route Regression

**Files:**
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go`
- Modify: `itsm-backend/controller/bpmn_workflow_controller_test.go`
- Modify: `itsm-backend/controller/bpmn_workflow_authorization_test.go`

**Interfaces:**
- Produces: `respondBPMNError(*gin.Context, error, string)`.
- Consumes: structured `common.AppError` values from services.

- [ ] **Step 1: Write failing HTTP status tests**

```go
func TestBPMNTaskHTTPAuthorizationStatus(t *testing.T) {
	f := newBPMNHTTPAuthorizationFixture(t)
	task := f.seedTaskForOtherActor()
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/bpmn/tasks/" + task.TaskID, ""},
		{http.MethodPut, "/api/v1/bpmn/tasks/" + task.TaskID + "/assign", `{"assignee":"7"}`},
		{http.MethodPut, "/api/v1/bpmn/tasks/" + task.TaskID + "/cancel", `{"reason":"x"}`},
		{http.MethodPut, "/api/v1/bpmn/tasks/" + task.TaskID + "/variables", `{"variables":{"x":1}}`},
	}
	for _, tc := range cases {
		resp := f.doAsActor(tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, resp.Code, tc.path)
	}
}
```

Add a table beside the task cases with these exact expectations:

```go
cases := []struct{ actor, method, path string; want int }{
	{"participant", http.MethodGet, "/api/v1/bpmn/process-instances/PI-1", http.StatusOK},
	{"outsider", http.MethodGet, "/api/v1/bpmn/process-instances/PI-1", http.StatusForbidden},
	{"other_tenant", http.MethodGet, "/api/v1/bpmn/process-instances/PI-1", http.StatusNotFound},
	{"instance_updater", http.MethodPut, "/api/v1/bpmn/process-instances/PI-1/suspend", http.StatusOK},
	{"participant", http.MethodPut, "/api/v1/bpmn/process-instances/PI-1/suspend", http.StatusForbidden},
	{"participant", http.MethodGet, "/api/v1/bpmn/stats/instances", http.StatusForbidden},
	{"task_reader", http.MethodGet, "/api/v1/bpmn/stats/tasks", http.StatusOK},
	{"participant", http.MethodGet, "/api/v1/workflow/instances", http.StatusForbidden},
	{"instance_reader", http.MethodGet, "/api/v1/workflow/instances", http.StatusOK},
}
```

For every denied case, assert the response body omits tenant IDs, candidate expressions, SQL text, and task variables. The `/bpmn/*` cases prove participant-aware access; the `/workflow/*` cases prove the existing exact RBAC middleware remains stricter and the shared service policy still runs after middleware.

- [ ] **Step 2: Run tests to verify current 500 responses**

Run: `cd itsm-backend && go test ./controller -run 'TestBPMN.*HTTPAuthorization' -v`

Expected: FAIL because handlers currently map service authorization errors to internal errors.

- [ ] **Step 3: Add and apply one structured mapper**

```go
var appErr *common.AppError
if errors.As(err, &appErr) {
	switch appErr.Code {
	case common.ErrCodeForbidden:
		common.Forbidden(ctx, appErr.Message)
	case common.ErrCodeNotFound:
		common.NotFound(ctx, appErr.Message)
	case common.ErrCodeBadRequest, common.ErrCodeValidation:
		common.Fail(ctx, common.ParamErrorCode, appErr.Message)
	case common.ErrCodeConflict:
		common.Fail(ctx, common.ConflictCode, appErr.Message)
	default:
		common.InternalError(ctx, fallback)
	}
	return
}
common.InternalError(ctx, fallback)
```

Use the helper in every S0 process/task handler. Return generic cross-tenant messages; log causes server-side without exposing tenant, candidate, SQL, or variable details. Do not remove or weaken the existing `/workflow/*` `RequirePermission` middleware, and do not add a second handler implementation for either route family.

- [ ] **Step 4: Run BPMN controller tests**

Run: `cd itsm-backend && go test ./controller -run 'TestBPMNWorkflow|TestBPMN.*HTTPAuthorization' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/controller/bpmn_workflow_controller.go itsm-backend/controller/bpmn_workflow_controller_test.go itsm-backend/controller/bpmn_workflow_authorization_test.go
git commit -m "fix(bpmn): return precise authorization responses"
```

### Task 8: Security Matrix and Final Verification

**Files:**
- Modify: `itsm-backend/service/bpmn_authorization_test.go`
- Modify: `itsm-backend/controller/bpmn_workflow_authorization_test.go`
- Modify: `docs/DEVELOPMENT_GUIDE.md`

**Interfaces:**
- Consumes: all S0 behavior.
- Produces: repeatable verification commands and a complete actor/tenant/action matrix.

- [ ] **Step 1: Add the final real-route matrix**

```go
func TestBPMNAuthorizationMatrix(t *testing.T) {
	cases := []struct {
		name, actor, operation string
		wantStatus             int
	}{
		{"participant reads instance", "participant", "get_instance", http.StatusOK},
		{"outsider cannot read instance", "outsider", "get_instance", http.StatusForbidden},
		{"cross tenant cannot read instance", "other_tenant", "get_instance", http.StatusNotFound},
		{"participant reads task", "participant", "get_task", http.StatusOK},
		{"outsider cannot assign", "outsider", "assign_task", http.StatusForbidden},
		{"task updater assigns", "task_updater", "assign_task", http.StatusOK},
		{"instance updater suspends", "instance_updater", "suspend_instance", http.StatusOK},
		{"participant cannot suspend", "participant", "suspend_instance", http.StatusForbidden},
		{"participant cannot read instance aggregates", "participant", "instance_stats", http.StatusForbidden},
		{"task reader reads task aggregates", "task_reader", "task_stats", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runBPMNAuthorizationMatrixCase(t, tc.actor, tc.operation, tc.wantStatus)
		})
	}
}
```

The final matrix must contain one participant, outsider, elevated, and cross-tenant row for each operation identifier below; `runBPMNAuthorizationMatrixCase` must use registered routes and real Ent rows rather than direct handler calls:

```go
operations := []string{
	"list_instances", "get_instance", "approval_history", "instance_variables",
	"instance_history", "instance_stats", "suspend_instance", "resume_instance",
	"terminate_instance", "list_tasks_with_override", "get_task", "task_variables",
	"task_stats", "assign_task", "cancel_task", "set_task_variables",
	"create_counter_sign", "get_counter_sign", "claim_task", "complete_task",
	"submit_decision", "vote",
}
```

Add alias rows proving `/workflow/instances` requires `process_instance:read` and `/workflow/tasks` requires `task:read`, even when the actor is a participant.

- [ ] **Step 2: Run focused, race, middleware, and build checks**

```bash
cd itsm-backend
go test ./service/bpmn ./service ./controller -run 'BPMN|ProcessTask|ProcessInstance|KafDelegate' -count=1
go test -race ./service -run 'TestBPMNAuthorization|TestTaskMutation|TestProcessInstanceMutation|TestCounterSign' -count=1
go test ./middleware ./router -count=1
go build ./...
```

Expected: all commands exit 0. If race support is unavailable, record that limitation and run the same tests with `-count=10`; do not claim race verification.

- [ ] **Step 3: Document the focused verification command**

Add this exact operational subsection without duplicating the architecture rationale from `AGENTS.md`:

````markdown
### BPMN instance authorization

Trusted BPMN scope is built only from authenticated `tenant_id`, `user_id`, role, and RBAC state. Elevated permissions are `process_instance:read`, `process_instance:update`, `task:read`, and `task:update`; request parameters never grant scope.

```bash
go test ./service/bpmn ./service ./controller -run 'BPMN|ProcessTask|ProcessInstance|KafDelegate' -count=1
```
````

- [ ] **Step 4: Run full verification**

```bash
git diff --check
cd itsm-backend && go test ./... -count=1
cd itsm-backend && go build ./...
```

Expected: all commands exit 0. Record unrelated pre-existing failures exactly; do not weaken or delete tests.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn_authorization_test.go itsm-backend/controller/bpmn_workflow_authorization_test.go docs/DEVELOPMENT_GUIDE.md
git commit -m "test(bpmn): lock instance authorization matrix"
```

## Completion Criteria

- Process-instance reads are tenant-safe and participant-scoped unless `process_instance:read` is present.
- Process-instance and task aggregate statistics require their corresponding read-all permission and trusted tenant scope.
- Process-instance mutations require `process_instance:update` and atomically record audit.
- Task reads are tenant-safe and participant-scoped unless `task:read` is present.
- Ordinary task mutations require participation or `task:update` and atomically record audit.
- Candidate matching is exact across ID, username, email, primary/additional roles, and groups.
- Caller filters cannot query another user's tasks without elevated permission.
- KAF delegated authorization and approval decisions remain green.
- HTTP authorization failures return 403/404, not 500, without cross-tenant details.
- `/bpmn/*` and `/workflow/*` share service authorization; the existing stricter RBAC middleware on `/workflow/*` remains intact.
- Focused tests, race verification when supported, full tests, and build pass with recorded evidence.
