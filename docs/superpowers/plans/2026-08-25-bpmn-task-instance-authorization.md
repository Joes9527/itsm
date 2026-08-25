# BPMN 流程实例/任务实例级授权修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the data-leak gap where BPMN process-instance/task read and action endpoints only do tenant-level isolation, not participant-level (initiator/assignee/candidate) authorization.

**Architecture:** Add a single shared "is this caller a participant in this task/instance" resolution helper in `service/bpmn`, reuse it everywhere task/instance participation is checked (fixing an existing `candidate_groups` gap along the way), gate "see everything" access behind the already-existing `process_instance:read`/`task:read`/`task:update` RBAC permissions (computed per-request via `middleware.HasResourcePermission`, never trusted from client input), backfill the never-populated `ProcessInstance.initiator` field, and wire the 4 previously-unaudited task-mutation actions into the existing `BPMNAuditService`.

**Tech Stack:** Go, Gin, Ent ORM, SQLite (`enttest`) for tests, `stretchr/testify`.

**Spec:** `docs/superpowers/specs/2026-08-25-bpmn-task-instance-authorization-design.md`

## Global Constraints

- Every new/modified query touching `ProcessInstance`/`ProcessTask` must include an explicit `TenantID` predicate — do not rely solely on upstream context injection (AGENTS.md: "Cross-tenant access must fail closed").
- The "elevated" (see/act on everything) decision must be computed server-side only, from `role`/`tenant_id`/`client` already present in `gin.Context` (set by `RBACMiddleware`) via `middleware.HasResourcePermission`. It must never be read from a client-suppliable request field (query param, JSON body) — that would let a caller grant themselves elevated access by just adding `?elevated=true`.
- There must be exactly ONE implementation of "is this user a participant in this task" (`service/bpmn.CallerIdentity.IsTaskParticipant` + `service/bpmn.ResolveCallerIdentity`, added in Task 1). `authorizeTaskActor`, `isTaskCandidate`, `ListUserTasks`, `GetTask`/`GetTaskByID`, and `ListProcessInstances` must all call into it — no independent re-implementations.
- Controllers stay thin: bind/validate, call service, map response (AGENTS.md). The elevated-permission check and the two-step participant query live in the service/controller-helper layer defined by this plan, not duplicated inline per handler.
- Do not touch `middleware.RequireLegacyBPMNRoles()` or the route-level role gate on `/api/v1/bpmn/*` — out of scope per the spec's non-goals.
- Do not write a historical-data backfill script for `initiator` — out of scope per the spec's non-goals (existing rows keep an empty `initiator`).
- Run `go build ./...` and the narrowed package tests after every task; run full `go test ./...` at the end (Task 9).

---

## File Structure

- **Create:** `itsm-backend/service/bpmn/participation.go` — the shared `CallerIdentity` type + `ResolveCallerIdentity` + `IsTaskParticipant`.
- **Create:** `itsm-backend/service/bpmn/participation_test.go` — unit tests for the above.
- **Modify:** `itsm-backend/service/bpmn/handler_base.go` — add `BPMNElevatedContextKey`.
- **Modify:** `itsm-backend/service/bpmn_process_engine.go` — `authorizeTaskActor`, `isTaskCandidate`, `StartProcess` (initiator), `ListProcessInstances`, `GetTask`/`GetTaskByID`, `ListUserTasks`, `AssignTask`, `CancelTask`, `SetTaskVariables`, `CreateCounterSignTasks`.
- **Modify:** `itsm-backend/service/bpmn_audit_service.go` — add `RecordTaskCancelled`, `RecordTaskVariablesChanged`, `RecordCounterSignCreated`.
- **Modify:** `itsm-backend/controller/bpmn_workflow_controller.go` — add `hasElevatedBPMNAccess` helper; wire it into `ListProcessInstances`, `GetTask`, `ListUserTasks`, `AssignTask`, `CancelTask`, `SetTaskVariables`, `CreateCounterSignTasks`.
- **Modify:** `itsm-backend/service/bpmn_process_engine_ext_test.go` — regression tests for the `authorizeTaskActor`/`isTaskCandidate` `candidate_groups` fix, `StartProcess` initiator, `ListProcessInstances`/`GetTask`/`ListUserTasks` scoping, and the 4 task-action authorization+audit paths.

---

### Task 1: Shared caller-participation helper

**Files:**
- Create: `itsm-backend/service/bpmn/participation.go`
- Create: `itsm-backend/service/bpmn/participation_test.go`

**Interfaces:**
- Produces: `type CallerIdentity struct { IDStr, Username, Email, GroupsCSV string }`; `func ResolveCallerIdentity(ctx context.Context, client *ent.Client, groupResolver *GroupResolver, tenantID, userID int) (*CallerIdentity, error)`; `func (id *CallerIdentity) IsTaskParticipant(task *ent.ProcessTask) bool`. All later tasks import `service/bpmn` and call these.

- [ ] **Step 1: Write the failing test**

```go
// itsm-backend/service/bpmn/participation_test.go
package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

func newParticipationTestClient(t *testing.T) (*ent.Client, int, int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:participation_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("part-1").SetDomain("part-1.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	u, err := client.User.Create().
		SetUsername("candidate1").SetEmail("candidate1@example.com").SetName("Candidate One").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	return client, tenant.ID, u.ID
}

func TestResolveCallerIdentity_PopulatesIDUsernameEmail(t *testing.T) {
	client, tenantID, userID := newParticipationTestClient(t)
	identity, err := ResolveCallerIdentity(context.Background(), client, NewGroupResolver(client), tenantID, userID)
	require.NoError(t, err)
	assert.Equal(t, "candidate1", identity.Username)
	assert.Equal(t, "candidate1@example.com", identity.Email)
	assert.NotEmpty(t, identity.IDStr)
}

func TestResolveCallerIdentity_UnknownUserErrors(t *testing.T) {
	client, tenantID, _ := newParticipationTestClient(t)
	_, err := ResolveCallerIdentity(context.Background(), client, NewGroupResolver(client), tenantID, 999999)
	assert.Error(t, err)
}

func TestIsTaskParticipant_MatchesAssigneeByIDUsernameOrEmail(t *testing.T) {
	identity := &CallerIdentity{IDStr: "42", Username: "candidate1", Email: "candidate1@example.com"}

	byID := &ent.ProcessTask{Assignee: "42"}
	assert.True(t, identity.IsTaskParticipant(byID))

	byUsername := &ent.ProcessTask{Assignee: "candidate1"}
	assert.True(t, identity.IsTaskParticipant(byUsername))

	byEmail := &ent.ProcessTask{Assignee: "candidate1@example.com"}
	assert.True(t, identity.IsTaskParticipant(byEmail))

	noMatch := &ent.ProcessTask{Assignee: "someone-else"}
	assert.False(t, identity.IsTaskParticipant(noMatch))
}

func TestIsTaskParticipant_MatchesCandidateUsersCSV(t *testing.T) {
	identity := &CallerIdentity{IDStr: "42", Username: "candidate1"}
	task := &ent.ProcessTask{CandidateUsers: "7, candidate1, 99"}
	assert.True(t, identity.IsTaskParticipant(task))
}

func TestIsTaskParticipant_MatchesCandidateGroupsByExactToken(t *testing.T) {
	identity := &CallerIdentity{IDStr: "42", GroupsCSV: "network_eng,dept_manager"}

	matches := &ent.ProcessTask{CandidateGroups: "network_eng"}
	assert.True(t, identity.IsTaskParticipant(matches))

	// A caller in "eng" must NOT match a task requiring "network_eng" — exact
	// token comparison, not substring, unlike the caller's group list being a
	// raw CSV.
	noPartialMatch := &ent.ProcessTask{CandidateGroups: "network_eng"}
	otherIdentity := &CallerIdentity{GroupsCSV: "eng"}
	assert.False(t, otherIdentity.IsTaskParticipant(noPartialMatch))

	noGroups := &ent.ProcessTask{CandidateGroups: ""}
	assert.False(t, identity.IsTaskParticipant(noGroups))
}

func TestIsTaskParticipant_EmptyTaskFieldsNeverMatch(t *testing.T) {
	identity := &CallerIdentity{IDStr: "42", Username: "candidate1", Email: "candidate1@example.com"}
	task := &ent.ProcessTask{}
	assert.False(t, identity.IsTaskParticipant(task))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./service/bpmn/... -run 'TestResolveCallerIdentity|TestIsTaskParticipant' -v`
