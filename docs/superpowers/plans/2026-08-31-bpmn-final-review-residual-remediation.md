# BPMN Final Review Residual Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the three Important findings left by the durable callback outbox scoped re-review so legacy callbacks fail closed, CC callbacks preserve a typed complete payload, and callback idempotency cannot break ordinary CC writes or schema migration.

**Architecture:** Legacy task descriptor recovery resolves only from the deployed BPMN definition and rejects missing nodes or unresolved handler declarations before mutating task/process state. CC callbacks normalize dynamic recipients into a fixed `ccResolvedUserIds` payload field, derive attribution from the authoritative process initiator at execution, and use a nullable callback `delivery_key` for retry deduplication instead of imposing a natural unique key on all TicketCC rows. Ordinary CC writes become transactional and reactivate an existing inactive relation rather than reporting success after a failed create.

**Tech Stack:** Go 1.25, Gin, Ent, SQLite unit tests, PostgreSQL integration tests, Testify.

**Spec:** `docs/superpowers/specs/2026-08-30-architecture-assessment-remediation-execution-plan-design.md`

## Global Constraints

- Work only in `/home/administrator/project/itsm/.worktrees/bpmn-instance-authorization` on `feat/bpmn-instance-authorization`; do not touch the main or KAF worktrees.
- `itsm-backend` remains the authority for tenant isolation, authorization, callback routing, workflow transitions, audit, and persistence.
- Participant variables must never select a callback handler, callback action, tenant, business target, endpoint, secret, or attribution actor.
- Callback payload persistence is typed and allowlisted; dynamic CC recipients are normalized to a fixed field and arbitrary variable names are never persisted.
- Durable callback execution is at-least-once; internal effects must be idempotent under a stable execution key.
- Every database query and uniqueness boundary introduced here is tenant-scoped.
- Do not add compatibility wrappers, dual writes, silent fallbacks, or a second callback/approval mechanism.
- Run Ent generation after handwritten schema changes and commit generated output in the same task.
- PostgreSQL tests use an already configured secret-safe test environment; never put credentials in plans, logs, command lines, reports, or commits.

---

### Task 1: Fail-Closed Legacy Callback Descriptor Recovery

**Files:**
- Modify: `itsm-backend/service/bpmn_callback_security.go`
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Test: `itsm-backend/service/bpmn_final_security_wave_test.go`
- Test: `itsm-backend/service/bpmn_usertask_callback_test.go`

**Interfaces:**
- Consumes: `bpmnCallbackDescriptor`, `bpmnNoUserTaskCallbackHandlerID`, `bpmnUnresolvedUserTaskCallbackHandlerID`, deployed `BPMNProcess` definitions.
- Produces: `descriptorForProcessTask(...)` that distinguishes a valid no-callback user task from a missing node or unresolved declared callback; `executeStep(...)` that rejects non-end elements with no outgoing flow.

- [ ] **Step 1: Add failing rollback tests for missing and unresolved legacy definitions**

Create table-driven tests that build a running process instance and pending legacy task with empty callback descriptor fields, then call the real completion transaction:

```go
tests := []struct {
    name       string
    taskKey    string
    bpmnXML    string
    wantErr    string
    wantStored string
}{
    {
        name:    "task node missing from deployed definition",
        taskKey: "removed_legacy_task",
        bpmnXML: processWithUnrelatedUserTask,
        wantErr: "任务节点不存在于已部署流程定义",
    },
    {
        name:       "declared legacy service reference is unregistered",
        taskKey:    "legacy_service",
        bpmnXML:    processWithLegacyServiceReference("retired_change_handler"),
        wantErr:    "回调描述符无法解析",
        wantStored: bpmnUnresolvedUserTaskCallbackHandlerID,
    },
}
```

For both cases assert the completion returns an error and the transaction preserves the task status, task variables, process version/status/current activity, audit count, and outbox count. For the unresolved declaration assert the persisted descriptor retains `TaskType="retired_change_handler"` or, if descriptor persistence participates in the rolled-back completion transaction, assert a direct descriptor recovery call returns that exact unresolved descriptor without converting it to the no-callback sentinel.

