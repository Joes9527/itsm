# BPMN Callback Effect and Notification Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `subagent-driven-development` (recommended) or `executing-plans` to implement this plan task by task. Use `test-driven-development` for every behavior change and `verification-before-completion` before reporting completion.

**Goal:** Make every synchronous BPMN callback produce an explicit, auditable business effect (`applied`, `idempotent`, `skipped_optional`, or `blocked`), and make ticket-notification requests use one authoritative `eventType` contract from UI to persistence.

**Architecture:** Replace the handler-level `Success bool` result and permissive callback payload fallbacks with a typed effect contract owned by `service/bpmn`. Handlers may report only `applied`, `idempotent`, or a typed `blocked` outcome; only the orchestration layer may convert a blocked outcome to `skipped_optional`, and only from a strict boolean declared in the BPMN definition and snapshotted into the callback outbox. Keep `ProcessCallbackOutbox` as the Phase 1 durable transport, record blocking with its existing `status`/`last_error_class`, and defer all unified-Outbox migration or deletion to Phase 2.

**Tech Stack:** Go 1.25.12, Gin, Ent, PostgreSQL migrations, Prometheus client, Next.js/React, TypeScript, Ant Design, Jest/React Testing Library.

---

## Scope and ownership constraints

- Baseline is commit `f1129031` (or the integration branch containing it). Verify the implementation worktree contains that commit before starting.
- This plan owns callback effect types, synchronous callback handler contracts, callback payload normalization, callback outbox outcome policy, notification-service result semantics, backend notification DTOs, and the frontend notification form/API contract.
- P1-A owns migration number `020` and the first edit to `itsm-backend/migration/migrations.go`. This plan creates migration `021` SQL/test assets, but registration in `migrations.go` is an explicit integration step after P1-A lands.
- P1-C owns `itsm-backend/service/bpmn_process_engine.go` during Wave 1A. Tasks 1 and 3–7 proceed without editing it; Task 2's production edits are held for the atomic Task 2 + Task 8 integration. Task 8 is the only planned engine edit and starts only after P1-C is merged and its engine tests pass.
- Phase 2 unified Outbox work is out of scope. Do not create a second callback outbox, dual-write callback events, migrate historical callback rows, or delete `ProcessCallbackOutbox` in this plan.
- The environment is development-only. No historical-data backfill is required, but every schema change remains a versioned, repeatable migration asset.
- Replace obsolete paths in the same change. Do not retain `Success bool`, legacy request `type`/`channel`, permissive unknown-action dispatch, or fake frontend notification fallbacks.

## Target contract

```go
type CallbackEffectStatus string

const (
    CallbackEffectApplied         CallbackEffectStatus = "applied"
    CallbackEffectIdempotent      CallbackEffectStatus = "idempotent"
    CallbackEffectSkippedOptional CallbackEffectStatus = "skipped_optional"
    CallbackEffectBlocked         CallbackEffectStatus = "blocked"
)

type CallbackBlockCode string

const (
    CallbackBlockTargetTypeMismatch  CallbackBlockCode = "target_type_mismatch"
    CallbackBlockTargetMissing       CallbackBlockCode = "target_missing"
    CallbackBlockRecipientMissing    CallbackBlockCode = "recipient_missing"
    CallbackBlockRecipientEmpty      CallbackBlockCode = "recipient_empty"
    CallbackBlockUnsupportedCCType   CallbackBlockCode = "unsupported_cc_type"
    CallbackBlockUnsupportedTemplate CallbackBlockCode = "unsupported_placeholder"
    CallbackBlockChannelUnavailable  CallbackBlockCode = "channel_unavailable"
    CallbackBlockDeliveryNotCreated  CallbackBlockCode = "delivery_not_created"
    CallbackBlockHandlerContract     CallbackBlockCode = "handler_contract"
)

type CallbackEffect struct {
    Status      CallbackEffectStatus
    BlockCode   CallbackBlockCode
    Message     string
    OutputVars  map[string]interface{}
    UpdatedData map[string]interface{}
}
```

Contract rules:

- `applied`: this attempt created the requested durable effect.
- `idempotent`: the exact effect already exists under the stable delivery/idempotency key; no duplicate was created.
- `blocked`: the requested effect cannot be proven. It has an allowlisted `BlockCode`, does not advance the process token, and stores `status=blocked` plus `last_error_class=<block code>` in `ProcessCallbackOutbox`.
- `skipped_optional`: never returned by a handler. The outbox executor derives it only when a blocked callback has `optional_declared=true`, then emits a process audit record and metric before advancing.
- Handler errors represent retryable infrastructure failure and remain on the current retry/lease path. A nil effect, an unknown status, `blocked` without an allowlisted code, or handler-produced `skipped_optional` is a `handler_contract` block—not success.
- Empty recipients, missing targets, unsupported placeholders, unknown CC types/actions, and unavailable channels cannot return `applied` or `idempotent`.

## Parallel work map

| Track | Tasks | Can start | Shared hotspot |
|---|---|---|---|
| D1 backend contract | 1 | Immediately | New effect files only |
| D1 final handler integration | 2 + 8 as one atomic unit | After P1-C | Handlers, test doubles, then engine |
| D2 metadata/schema | 3–4 | Immediately; registration waits for P1-A | `migrations.go` only in integration substep |
| D3 notifications/API/UI | 5 and 7 | Immediately | No engine edits |
| D4 outcome observability | 6 | After Tasks 1 and 3 | Outbox executor, audit, metrics |
| D5 engine integration | 8 | After P1-C plus Tasks 1, 4, 6 | `bpmn_process_engine.go` |
| Release | 9 | All tracks merged | Repository-wide checks |