Expected: FAIL with `undefined: ResolveCallerIdentity` / `undefined: CallerIdentity` (the `ent` import will also need adding to the test file's imports — add `"itsm-backend/ent"`).

- [ ] **Step 3: Write minimal implementation**

```go
// itsm-backend/service/bpmn/participation.go
package bpmn

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// CallerIdentity captures every string form a caller can be matched against
// on a ProcessTask's Assignee/CandidateUsers/CandidateGroups CSV columns:
// numeric user ID, username, or email — Assignee/CandidateUsers can hold any
// of the three depending on which code path wrote them (designer-set
// assignees are usernames, auto-resolved assignees are numeric IDs) — plus
// the caller's resolved group/role names for CandidateGroups matching.
type CallerIdentity struct {
	IDStr     string
	Username  string
	Email     string
	GroupsCSV string
}

// ResolveCallerIdentity loads the identity forms needed to evaluate task
// participation for userID. This is the SINGLE place that resolves "who is
// this caller, for BPMN candidate-matching purposes" — authorizeTaskActor,
// isTaskCandidate, ListUserTasks, GetTask, and ListProcessInstances must all
// call this instead of independently re-deriving these fields, so the
// matching rules cannot silently drift apart across call sites.
func ResolveCallerIdentity(ctx context.Context, client *ent.Client, groupResolver *GroupResolver, tenantID, userID int) (*CallerIdentity, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("无效的用户ID")
	}
	actor, err := client.User.Query().Where(user.ID(userID)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("用户不存在: %w", err)
	}
	identity := &CallerIdentity{
		IDStr:    strconv.Itoa(userID),
		Username: strings.TrimSpace(actor.Username),
		Email:    strings.TrimSpace(actor.Email),
	}
	if groupResolver != nil {
		groupsCSV, gErr := groupResolver.GetUserGroupNames(ctx, tenantID, userID)
		if gErr != nil {
			return nil, fmt.Errorf("查询用户所属组失败: %w", gErr)
		}
		identity.GroupsCSV = groupsCSV
	}
	return identity, nil
}

// IsTaskParticipant reports whether this identity is the task's assignee, is
// listed in its candidate_users, or belongs to a role/group listed in its
// candidate_groups. assignee/candidate_users are matched by exact token
// after splitting on commas (a task's CSV may hold IDs, usernames, or
// emails in any position). candidate_groups is matched by exact token on
// both sides — NOT the substring-of-whole-CSV comparison ListUserTasks's
// query used before Task 3 of this plan, which only worked reliably for
// single-group callers.
func (id *CallerIdentity) IsTaskParticipant(task *ent.ProcessTask) bool {
	matchesUser := func(csv string) bool {
		for _, candidate := range strings.Split(csv, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if candidate == id.IDStr || candidate == id.Username || (id.Email != "" && candidate == id.Email) {
				return true
			}
		}
		return false
	}
	if matchesUser(task.Assignee) || matchesUser(task.CandidateUsers) {
		return true
	}
	if id.GroupsCSV == "" || task.CandidateGroups == "" {
		return false
	}
	callerGroups := make(map[string]bool)
	for _, g := range strings.Split(id.GroupsCSV, ",") {
		g = strings.TrimSpace(g)
		if g != "" {
			callerGroups[g] = true
		}
	}
	for _, g := range strings.Split(task.CandidateGroups, ",") {
		g = strings.TrimSpace(g)
		if g != "" && callerGroups[g] {
			return true
		}
	}
	return false
}
```

Add `"itsm-backend/ent"` to the test file's import block (needed for `&ent.ProcessTask{...}` fixtures).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./service/bpmn/... -run 'TestResolveCallerIdentity|TestIsTaskParticipant' -v`
Expected: PASS (7 tests)

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add service/bpmn/participation.go service/bpmn/participation_test.go
git commit -m "feat(bpmn): add shared caller-participation resolution helper"
```

---

### Task 2: Rewire `authorizeTaskActor`/`isTaskCandidate` onto the shared helper + fix `candidate_groups` gap

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:517-539` (`authorizeTaskActor`), `itsm-backend/service/bpmn_process_engine.go:2417-2432` (`isTaskCandidate`)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.ResolveCallerIdentity(ctx, client, groupResolver, tenantID, userID) (*bpmn.CallerIdentity, error)`, `(*bpmn.CallerIdentity).IsTaskParticipant(task *ent.ProcessTask) bool` from Task 1.
- Produces: unchanged signatures — `func (e *CustomProcessEngine) authorizeTaskActor(ctx context.Context, task *ent.ProcessTask) error` and `func isTaskCandidate(ctx context.Context, client *ent.Client, userID int, task *ent.ProcessTask) (bool, error)` — callers (`CompleteTask`, `ClaimTask`, `ClaimTaskByID`, `SubmitTaskDecision`, `Vote`) are unaffected.

- [ ] **Step 1: Write the failing test**

```go
// Append to itsm-backend/service/bpmn_process_engine_ext_test.go

func TestAuthorizeTaskActor_AllowsCandidateGroupMatch(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err := engine.client.Group.Create().
		SetName("network_eng").SetTenantID(tenantID).
		AddMemberIDs(actorID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "groupauthz1")
	task, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetCandidateGroups("network_eng").
		Save(ctx)
	require.NoError(t, err)

	assert.NoError(t, engine.authorizeTaskActor(ctx, task),
		"a caller who is only a candidate via candidate_groups must be allowed to act on the task")
}

func TestIsTaskCandidate_AllowsCandidateGroupMatch(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err := engine.client.Group.Create().
		SetName("network_eng").SetTenantID(tenantID).
		AddMemberIDs(actorID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "groupcandidate1")
	task, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetCandidateGroups("network_eng").
		Save(ctx)
	require.NoError(t, err)

	ok, err := isTaskCandidate(ctx, engine.client, actorID, task)
	require.NoError(t, err)
	assert.True(t, ok, "a caller who is only a candidate via candidate_groups must be claimable")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./service/... -run 'TestAuthorizeTaskActor_AllowsCandidateGroupMatch|TestIsTaskCandidate_AllowsCandidateGroupMatch' -v`
Expected: FAIL — both assertions fail because neither function currently checks `CandidateGroups` at all.

- [ ] **Step 3: Write minimal implementation**

```go
// itsm-backend/service/bpmn_process_engine.go — replace lines 514-539

// authorizeTaskActor ensures that task actions are performed by the assigned
// user or an explicitly resolved candidate (by ID, username, email, or
// candidate_groups membership — see bpmn.CallerIdentity.IsTaskParticipant).
// System/internal calls without an authenticated actor keep their existing
// permissive behavior.
func (e *CustomProcessEngine) authorizeTaskActor(ctx context.Context, task *ent.ProcessTask) error {
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return nil
	}
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, e.client, e.groupResolver, tenantID, userID)
	if err != nil {
		return fmt.Errorf("审批用户不存在: %w", err)
	}
	if identity.IsTaskParticipant(task) {
		return nil
	}
	return fmt.Errorf("当前用户不是该任务的审批人或候选人")
}
```

```go
// itsm-backend/service/bpmn_process_engine.go — replace lines 2414-2432

// isTaskCandidate 复用共享的 bpmn.CallerIdentity 参与者判定（用户 ID/用户名/邮箱，或
// candidate_groups 命中），用于 ClaimTask/ClaimTaskByID 校验：只有任务的
// assignee/candidate_users/candidate_groups 命中的人才能认领未分配的任务——否则任何
// 登录用户都能抢先认领任何审批任务（包括自己提交的工单）。
func isTaskCandidate(ctx context.Context, client *ent.Client, userID int, task *ent.ProcessTask) (bool, error) {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, client, bpmn.NewGroupResolver(client), tenantID, userID)
	if err != nil {
		return false, err
	}
	return identity.IsTaskParticipant(task), nil
}
```

Remove the now-unused `strings`/`strconv`-only-for-this-purpose imports only if `go build` reports them unused elsewhere in the file — in practice both packages are used extensively elsewhere in `bpmn_process_engine.go`, so no import changes are expected.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./service/... -run 'TestAuthorizeTaskActor|TestIsTaskCandidate' -v`
Expected: PASS, including the two new tests and the three pre-existing ones (`TestAuthorizeTaskActor_AllowsAssigneeAndCandidate`, `TestAuthorizeTaskActor_NoActorContextIsPermissive`) unchanged.

