# Final Whole-Plan Review Fix Report

Date: 2026-08-08
Branch: `feat/service-request-ticket-unification`
Worktree: `/home/administrator/project/itsm/.claude/worktrees/sr-ticket-unification`

## Scope

Fixes two findings from the final whole-plan security review of the prior
ServiceRequest-to-Ticket self-approval-gap fix in `service/bpmn_process_engine.go`:

- Finding 1 (Critical): `ClaimTask`/`ClaimTaskByID` let any authenticated user claim
  any unassigned task, with no check that the claimant is an actual candidate.
- Finding 2 (Important): `createUserTask`'s approval branch only checked
  `task.CandidateGroups` before running department-manager resolution, missing the
  case where a BPMN approval node declares `candidateUsers` without `candidateGroups`.

Two other findings from the same review (process-definition redeploy-on-restart not
picking up new XML content, and the `Activity_Assign` dual-unconditional-flow BPMN
topology bug) were explicitly out of scope and were **not** touched. Neither
`service/bpmn_template_service.go` nor any `.bpmn` file was modified.

## Finding 1 fix — candidate check on claim

Added `isTaskCandidate` (service/bpmn_process_engine.go, placed immediately before
`ClaimTask`) — a free function taking `(ctx, client, userID int, task *ent.ProcessTask)`
that mirrors `authorizeTaskActor`'s existing matching semantics exactly (candidate CSV
split on `,`, trimmed, matched against the user's decimal ID string or username; checks
both `task.Assignee` and `task.CandidateUsers`). `authorizeTaskActor` itself was left
untouched, per the plan's explicit instruction — this is a small, deliberately
duplicated check rather than a refactor of a function already reviewed clean.

Wired into both claim paths, right after the existing "already assigned" check and
before the `UpdateOne(...).SetAssignee(...)` call:

- `ClaimTask(ctx, taskID string, userID string)` — parses `userID` via `strconv.Atoi`
  (rejecting non-positive/invalid values with `"无效的用户ID"`), then calls
  `isTaskCandidate`; returns `"当前用户不是该任务的候选人，无法认领"` if not a candidate.
- `ClaimTaskByID(ctx, id int, userID int)` — calls `isTaskCandidate` directly with the
  already-int `userID`; same rejection message.

No new imports were needed — `strconv`, `strings`, and the `ent/user` package were
already imported and used elsewhere in the file (e.g. `authorizeTaskActor` at line 406).

This does not change behavior for any task that already has a real assignee or
non-empty `candidate_users` — it only closes the gap where a task has neither (which
`authorizeTaskActor` already treats as "nobody can complete this" today), and the case
where someone with no claim to the task tries to grab it.

## Finding 2 fix — guard also checks CandidateUsers

In `createUserTask`'s approval branch, changed:

```go
if strings.TrimSpace(task.CandidateGroups) == "" {
    assignee = e.resolveApprovalAssignee(ctx, instance, approvalRequester)
}
```

to:

```go
if strings.TrimSpace(task.CandidateGroups) == "" && strings.TrimSpace(task.CandidateUsers) == "" {
    assignee = e.resolveApprovalAssignee(ctx, instance, approvalRequester)
}
```

and extended the comment above it to explain the `candidateUsers` case (BPMN nodes
authored via the workflow designer that set explicit `candidateUsers` without
`candidateGroups` should not have department-manager resolution silently override
them).

## Tests added

All in `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go`, reusing
the existing `approvalAssignmentFixture` helpers (`fx.createUser`, `fx.createDepartment`,
`fx.createGroup`, `fx.createInstance`, `approvalTask`, `fx.getCreatedTask`):

- `TestCreateUserTask_Approval_ExplicitCandidateUsersSkipsManagerPath` (Finding 2):
  requester's department has a real manager (so manager-path resolution would
  otherwise succeed); the BPMN task sets `task.CandidateUsers` to an explicit approver
  ID with no `CandidateGroups`. Asserts `task.Assignee` stays empty and the explicit
  `CandidateUsers` value is preserved in `candidate_users`, and that the unrelated
  department manager is not mixed in.