---

### Task 1: Introduce the authoritative callback effect type

**Files:**

- Create: `itsm-backend/service/bpmn/callback_effect.go`
- Create: `itsm-backend/service/bpmn/callback_effect_test.go`

- [ ] **Step 1: Write failing constructor and validation tests**

Test the exact status values, require an allowlisted code for `blocked`, reject a code on non-blocked effects, and reject handler-produced `skipped_optional`:

```go
func TestValidateHandlerEffect(t *testing.T) {
    tests := []struct {
        name    string
        effect  *CallbackEffect
        wantErr bool
    }{
        {"applied", AppliedEffect("sent", nil), false},
        {"idempotent", IdempotentEffect("already sent", nil), false},
        {"blocked", BlockedEffect(CallbackBlockRecipientEmpty, "no recipients"), false},
        {"nil", nil, true},
        {"blocked without code", &CallbackEffect{Status: CallbackEffectBlocked}, true},
        {"unknown", &CallbackEffect{Status: "unknown"}, true},
        {"handler cannot skip", &CallbackEffect{Status: CallbackEffectSkippedOptional}, true},
    }
    // table assertions
}
```

Run: `cd itsm-backend && go test ./service/bpmn -run 'TestValidateHandlerEffect|TestCallbackEffect' -count=1`

Expected: FAIL because the effect contract does not exist.

- [ ] **Step 2: Implement the type and constructors**

Add the target contract above plus:

```go
func AppliedEffect(message string, output map[string]interface{}) *CallbackEffect
func IdempotentEffect(message string, output map[string]interface{}) *CallbackEffect
func BlockedEffect(code CallbackBlockCode, message string) *CallbackEffect
func ValidateHandlerEffect(effect *CallbackEffect) error
func IsAllowedCallbackBlockCode(code CallbackBlockCode) bool
```

Keep the allowlist closed and in this file. Do not accept arbitrary database/error strings as block codes.

- [ ] **Step 3: Verify the isolated contract package**

Run: `cd itsm-backend && go test ./service/bpmn -run 'TestValidateHandlerEffect|TestCallbackEffect' -count=1`

Expected: PASS.

This task deliberately adds only the new value object. The existing interface remains untouched until Task 2 can replace every implementation and test double in one compiling change; do not add an alias or compatibility executor.

- [ ] **Step 4: Commit**

```bash
git add itsm-backend/service/bpmn/callback_effect.go itsm-backend/service/bpmn/callback_effect_test.go
git commit -m "refactor(bpmn): define explicit callback effect contract"
```

### Task 2: Make every synchronous handler prove its effect

**Dependency and atomicity:** Author tests and review handler mappings in parallel, but do not merge or hand off the interface/handler production edits independently. After P1-C lands, execute Task 2 and Task 8 as one atomic integration unit and create only Task 8's final commit. This prevents any intermediate revision in which the handler return type and engine call sites disagree or the repository does not compile.

**Files:**

- Create: `itsm-backend/service/bpmn/callback_contract.go`
- Create: `itsm-backend/service/bpmn/callback_contract_test.go`
- Delete: `itsm-backend/service/bpmn/callback_payload_policy.go`
- Modify: `itsm-backend/service/bpmn/handler_base.go`
- Modify: `itsm-backend/service/bpmn/handler_base_test.go`
- Modify: `itsm-backend/dto/bpmn_process_trigger_dto.go`
- Modify: `itsm-backend/service/bpmn/generic_handler.go`
- Modify: `itsm-backend/service/bpmn/generic_handler_test.go`
- Modify: `itsm-backend/service/bpmn/cc_handler.go`
- Modify: `itsm-backend/service/bpmn/cc_handler_test.go`
- Modify: `itsm-backend/service/bpmn/notification_handler.go`
- Modify: `itsm-backend/service/bpmn/notification_handler_test.go`
- Modify: `itsm-backend/service/bpmn/ticket_handler.go`
- Modify: `itsm-backend/service/bpmn/ticket_handler_test.go`
- Modify: `itsm-backend/service/bpmn/change_handler.go`
- Modify: `itsm-backend/service/bpmn/change_handler_test.go`
- Modify: `itsm-backend/service/bpmn/incident_handler.go`
- Modify: `itsm-backend/service/bpmn/incident_handler_test.go`
- Modify: `itsm-backend/service/bpmn/service_request_handler.go`
- Modify: `itsm-backend/service/bpmn/service_request_handler_test.go`
- Modify: `itsm-backend/service/bpmn/release_handler.go`
- Modify: `itsm-backend/service/bpmn/release_handler_test.go`
- Modify: `itsm-backend/service/bpmn/webhook_handler.go`
- Modify: `itsm-backend/service/bpmn/webhook_handler_test.go`
- Modify: `itsm-backend/service/bpmn/kaf_delegate_handler.go`
- Test: `itsm-backend/service/bpmn/*_test.go`
- Modify: `itsm-backend/controller/kaf_delegation_controller_test.go`
- Modify: `itsm-backend/service/bpmn_process_engine_ext_test.go`
- Modify: `itsm-backend/service/bpmn_callback_recovery_test.go`
- Modify: `itsm-backend/service/bpmn_final_security_wave_test.go`
- Modify: `itsm-backend/service/kaf_delegation_service_test.go`
- Check for additional test doubles with: `rg -l 'ServiceTaskResult|func .* Validate\(' itsm-backend --glob '*_test.go'`

- [ ] **Step 1: Write failing action-contract tests**

Define:

```go
type CallbackActionContract struct {
    PayloadFields  []string
    RequiredFields []string
}

type CallbackContractProvider interface {
    CallbackContract(action string) (CallbackActionContract, bool)
}
```