- [ ] **Step 5: Run full package test and build**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS, no regressions in `ClaimTask`/`CompleteTask`/`Vote`/`SubmitTaskDecision` callers.

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): authorizeTaskActor/isTaskCandidate honor candidate_groups"
```

---

### Task 3: Populate `ProcessInstance.initiator` in `StartProcess`

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:241-255` (`StartProcess`'s `ProcessInstance.Create()` chain)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `ctx.Value(bpmn.BPMNUserIDContextKey).(int)` (already used elsewhere in this file), `variables["requester_id"]` (existing convention, already read elsewhere via `GetIntFromVars`-style helpers) as the system-triggered fallback.
- Produces: `ProcessInstance.Initiator` (existing ent field, string) now actually populated. No signature changes.

- [ ] **Step 1: Write the failing test**

```go
// Append to itsm-backend/service/bpmn_process_engine_ext_test.go

func TestStartProcess_PopulatesInitiatorFromContextUser(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)

	deployment, err := engine.client.ProcessDeployment.Create().
		SetDeploymentID("DEP-initiator1").SetDeploymentName("Deployment initiator1").
		SetDeploymentTime(time.Now()).SetDeployedBy("test").SetIsActive(true).
		SetTenantID(tenantID).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessDefinition.Create().
		SetKey("initiator_test_flow").SetName("Initiator Test").SetVersion("1").
		SetIsLatest(true).SetIsActive(true).
		SetBpmnXML([]byte(minimalStartToEndBPMN("initiator_test_flow"))).
		SetDeploymentID(deployment.ID).SetDeployedAt(time.Now()).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorID)

	instance, err := engine.StartProcess(ctx, "initiator_test_flow", "initiator-biz-1", map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%d", actorID), instance.Initiator)
}

func TestStartProcess_FallsBackToRequesterIDVariableWhenNoContextUser(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)

	deployment, err := engine.client.ProcessDeployment.Create().
		SetDeploymentID("DEP-initiator2").SetDeploymentName("Deployment initiator2").
		SetDeploymentTime(time.Now()).SetDeployedBy("test").SetIsActive(true).
		SetTenantID(tenantID).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessDefinition.Create().
		SetKey("initiator_test_flow2").SetName("Initiator Test 2").SetVersion("1").
		SetIsLatest(true).SetIsActive(true).
		SetBpmnXML([]byte(minimalStartToEndBPMN("initiator_test_flow2"))).
		SetDeploymentID(deployment.ID).SetDeployedAt(time.Now()).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	// System-triggered start: no authenticated user in ctx, only the
	// requester_id convention variable (see ticket_service.go's trigger path).
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)

	instance, err := engine.StartProcess(ctx, "initiator_test_flow2", "initiator-biz-2", map[string]interface{}{
		"requester_id": float64(actorID),
	})
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%d", actorID), instance.Initiator)
}
```

Add this shared BPMN XML fixture builder near the other test helpers (e.g. right below `createProcessFixture`) if it does not already exist — search first with `grep -n "minimalStartToEndBPMN" itsm-backend/service/*_test.go`; if an equivalent minimal-start-to-end BPMN string builder already exists under a different name, reuse that one instead of adding a duplicate:

```go
func minimalStartToEndBPMN(processKey string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://itsm">
  <process id="` + processKey + `" isExecutable="true">
    <startEvent id="StartEvent_1" name="Start">
      <outgoing>Flow_1</outgoing>
    </startEvent>
    <endEvent id="EndEvent_1" name="End">
      <incoming>Flow_1</incoming>
    </endEvent>
    <sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="EndEvent_1"/>
  </process>
</definitions>`
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./service/... -run 'TestStartProcess_PopulatesInitiator|TestStartProcess_FallsBackToRequesterID' -v`
Expected: FAIL — `instance.Initiator` is `""` in both cases (field never set today).

- [ ] **Step 3: Write minimal implementation**

```go
// itsm-backend/service/bpmn_process_engine.go — inside StartProcess, replace
// the "4. 创建流程实例" block (current lines 240-255)

	// 4. 创建流程实例
	// initiator 取自当前认证用户（controller 的 getBPMNTenantContext 注入的
	// BPMNUserIDContextKey）；系统触发（无认证用户上下文，例如工单创建自动触发
	// 流程）时，沿用现有的 requester_id 变量约定作为兜底。空字符串（两者都没有）
	// 是允许的——这类实例只能靠任务参与关系被参与者看到，不会出现在任何人的
	// "我发起的" 视图里。
	initiator := ""
	if callerID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int); callerID > 0 {
		initiator = strconv.Itoa(callerID)
	} else if reqID, ok := variables["requester_id"]; ok {
		switch v := reqID.(type) {
		case float64:
			initiator = strconv.Itoa(int(v))
		case int:
			initiator = strconv.Itoa(v)
		case string:
			initiator = v
		}
	}

	instance, err := e.client.ProcessInstance.Create().
		SetProcessInstanceID(fmt.Sprintf("PI-%s-%d", processDefinitionKey, time.Now().UnixNano())).
		SetBusinessKey(businessKey).
		SetProcessDefinitionKey(processDefinitionKey).
		SetProcessDefinitionID(definition.ID).
		SetStatus("running").
		SetVariables(variables).
		SetStartTime(time.Now()).
		SetTenantID(definition.TenantID).
		SetCurrentActivityID(startEvent.ID).
		SetCurrentActivityName(startEvent.Name).
		SetInitiator(initiator).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建流程实例失败: %w", err)
	}
```

Confirm `strconv` is already imported in `bpmn_process_engine.go` (it is, used elsewhere in the file — no import change needed).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./service/... -run 'TestStartProcess_PopulatesInitiator|TestStartProcess_FallsBackToRequesterID' -v`
Expected: PASS

- [ ] **Step 5: Run full package test**

Run: `cd itsm-backend && go build ./... && go test ./service/...`
Expected: PASS, no regressions (StartProcess is called from many existing tests/fixtures — none should assert `Initiator == ""`, but confirm none do).

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go service/bpmn_process_engine_ext_test.go
git commit -m "feat(bpmn): populate ProcessInstance.initiator in StartProcess"
```

---

### Task 4: Elevated-access context key + controller helper

**Files:**
- Modify: `itsm-backend/service/bpmn/handler_base.go` (add context key)
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go` (add helper function)

**Interfaces:**
- Produces: `var BPMNElevatedContextKey = bpmnElevatedKey{}` (service/bpmn package); `func hasElevatedBPMNAccess(ctx *gin.Context, resource, action string) bool` (controller package) — Tasks 5-8 call this from each handler and inject the result into the service-layer context via `context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)`.

- [ ] **Step 1: Add the context key**

```go
// itsm-backend/service/bpmn/handler_base.go — add after BPMNUserIDContextKey (line 23)

type bpmnElevatedKey struct{}

// BPMNElevatedContextKey carries whether the caller holds the elevated
// RBAC permission for the endpoint being served (e.g. process_instance:read,
// task:read, task:update — the specific resource:action pair is decided by
// the controller handler, not here). When true, participant-scoping is
// skipped and the caller sees/acts on data tenant-wide, matching ops-console
// use cases. Must only ever be set from a server-computed
// middleware.HasResourcePermission(...) result — never from client input.
var BPMNElevatedContextKey = bpmnElevatedKey{}
```

- [ ] **Step 2: Add the controller helper**

```go
// itsm-backend/controller/bpmn_workflow_controller.go — add after getBPMNTenantContext (after line 50)

// hasElevatedBPMNAccess reports whether the caller's role holds the given
// RBAC permission (resource:action), computed server-side from context
// values RBACMiddleware already populated (role/tenant_id/client) — never
// from any client-suppliable request field. Used to decide whether a BPMN
// read/action endpoint should return/operate on tenant-wide data (ops
// console use case) or be scoped down to the caller's own participation.
func hasElevatedBPMNAccess(ctx *gin.Context, resource, action string) bool {
	roleAny, exists := ctx.Get("role")
	if !exists {
		return false
	}
	role, _ := roleAny.(string)

	tenantIDAny, exists := ctx.Get("tenant_id")
	if !exists {
		return false
	}
	tenantID, _ := tenantIDAny.(int)

	clientAny, exists := ctx.Get("client")
	if !exists {
		return false
	}
	client, ok := clientAny.(*ent.Client)
	if !ok {
		return false
	}

	return middleware.HasResourcePermission(client, role, resource, action, tenantID)
}
```

`"itsm-backend/ent"` and `"itsm-backend/middleware"` are already imported in this file (see the existing import block) — no import changes needed.

- [ ] **Step 3: Verify it builds**

Run: `cd itsm-backend && go build ./...`
Expected: builds cleanly (this task adds unused-until-Task-5 code; Go does not flag unused package-level funcs/vars, only unused local variables/imports, so this compiles even before it has callers).

- [ ] **Step 4: Commit**

```bash
cd itsm-backend
git add service/bpmn/handler_base.go controller/bpmn_workflow_controller.go
git commit -m "feat(bpmn): add elevated-access context key and permission-check helper"
```

---

### Task 5: `ListProcessInstances` — participant-scoped filtering

**Files:**
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:463-498` (`ListProcessInstances` handler)
- Modify: `itsm-backend/service/bpmn_process_engine.go:2055-2087` (`bpmnProcessInstanceService.ListProcessInstances`)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.BPMNElevatedContextKey`, `bpmn.ResolveCallerIdentity`/`(*bpmn.CallerIdentity)` are NOT needed here (task-level participation is queried directly via Ent predicates, not the in-memory `IsTaskParticipant`) — this task only needs `bpmn.BPMNUserIDContextKey`/`bpmn.BPMNTenantIDContextKey` (already used) plus the new `bpmn.BPMNElevatedContextKey`.
- Produces: `ListProcessInstances` behavior change only — same signature `func (s *bpmnProcessInstanceService) ListProcessInstances(ctx context.Context, req *ListProcessInstancesRequest) ([]*ent.ProcessInstance, int, error)`.

- [ ] **Step 1: Write the failing test**

```go
// Append to itsm-backend/service/bpmn_process_engine_ext_test.go

func TestListProcessInstances_NonElevatedSeesOnlyInitiatedOrParticipated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("other-user").SetEmail("other-user@example.com").SetName("Other User").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	// Instance 1: viewer is the initiator.
	initiatedInstanceID, _ := createProcessFixture(t, engine, tenantID, "list1-initiated")
	_, err = engine.client.ProcessInstance.UpdateOneID(initiatedInstanceID).
		SetInitiator(fmt.Sprintf("%d", viewerID)).Save(context.Background())
	require.NoError(t, err)

	// Instance 2: viewer is not the initiator, but is a task assignee on it.
	participatedInstanceID, participatedTaskID := createProcessFixture(t, engine, tenantID, "list1-participated")
	_, err = engine.client.ProcessInstance.UpdateOneID(participatedInstanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(participatedTaskID).
		SetAssignee(fmt.Sprintf("%d", viewerID)).Save(context.Background())
	require.NoError(t, err)

	// Instance 3: viewer has no relation to it at all.
	unrelatedInstanceID, _ := createProcessFixture(t, engine, tenantID, "list1-unrelated")
	_, err = engine.client.ProcessInstance.UpdateOneID(unrelatedInstanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	// Not elevated.
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	svc := engine.ProcessInstanceService()
	instances, total, err := svc.ListProcessInstances(ctx, &ListProcessInstancesRequest{TenantID: tenantID, Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	ids := make([]int, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.ID)
	}
	assert.ElementsMatch(t, []int{initiatedInstanceID, participatedInstanceID}, ids)
}

func TestListProcessInstances_ElevatedSeesEverythingInTenant(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("other-user2").SetEmail("other-user2@example.com").SetName("Other User 2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	unrelatedInstanceID, _ := createProcessFixture(t, engine, tenantID, "list2-unrelated")
	_, err = engine.client.ProcessInstance.UpdateOneID(unrelatedInstanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	svc := engine.ProcessInstanceService()
	_, total, err := svc.ListProcessInstances(ctx, &ListProcessInstancesRequest{TenantID: tenantID, Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, 1, total, "elevated caller must see the unrelated instance too")
}

func TestListProcessInstances_CrossTenantNeverLeaks(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)

	otherTenant, err := engine.client.Tenant.Create().
		SetName("Other").SetCode("part-other").SetDomain("part-other.example.com").SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	otherInstanceID, _ := createProcessFixture(t, engine, otherTenant.ID, "list3-other-tenant")
	_, err = engine.client.ProcessInstance.UpdateOneID(otherInstanceID).
		SetInitiator(fmt.Sprintf("%d", viewerID)). // same viewer ID, different tenant
		Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true) // even elevated

	svc := engine.ProcessInstanceService()
	_, total, err := svc.ListProcessInstances(ctx, &ListProcessInstancesRequest{TenantID: tenantID, Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "must never return another tenant's instance regardless of elevation")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./service/... -run 'TestListProcessInstances_' -v`
