# WorkItem Task Assignment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Explicit fulfillment tasks follow the current WorkItem assignee, with authoritative permissions, terminal history, atomic reassignment audit and reliable notification.

**Architecture:** Extend the existing BPMN task model and commands. WorkItem remains the only current assignee source for bound tasks; reuse professional authorization and existing Outbox infrastructure. Consolidate reachable assignment mutations below HTTP and above persistence without importing application services from repositories or callbacks.

**Tech Stack:** Go/Gin/Ent, Postgres, existing Outbox worker, Next.js/TypeScript/Ant Design, Jest and Playwright.

**Spec:** [Accepted execution scope](../specs/2026-09-14-work-item-task-assignment-design.md), revision ec1901f8.

**Status:** accepted for sequential execution by the user's subagent-driven-development request; implementation in progress.

**Branch/base:** codex/feat/work-item-task-assignment, ec1901f8. This branch stacks on PR #24 and the runtime prerequisites; do not merge upstream branches or main while tasks run.

## Global Constraints

- WorkItem.assignee_id 是绑定执行任务的当前负责人来源，任务只存不可变的绑定方式。
- 绑定任务的持久化 assignee 保持空值；候选字段也为空。
- 绑定任务不允许单独领取、分配或委派；其改派入口是已授权的工单分配。
- 明确 elevated 权限只允许豁免参与者限制，不能跳过上述业务权限交集。
- 所有取消入口逐任务原子保存终态负责人和实际操作人；终态不随之后改派变化。
- 负责人变更、审计和通知事件在同一事务持久化，事件写入失败则整体回滚。
- 统一锁顺序 WorkItem → ProcessInstance → ProcessTask；任务 ID 稳定排序。不能跳过锁后再返回较早层。
- Unknown configuration, invalid identity, cross-tenant association and missing authorization fail closed. No applicant fallback for bound tasks.
- No second approval, assignment or notification engine; remove replaced authoritative write/send paths in the same implementation sequence. No production deployment of intermediate commits.
- Existing migration bytes/checksums must remain unchanged. Schema migration files are deliverables, not permission to migrate the shared development database. No shared migration, production catalog publication, old-instance rewrite or external KAF/VPN operation in Tasks 1–7.
- Use only owned isolated test databases/schemas. Tests must not use application credentials or reset shared data. Avoid printing secrets and unbounded application logs.
- Each task uses TDD for changed behavior, commits only its files and reports exact commands/results. No worker subagents; controller runs independent task review after each task.

## Files and responsibilities

- `itsm-backend/service/bpmn_assignment_source.go`: BPMN source validation, effective participant resolution and immutable terminal assignment projection.
- `itsm-backend/ent/schema/process_task.go`, generated Ent files and migration 032: structured source metadata.
- `itsm-backend/service/bpmn_process_engine.go`, `bpmn_publication.go`, `bpmn_task_ui_actions.go`, task DTO/controller mappers: integrate source without parallel endpoints.
- `itsm-backend/handlers/common/workitemassignment/`: transaction-aware shared assignment mutation and audit/outbox persistence, callable from services and BPMN callbacks without circular imports. Professional state rules stay with callers.
- `itsm-backend/service/work_item_assignment_notification.go`: delivery handler registered with existing Outbox worker.
- `itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts`, `designer/WorkflowNodeInspector.tsx`: preserve and configure source metadata.
- `itsm-frontend/src/components/ticket/TicketProcessTasks.tsx`: render projected source/state and authoritative actions.

### Task 1: Persist and validate the explicit binding source

**Files:** Modify `itsm-backend/ent/schema/process_task.go`, generated Ent files, `service/bpmn_types.go`, `service/bpmn_publication.go`, `service/bpmn_process_engine.go`, `migration/migrations.go`; create `service/bpmn_assignment_source.go`, `service/bpmn_assignment_source_test.go`, `migration/bpmn_assignment_source.go`, `migration/bpmn_assignment_source_test.go`, `migrations/032_bpmn_assignment_source.sql`, `migrations/032_bpmn_assignment_source_verify.sql`.