Add table tests proving every registered synchronous handler declares every supported action, unknown actions return `ok=false`, and required fields are a subset of payload fields. Use these authoritative action sets:

- ticket: `update_status`, `notify_requester`, `notify_handler`, `escalate`, `assign`
- change: `create_change`, `update_change`, `approve_change`, `reject_change`, `schedule_change`, `implement_change`, `verify_change`, `close_change`, `assess_risk`, `notify_stakeholders`
- incident: `create_incident`, `assign_incident`, `escalate_incident`, `resolve_incident`, `close_incident`, `update_incident`, `acknowledge_incident`, `categorize_incident`
- service request: `create_request`, `update_request`, `approve_request`, `reject_request`, `assign_request`, `provision_resource`, `complete_request`, `cancel_request`; `create_request` is declared but deterministically blocks because this architecture creates the request before starting BPMN
- generic: `complete_service`, `notify_rejection`, `notify`
- notification: `send_in_app`, `send_email`, `send_sms`, `send_webhook`; the last three are declared so they can return the specific `channel_unavailable` block instead of degrading into an unknown-action error
- CC: only the empty action, with `ccType` and `ccTargets` declared
- webhook: `call_webhook`, `send_notification`
- release: `tech_review`, `approval`, `schedule`, `execute`, `verify`
- KAF delegation: asynchronous and therefore not a synchronous callback contract provider

Run: `cd itsm-backend && go test ./service/bpmn -run 'Contract|UnknownAction' -count=1`

Expected: FAIL because current payload policy switches are permissive and incomplete.

- [ ] **Step 2: Replace the handler interface and payload policy atomically**

Change `ServiceTaskHandlerInterface.Execute` to return `(*CallbackEffect, error)`, remove its unused `Validate` method, update every handler and test double, and delete `dto.ServiceTaskResult`. Delete `CallbackPayloadPolicy` and replace it with `CallbackActionContract`. Keep `CallbackPayloadNormalizer` only where a handler must derive normalized values. An unregistered handler/action is a blocked dispatch; do not fall back to empty payload, all payload, or a default action. Complete the engine call-site adaptation in Task 8 before compiling or committing this production change.

- [ ] **Step 3: Write failing no-effect tests before changing each handler**

At minimum cover:

- generic non-ticket `complete_service` → `target_type_mismatch`
- generic notification with no ticket → `target_missing`
- ticket notify with zero requester/assignee → `recipient_missing`
- CC empty resolved set → `recipient_empty`
- unknown `ccType` → `unsupported_cc_type` (remove the current default-to-user behavior)
- any unresolved `${...}` CC target → `unsupported_placeholder` (remove warn-and-drop)
- notification `email`/`sms` → `channel_unavailable`
- release `approval` → `idempotent` only when the authoritative `ProcessApprovalDecision` for the same instance/node/action already exists; delete the current unconditional no-op success
- duplicate stable delivery key → `idempotent`
- successful durable mutation/delivery → `applied`

Example:

```go
func TestCCHandlerBlocksUnsupportedPlaceholder(t *testing.T) {
    effect, err := handler.Execute(ctx, task, map[string]interface{}{
        "ccType": "user", "ccTargets": []interface{}{"${manager}"},
    })
    require.NoError(t, err)
    require.Equal(t, CallbackEffectBlocked, effect.Status)
    require.Equal(t, CallbackBlockUnsupportedTemplate, effect.BlockCode)
}
```

- [ ] **Step 4: Replace every `Success: true` return**

- Return `applied` only after the transaction or durable write succeeds.
- Return `idempotent` only when the stable key or already-achieved domain state proves the exact effect exists.
- Return a typed `blocked` result for deterministic no-effect conditions.
- Return `error` for retryable database/adapter failures.
- Never infer optionality inside a handler.
- Do not change KAF lease/fencing semantics; only migrate shared result typing if compilation requires it.
- Treat the release approval decision already persisted by the BPMN engine as the exact existing effect; absence or mismatch blocks and cannot be called idempotent.

Do not run or claim a green repository at this intermediate point. Continue immediately to Task 8, finish all engine/test-double call-site changes, then run the combined focused tests there.

- [ ] **Step 5: Prove obsolete result paths are gone**

Run: `rg -n 'ServiceTaskResult|Success:\s*(true|false)|CallbackPayloadPolicy|func .* Validate\(' itsm-backend/service/bpmn itsm-backend/dto`

Expected: no matches related to synchronous callback handlers.

- [ ] **Step 6: Continue directly to Task 8 without committing**

Keep the Task 2 handler/interface/test-double diff together with Task 8's engine result handling. Do not publish a red intermediate commit, compatibility adapter, alias, or dual execution method.

### Task 3: Parse and persist definition-declared optionality

**Files:**

- Modify: `itsm-backend/service/bpmn_types.go`
- Create: `itsm-backend/service/bpmn_types_test.go`
- Modify: `itsm-backend/ent/schema/process_callback_outbox.go`
- Create: `itsm-backend/migrations/021_add_callback_optional_declared.sql`
- Create: `itsm-backend/migration/callback_optional_declared_test.go`
- Modify later, main-agent integration only: `itsm-backend/migration/migrations.go`
- Regenerate: `itsm-backend/ent/processcallbackoutbox/*`
- Regenerate: `itsm-backend/ent/processcallbackoutbox_create.go`
- Regenerate: `itsm-backend/ent/processcallbackoutbox_update.go`
- Regenerate: `itsm-backend/ent/processcallbackoutbox.go`
- Regenerate as needed: `itsm-backend/ent/client.go`, `itsm-backend/ent/mutation.go`, `itsm-backend/ent/tx.go`

