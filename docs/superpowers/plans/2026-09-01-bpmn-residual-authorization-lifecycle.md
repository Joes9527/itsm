# BPMN Residual Authorization and Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining BPMN object-authorization, terminal-lifecycle, compare-and-set, and process-start audit gaps on baseline `f11290317499b958ba93d85689286fdccccfe697` without introducing compatibility authorization paths.

**Architecture:** Extract the existing instance participation decision into one reusable service policy, and make process-trigger, dashboard, monitoring, audit, and timeline reads consume that policy through authenticated `BPMNAccessScope`. Add one lifecycle-policy module for process-instance and task commands; every mutation loads and authorizes through the transaction client, updates with tenant plus allowed-source-status predicates, checks `affected == 1`, and writes its success or rejected-terminal audit before committing. Process-start audit identity is resolved from the typed access scope or an explicit trusted trigger actor, never from a string context key.

**Tech Stack:** Go 1.25.12, Gin, Ent, SQLite enttest, PostgreSQL integration tests with `lib/pq`, Testify.

**Spec:** `docs/superpowers/specs/2026-09-01-architecture-hardening-agent-platform-evolution-design.md` (Phase 1, section 5.2; Phase 1 gates; Wave 1A Agent B).

## Global Constraints

- Baseline is exactly `f11290317499b958ba93d85689286fdccccfe697`; record execution HEAD before implementation and stop if the target files have diverged without an integration-owner decision.
- P1-C exclusively owns `itsm-backend/service/bpmn_process_engine.go` during Wave 1A. P1-D must not edit that file until P1-C is integrated.
- P1-C owns BPMN object authorization, process/task lifecycle policy, mutation CAS, and process-start actor attribution. P1-D owns callback effect semantics and notification contract; this plan does not alter callback result semantics.
- Tenant ID, user ID, role, and elevated flags come only from authenticated context. HTTP query, path, and body values never construct tenant or elevated scope.
- `WithTrustedBPMNTenantContext` remains valid only for application-service starts whose tenant came from an authoritative domain record. It is not accepted by HTTP read or mutation endpoints as a substitute for `BPMNAccessScope`.
- Participant matching remains token-exact through the existing `bpmnParticipationResolver`; do not add substring matching, role inference, or a second candidate resolver.
- Participant object reads are allowed; tenant-wide audit, metrics, monitoring, SLA, activity, health, and status-list queries require `CanReadAllInstances`.
- Process-instance cancel, suspend, and resume require `CanUpdateAllInstances`. Task commands retain participant-or-`CanUpdateAllTasks` authorization and add lifecycle enforcement.
- Cross-tenant object access fails closed without variables, candidate expressions, tenant identifiers, or SQL details in the response.
- State mutation and success audit commit in one Ent transaction. A rejected terminal command commits a rejection audit without changing the task.
- Preserve KAF task-type, delegated-status, actor-role, tenant, idempotent-completion, receipt, and recovery rules.
- Preserve the durable callback outbox and the existing `completeAuthorizedTaskWithClient` callback-enqueue boundary. P1-D consumes this boundary and must not add a direct `ProcessTask` status update.
- Unknown lifecycle commands and statuses fail closed with a typed conflict or validation error.
- Do not add deprecated aliases, dual signatures, tenant-parameter fallbacks, bridge services, role-name authorization, or silent empty-result fallbacks.
- Use TDD. Each task ends with its focused tests, deletion scan, and a small commit before the next task begins.

## Ownership, Dependencies, Deletions, and Evidence

| Contract | Detail |
|---|---|
| `owns` | BPMN residual HTTP object policy, audit/timeline policy, process/task lifecycle predicates, mutation CAS, terminal rejection audit, start actor audit. |
| `depends_on` | Existing `BPMNAccessScope`, `bpmnParticipationResolver`, transactional `BPMNAuditService.ForClient`, and current KAF/callback tests at `f1129031`. |
| `deletes` | `processTriggerTenant`; `resolveProcessInstanceBusinessID`; tenant arguments on process-trigger status/cancel/suspend/resume; dashboard-local `resolveTenantID`; parameterless dashboard timeline route; legacy role gate as the authorization decision for dashboard/monitoring/object process-trigger routes; `ctx.Value("user")` process-start audit fallback; per-method terminal `StatusNEQ` fragments replaced by the lifecycle policy. |
| `evidence` | HTTP actor matrix, service object-policy tests, task/process lifecycle unit tests, SQLite rollback tests, real PostgreSQL CAS races, KAF/callback regression tests, full backend test/race/build, route and legacy-symbol scans, clean diff. |

## File Structure

| File | Responsibility |
|---|---|
| `itsm-backend/service/bpmn_instance_access.go` | Single instance load/read/read-all/update-all policy shared by process-instance, trigger, audit, and monitoring services. |
| `itsm-backend/service/bpmn_instance_access_test.go` | Initiator/participant/elevated/outsider/cross-tenant policy tests. |
| `itsm-backend/service/bpmn_lifecycle_policy.go` | Process-instance and task command types, allowed source states, validators, and Ent predicates. |
| `itsm-backend/service/bpmn_lifecycle_policy_test.go` | Complete command/status truth tables and unknown-command fail-closed tests. |
| `itsm-backend/service/bpmn_process_engine.go` | Wave 1A integration owner: route all process/task mutations through policy, CAS, transaction-bound authorization/audit, and typed start actor resolution. |
| `itsm-backend/service/bpmn_process_trigger_service.go` | Use the authoritative process-instance service directly; expose no caller-supplied tenant arguments. |
| `itsm-backend/service/bpmn_audit_service.go` | Scope audit/timeline/activity reads and record rejected terminal task commands. |
| `itsm-backend/service/bpmn_monitoring_service.go` | Enforce object policy for instance status/history/timeline and read-all for aggregate monitoring. |
| `itsm-backend/controller/bpmn_process_trigger_controller.go` | Build trusted scope and call the reduced process-trigger signatures. |
| `itsm-backend/controller/bpmn_dashboard_controller.go` | Build trusted scope for every route, require read-all for aggregate routes, and expose a valid object timeline path. |
| `itsm-backend/controller/bpmn_monitoring_controller.go` | Replace tenant-only/legacy-role authorization with the same trusted scope and structured BPMN errors. |
| `itsm-backend/controller/bpmn_process_trigger_mutation_authorization_test.go` | Full process-trigger status/mutation actor matrix and lifecycle responses. |
| `itsm-backend/controller/bpmn_dashboard_controller_test.go` | Dashboard audit/timeline/aggregate authorization matrix and safe denial bodies. |
| `itsm-backend/controller/bpmn_monitoring_authorization_test.go` | Monitoring status/timeline/list/aggregate authorization matrix. |
| `itsm-backend/service/bpmn_task_terminal_mutation_test.go` | Terminal rejection, rejection-audit, rollback, and no-state-change service tests. |
| `itsm-backend/service/bpmn_task_lifecycle_integration_test.go` | Real PostgreSQL concurrent assign/cancel/complete/variable CAS proof. |
| `itsm-backend/service/bpmn_start_audit_actor_test.go` | HTTP-scope, trusted-trigger, inactive, wrong-tenant, and rollback start-audit tests. |
| `itsm-backend/handlers/change/service.go` | Adopt reduced `CancelProcess` signature; retain explicit update-all actor scope. |
| `itsm-backend/handlers/change/service_bpmn_test.go` | Verify compensation cancellation after signature deletion. |
| `itsm-backend/service/ticket_service.go` | Adopt reduced `CancelProcess` signature. |
| `itsm-backend/service/bpmn_process_trigger_service_test.go` | Trusted trigger and reduced-signature regression coverage. |