- [ ] **Step 2: Add a failing orphan-flow test**

Call `executeStep` on a non-end user/service element that has no outgoing sequence flow and assert a configuration error containing the element ID. Assert the process remains running and is not marked completed.

- [ ] **Step 3: Run the RED tests**

Run:

```bash
cd itsm-backend
go test ./service -run 'TestLegacyCallbackDescriptor|TestExecuteStepRejectsOrphanElement' -count=1 -v
```

Expected: FAIL because missing nodes default to `__no_user_task_callback__`, unregistered legacy service references lose their declared type, or orphan elements return success.

- [ ] **Step 4: Implement explicit descriptor classification**

Change legacy recovery to follow these exact rules:

```go
func (e *CustomProcessEngine) descriptorForProcessTask(
    ctx context.Context,
    client *ent.Client,
    task *ent.ProcessTask,
    process *BPMNProcess,
) (bpmnCallbackDescriptor, error) {
    // Existing immutable descriptor remains authoritative.
    // A matching user task with no declared callback is the only legacy case
    // that may resolve to bpmnNoUserTaskCallbackHandlerID.
    // A matching user/service task with a non-empty but unregistered task type
    // resolves to bpmnUnresolvedUserTaskCallbackHandlerID and retains the type.
    // No matching node is a configuration error and must not persist a sentinel.
}
```

Make `definitionDeclaredServiceTaskType` return the first trimmed non-empty declared reference from `Implementation`, `Class`, `DelegateExpression`, or `OperationRef` without requiring a currently registered handler. Pass that declaration through `callbackDescriptor`, which already maps unknown non-empty types to the unresolved sentinel.

Immediately after descriptor recovery in completion, reject `bpmnUnresolvedUserTaskCallbackHandlerID` before updating process variables or task state. Do not enqueue an unresolved callback and do not reinterpret it from participant variables.

- [ ] **Step 5: Make orphan workflow nodes fail closed**

In `executeStep`, retain completion only for a real end event. For any other element with zero outgoing flows, return a fixed configuration error containing the element ID:

```go
if len(outgoingFlows) == 0 {
    if e.isEndEvent(process, currentElementID) {
        return e.completeProcess(ctx, instance)
    }
    return fmt.Errorf("流程节点 %s 没有出向顺序流且不是结束事件", currentElementID)
}
```

- [ ] **Step 6: Run focused, race, and regression tests**

```bash
cd itsm-backend
go test ./service -run 'TestLegacyCallbackDescriptor|TestExecuteStepRejectsOrphanElement|UserTaskCallback|CallbackDescriptor' -count=1
go test -race ./service -run 'TestLegacyCallbackDescriptor|TestExecuteStepRejectsOrphanElement|UserTaskCallback' -count=1
```

Expected: PASS, with no task/process/audit/outbox mutation on rejected completion.

- [ ] **Step 7: Commit Task 1**

```bash
git add itsm-backend/service/bpmn_callback_security.go \
  itsm-backend/service/bpmn_process_engine.go \
  itsm-backend/service/bpmn_final_security_wave_test.go \
  itsm-backend/service/bpmn_usertask_callback_test.go
git commit -m "fix(bpmn): fail closed on legacy callback gaps"
```

---

### Task 2: Normalize Complete CC Callback Payloads

**Files:**
- Modify: `itsm-backend/service/bpmn/handler_base.go`
- Modify: `itsm-backend/service/bpmn/callback_payload_policy.go`
- Modify: `itsm-backend/service/bpmn/cc_handler.go`
- Modify: `itsm-backend/service/bpmn_callback_security.go`
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Test: `itsm-backend/service/bpmn/cc_handler_test.go`
- Test: `itsm-backend/service/bpmn_final_security_wave_test.go`
- Test: `itsm-backend/service/bpmn_callback_recovery_test.go`

**Interfaces:**
- Consumes: stable callback execution key, `ProcessInstance.Initiator`, authoritative tenant/business identity reconstruction, BPMN CC definition fields.
- Produces: optional `CallbackPayloadNormalizer` implemented by `CCTaskHandler`; fixed persisted `ccResolvedUserIds`; authoritative runtime `addedBy`; enqueue-to-execute coverage.