- `TestClaimTaskByID_RequesterCannotClaimOwnCandidateGroupFallbackTask` (Finding 1):
  requester's approval task falls back to the `ticket-approvers` candidate group, which
  does not include the requester (excluded per the prior fix). Asserts
  `TaskService().ClaimTaskByID(ctx, task.ID, requester.ID)` returns an error.
- `TestClaimTaskByID_RealCandidateCanClaim` (Finding 1): a real member of the
  fallback candidate group successfully claims via `ClaimTaskByID`, and the task's
  `Assignee` is set to that user's ID afterward.

Accessed the task service via `fx.engine.TaskService()` (the existing
`CustomProcessEngine.TaskService()` getter, which returns the `TaskService` interface
including `ClaimTaskByID`).

## Documentation

Added item 8 to the "Security Hardening" section of `docs/operations.md`, documenting
that the `ticket-approvers` fallback group (named by the `approvalFallbackCandidateGroup`
constant in `service/bpmn_process_engine.go`) must be created via `/admin/groups` with
at least 2 members before going live, and explaining why (self-approval exclusion plus
the new claim-side candidate check can otherwise leave a task with no eligible
claimant).

## Verification

### `go build ./...`

Passed with no output (no errors).

### Targeted tests

```
go test ./service/... -run 'TestCreateUserTask_Approval|TestClaimTask|TestAuthorizeTaskActor' -v
```

Full output:

```
=== RUN   TestCreateUserTask_Approval_ManagerPath_AssignsDeptManager
    logger.go:146: 2026-08-08T19:31:44.930+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": "1"}
--- PASS: TestCreateUserTask_Approval_ManagerPath_AssignsDeptManager (0.02s)
=== RUN   TestCreateUserTask_Approval_ManagerPath_ParentDepartmentFallback
    logger.go:146: 2026-08-08T19:31:44.952+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": "1"}
--- PASS: TestCreateUserTask_Approval_ManagerPath_ParentDepartmentFallback (0.02s)
=== RUN   TestCreateUserTask_Approval_FallbackPath_NoManager_UsesCandidateGroup
    logger.go:146: 2026-08-08T19:31:44.970+0800	INFO	审批任务未解析到部门负责人，转候选组兜底	{"requesterID": 1, "departmentID": 1, "error": "no manager found for department 1 or its ancestors"}
    logger.go:146: 2026-08-08T19:31:44.970+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": ["approverA", "approverB"]}
    logger.go:146: 2026-08-08T19:31:44.970+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestCreateUserTask_Approval_FallbackPath_NoManager_UsesCandidateGroup (0.02s)
=== RUN   TestCreateUserTask_Approval_ManagerPath_SkipsWhenManagerIsRequester
    logger.go:146: 2026-08-08T19:31:44.992+0800	INFO	部门负责人是申请人本人，转候选组兜底，避免自己审批自己	{"requesterID": 1, "departmentID": 1}
    logger.go:146: 2026-08-08T19:31:44.992+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": ["backupApprover"]}
    logger.go:146: 2026-08-08T19:31:44.992+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestCreateUserTask_Approval_ManagerPath_SkipsWhenManagerIsRequester (0.02s)
=== RUN   TestCreateUserTask_Approval_FallbackPath_ExcludesRequesterFromCandidateGroup
    logger.go:146: 2026-08-08T19:31:45.016+0800	INFO	审批任务未解析到部门负责人，转候选组兜底	{"requesterID": 1, "departmentID": 1, "error": "no manager found for department 1 or its ancestors"}
    logger.go:146: 2026-08-08T19:31:45.016+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": ["backupApprover2"]}
    logger.go:146: 2026-08-08T19:31:45.016+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestCreateUserTask_Approval_FallbackPath_ExcludesRequesterFromCandidateGroup (0.02s)
=== RUN   TestCreateUserTask_Approval_FallbackPath_EmptyAfterExclusion_NoOrphanRequester
    logger.go:146: 2026-08-08T19:31:45.034+0800	INFO	审批任务未解析到部门负责人，转候选组兜底	{"requesterID": 1, "departmentID": 1, "error": "no manager found for department 1 or its ancestors"}
    logger.go:146: 2026-08-08T19:31:45.035+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": []}
    logger.go:146: 2026-08-08T19:31:45.035+0800	WARN	审批任务没有解析到任何审批人（部门负责人未配置，候选组展开后也为空），任务将无人可领	{"taskID": "Activity_Approval", "taskName": "工单审批", "candidateGroups": "ticket-approvers"}
    logger.go:146: 2026-08-08T19:31:45.035+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestCreateUserTask_Approval_FallbackPath_EmptyAfterExclusion_NoOrphanRequester (0.02s)
=== RUN   TestCreateUserTask_Approval_IgnoresAssigneeIDVariable
    logger.go:146: 2026-08-08T19:31:45.053+0800	INFO	审批任务未解析到部门负责人，转候选组兜底	{"requesterID": 1, "departmentID": 1, "error": "no manager found for department 1 or its ancestors"}
    logger.go:146: 2026-08-08T19:31:45.053+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": []}
    logger.go:146: 2026-08-08T19:31:45.053+0800	WARN	审批任务没有解析到任何审批人（部门负责人未配置，候选组展开后也为空），任务将无人可领	{"taskID": "Activity_Approval", "taskName": "工单审批", "candidateGroups": "ticket-approvers"}
    logger.go:146: 2026-08-08T19:31:45.053+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestCreateUserTask_Approval_IgnoresAssigneeIDVariable (0.02s)
=== RUN   TestCreateUserTask_Approval_ExplicitCandidateGroupsSkipsManagerPath
    logger.go:146: 2026-08-08T19:31:45.072+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "legacy-role-approvers", "expandedUsers": ["legacyApprover"]}
    logger.go:146: 2026-08-08T19:31:45.073+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestCreateUserTask_Approval_ExplicitCandidateGroupsSkipsManagerPath (0.02s)
=== RUN   TestCreateUserTask_Approval_TenantIsolation_DepartmentNotVisibleAcrossTenants
    logger.go:146: 2026-08-08T19:31:45.094+0800	INFO	审批任务未解析到部门负责人，转候选组兜底	{"requesterID": 2, "departmentID": 1, "error": "department not found: 1"}
    logger.go:146: 2026-08-08T19:31:45.094+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": ["backupApprover3"]}
    logger.go:146: 2026-08-08T19:31:45.094+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestCreateUserTask_Approval_TenantIsolation_DepartmentNotVisibleAcrossTenants (0.02s)
=== RUN   TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask_ManagerPath
    logger.go:146: 2026-08-08T19:31:45.110+0800	INFO	部门负责人是申请人本人，转候选组兜底，避免自己审批自己	{"requesterID": 1, "departmentID": 1}
    logger.go:146: 2026-08-08T19:31:45.110+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": ["backupApprover4"]}
    logger.go:146: 2026-08-08T19:31:45.110+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask_ManagerPath (0.02s)
=== RUN   TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask_CandidateGroupPath
    logger.go:146: 2026-08-08T19:31:45.128+0800	INFO	审批任务未解析到部门负责人，转候选组兜底	{"requesterID": 1, "departmentID": 1, "error": "no manager found for department 1 or its ancestors"}
    logger.go:146: 2026-08-08T19:31:45.128+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": ["backupApprover5"]}
    logger.go:146: 2026-08-08T19:31:45.128+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask_CandidateGroupPath (0.02s)
=== RUN   TestCreateUserTask_Approval_ExplicitCandidateUsersSkipsManagerPath
    logger.go:146: 2026-08-08T19:31:45.147+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": []}
    logger.go:146: 2026-08-08T19:31:45.147+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestCreateUserTask_Approval_ExplicitCandidateUsersSkipsManagerPath (0.02s)
=== RUN   TestClaimTaskByID_RequesterCannotClaimOwnCandidateGroupFallbackTask
    logger.go:146: 2026-08-08T19:31:45.166+0800	INFO	审批任务未解析到部门负责人，转候选组兜底	{"requesterID": 1, "departmentID": 1, "error": "no manager found for department 1 or its ancestors"}
    logger.go:146: 2026-08-08T19:31:45.166+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": ["backupApprover6"]}
    logger.go:146: 2026-08-08T19:31:45.166+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestClaimTaskByID_RequesterCannotClaimOwnCandidateGroupFallbackTask (0.02s)
=== RUN   TestClaimTaskByID_RealCandidateCanClaim
    logger.go:146: 2026-08-08T19:31:45.184+0800	INFO	审批任务未解析到部门负责人，转候选组兜底	{"requesterID": 1, "departmentID": 1, "error": "no manager found for department 1 or its ancestors"}
    logger.go:146: 2026-08-08T19:31:45.184+0800	INFO	审批组已展开	{"taskID": "Activity_Approval", "candidateGroups": "ticket-approvers", "expandedUsers": ["backupApprover7"]}
    logger.go:146: 2026-08-08T19:31:45.185+0800	INFO	User task created with auto-assignment	{"taskID": "Activity_Approval", "taskName": "工单审批", "assignee": ""}
--- PASS: TestClaimTaskByID_RealCandidateCanClaim (0.02s)
=== RUN   TestAuthorizeTaskActor_AllowsAssigneeAndCandidate
--- PASS: TestAuthorizeTaskActor_AllowsAssigneeAndCandidate (0.02s)
=== RUN   TestAuthorizeTaskActor_NoActorContextIsPermissive
--- PASS: TestAuthorizeTaskActor_NoActorContextIsPermissive (0.02s)
PASS
ok  	itsm-backend/service	(cached)
testing: warning: no tests to run
PASS
ok  	itsm-backend/service/approver	(cached) [no tests to run]
testing: warning: no tests to run
PASS
ok  	itsm-backend/service/bpmn	(cached) [no tests to run]
testing: warning: no tests to run
PASS
ok  	itsm-backend/service/cloud	(cached) [no tests to run]
?   	itsm-backend/service/cloud/aliyun	[no test files]
?   	itsm-backend/service/common/event	[no test files]
testing: warning: no tests to run
PASS
ok  	itsm-backend/service/marketplace	(cached) [no tests to run]
?   	itsm-backend/service/scenario	[no test files]
```