## Frozen Interfaces

The following signatures are the integration contract for this subproject:

```go
type BPMNProcessCommand string

const (
	BPMNProcessCommandSuspend   BPMNProcessCommand = "suspend"
	BPMNProcessCommandResume    BPMNProcessCommand = "resume"
	BPMNProcessCommandTerminate BPMNProcessCommand = "terminate"
)

type BPMNTaskCommand string

const (
	BPMNTaskCommandAssign            BPMNTaskCommand = "assign"
	BPMNTaskCommandClaim             BPMNTaskCommand = "claim"
	BPMNTaskCommandComplete          BPMNTaskCommand = "complete"
	BPMNTaskCommandCancel            BPMNTaskCommand = "cancel"
	BPMNTaskCommandDelegate          BPMNTaskCommand = "delegate"
	BPMNTaskCommandSetVariables      BPMNTaskCommand = "set_variables"
	BPMNTaskCommandCreateCounterSign BPMNTaskCommand = "create_counter_sign"
	BPMNTaskCommandVote              BPMNTaskCommand = "vote"
)

func ValidateBPMNProcessLifecycle(command BPMNProcessCommand, status string) error
func ValidateBPMNTaskLifecycle(command BPMNTaskCommand, status string) error
func RequireBPMNInstanceReadAll(ctx context.Context) (BPMNAccessScope, error)

func (s *ProcessTriggerService) GetProcessStatus(ctx context.Context, processInstanceID int) (*dto.ProcessTriggerResponse, error)
func (s *ProcessTriggerService) CancelProcess(ctx context.Context, processInstanceID int, reason string) error
func (s *ProcessTriggerService) SuspendProcess(ctx context.Context, processInstanceID int, reason string) error
func (s *ProcessTriggerService) ResumeProcess(ctx context.Context, processInstanceID int) error

func (s *BPMNAuditService) QueryAuditLogs(ctx context.Context, req *QueryAuditLogsRequest) ([]*ent.ProcessAuditLog, int, error)
func (s *BPMNAuditService) GetProcessTimeline(ctx context.Context, processInstanceKey string) ([]*ent.ProcessAuditLog, error)
func (s *BPMNAuditService) GetUserActivity(ctx context.Context, userID int, startTime, endTime time.Time) ([]*ent.ProcessAuditLog, error)

func (s *BPMNMonitoringService) GetProcessInstanceStatus(ctx context.Context, processInstanceID int) (*ProcessInstanceStatus, error)
func (s *BPMNMonitoringService) GetProcessInstanceHistory(ctx context.Context, processInstanceID int) ([]*ent.ProcessExecutionHistory, error)
func (s *BPMNMonitoringService) GetProcessTimeline(ctx context.Context, processInstanceKey string) ([]*ProcessTimelineEntry, error)
func (s *BPMNMonitoringService) GetSystemHealth(ctx context.Context) (map[string]interface{}, error)
func (s *BPMNMonitoringService) GetPerformanceAlerts(ctx context.Context) ([]map[string]interface{}, error)
```

`ProcessEngine`, `ProcessInstanceService`, `TaskService`, and `completeAuthorizedTaskWithClient` retain their current public/internal signatures. P1-D may call the existing completion/callback boundary but may not set task status or bypass `ValidateBPMNTaskLifecycle`.

---

### Task 1: Extract the Single Process-Instance Access Policy

**Files:**
- Create: `itsm-backend/service/bpmn_instance_access.go`
- Create: `itsm-backend/service/bpmn_instance_access_test.go`
- Modify: `itsm-backend/service/bpmn_access_scope.go`
- Modify: `itsm-backend/service/bpmn_process_engine.go:2678-2810`

**Interfaces:**
- Consumes: `BPMNAccessScopeFromContext(context.Context)` and `bpmnParticipationResolver`.
- Produces: `newBPMNInstanceAccessPolicy(*ent.Client, *bpmnParticipationResolver) *bpmnInstanceAccessPolicy`.
- Produces: `(*bpmnInstanceAccessPolicy).loadForRead(context.Context, string) (*ent.ProcessInstance, error)`.
- Produces: `(*bpmnInstanceAccessPolicy).loadForUpdate(context.Context, string) (*ent.ProcessInstance, error)`.
- Produces: `(*bpmnInstanceAccessPolicy).authorizedInstanceIDs(context.Context) ([]int, error)`.
- Produces: `(*bpmnInstanceAccessPolicy).forClient(*ent.Client) *bpmnInstanceAccessPolicy` for transaction-bound authorization.
- Produces: `RequireBPMNInstanceReadAll(context.Context) (BPMNAccessScope, error)`.

- [ ] **Step 1: Write failing policy tests**

```go
func TestBPMNInstanceAccessPolicyReadMatrix(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	policy := newBPMNInstanceAccessPolicy(f.client, f.resolver)
	instance := f.createAuthorizedReadInstance(t)

	_, err := policy.loadForRead(f.scopedCtx(false, false, false, false), instance.ProcessInstanceID)
	require.NoError(t, err)

	_, err = policy.loadForRead(f.actorScopeCtx(f.outsider, f.tenant, false), instance.ProcessInstanceID)
	requireBPMNForbidden(t, err)

	_, err = policy.loadForRead(f.actorScopeCtx(f.otherActor, f.otherTenant, true), instance.ProcessInstanceID)
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), f.tenant.Code)

	_, err = policy.loadForRead(f.scopedCtx(true, false, false, false), instance.ProcessInstanceID)
	require.NoError(t, err)
}

func TestRequireBPMNInstanceReadAllRejectsParticipantScope(t *testing.T) {
	_, err := RequireBPMNInstanceReadAll(WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID: 1, TenantID: 1,
	}))
	requireBPMNForbidden(t, err)
}
```

Use the existing authorization fixture identities; do not create a second participant matching fixture.

- [ ] **Step 2: Run tests and confirm failure**

Run: `cd itsm-backend && go test ./service -run 'TestBPMNInstanceAccessPolicy|TestRequireBPMNInstanceReadAll' -count=1 -v`

Expected: FAIL because `bpmnInstanceAccessPolicy` and `RequireBPMNInstanceReadAll` do not exist.

- [ ] **Step 3: Implement the focused policy**

```go
type bpmnInstanceAccessPolicy struct {
	client                *ent.Client
	participationResolver *bpmnParticipationResolver
}

func RequireBPMNInstanceReadAll(ctx context.Context) (BPMNAccessScope, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return BPMNAccessScope{}, err
	}
	if !scope.CanReadAllInstances {
		return BPMNAccessScope{}, common.NewForbiddenError("无权读取流程实例汇总数据")
	}
	return scope, nil
}
```

Implement one tenant-scoped loader that accepts the existing numeric database ID or `ProcessInstanceID` string. `loadForRead` calls that loader and authorizes initiator, exact participant, or read-all. `loadForUpdate` requires `CanUpdateAllInstances` before the tenant-scoped load. `authorizedInstanceIDs` returns all same-tenant IDs only for read-all; otherwise it returns the deduplicated union of initiator IDs and exact participant IDs.