- [ ] **Step 1: Add failing payload-normalization tests**

Add tests for the real `filterBPMNCallbackPayload` path:

```go
input := map[string]interface{}{
    "ccType":         "variable",
    "ccVariable":     "approval_watchers",
    "approval_watchers": []interface{}{float64(12), "13"},
    "notifyChannels": "in_app,email",
    "addedBy":        999999,
    "authorization":  "must-not-persist",
}
```

Assert the filtered payload contains `ccType`, `ccVariable`, `ccResolvedUserIds` as a cloned fixed list, and `notifyChannels`; it must not contain `approval_watchers`, caller `addedBy`, `authorization`, or any arbitrary object. Add invalid dynamic value cases (missing variable, map/object, non-positive/non-numeric ID) that fail before enqueue.

- [ ] **Step 2: Add a failing enqueue-to-execute recovery test**

Create a real CC service task definition using `ccType="variable"`, `ccVariable="approval_watchers"`, `notifyChannels="in_app,email"`, and a process initiator containing a valid same-tenant user ID. Execute scheduling through the process engine, inspect the outbox row, then process it through `ProcessCallbackOutboxByID`.

Assert:

```go
require.NotContains(t, row.Variables, "approval_watchers")
require.ElementsMatch(t, []interface{}{float64(recipientA.ID), float64(recipientB.ID)}, row.Variables["ccResolvedUserIds"])
require.Equal(t, "in_app,email", row.Variables["notifyChannels"])
require.NotContains(t, row.Variables, "addedBy")
```

After execution assert both same-tenant CC relations exist, notification channels are preserved, and each relation's `AddedBy` equals the authoritative process initiator rather than a participant-supplied value.

- [ ] **Step 3: Run the RED tests**

```bash
cd itsm-backend
go test ./service ./service/bpmn -run 'TestCCCallbackPayload|TestCCCallbackOutboxVariableRecipients' -count=1 -v
```

Expected: FAIL because the current static allowlist drops `ccVariable`, its dynamic value, `notifyChannels`, and trusted attribution.

- [ ] **Step 4: Add a typed normalizer extension point**

Define beside `CallbackPayloadPolicy`:

```go
type CallbackPayloadNormalizer interface {
    NormalizeCallbackPayload(action string, variables map[string]interface{}) (map[string]interface{}, error)
}
```

Update `filterBPMNCallbackPayload` to prefer this interface. Clone and validate the normalizer result with the existing JSON value guard so a handler cannot return aliases or unsupported values. Keep the existing static allowlist path for all other handlers.

- [ ] **Step 5: Implement CC normalization and fixed recipient consumption**

Implement `CCTaskHandler.NormalizeCallbackPayload` to copy only the static keys `ccType`, `ccUserIds`, `ccGroupIds`, `ccRoleIds`, `ccVariable`, `ccNotify`, and `notifyChannels`. When `ccType == "variable"`, require a non-empty `ccVariable`, read that named source value once, normalize it to a deduplicated positive integer list under `ccResolvedUserIds`, and never retain the arbitrary source key.

Change `resolveCCUsers` so the `variable` branch consumes only `ccResolvedUserIds`; it must not look up `variables[ccVariable]` during durable execution. Preserve tenant-scoped user validation.

- [ ] **Step 6: Derive trusted CC attribution at execution**

In `authoritativeCallbackVariables`, when `handler.GetTaskType() == "cc_task"`, parse `instance.Initiator` as a positive integer and set runtime-only `addedBy`. Return a fixed configuration error if the initiator is absent, non-numeric, non-positive, or not a same-tenant active user. Never persist participant-provided `addedBy` in the outbox.

- [ ] **Step 7: Run focused, race, and sensitive-field tests**

```bash
cd itsm-backend
go test ./service ./service/bpmn -run 'TestCCCallbackPayload|TestCCCallbackOutboxVariableRecipients|TestCCTaskHandler' -count=1
go test -race ./service ./service/bpmn -run 'TestCCCallbackPayload|TestCCCallbackOutboxVariableRecipients|TestCCTaskHandler' -count=1
```