**Interfaces:** Produces `const BPMNAssigneeSourceWorkItem = "work_item_assignee"`, `func validateBPMNAssigneeSource(task *BPMNUserTask) error`, `BPMNUserTask.AssigneeSource string`, `ent.ProcessTask.AssigneeSource string`. Later tasks extend the same source file or use focused adjacent files, never a duplicate resolver.

- [ ] Write table-driven failing validation tests with valid source, empty legacy source, unknown source, non-fulfillment purpose, explicit assignee, candidates and every approval-specific selector conflict. Parse XML to verify namespace-compatible attribute decoding. Reuse current publication fixtures to prove invalid source cannot publish.

```go
for _, source := range []string{"unknown", "${assignee_id}"} {
    require.Error(t, validateBPMNAssigneeSource(&BPMNUserTask{
        TaskPurpose: "fulfillment", AssigneeSource: source,
    }))
}
require.NoError(t, validateBPMNAssigneeSource(&BPMNUserTask{
    TaskPurpose: "fulfillment", AssigneeSource: BPMNAssigneeSourceWorkItem,
}))
```

- [ ] Run `go test ./service -run TestBPMNAssignmentSource -count=1` and record expected RED before production changes.
- [ ] Add the XML attribute and structured field. Only empty and supported source may be stored. Existing tasks default empty; do not write current person into bound task assignee/candidate fields. Validate before createUserTask fallback and capture source from definition, never request variables. Runtime source validation must reject unsupported/external delegated handlers too.

```go
AssigneeSource string `xml:"assigneeSource,attr"`
// Ent field declaration:
field.String("assignee_source").Default("").Comment("Immutable task assignment source from the pinned process definition")
```

- [ ] Add migration 032 through the existing registry and matching operational SQL. `ALTER TABLE process_tasks ADD COLUMN IF NOT EXISTS assignee_source varchar NOT NULL DEFAULT '';` plus a source value constraint. Reject conflicting persisted bound assignee/candidates rather than backfill them. Preserve all existing migration content. Generate using `go generate ./ent` from backend; inspect generated diff for unrelated tool drift.
- [ ] Run source creation tests asserting bound tasks stay empty even when requester_id is present, legacy candidate tests, migration registry tests and `git diff --check`. Ensure no test expects binding authorization to be complete yet: Tasks 2 and 5 provide it, and intermediate branches are not deployed.
- [ ] Commit `feat(bpmn): persist explicit WorkItem assignment source`; full report includes RED/GREEN, migration checksum evidence and generated file explanation.

### Task 2: Resolve effective assignee and enforce read/command authority

**Files:** Extend `service/bpmn_assignment_source.go`; create `service/bpmn_assignment_authorization.go` and tests; modify `service/bpmn_process_engine.go`, `service/bpmn_participation.go`, `service/bpmn_task_ui_actions.go`, `dto/bpmn_task_dto.go`, affected existing task controller mappings and their tests.

**Interfaces:** Consumes Task 1 field/constant. Produces an internal `BPMNTaskAssignment` projection (`Assignee`, `Source`, `State` strings; `ResponsibleUserID`, `ActorID` ints) and `func (e *CustomProcessEngine) resolveTaskAssignment(ctx context.Context, client *ent.Client, task *ent.ProcessTask) (BPMNTaskAssignment, error)`. Reuse existing WorkItem identity/authorization and row-scope policy; do not call private repositories across domains.

- [ ] RED tests create owned WorkItem/instance/bound task with current actor A, change only WorkItem to B, then assert resolver and task list show B. Persisted task assignee remains empty. Cover missing/inactive user and foreign tenant/invalid business identity. Terminal projection reads immutable task terminal audit, never live WorkItem assignee.
- [ ] Implement a single effective-source resolver. Empty source uses existing task semantics; unknown nonempty source errors. Derive identity through ProcessInstance structured fields and the existing record-class registry. Terminal audit must uniquely identify task and terminal transition; missing evidence yields unavailable rather than a guessed owner.