Replace `bpmnProcessInstanceService.loadProcessInstance` and `authorizeProcessInstanceRead` logic with delegation to this policy. Its `forClient` rebuilds both the policy and `bpmnParticipationResolver` on the transaction client. `BPMNAuditService.ForClient` and `CustomProcessEngine.forClient` must carry that transaction-bound policy. Keep `ProcessInstanceService.GetProcessInstance` unchanged so current controllers and P1-D retain one object-read entry point.

- [ ] **Step 4: Run policy and existing authorization tests**

Run: `cd itsm-backend && go test ./service -run 'TestBPMNInstanceAccessPolicy|TestRequireBPMNInstanceReadAll|TestParticipationResolver|TestProcessInstance' -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Scan for duplicate participant authorization**

Run: `rg -n 'authorizeProcessInstanceRead|participatingInstanceIDs|Initiator\(strconv\.Itoa\(scope\.UserID\)\)' itsm-backend/service`

Expected: participant-instance authorization is implemented only in `bpmn_instance_access.go`; `bpmn_participation.go` remains the sole identity matcher.

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/service/bpmn_access_scope.go itsm-backend/service/bpmn_instance_access.go itsm-backend/service/bpmn_instance_access_test.go itsm-backend/service/bpmn_process_engine.go
git commit -m "refactor(bpmn): centralize instance access policy"
```

### Task 2: Route Process-Trigger Status and Mutations Through the Authoritative Policy

**Files:**
- Modify: `itsm-backend/service/bpmn_process_trigger_service.go:210-313`
- Modify: `itsm-backend/controller/bpmn_process_trigger_controller.go:30-200`
- Modify: `itsm-backend/controller/bpmn_process_trigger_mutation_authorization_test.go`
- Modify: `itsm-backend/service/bpmn_process_trigger_service_test.go`
- Modify: `itsm-backend/handlers/change/service.go`
- Modify: `itsm-backend/handlers/change/service_bpmn_test.go`
- Modify: `itsm-backend/service/ticket_service.go`

**Interfaces:**
- Consumes: `ProcessInstanceService.GetProcessInstance(ctx, strconv.Itoa(id))` for participant-aware status.
- Consumes: existing `ProcessEngine.SuspendProcess`, `ResumeProcess`, and `TerminateProcess`; their loader accepts numeric ID strings.
- Produces: the four reduced `ProcessTriggerService` signatures frozen above.
- Produces: `(*ProcessTriggerService).toProcessTriggerResponse(context.Context, *ent.ProcessInstance) (*dto.ProcessTriggerResponse, error)`.
- Deletes: `processTriggerTenant`, `resolveProcessInstanceBusinessID`, and all four caller-supplied tenant arguments.

- [ ] **Step 1: Extend the HTTP fixture with participant, outsider, elevated, and cross-tenant actors**

```go
func TestBPMNProcessTriggerStatusAuthorizationMatrix(t *testing.T) {
	f := newProcessTriggerAuthorizationFixture(t)
	cases := []struct {
		actor string
		want  int
	}{
		{actor: "participant", want: http.StatusOK},
		{actor: "outsider", want: http.StatusForbidden},
		{actor: "elevated", want: http.StatusOK},
		{actor: "cross_tenant", want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.actor, func(t *testing.T) {
			response := f.do(tc.actor, http.MethodGet, fmt.Sprintf("/api/v1/process-trigger/status/%d", f.instance.ID), "")
			require.Equal(t, tc.want, response.Code, response.Body.String())
			if tc.want != http.StatusOK {
				assertBPMNDenialBodyIsSafe(t, response, "privateVariable", f.tenant.Code)
			}
		})
	}
}
```

Add a mutation table for cancel/suspend/resume with participant and outsider expecting 403, elevated expecting 200, and cross-tenant expecting 404. Assert denied operations leave instance status and audit count unchanged.

- [ ] **Step 2: Run the process-trigger tests and confirm the status leak**

Run: `cd itsm-backend && go test ./controller -run 'TestBPMNProcessTrigger(StatusAuthorizationMatrix|MutationAuthorizationMatrix)' -count=1 -v`

Expected: FAIL because a same-tenant outsider can read status and because current signatures still accept a tenant argument.

- [ ] **Step 3: Delete the legacy translation/signature path**

Implement the reduced methods exactly:

```go
func (s *ProcessTriggerService) GetProcessStatus(ctx context.Context, id int) (*dto.ProcessTriggerResponse, error) {
	instance, err := s.processEngine.ProcessInstanceService().GetProcessInstance(ctx, strconv.Itoa(id))
	if err != nil {
		return nil, err
	}
	return s.toProcessTriggerResponse(ctx, instance)
}

func (s *ProcessTriggerService) CancelProcess(ctx context.Context, id int, reason string) error {
	return s.processEngine.TerminateProcess(ctx, strconv.Itoa(id), reason)
}

func (s *ProcessTriggerService) SuspendProcess(ctx context.Context, id int, reason string) error {
	return s.processEngine.SuspendProcess(ctx, strconv.Itoa(id), reason)
}

func (s *ProcessTriggerService) ResumeProcess(ctx context.Context, id int) error {
	return s.processEngine.ResumeProcess(ctx, strconv.Itoa(id))
}
```

Move response mapping into `toProcessTriggerResponse`; its process-definition lookup must include `instance.TenantID` and propagate database errors except `ent.IsNotFound`, where the definition key remains the display name.

Split route registration so `POST /process-trigger` keeps its current coarse creation gate, while status/cancel/suspend/resume do not depend on `RequireLegacyBPMNRoles`; the service scope is authoritative. Every handler calls `getBPMNTenantContext` and the reduced signature.

Update Change and Ticket callers to remove `tenantID` while preserving their explicit `WithBPMNAccessScope(...CanUpdateAllInstances: true)` context. Do not add an overload retaining the old signature.

- [ ] **Step 4: Run service, controller, Change, and Ticket tests**

Run: `cd itsm-backend && go test ./service ./controller ./handlers/change -run 'TestBPMNProcessTrigger|Test.*Cancel.*Process|Test.*Compensat' -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Prove old signatures and helpers are gone**

Run: `rg -n 'processTriggerTenant|resolveProcessInstanceBusinessID|CancelProcess\([^\n]+tenantID\)|SuspendProcess\([^\n]+tenantID\)|ResumeProcess\([^\n]+tenantID\)|GetProcessStatus\([^\n]+tenantID\)' itsm-backend`

Expected: no matches.

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/service/bpmn_process_trigger_service.go itsm-backend/controller/bpmn_process_trigger_controller.go itsm-backend/controller/bpmn_process_trigger_mutation_authorization_test.go itsm-backend/service/bpmn_process_trigger_service_test.go itsm-backend/handlers/change/service.go itsm-backend/handlers/change/service_bpmn_test.go itsm-backend/service/ticket_service.go
git commit -m "fix(bpmn): authorize process trigger objects"
```

### Task 3: Authorize Dashboard, Audit, Timeline, and Monitoring Reads

**Files:**
- Modify: `itsm-backend/service/bpmn_audit_service.go:319-430`
- Modify: `itsm-backend/service/bpmn_monitoring_service.go:360-540,1084-1280`
- Modify: `itsm-backend/controller/bpmn_dashboard_controller.go`
- Modify: `itsm-backend/controller/bpmn_dashboard_controller_test.go`
- Modify: `itsm-backend/controller/bpmn_monitoring_controller.go`
- Create: `itsm-backend/controller/bpmn_monitoring_authorization_test.go`