- [ ] **Step 1: Write strict metadata parsing tests**

Add `CallbackOptionalDeclared() (bool, error)` on the BPMN task metadata types used by callbacks. Because `<bpmn:metaData>` is XML text, accept only the exact trimmed lower-case values `true` and `false` from `callback_optional`; absence means false. Reject mixed case, numeric values, empty values, and all other text.

```go
func TestBPMNUserTaskCallbackOptionalDeclared(t *testing.T) {
    task := BPMNUserTask{ExtensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
        {Name: "callback_optional", Value: "true"},
    }}}
    got, err := task.CallbackOptionalDeclared()
    require.NoError(t, err)
    require.True(t, got)
}

func TestBPMNUserTaskRejectsStringOptional(t *testing.T) {
    task := BPMNUserTask{ExtensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
        {Name: "callback_optional", Value: "TRUE"},
    }}}
    _, err := task.CallbackOptionalDeclared()
    require.Error(t, err)
}
```

Run: `cd itsm-backend && go test ./service -run 'CallbackOptionalDeclared' -count=1`

Expected: FAIL.

- [ ] **Step 2: Implement metadata parsing for the actual callback-capable task types**

Use one shared strict helper called by `BPMNUserTask` and `BPMNServiceTask` accessors. Do not treat missing handler/config as evidence that a callback is optional.

- [ ] **Step 3: Add the Ent field and migration 021 asset**

Add only:

```go
field.Bool("optional_declared").Default(false)
```

Migration asset:

```sql
ALTER TABLE process_callback_outboxes
    ADD COLUMN IF NOT EXISTS optional_declared boolean NOT NULL DEFAULT false;
```

Do not add `effect_status`, `effect_code`, or a replacement outbox table.

Keep the standalone operator script under the repository's existing `itsm-backend/migrations/` directory. When P1-A releases `migration/migrations.go`, register the same statement in the canonical migration switch and add a parity test that normalizes whitespace and proves the registered SQL equals the retained script; do not let the executable migration and retained script drift.

- [ ] **Step 4: Test migration idempotence and schema contract**

Before P1-A releases the registry, the migration test reads `../migrations/021_add_callback_optional_declared.sql`, asserts the exact table/column/default/not-null contract, runs it twice against the repository's migration test database when available, and asserts pre-existing rows read `false`. Because no production history exists, no backfill script beyond the default is required.

Run: `cd itsm-backend && go test ./migration -run 'CallbackOptionalDeclared|Migration021' -count=1`

Expected before registration: the asset test passes; the canonical-stream test remains pending until P1-A lands.

- [ ] **Step 5: Regenerate Ent code**

Run: `cd itsm-backend && go generate ./ent`

Run: `cd itsm-backend && go test ./ent/... ./migration -run 'CallbackOptionalDeclared|Migration021' -count=1`

Expected: PASS for schema/generated code and the standalone asset test.

- [ ] **Step 6: Register 021 only after P1-A/main ownership releases the hotspot**

After migration 020 is present, add RegisteredMigrations version `021_add_callback_optional_declared` and its `GetMigrationSQL` case to `migration/migrations.go`, using the exact retained script statement, update the canonical post-schema bootstrap test from 020 to 021, and extend the migration test with a whitespace-normalized parity assertion between `GetMigrationSQL("021_add_callback_optional_declared")` and the retained script. Do not renumber P1-A's migration or edit the switch concurrently.

Run: `cd itsm-backend && go test ./migration -count=1`

Expected: PASS and migration order `...019, 020, 021`.

- [ ] **Step 7: Commit in two reviewable changes**

```bash
git add itsm-backend/service/bpmn_types.go itsm-backend/service/bpmn_types_test.go itsm-backend/ent itsm-backend/migrations/021_add_callback_optional_declared.sql itsm-backend/migration/callback_optional_declared_test.go
git commit -m "feat(bpmn): snapshot definition-declared callback optionality"

git add itsm-backend/migration/migrations.go itsm-backend/migration
git commit -m "chore(migration): register callback optionality migration"
```

### Task 4: Normalize and validate callbacks before enqueue

**Files:**

- Create: `itsm-backend/service/bpmn_callback_enqueue_plan.go`
- Create: `itsm-backend/service/bpmn_callback_enqueue_plan_test.go`
- Modify: `itsm-backend/service/bpmn_callback_security.go`
- Create: `itsm-backend/service/bpmn_callback_security_test.go`
- Create: `itsm-backend/service/bpmn_callback_security_matrix_test.go`

- [ ] **Step 1: Write failing enqueue-plan tests**

Define a focused, engine-independent service:

```go
type CallbackEnqueuePlan struct {
    HandlerID       string
    TaskType        string
    Action          string
    ConfigRef       string
    Payload         map[string]interface{}
    OptionalDeclared bool
    BlockCode       CallbackBlockCode
    BlockMessage    string
}

func BuildCallbackEnqueuePlan(
    descriptor CallbackDescriptor,
    rawPayload map[string]interface{},
    optionalDeclared bool,
    registry *bpmn.CallbackRegistry,
) (CallbackEnqueuePlan, error)
```

Test:

- known handler/action filters to declared `PayloadFields`
- required field missing returns a plan blocked with `handler_contract`
- unknown handler/action returns a blocked plan, never an empty-success payload
- normalizer output cannot introduce undeclared fields
- malformed normalizer output returns an error or blocked plan, never raw-payload fallback
- `OptionalDeclared` is copied from parsed definition metadata only
- blocked plans retain only allowlisted, non-sensitive diagnostic data