```text
resolve(task):
  source empty -> independent task assignment
  source invalid -> error
  task terminal -> terminal audit responsibleUserId + actorId
  active -> validated instance WorkItem identity -> current active assignee
```

- [ ] Apply authorization intersection before list pagination/count and on detail/actions/commands: tenant/MSP + WorkItem row visibility + professional resource permission + BPMN permission + effective participant. For fulfillment use existing professional fulfillment permission (Requested Item provision), not requester submission permission. Elevated BPMN permissions waive participant only. Internal identities retain explicit trusted boundary.
- [ ] Reject assign/claim/delegate commands for bound tasks regardless of persisted empty assignee, including numeric/string task routes. Missing owner cannot complete, including elevated actors. Do not grant permissions when WorkItem is assigned. Response `assigneeSource` and `assignmentState` use camelCase.
- [ ] GREEN tests cover A removal/B visibility, numeric and textual user identities, cross-tenant denial, no professional permission, no row visibility, elevated-only denial, correct filtered total and page boundaries; legacy approval/candidate regressions. Run owning service/controller/DTO focused tests. Commit `feat(bpmn): authorize tasks through current WorkItem assignment`.

### Task 3: Implement transactional shared assignment and Outbox notification

**Files:** Create `handlers/common/workitemassignment/assignment.go`, `assignment_test.go`; create `service/work_item_assignment_notification.go` and tests; update existing bootstrap Outbox registry wiring. Keep tests adjacent and use existing Outbox contracts.

**Interfaces:** Consumes existing `ent.Client`, `ent.Tx`, WorkItem identity and source constant value. Produces `workitemassignment.Command` carrying WorkItemID, TenantID, ActorTenantID, ActorID, AssigneeID, ExpectedVersion ints; Source, Reason strings. AssigneeID=0 explicitly clears assignment; negative IDs are rejected, and nonzero targets must be active in the authorized target scope. `type Event struct { Command Command; PreviousAssigneeID, Version int; EventID string }`, `type Enqueue func(context.Context, *ent.Client, Event) error`, `type IdentityValidator func(context.Context, *ent.Client, Command) error`, `NewWriter(enqueue Enqueue, validateIdentities IdentityValidator) *Writer`, and `(*Writer).Apply(ctx context.Context, txClient *ent.Client, cmd Command) (*ent.Ticket, error)` perform mutation only on caller's transaction; owning caller begins/commits. The injected enqueue implementation uses the existing OutboxEventRepository constructed with that transaction client, avoiding a service-package import cycle or duplicate Outbox persistence query. Actor and nonzero assignee validation is one explicit required constructor dependency: the owning service reuses the existing verified session/directory snapshot and current allocation policy for native and authorized MSP identities; nil validation fails closed. Native tenant equality must not exclude legitimately allocated MSP assignees. SessionSnapshot.ValidateAssignmentIdentities uses the private active-write target, never mutable exported identity fields. No mutable validator setter, unverified context capability, or privileged RLS bypass. Returns full shared record, no professional extension mutation. Event type `work_item.assigned` and deterministic event ID from tenant/WorkItem/new version.

- [ ] RED tests use an existing Ent transaction fixture: change owner, read audit and pending outbox in the same transaction; force audit/outbox insert failure and prove rollback preserves prior owner/version. Validate inactive/cross-tenant target, actor/source, stale version, and same-owner no-op without duplicate events.
- [ ] Implement validation and WorkItem locking/CAS, stable affected bound-task enumeration, one audit event with actor/source and old/new owner, then invoke the injected enqueue using caller tx client. Reuse TicketWorkflowRecord as shared assignment audit with metadata and From/ToUserID/OperatorID/Reason; do not add an audit entity. This lower application operation must not import `service` or `service/bpmn`; infrastructure does not invoke application services upward.

```text
Apply(tx, command):
  load/lock tenant WorkItem; check expected version and valid target
  if unchanged: return item
  update assignee + version (no generic lifecycle transition)
  append assignment audit and work_item.assigned outbox in tx
  return updated item; caller commits
```