**Interfaces:**
- Consumes: `bpmnInstanceAccessPolicy.loadForRead`, `authorizedInstanceIDs`, and `RequireBPMNInstanceReadAll`.
- Produces: tenant-free `BPMNAuditService.GetProcessTimeline` and `GetUserActivity` signatures frozen above.
- Produces: the tenant-free monitoring object/health/alert signatures frozen above.
- Preserves: response DTO shapes for successful dashboard and monitoring requests.
- Deletes: `TenantID` from `QueryAuditLogsRequest`, `AuditLogRequest`, and `ListProcessInstanceStatusQuery`; dashboard-local `resolveTenantID`; tenant parameters from monitoring object/health/alert methods; the parameterless timeline route; and empty-success behavior when monitoring has no audit service.

- [ ] **Step 1: Replace the dashboard role-gate test with an authorization matrix**

```go
func TestBPMNDashboardAuthorizationMatrix(t *testing.T) {
	f := newBPMNDashboardAuthorizationFixture(t)
	cases := []struct {
		actor, path string
		want        int
	}{
		{"participant", "/api/v1/bpmn/dashboard/audit-logs?process_instance_id=" + strconv.Itoa(f.instance.ID), http.StatusOK},
		{"outsider", "/api/v1/bpmn/dashboard/audit-logs?process_instance_id=" + strconv.Itoa(f.instance.ID), http.StatusForbidden},
		{"participant", "/api/v1/bpmn/dashboard/audit-logs/timeline/" + f.instance.ProcessInstanceID, http.StatusOK},
		{"outsider", "/api/v1/bpmn/dashboard/audit-logs/timeline/" + f.instance.ProcessInstanceID, http.StatusForbidden},
		{"participant", "/api/v1/bpmn/dashboard/metrics", http.StatusForbidden},
		{"elevated", "/api/v1/bpmn/dashboard/metrics", http.StatusOK},
		{"cross_tenant", "/api/v1/bpmn/dashboard/audit-logs?process_instance_id=" + strconv.Itoa(f.instance.ID), http.StatusNotFound},
	}
	for _, tc := range cases {
		response := f.get(tc.actor, tc.path)
		require.Equal(t, tc.want, response.Code, response.Body.String())
	}
}
```

Seed `VariablesBefore` and `VariablesAfter` with `audit-variable-secret` and assert every denial body excludes it.

- [ ] **Step 2: Add monitoring route tests before implementation**

```go
func TestBPMNMonitoringObjectAndAggregateAuthorization(t *testing.T) {
	f := newBPMNMonitoringAuthorizationFixture(t)
	assert.Equal(t, http.StatusOK, f.get("participant", fmt.Sprintf("/api/v1/bpmn/monitoring/instances/%d/status", f.instance.ID)).Code)
	assert.Equal(t, http.StatusForbidden, f.get("outsider", fmt.Sprintf("/api/v1/bpmn/monitoring/instances/%d/status", f.instance.ID)).Code)
	assert.Equal(t, http.StatusOK, f.get("participant", "/api/v1/bpmn/monitoring/instances/"+f.instance.ProcessInstanceID+"/timeline").Code)
	assert.Equal(t, http.StatusForbidden, f.get("participant", "/api/v1/bpmn/monitoring/metrics").Code)
	assert.Equal(t, http.StatusOK, f.get("elevated", "/api/v1/bpmn/monitoring/metrics").Code)
}
```

Also cover status lists, performance, alerts, health, and unscoped audit logs: participant 403, elevated 200, cross-tenant target 404.

- [ ] **Step 3: Run both controller packages and confirm failures**

Run: `cd itsm-backend && go test ./controller -run 'TestBPMN(DashboardAuthorizationMatrix|MonitoringObjectAndAggregateAuthorization)' -count=1 -v`

Expected: FAIL because the endpoints currently use tenant-only filtering and a legacy role allowlist.

- [ ] **Step 4: Scope audit queries in the audit service**

Apply these rules in `QueryAuditLogs`:

```go
scope, err := BPMNAccessScopeFromContext(ctx)
if err != nil {
	return nil, 0, err
}
switch {
case req.ProcessInstanceID > 0:
	if _, err := s.instanceAccess.loadForRead(ctx, strconv.Itoa(req.ProcessInstanceID)); err != nil {
		return nil, 0, err
	}
case req.ProcessInstanceKey != "":
	if _, err := s.instanceAccess.loadForRead(ctx, req.ProcessInstanceKey); err != nil {
		return nil, 0, err
	}
default:
	if _, err := RequireBPMNInstanceReadAll(ctx); err != nil {
		return nil, 0, err
	}
}
```

Always include `processauditlog.TenantID(scope.TenantID)`. `GetProcessTimeline` loads and authorizes the instance, then queries by both tenant and authoritative `ProcessInstanceID`. `GetUserActivity` requires read-all and derives tenant from scope. Monitoring object methods have no tenant argument and derive the target tenant through `loadForRead`; monitoring aggregate methods require read-all and use `scope.TenantID`. Missing `auditService` in monitoring returns an explicit internal/configuration error, never an empty successful list.

- [ ] **Step 5: Replace controller tenant extraction and fix the timeline route**

Register `GET /bpmn/dashboard/audit-logs/timeline/:process_instance_key`; delete the parameterless route. Remove `dashboard.Use(RequireLegacyBPMNRoles())` and `resolveTenantID`. Each dashboard handler obtains `workflowCtx` through `getBPMNTenantContext`; aggregate handlers call `RequireBPMNInstanceReadAll(workflowCtx)` before their existing service. Audit/timeline handlers pass `workflowCtx` to `BPMNAuditService`, which performs object authorization.

Apply the same scope construction to every monitoring handler. Object status/history/timeline methods call the instance policy. Status lists use `authorizedInstanceIDs` for participants or all tenant IDs for read-all. Metrics, performance, alerts, health, and unscoped audit call `RequireBPMNInstanceReadAll`. Route errors use `respondBPMNError` so forbidden/not-found responses do not become 500.

- [ ] **Step 6: Run controller and service tests**

Run: `cd itsm-backend && go test ./controller ./service -run 'TestBPMN(Dashboard|Monitoring|Audit|ProcessTimeline|InstanceAccess)' -count=1 -v`

Expected: PASS.

- [ ] **Step 7: Prove active routes no longer use tenant-only authorization**

Run: `rg -n 'resolveTenantID\(|RequireLegacyBPMNRoles\(\)|GetProcessTimeline\([^\n]+tenantID|GetUserActivity\([^\n]+tenantID' itsm-backend/controller/bpmn_dashboard_controller.go itsm-backend/controller/bpmn_monitoring_controller.go itsm-backend/service/bpmn_audit_service.go itsm-backend/service/bpmn_monitoring_service.go`

Expected: no matches.

- [ ] **Step 8: Commit**

```bash
git add itsm-backend/service/bpmn_audit_service.go itsm-backend/service/bpmn_monitoring_service.go itsm-backend/controller/bpmn_dashboard_controller.go itsm-backend/controller/bpmn_dashboard_controller_test.go itsm-backend/controller/bpmn_monitoring_controller.go itsm-backend/controller/bpmn_monitoring_authorization_test.go
git commit -m "fix(bpmn): authorize dashboard and audit reads"
```