Run: `cd itsm-backend && go test ./service/bpmn -run 'CallbackEnqueuePlan|CallbackSecurity' -count=1`

Expected: FAIL because current code silently returns an empty payload without a policy.

- [ ] **Step 2: Implement fail-closed normalization**

Replace the permissive payload-filter fallback. Build the descriptor and normalized payload through the declared handler/action contract. Keep server-authoritative task/tenant/work-item identity hydration at execution time; never trust callback payload copies of those fields.

- [ ] **Step 3: Separate deterministic validation blocks from infrastructure errors**

- Deterministic definition/config defects produce a blocked enqueue plan so the durable callback row can expose the block.
- Repository/registry failures return errors and prevent enqueue.
- Do not convert either case to optional skip during planning.

- [ ] **Step 4: Verify the security matrix**

Run: `cd itsm-backend && go test ./service/bpmn -run 'CallbackEnqueuePlan|CallbackSecurity|CallbackPayload' -count=1`

Expected: PASS, including tests that sensitive undeclared payload fields never enter the outbox.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn_callback_enqueue_plan.go itsm-backend/service/bpmn_callback_enqueue_plan_test.go itsm-backend/service/bpmn_callback_security.go itsm-backend/service/bpmn_callback_security_test.go itsm-backend/service/bpmn_callback_security_matrix_test.go
git commit -m "refactor(bpmn): validate callback effects before enqueue"
```

### Task 5: Make ordinary ticket notification return a proven delivery summary

**Files:**

- Modify: `itsm-backend/dto/ticket_notification_dto.go`
- Modify: `itsm-backend/service/ticket_notification_service.go`
- Create: `itsm-backend/service/ticket_notification_service_test.go`
- Modify: `itsm-backend/controller/ticket_notification_controller.go`
- Modify: `itsm-backend/controller/ticket_notification_controller_test.go`
- Modify: `itsm-backend/service/bpmn/ticket_handler.go`
- Modify: `itsm-backend/service/bpmn/ticket_handler_test.go`
- Modify: `itsm-backend/service/escalation_service.go`
- Modify: `itsm-backend/service/ticket_automation_rule_service.go`
- Modify: `itsm-backend/service/ticket_rating_service.go`
- Modify: `itsm-backend/service/ticket_notification_multi_channel_test.go`
- Modify: `itsm-backend/service/ticket_workflow_service_test.go`

- [ ] **Step 1: Define and test a delivery summary**

Change the service boundary from error-only success to:

```go
type SendTicketNotificationResult struct {
    Effect          string `json:"effect"`
    RecipientCount  int    `json:"recipientCount"`
    AppliedCount    int    `json:"appliedCount"`
    IdempotentCount int    `json:"idempotentCount"`
    DeliveryCount   int    `json:"deliveryCount"`
    BlockCode       string `json:"-"`
}

