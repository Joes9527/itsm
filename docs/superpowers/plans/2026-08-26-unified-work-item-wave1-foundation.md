# Unified Work Item — Wave 1 Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the shared ent schema fields, structured BPMN business identity, CallbackRegistry narrow-interface fixes, and the frontend WorkItemShell contract that every Wave 2 domain-migration task package (Incident/Problem/Change/ServiceRequest) will depend on.

**Architecture:** Single-writer schema change (one `go generate` pass, one commit) followed by small, independently-testable fixes to the BPMN engine and CallbackRegistry that consume the new fields, plus an isolated frontend contract package. This is Wave 1 of a larger multi-agent execution plan — Wave 2 (the four domain migrations) cannot start until this plan's tasks all land on `refactor/unified-work-item`.

**Tech Stack:** Go 1.x, entgo.io/ent (code-generation ORM), Gin, PostgreSQL, Next.js/TypeScript/React (App Router), Ant Design.

**Spec:** `docs/superpowers/specs/2026-08-26-unified-work-item-multi-agent-execution-plan.md` §4 (Wave 1), which itself implements `docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md` §6, §10, §15.2, §17.3.

## Global Constraints

- All ent schema changes land in exactly one commit with exactly one `go generate ./ent` run — no other task in this plan may touch `ent/schema/*.go` or re-run codegen (spec §0: ent codegen is not incrementally mergeable).
- `recordClass`/`record_class` is immutable after a professional extension row exists for that Ticket — enforced in Wave 2, not this plan, but no code in this plan may write to it more than once.
- No compatibility layers, bridge services, or "temporary" translation layers between old and new models (AGENTS.md "Do Not Patch Around Problems").
- Controllers/handlers never access `*ent.Client` directly for business logic — this plan actively removes two existing violations (Task 5, Task 6), it must not introduce new ones.
- Backend responses stay camelCase at the DTO boundary; Go files stay snake_case; this plan does not add any new HTTP endpoints, so this mostly applies to the one new DTO field surfaced in Task 7's frontend contract.
- `go build ./...` and `go test ./...` must pass after every task's commit — never leave the tree red between tasks.
- Every new dependency injected into a `service/bpmn` handler must go through a locally-declared narrow interface (matching the existing `TicketNotificationServiceInterface` pattern) — `service/bpmn` must never import the `service` package directly (would create an import cycle; `service` already imports `service/bpmn`).

---

### Task 1: Ent schema additions (single codegen pass)

**Files:**
- Modify: `itsm-backend/ent/schema/ticket.go`
- Modify: `itsm-backend/ent/schema/incident.go`
- Modify: `itsm-backend/ent/schema/problem.go`
- Modify: `itsm-backend/ent/schema/change.go`
- Modify: `itsm-backend/ent/schema/process_instance.go`
- Modify: `itsm-backend/ent/schema/processbinding.go`
- Modify: `itsm-backend/ent/schema/servicecatalog.go`
- Create: `itsm-backend/ent/schema/work_item_relation.go`
- Test: `itsm-backend/ent/schema/work_item_relation_test.go` (new — a minimal enttest smoke test; the other schema changes are covered by existing domain test suites once later tasks consume the new fields)

**Interfaces:**
- Consumes: nothing (first task).
- Produces: ent-generated types `ticket.Ticket.RecordClass/OpenedByID/AssignmentGroupID`, `incident.Incident.WorkItemID`, `problem.Problem.WorkItemID`, `change.Change.WorkItemID`, `processinstance.ProcessInstance.BusinessType/BusinessID`, `processbinding.ProcessBinding.CategoryID`, `servicecatalog.ServiceCatalog.TargetClass`, and the new `ent.WorkItemRelation` entity with fields `TenantID, SourceWorkItemID, TargetWorkItemID, RelationType, CreatedByID, Metadata, CreatedAt, DeletedAt`. Task 2-7 all depend on these exact field names.

- [ ] **Step 1: Add fields to `ticket.go`**

In `itsm-backend/ent/schema/ticket.go`, inside `func (Ticket) Fields() []ent.Field`, add these three fields right after the existing `field.String("source")...` field (keep everything else unchanged):

```go
		field.String("record_class").
			Comment("WorkItem 记录类型：generic/service_request_item/incident/problem/change_request/catalog_task；创建后不可变，由领域服务在事务内校验，不在 schema 层强制").
			Default("generic"),
		field.Int("opened_by_id").
			Comment("实际录入/触发者ID（区别于 requester_id 服务接受者）").
			Optional(),
		field.Int("assignment_group_id").
			Comment("当前处理组ID").
			Optional(),
```

Then in `func (Ticket) Indexes() []ent.Index`, add:

```go
		index.Fields("tenant_id", "record_class"),
```

- [ ] **Step 2: Add `work_item_id` to `incident.go`**

In `itsm-backend/ent/schema/incident.go`, inside `func (Incident) Fields() []ent.Field`, add right after the `field.Int("reporter_id")...` field:

```go
		field.Int("work_item_id").
			Comment("关联的 WorkItem（tickets.id），唯一，必填——Incident 迁移到 WorkItem 后每条记录必须有且仅有一条对应的 tickets 行").
			Optional().
			Unique(),
```

Note: `Optional().Unique()` (not `.Positive()` without `.Optional()`) — historical Incident rows have no WorkItem yet, and Wave 2's Incident migration task backfills this column before making it a hard requirement at the application layer. A nullable column can still carry a `Unique()` index in Postgres (multiple NULLs are allowed under a unique index), so this doesn't weaken the "at most one Incident per WorkItem" invariant once populated.