### Task 4: Define the Central Process and Task Lifecycle Policies

**Files:**
- Create: `itsm-backend/service/bpmn_lifecycle_policy.go`
- Create: `itsm-backend/service/bpmn_lifecycle_policy_test.go`

**Interfaces:**
- Produces: the exported command types, constants, and validators frozen above.
- Produces: private `bpmnProcessLifecyclePredicate(BPMNProcessCommand, int) (predicate.ProcessInstance, error)`, where the integer is the observed `ProcessInstance.Version`.
- Produces: private `bpmnTaskLifecyclePredicate(BPMNTaskCommand, int) (predicate.ProcessTask, error)`, where the integer is the observed `ProcessTask.AggregationVersion` used as the existing row CAS counter.
- Produces: private `bpmnProcessLifecycleConflict(BPMNProcessCommand) error` and `bpmnTaskLifecycleConflict(BPMNTaskCommand) error`.

- [ ] **Step 1: Write the full process truth table**

```go
func TestValidateBPMNProcessLifecycle(t *testing.T) {
	tests := []struct {
		command BPMNProcessCommand
		status  string
		allowed bool
	}{
		{BPMNProcessCommandSuspend, "running", true},
		{BPMNProcessCommandSuspend, "suspended", false},
		{BPMNProcessCommandSuspend, "completed", false},
		{BPMNProcessCommandSuspend, "terminated", false},
		{BPMNProcessCommandResume, "suspended", true},
		{BPMNProcessCommandResume, "running", false},
		{BPMNProcessCommandResume, "completed", false},
		{BPMNProcessCommandResume, "terminated", false},
		{BPMNProcessCommandTerminate, "running", true},
		{BPMNProcessCommandTerminate, "suspended", true},
		{BPMNProcessCommandTerminate, "completed", false},
		{BPMNProcessCommandTerminate, "terminated", false},
	}
	for _, tc := range tests {
		err := ValidateBPMNProcessLifecycle(tc.command, tc.status)
		assert.Equal(t, tc.allowed, err == nil, "%s from %s", tc.command, tc.status)
	}
}
```

- [ ] **Step 2: Write the full task truth table**

```go
func TestValidateBPMNTaskLifecycle(t *testing.T) {
	active := []string{
		common.ProcessTaskStatusCreated,
		common.ProcessTaskStatusAssigned,
		common.ProcessTaskStatusStarted,
		common.ProcessTaskStatusDelegated,
	}
	activeCommands := []BPMNTaskCommand{
		BPMNTaskCommandAssign,
		BPMNTaskCommandComplete,
		BPMNTaskCommandCancel,
		BPMNTaskCommandDelegate,
		BPMNTaskCommandSetVariables,
		BPMNTaskCommandCreateCounterSign,
	}
	for _, command := range activeCommands {
		for _, status := range active {
			require.NoError(t, ValidateBPMNTaskLifecycle(command, status), "%s from %s", command, status)
		}
		assert.Error(t, ValidateBPMNTaskLifecycle(command, common.ProcessTaskStatusCompleted))
		assert.Error(t, ValidateBPMNTaskLifecycle(command, common.ProcessTaskStatusCancelled))
	}
	require.NoError(t, ValidateBPMNTaskLifecycle(BPMNTaskCommandClaim, common.ProcessTaskStatusCreated))
	assert.Error(t, ValidateBPMNTaskLifecycle(BPMNTaskCommandClaim, common.ProcessTaskStatusAssigned))
	require.NoError(t, ValidateBPMNTaskLifecycle(BPMNTaskCommandVote, common.ProcessTaskStatusAssigned))
	assert.Error(t, ValidateBPMNTaskLifecycle(BPMNTaskCommandVote, common.ProcessTaskStatusCreated))
	assert.Error(t, ValidateBPMNTaskLifecycle(BPMNTaskCommand("unknown"), common.ProcessTaskStatusCreated))
}
```

- [ ] **Step 3: Run tests and confirm failure**

Run: `cd itsm-backend && go test ./service -run 'TestValidateBPMN(Process|Task)Lifecycle' -count=1 -v`

Expected: FAIL because the lifecycle module does not exist.

- [ ] **Step 4: Implement validators and predicates from one status map**

```go
var bpmnTaskAllowedSourceStatuses = map[BPMNTaskCommand][]string{
	BPMNTaskCommandAssign:            {common.ProcessTaskStatusCreated, common.ProcessTaskStatusAssigned, common.ProcessTaskStatusStarted, common.ProcessTaskStatusDelegated},
	BPMNTaskCommandClaim:             {common.ProcessTaskStatusCreated},
	BPMNTaskCommandComplete:          {common.ProcessTaskStatusCreated, common.ProcessTaskStatusAssigned, common.ProcessTaskStatusStarted, common.ProcessTaskStatusDelegated},
	BPMNTaskCommandCancel:            {common.ProcessTaskStatusCreated, common.ProcessTaskStatusAssigned, common.ProcessTaskStatusStarted, common.ProcessTaskStatusDelegated},
	BPMNTaskCommandDelegate:          {common.ProcessTaskStatusCreated, common.ProcessTaskStatusAssigned, common.ProcessTaskStatusStarted, common.ProcessTaskStatusDelegated},
	BPMNTaskCommandSetVariables:      {common.ProcessTaskStatusCreated, common.ProcessTaskStatusAssigned, common.ProcessTaskStatusStarted, common.ProcessTaskStatusDelegated},
	BPMNTaskCommandCreateCounterSign: {common.ProcessTaskStatusCreated, common.ProcessTaskStatusAssigned, common.ProcessTaskStatusStarted, common.ProcessTaskStatusDelegated},
	BPMNTaskCommandVote:              {common.ProcessTaskStatusAssigned},
}
```

The process map is suspend=`running`, resume=`suspended`, terminate=`running|suspended`. Validators normalize neither unknown commands nor unknown persisted statuses; both return explicit errors. Predicates are generated from these same maps with `StatusIn`, plus the observed row version. Task commands use the existing `aggregation_version` integer as their one optimistic concurrency counter and increment it on every task mutation; do not add a second version field during Wave 1A. Process commands predicate on and increment `ProcessInstance.Version`.

- [ ] **Step 5: Run lifecycle tests**

Run: `cd itsm-backend && go test ./service -run 'TestValidateBPMN(Process|Task)Lifecycle|TestBPMN.*LifecyclePredicate' -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/service/bpmn_lifecycle_policy.go itsm-backend/service/bpmn_lifecycle_policy_test.go
git commit -m "feat(bpmn): define lifecycle policies"
```