func (s *TicketNotificationService) SendNotification(
    ctx context.Context,
    ticketID int,
    req *dto.SendTicketNotificationRequest,
    tenantID int,
) (*dto.SendTicketNotificationResult, error)
```

Use `effect` values `applied`, `idempotent`, and `blocked`. A deterministic no-effect returns a non-nil blocked summary with an allowlisted internal `BlockCode`; adapter/database failures return an error. The HTTP controller converts blocked summaries to a non-2xx response, while the BPMN ticket handler maps them to `BlockedEffect`. `BlockCode` is never serialized to the public success response.

Tests must prove:

- zero requested recipients returns a blocked summary before writes
- missing/foreign-tenant recipients fail; they are not silently dropped
- all preferences disabled returns a blocked, zero-delivery summary
- first in-app delivery commits notification and delivery record atomically and returns `applied`
- the same delivery key returns `idempotent` without duplicates
- a database failure rolls back both records and is returned
- unavailable email/SMS delivery returns an error, not logged-and-swallowed

Run: `cd itsm-backend && go test ./service -run 'TicketNotification' -count=1`

Expected: FAIL under current swallowed-error behavior.

- [ ] **Step 2: Implement prevalidation and an atomic in-app write**

Resolve all recipients and preferences before writing. Use one Ent transaction for the notification and ticket-delivery records. Preserve tenant scope and stable delivery keys. Return adapter/database errors to the caller. Do not claim `applied` if `AppliedCount == 0`, and report mixed applied/idempotent batches deterministically as `applied` with both counters populated.

- [ ] **Step 3: Update every backend caller deliberately**

For escalation, rating, automation, and ticket-service callers, either propagate the error or record it through their existing durable/observable failure boundary; do not discard the result with `_`. Update the HTTP controller in this task so the signature change never leaves the repository uncompilable: return the real summary for applied/idempotent results and a non-2xx response for blocked results. Task 7 then adds strict unknown-field binding and aligns the frontend. For the BPMN ticket handler:

- summary `applied` → `AppliedEffect`
- summary `idempotent` → `IdempotentEffect`
- summary `blocked` → the matching allowlisted `CallbackBlockCode`
- infrastructure error → return `error` for retry

Do not introduce a second notification implementation inside BPMN.

- [ ] **Step 4: Run focused and caller tests**

Run: `cd itsm-backend && go test ./service/... -run 'Notification|Escalat|Rating|Automation' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/dto/ticket_notification_dto.go itsm-backend/controller/ticket_notification_controller.go itsm-backend/controller/ticket_notification_controller_test.go itsm-backend/service/ticket_notification_service.go itsm-backend/service/ticket_notification_service_test.go itsm-backend/service/ticket_notification_multi_channel_test.go itsm-backend/service/escalation_service.go itsm-backend/service/ticket_automation_rule_service.go itsm-backend/service/ticket_rating_service.go itsm-backend/service/ticket_workflow_service_test.go itsm-backend/service/bpmn/ticket_handler.go itsm-backend/service/bpmn/ticket_handler_test.go
git commit -m "refactor(notification): return durable delivery outcomes"
```

### Task 6: Persist callback outcomes and make optional skips observable

**Files:**

- Create: `itsm-backend/service/bpmn/callback_outcome_policy.go`
- Create: `itsm-backend/service/bpmn/callback_outcome_policy_test.go`
- Modify: `itsm-backend/service/bpmn_callback_outbox.go`
- Modify: `itsm-backend/service/bpmn_callback_outbox_test.go`
- Modify: `itsm-backend/service/bpmn_callback_recovery_test.go`
- Create: `itsm-backend/service/bpmn_callback_audit.go`
- Create: `itsm-backend/service/bpmn_callback_audit_test.go`
- Modify: `itsm-backend/metrics/metrics.go`
- Create: `itsm-backend/metrics/metrics_test.go`

- [ ] **Step 1: Write the outcome-policy truth table as tests**

Create a pure policy function whose result describes outbox terminal state, advancement permission, audit action, and metric effect:

| Handler result | `optional_declared` | Outbox result | Advance | Audit/metric |
|---|---:|---|---:|---|
| `applied` | either | `completed` | yes | `applied` metric |
| `idempotent` | either | `completed` | yes | `idempotent` metric |
| `blocked(code)` | false | `blocked`, `last_error_class=code` | no | `callback_blocked` + `blocked` metric |
| `blocked(code)` | true | `completed` | yes | `callback_skipped_optional` + `skipped_optional` metric |
| nil/invalid/handler skip | either | `blocked`, `last_error_class=handler_contract` | no | `callback_blocked` + `blocked` metric |
| returned error | either | existing retry policy | no | existing retry metric/log |

Run: `cd itsm-backend && go test ./service/bpmn ./service -run 'CallbackOutcome|CallbackOutbox' -count=1`

Expected: FAIL because the worker currently ignores handler results.

- [ ] **Step 2: Implement the pure policy and outbox transitions**

Use existing columns only:

- terminal block: `status="blocked"`, `last_error_class=<allowlisted code>`, `completed_at=<now>`; store sanitized diagnostic detail in `ProcessAuditLog`, because the outbox has no `last_error` column
- applied/idempotent/optional skip: `status="completed"`, clear transient error fields, set `completed_at`
- optional skip status exists in audit/metric, not a new outbox column

Ensure the claimer selects only pending/retryable expired-processing rows; it must never reclaim `blocked`.

- [ ] **Step 3: Add audit APIs**

Add focused methods on the existing `BPMNAuditService` in the new `bpmn_callback_audit.go` file. They write `ProcessAuditLog` actions `callback_blocked` and `callback_skipped_optional` with tenant, process instance, task, handler, action, block code, and definition-declared optional flag. Keeping these methods in a focused file avoids a Wave 1A edit collision with P1-C's ownership of `bpmn_audit_service.go`. Do not include raw payload, secrets, or arbitrary handler errors.

- [ ] **Step 4: Add bounded-cardinality metrics**

Register:

```go
var BPMNCallbackEffectsTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{Name: "itsm_bpmn_callback_effects_total"},
    []string{"handler_id", "action", "effect"},
)
```

`effect` is restricted to the four status constants. Never label by tenant, task ID, message, or block code.

- [ ] **Step 5: Extend recovery tests**

Prove lease/fencing behavior remains intact and add:

- non-optional block survives worker restart and is never reclaimed
- optional block audits once, advances once, and does not redeliver
- duplicate delivery returns idempotent and advances once
- malformed handler result blocks and does not advance
- transient error still retries under the existing error-class policy

Run: `cd itsm-backend && go test ./service -run 'CallbackOutbox|CallbackOutcome|CallbackAudit' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/service/bpmn/callback_outcome_policy.go itsm-backend/service/bpmn/callback_outcome_policy_test.go itsm-backend/service/bpmn_callback_outbox.go itsm-backend/service/bpmn_callback_outbox_test.go itsm-backend/service/bpmn_callback_recovery_test.go itsm-backend/service/bpmn_callback_audit.go itsm-backend/service/bpmn_callback_audit_test.go itsm-backend/metrics/metrics.go itsm-backend/metrics/metrics_test.go
git commit -m "feat(bpmn): persist and observe callback effects"
```

### Task 7: Unify ticket notification API and frontend on `eventType`

**Files:**

- Modify: `itsm-backend/dto/ticket_notification_dto.go`
- Modify: `itsm-backend/controller/ticket_notification_controller.go`
- Modify: `itsm-backend/controller/ticket_notification_controller_test.go`
- Modify: `itsm-frontend/src/lib/api/ticket-notification-api.ts`
- Modify: `itsm-frontend/src/lib/api/__tests__/ticket-notification-api.test.ts`
- Modify: `itsm-frontend/src/components/business/TicketNotificationSection.tsx`
- Modify: `itsm-frontend/src/components/business/__tests__/TicketNotificationSection.test.tsx`

- [ ] **Step 1: Lock the backend request/response contract with failing tests**

Request:

```json
{
  "userIds": [123],
  "eventType": "ticket_updated",
  "content": "已更新处理进度"
}
```

Response data:

```json
{
  "effect": "applied",
  "recipientCount": 1,
  "deliveryCount": 1
}
```

Test that `eventType` is required and allowlisted, `type` and `channel` are rejected as unknown request fields, and the controller returns the real service summary. Keep response/list DTO fields describing persisted notification `type` or `channel` if they remain authoritative read-model fields; remove them only from the send request.

Run: `cd itsm-backend && go test ./controller -run 'TicketNotification' -count=1`

Expected: FAIL because the controller currently returns an empty success body.

- [ ] **Step 2: Implement strict backend binding**

Use a decoder/binder path that rejects unknown JSON fields for this endpoint, validate `eventType` against the backend-owned event registry, call the service, and return `SendTicketNotificationResult`. Do not accept deprecated aliases.

- [ ] **Step 3: Write failing frontend API and component tests**

Assert:

- `SendTicketNotificationRequest` contains `userIds`, `eventType`, and `content` only
- the API serializes `eventType`, never `type`/`channel`
- the form loads and offers `eventTypes` returned by the existing notification-preferences API
- the channel selector is absent
- success UI uses the returned delivery summary
- zero-delivery/blocked/API failure shows an error and does not append a fake notification

Run: `cd itsm-frontend && npm test -- --runInBand src/lib/api/__tests__/ticket-notification-api.test.ts src/components/business/__tests__/TicketNotificationSection.test.tsx`

Expected: FAIL under the legacy request and `Date.now()` fake row.

- [ ] **Step 4: Implement the frontend contract and delete fallbacks**

Change:

```ts
export interface SendTicketNotificationRequest {
  userIds: number[];
  eventType: string;
  content: string;
}