Expected: FAIL — `TestListProcessInstances_NonElevatedSeesOnlyInitiatedOrParticipated` gets `total == 3` (no scoping today); the elevated/cross-tenant tests likely pass already by coincidence (current code is tenant-scoped and unconditionally "sees everything") but re-run all three together since Step 3 changes shared code.

- [ ] **Step 3: Write minimal implementation**

```go
// itsm-backend/service/bpmn_process_engine.go — replace ListProcessInstances (lines 2055-2087)

func (s *bpmnProcessInstanceService) ListProcessInstances(ctx context.Context, req *ListProcessInstancesRequest) ([]*ent.ProcessInstance, int, error) {
	query := s.client.ProcessInstance.Query()

	if req.ProcessDefinitionKey != "" {
		query = query.Where(processinstance.ProcessDefinitionKey(req.ProcessDefinitionKey))
	}
	if req.Status != "" {
		query = query.Where(processinstance.Status(req.Status))
	}
	if req.BusinessKey != "" {
		query = query.Where(processinstance.BusinessKey(req.BusinessKey))
	}
	if req.TenantID > 0 {
		query = query.Where(processinstance.TenantID(req.TenantID))
	}

	elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool)
	if !elevated {
		userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
		if userID <= 0 {
			// No authenticated actor and not elevated: fail closed, see nothing.
			return []*ent.ProcessInstance{}, 0, nil
		}
		userIDStr := strconv.Itoa(userID)

		// Two-step: first find which instances this user participates in via
		// any task (assignee/candidate_users/candidate_groups), then OR that
		// with instances they initiated. Deliberately not a single ent
		// subquery/JOIN — ent's subquery support for "EXISTS a related row
		// matching X" is awkward here and the two simple queries are easier
		// to read, test, and keep tenant-scoped independently.
		identity, err := bpmn.ResolveCallerIdentity(ctx, s.client, bpmn.NewGroupResolver(s.client), req.TenantID, userID)
		if err != nil {
			return nil, 0, fmt.Errorf("解析调用者身份失败: %w", err)
		}
		taskOrPreds := []predicate.ProcessTask{
			processtask.Assignee(identity.IDStr),
			processtask.CandidateUsersContains(identity.IDStr),
		}
		if identity.Username != "" {
			taskOrPreds = append(taskOrPreds, processtask.Assignee(identity.Username), processtask.CandidateUsersContains(identity.Username))
		}
		if identity.Email != "" {
			taskOrPreds = append(taskOrPreds, processtask.Assignee(identity.Email), processtask.CandidateUsersContains(identity.Email))
		}
		for _, group := range strings.Split(identity.GroupsCSV, ",") {
			group = strings.TrimSpace(group)
			if group != "" {
				taskOrPreds = append(taskOrPreds, processtask.CandidateGroupsContains(group))
			}
		}
		taskQuery := s.client.ProcessTask.Query().Where(processtask.Or(taskOrPreds...))
		if req.TenantID > 0 {
			taskQuery = taskQuery.Where(processtask.TenantID(req.TenantID))
		}
		participantTasks, err := taskQuery.All(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("查询参与任务失败: %w", err)
		}
		instanceIDSet := make(map[int]struct{}, len(participantTasks))
		for _, t := range participantTasks {
			instanceIDSet[t.ProcessInstanceID] = struct{}{}
		}
		instanceIDs := make([]int, 0, len(instanceIDSet))
		for id := range instanceIDSet {
			instanceIDs = append(instanceIDs, id)
		}

		if len(instanceIDs) > 0 {
			query = query.Where(processinstance.Or(
				processinstance.Initiator(userIDStr),
				processinstance.IDIn(instanceIDs...),
			))
		} else {
			query = query.Where(processinstance.Initiator(userIDStr))
		}
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取流程实例总数失败: %w", err)
	}

	if req.Page > 0 && req.PageSize > 0 {
		offset := (req.Page - 1) * req.PageSize
		query = query.Offset(offset).Limit(req.PageSize)
	}

	instances, err := query.Order(ent.Desc(processinstance.FieldStartTime)).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取流程实例列表失败: %w", err)
	}

	return instances, total, nil
}
```

`"strings"`, `"itsm-backend/ent/predicate"`, and `"itsm-backend/service/bpmn"` are all already imported in this file — no import changes needed.