### Task 5: Apply Lifecycle CAS to Process and Task Mutations

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:400-500,2100-2275,3281-3975`
- Modify: `itsm-backend/service/bpmn_audit_service.go`
- Create: `itsm-backend/service/bpmn_task_terminal_mutation_test.go`
- Create: `itsm-backend/service/bpmn_task_lifecycle_integration_test.go`
- Modify: `itsm-backend/service/bpmn_authorization_test.go`
- Modify: `itsm-backend/service/bpmn_final_fix_test.go`
- Modify: `itsm-backend/service/bpmn_counter_sign_transaction_test.go`
- Modify: `itsm-backend/service/bpmn_claim_cas_integration_test.go`

**Interfaces:**
- Consumes: both lifecycle predicates from Task 4.
- Produces: `AuditActionTaskMutationRejected = "task_mutation_rejected"`.
- Produces the following exact rejection-audit signature:

```go
func (s *BPMNAuditService) RecordTaskMutationRejected(
	ctx context.Context,
	task *ent.ProcessTask,
	actorID int,
	actorName string,
	command BPMNTaskCommand,
	currentStatus string,
) error
```

- Preserves: `completeAuthorizedTaskWithClient` as the single task-completion/token/callback integration boundary.

- [ ] **Step 1: Write terminal mutation table tests**

```go
func TestBPMNTaskTerminalMutationsAreRejectedAndAudited(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	commands := []struct {
		name string
		run  func(context.Context, *ent.ProcessTask) error
	}{
		{"assign", func(ctx context.Context, task *ent.ProcessTask) error { return f.engine.TaskService().AssignTask(ctx, task.TaskID, strconv.Itoa(f.outsider.ID)) }},
		{"claim", func(ctx context.Context, task *ent.ProcessTask) error { return f.engine.TaskService().ClaimTask(ctx, task.TaskID, strconv.Itoa(f.actor.ID)) }},
		{"complete", func(ctx context.Context, task *ent.ProcessTask) error { return f.engine.TaskService().CompleteTask(ctx, task.TaskID, map[string]interface{}{"approved": true}) }},
		{"cancel", func(ctx context.Context, task *ent.ProcessTask) error { return f.engine.TaskService().CancelTask(ctx, task.TaskID, "late cancel") }},
		{"delegate", func(ctx context.Context, task *ent.ProcessTask) error { return f.engine.TaskService().DelegateTask(ctx, task.TaskID, strconv.Itoa(f.outsider.ID)) }},
		{"set_variables", func(ctx context.Context, task *ent.ProcessTask) error { return f.engine.TaskService().SetTaskVariables(ctx, task.TaskID, map[string]interface{}{"late": true}) }},
		{"counter_sign", func(ctx context.Context, task *ent.ProcessTask) error { _, err := f.engine.TaskService().CreateCounterSignTasks(ctx, task.TaskID, &CounterSignRequest{Approvers: []string{strconv.Itoa(f.actor.ID)}, ApprovalType: "parallel", Threshold: 1}); return err }},
	}
	for _, terminal := range []string{common.ProcessTaskStatusCompleted, common.ProcessTaskStatusCancelled} {
		for _, command := range commands {
			t.Run(terminal+"/"+command.name, func(t *testing.T) {
				task := f.seedParticipantTaskWithStatus(t, terminal)
				before := task.TaskVariables
				err := command.run(f.scopedCtx(false, false, false, false), task)
				assert.Error(t, err)
				after := f.client.ProcessTask.GetX(context.Background(), task.ID)
				assert.Equal(t, terminal, after.Status)
				assert.Equal(t, before, after.TaskVariables)
				assert.Equal(t, 1, rejectedTaskAuditCount(t, f.client, task, command.name))
			})
		}
	}
}
```

Define the helpers in `bpmn_task_terminal_mutation_test.go`:

```go
func (f *bpmnAuthorizationFixture) seedParticipantTaskWithStatus(t *testing.T, status string) *ent.ProcessTask {
	t.Helper()
	suffix := fmt.Sprintf("terminal-%s-%d", status, time.Now().UnixNano())
	instance := f.createProcessInstance(t, f.tenant, suffix)
	task := f.createProcessTask(t, instance, f.tenant.ID, suffix, "", f.actor.Username, "")
	updated, err := f.client.ProcessTask.UpdateOne(task).SetStatus(status).Save(context.Background())
	require.NoError(t, err)
	return updated
}