Expected: PASS. A tracked diff scan must show no persisted arbitrary dynamic key and no callback log containing variables, recipients, endpoint, headers, secrets, or tokens.

- [ ] **Step 8: Commit Task 2**

```bash
git add itsm-backend/service/bpmn/handler_base.go \
  itsm-backend/service/bpmn/callback_payload_policy.go \
  itsm-backend/service/bpmn/cc_handler.go \
  itsm-backend/service/bpmn_callback_security.go \
  itsm-backend/service/bpmn_process_engine.go \
  itsm-backend/service/bpmn/cc_handler_test.go \
  itsm-backend/service/bpmn_final_security_wave_test.go \
  itsm-backend/service/bpmn_callback_recovery_test.go
git commit -m "fix(bpmn): preserve typed CC callback inputs"
```

---

### Task 3: Isolate CC Callback Idempotency From Ordinary CC Semantics

**Files:**
- Modify: `itsm-backend/ent/schema/ticket_cc.go`
- Regenerate: `itsm-backend/ent/**`
- Modify: `itsm-backend/service/bpmn/cc_handler.go`
- Modify: `itsm-backend/service/ticket_workflow_service.go`
- Test: `itsm-backend/service/bpmn/cc_handler_test.go`
- Test: `itsm-backend/service/ticket_workflow_service_test.go`
- Test: `itsm-backend/service/bpmn_callback_outbox_schema_test.go`
- Test: `itsm-backend/service/bpmn_outbox_integration_test.go`

**Interfaces:**
- Consumes: `bpmn.CallbackExecutionKeyFromContext`, tenant-scoped TicketCC relations, Ent transaction clients.
- Produces: nullable sensitive `TicketCC.delivery_key`; unique callback-only index `(tenant_id, delivery_key, user_id)`; transactional ordinary reactivation/create behavior whose workflow history lists only persisted CC users.

- [ ] **Step 1: Add failing schema and migration-compatibility tests**

Assert the TicketCC Ent schema no longer declares unconditional unique `(tenant_id, ticket_id, user_id)`. Assert it declares:

```go
field.String("delivery_key").Optional().Nillable().Sensitive()
index.Fields("tenant_id", "delivery_key", "user_id").Unique()
```

Create SQLite rows that model historical duplicates and inactive relations before running the updated schema migration. Assert migration succeeds because ordinary rows have `NULL delivery_key`. Add a real PostgreSQL integration variant using an isolated temporary schema when `ITSM_TEST_DB` is available.

- [ ] **Step 2: Add failing callback retry tests**

Exercise `CCTaskHandler.Execute` twice with the same stable execution key and assert exactly one TicketCC relation and one set of notifications. Execute once with a different key and an already-active ordinary CC relation; assert no duplicate relation is created and the handler reports zero newly added users. Assert callback-created rows store the stable delivery key and API DTOs do not expose it.

- [ ] **Step 3: Add failing ordinary inactive-reactivation and rollback tests**

Create an inactive TicketCC row, call the real `CCTicket`, and assert the same row is reactivated with the new `AddedBy`/`AddedAt`, notifications are produced once, and workflow metadata lists that user. Inject a create/update or workflow-record failure and assert the transaction rolls back relation, notification, and workflow state rather than logging and returning success.

- [ ] **Step 4: Run the RED tests**

```bash
cd itsm-backend
go test ./service ./service/bpmn -run 'TestTicketCCSchema|TestCCTaskHandlerRetry|TestCCTicketReactivates|TestCCTicketRollback' -count=1 -v
```

Expected: FAIL because the natural unique index is unconditional, TicketCC has no delivery key, and ordinary CCTicket currently continues after persistence errors.

- [ ] **Step 5: Replace the natural unique index with callback-only dedupe**

Update the handwritten TicketCC schema exactly as tested, remove the natural unique index, then run:

```bash
cd itsm-backend
go generate ./ent
git diff --check
```

Do not manually edit generated Ent files. Confirm ordinary rows can coexist with historical inactive/duplicate rows because `delivery_key` is nullable, while callback rows are tenant-scoped and deduplicated by stable delivery key plus user.