```go
// itsm-backend/controller/bpmn_workflow_controller.go — replace ListProcessInstances (lines 463-498)

func (c *BPMNWorkflowController) ListProcessInstances(ctx *gin.Context) {
	var req service.ListProcessInstancesRequest
	if err := ctx.ShouldBindQuery(&req); err != nil {
		common.Fail(ctx, common.ParamErrorCode, "请求参数错误: "+err.Error())
		return
	}

	// 从JWT获取租户ID
	tenantID, exists := ctx.Get("tenant_id")
	if !exists {
		common.AuthFailed(ctx, "未授权访问")
		return
	}
	req.TenantID = tenantID.(int)

	// 设置默认分页参数
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}

	workflowCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	elevated := hasElevatedBPMNAccess(ctx, "process_instance", "read")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)

	instances, total, err := c.processEngine.ProcessInstanceService().ListProcessInstances(workflowCtx, &req)
	if err != nil {
		common.InternalError(ctx, "获取流程实例列表失败: "+err.Error())
		return
	}

	// 使用统一响应格式
	listResponse := common.NewListResponse(
		dto.ToBPMNProcessInstanceListResponse(instances),
		common.NewPaginationResponse(int(req.Page), int(req.PageSize), int64(total)),
	)
	common.Success(ctx, listResponse)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./service/... -run 'TestListProcessInstances_' -v`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Build and run package tests**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS — check for any other existing test that calls `ListProcessInstances` without setting `bpmn.BPMNElevatedContextKey`/`bpmn.BPMNUserIDContextKey`; if one exists and now returns fewer rows than it expects, that test was implicitly relying on the old "always see everything" behavior — update its context setup to add `context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)` (it's testing an ops/admin scenario) rather than weakening the new scoping.

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go controller/bpmn_workflow_controller.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): scope ListProcessInstances to caller participation unless elevated"
```

---

### Task 6: `GetTask`/`GetTaskByID` — participant/initiator/elevated check

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:2213-2240` (`GetTask`, `GetTaskByID`)
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:662-685` (`GetTask` handler)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.ResolveCallerIdentity`, `(*bpmn.CallerIdentity).IsTaskParticipant`, `bpmn.BPMNElevatedContextKey` (all from Tasks 1 and 4).
- Produces: `GetTask(ctx, taskID string) (*ent.ProcessTask, error)` and `GetTaskByID(ctx, id int) (*ent.ProcessTask, error)` now return an error for non-participant, non-elevated callers — same signatures, existing callers (`ClaimTask`, `CompleteTaskByID`, etc. which call `GetTask`/`GetTaskByID` internally) will now also enforce this check as a side effect. This is intentional — those call sites already separately enforce candidate checks before mutating, but today they can freely READ the task first; tightening the read is consistent with the goal of this plan. Verify in Step 5 that no legitimate internal call site breaks (e.g. a system/internal context with no user ID stays permissive, matching `authorizeTaskActor`'s existing convention).

- [ ] **Step 1: Write the failing test**

```go
// Append to itsm-backend/service/bpmn_process_engine_ext_test.go

func TestGetTaskByID_ParticipantCanView(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "gettask1")
	_, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", actorID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	task, err := engine.TaskService().GetTaskByID(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, taskID, task.ID)
}

func TestGetTaskByID_InitiatorCanViewReadOnly(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, initiatorID := setupApprovalDecisionFixture(t, engine)
	instanceID, taskID := createProcessFixture(t, engine, tenantID, "gettask2")
	_, err := engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", initiatorID)).Save(context.Background())
	require.NoError(t, err)
	// Task is assigned to someone else — initiator is not a candidate on it.
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee("someone-else").Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, initiatorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	task, err := engine.TaskService().GetTaskByID(ctx, taskID)
	require.NoError(t, err, "the process initiator must be able to view any task in their own instance")
	assert.Equal(t, taskID, task.ID)
}

func TestGetTaskByID_NonParticipantDenied(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("bystander").SetEmail("bystander@example.com").SetName("Bystander").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	instanceID, taskID := createProcessFixture(t, engine, tenantID, "gettask3")
	_, err = engine.client.ProcessInstance.UpdateOneID(instanceID).
		SetInitiator(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)

	_, err = engine.TaskService().GetTaskByID(ctx, taskID)
	assert.Error(t, err, "a non-participant, non-elevated caller must not be able to view the task")
}

func TestGetTaskByID_ElevatedCanViewAnything(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("bystander2").SetEmail("bystander2@example.com").SetName("Bystander 2").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "gettask4")
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	task, err := engine.TaskService().GetTaskByID(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, taskID, task.ID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./service/... -run 'TestGetTaskByID_' -v`
Expected: FAIL on `TestGetTaskByID_NonParticipantDenied` (currently succeeds with no error — zero authorization today). The other three should already pass coincidentally (no check means everyone can view), but run all four together since Step 3 adds the check that must not break them.

- [ ] **Step 3: Write minimal implementation**

```go
// itsm-backend/service/bpmn_process_engine.go — replace GetTask/GetTaskByID (lines 2213-2240)

func (s *bpmnTaskService) GetTask(ctx context.Context, taskID string) (*ent.ProcessTask, error) {
	query := s.client.ProcessTask.Query().
		Where(processtask.TaskID(taskID))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processtask.TenantID(tenantID))
	}
	task, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务失败: %w", err)
	}
	if err := s.authorizeTaskViewer(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

// GetTaskByID 根据数据库自增ID获取任务
func (s *bpmnTaskService) GetTaskByID(ctx context.Context, id int) (*ent.ProcessTask, error) {
	query := s.client.ProcessTask.Query().
		Where(processtask.ID(id))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		query = query.Where(processtask.TenantID(tenantID))
	}
	task, err := query.First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取任务失败: %w", err)
	}
	if err := s.authorizeTaskViewer(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

// authorizeTaskViewer allows a task to be read by: an elevated caller
// (task:read permission), the task's participant (assignee/candidate_users/
// candidate_groups — see bpmn.CallerIdentity.IsTaskParticipant), or the
// initiator of the task's parent process instance (read-only progress
// visibility — matches the "can I see my own submitted request's approval
// chain" product expectation, distinct from being allowed to act on it).
// System/internal calls without an authenticated actor stay permissive,
// matching authorizeTaskActor's existing convention.
func (s *bpmnTaskService) authorizeTaskViewer(ctx context.Context, task *ent.ProcessTask) error {
	if elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool); elevated {
		return nil
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return nil
	}
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	identity, err := bpmn.ResolveCallerIdentity(ctx, s.client, s.groupResolver, tenantID, userID)
	if err != nil {
		return fmt.Errorf("查看用户不存在: %w", err)
	}
	if identity.IsTaskParticipant(task) {
		return nil
	}
	instance, err := s.client.ProcessInstance.Get(ctx, task.ProcessInstanceID)
	if err == nil && instance.Initiator == identity.IDStr {
		return nil
	}
	return fmt.Errorf("当前用户无权查看该任务")
}
```

- [ ] **Step 4: Wire the elevated context into the controller**

```go
// itsm-backend/controller/bpmn_workflow_controller.go — replace GetTask (lines 662-685)

// GetTask 获取任务
func (c *BPMNWorkflowController) GetTask(ctx *gin.Context) {
	taskID := ctx.Param("id")
	workflowCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	elevated := hasElevatedBPMNAccess(ctx, "task", "read")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)

	// 先尝试解析为数字ID（数据库自增ID）
	id, err := strconv.Atoi(taskID)
	var task interface{}
	if err == nil {
		// 数字ID，使用GetTaskByID
		task, err = c.processEngine.TaskService().GetTaskByID(workflowCtx, id)
	} else {
		// 字符串ID（BPMN标准task_id），使用GetTask
		task, err = c.processEngine.TaskService().GetTask(workflowCtx, taskID)
	}
	if err != nil {
		common.NotFound(ctx, "任务不存在: "+err.Error())
		return
	}

	common.Success(ctx, task)
}
```

Note: reusing `common.NotFound` (unchanged) for the new authorization failure deliberately does not distinguish "task doesn't exist" from "task exists but you can't see it" in the HTTP response — matches the existing minimal-information-disclosure convention already used for every other BPMN error path in this file, not a new design decision.

- [ ] **Step 5: Run test to verify it passes, then full package test**

Run: `cd itsm-backend && go test ./service/... -run 'TestGetTaskByID_' -v`
Expected: PASS (4 tests)

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS. Pay particular attention to any test exercising `ClaimTask`/`ClaimTaskByID`/`CompleteTask`/`CompleteTaskByID`/`SubmitTaskDecision`/`Vote` — they call `GetTask`/`GetTaskByID` internally and now inherit this read-time check. If any fails, check whether its test context sets `bpmn.BPMNUserIDContextKey` to a user who is NOT the task's assignee/candidate (a system/internal-style test that never needed to be) — fix by adding the missing assignee/candidate setup to the fixture rather than weakening `authorizeTaskViewer`.

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go controller/bpmn_workflow_controller.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): GetTask/GetTaskByID require participant, initiator, or elevated access"
```

---

### Task 7: `ListUserTasks` — force-to-self when not elevated, reuse shared helper

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:2259-2320` (`ListUserTasks`)
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:610-659` (`ListUserTasks` handler)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.ResolveCallerIdentity`, `bpmn.BPMNElevatedContextKey`.
- Produces: `ListUserTasks(ctx, req *ListUserTasksRequest) ([]*ent.ProcessTask, int, error)` — same signature. Non-elevated callers now get `req.UserID`/`Assignee`/`CandidateUsers`/`CandidateGroups` overridden to their own identity before the query runs, regardless of what was requested.

- [ ] **Step 1: Write the failing test**

```go
// Append to itsm-backend/service/bpmn_process_engine_ext_test.go

func TestListUserTasks_NonElevatedIgnoresOverrideParams(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("victim").SetEmail("victim@example.com").SetName("Victim").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, viewerTaskID := createProcessFixture(t, engine, tenantID, "listtasks1-mine")
	_, err = engine.client.ProcessTask.UpdateOneID(viewerTaskID).
		SetAssignee(fmt.Sprintf("%d", viewerID)).Save(context.Background())
	require.NoError(t, err)

	_, otherTaskID := createProcessFixture(t, engine, tenantID, "listtasks1-victim")
	_, err = engine.client.ProcessTask.UpdateOneID(otherTaskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	// Attacker-style request: explicitly ask for the OTHER user's tasks.
	req := &ListUserTasksRequest{
		Assignee: fmt.Sprintf("%d", otherUser.ID),
		TenantID: tenantID,
		Page:     1, PageSize: 50,
	}
	tasks, total, err := engine.TaskService().ListUserTasks(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, total, "must be forced back to the caller's own scope, not the requested override")
	assert.Equal(t, viewerTaskID, tasks[0].ID)
}

func TestListUserTasks_ElevatedHonorsOverrideParams(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("target-user").SetEmail("target-user@example.com").SetName("Target").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, otherTaskID := createProcessFixture(t, engine, tenantID, "listtasks2-target")
	_, err = engine.client.ProcessTask.UpdateOneID(otherTaskID).
		SetAssignee(fmt.Sprintf("%d", otherUser.ID)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	req := &ListUserTasksRequest{
		Assignee: fmt.Sprintf("%d", otherUser.ID),
		TenantID: tenantID,
		Page:     1, PageSize: 50,
	}
	tasks, total, err := engine.TaskService().ListUserTasks(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	assert.Equal(t, otherTaskID, tasks[0].ID)
}

func TestListUserTasks_MultiGroupCallerMatchesAnyOfTheirGroups(t *testing.T) {
	// Regression for the fix in this task: the caller's GroupsCSV is now
	// split into individual OR-predicates instead of one
	// CandidateGroupsContains(wholeCSV) check, which only matched
	// single-group callers before.
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, viewerID := setupApprovalDecisionFixture(t, engine)

	_, err := engine.client.Group.Create().
		SetName("dept_manager").SetTenantID(tenantID).AddMemberIDs(viewerID).
		Save(context.Background())
	require.NoError(t, err)
	_, err = engine.client.Group.Create().
		SetName("network_eng").SetTenantID(tenantID).AddMemberIDs(viewerID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "listtasks3-group")
	_, err = engine.client.ProcessTask.UpdateOneID(taskID).
		SetCandidateGroups("network_eng").Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, viewerID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	tasks, total, err := engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
		UserID: viewerID, TenantID: tenantID, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	assert.Equal(t, taskID, tasks[0].ID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./service/... -run 'TestListUserTasks_' -v`