func rejectedTaskAuditCount(t *testing.T, client *ent.Client, task *ent.ProcessTask, command string) int {
	t.Helper()
	logs, err := client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(task.ProcessInstanceID),
		processauditlog.ActivityID(task.TaskDefinitionKey),
		processauditlog.Action(AuditActionTaskMutationRejected),
	).All(context.Background())
	require.NoError(t, err)
	count := 0
	for _, log := range logs {
		if log.Metadata["command"] == command {
			count++
		}
	}
	return count
}
```

Add process tests proving suspend-completed, resume-terminated, and terminate-completed return conflict, preserve status, and create no success audit.

Add a KAF fence table using an async delegated task: assign, claim, cancel, delegate, variables, counter-sign, and vote must be rejected for human actors without changing task state; completion must still require the dedicated active same-tenant `kaf_automation` actor and delegated status. This table protects task-type/status/actor/tenant rules while lifecycle code is centralized.

- [ ] **Step 2: Run terminal tests and confirm state corruption**

Run: `cd itsm-backend && go test ./service -run 'TestBPMN(TaskTerminalMutations|ProcessTerminalMutations)' -count=1 -v`

Expected: FAIL; assign currently resurrects terminal tasks, cancel rewrites completed tasks, and variables can change after completion.

- [ ] **Step 3: Move authorization and loading inside each transaction**

For assign, cancel, variables, delegate, and counter-sign:

1. Open the Ent transaction.
2. Build `txEngine := s.engine.forClient(tx.Client(), nil)` and use its task service/resolver.
3. Load by `TaskID + scope.TenantID` with the transaction client.
4. Authorize the exact row through the existing participant/update-all method.
5. Validate current status through `ValidateBPMNTaskLifecycle`.
6. Resolve actor and assignee through the transaction client.
7. Update with `ID + TenantID + bpmnTaskLifecyclePredicate(command, task.AggregationVersion)` and increment `AggregationVersion` by one.
8. Require exactly one affected row.
9. Write success audit through `ForClient(tx.Client())`.
10. Commit.

Apply the same predicate to claim, completion, and vote rather than retaining local status fragments. KAF completion remains delegated-only at its dedicated entry gate and reaches the common complete predicate only after that validation.

Before human task commands apply lifecycle predicates, resolve the task callback handler exactly as the current completion path does. If it is asynchronous, reject assign, claim, cancel, delegate, variable mutation, counter-sign, and vote. Do not infer KAF behavior from role names or status alone, and do not weaken `authorizeKafAutomationActorForStatusWithClient`.

- [ ] **Step 4: Add and use a rejected-terminal audit helper**

```go
func (s *BPMNAuditService) RecordTaskMutationRejected(
	ctx context.Context,
	task *ent.ProcessTask,
	actorID int,
	actorName string,
	command BPMNTaskCommand,
	currentStatus string,
) error {
	auditCtx, err := s.taskAuditContext(ctx, task, actorID, actorName)
	if err != nil {
		return err
	}
	auditCtx.Action = AuditActionTaskMutationRejected
	auditCtx.Metadata = map[string]interface{}{
		"command":        string(command),
		"current_status": currentStatus,
		"result":         "conflict",
	}
	return s.RecordAudit(ctx, auditCtx)
}
```

When the loaded status is already disallowed, record rejection with the transaction client, commit the audit-only transaction, then return the lifecycle conflict. When a CAS affects zero rows, roll back the mutation transaction first; then open a fresh audit-only transaction, re-read by ID+tenant, validate the actor again, record the observed status/version, commit that audit transaction, and return conflict. This separate transaction is mandatory because completion merges instance variables before its task CAS; committing the original transaction after a zero-row task update would leak a partial mutation. Never write a success audit on either rejection path.

- [ ] **Step 5: Apply process-instance CAS**

Suspend, resume, and terminate load through `loadForUpdate` using the transaction client, validate the process lifecycle, and update with `ID + TenantID + bpmnProcessLifecyclePredicate(command, instance.Version)`, incrementing `ProcessInstance.Version`. Check `affected == 1` before writing success audit. Termination cancels only active child tasks using each task's observed `AggregationVersion` and `bpmnTaskLifecyclePredicate(BPMNTaskCommandCancel, task.AggregationVersion)`; it never rewrites completed/cancelled children.

- [ ] **Step 6: Write real PostgreSQL race tests**

```go
func TestBPMNTaskTerminalCommandsRaceWithCompletionPostgres(t *testing.T) {
	clientA, clientB, fixture := newBPMNPostgresRaceFixture(t)
	commands := []struct {
		name string
		run  func(TaskService, context.Context, string) error
	}{
		{"assign", func(s TaskService, ctx context.Context, id string) error { return s.AssignTask(ctx, id, strconv.Itoa(fixture.otherActor.ID)) }},
		{"cancel", func(s TaskService, ctx context.Context, id string) error { return s.CancelTask(ctx, id, "race") }},
		{"variables", func(s TaskService, ctx context.Context, id string) error { return s.SetTaskVariables(ctx, id, map[string]interface{}{"race": true}) }},
	}
	for _, command := range commands {
		results := runConcurrentTaskCommands(t,
			func() error { return fixture.engineFor(clientA).TaskService().CompleteTask(fixture.actorCtx, fixture.task.TaskID, map[string]interface{}{"approved": true}) },
			func() error { return command.run(fixture.engineFor(clientB).TaskService(), fixture.actorCtx, fixture.task.TaskID) },
		)
		assertExactlyOneTaskTerminalWinner(t, results)
		assertSingleTaskSuccessAudit(t, fixture.client, fixture.task.ID)
	}
}
```

Use the repository `ITSM_TEST_DB` helper and independent Ent connections. Assert one legal final state, one success audit, one rejected-command audit, and no variable write if completion wins. SQLite is not accepted as concurrency evidence.

- [ ] **Step 7: Run lifecycle, rollback, and PostgreSQL tests**

Run:

```bash
cd itsm-backend
go test ./service -run 'TestBPMN(TaskTerminalMutations|ProcessTerminalMutations|Task.*Rollback|CounterSign|Claim|Complete)' -count=1 -v
go test -tags=integration ./service -run 'TestBPMNTaskTerminalCommandsRaceWithCompletionPostgres' -count=1 -v
go test -race -tags=integration ./service -run 'TestBPMNTaskTerminalCommandsRaceWithCompletionPostgres' -count=1 -v
```

Expected: all PASS with the PostgreSQL test executed, not skipped.

- [ ] **Step 8: Prove ad hoc terminal predicates are gone**

Run: `rg -n 'StatusNEQ\(common\.ProcessTaskStatusCompleted\)|StatusNEQ\(common\.ProcessTaskStatusCancelled\)|task\.Status == common\.ProcessTaskStatusCompleted|task\.Status == common\.ProcessTaskStatusCancelled' itsm-backend/service/bpmn_process_engine.go`

Expected: no mutation authorization remains outside `bpmn_lifecycle_policy.go`; read-only presentation checks are either moved to named lifecycle helpers or documented by the implementation commit.

- [ ] **Step 9: Commit**

```bash
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_audit_service.go itsm-backend/service/bpmn_task_terminal_mutation_test.go itsm-backend/service/bpmn_task_lifecycle_integration_test.go itsm-backend/service/bpmn_authorization_test.go itsm-backend/service/bpmn_final_fix_test.go itsm-backend/service/bpmn_counter_sign_transaction_test.go itsm-backend/service/bpmn_claim_cas_integration_test.go
git commit -m "fix(bpmn): enforce lifecycle CAS for mutations"
```

### Task 6: Attribute Process-Start Audit to the Trusted Actor

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:241-345`
- Modify: `itsm-backend/service/bpmn_process_trigger_service.go:90-115,335-365`
- Create: `itsm-backend/service/bpmn_start_audit_actor_test.go`
- Modify: `itsm-backend/controller/bpmn_workflow_authorization_test.go`

**Interfaces:**
- Produces: private `resolveBPMNProcessStartActor(context.Context, *ent.Client, int, map[string]interface{}) (*ent.User, string, error)`.
- Consumes: typed `BPMNAccessScope`, `bpmn.BPMNUserIDContextKey`, and the authoritative `triggered_by` variable generated by `ProcessTriggerService`.
- Deletes: `ctx.Value("user")` from process-start audit attribution.

- [ ] **Step 1: Write actor and rollback tests**

```go
func TestStartProcessAuditUsesAuthenticatedScopeActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance, err := f.engine.StartProcess(
		f.scopedCtx(false, false, false, false),
		f.definition.Key,
		"ticket:scope-actor",
		"generic",
		91,
		map[string]interface{}{"requester_id": f.actor.ID},
	)
	require.NoError(t, err)
	audit := f.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionProcessStarted),
	).OnlyX(context.Background())
	assert.Equal(t, f.actor.ID, audit.UserID)
	assert.Equal(t, f.actor.Name, audit.UserName)
}

func TestStartProcessRejectsWrongTenantOrInactiveAuditActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	inactive, err := f.client.User.Create().
		SetUsername("inactive.start.actor").
		SetEmail("inactive.start.actor@example.test").
		SetName("Inactive Start Actor").
		SetPasswordHash("test").
		SetRole("end_user").
		SetActive(false).
		SetTenantID(f.tenant.ID).
		Save(context.Background())
	require.NoError(t, err)
	for _, actorID := range []int{f.otherActor.ID, inactive.ID} {
		ctx := WithTrustedBPMNTenantContext(context.Background(), f.tenant.ID)
		_, err = f.engine.StartProcess(ctx, f.definition.Key, "ticket:bad-actor", "generic", 92, map[string]interface{}{
			"triggered_by": strconv.Itoa(actorID),
		})
		assert.Error(t, err)
		assert.Zero(t, f.client.ProcessInstance.Query().Where(processinstance.BusinessKey("ticket:bad-actor")).CountX(context.Background()))
	}
}
```

Add a trusted domain-trigger success case where `triggered_by` is an active same-tenant user, and an explicit system case only when the trusted context carries `triggered_by: "system"`; assert audit name `system` and user ID `0`.

- [ ] **Step 2: Run tests and confirm audit actor is zero**

Run: `cd itsm-backend && go test ./service -run 'TestStartProcess(AuditUsesAuthenticatedScopeActor|RejectsWrongTenantOrInactiveAuditActor|AuditUsesTrustedTriggerActor)' -count=1 -v`

Expected: FAIL because the current code reads only `ctx.Value("user")`.

- [ ] **Step 3: Implement typed actor resolution inside the start transaction**