export interface SendTicketNotificationResult {
  effect: 'applied' | 'idempotent';
  recipientCount: number;
  appliedCount: number;
  idempotentCount: number;
  deliveryCount: number;
}
```

Change `TicketNotificationApi.sendTicketNotification` from `Promise<void>` to `Promise<SendTicketNotificationResult>` and pass that result to the component success branch.

Use `dto.ListNotificationEventTypes()` as the backend validation registry and the existing notification-preferences response `eventTypes` as the form's runtime options. Do not maintain a second hardcoded frontend event list. Keep the TypeScript field as `string` because the backend registry is configuration data, then require selection from the fetched options. Remove the send-form `type` and `channel` fields, the unused `onNotificationSent` prop, the synthetic `tempNotification`, and any optimistic success that is not backed by response data. Refresh the server list after applied/idempotent success.

- [ ] **Step 5: Verify no legacy request path remains**

Run: `rg -n 'SendTicketNotificationRequest|onNotificationSent|tempNotification|channel.*in_app|type.*manual' itsm-frontend/src itsm-backend/dto itsm-backend/controller`

Expected: only authoritative read-model channel/type references remain; no send-request or fake-row references.

Run: `cd itsm-frontend && npm test -- --runInBand src/lib/api/__tests__/ticket-notification-api.test.ts src/components/business/__tests__/TicketNotificationSection.test.tsx && npm run type-check`

Expected: PASS.

- [ ] **Step 6: Commit backend and frontend separately**

```bash
git add itsm-backend/dto/ticket_notification_dto.go itsm-backend/controller/ticket_notification_controller.go itsm-backend/controller/ticket_notification_controller_test.go
git commit -m "fix(api): make event type authoritative for ticket notifications"

git add itsm-frontend/src/lib/api/ticket-notification-api.ts itsm-frontend/src/lib/api/__tests__/ticket-notification-api.test.ts itsm-frontend/src/components/business/TicketNotificationSection.tsx itsm-frontend/src/components/business/__tests__/TicketNotificationSection.test.tsx
git commit -m "refactor(frontend): remove legacy notification request fields"
```

### Task 8: Integrate the effect gate into the engine after P1-C

**Dependency:** Do not start until P1-C is merged, `bpmn_process_engine.go` is released by its owner, and P1-C focused engine tests pass. Then apply the prepared Task 2 handler/interface changes and this engine hook in the same working session and commit them atomically.

**Files:**

- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Modify: `itsm-backend/service/bpmn_process_engine_test.go`
- Modify: `itsm-backend/service/bpmn_callback_recovery_test.go`
- Use without changing unless integration requires it: `itsm-backend/service/bpmn_callback_enqueue_plan.go`
- Use without changing unless integration requires it: `itsm-backend/service/bpmn/callback_outcome_policy.go`

- [ ] **Step 1: Rebase/merge P1-C and establish its green baseline**

Run the exact P1-C verification commands plus:

`cd itsm-backend && go test ./service -run 'BPMN.*(Complete|Callback|Outbox|Authorization)' -count=1`

Expected: PASS before P1-D engine edits. If not, stop and resolve ownership rather than layering fixes.

- [ ] **Step 2: Write failing gate-order tests**

For a UserTask with a synchronous callback, assert transactionally observable order:

1. validate authorization and task identity
2. build strict enqueue plan and snapshot `optional_declared`
3. persist callback outbox row
4. do not advance the token yet
5. worker executes handler
6. only `applied`, `idempotent`, or engine-derived `skipped_optional` advances/completes
7. `blocked` leaves the process at the callback gate

Also prove a UserTask with no callback follows the existing direct completion path, and KAF/asynchronous delegation retains its lease/fencing path.

Example assertions:

```go
require.Equal(t, "pending", callback.Status)
require.True(t, callback.OptionalDeclared)
require.Equal(t, originalTokenPosition, loadTokenPosition(t, client, instanceID))