Add an `Indexes()` function (this schema doesn't have one yet) after `Edges()`:

```go
// Indexes of the Incident.
func (Incident) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("work_item_id"),
	}
}
```

Add `"entgo.io/ent/schema/index"` to the imports.

- [ ] **Step 3: Add `work_item_id` to `problem.go`**

In `itsm-backend/ent/schema/problem.go`, inside `func (Problem) Fields() []ent.Field`, add right after `field.Int("created_by")...`:

```go
		field.Int("work_item_id").
			Comment("关联的 WorkItem（tickets.id），唯一，必填——迁移完成前允许为空").
			Optional().
			Unique(),
```

Add an `Indexes()` function (doesn't exist yet):

```go
// Indexes of the Problem.
func (Problem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("work_item_id"),
	}
}
```

Add `"entgo.io/ent/schema/index"` to the imports.

- [ ] **Step 4: Add `work_item_id` to `change.go`**

In `itsm-backend/ent/schema/change.go`, inside `func (Change) Fields() []ent.Field`, add right after `field.Int("created_by")...`:

```go
		field.Int("work_item_id").
			Comment("关联的 WorkItem（tickets.id），唯一，必填——迁移完成前允许为空").
			Optional().
			Unique(),
```

Add an `Indexes()` function (doesn't exist yet):

```go
// Indexes of the Change.
func (Change) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("work_item_id"),
	}
}
```

Add `"entgo.io/ent/schema/index"` to the imports.

- [ ] **Step 5: Add structured business identity to `process_instance.go`**

In `itsm-backend/ent/schema/process_instance.go`, inside `func (ProcessInstance) Fields() []ent.Field`, add right after the existing `field.String("business_key")...` field:

```go
		field.String("business_type").
			Comment("结构化业务类型（recordClass：generic/service_request_item/incident/problem/change_request/catalog_task，历史保留值 release），迁移期与 business_key 由同一次 TriggerProcess 调用原子写入，不从 variables JSON 里现取").
			Optional(),
		field.Int("business_id").
			Comment("结构化业务主键（迁移完成前是各专业域自己的表主键，迁移完成后是 WorkItem ID/tickets.id），与 business_type 成对使用").
			Optional(),
```

Both `Optional()` (not `NotEmpty()`/`Positive()`) — the existing manual "start workflow" admin endpoint (`controller/bpmn_workflow_controller.go` `StartProcess`) only supplies a raw `businessKey` string with no structured type/id, and forcing these fields to be required would break that endpoint. The design spec's §4.1 table describes these as "非空/正数"; this plan deviates to `Optional()` for that concrete, verified reason — the domain-triggered path (the one that matters for WorkItem identity) always populates both via Task 2 below, so the invariant that matters in practice still holds.

In `func (ProcessInstance) Indexes() []ent.Index`, add:

```go
		index.Fields("tenant_id", "business_type", "business_id", "status"),
```

- [ ] **Step 6: Add `category_id` to `processbinding.go`**

In `itsm-backend/ent/schema/processbinding.go`, inside `func (ProcessBinding) Fields() []ent.Field`, add right after the existing `category` field:

```go
		field.Int("category_id").
			Comment("TicketCategory ID，可选——比 category 字符串更精确的分类匹配条件，后续 ProcessBindingService.FindBestBinding 可以按它精确匹配").
			Optional(),
```

In `func (ProcessBinding) Indexes() []ent.Index`, add:

```go
		index.Fields("tenant_id", "business_type"),
```

(This is intentionally a narrower index than the existing 5-column composite one — it exists so a lookup by just tenant+businessType, without knowing department/team/scenario, doesn't have to scan the wider index.)

- [ ] **Step 7: Add `target_class` to `servicecatalog.go`**

In `itsm-backend/ent/schema/servicecatalog.go`, inside `func (ServiceCatalog) Fields() []ent.Field`, add right after the `itsm_type` field:

```go
		field.String("target_class").Comment("WorkItem 目标类：service_request_item|incident|change_request，Wave 2 由 itsm_type 迁移填充，本阶段只加列").Optional(),
```

(Match this file's existing single-line field style, not the multi-line style used elsewhere — follow the file you're editing.)

- [ ] **Step 8: Create `work_item_relation.go`**

Create `itsm-backend/ent/schema/work_item_relation.go`:

```go
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WorkItemRelation holds the schema definition for structured cross-domain
// relationships between WorkItems (Incident/Problem/Change/ServiceRequestItem/
// CatalogTask, all physically rows in the tickets table). See
// docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md §10.
//
// This schema intentionally has no ent edges to Ticket — source/target are
// plain int columns joined at the application layer, matching how
// ProcessTask.process_instance_id already inherits identity by plain FK
// rather than a required ent edge elsewhere in this codebase. Keeping this
// table decoupled from Ticket's own Edges() means Wave 2 domain migrations
// don't need to touch ticket.go a second time just to add relation traversal.
type WorkItemRelation struct {
	ent.Schema
}

// Fields of the WorkItemRelation.
func (WorkItemRelation) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").
			Comment("租户ID").
			Positive(),
		field.Int("source_work_item_id").
			Comment("源 WorkItem（tickets.id）").
			Positive(),
		field.Int("target_work_item_id").
			Comment("目标 WorkItem（tickets.id）").
			Positive(),
		field.String("relation_type").
			Comment("关系类型：investigated_by/caused_by/resolved_by_change/requested_change/fulfilled_by/parent_child/duplicate_of/related_to").
			NotEmpty(),
		field.Int("created_by_id").
			Comment("创建人ID").
			Positive(),
		field.JSON("metadata", map[string]interface{}{}).
			Comment("少量关系专属元数据，不存业务主体").
			Optional(),
		field.Time("created_at").
			Comment("创建时间").
			Default(time.Now),
		field.Time("deleted_at").
			Comment("软删除时间").
			Optional().
			Nillable(),
	}
}

// Indexes of the WorkItemRelation.
func (WorkItemRelation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "source_work_item_id", "target_work_item_id", "relation_type").
			Unique(),
		index.Fields("tenant_id", "target_work_item_id"),
	}
}
```

- [ ] **Step 9: Regenerate ent code**

Run: `cd itsm-backend && go generate ./ent`

Expected: completes without error; `git status` shows changes across `ent/client.go`, `ent/tx.go`, `ent/mutation.go`, `ent/runtime.go`, `ent/migrate/schema.go`, plus new `ent/workitemrelation/`, `ent/workitemrelation_*.go` files, and diffs in `ent/incident.go`, `ent/problem.go`, `ent/change.go`, `ent/ticket.go`, `ent/processinstance.go`, `ent/processbinding.go`, `ent/servicecatalog.go`.

- [ ] **Step 10: Write the WorkItemRelation smoke test**

Create `itsm-backend/ent/schema/work_item_relation_test.go`:

```go
package schema_test

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestWorkItemRelation_UniqueConstraint(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:workitemrelation?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	_, err := client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("related_to").
		SetCreatedByID(1).
		Save(ctx)
	require.NoError(t, err)

	// Same (tenant, source, target, relation_type) tuple must be rejected.
	_, err = client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("related_to").
		SetCreatedByID(1).
		Save(ctx)
	require.Error(t, err)

	// A different relation_type between the same two WorkItems is allowed.
	_, err = client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("duplicate_of").
		SetCreatedByID(1).
		Save(ctx)
	require.NoError(t, err)
}
```

This matches the exact `enttest.Open(t, "sqlite3", "file:...?mode=memory&cache=shared&_fk=1")` + `_ "github.com/mattn/go-sqlite3"` pattern already used in `itsm-backend/handlers/service_catalog/service_test.go` — no need to invent a different setup.

- [ ] **Step 11: Run the test and verify the build**

Run: `cd itsm-backend && go test ./ent/schema/... -run TestWorkItemRelation -v`
Expected: PASS, including the second `Create()` call failing with a unique constraint error.

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 12: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/ent/schema/ticket.go itsm-backend/ent/schema/incident.go \
  itsm-backend/ent/schema/problem.go itsm-backend/ent/schema/change.go \
  itsm-backend/ent/schema/process_instance.go itsm-backend/ent/schema/processbinding.go \
  itsm-backend/ent/schema/servicecatalog.go itsm-backend/ent/schema/work_item_relation.go \
  itsm-backend/ent/schema/work_item_relation_test.go itsm-backend/ent/
git commit -m "feat(work-item): add Wave 1 ent schema foundation (record_class, work_item_id, structured BPMN identity, WorkItemRelation)"
```

---

### Task 2: Populate structured `business_type`/`business_id` at process-trigger time

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:43` (interface), `:208-256` (`StartProcess` implementation)
- Modify: `itsm-backend/service/bpmn_process_trigger_service.go:93` (call site)
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go:453` (call site)
- Test: `itsm-backend/service/bpmn_process_engine_test.go` (add a case to an existing `TestStartProcess`-style test, or create one if none exists — check first)

**Interfaces:**
- Consumes: `ent.ProcessInstance.BusinessType`/`BusinessID` field setters from Task 1's codegen (`SetBusinessType(string)`, `SetBusinessID(int)`).
- Produces: `ProcessEngine.StartProcess(ctx, processDefinitionKey, businessKey, businessType, businessID, variables)` — the new signature every future caller (including Wave 2 domain migrations) must use.

- [ ] **Step 1: Check for existing StartProcess tests**

Run: `grep -rn "StartProcess(" itsm-backend/service/*_test.go itsm-backend/controller/*_test.go`

If a test calls `StartProcess` with the old 4-argument signature, note its exact location — it needs updating in Step 5 below alongside the two production call sites.

- [ ] **Step 2: Extend the `ProcessEngine` interface**

In `itsm-backend/service/bpmn_process_engine.go`, change line 43 from:

```go
	StartProcess(ctx context.Context, processDefinitionKey string, businessKey string, variables map[string]interface{}) (*ent.ProcessInstance, error)
```

to:

```go
	StartProcess(ctx context.Context, processDefinitionKey string, businessKey string, businessType string, businessID int, variables map[string]interface{}) (*ent.ProcessInstance, error)
```

- [ ] **Step 3: Update the implementation**

In `itsm-backend/service/bpmn_process_engine.go`, change the `StartProcess` function signature (currently at line 208) to match, and update the `ProcessInstance.Create()` chain. Find:

```go
func (e *CustomProcessEngine) StartProcess(ctx context.Context, processDefinitionKey string, businessKey string, variables map[string]interface{}) (*ent.ProcessInstance, error) {
```

Replace with:

```go
func (e *CustomProcessEngine) StartProcess(ctx context.Context, processDefinitionKey string, businessKey string, businessType string, businessID int, variables map[string]interface{}) (*ent.ProcessInstance, error) {
```

Find the `ProcessInstance.Create()` chain (currently):

```go
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
		Save(ctx)
```

Replace with:

```go
	createInstance := e.client.ProcessInstance.Create().
		SetProcessInstanceID(fmt.Sprintf("PI-%s-%d", processDefinitionKey, time.Now().UnixNano())).
		SetBusinessKey(businessKey).
		SetProcessDefinitionKey(processDefinitionKey).
		SetProcessDefinitionID(definition.ID).
		SetStatus("running").
		SetVariables(variables).
		SetStartTime(time.Now()).
		SetTenantID(definition.TenantID).
		SetCurrentActivityID(startEvent.ID).
		SetCurrentActivityName(startEvent.Name)
	if businessType != "" {
		createInstance = createInstance.SetBusinessType(businessType)
	}
	if businessID > 0 {
		createInstance = createInstance.SetBusinessID(businessID)
	}
	instance, err := createInstance.Save(ctx)
```

- [ ] **Step 4: Update the domain-triggered call site**

In `itsm-backend/service/bpmn_process_trigger_service.go`, find (around line 93):

```go
	instance, err := s.processEngine.StartProcess(triggerCtx, processDefKey, businessKey, variables)
```

Replace with:

```go
	instance, err := s.processEngine.StartProcess(triggerCtx, processDefKey, businessKey, strings.ToLower(string(req.BusinessType)), req.BusinessID, variables)
```

`strings` is already imported in this file (used by `buildBusinessKey`). This is the single source of truth this task exists for: `businessKey`, `businessType`, and `businessID` are all derived from the same `req` in the same function call, matching spec §4.1.1's "same call, one computation" requirement.

- [ ] **Step 5: Update the manual/admin call site**

In `itsm-backend/controller/bpmn_workflow_controller.go`, find (around line 453):

```go
	instance, err := c.processEngine.StartProcess(workflowCtx, req.ProcessDefinitionKey, req.BusinessKey, req.Variables)
```

Replace with:

```go
	instance, err := c.processEngine.StartProcess(workflowCtx, req.ProcessDefinitionKey, req.BusinessKey, "", 0, req.Variables)
```

This endpoint's request struct has no structured business identity (it's a generic admin/debug "start any process" tool) — passing `"", 0` leaves the new columns empty for instances started this way, which is correct: nothing downstream should treat admin-triggered test instances as having a real WorkItem identity.

- [ ] **Step 6: Fix any existing test call sites found in Step 1**

Update each one to the new 6-argument signature, passing `"", 0` unless the test is specifically about business identity (in which case pass real values and extend the assertion to check `instance.BusinessType`/`instance.BusinessID`).

- [ ] **Step 7: Write a new test for the structured identity**

There is no existing `bpmn_process_trigger_service_test.go` in this repo — create it fresh. The fixture pattern below (deploy real default templates via `BPMNTemplateService.LoadAndDeployTemplates`, then call `TriggerProcess` against the real `change_normal_flow` template it deploys) is copied verbatim from the working pattern already proven in `itsm-backend/handlers/change/service_bpmn_test.go`'s `TestCompleteChangeApprovalTask_ApproveCompletesScheduleNode` — reusing `change_normal_flow` here (rather than inventing a minimal test-only BPMN template) means this test doesn't depend on guessing what other default templates are named.

Create `itsm-backend/service/bpmn_process_trigger_service_test.go`:

```go
package service

import (
	"context"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestTriggerProcess_PopulatesStructuredBusinessIdentity(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:trigger_business_identity?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Trigger Identity Tenant").
		SetCode("trigger-identity").
		SetDomain("trigger-identity.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	logger := zaptest.NewLogger(t).Sugar()
	engine := NewCustomProcessEngine(client, logger)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)

	deploySvc := NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := NewProcessTriggerService(client, engine)
	resp, err := trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           42,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": false},
		TenantID:             tenant.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "change:42", resp.BusinessKey)

	// dto.ProcessTriggerResponse.ProcessInstanceID is the ent row's integer primary
	// key (instance.ID), not the string BPMN engine id (instance.ProcessInstanceID) —
	// confirmed against the response construction in TriggerProcess itself
	// (service/bpmn_process_trigger_service.go: "ProcessInstanceID: instance.ID").
	instance, err := client.ProcessInstance.Get(ctx, resp.ProcessInstanceID)
	require.NoError(t, err)
	require.Equal(t, "change", instance.BusinessType)
	require.Equal(t, 42, instance.BusinessID)
}
```

- [ ] **Step 8: Run tests and verify build**

Run: `go build ./... && go test ./service/... ./controller/... -run "StartProcess|TriggerProcess" -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_process_trigger_service.go \
  itsm-backend/controller/bpmn_workflow_controller.go itsm-backend/service/bpmn_process_trigger_service_test.go
git commit -m "feat(work-item): populate structured ProcessInstance business identity at trigger time"
```

---

### Task 3: Fix `recordApprovalDecision` to read structured fields, not `Variables` JSON

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go:495-518` (`recordApprovalDecision`)
- Test: `itsm-backend/service/bpmn_process_engine_test.go`

**Interfaces:**
- Consumes: `instance.BusinessType`/`instance.BusinessID` (populated by Task 2).
- Produces: nothing new — this is an internal correctness fix, no signature changes.

- [ ] **Step 1: Write the failing test**

Find the existing test file covering `recordApprovalDecision` (search: `grep -rn "recordApprovalDecision\|ProcessApprovalDecision" itsm-backend/service/*_test.go`). Add a test that starts a process with `businessType="change"`, `businessID=99` via the now-updated `StartProcess`, drives it to a user task, completes the task with `variables["approvalAction"]="approve"` but **without** setting `variables["business_type"]`/`variables["business_id"]` in the completion variables (simulating a caller that doesn't inject them, unlike `BPMNApprovalBridge` today) — then assert the resulting `ProcessApprovalDecision` row still has `BusinessType == "change"` and `BusinessID == "99"`.

This is the actual regression this task closes: today, if a caller forgets to inject `variables["business_type"]`/`["business_id"]` before completing an approval task, `recordApprovalDecision` silently writes `"<nil>"` strings (the `fmt.Sprint` of a missing map key) — after this fix it can't happen because the values come from the instance itself, not from what the completer happened to pass in.

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./service/... -run TestRecordApprovalDecision -v` (or whatever name you gave the test)
Expected: FAIL — `BusinessType`/`BusinessID` come back empty/`"<nil>"`.

- [ ] **Step 3: Fix `recordApprovalDecision`**

In `itsm-backend/service/bpmn_process_engine.go`, find:

```go
	businessType := fmt.Sprint(instance.Variables["business_type"])
	businessID := fmt.Sprint(instance.Variables["business_id"])
```

Replace with:

```go
	businessType := instance.BusinessType
	businessID := strconv.Itoa(instance.BusinessID)
```

Add `"strconv"` to this file's imports if not already present (check first — this file is large and may already import it for other functions).

- [ ] **Step 4: Run the test again**

Run: `go test ./service/... -run TestRecordApprovalDecision -v`
Expected: PASS.

- [ ] **Step 5: Run the full BPMN-related suite to catch regressions**

Run: `go test ./service/... ./service/bpmn/... ./handlers/change/... -count=1`
Expected: all PASS — this specifically must not break `TestChangeServiceTaskHandler_*`/`TestTransitionStatus_*` (Track4's approval regression suite), since those flows go through this exact function whenever a Change approval is completed.

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_process_engine_test.go
git commit -m "fix(work-item): recordApprovalDecision reads structured ProcessInstance identity instead of Variables JSON"
```

---

### Task 4: Data integrity check command

**Files:**
- Create: `itsm-backend/cmd/check_work_item_integrity/main.go`
- Test: manual (this is a one-off ops tool; verify by running it against a deliberately-inconsistent fixture, per Step 4 below — matches the verification style already used for `cmd/backfill_legacy_pending_changes`, which also has no `_test.go`)

**Interfaces:**
- Consumes: `ticket.Ticket.RecordClass`, `incident.Incident.WorkItemID`, `problem.Problem.WorkItemID`, `change.Change.WorkItemID` (Task 1).
- Produces: nothing consumed by later tasks — this is a standalone ops command, per spec §4.2.

- [ ] **Step 1: Write the command**

Create `itsm-backend/cmd/check_work_item_integrity/main.go`:

```go
// check_work_item_integrity 是 Wave 1（统一 Work Item 领域模型重构）新增的常驻可重复运行的
// 数据完整性检查工具，不是一次性迁移脚本——设计文档 §18.3-9。
//
// 检查内容：一条 tickets 行的 record_class 若不是 "generic"，就应该有且仅有一条对应专业
// 扩展表（incidents/problems/changes）的行通过 work_item_id 指回它；反之，一条专业扩展表
// 行的 work_item_id 若指向某个 tickets.id，那条 ticket 的 record_class 应该跟这张扩展表
// 匹配。任何一边对不上都报告为异常，不自动修复——自动修复需要业务判断（比如该建一条缺失的
// 专业记录，还是该纠正 record_class），这个工具只负责发现，不负责决定怎么修。
//
// 用法：
//
//	go run ./cmd/check_work_item_integrity -tenant-id=0   # 0 表示检查所有租户
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

type mismatch struct {
	kind        string
	ticketID    int
	tenantID    int
	recordClass string
	detail      string
}

func main() {
	tenantID := flag.Int("tenant-id", 0, "只检查指定租户（<=0 表示检查所有租户）")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	client, err := database.InitDatabaseWithRLS(&cfg.Database, &cfg.RLS, sugar)
	if err != nil {
		sugar.Fatalw("connect database", "error", err)
	}
	defer client.Close()

	ctx := tenantctx.SystemContext(
		context.Background(),
		"ops:check_work_item_integrity",
		"WorkItem 重构：record_class 与专业扩展表 work_item_id 一致性检查",
	)

	mismatches, err := findMismatches(ctx, client, *tenantID)
	if err != nil {
		sugar.Fatalw("检查失败", "error", err)
	}

	if len(mismatches) == 0 {
		sugar.Infow("未发现不一致", "tenant_id", *tenantID)
		return
	}

	sugar.Warnw("发现不一致记录", "count", len(mismatches), "tenant_id", *tenantID)
	for _, m := range mismatches {
		sugar.Warnw("不一致", "kind", m.kind, "ticket_id", m.ticketID, "tenant_id", m.tenantID,
			"record_class", m.recordClass, "detail", m.detail)
	}
	os.Exit(1)
}

func findMismatches(ctx context.Context, client *ent.Client, tenantID int) ([]mismatch, error) {
	var out []mismatch

	// 1) record_class != generic 但找不到对应专业扩展记录。
	q := client.Ticket.Query().Where(ticket.RecordClassNEQ("generic"))
	if tenantID > 0 {
		q = q.Where(ticket.TenantID(tenantID))
	}
	tickets, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询非 generic 工单失败: %w", err)
	}
	for _, t := range tickets {
		var exists bool
		var checkErr error
		switch t.RecordClass {
		case "incident":
			exists, checkErr = client.Incident.Query().Where(incident.WorkItemID(t.ID)).Exist(ctx)
		case "problem":
			exists, checkErr = client.Problem.Query().Where(problem.WorkItemID(t.ID)).Exist(ctx)
		case "change_request":
			exists, checkErr = client.Change.Query().Where(change.WorkItemID(t.ID)).Exist(ctx)
		default:
			// service_request_item/catalog_task 在 Wave 1 阶段还没有对应的
			// work_item_id 外键（ServiceRequest 沿用既有 ticket_id 列，CatalogTask
			// 是 Wave 2 才新建的表），这两类暂不检查，留给各自的 Wave 2 任务包。
			continue
		}
		if checkErr != nil {
			return nil, fmt.Errorf("查询 ticket %d 的专业扩展记录失败: %w", t.ID, checkErr)
		}
		if !exists {
			out = append(out, mismatch{
				kind: "missing_extension", ticketID: t.ID, tenantID: t.TenantID,
				recordClass: t.RecordClass,
				detail:      fmt.Sprintf("record_class=%s 但找不到 work_item_id=%d 的专业扩展记录", t.RecordClass, t.ID),
			})
		}
	}

	// 2) 专业扩展记录的 work_item_id 指向的 ticket 的 record_class 对不上。
	incidents, err := queryScoped(ctx, client.Incident.Query().Where(incident.WorkItemIDNotNil()), tenantID)
	if err != nil {
		return nil, err
	}
	for _, i := range incidents {
		if err := checkBackref(ctx, client, i.WorkItemID, i.TenantID, "incident", &out); err != nil {
			return nil, err
		}
	}
	problems, err := queryScopedProblem(ctx, client, tenantID)
	if err != nil {
		return nil, err
	}
	for _, p := range problems {
		if err := checkBackref(ctx, client, p.WorkItemID, p.TenantID, "problem", &out); err != nil {
			return nil, err
		}
	}
	changes, err := queryScopedChange(ctx, client, tenantID)
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		if err := checkBackref(ctx, client, c.WorkItemID, c.TenantID, "change_request", &out); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func checkBackref(ctx context.Context, client *ent.Client, workItemID, tenantID int, expectedClass string, out *[]mismatch) error {
	t, err := client.Ticket.Get(ctx, workItemID)
	if err != nil {
		if ent.IsNotFound(err) {
			*out = append(*out, mismatch{
				kind: "dangling_work_item_id", ticketID: workItemID, tenantID: tenantID,
				recordClass: expectedClass,
				detail:      fmt.Sprintf("work_item_id=%d 指向的 ticket 不存在", workItemID),
			})
			return nil
		}
		return fmt.Errorf("查询 ticket %d 失败: %w", workItemID, err)
	}
	if t.RecordClass != expectedClass {
		*out = append(*out, mismatch{
			kind: "record_class_mismatch", ticketID: workItemID, tenantID: tenantID,
			recordClass: t.RecordClass,
			detail:      fmt.Sprintf("专业扩展记录期望 record_class=%s，实际是 %s", expectedClass, t.RecordClass),
		})
	}
	return nil
}
```

- [ ] **Step 2: Add the three small query-scoping helpers**

Append to the same file (kept separate from `findMismatches` only because each professional table needs its own generated query type — this is boilerplate, not duplicated logic):

```go
func queryScoped(ctx context.Context, q *ent.IncidentQuery, tenantID int) ([]*ent.Incident, error) {
	if tenantID > 0 {
		q = q.Where(incident.TenantID(tenantID))
	}
	return q.All(ctx)
}

func queryScopedProblem(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.Problem, error) {
	q := client.Problem.Query().Where(problem.WorkItemIDNotNil())
	if tenantID > 0 {
		q = q.Where(problem.TenantID(tenantID))
	}
	return q.All(ctx)
}

func queryScopedChange(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.Change, error) {
	q := client.Change.Query().Where(change.WorkItemIDNotNil())
	if tenantID > 0 {
		q = q.Where(change.TenantID(tenantID))
	}
	return q.All(ctx)
}
```

- [ ] **Step 3: Build it**

Run: `go build ./cmd/check_work_item_integrity/`
Expected: no errors. If `WorkItemIDNotNil()`/`RecordClassNEQ()`-style generated predicate names don't match exactly what Task 1's codegen produced, check `ent/incident/where.go` (generated) for the actual predicate function names and fix the calls above to match — ent's naming for nullable-int predicates is consistent (`FieldNameNotNil`) but confirm against the generated file rather than assuming.

- [ ] **Step 4: Manually verify against a deliberately-broken fixture**

Run this against a local dev database (not production):

```bash
cd itsm-backend
go run -tags create_user main.go   # if you don't already have a dev DB with at least one tenant/ticket
```

Then, using `psql` or the app itself, set one ticket's `record_class` to `'incident'` without creating a matching `incidents` row (e.g. `UPDATE tickets SET record_class='incident' WHERE id=<some id with no incident row>;`), then run:

```bash
go run ./cmd/check_work_item_integrity -tenant-id=<that tenant>
```

Expected: exits with status 1 and logs a `missing_extension` mismatch naming that ticket ID. Revert the manual `UPDATE` afterward.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/cmd/check_work_item_integrity/
git commit -m "feat(work-item): add record_class/work_item_id integrity check command"
```

---

### Task 5: CallbackRegistry — Ticket handler stops writing Ent directly

**Files:**
- Modify: `itsm-backend/service/bpmn/ticket_handler.go`
- Modify: `itsm-backend/service/ticket_service.go` (add one adapter method)
- Modify: `itsm-backend/internal/bootstrap/app.go` (wire the new setter)
- Test: `itsm-backend/service/bpmn/ticket_handler_test.go` (check if it exists first; extend or create)

**Interfaces:**
- Consumes: `TicketService.UpdateTicketStatus(ctx, ticketID, status, tenantID, operatorID) (*ticket.Ticket, error)` (already exists at `service/ticket_service.go:1549`).
- Produces: `TicketServiceTaskHandler.SetTicketService(svc TicketStatusServiceInterface)`; `TicketService.UpdateTicketStatusForWorkflow(ctx, ticketID, status, tenantID, operatorID) error`.

- [ ] **Step 1: Add the adapter method on `TicketService`**

`service/bpmn` cannot import the `service` package (would create an import cycle — `service` already imports `service/bpmn`). Any interface the `bpmn` package declares locally must therefore match a method whose signature it can express without importing `service`'s own domain types. `TicketService.UpdateTicketStatus` returns `*ticket.Ticket` where `ticket` is `itsm-backend/repository/ticket` (the DDD domain type) — rather than have the `bpmn` package import that domain package just to declare a matching interface, add a thin adapter that returns only `error`.

In `itsm-backend/service/ticket_service.go`, add right after the existing `UpdateTicketStatus` function (currently ending around line 1549+its body):

```go
// UpdateTicketStatusForWorkflow 是给 BPMN ServiceTask handler 用的窄接口适配：
// handler 不需要返回的领域 Ticket 对象，也不能 import service 包（会形成 service ->
// service/bpmn -> service 的循环依赖），所以这里只暴露一个返回 error 的签名，供
// service/bpmn 包本地声明的接口去匹配。
func (s *TicketService) UpdateTicketStatusForWorkflow(ctx context.Context, ticketID int, status string, tenantID int, operatorID int) error {
	_, err := s.UpdateTicketStatus(ctx, ticketID, status, tenantID, operatorID)
	return err
}
```

- [ ] **Step 2: Declare the narrow interface and add the setter in `ticket_handler.go`**

In `itsm-backend/service/bpmn/ticket_handler.go`, add a new interface next to the existing `TicketNotificationServiceInterface`:

```go
// TicketStatusServiceInterface 工单状态更新服务接口（避免循环依赖，见
// TicketService.UpdateTicketStatusForWorkflow 的注释）
type TicketStatusServiceInterface interface {
	UpdateTicketStatusForWorkflow(ctx context.Context, ticketID int, status string, tenantID int, operatorID int) error
}
```

Add a field to the struct and a setter, mirroring how `notificationService` is already handled:

```go
type TicketServiceTaskHandler struct {
	HandlerBase
	client              *ent.Client
	logger              *zap.SugaredLogger
	notificationService TicketNotificationServiceInterface
	statusService       TicketStatusServiceInterface
}
```

```go
// SetTicketService 注入工单状态服务，由 bootstrap 在 TicketService 构造完成后调用
// （TicketService 构造时依赖的东西比 CallbackRegistry 晚初始化，不能在 NewTicketServiceTaskHandler
// 里直接注入，跟 SetNotificationService 是同一个延迟装配模式）。
func (h *TicketServiceTaskHandler) SetTicketService(svc TicketStatusServiceInterface) {
	h.statusService = svc
}
```

- [ ] **Step 3: Replace the direct Ent write in `updateTicketStatus`**

In `itsm-backend/service/bpmn/ticket_handler.go`, find the `updateTicketStatus` function body (currently uses `h.client.Ticket.UpdateOneID(...)`). Replace the "执行更新" section:

```go
	// 执行更新
	tenantID := h.getTenantID(ctx, variables)
	update := h.client.Ticket.UpdateOneID(ticketID)
	if tenantID > 0 {
		update = update.Where(ticket.TenantID(tenantID))
	}
	if newStatus == "resolved" || newStatus == "closed" {
		update = update.SetResolvedAt(time.Now())
	}
	_, err := update.
		SetStatus(newStatus).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		h.logger.Errorw("Failed to update ticket status", "ticket_id", ticketID, "error", err)
		return nil, fmt.Errorf("更新工单状态失败: %w", err)
	}
```

with:

```go
	// 执行更新——通过 TicketService 走状态机校验、通知、飞书同步等既有业务规则，
	// 不再绕过领域服务直接改 Ent（AGENTS.md：Handler 不能绕过专业服务直接修改状态）。
	if h.statusService == nil {
		return nil, fmt.Errorf("ticket status service 未注入，无法更新工单状态")
	}
	tenantID := h.getTenantID(ctx, variables)
	// operatorID=0 表示系统身份（BPMN 引擎驱动的状态变更，不是某个登录用户点的按钮）；
	// TicketService.UpdateTicketStatus 本身不强制 operatorID>0。
	if err := h.statusService.UpdateTicketStatusForWorkflow(ctx, ticketID, newStatus, tenantID, 0); err != nil {
		h.logger.Errorw("Failed to update ticket status", "ticket_id", ticketID, "error", err)
		return nil, fmt.Errorf("更新工单状态失败: %w", err)
	}
```

Remove the now-unused `"itsm-backend/ent/ticket"` and `"time"` imports from this file only if nothing else in the file still uses them — check with `grep -n "ticket\.\|time\." itsm-backend/service/bpmn/ticket_handler.go` first, this file has other functions.

- [ ] **Step 4: Wire the setter in bootstrap**

In `itsm-backend/internal/bootstrap/app.go`, find the existing block of `Set*Service` calls (around line 453, where `ticketService.SetNotificationService(ticketNotificationService)` lives). You'll need the `CallbackRegistry` accessor from Step 5 of this task (added to `CustomProcessEngine`) — add it right after `ticketService` is fully constructed:

```go
	if h, ok := processEngine.CallbackRegistry().GetHandler("ticket_service_handler").(*bpmn.TicketServiceTaskHandler); ok {
		h.SetTicketService(ticketService)
	}
```

Add `"itsm-backend/service/bpmn"` to `app.go`'s imports if not already present (check first — this file is huge and likely already imports it for other handler types).

- [ ] **Step 5: Expose `CallbackRegistry()` from `CustomProcessEngine`**

In `itsm-backend/service/bpmn_process_engine.go`, add a new public accessor next to the existing `ProcessDefinitionService()`/`TaskService()` accessors:

```go
// CallbackRegistry 暴露内部的 ServiceTask 回调注册中心，供 bootstrap 在各领域 service
// 构造完成后做延迟依赖注入（跟 TicketService.SetNotificationService 是同一个模式）。
func (e *CustomProcessEngine) CallbackRegistry() *bpmn.CallbackRegistry {
	return e.callbackRegistry
}
```

- [ ] **Step 6: Write the test**

Check if `itsm-backend/service/bpmn/ticket_handler_test.go` exists (`ls itsm-backend/service/bpmn/ticket_handler_test.go`). Add or create a test. `Execute` requires a numeric `variables["business_id"]` before it even reaches the `action` switch (it returns `"无效的 business_id"` otherwise) — the action key for this path is `"update_status"`, not `"ticket_id"`:

```go
func TestTicketServiceTaskHandler_UpdateStatus_RequiresInjectedService(t *testing.T) {
	handler := NewTicketServiceTaskHandler(nil, zap.NewNop().Sugar())
	// statusService is nil (never injected) — must fail loud, not silently no-op.
	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"business_id": 1,
		"action":      "update_status",
		"new_status":  "in_progress",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "未注入")
}

func TestTicketServiceTaskHandler_UpdateStatus_DelegatesToInjectedService(t *testing.T) {
	handler := NewTicketServiceTaskHandler(nil, zap.NewNop().Sugar())
	fake := &fakeTicketStatusService{}
	handler.SetTicketService(fake)

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"business_id": 42,
		"action":      "update_status",
		"new_status":  "resolved",
	})
	require.NoError(t, err)
	require.Equal(t, 42, fake.lastTicketID)
	require.Equal(t, "resolved", fake.lastStatus)
}

type fakeTicketStatusService struct {
	lastTicketID int
	lastStatus   string
}

func (f *fakeTicketStatusService) UpdateTicketStatusForWorkflow(ctx context.Context, ticketID int, status string, tenantID int, operatorID int) error {
	f.lastTicketID = ticketID
	f.lastStatus = status
	return nil
}
```

- [ ] **Step 7: Run tests**

Run: `go build ./... && go test ./service/bpmn/... ./service/... ./internal/bootstrap/... -count=1`
Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add itsm-backend/service/bpmn/ticket_handler.go itsm-backend/service/bpmn/ticket_handler_test.go \
  itsm-backend/service/ticket_service.go itsm-backend/service/bpmn_process_engine.go \
  itsm-backend/internal/bootstrap/app.go
git commit -m "fix(work-item): TicketServiceTaskHandler delegates status updates to TicketService instead of writing Ent directly"
```

---

### Task 6: CallbackRegistry — Incident handler stops writing Ent directly

**Files:**
- Modify: `itsm-backend/service/bpmn/incident_handler.go`
- Modify: `itsm-backend/service/incident_service.go` (add `UpdateStatus` method + one adapter)
- Modify: `itsm-backend/internal/bootstrap/app.go`
- Test: `itsm-backend/service/bpmn/incident_handler_test.go` (check if it exists first)

**Interfaces:**
- Consumes: `IncidentService.CreateIncident(ctx, req *dto.CreateIncidentRequest, tenantID, userID) (*dto.IncidentResponse, error)` and `IncidentService.AssignIncident(ctx, id, assigneeID, tenantID) (*dto.IncidentResponse, error)` (both already exist).
- Produces: `IncidentService.UpdateStatus(ctx, id, status, tenantID) (*dto.IncidentResponse, error)` (new); `IncidentServiceTaskHandler.SetIncidentService(svc IncidentDomainServiceInterface)`.

- [ ] **Step 1: Verify the `reporter_id` variable is always a real user before swapping `createIncident`**

`IncidentService.CreateIncident` validates that `userID` is an active user in the tenant — the current raw-Ent `createIncident` handler does not. Run:

```bash
grep -rln "reporter_id" itsm-backend/service/bpmn/*.bpmn itsm-backend/service/bpmn/*_cn.bpmn 2>/dev/null
grep -rn "reporter_id" itsm-backend/service/bpmn/*.go | grep -v _test.go
```

Confirm every BPMN template/Go call site that feeds this ServiceTask's `reporter_id` variable is sourced from a real, already-authenticated user ID (e.g. the ticket's requester, or an incident-creation flow's actual reporter) — not a hardcoded placeholder or 0. If you find a template that doesn't set `reporter_id` at all, check whether `GetIntFromVars(variables, "reporter_id")` defaulting to 0 would now make `CreateIncident` fail (it queries for an active user with `id=0`, which will simply not exist, causing a clear "reporter not found or inactive" error instead of the previous silent `SetReporterID(0)`). If this is a live path with no real reporter available, note it in the commit message as a known behavior change — do not add a fallback fake user to paper over it (AGENTS.md: no success-shaped fallback).

- [ ] **Step 2: Add `IncidentService.UpdateStatus`**

The existing `AssignIncident` doesn't touch status, but the BPMN handler's `assignIncident` currently also sets `status="assigned"` as part of the same write. Add a small, narrowly-scoped status setter (this is not building out Incident's full state machine — that's Wave 2's job — just closing the direct-Ent-write hole for the one transition this handler needs today).

In `itsm-backend/service/incident_service.go`, add after `AssignIncident`:

```go
// UpdateStatus 更新事件状态（租户校验 + 乐观锁版本递增 + 审计事件），不做状态机合法性
// 校验——Incident 完整状态机是 Wave 2 迁移的范围，这里只是把 BPMN handler 现有的一次
// "assigned" 状态写入从裸 Ent 操作收回到领域服务里，不新增业务规则。
func (s *IncidentService) UpdateStatus(ctx context.Context, id int, status string, tenantID int) (*dto.IncidentResponse, error) {
	updated, err := s.client.Incident.UpdateOneID(id).
		Where(incident.TenantIDEQ(tenantID), incident.DeletedAtIsNil()).
		SetStatus(status).
		SetUpdatedAt(time.Now()).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found")
		}
		return nil, fmt.Errorf("failed to update incident status: %w", err)
	}
	s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID:  id,
		EventType:   "status_changed",
		EventName:   "状态变更",
		Description: fmt.Sprintf("事件状态变更为 %s", status),
		Status:      "active",
		Severity:    "info",
		Source:      "system",
	}, tenantID)
	return s.toIncidentResponse(updated), nil
}
```

Check the exact import alias for the `incident` ent package at the top of `incident_service.go` (it may be aliased differently than in `incident_handler.go`) before using `incident.TenantIDEQ`/`incident.DeletedAtIsNil` — match whatever this file already uses elsewhere (e.g. in `AssignIncident` a few lines above, which already uses these exact predicates).

- [ ] **Step 3: Declare the narrow interface in `incident_handler.go`**

```go
// IncidentDomainServiceInterface 事件领域服务接口（避免 service/bpmn 反向 import service
// 包造成循环依赖，同 TicketStatusServiceInterface 的理由）
type IncidentDomainServiceInterface interface {
	CreateIncident(ctx context.Context, req *dto.CreateIncidentRequest, tenantID, userID int) (*dto.IncidentResponse, error)
	AssignIncident(ctx context.Context, id int, assigneeID int, tenantID int) (*dto.IncidentResponse, error)
	UpdateStatus(ctx context.Context, id int, status string, tenantID int) (*dto.IncidentResponse, error)
}
```

`dto.CreateIncidentRequest`/`dto.IncidentResponse` are already imported by this file (`"itsm-backend/dto"`), so this interface doesn't introduce any new import-cycle risk — `dto` is a leaf package.

Add the field and setter:

```go
type IncidentServiceTaskHandler struct {
	HandlerBase
	client          *ent.Client
	logger          *zap.SugaredLogger
	incidentService IncidentDomainServiceInterface
}

// SetIncidentService 注入事件领域服务，由 bootstrap 在 IncidentService 构造完成后调用。
func (h *IncidentServiceTaskHandler) SetIncidentService(svc IncidentDomainServiceInterface) {
	h.incidentService = svc
}
```

- [ ] **Step 4: Replace the direct Ent write in `createIncident`**

Find:

```go
	incident, err := h.client.Incident.Create().
		SetTitle(title).
		SetDescription(description).
		SetType(incidentType).
		SetPriority(priority).
		SetSeverity(severity).
		SetStatus(common.IncidentStatusNew).
		SetReporterID(GetIntFromVars(variables, "reporter_id")).
		SetTenantID(tenantID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建事件失败: %w", err)
	}

	h.logger.Infow("Incident created via BPMN", "incident_id", incident.ID, "title", title)

	return &dto.ServiceTaskResult{
		Success:    true,
		Message:    fmt.Sprintf("事件 %d 已创建", incident.ID),
		OutputVars: map[string]interface{}{"incident_id": incident.ID, "incident_number": incident.IncidentNumber},
	}, nil
```

Replace with:

```go
	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法创建事件")
	}
	reporterID := GetIntFromVars(variables, "reporter_id")
	resp, err := h.incidentService.CreateIncident(ctx, &dto.CreateIncidentRequest{
		Title:       title,
		Description: description,
		Type:        incidentType,
		Priority:    priority,
		Severity:    severity,
	}, tenantID, reporterID)
	if err != nil {
		return nil, fmt.Errorf("创建事件失败: %w", err)
	}

	h.logger.Infow("Incident created via BPMN", "incident_id", resp.ID, "title", title)

	return &dto.ServiceTaskResult{
		Success:    true,
		Message:    fmt.Sprintf("事件 %d 已创建", resp.ID),
		OutputVars: map[string]interface{}{"incident_id": resp.ID, "incident_number": resp.IncidentNumber},
	}, nil
```

Check `dto.IncidentResponse`'s exact field names (`ID`/`IncidentNumber` — confirm capitalization by reading the struct in `dto/incident_dto.go` before assuming) and that `tenantID` in this function is obtained the same way as before (`GetTenantIDFromVars(ctx, variables)`, already at the top of this function per the earlier read — don't remove that line).

- [ ] **Step 5: Replace the direct Ent write in `assignIncident`**

Find:

```go
	updated, err := h.client.Incident.Update().
		Where(incident.ID(incidentID), incident.TenantID(tenantID)).
		SetAssigneeID(assigneeID).
		SetStatus(common.IncidentStatusAssigned).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("分配事件失败: %w", err)
	}
	if updated == 0 {
		return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", incidentID)
	}
```

Replace with:

```go
	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法分配事件")
	}
	if _, err := h.incidentService.AssignIncident(ctx, incidentID, assigneeID, tenantID); err != nil {
		return nil, fmt.Errorf("分配事件失败: %w", err)
	}
	if _, err := h.incidentService.UpdateStatus(ctx, incidentID, common.IncidentStatusAssigned, tenantID); err != nil {
		return nil, fmt.Errorf("更新事件状态失败: %w", err)
	}
```

`common.IncidentStatusAssigned` is a plain untyped string constant (`common/constants.go`, inside a bare `const (...)` block with no named type) — no cast needed, it already satisfies `UpdateStatus`'s `status string` parameter directly.

Remove the `SetStatus(...)`-based `updated == 0` not-found check since `AssignIncident` already does its own not-found check (`incident not found` error) before this code would even run — don't duplicate it.

- [ ] **Step 6: Remove now-unused imports if any**

Check `grep -n "\"itsm-backend/ent/incident\"\|common\.Incident" itsm-backend/service/bpmn/incident_handler.go` — the `incident` ent package import might still be needed elsewhere in this file (e.g. other functions not touched by this task); only remove it if genuinely unused after this change.

- [ ] **Step 7: Wire both setters in bootstrap**

In `itsm-backend/internal/bootstrap/app.go`, next to the `ticketService.SetNotificationService(...)` block, add (after `incidentService` — line 191 — and `processEngine` — line 228 — are both already in scope, which they are by this point in the function):

```go
	if h, ok := processEngine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler); ok {
		h.SetIncidentService(incidentService)
	}
```

- [ ] **Step 8: Write the tests**

Mirror Task 5 Step 6's pattern. `IncidentServiceTaskHandler.Execute` dispatches on `variables["action"]` with values `"create_incident"` and `"assign_incident"` (no `business_id` gate unlike the ticket handler — `incidentID`/`title` etc. are read straight out of `variables` inside each sub-function):

```go
func TestIncidentServiceTaskHandler_CreateIncident_RequiresInjectedService(t *testing.T) {
	handler := NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action": "create_incident",
		"title":  "测试事件",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "未注入")
}

func TestIncidentServiceTaskHandler_CreateIncident_DelegatesToInjectedService(t *testing.T) {
	handler := NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	fake := &fakeIncidentService{createResp: &dto.IncidentResponse{ID: 7, IncidentNumber: "INC-1"}}
	handler.SetIncidentService(fake)

	result, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "create_incident",
		"title":       "测试事件",
		"reporter_id": 3,
	})
	require.NoError(t, err)
	require.Equal(t, "测试事件", fake.lastCreateReq.Title)
	require.Equal(t, 3, fake.lastCreateUserID)
	require.Equal(t, 7, result.OutputVars["incident_id"])
}

func TestIncidentServiceTaskHandler_AssignIncident_DelegatesAndUpdatesStatus(t *testing.T) {
	handler := NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	fake := &fakeIncidentService{}
	handler.SetIncidentService(fake)

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "assign_incident",
		"incident_id": 9,
		"assignee_id": 4,
		"tenant_id":   1,
	})
	require.NoError(t, err)
	require.Equal(t, 9, fake.lastAssignID)
	require.Equal(t, 4, fake.lastAssigneeID)
	require.Equal(t, 9, fake.lastStatusID)
	require.Equal(t, "assigned", fake.lastStatus)
}

type fakeIncidentService struct {
	createResp       *dto.IncidentResponse
	lastCreateReq    *dto.CreateIncidentRequest
	lastCreateUserID int
	lastAssignID     int
	lastAssigneeID   int
	lastStatusID     int
	lastStatus       string
}

func (f *fakeIncidentService) CreateIncident(ctx context.Context, req *dto.CreateIncidentRequest, tenantID, userID int) (*dto.IncidentResponse, error) {
	f.lastCreateReq = req
	f.lastCreateUserID = userID
	if f.createResp != nil {
		return f.createResp, nil
	}
	return &dto.IncidentResponse{ID: 1}, nil
}

func (f *fakeIncidentService) AssignIncident(ctx context.Context, id int, assigneeID int, tenantID int) (*dto.IncidentResponse, error) {
	f.lastAssignID = id
	f.lastAssigneeID = assigneeID
	return &dto.IncidentResponse{ID: id}, nil
}

func (f *fakeIncidentService) UpdateStatus(ctx context.Context, id int, status string, tenantID int) (*dto.IncidentResponse, error) {
	f.lastStatusID = id
	f.lastStatus = status
	return &dto.IncidentResponse{ID: id}, nil
}
```

Confirm `dto.ServiceTaskResult.OutputVars` is a `map[string]interface{}` (already used that way in the pre-existing `createIncident` code this task replaces) before relying on `result.OutputVars["incident_id"]` in the assertion — it is, per the code read earlier in this task.

- [ ] **Step 9: Run tests**

Run: `go build ./... && go test ./service/bpmn/... ./service/... ./internal/bootstrap/... -count=1`
Expected: all PASS.

- [ ] **Step 10: Commit**

```bash
git add itsm-backend/service/bpmn/incident_handler.go itsm-backend/service/bpmn/incident_handler_test.go \
  itsm-backend/service/incident_service.go itsm-backend/internal/bootstrap/app.go
git commit -m "fix(work-item): IncidentServiceTaskHandler delegates create/assign/status to IncidentService instead of writing Ent directly"
```

---

### Task 7: Frontend WorkItemShell + locked Props/Context contract

**Files:**
- Create: `itsm-frontend/src/components/work-item/WorkItemTypes.ts`
- Create: `itsm-frontend/src/components/work-item/WorkItemContext.tsx`
- Create: `itsm-frontend/src/components/work-item/WorkItemShell.tsx`
- Create: `itsm-frontend/src/components/work-item/WorkItemComments.tsx`
- Create: `itsm-frontend/src/components/work-item/WorkItemAttachments.tsx`
- Test: `itsm-frontend/src/components/work-item/__tests__/WorkItemShell.test.tsx`

**Interfaces:**
- Consumes: nothing backend-specific — this task has no dependency on Tasks 1-6 and can run in parallel with them if you're splitting Wave 1 across two subagents.
- Produces: `WorkItemShellProps` (exported type), `WorkItemShell` (component), `useWorkItemContext()` (hook, throws outside provider — matches this repo's existing `useWorkflowDesigner()` convention in `src/components/workflow/designer/WorkflowContext.tsx`), `<WorkItemComments />`, `<WorkItemAttachments />`. Every Wave 2 frontend task package imports these verbatim.

- [ ] **Step 1: Write the shared types**

Create `itsm-frontend/src/components/work-item/WorkItemTypes.ts`:

```typescript
// WorkItem 共享前端类型契约。Wave 2 的四个域迁移任务包直接消费这个文件里的类型，不允许
// 各自重新定义形状——见 docs/superpowers/specs/2026-08-26-unified-work-item-multi-agent-execution-plan.md §4.4。

export interface WorkItemActionState {
  allowed: boolean;
  reason?: string;
}

export interface WorkItemCommon {
  id: number;
  number: string;
  recordClass: 'generic' | 'service_request_item' | 'incident' | 'problem' | 'change_request' | 'catalog_task';
  title: string;
  status: string;
  priority: string;
  requesterId: number;
  assigneeId?: number;
  categoryId?: number;
  createdAt: string;
  updatedAt: string;
}

export interface WorkItemSLAState {
  remainingSeconds: number | null;
  isBreached: boolean;
  responseDeadline: string | null;
  resolutionDeadline: string | null;
}

export type WorkItemActionType = 'approve' | 'reject' | 'resolve' | 'close' | 'assign' | string;

export interface WorkItemActionDispatch {
  (action: WorkItemActionType, payload?: Record<string, unknown>): Promise<void>;
}

export interface WorkItemShellProps {
  workItem: WorkItemCommon;
  actions: Record<string, WorkItemActionState>;
  sla?: WorkItemSLAState;
  onActionDispatch: WorkItemActionDispatch;
  /** 专业 Panel（Incident/Problem/Change/ServiceRequestItem 各自实现）挂载点 */
  professionalPanelSlot: React.ReactNode;
  loading?: boolean;
  error?: string | null;
}
```

- [ ] **Step 2: Write the context**

Create `itsm-frontend/src/components/work-item/WorkItemContext.tsx`, following this repo's existing pattern in `src/components/workflow/designer/WorkflowContext.tsx`:

```tsx
'use client';

import type { ReactNode } from 'react';
import React, { createContext, useContext } from 'react';
import type { WorkItemCommon, WorkItemSLAState, WorkItemActionDispatch } from './WorkItemTypes';

interface WorkItemContextValue {
  workItem: WorkItemCommon;
  sla?: WorkItemSLAState;
  onActionDispatch: WorkItemActionDispatch;
}

const WorkItemContext = createContext<WorkItemContextValue | null>(null);

export function WorkItemProvider({
  value,
  children,
}: {
  value: WorkItemContextValue;
  children: ReactNode;
}) {
  return <WorkItemContext.Provider value={value}>{children}</WorkItemContext.Provider>;
}

export function useWorkItemContext(): WorkItemContextValue {
  const ctx = useContext(WorkItemContext);
  if (!ctx) {
    throw new Error('useWorkItemContext must be used within a WorkItemProvider (i.e. inside WorkItemShell)');
  }
  return ctx;
}
```

- [ ] **Step 3: Write the Shell**

Create `itsm-frontend/src/components/work-item/WorkItemShell.tsx`:

```tsx
'use client';

import React from 'react';
import { Card, Space, Tag, Descriptions } from 'antd';
import { WorkItemProvider } from './WorkItemContext';
import type { WorkItemShellProps } from './WorkItemTypes';
import { WorkItemComments } from './WorkItemComments';
import { WorkItemAttachments } from './WorkItemAttachments';

// WorkItemShell 提供所有 recordClass 共用的公共区块骨架（编号/标题/状态/优先级/请求人/
// 分派/SLA/评论/附件），专业字段由调用方通过 professionalPanelSlot 传入。本组件本身
// 不实现任何专业 Panel——那是 Wave 2 各域任务包的范围。
//
// 不做的事：不在这里拼装任何具体域的 API 调用。所有动作都通过 onActionDispatch 回调
// 交给调用方处理，专业 Panel 也应该复用同一个回调，不要在 Panel 内部单独发 HTTP 请求。
export function WorkItemShell({
  workItem,
  actions,
  sla,
  onActionDispatch,
  professionalPanelSlot,
  loading,
  error,
}: WorkItemShellProps) {
  if (loading) {
    return <Card loading />;
  }
  if (error) {
    return <Card><Tag color="red">加载失败：{error}</Tag></Card>;
  }

  return (
    <WorkItemProvider value={{ workItem, sla, onActionDispatch }}>
      <Space direction="vertical" style={{ width: '100%' }} size="large">
        <Card>
          <Descriptions column={3} title={`${workItem.number} · ${workItem.title}`}>
            <Descriptions.Item label="状态">{workItem.status}</Descriptions.Item>
            <Descriptions.Item label="优先级">{workItem.priority}</Descriptions.Item>
            <Descriptions.Item label="处理人">{workItem.assigneeId ?? '未分配'}</Descriptions.Item>
          </Descriptions>
        </Card>
        <Card>{professionalPanelSlot}</Card>
        <WorkItemComments workItemId={workItem.id} />
        <WorkItemAttachments workItemId={workItem.id} />
      </Space>
    </WorkItemProvider>
  );
}
```

Do not wire real SLA countdown math or real comment/attachment API calls in this task — `WorkItemComments`/`WorkItemAttachments` below are deliberately placeholder-shaped components with a defined prop contract (`workItemId`) so Wave 2 can render *something* through the Shell today; wiring them to the actual `/api/v1/tickets/:id/comments` etc. endpoints is Wave 2 scope (each domain already has working comment/attachment UI today — Wave 2 replaces those call sites to render through these shared components without duplicating the fetch logic per domain, but that replacement is out of scope for Wave 1, which only needs the mount points to exist with a stable prop shape).

- [ ] **Step 4: Write the two mount-point placeholder components**

Create `itsm-frontend/src/components/work-item/WorkItemComments.tsx`:

```tsx
'use client';

import React from 'react';
import { Card } from 'antd';

// 挂载点占位实现——Wave 2 迁移各域评论 UI 时改造这个组件去调用真实的评论 API，
// 调用方（WorkItemShell 及所有消费它的专业 Panel）只依赖 workItemId 这一个 prop，
// 不会因为内部实现从占位换成真实请求而改动。
export function WorkItemComments({ workItemId }: { workItemId: number }) {
  return <Card size="small" title="评论" data-work-item-id={workItemId} />;
}
```

Create `itsm-frontend/src/components/work-item/WorkItemAttachments.tsx`:

```tsx
'use client';

import React from 'react';
import { Card } from 'antd';

export function WorkItemAttachments({ workItemId }: { workItemId: number }) {
  return <Card size="small" title="附件" data-work-item-id={workItemId} />;
}
```

- [ ] **Step 5: Write the consumption test**

Create `itsm-frontend/src/components/work-item/__tests__/WorkItemShell.test.tsx`:

```tsx
import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemShell } from '../WorkItemShell';
import { useWorkItemContext } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

const workItem: WorkItemCommon = {
  id: 1,
  number: 'INC-202608-000001',
  recordClass: 'incident',
  title: '测试事件',
  status: 'in_progress',
  priority: 'high',
  requesterId: 10,
  createdAt: '2026-08-26T00:00:00Z',
  updatedAt: '2026-08-26T00:00:00Z',
};

function ProbePanel() {
  const { workItem: fromContext } = useWorkItemContext();
  return <div data-testid="probe">{fromContext.title}</div>;
}

describe('WorkItemShell', () => {
  it('renders the common fields and exposes them via useWorkItemContext to the professional panel slot', () => {
    render(
      <WorkItemShell
        workItem={workItem}
        actions={{ resolve: { allowed: true } }}
        onActionDispatch={jest.fn()}
        professionalPanelSlot={<ProbePanel />}
      />
    );
    expect(screen.getByText(/INC-202608-000001/)).toBeInTheDocument();
    expect(screen.getByTestId('probe')).toHaveTextContent('测试事件');
  });

  it('shows an error state without rendering the panel slot', () => {
    render(
      <WorkItemShell
        workItem={workItem}
        actions={{}}
        onActionDispatch={jest.fn()}
        professionalPanelSlot={<ProbePanel />}
        error="加载失败"
      />
    );
    expect(screen.queryByTestId('probe')).not.toBeInTheDocument();
  });
});
```

Check this repo's existing Jest setup for whether `@testing-library/react` is already a dependency and whether component tests elsewhere use `describe`/`it` or `test` — match the prevailing style (search one existing `__tests__/*.test.tsx` file under `src/components/` first).

- [ ] **Step 6: Run the test and type-check**

Run: `cd itsm-frontend && npx jest src/components/work-item --verbose`
Expected: both tests PASS.

Run: `npm run type-check`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add itsm-frontend/src/components/work-item/
git commit -m "feat(work-item): add shared WorkItemShell, context, and Props contract for Wave 2 frontend tasks"
```

---

### Task 8: Wave 1 acceptance verification

**Files:** none (verification only).

**Interfaces:**
- Consumes: everything from Tasks 1-7.
- Produces: nothing — this is the gate before Wave 1 merges and Wave 2 task packages get dispatched.

- [ ] **Step 1: Full backend suite**

Run: `cd itsm-backend && go build ./... && go test ./... -count=1`
Expected: all PASS, zero FAIL lines.

- [ ] **Step 2: Full frontend type-check and unit tests**

Run: `cd itsm-frontend && npm run type-check && npx jest --silent`
Expected: no type errors; all existing + new tests PASS.

- [ ] **Step 3: Re-run the integrity check command against the dev database**

Run: `cd itsm-backend && go run ./cmd/check_work_item_integrity -tenant-id=0`
Expected: no mismatches on a database that hasn't had Wave 2 migrations run yet (every `tickets` row should still be `record_class='generic'` at this point, since nothing has started writing `work_item_id` yet — this run is really just confirming the command itself works end-to-end against real data, not that data is consistent yet).

- [ ] **Step 4: Confirm the §4.1 schema table, item by item**

Go through every row of the spec's §4.1 table and Task 1-7's "Produces" sections; for each, run the matching `grep`/`go doc` command to confirm the field/method actually exists with the exact name used above (e.g. `go doc ./ent/incident Incident.WorkItemID`, `go doc ./service TicketService.UpdateTicketStatusForWorkflow`). Do not rely on "the earlier steps said so" — this is the same "re-verify before declaring done" discipline this whole reconstruction project has already needed once today (the P0 approval spec that turned out to already be implemented upstream).

- [ ] **Step 5: Write the completion note**

Update `docs/superpowers/specs/2026-08-26-unified-work-item-multi-agent-execution-plan.md` §4 with a short "Wave 1 completed: <date>, commit <hash>" line, listing which of the "明确不做" items (if any) turned out to need revisiting during implementation — this is the reference Wave 2's four task packages will read before they start.

- [ ] **Step 6: Do not merge to main yet**

Per the execution spec §1, Wave 1 lands on `refactor/unified-work-item`, not `main` — Wave 2 task packages branch from there. Push the branch and stop; merging to `main` happens only after Wave 3's final review.