- [ ] Delivery handler validates immutable event payload and tenant, uses existing notification delivery boundaries, registers with existing worker. Provide idempotence using the current notification/event identity mechanism; do not mark non-idempotent external transport replay-safe. Ambiguous external success uses existing observable blocked/reconciliation behavior instead of blind retry. No real emails in tests.
- [ ] GREEN tests: transaction rollback, stable event identity, duplicate delivery, failure/retry, restart before dispatch. Run shared operation and Outbox handler focused tests; commit `feat(workitem): transact assignment audit and notification events`.

### Task 4: Route reachable assignment writes through the shared operation

**Files:** Modify reachable owner-writing paths in `service/ticket_service.go`, `ticket_workflow_service.go`, `ticket_assignment_service.go`, `ticket_assignment_smart_service.go`, `ticket_lifecycle_service.go`, professional assignment services, `handlers/problem/repository_impl.go`, `handlers/change/repository_impl.go`, their owning services, and `service/bpmn/ticket_handler.go`/`handlers/service_request/workflow_callback.go` as identified by preflight. Remove the repositories’ unconditional owner rewrites, moving actual changes to the owning application transaction. Preserve one version increment for each aggregate command, not an extra increment for assignment plus another for other fields. Modify owning repository interfaces only where necessary to pass an existing transaction without nesting. Update matching existing tests and add regression tests beside owning services.

**Interfaces:** Consumes Task 3 Command/Apply and existing professional authorization/lifecycle policies. Existing public endpoint signatures remain authoritative; do not add duplicate assign endpoints. All callers supply actual actor/source/tenant and version from trusted scope, not request-selected identity.

- [ ] Inventory each reachable `SetAssigneeID`/`ClearAssigneeID` write and categorize creation vs reassignment. New object creation preserves its owning atomic creator; existing object reassignment routes through Apply. Table belongs in this task report and final verification document, not comments with duplicate business logic.
- [ ] RED tests invoke real owning methods for manual assign, transfer ownership, batch, automatic routing, escalation and callbacks; verify shared owner/audit/outbox atomically. Professional state changes remain in their service transaction and cannot be lost by replacing assignment code.
- [ ] Replace direct reassignment mutations and old assignment notification goroutines. Preserve each professional state guard and side effect, reuse caller transaction, and do not open nested transactions or commit inside Apply. A no-assignee/unknown behavior must fail closed or retain an explicit waiting state according to owning lifecycle.
- [ ] GREEN tests cover all reachable classes/callers, tenant and version failures, no duplicate notification. Run owning service packages once; scan production owner writes and explain every remaining direct write (atomic creation or unreachable code), otherwise the task is incomplete. Commit `refactor(workitem): centralize authoritative reassignment writes`.

**Reviewed caller integration:** Preserve the registered `/tickets/:id/assign` and MSP assignment entry points for every implemented professional WorkItem. `workitemassignment.Owner.AssignWorkItem(ctx, session, item, assigneeID, source)` dispatches through bootstrap-registered owning services in the same verified `SessionReader.Write` transaction; reuse professional guards and side effects and do not replay extension fields. Unregistered classes are not advertised assignable. Mixed owner/status updates persist status notification intent for the final owner through the existing notification queue in that transaction, with no external delivery before commit. Non-transfer forwarding requires existing workflow permission and row visibility; assignment permission is added only for ownership transfer.

### Task 5: Serialize bound task execution, cancellation and terminal history

**Files:** Modify `service/bpmn_process_engine.go`, `service/bpmn_audit_service.go`; add focused `bpmn_assignment_lifecycle.go` and tests; amend existing completion/cancellation/termination fixtures and the real Postgres concurrency harness.

**Interfaces:** Consumes Task 2 assignment resolver and Task 3 common WorkItem mutation boundary. Existing CompleteTask, CancelTask, TerminateProcess stay the command owners. Terminal audit records responsibleUserId and actual actor/source; audit projection is consumed by Task 2 resolver.