runCallbackWorkerOnce(t, svc)
require.Equal(t, advancedTokenPosition, loadTokenPosition(t, client, instanceID))
```

Run: `cd itsm-backend && go test ./service -run 'UserTaskCallbackGate|CallbackBlocksAdvance|OptionalCallbackSkip' -count=1`

Expected: FAIL because the current completion path advances before callback enqueue/execution and ignores the handler result.

- [ ] **Step 3: Wire the focused services into the engine**

- Replace inline callback filtering with `BuildCallbackEnqueuePlan`.
- Persist `optional_declared` from BPMN definition metadata into the outbox row.
- For deterministic enqueue-plan blocks, persist a durable blocked callback row with the allowlisted code; never report successful task completion.
- In callback execution, validate the returned effect and apply `CallbackOutcomePolicy` before token advancement.
- Make token advancement and terminal outbox transition idempotent under the existing transaction/lease/fencing design.
- Preserve the P1-C authorization/lifecycle changes; do not reintroduce pre-P1-C helper variants.

- [ ] **Step 4: Prove no-success-without-effect end to end**

Add integration cases for:

- missing target blocks task/process advancement
- empty recipient set blocks
- unknown CC type blocks
- unsupported placeholder blocks
- unavailable notification channel blocks
- duplicate callback is idempotent and advances once
- definition `callback_optional=true` skips with audit/metric and advances once
- absent/false/malformed optional metadata never skips
- crash after effect but before acknowledgement recovers idempotently

Run: `cd itsm-backend && go test ./service -run 'Callback|Outbox|UserTask' -count=1`

Expected: PASS.

- [ ] **Step 5: Prove obsolete engine paths are deleted**

Run: `rg -n 'result\.Success|ServiceTaskResult|filterCallbackPayload\(|executeStep\(.*callback' itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn`

Expected: no obsolete result check, permissive payload helper, or advance-before-callback branch.

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/service/bpmn itsm-backend/dto/bpmn_process_trigger_dto.go itsm-backend/controller/kaf_delegation_controller_test.go itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_process_engine_test.go itsm-backend/service/bpmn_process_engine_ext_test.go itsm-backend/service/bpmn_callback_recovery_test.go itsm-backend/service/bpmn_final_security_wave_test.go itsm-backend/service/kaf_delegation_service_test.go
git commit -m "fix(bpmn): gate process advancement on explicit callback effects"
```

### Task 9: Run release gates and document the Phase 1 boundary

**Files:**

- Modify if commands/contract changed: `docs/DEVELOPMENT_GUIDE.md`
- Modify if architecture summary changed: `AGENTS.md`
- Modify in the same change if `AGENTS.md` changed: `CLAUDE.md`
- Test: all files changed by Tasks 1–8

- [ ] **Step 1: Run backend focused tests**

```bash
cd itsm-backend
go test ./service/bpmn -count=1
go test ./service -run 'BPMN|Callback|Notification|Outbox' -count=1
go test ./controller -run 'TicketNotification' -count=1
go test ./migration -count=1
```

Expected: PASS.

- [ ] **Step 2: Run backend broad gates**

```bash
cd itsm-backend
go test ./...
go vet ./...
```

Expected: PASS. Record any unrelated pre-existing failure with its exact command/output; do not weaken or skip the new focused gates.

- [ ] **Step 3: Run frontend gates**

```bash
cd itsm-frontend
npm test -- --runInBand src/lib/api/__tests__/ticket-notification-api.test.ts src/components/business/__tests__/TicketNotificationSection.test.tsx
npm run type-check
npm run lint
npm run build
```

Expected: PASS.

- [ ] **Step 4: Run contract deletion checks**

```bash
rg -n 'ServiceTaskResult|Success:\s*(true|false)|CallbackPayloadPolicy' itsm-backend
rg -n 'onNotificationSent|tempNotification' itsm-frontend/src
rg -n 'json:"(type|channel)"' itsm-backend/dto/ticket_notification_dto.go
```

Expected: no obsolete callback result/policy, fake frontend callback, or legacy send-request fields. Review read-model `type`/`channel` matches manually; they may remain only where they describe persisted notifications.

- [ ] **Step 5: Verify architecture and migration boundaries**

Confirm:

- migration stream includes 020 then 021
- `process_callback_outboxes.optional_declared` is the only P1-D outbox schema addition
- no unified-Outbox callback dual write exists
- blocked callbacks are terminal and not reclaimed
- only BPMN definition metadata can produce optional skip
- each skip has one audit record and one bounded metric increment
- no handler reports `skipped_optional`
- ordinary notification and BPMN notification use the same delivery service
- frontend sends `eventType` only and displays server-confirmed outcomes

- [ ] **Step 6: Update documentation only if implementation changes commands or architectural summaries**

If no operational or architectural documentation text changed, leave `AGENTS.md`, `CLAUDE.md`, and `DEVELOPMENT_GUIDE.md` untouched. Otherwise update the owning document and keep the AGENTS/CLAUDE architecture summaries synchronized.

- [ ] **Step 7: Final commit**

```bash
git add docs/DEVELOPMENT_GUIDE.md AGENTS.md CLAUDE.md
git commit -m "docs: record callback effect verification contract"
```

Create this commit only when at least one listed document genuinely changed.

## Completion criteria

- Every synchronous callback produces a validated `applied`, `idempotent`, or `blocked` handler result.
- `skipped_optional` is derived only from strict, definition-declared metadata snapshotted in the callback outbox.
- A required blocked callback never advances its process token and remains visibly terminal.
- Empty/missing/unknown/unsupported callback inputs cannot be reported as success.
- Ticket notification sends return a durable effect summary; swallowed-error/no-delivery success paths are deleted.
- Frontend and backend use `eventType` as the only send-request discriminator; legacy `type`/`channel` and fake optimistic rows are deleted.
- Migration 021 is versioned and ordered after P1-A's 020; no historical migration or Phase 2 Outbox consolidation is included.
- P1-C engine changes are preserved and P1-D touches the engine only in the final dependency-gated integration task.
- Focused tests, broad backend checks, frontend checks, deletion searches, and migration checks all pass with captured evidence.