Expected: FAIL on `TestListUserTasks_NonElevatedIgnoresOverrideParams` (currently returns the other user's task — the leak this plan exists to fix) and likely `TestListUserTasks_MultiGroupCallerMatchesAnyOfTheirGroups` (current single-CSV-substring check does not reliably match multi-group callers).

- [ ] **Step 3: Write minimal implementation**

```go
// itsm-backend/service/bpmn_process_engine.go — replace ListUserTasks (lines 2259-2320)

func (s *bpmnTaskService) ListUserTasks(ctx context.Context, req *ListUserTasksRequest) ([]*ent.ProcessTask, int, error) {
	s.logger.Debugw("ListUserTasks called", "assignee", req.Assignee, "userID", req.UserID, "tenantID", req.TenantID)

	elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool)
	if !elevated {
		// Non-elevated callers can never see anyone else's tasks — ignore
		// whatever Assignee/CandidateUsers/CandidateGroups/UserID they sent
		// and force the query back to their own authenticated identity.
		callerID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
		req.UserID = callerID
		req.Assignee = ""
		req.CandidateUsers = ""
		req.CandidateGroups = ""
	}

	query := s.client.ProcessTask.Query()

	// 「我的待办」语义：UserID 透传时，查出"分配给我 OR 我是候选人 OR 我所在组是候选组"的任务。
	if req.UserID > 0 {
		tenantID := req.TenantID
		if tenantID == 0 {
			if v, ok := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); ok {
				tenantID = v
			}
		}
		identity, err := bpmn.ResolveCallerIdentity(ctx, s.client, s.groupResolver, tenantID, req.UserID)
		if err != nil {
			return nil, 0, fmt.Errorf("解析用户身份失败: %w", err)
		}

		orPreds := []predicate.ProcessTask{
			processtask.Assignee(identity.IDStr),
			processtask.CandidateUsersContains(identity.IDStr),
		}
		if identity.Username != "" {
			orPreds = append(orPreds, processtask.Assignee(identity.Username), processtask.CandidateUsersContains(identity.Username))
		}
		if identity.Email != "" {
			orPreds = append(orPreds, processtask.Assignee(identity.Email), processtask.CandidateUsersContains(identity.Email))
		}
		// 每个组各自作为一条 OR 分支——之前把整份 GroupsCSV 当一个整体传给
		// CandidateGroupsContains，只有调用者恰好只属于一个组时才碰巧能匹配上；
		// 多组用户会被漏掉。
		for _, group := range strings.Split(identity.GroupsCSV, ",") {
			group = strings.TrimSpace(group)
			if group != "" {
				orPreds = append(orPreds, processtask.CandidateGroupsContains(group))
			}
		}
		query = query.Where(processtask.Or(orPreds...))
	} else {
		if req.Assignee != "" {
			query = query.Where(processtask.Assignee(req.Assignee))
		}
		if req.CandidateUsers != "" {
			query = query.Where(processtask.CandidateUsersContains(req.CandidateUsers))
		}
		if req.CandidateGroups != "" {
			query = query.Where(processtask.CandidateGroupsContains(req.CandidateGroups))
		}
	}
	if req.Status != "" {
		query = query.Where(processtask.Status(req.Status))
	}
	if req.ProcessDefinitionKey != "" {
		query = query.Where(processtask.ProcessDefinitionKey(req.ProcessDefinitionKey))
	}
	if req.ProcessInstanceID > 0 {
		query = query.Where(processtask.ProcessInstanceID(req.ProcessInstanceID))
	}
	if req.TenantID > 0 {
		query = query.Where(processtask.TenantID(req.TenantID))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取任务总数失败: %w", err)
	}

	if req.Page > 0 && req.PageSize > 0 {
		offset := (req.Page - 1) * req.PageSize
		query = query.Offset(offset).Limit(req.PageSize)
	}

	tasks, err := query.Order(ent.Desc(processtask.FieldCreatedTime)).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("获取任务列表失败: %w", err)
	}

	return tasks, total, nil
}
```

This removes the old inline `s.client.User.Get(ctx, req.UserID)` username/email lookup and the old `s.groupResolver.GetUserGroupNames` call, replacing both with the single `bpmn.ResolveCallerIdentity` call — delete those now-unused lines rather than leaving them dead.

```go
// itsm-backend/controller/bpmn_workflow_controller.go — replace ListUserTasks (lines 610-659)

// ListUserTasks 获取用户任务列表（默认「我的待办」语义）
func (c *BPMNWorkflowController) ListUserTasks(ctx *gin.Context) {
	var req service.ListUserTasksRequest
	if err := ctx.ShouldBindQuery(&req); err != nil {
		common.Fail(ctx, common.ParamErrorCode, "请求参数错误: "+err.Error())
		return
	}

	// 从JWT获取租户ID
	tenantID, exists := ctx.Get("tenant_id")
	if !exists {
		common.AuthFailed(ctx, "未授权访问")
		return
	}
	req.TenantID = tenantID.(int)

	// 如果调用方没有指定 user_id / assignee / candidate_users，则按「我的待办」语义
	// 使用当前登录用户 ID 进行过滤（包含 assign、candidate、组员命中）。这个默认值
	// 在非 elevated 调用方那里其实已经不重要了——service 层会强制覆盖回调用者自己的
	// 身份，不管这里传不传；保留它是为了 elevated 调用方在不显式传参数时也能拿到
	// 合理的默认行为（自己的待办），而不是空条件返回全部。
	if req.UserID <= 0 && req.Assignee == "" && req.CandidateUsers == "" {
		if uid, ok := ctx.Get("user_id"); ok {
			switch v := uid.(type) {
			case int:
				req.UserID = v
			case int64:
				req.UserID = int(v)
			case string:
				if id, err := strconv.Atoi(v); err == nil {
					req.UserID = id
				}
			}
		}
	}

	// 设置默认分页参数
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}

	workflowCtx, _, ok := getBPMNTenantContext(ctx)
	if !ok {
		return
	}
	elevated := hasElevatedBPMNAccess(ctx, "task", "read")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)

	tasks, total, err := c.processEngine.TaskService().ListUserTaskViews(workflowCtx, &req)
	if err != nil {
		common.InternalError(ctx, "获取用户任务列表失败: "+err.Error())
		return
	}

	// 使用统一响应格式
	listResponse := common.NewListResponse(tasks, common.NewPaginationResponse(int(req.Page), int(req.PageSize), int64(total)))
	common.Success(ctx, listResponse)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./service/... -run 'TestListUserTasks_' -v`
Expected: PASS (3 new tests)

- [ ] **Step 5: Build and run full package test**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS. Check especially any pre-existing test asserting on `ListUserTasks`/`ListUserTaskViews` result counts that relied on the old fragile single-group `CandidateGroupsContains(wholeCSV)` behavior — if one now returns MORE matches than before (because multi-group matching is now correct), that's the intended fix, update the expected count rather than reverting the logic.

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/bpmn_process_engine.go controller/bpmn_workflow_controller.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): ListUserTasks ignores caller-supplied override params unless elevated"
```

---

### Task 8: Task-action authorization + audit logging (assign/cancel/setVariables/counter-sign)

**Files:**
- Modify: `itsm-backend/service/bpmn_audit_service.go` (add 3 new `Record*` methods)
- Modify: `itsm-backend/service/bpmn_process_engine.go:2399-2412` (`AssignTask`), `:2501-2512` (`CancelTask`), `:2523-2534` (`SetTaskVariables`), `:2707-2774` (`CreateCounterSignTasks`)
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:688-710` (`AssignTask`), `:779-801` (`CancelTask`), `:804-826` (`SetTaskVariables`), `:1010-1030` (`CreateCounterSignTasks`)
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Consumes: `bpmn.ResolveCallerIdentity`, `(*bpmn.CallerIdentity).IsTaskParticipant`, `bpmn.BPMNElevatedContextKey`, `bpmn.BPMNUserIDContextKey` (all existing/from earlier tasks).
- Produces: `AssignTask(ctx, taskID, assignee string) error`, `CancelTask(ctx, taskID, reason string) error`, `SetTaskVariables(ctx, taskID string, variables map[string]interface{}) error`, `CreateCounterSignTasks(ctx, parentTaskID string, req *CounterSignRequest) ([]*ent.ProcessTask, error)` — same signatures, now authorization-checked and audited. New audit methods: `(*BPMNAuditService) RecordTaskCancelled(ctx, task *ent.ProcessTask, userID int, userName, reason string) error`, `RecordTaskVariablesChanged(ctx, task *ent.ProcessTask, userID int, userName string, before, after map[string]interface{}) error`, `RecordCounterSignCreated(ctx, parentTask *ent.ProcessTask, userID int, userName string, approverCount int) error`.

- [ ] **Step 1: Write the failing tests**

```go
// Append to itsm-backend/service/bpmn_process_engine_ext_test.go

func TestAssignTask_NonParticipantDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	otherUser, err := engine.client.User.Create().
		SetUsername("assignee-target").SetEmail("assignee-target@example.com").SetName("Target").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "assign1")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	notParticipantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, false)

	err = engine.TaskService().AssignTask(notParticipantCtx, task.TaskID, fmt.Sprintf("%d", otherUser.ID))
	assert.Error(t, err, "a non-participant, non-elevated caller must not be able to reassign the task")

	elevatedCtx := context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, true)
	err = engine.TaskService().AssignTask(elevatedCtx, task.TaskID, fmt.Sprintf("%d", otherUser.ID))
	require.NoError(t, err, "an elevated caller must be able to reassign any task")

	auditLogs, err := engine.client.ProcessAuditLog.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, auditLogs, 1)
	assert.Equal(t, AuditActionTaskAssigned, auditLogs[0].Action)
	assert.Equal(t, actorID, auditLogs[0].UserID)
}

func TestCancelTask_NonParticipantDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "cancel1")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	notParticipantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, false)

	err = engine.TaskService().CancelTask(notParticipantCtx, task.TaskID, "no longer needed")
	assert.Error(t, err)

	elevatedCtx := context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, true)
	err = engine.TaskService().CancelTask(elevatedCtx, task.TaskID, "no longer needed")
	require.NoError(t, err)

	auditLogs, err := engine.client.ProcessAuditLog.Query().
		Where(processauditlog.Action(AuditActionTaskCancelled)).All(context.Background())
	require.NoError(t, err)
	require.Len(t, auditLogs, 1)
}

func TestSetTaskVariables_ParticipantAllowedAndAudited(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	_, taskID := createProcessFixture(t, engine, tenantID, "vars1")
	_, err := engine.client.ProcessTask.UpdateOneID(taskID).
		SetAssignee(fmt.Sprintf("%d", actorID)).SetTaskVariables(map[string]interface{}{"comment": "old"}).
		Save(context.Background())
	require.NoError(t, err)
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	ctx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, false)

	err = engine.TaskService().SetTaskVariables(ctx, task.TaskID, map[string]interface{}{"comment": "new"})
	require.NoError(t, err)

	auditLogs, err := engine.client.ProcessAuditLog.Query().
		Where(processauditlog.Action(AuditActionVariableChanged)).All(context.Background())
	require.NoError(t, err)
	require.Len(t, auditLogs, 1)
}

func TestCreateCounterSignTasks_NonParticipantDeniedUnlessElevated(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	approver, err := engine.client.User.Create().
		SetUsername("countersign-approver").SetEmail("countersign-approver@example.com").SetName("Approver").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)

	_, taskID := createProcessFixture(t, engine, tenantID, "countersign1")
	task, err := engine.client.ProcessTask.Get(context.Background(), taskID)
	require.NoError(t, err)

	notParticipantCtx := context.WithValue(baseCtx, bpmn.BPMNUserIDContextKey, actorID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	notParticipantCtx = context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, false)

	_, err = engine.TaskService().CreateCounterSignTasks(notParticipantCtx, task.TaskID, &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{fmt.Sprintf("%d", approver.ID)},
		Threshold:    1,
	})
	assert.Error(t, err)

	elevatedCtx := context.WithValue(notParticipantCtx, bpmn.BPMNElevatedContextKey, true)
	created, err := engine.TaskService().CreateCounterSignTasks(elevatedCtx, task.TaskID, &CounterSignRequest{
		ApprovalType: "parallel",
		Approvers:    []string{fmt.Sprintf("%d", approver.ID)},
		Threshold:    1,
	})
	require.NoError(t, err)
	assert.Len(t, created, 1)

	auditLogs, err := engine.client.ProcessAuditLog.Query().
		Where(processauditlog.Action(AuditActionActivityStarted)).All(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(auditLogs), 1)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./service/... -run 'TestAssignTask_NonParticipant|TestCancelTask_NonParticipant|TestSetTaskVariables_Participant|TestCreateCounterSignTasks_NonParticipant' -v`