- [ ] RED tests prove A cannot complete after B reassignment and completion creates immutable terminal attribution. Admin cancellation records responsible B distinct from admin actor. Instance termination must create one terminal task audit for every actually cancelled bound task, retaining existing instance audit.
- [ ] Lock WorkItem before instance/task mutations and reauthorize using locked current state. Source reads must never replace stored fields on the Ent object with projected values. Task creation, direct cancellation and instance termination share this lock order; stable task ordering prevents cyclic multi-task locks. Synchronous callbacks retain caller transaction.

```text
terminal mutation:
  lock WorkItem -> authorize current business scope -> lock instance when mutated
  load/lock task -> validate current lifecycle/version
  snapshot responsible identity -> mutate task -> append task terminal audit
  run existing domain callback in owning transaction -> commit
```

- [ ] Add real Postgres races: reassign vs complete/create/terminate and audit failure mid multi-task termination. Assert one serial order, no mixed owner history, no partial cancellation, no deadlock, old actor denied after successful assignment response. Use isolated schema and ordinary tenant connection; do not substitute SQLite for this evidence.
- [ ] Run focused lifecycle/concurrency tests then affected engine suite. Commit `fix(bpmn): serialize bound task lifecycle with WorkItem assignment`.

### Task 6: Preserve binding configuration and render effective assignment

**Files:** Modify `itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts`, `components/workflow/designer/WorkflowNodeInspector.tsx`, adjacent workflow tests; modify `components/ticket/TicketProcessTasks.tsx`, adjacent tests and existing BPMN API types in `src/lib/api/`.

**Interfaces:** Consumes backend assigneeSource, assignmentState and uiActions. Exact option value `work_item_assignee`; Chinese label `工单当前处理人`. States map to human-readable assignment/waiting/unavailable/history copy, no frontend authority or status engine.

- [ ] RED tests: XML import/export retains source; inspector select writes supported value, switching policy removes conflicting explicit/candidate values, unsupported config is not silently erased; task panel renders waiting state without claim/complete actions and B projection with backend-provided complete action.
- [ ] Add moddle property and fulfillment-only selector. Do not infer source from node names, hardcode Helpdesk IDs or auto-convert old definitions. Render backend owner projection and immutable terminal actor separately if present. Claim/complete remain existing endpoints.

```ts
{ name: 'assigneeSource', type: 'String', isAttr: true }
// UI option value: 'work_item_assignee'; label: '工单当前处理人'
```

- [ ] GREEN workflow and task panel Jest tests, TypeScript and affected lint, production build. Verify legacy fixed/candidate/approval view tests still pass. Commit `feat(ui): expose WorkItem-bound task assignment`.

### Task 7: Whole-path verification and deployment-ready delivery

**Files:** Add long-lived acceptance tests under existing backend integration/contract directories and frontend `tests/e2e/flows/`; update the authoritative catalog-lifecycle validation plan with verified results and remaining deployment boundaries. No temporary scripts/screenshots/credentials in git.

**Interfaces:** Uses Tasks 1–6 through existing HTTP/UI routes and temporary pure-human fixtures. No new testing-only endpoints.

- [ ] Run backend affected/full tests with bounded parallelism, frontend affected/full required gates and migration checksum comparisons. A skipped Postgres test is not a pass: obtain isolated test database evidence or report exactly why unavailable.
- [ ] Prepare migration 032 preflight, rollback boundary, binary/frontend build artifacts and pure-human browser A/B fixture. Do not execute shared migration under this plan's source-only authorization; stop at that concrete gate after completing all independent work. Publishing real catalog definitions is also downstream of successful controlled runtime acceptance.
- [ ] Browser acceptance after authorized deployment: requester submits; Helpdesk receives; authorized A owns execution; reassign B; A cannot complete via UI or direct command; B can complete; manager/confirmation responsibilities remain independent; terminal owner stable after later reassignment. Assert audit/Outbox persistence, no external provisioning; clean owned fixtures through existing APIs.
- [ ] Record exact tested source, commands, results, isolated DB scope and pending shared environment actions. Commit `test(bpmn): verify WorkItem-bound assignment lifecycle`. Final independent whole-branch review required before source delivery; no main merge.