```go
func resolveBPMNProcessStartActor(
	ctx context.Context,
	client *ent.Client,
	tenantID int,
	variables map[string]interface{},
) (*ent.User, string, error) {
	actorID := 0
	if _, present := bpmnAccessScopeValue(ctx); present {
		scope, err := BPMNAccessScopeFromContext(ctx)
		if err != nil || scope.TenantID != tenantID {
			return nil, "", common.NewForbiddenError("流程启动用户租户不一致")
		}
		actorID = scope.UserID
	} else if userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int); userID > 0 {
		actorID = userID
	} else if triggeredBy := strings.TrimSpace(bpmn.GetStringFromVars(variables, "triggered_by")); triggeredBy == "system" {
		return nil, "system", nil
	} else if triggeredBy != "" {
		parsed, err := strconv.Atoi(triggeredBy)
		if err != nil || parsed <= 0 {
			return nil, "", fmt.Errorf("流程启动用户无效")
		}
		actorID = parsed
	} else {
		return nil, "", fmt.Errorf("流程启动缺少权威操作用户")
	}
	actor, err := client.User.Query().Where(user.ID(actorID), user.TenantID(tenantID), user.Active(true)).Only(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("获取流程启动用户失败: %w", err)
	}
	return actor, actor.Name, nil
}
```

Call this helper through `tx.Client()` before `RecordProcessStarted`. Use actor ID/name or `(0, "system")`. Ensure `ProcessTriggerService` always places its explicit `TriggeredBy` in `triggered_by`; HTTP start uses the typed scope and ignores body attempts to replace actor identity.

- [ ] **Step 4: Run start, rollback, trigger, and callback tests**

Run: `cd itsm-backend && go test ./service ./controller -run 'TestStartProcess|TestBPMNProcessTrigger|Test.*InitialCallbackOutbox|TestBPMNAuthorizationMatrix' -count=1 -v`

Expected: PASS. A forced audit or actor-resolution failure leaves no instance, task, execution history, audit, or callback outbox row.

- [ ] **Step 5: Prove the string actor fallback is deleted**

Run: `rg -n 'ctx\.Value\("user"\)|context\.WithValue\([^\n]+"user"' itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_start_audit_actor_test.go itsm-backend/controller/bpmn_workflow_authorization_test.go`

Expected: no matches.

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_process_trigger_service.go itsm-backend/service/bpmn_start_audit_actor_test.go itsm-backend/controller/bpmn_workflow_authorization_test.go
git commit -m "fix(bpmn): attribute process start audits"
```

### Task 7: Run the P1-C Integration and Deletion Gates

**Files:**
- Modify only if a failing test exposes a P1-C defect: files already listed in Tasks 1-6.
- Do not modify: callback handler result types, notification DTOs, frontend notification files, callback outbox schema, KAF schemas, `AGENTS.md`, or `CLAUDE.md`.

**Interfaces:**
- Verifies: all Frozen Interfaces in this plan.
- Produces: one evidence record in the implementation handoff containing commands, exit codes, pass/fail/skip counts, deletion scans, and final commit list.

- [ ] **Step 1: Run focused authorization and lifecycle suites**

```bash
cd itsm-backend
go test ./controller ./service ./handlers/change -run 'TestBPMN(ProcessTrigger|Dashboard|Monitoring|Authorization|InstanceAccess|TaskTerminal|ProcessTerminal|StartProcess|CounterSign|Claim|Complete)' -count=1 -v
```

Expected: PASS.

- [ ] **Step 2: Run focused race suites**

```bash
cd itsm-backend
go test -race ./controller ./service -run 'TestBPMN(Authorization|TaskTerminal|ProcessTerminal|StartProcess|CounterSign|Claim|Complete)' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run real PostgreSQL integration suites without skips**

```bash
cd itsm-backend
go test -tags=integration ./service -run 'TestBPMN(TaskTerminalCommandsRaceWithCompletionPostgres|ClaimCASPostgres|CounterSign.*Postgres|CallbackOutboxLeaseRecoveryPostgres|Kaf.*Postgres)' -count=1 -v
go test -race -tags=integration ./service -run 'TestBPMN(TaskTerminalCommandsRaceWithCompletionPostgres|ClaimCASPostgres|CounterSign.*Postgres|CallbackOutboxLeaseRecoveryPostgres|Kaf.*Postgres)' -count=1 -v
```

Expected: PASS with zero skips. If `ITSM_TEST_DB` is unavailable, restore the repository test PostgreSQL service and rerun; do not substitute SQLite evidence.

- [ ] **Step 4: Run all backend tests and build**

```bash
cd itsm-backend
go test ./... -count=1
go test -race ./controller ./service ./handlers/change -count=1
go build ./...
```

Expected: PASS.

- [ ] **Step 5: Run route, legacy, and ownership scans**

```bash
rg -n 'processTriggerTenant|resolveProcessInstanceBusinessID|ctx\.Value\("user"\)' itsm-backend
rg -n 'audit-logs/timeline"' itsm-backend/controller
rg -n 'RequireLegacyBPMNRoles\(\)' itsm-backend/controller/bpmn_dashboard_controller.go itsm-backend/controller/bpmn_monitoring_controller.go
rg -n 'StatusNEQ\(common\.ProcessTaskStatusCompleted\)|StatusNEQ\(common\.ProcessTaskStatusCancelled\)' itsm-backend/service/bpmn_process_engine.go
git diff --check f11290317499b958ba93d85689286fdccccfe697..HEAD
git status --short
```

Expected: the first four scans return no matches; `git diff --check` returns no output; status contains only the intended P1-C files. The pre-existing untracked architecture review file is not staged, edited, deleted, or included in any commit.

- [ ] **Step 6: Verify P1-D’s engine contract**

Run: `git diff f11290317499b958ba93d85689286fdccccfe697..HEAD -- itsm-backend/service/bpmn_process_engine.go | rg -n 'completeAuthorizedTaskWithClient|enqueueUserTaskCallback|callbackOutbox|ProcessEngine interface'`

Expected: `ProcessEngine` and `completeAuthorizedTaskWithClient` signatures are unchanged; lifecycle predicates guard completion before callback enqueue; no second callback or status-write path exists.

- [ ] **Step 7: Commit only a gate-discovered correction if one was required**

If all gates passed without correction, do not create an empty commit. If a gate failed, return to the task that owns the failing behavior, add the correction and regression assertion to that task's exact file list, rerun that task's commands, and use that task's explicit `git add` and commit command. Never stage the untracked architecture review file.

## Completion Criteria

- Process-trigger status allows only initiator, exact participant, or instance read-all; cancel/suspend/resume require instance update-all.
- Process-trigger methods have no caller-supplied tenant argument and no numeric-ID translation helper.
- Dashboard and monitoring object status/timeline/audit routes use the same instance access policy.
- Tenant-wide audit, user activity, metrics, status lists, SLA, bottleneck, performance, alert, health, and tenant-stat routes require instance read-all.
- The dashboard timeline route contains `:process_instance_key`; the parameterless dead route is deleted.
- Completed/cancelled tasks cannot be assigned, claimed, completed again, cancelled, delegated, modified, voted, or expanded into counter-sign children.
- Completed/terminated process instances cannot be suspended, resumed, or terminated through another command.
- Every mutation uses tenant plus lifecycle CAS and checks exactly one affected row.
- Every successful state mutation and its audit share one transaction. Every rejected terminal task command leaves one rejection audit and no state change.
- Concurrent completion versus assign/cancel/variables produces one legal winner, one success audit, one rejection audit, and no lost update in real PostgreSQL.
- Process-start audit uses an active same-tenant authenticated or explicit trusted actor; the string `"user"` context key is absent.
- KAF delegated task status/actor/tenant rules, callback outbox recovery, and callback integration remain green.
- No legacy overload, role-only object authorization, empty-success monitoring fallback, or duplicate participant/lifecycle implementation remains.
- Focused, race, PostgreSQL, full backend, and build gates pass with recorded zero-skip integration evidence.