- [ ] **Step 6: Make the CC handler idempotent by delivery key**

Require a non-empty callback execution key in `CCTaskHandler.Execute`. For each resolved user, first query `(tenant_id, delivery_key, user_id)`; a match means that delivery already succeeded for the relation. If no delivery-key row exists but an active natural relation exists, do not create another relation. Otherwise create the TicketCC row with `delivery_key`. Preserve one transaction for relation and notification effects; any persistence failure returns an error and rolls back.

- [ ] **Step 7: Make ordinary CCTicket writes transactional and truthful**

Refactor `CCTicket` to use one Ent transaction for relation writes, notifications, and workflow history. For each validated target user:

1. Reuse an active tenant/ticket/user row without reporting it as newly added.
2. Otherwise reactivate one deterministic inactive row (highest ID), updating `AddedBy`, `AddedAt`, and `IsActive=true`.
3. Otherwise create a new ordinary row with `delivery_key=NULL`.
4. On any query/update/create/history error, return the error and roll back; do not continue or emit success history.
5. Put only users actually created or reactivated into notification and workflow metadata.

Introduce a private `withClient(*ent.Client) *TicketWorkflowService` or equivalent existing service rebinding pattern so `createWorkflowRecord` and notification writes use the transaction client; do not open a nested transaction.

- [ ] **Step 8: Run generated-code, focused, PostgreSQL, race, and full gates**

```bash
cd itsm-backend
go generate ./ent
before=$(git diff -- ent | git hash-object --stdin)
go generate ./ent
after=$(git diff -- ent | git hash-object --stdin)
test "$before" = "$after"
go test ./ent/... ./internal/bootstrap/... ./service ./service/bpmn -run 'TicketCC|CCTicket|CCTask|Callback' -count=1
go test -race -p 1 ./service ./service/bpmn -run 'TicketCC|CCTicket|CCTask|Callback' -count=1
go test -tags integration ./service -run 'TestBPMNCallbackOutboxLeaseRecoveryPostgres|TestTicketCCMigrationCompatibilityPostgres' -count=1 -v
go test -race -tags integration -p 1 ./service -run 'TestBPMNCallbackOutboxLeaseRecoveryPostgres|TestTicketCCMigrationCompatibilityPostgres' -count=1 -v
go test ./... -count=1
go build ./...
git diff c15af6eda1febd47a75fb1e621907b16bbaac336..HEAD --check
```

Expected: all commands PASS from a stable tree. If PostgreSQL environment is absent, the integration test must fail closed or explicitly skip only under the repository's established `ITSM_TEST_DB` contract; SQLite is not substitute evidence for the release gate.

- [ ] **Step 9: Commit Task 3**

```bash
git add itsm-backend/ent itsm-backend/service/bpmn/cc_handler.go \
  itsm-backend/service/ticket_workflow_service.go \
  itsm-backend/service/bpmn/cc_handler_test.go \
  itsm-backend/service/ticket_workflow_service_test.go \
  itsm-backend/service/bpmn_callback_outbox_schema_test.go \
  itsm-backend/service/bpmn_outbox_integration_test.go
git commit -m "fix(bpmn): isolate CC callback deduplication"
```

---

## Final Review And Integration Gates

- Generate one whole-plan review package from `6ba5bce4` to the remediation HEAD.
- Run one final whole-branch review against the architecture spec, this plan, and all three original residual findings.
- Allow one consolidated final-fix dispatch and one scoped re-review under the new SDD workspace; adjudicate any remaining findings without silently parking load-bearing defects.
- Fetch `origin`, merge the latest local/remote `main` into the feature worktree without touching the main worktree's untracked `docs/implementation/` or the KAF worktree, and resolve only feature-owned conflicts.
- Re-run the PostgreSQL normal/race tests, full backend tests, build, diff check, generated-code check, and secret/log scans on the integrated feature commit.
- Fast-forward or merge `feat/bpmn-instance-authorization` into local `main` only if the main worktree's unrelated untracked files remain untouched and Git confirms no overwrite risk.
- Re-run full backend tests and build on the merged `main`, push without force, and confirm `origin/main` points to the verified merge SHA.