Expected: FAIL — no authorization today (the "deny" assertions fail) and no audit rows exist (the `require.Len(..., 1)` assertions fail with 0).

- [ ] **Step 3: Add the new audit-service methods**

```go
// itsm-backend/service/bpmn_audit_service.go — add after RecordTaskAssigned (after line 202, before RecordTaskClaimed)

// RecordTaskCancelled 记录任务取消审计
func (s *BPMNAuditService) RecordTaskCancelled(ctx context.Context, task *ent.ProcessTask, userID int, userName, reason string) error {
	instance, err := s.client.ProcessInstance.Get(ctx, task.ProcessInstanceID)
	processInstanceKey := ""
	processDefinitionID := 0
	tenantID := 0
	if err == nil {
		processInstanceKey = instance.ProcessInstanceID
		processDefinitionID = instance.ProcessDefinitionID
		tenantID = instance.TenantID
	}

	return s.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    task.ProcessInstanceID,
		ProcessInstanceKey:   processInstanceKey,
		ProcessDefinitionKey: task.ProcessDefinitionKey,
		ProcessDefinitionID:  processDefinitionID,
		ActivityID:           task.TaskDefinitionKey,
		ActivityName:         task.TaskName,
		ActivityType:         task.TaskType,
		Action:               AuditActionTaskCancelled,
		UserID:                userID,
		UserName:              userName,
		Comment:               reason,
		TenantID:              tenantID,
	})
}

// RecordTaskVariablesChanged 记录任务变量变更审计（区别于 RecordVariableChanged，后者是
// 流程实例级变量；这是任务自身的 TaskVariables）
func (s *BPMNAuditService) RecordTaskVariablesChanged(ctx context.Context, task *ent.ProcessTask, userID int, userName string, before, after map[string]interface{}) error {
	instance, err := s.client.ProcessInstance.Get(ctx, task.ProcessInstanceID)
	processInstanceKey := ""
	processDefinitionID := 0
	tenantID := 0
	if err == nil {
		processInstanceKey = instance.ProcessInstanceID
		processDefinitionID = instance.ProcessDefinitionID
		tenantID = instance.TenantID
	}

	return s.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    task.ProcessInstanceID,
		ProcessInstanceKey:   processInstanceKey,
		ProcessDefinitionKey: task.ProcessDefinitionKey,
		ProcessDefinitionID:  processDefinitionID,
		ActivityID:           task.TaskDefinitionKey,
		ActivityName:         task.TaskName,
		ActivityType:         task.TaskType,
		Action:               AuditActionVariableChanged,
		UserID:               userID,
		UserName:             userName,
		VariablesBefore:      before,
		VariablesAfter:       after,
		TenantID:             tenantID,
	})
}

// RecordCounterSignCreated 记录会签任务创建审计
func (s *BPMNAuditService) RecordCounterSignCreated(ctx context.Context, parentTask *ent.ProcessTask, userID int, userName string, approverCount int) error {
	instance, err := s.client.ProcessInstance.Get(ctx, parentTask.ProcessInstanceID)
	processInstanceKey := ""
	processDefinitionID := 0
	tenantID := 0
	if err == nil {
		processInstanceKey = instance.ProcessInstanceID
		processDefinitionID = instance.ProcessDefinitionID
		tenantID = instance.TenantID
	}

	return s.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    parentTask.ProcessInstanceID,
		ProcessInstanceKey:   processInstanceKey,
		ProcessDefinitionKey: parentTask.ProcessDefinitionKey,
		ProcessDefinitionID:  processDefinitionID,
		ActivityID:           parentTask.TaskDefinitionKey,
		ActivityName:         parentTask.TaskName,
		ActivityType:         parentTask.TaskType,
		Action:               AuditActionActivityStarted,
		UserID:               userID,
		UserName:             userName,
		Comment:               fmt.Sprintf("创建 %d 个会签任务", approverCount),
		TenantID:              tenantID,
	})
}
```

- [ ] **Step 4: Wire authorization + audit into the 4 service methods**

```go
// itsm-backend/service/bpmn_process_engine.go — replace AssignTask (lines 2399-2412)

func (s *bpmnTaskService) AssignTask(ctx context.Context, taskID string, assignee string) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	actorID, actorName, err := s.authorizeTaskMutation(ctx, task)
	if err != nil {
		return err
	}

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetAssignee(assignee).
		SetStatus(common.ProcessTaskStatusAssigned).
		SetAssignedTime(time.Now()).
		Save(ctx)
	if err != nil {
		return err
	}

	var assigneeUser *ent.User
	if assigneeID, convErr := strconv.Atoi(assignee); convErr == nil {
		assigneeUser, _ = s.client.User.Get(ctx, assigneeID)
	}
	if auditErr := s.auditService.RecordTaskAssigned(ctx, task, assigneeUser, actorID, actorName); auditErr != nil {
		s.logger.Warnw("audit record failed", "error", auditErr, "task_id", task.TaskID)
	}
	return nil
}
```

```go
// itsm-backend/service/bpmn_process_engine.go — replace CancelTask (lines 2501-2512)

func (s *bpmnTaskService) CancelTask(ctx context.Context, taskID string, reason string) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	actorID, actorName, err := s.authorizeTaskMutation(ctx, task)
	if err != nil {
		return err
	}

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetStatus("cancelled").
		Save(ctx)
	if err != nil {
		return err
	}

	if auditErr := s.auditService.RecordTaskCancelled(ctx, task, actorID, actorName, reason); auditErr != nil {
		s.logger.Warnw("audit record failed", "error", auditErr, "task_id", task.TaskID)
	}
	return nil
}
```

```go
// itsm-backend/service/bpmn_process_engine.go — replace SetTaskVariables (lines 2523-2534)

func (s *bpmnTaskService) SetTaskVariables(ctx context.Context, taskID string, variables map[string]interface{}) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	actorID, actorName, err := s.authorizeTaskMutation(ctx, task)
	if err != nil {
		return err
	}
	before := task.TaskVariables

	_, err = s.client.ProcessTask.UpdateOne(task).
		SetTaskVariables(variables).
		Save(ctx)
	if err != nil {
		return err
	}

	if auditErr := s.auditService.RecordTaskVariablesChanged(ctx, task, actorID, actorName, before, variables); auditErr != nil {
		s.logger.Warnw("audit record failed", "error", auditErr, "task_id", task.TaskID)
	}
	return nil
}
```