All 16 matched tests pass, including the 3 new ones
(`TestCreateUserTask_Approval_ExplicitCandidateUsersSkipsManagerPath`,
`TestClaimTaskByID_RequesterCannotClaimOwnCandidateGroupFallbackTask`,
`TestClaimTaskByID_RealCandidateCanClaim`).

### Full regression sweep

```
go test ./... 2>&1 | grep -v "^ok"
```

Output contained only `?   ...  [no test files]` lines for packages with no tests
(ent generated subpackages, cmd/, config, connector/builtin/console, etc.) — **no
`FAIL` lines**. All packages with tests reported `ok`, including the packages exercising
this change directly:

```
ok  	itsm-backend/controller	19.237s
ok  	itsm-backend/handlers/service_request	1.948s
ok  	itsm-backend/router	0.521s
ok  	itsm-backend/service	16.588s
ok  	itsm-backend/service/approver	(cached)
ok  	itsm-backend/service/bpmn	(cached)
```

(Full list of 36 `ok` package results and the "no test files" list were reviewed; no
FAIL anywhere in the sweep.)

## Files touched

- `itsm-backend/service/bpmn_process_engine.go` — added `isTaskCandidate` helper,
  wired into `ClaimTask`/`ClaimTaskByID` (Finding 1); extended the approval
  auto-assignment guard to also check `task.CandidateUsers` (Finding 2).
- `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go` — added 3
  new tests (see above).
- `docs/operations.md` — added Security Hardening item 8 documenting the
  `ticket-approvers` group provisioning requirement.

Not touched (explicitly out of scope): `service/bpmn_template_service.go`, any `.bpmn`
XML file.

## Commit

See git log for the commit hash covering these three files.