```go
// itsm-backend/service/bpmn_process_engine.go — replace CreateCounterSignTasks (lines 2707-2771)
//
// NOTE, flag to the user before implementing this task: the existing parentTask lookup
// below (`s.client.ProcessTask.Query().Where(processtask.TaskID(parentTaskID)).First(ctx)`)
// has NO TenantID filter at all — a caller who knows/guesses a parentTaskID from a
// DIFFERENT tenant can create counter-sign tasks against it today. This is a different bug
// class (tenant isolation) than what this plan's spec scoped (participant-level
// authorization on top of already-existing tenant isolation) — it was found while writing
// this task, not part of the original spec. Left unfixed below, matching scope discipline
// (AGENTS.md: "Do NOT expand scope beyond the reported issue... list them for the user to
// decide"). If the user wants it fixed in the same pass, add
// `.Where(processtask.TenantID(tenantID))` (tenantID from `ctx.Value(bpmn.BPMNTenantIDContextKey)`)
// to the query below and add a cross-tenant regression test alongside Step 1's tests.

func (s *bpmnTaskService) CreateCounterSignTasks(ctx context.Context, parentTaskID string, req *CounterSignRequest) ([]*ent.ProcessTask, error) {
	// 获取父任务
	parentTask, err := s.client.ProcessTask.Query().
		Where(processtask.TaskID(parentTaskID)).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取父任务失败: %w", err)
	}

	actorID, actorName, err := s.authorizeTaskMutation(ctx, parentTask)
	if err != nil {
		return nil, err
	}

	// 生成根任务ID（如果是第一个会签任务）
	rootTaskID := parentTaskID
	if parentTask.RootTaskID != "" {
		rootTaskID = parentTask.RootTaskID
	}

	threshold := req.Threshold
	if threshold == 0 {
		threshold = len(req.Approvers)
	}

	var tasks []*ent.ProcessTask
	for i, approver := range req.Approvers {
		taskID := fmt.Sprintf("%s_countersign_%d", parentTaskID, i)
		status := common.ProcessTaskStatusAssigned
		if req.ApprovalType == "serial" && i > 0 {
			status = "created"
		}
		task, err := s.client.ProcessTask.Create().
			SetTaskID(taskID).
			SetProcessInstanceID(parentTask.ProcessInstanceID).
			SetProcessDefinitionKey(parentTask.ProcessDefinitionKey).
			SetTaskDefinitionKey(parentTask.TaskDefinitionKey + "_counter").
			SetTaskName(parentTask.TaskName + "_会签").
			SetTaskType("user_task").
			SetAssignee(approver).
			SetStatus(status).
			SetPriority(parentTask.Priority).
			SetParentTaskID(parentTaskID).
			SetRootTaskID(rootTaskID).
			SetTenantID(parentTask.TenantID).
			SetCreatedTime(time.Now()).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("创建会签任务失败: %w", err)
		}
		tasks = append(tasks, task)
	}

	// 更新父任务状态为会签中
	_, err = s.client.ProcessTask.UpdateOneID(parentTask.ID).
		SetTaskVariables(map[string]interface{}{
			"approval_type": req.ApprovalType,
			"threshold":     threshold,
			"total":         len(req.Approvers),
			"completed":     0,
			"approved":      0,
			"rejected":      0,
		}).
		Save(ctx)
	if err != nil {
		s.logger.Warnf("更新父任务变量失败: %v", err)
	}

	if auditErr := s.auditService.RecordCounterSignCreated(ctx, parentTask, actorID, actorName, len(req.Approvers)); auditErr != nil {
		s.logger.Warnw("audit record failed", "error", auditErr, "task_id", parentTask.TaskID)
	}

	return tasks, nil
}
```

Now add the shared `authorizeTaskMutation` helper (elevated bypass + participant check + actor identity lookup, reused by all four call sites above):

```go
// itsm-backend/service/bpmn_process_engine.go — add near isTaskCandidate/authorizeTaskViewer

// authorizeTaskMutation gates assign/cancel/setVariables/counter-sign the
// same way authorizeTaskActor gates claim/complete: elevated callers
// (task:update permission) bypass the check entirely (managing a stuck task
// is a legitimate admin action); everyone else must be the task's
// assignee/candidate. Returns the resolved actor's ID and display name for
// the caller to pass into the corresponding BPMNAuditService.Record* call —
// every mutation this guards must be audited, elevated bypass or not.
//
// IMPORTANT (ruling recorded during Task 2's fix loop, see ledger): this must
// use the same two-tier check + requester exclusion as authorizeTaskActor/
// isTaskCandidate, NOT the combined IsTaskParticipant. candidate_groups is a
// LIVE re-evaluated check, not pre-filtered at task-creation time the way
// candidate_users is (createUserTask's excludeUserFromCandidates only
// filters candidate_users) — a requester who happens to belong to the
// configured approval group must not be able to bypass self-approval
// prevention by cancelling/reassigning/editing variables on their own
// approval task via that group membership. Reassignment/cancellation of
// one's own approval request is the same segregation-of-duties concern as
// self-approval, so this exclusion applies here too — unlike the read-only
// authorizeTaskViewer (Task 6), where viewing one's own request's progress
// is not a segregation-of-duties issue and needs no exclusion.
func (s *bpmnTaskService) authorizeTaskMutation(ctx context.Context, task *ent.ProcessTask) (actorID int, actorName string, err error) {
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return 0, "", nil // system/internal call, matches authorizeTaskActor's existing convention
	}
	elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool)
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID == 0 {
		tenantID = task.TenantID
	}
	actor, actorErr := s.client.User.Query().Where(user.ID(userID), user.TenantID(tenantID)).Only(ctx)
	if actorErr != nil {
		return 0, "", fmt.Errorf("操作用户不存在: %w", actorErr)
	}
	if elevated {
		return userID, actor.Name, nil
	}
	identity, identErr := bpmn.ResolveCallerIdentity(ctx, s.client, s.groupResolver, tenantID, userID)
	if identErr != nil {
		return 0, "", fmt.Errorf("解析调用者身份失败: %w", identErr)
	}
	if identity.MatchesAssigneeOrCandidateUser(task) {
		return userID, actor.Name, nil
	}
	if identity.MatchesCandidateGroup(task) {
		isRequester, reqErr := isProcessInstanceRequester(ctx, s.client, task.ProcessInstanceID, tenantID, userID)
		if reqErr != nil {
			return 0, "", fmt.Errorf("校验申请人身份失败: %w", reqErr)
		}
		if !isRequester {
			return userID, actor.Name, nil
		}
	}
	return 0, "", fmt.Errorf("当前用户不是该任务的审批人或候选人")
}
```

`MatchesAssigneeOrCandidateUser`, `MatchesCandidateGroup`, and the standalone `isProcessInstanceRequester(ctx, client, processInstanceID, tenantID, userID) (bool, error)` helper are added in Task 2's fix rounds (see that task's section above and the ledger) — by the time Task 8 runs, they already exist in `service/bpmn/participation.go` and `service/bpmn_process_engine.go` respectively (`isProcessInstanceRequester` takes `tenantID` — its own `ProcessInstance` lookup is tenant-scoped, per Task 2's second fix round). Do not reintroduce a call to the combined `IsTaskParticipant` here.

`bpmnTaskService` does not currently have an `auditService` field (only `CustomProcessEngine` does) — add one:

```go
// itsm-backend/service/bpmn_process_engine.go — replace the bpmnTaskService struct (current lines 2206-2210)

type bpmnTaskService struct {
	client        *ent.Client
	logger        *zap.SugaredLogger
	groupResolver *bpmn.GroupResolver
	auditService  *BPMNAuditService
}
```

Wire it at both construction sites:

```go
// itsm-backend/service/bpmn_process_engine.go line 128, inside NewCustomProcessEngine —
// this line currently runs BEFORE engine.auditService is assigned (that happens on the
// next line, 129, per the code already read). Reorder so auditService is constructed
// first, then reference it:

	engine.auditService = NewBPMNAuditService(client, logger)
	engine.taskService = &bpmnTaskService{client: client, logger: logger, groupResolver: engine.groupResolver, auditService: engine.auditService}
```

```go
// itsm-backend/service/bpmn_process_engine.go line 202, inside CustomProcessEngine's
// TaskService() method — add auditService: e.auditService to the existing struct literal:

	return &bpmnTaskService{client: e.client, logger: e.logger, groupResolver: e.groupResolver, auditService: e.auditService}
```

- [ ] **Step 5: Wire `elevated` into the 4 controller handlers**

```go
// itsm-backend/controller/bpmn_workflow_controller.go — AssignTask (lines 688-710),
// add these two lines right after the existing getBPMNTenantContext call, same pattern
// as Task 5/6/7:
	elevated := hasElevatedBPMNAccess(ctx, "task", "update")
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNElevatedContextKey, elevated)
```

Apply the identical two-line addition (same `"task", "update"` resource:action pair) to `CancelTask` (lines 779-801), `SetTaskVariables` (lines 804-826), and `CreateCounterSignTasks` (lines 1010-1030), each right after their existing `getBPMNTenantContext(ctx)` call, before the service call that follows it.

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd itsm-backend && go test ./service/... -run 'TestAssignTask_NonParticipant|TestCancelTask_NonParticipant|TestSetTaskVariables_Participant|TestCreateCounterSignTasks_NonParticipant' -v`
Expected: PASS (4 tests)

- [ ] **Step 7: Build and run full package test**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/...`
Expected: PASS. `CreateCounterSignTasks`'s existing tests may need their context updated to include `bpmn.BPMNUserIDContextKey`/`bpmn.BPMNElevatedContextKey` if they previously called it with a bare `context.Background()` — add `context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)` to those (they're testing counter-sign mechanics, not authorization, so elevated bypass keeps them focused on their original assertion).

- [ ] **Step 8: Commit**

```bash
cd itsm-backend
git add service/bpmn_audit_service.go service/bpmn_process_engine.go controller/bpmn_workflow_controller.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): require participant/elevated access + audit assign/cancel/setVariables/counter-sign"
```

---

### Task 9: Full regression pass

**Files:** none (verification only)

- [ ] **Step 1: Full build**

Run: `cd itsm-backend && go build ./...`
Expected: clean build, no errors.

- [ ] **Step 2: Full test suite**

Run: `cd itsm-backend && go test ./...`
Expected: all packages pass. Report the pass count (package count + no FAIL lines), per AGENTS.md's "Always run the full test suite after multi-file refactors and report the pass count."

- [ ] **Step 3: Spot-check the spec's testing plan is fully covered**

Cross-check against `docs/superpowers/specs/2026-08-25-bpmn-task-instance-authorization-design.md`'s "测试计划" section: participant-sees-own / non-participant-denied / cross-tenant-denied / elevated-sees-all covered for `ListProcessInstances` (Task 5), `GetTask` (Task 6), `ListUserTasks` (Task 7), and the 4 task actions (Task 8); `candidate_groups` regression covered (Tasks 2, 7); `initiator` backfill + system-triggered fallback covered (Task 3); audit records asserted for all 4 mutating actions (Task 8). If any cell is missing, add the missing test now rather than closing this plan out with a gap.

- [ ] **Step 4: No commit needed** — this task is verification-only; if Step 3 uncovered a gap, the fix belongs in the relevant earlier task's file and should be committed there, not as a new ad hoc task.
