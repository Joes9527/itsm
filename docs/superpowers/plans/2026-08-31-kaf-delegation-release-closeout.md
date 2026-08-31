# KAF Delegation Release Closeout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the merged ITSM/KAF delegation capabilities into a reproducible Dev baseline proven by persistent read auditing, a Graph-only SSLVPN procedure, one real cross-process Azure AD grant, replay-only recovery, deterministic breakers, PostgreSQL RLS, and verified cleanup.

**Architecture:** ITSM remains authoritative for Service Request, BPMN, tenant scope, task actions, ledger, receipt, timeline, and audit; KAF remains authoritative for Procedure retrieval, typed Tool invocation, delivery leases, and completion replay. The one real path uses the official Service Request API, the existing process trigger and variables API, the authoritative `vpn_permission_grant` Procedure, and the registered Graph tools; all duplicate/recovery checks after the grant reuse the persisted completion payload and must not invoke Graph again.

**Tech Stack:** Go 1.25.12, Gin, Ent, PostgreSQL/SQLite tests, BPMN XML, Python 3.12+, pytest, Pydantic, Qdrant, SQLAlchemy/PostgreSQL, FastAPI/Uvicorn, Microsoft Graph, Bash/curl/jq.

**Spec:** `docs/superpowers/specs/2026-08-31-kaf-delegation-release-closeout-design.md`

## Global Constraints

- ITSM source worktree is `/home/administrator/project/itsm`; KAF source worktree is `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery`.
- Never read, write, probe, or use credentials for KAF PROD `10.128.35.195`; the accepted KAF Dev API for this closeout is the current-source process bound to `127.0.0.1:8001`.
- The sole real identity fixture is `Julian@dawnpro.onmicrosoft.com`; the sole target group Object ID is `b7c7f066-3042-4a11-9e36-2ea80b979ae3`.
- Azure AD/Microsoft Graph is the only SSLVPN backend in this scenario; LDAP must not appear in the authoritative Procedure or live execution path.
- The target group comes only from `VPN_USERS_GROUP_ID`; no Procedure, application code, or committed validation script may hardcode the group, tenant, credentials, or Julian fixture.
- Use the existing `vpn_permission_grant` Procedure and existing APIs/tools; do not create an E2E-only Procedure, test-only endpoint, direct database mutation, second approval engine, Intake service, or VPN-specific headless branch.
- The Service Request API starts the bound BPMN. Inject `intake_snapshot` through `PUT /api/v1/bpmn/process-instances/:id/variables` before completing the first approval; do not claim that the creation DTO accepts it.
- `PROCESS_INSTANCE_ID` means the numeric Ent row ID used by task-list APIs; `PROCESS_INSTANCE_KEY` means the string `processInstanceId` used by the variables endpoint. Never interchange them.
- The real Graph scenario is serial. Install cleanup before the first mutation; after any later failure, resolve the exact user and group again, remove membership if present, and verify `member=false`.
- Once a completion payload is persisted, every retry/recovery/replay must send that exact payload and must not execute the Procedure or Graph Tool again.
- Database access is read-only evidence except the repository-owned application paths and the dedicated `integration_rls` test suite.
- Persist only sanitized audit/report evidence. Never print or commit Azure secrets, JWTs, webhook secrets, access tokens, attachment URLs/paths, raw Tool output, or unredacted intake content.
- The KAF repository-wide suite may retain documented unrelated baseline failures, but focused modified-scope tests must pass and the complete pytest exit code/counts must be reported exactly.
- `docs/implementation/` in the ITSM worktree is pre-existing user content; never stage or modify it.

## File Map

### ITSM files

- Modify `itsm-backend/service/kaf_delegation_service.go`: split no-audit context assembly from public reads and persist one sanitized audit per successful context/list request.
- Modify `itsm-backend/controller/kaf_delegation_controller_test.go`: prove context audit, aggregate list audit, fail-closed persistence, and sensitive-field exclusion at the HTTP boundary.
- Modify `itsm-backend/service/bpmn/sslvpn_approval_flow.bpmn`: add the authoritative `kaf_delegate` service task after the two existing approvals.
- Modify `itsm-backend/tests/fixtures/sslvpn_fixtures_test.go`: parse the shipped BPMN and lock its KAF metadata and route.
- Reuse `itsm-backend/handlers/service_request/kaf_delegation_sslvpn_e2e_test.go`: run its existing in-process transactional/replay coverage; do not duplicate it.
- Modify `docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md`: append the sanitized Live Dev Closeout Addendum only after evidence collection.
- Reuse `docs/testing/kaf-delegation-release-closeout-fixture.md`: operator fixture and recovery contract; no secret values.

### KAF files

- Modify `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py`: project only `intake_snapshot.collected_fields` into assembler state and reject a non-object value.
- Modify `tests/test_kaf_delegation_pipeline.py`: lock nested projection and replay-only semantics.
- Modify `src/acp/workflows/assembler.py`: bind `ad_grant_vpn_access(user_identifier: str)` from collected fields.
- Modify `tests/test_hitl.py`: prove the exact Graph Tool kwargs and absence of group/default parameters.
- Modify `src/acp/tools/metadata.py`: register non-agent-visible, high-risk, audited Graph grant metadata without adding a second KAF approval gate.
- Modify `tests/test_tool_registry.py`: lock the Graph grant metadata contract.
- Modify `scripts/procedures/vpn_permission_grant.md`: make the sole authoritative Procedure Graph-only with `user_identifier` as its only collected execution field.
- Modify `tests/test_vpn_permission_grant_procedure_doc.py`: reject LDAP, tenant/group hardcoding, unused fields, and extra gates.
- Modify `tests/test_sr_detect_intent.py`: update the Procedure fixture to the authoritative Graph form.
- Modify `tests/test_sr_execution_worker.py`: update successful execution expectations to `ad_grant_vpn_access` and one typed argument.
- Reuse `src/acp/mcp/tools/vpn.py`, `tests/test_vpn_grant_tool.py`, and `tests/integration/mcp/test_vpn.py`: verify existing Graph add/remove and idempotency; modify only if a red regression test exposes a product defect.

---

### Task 1: Persist Sanitized ITSM KAF Read Audits

**Files:**

- Modify: `itsm-backend/service/kaf_delegation_service.go`
- Test: `itsm-backend/controller/kaf_delegation_controller_test.go`

**Interfaces:**

- Consumes: `(*KafDelegationService).taskForTenant(context.Context, string)`, `AuthorizeTask(context.Context, *ent.ProcessTask)`, Ent `AuditLog`.
- Produces: `buildKafTaskContext(context.Context, *ent.ProcessTask) (*KafTaskContext, error)` and `recordKafReadAudit(context.Context, int, string, string, map[string]interface{}) error`.
- Public behavior: `GetTaskContext` writes action `kaf_delegate.context_read`; `ListDelegatedTaskPage` writes action `kaf_delegate.list`; either audit write failure prevents a successful response.

- [ ] **Step 1: Add failing HTTP tests for one sanitized context audit**

Add a test that attaches a WorkItem and sensitive attachment, performs one successful GET, and inspects the single audit row:

```go
func TestKafContext_RecordsOneSanitizedReadAudit(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{
		actorTenantID: 1, taskTenantID: 1,
		taskType: bpmn.KafDelegateTaskType, status: common.ProcessTaskStatusDelegated,
	})
	attachKafWorkItem(t, client, taskID)
	task, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID)).Only(context.Background())
	require.NoError(t, err)
	_, err = client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).
		SetVariables(map[string]interface{}{
			"intake_snapshot": map[string]interface{}{
				"operationKind": "vpn_permission_grant",
				"collected_fields": map[string]interface{}{
					"user_identifier": "sensitive-user@example.test",
				},
			},
		}).Save(context.Background())
	require.NoError(t, err)

	response := doKafRequest(t, router, http.MethodGet,
		"/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", "")
	require.Equal(t, http.StatusOK, response.Code)

	audits, err := client.AuditLog.Query().
		Where(auditlog.ActionEQ("kaf_delegate.context_read")).All(context.Background())
	require.NoError(t, err)
	require.Len(t, audits, 1)
	assert.Equal(t, "process_task", audits[0].Resource)
	assert.Equal(t, "GET", audits[0].Method)
	assert.Equal(t, http.StatusOK, audits[0].StatusCode)
	assert.Contains(t, audits[0].RequestBody, taskID)
	assert.Contains(t, audits[0].RequestBody, "corr-kaf-http")
	for _, forbidden := range []string{
		"KAF delegated VPN access", "sensitive-user@example.test",
		"intake_snapshot", "collected_fields", "token", "secret",
	} {
		assert.NotContains(t, audits[0].RequestBody, forbidden)
	}
}
```

- [ ] **Step 2: Add failing tests for one aggregate list audit and zero per-item context audits**

```go
func TestKafDelegatedList_RecordsOneAggregateAudit(t *testing.T) {
	router, _, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{
		actorTenantID: 1, taskTenantID: 1,
		taskType: bpmn.KafDelegateTaskType, status: common.ProcessTaskStatusDelegated,
	})
	response := doKafRequest(t, router, http.MethodGet,
		"/api/v1/bpmn/process-tasks/kaf-delegated?status=delegated", "")
	require.Equal(t, http.StatusOK, response.Code)

	listCount, err := client.AuditLog.Query().Where(auditlog.ActionEQ("kaf_delegate.list")).Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, listCount)
	contextCount, err := client.AuditLog.Query().Where(auditlog.ActionEQ("kaf_delegate.context_read")).Count(context.Background())
	require.NoError(t, err)
	assert.Zero(t, contextCount)
	audit := client.AuditLog.Query().Where(auditlog.ActionEQ("kaf_delegate.list")).OnlyX(context.Background())
	assert.JSONEq(t, `{"resultCount":1}`, audit.RequestBody)
}
```

- [ ] **Step 3: Add a failing audit-write test that proves context is not returned**

Close the Ent client after fixture construction so the context assembly reaches the persistence boundary but the audit insert cannot succeed; assert a 5xx and no serialized `KafTaskContext`:

```go
func TestKafContext_AuditPersistenceFailureDoesNotReturnContext(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{
		actorTenantID: 1, taskTenantID: 1,
		taskType: bpmn.KafDelegateTaskType, status: common.ProcessTaskStatusDelegated,
	})
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if _, ok := mutation.(*ent.AuditLogMutation); ok {
				return nil, errors.New("forced audit persistence failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})
	response := doKafRequest(t, router, http.MethodGet,
		"/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", "")
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.NotContains(t, response.Body.String(), "corr-kaf-http")
}
```

The Ent mutation hook is installed only after fixture creation, so context reads still succeed and the test deterministically fails only the persistent audit insert. Do not add a production injection seam, endpoint, or silent fallback for this test.

- [ ] **Step 4: Run the new tests and confirm the missing-audit failures**

Run:

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./controller -run 'TestKaf(Context_RecordsOneSanitizedReadAudit|Context_AuditPersistenceFailureDoesNotReturnContext|DelegatedList_RecordsOneAggregateAudit)' -count=1 -v
```

Expected: FAIL because no `kaf_delegate.context_read`/`kaf_delegate.list` rows exist; the persistence-failure test must not pass accidentally.

- [ ] **Step 5: Split context assembly and add fail-closed audit persistence**

Refactor only the already-authorized assembly body into this private method:

```go
func (s *KafDelegationService) buildKafTaskContext(ctx context.Context, task *ent.ProcessTask) (*KafTaskContext, error) {
	instance, err := s.client.ProcessInstance.Query().
		Where(processinstance.IDEQ(task.ProcessInstanceID), processinstance.TenantIDEQ(task.TenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load KAF process instance: %w", err)
	}
	recordClass, err := kafDelegationRecordClass(ctx, s.client, instance)
	if err != nil {
		return nil, err
	}
	workItem := KafWorkItem{ID: instance.BusinessID, RecordClass: recordClass}
	attachments := []KafAttachmentRef{}
	if instance.BusinessID > 0 {
		item, itemErr := s.client.Ticket.Query().
			Where(ticket.IDEQ(instance.BusinessID), ticket.TenantIDEQ(task.TenantID), ticket.DeletedAtIsNil()).
			Only(ctx)
		if itemErr == nil {
			workItem.Title = item.Title
			workItem.Priority = item.Priority
			workItem.Status = item.Status
			attachmentIDs, attachmentErr := s.client.TicketAttachment.Query().
				Where(ticketattachment.TicketIDEQ(item.ID), ticketattachment.TenantIDEQ(task.TenantID)).
				Order(ent.Asc(ticketattachment.FieldID)).
				IDs(ctx)
			if attachmentErr != nil {
				return nil, fmt.Errorf("load KAF WorkItem attachment references: %w", attachmentErr)
			}
			for _, attachmentID := range attachmentIDs {
				attachments = append(attachments, KafAttachmentRef{ID: attachmentID})
			}
		}
	}
	return &KafTaskContext{
		TaskID: task.TaskID, TaskType: task.TaskType, Status: task.Status,
		CorrelationID: task.CorrelationID, TenantID: strconv.Itoa(task.TenantID), RecordClass: recordClass,
		AllowedActions: kafAllowedActions(task), ExpectedVersion: instance.Version,
		WaitingPoint: KafWaitingPoint{
			ProcessInstanceID: instance.ProcessInstanceID,
			ProcessDefinition: instance.ProcessDefinitionKey,
			ActivityID: instance.CurrentActivityID,
			ActivityName: instance.CurrentActivityName,
		},
		IntakeSnapshot: frozenKafIntakeSnapshot(instance.Variables),
		WorkItem: workItem,
		Attachments: attachments,
	}, nil
}
```

Make the public paths explicit:

```go
func (s *KafDelegationService) GetTaskContext(ctx context.Context, taskID string) (*KafTaskContext, error) {
	task, err := s.taskForTenant(ctx, taskID)
	if err != nil { return nil, err }
	if err := requireKafDelegatedTaskType(task); err != nil { return nil, err }
	if err := s.AuthorizeTask(ctx, task); err != nil { return nil, err }
	result, err := s.buildKafTaskContext(ctx, task)
	if err != nil { return nil, err }
	if err := s.recordKafReadAudit(ctx, task.TenantID, "kaf_delegate.context_read",
		"bpmn/process-tasks/kaf-context", map[string]interface{}{
			"taskId": task.TaskID, "correlationId": task.CorrelationID,
		}); err != nil {
		return nil, fmt.Errorf("record KAF context read audit: %w", err)
	}
	return result, nil
}
```

In the list loop, call `AuthorizeTask` then `buildKafTaskContext` directly. After `page.Items` is assigned, write exactly one aggregate audit:

```go
if err := s.recordKafReadAudit(ctx, tenantID, "kaf_delegate.list",
	"bpmn/process-tasks/kaf-delegated", map[string]interface{}{
		"resultCount": len(page.Items),
	}); err != nil {
	return nil, fmt.Errorf("record KAF delegated list audit: %w", err)
}
```

The helper must marshal only its supplied map and derive actor from the authenticated context:

```go
func (s *KafDelegationService) recordKafReadAudit(
	ctx context.Context, tenantID int, action, path string, body map[string]interface{},
) error {
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	encoded, err := json.Marshal(body)
	if err != nil { return err }
	create := s.client.AuditLog.Create().
		SetTenantID(tenantID).SetResource("process_task").SetAction(action).
		SetPath(path).SetMethod(http.MethodGet).SetStatusCode(http.StatusOK).
		SetRequestBody(string(encoded))
	if actorID > 0 { create.SetUserID(actorID) }
	return create.Exec(ctx)
}
```

- [ ] **Step 6: Run focused and existing authorization/attachment tests**

Run:

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./controller -run 'TestKaf(Context|DelegatedList)' -count=1 -v
go test ./service -run 'TestKaf' -count=1 -v
```

Expected: PASS; successful list requests have one list audit and zero context-read audits, while rejected requests remain fail closed.

- [ ] **Step 7: Commit the ITSM read-audit slice**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/service/kaf_delegation_service.go itsm-backend/controller/kaf_delegation_controller_test.go
git commit -m "feat(kaf): audit delegated task reads"
```

### Task 2: Put the KAF Delegation Wait State in the Shipped SSLVPN BPMN

**Files:**

- Modify: `itsm-backend/service/bpmn/sslvpn_approval_flow.bpmn`
- Test: `itsm-backend/tests/fixtures/sslvpn_fixtures_test.go`

**Interfaces:**

- Consumes: `service.NewBPMNParser()`, `(*BPMNParser).ParseXML([]byte)`, `(*BPMNServiceTask).ServiceTaskType()`, `(*BPMNServiceTask).AllowedActions()`.
- Produces: BPMN service task `ServiceTask_KafDelegate` with `service_task_type=kaf_delegate` and `allowed_actions=complete_bpmn_task`, located after L2 approval and before the end event.

- [ ] **Step 1: Add a failing structural fixture test**

Read the shipped BPMN, parse it, locate the exact task, and assert its only outgoing route reaches the end:

```go
func TestSSLVPNApprovalFlow_DelegatesToKafAfterL2Approval(t *testing.T) {
	xmlBytes, err := os.ReadFile("../../service/bpmn/sslvpn_approval_flow.bpmn")
	require.NoError(t, err)
	definitions, err := service.NewBPMNParser().ParseXML(xmlBytes)
	require.NoError(t, err)
	require.Len(t, definitions.Processes, 1)

	var delegated *service.BPMNServiceTask
	for index := range definitions.Processes[0].ServiceTasks {
		task := definitions.Processes[0].ServiceTasks[index]
		if task.ID == "ServiceTask_KafDelegate" { delegated = task; break }
	}
	require.NotNil(t, delegated)
	assert.Equal(t, bpmn.KafDelegateTaskType, delegated.ServiceTaskType())
	assert.Equal(t, "complete_bpmn_task", delegated.AllowedActions())
	var outgoing *service.BPMNSequenceFlow
	for _, flow := range definitions.Processes[0].SequenceFlows {
		if flow.SourceRef == delegated.ID { outgoing = flow; break }
	}
	require.NotNil(t, outgoing)
	assert.Equal(t, "Flow_Kaf_To_End", outgoing.ID)
	assert.Equal(t, "EndEvent_1", outgoing.TargetRef)
}
```

Add the necessary `os`, `service`, and `service/bpmn` imports using the package’s existing import style.

- [ ] **Step 2: Run the structural test and verify it fails**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./tests/fixtures -run TestSSLVPNApprovalFlow_DelegatesToKafAfterL2Approval -count=1 -v
```

Expected: FAIL because `ServiceTask_KafDelegate` is absent.

- [ ] **Step 3: Add the service task, metadata, sequence flows, and diagram shape**

Replace the current L2-to-end flow with:

```xml
<bpmn:serviceTask id="ServiceTask_KafDelegate" name="KAF 执行 VPN 授权">
  <bpmn:extensionElements>
    <bpmn:metaData name="service_task_type">kaf_delegate</bpmn:metaData>
    <bpmn:metaData name="allowed_actions">complete_bpmn_task</bpmn:metaData>
  </bpmn:extensionElements>
</bpmn:serviceTask>
<bpmn:sequenceFlow id="Flow_L2_To_Kaf" sourceRef="UserTask_L2NetworkOpsApproval" targetRef="ServiceTask_KafDelegate" />
<bpmn:sequenceFlow id="Flow_Kaf_To_End" sourceRef="ServiceTask_KafDelegate" targetRef="EndEvent_1" />
```

The local parser derives routing from `sequenceFlow`, so do not add unsupported `incoming`/`outgoing` fields to `BPMNServiceTask`. Replace `Flow_To_End`, move `EndEvent_1` to the right, and add a KAF `BPMNShape` plus both new `BPMNEdge` elements. Preserve the existing approval task IDs, candidate groups, and metadata.

- [ ] **Step 4: Run fixture and in-process SSLVPN delegation tests**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./tests/fixtures -run 'TestSSLVPN' -count=1 -v
go test ./handlers/service_request -run 'TestServiceRequestKafDelegationSSLVPN' -count=1 -v
```

Expected: PASS; the shipped fixture contains KAF metadata, and existing transactional/outbox/ledger/receipt/replay coverage remains green.

- [ ] **Step 5: Commit the shipped BPMN slice**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/service/bpmn/sslvpn_approval_flow.bpmn itsm-backend/tests/fixtures/sslvpn_fixtures_test.go
git commit -m "feat(bpmn): delegate sslvpn fulfillment to kaf"
```

### Task 3: Project the Frozen Intake into the KAF Assembler Without a VPN Special Case

**Files:**

- Modify: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py`
- Test: `tests/test_kaf_delegation_pipeline.py`

**Interfaces:**

- Consumes: ITSM context JSON `intakeSnapshot: dict[str, object]` with `operationKind: str` and `collected_fields: dict[str, object]`.
- Produces: assembler initial state `collected_fields: dict[str, object]`; raises `ValueError("kaf_procedure_collected_fields_invalid")` before Procedure lookup/execution when the nested value is not a mapping.
- Preserves: existing `ProcedureRunner = Callable[[KafExecutionContext], Awaitable[None]]` and persisted completion-payload replay semantics.

- [ ] **Step 1: Change the default-runner success test to require nested collected fields**

In `test_default_runner_resolves_workspace_scopes_execution_and_completes_itsm`, make the fake graph reject the old whole-snapshot projection:

```python
class Graph:
    async def ainvoke(self, state, config):
        assert_execution_scope()
        assert state["collected_fields"] == {
            "user_identifier": "user@company.example"
        }
        assert "operationKind" not in state["collected_fields"]
        return {"status": "succeeded", "response": "VPN granted"}

client.context_response.update(
    {
        "tenantId": "7",
        "workItem": {"id": 42, "title": "Grant VPN"},
        "intakeSnapshot": {
            "operationKind": "vpn_permission_grant",
            "collected_fields": {
                "user_identifier": "user@company.example",
            },
        },
    }
)
```

- [ ] **Step 2: Add a failing non-object collected-fields test**

Reuse `_seed_workspace`, `FakeItsmClient`, and `InMemoryDeliverySession`; make Procedure lookup fail the test if called:

```python
async def test_default_runner_rejects_non_mapping_collected_fields(
    monkeypatch, sqlite_workspace_sessions
):
    from acp.orchestration.headless_tasks import kaf_delegation_pipeline as pipeline_module
    from acp.procedures import rag

    await _seed_workspace(sqlite_workspace_sessions)
    monkeypatch.setattr(pipeline_module, "async_session_factory", sqlite_workspace_sessions)

    async def unexpected_lookup(intent: str, workspace_id: str):
        pytest.fail("Procedure lookup must not run with invalid collected_fields")

    monkeypatch.setattr(rag, "get_by_intent", unexpected_lookup)
    client = FakeItsmClient()
    client.context_response.update(
        {
            "tenantId": "7",
            "workItem": {"id": 42, "title": "Grant VPN"},
            "intakeSnapshot": {
                "operationKind": "vpn_permission_grant",
                "collected_fields": ["not", "an", "object"],
            },
        }
    )
    session = InMemoryDeliverySession()
    pipeline = pipeline_module.KafDelegationPipeline(
        session=session, itsm_client=client, inline_execution=True
    )

    await pipeline.accept(delegate_event(event_id=str(uuid4()), task_id="TASK-100", tenant_id="7"))

    delivery = session.deliveries[next(iter(session.deliveries))]
    assert delivery.status == "retryable"
    assert "kaf_procedure_collected_fields_invalid" in delivery.last_error
    assert client.action_calls == []
```

- [ ] **Step 3: Run both tests and verify the old projection fails**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest \
  tests/test_kaf_delegation_pipeline.py::test_default_runner_resolves_workspace_scopes_execution_and_completes_itsm \
  tests/test_kaf_delegation_pipeline.py::test_default_runner_rejects_non_mapping_collected_fields -q
```

Expected: FAIL because the graph currently receives the entire `intakeSnapshot` and invalid nested data is not rejected.

- [ ] **Step 4: Validate and project the nested object generically**

Immediately after validating `operationKind`, add:

```python
collected_fields = intake.get("collected_fields")
if not isinstance(collected_fields, dict):
    raise ValueError("kaf_procedure_collected_fields_invalid")
```

Then change only the assembler state assignment:

```python
"collected_fields": dict(collected_fields),
```

Do not inspect `operationKind` for VPN-specific behavior and do not supply a default user or group.

- [ ] **Step 5: Run the focused pipeline and replay suites**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest \
  tests/test_kaf_delegation_pipeline.py -q
```

Expected: PASS, including `test_sqlite_pending_completion_replay_never_reexecutes_listed_procedure` with `procedure_calls == 1`.

- [ ] **Step 6: Commit the nested projection slice**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
git add src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_pipeline.py
git commit -m "fix(kaf): project delegated intake fields"
```

### Task 4: Bind and Govern the Existing Microsoft Graph VPN Grant Tool

**Files:**

- Modify: `src/acp/workflows/assembler.py`
- Test: `tests/test_hitl.py`
- Modify: `src/acp/tools/metadata.py`
- Test: `tests/test_tool_registry.py`

**Interfaces:**

- Consumes: assembler state `collected_fields: Mapping[str, object]`; existing registered async Tool `ad_grant_vpn_access(user_identifier: str)`.
- Produces: `_build_final_action_kwargs("ad_grant_vpn_access", state) -> {"user_identifier": str}`.
- Produces metadata: `OpType.GRAPH_API`, `RiskLevel.HIGH`, `audit_required=True`, `requires_operator_review=False`, `agent_visible=False`, `case_types=("service_request",)`, `read_only=False`, `retry_on_failure=True`.
- Approval ownership: `requires_operator_review=False` is intentional because the two BPMN user tasks are the explicit approval boundary; KAF must not fabricate a second unresumable gate.

- [ ] **Step 1: Add a failing exact-kwargs binder test**

Place this beside the existing LDAP binder tests without deleting those global-capability tests:

```python
def test_bind_tool_params_ad_grant_vpn_access_uses_only_collected_identifier():
    state = {
        "collected_fields": {
            "user_identifier": "user@company.example",
            "target_vpn_group": "must-not-bind",
        },
        "raw_ticket": {"description": "must-not-bind"},
    }

    assert bind_tool_params("ad_grant_vpn_access", state, {}) == {
        "user_identifier": "user@company.example",
    }
```

- [ ] **Step 2: Add a failing registry metadata test**

Replace the registry-test import with this exact import, then assert the full security posture:

```python
from acp.tool_registry import (
    OpType,
    RiskLevel,
    TOOL_REGISTRY,
    apply_governance,
    call_with_sandbox,
    get_metadata,
)
```

```python
def test_ad_grant_vpn_access_metadata_is_high_risk_workflow_only_graph_write():
    metadata = TOOL_REGISTRY["ad_grant_vpn_access"]
    assert metadata.op_type is OpType.GRAPH_API
    assert metadata.risk_level is RiskLevel.HIGH
    assert metadata.audit_required is True
    assert metadata.requires_operator_review is False
    assert metadata.agent_visible is False
    assert metadata.case_types == ("service_request",)
    assert metadata.read_only is False
    assert metadata.retry_on_failure is True
```

- [ ] **Step 3: Run the tests and verify both contracts are absent**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest \
  tests/test_hitl.py::test_bind_tool_params_ad_grant_vpn_access_uses_only_collected_identifier \
  tests/test_tool_registry.py::test_ad_grant_vpn_access_metadata_is_high_risk_workflow_only_graph_write -q
```

Expected: FAIL because the assembler lacks the Graph branch and `TOOL_REGISTRY` lacks this key.

- [ ] **Step 4: Add the minimal typed binder**

Add before the legacy LDAP branch:

```python
if tool_name == "ad_grant_vpn_access":
    collected = state.get("collected_fields") or {}
    return {
        "user_identifier": str(collected.get("user_identifier") or ""),
    }
```

Do not pass group ID, reason, tenant, requester, expiry, vendor state, or LDAP options.

- [ ] **Step 5: Register the existing Graph Tool metadata**

Add one authoritative registry entry in the high-risk/write section:

```python
"ad_grant_vpn_access": ToolMetadata(
    tool_name="ad_grant_vpn_access",
    op_type=OpType.GRAPH_API,
    risk_level=RiskLevel.HIGH,
    audit_required=True,
    requires_operator_review=False,
    agent_visible=False,
    case_types=("service_request",),
    read_only=False,
    retry_on_failure=True,
    description="Grant VPN access through the configured Microsoft Graph security group.",
),
```

Do not make the Tool agent-visible and do not change the LDAP registry entries; LDAP retirement outside this Procedure is explicitly out of scope.

- [ ] **Step 6: Run assembler, governance, registry, and Graph Tool tests**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest \
  tests/test_hitl.py tests/test_tool_registry.py tests/test_vpn_grant_tool.py tests/integration/mcp/test_vpn.py -q
```

Expected: PASS; existing Graph already-member behavior and explicit missing-config/user-resolution failures remain covered.

- [ ] **Step 7: Commit the binder and metadata slice**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
git add src/acp/workflows/assembler.py tests/test_hitl.py src/acp/tools/metadata.py tests/test_tool_registry.py
git commit -m "feat(vpn): bind graph grant tool"
```

### Task 5: Replace the Authoritative SSLVPN Procedure with the Graph-Only Contract

**Files:**

- Modify: `scripts/procedures/vpn_permission_grant.md`
- Test: `tests/test_vpn_permission_grant_procedure_doc.py`
- Test: `tests/test_sr_detect_intent.py`
- Test: `tests/test_sr_execution_worker.py`

**Interfaces:**

- Consumes: frozen `collected_fields.user_identifier`; the two approvals already completed in ITSM BPMN.
- Produces: Procedure frontmatter with exactly one required field (`user_identifier`) and exactly one step (`{"type":"tool","name":"ad_grant_vpn_access"}`).
- Explicitly removes from this Procedure: `engineer_confirmation`, `ldap_grant_vpn_access`, `target_vpn_group`, `valid_end`, `is_vendor`, `vendor_name`, OU routing, organization-specific groups/domains, renew/vendor subflows.

- [ ] **Step 1: Rewrite the document contract tests to fail on the current LDAP Procedure**

Keep the existing YAML-frontmatter loader and replace its legacy assertions with exact set/list assertions:

```python
def test_vpn_permission_grant_is_graph_only():
    frontmatter = _frontmatter()
    body = _DOC_PATH.read_text(encoding="utf-8").split("\n---", 2)[2]
    assert frontmatter["intent"] == "vpn_permission_grant"
    assert frontmatter["workflow_id"] == "vpn_permission_grant"
    assert frontmatter["required_fields"] == [
        {
            "name": "user_identifier",
            "label": "目标用户 UPN",
            "quality_hint": "必须是 Microsoft Entra ID 可解析的用户 UPN。",
        }
    ]
    assert frontmatter["steps"] == [
        {"type": "tool", "name": "ad_grant_vpn_access"}
    ]
    rendered = yaml.safe_dump(frontmatter, allow_unicode=True) + body
    for forbidden in (
        "ldap_grant_vpn_access", "LDAP", "target_vpn_group", "valid_end",
        "is_vendor", "vendor_name", "CNDL-OKTA", "KEAS-SZX",
        "@kerryeas.com", "engineer_confirmation",
        "b7c7f066-3042-4a11-9e36-2ea80b979ae3",
        "Julian@dawnpro.onmicrosoft.com",
    ):
        assert forbidden not in rendered
```

Reuse the existing `_frontmatter()` parser; do not create a second YAML parser.

- [ ] **Step 2: Update intent and worker fixtures to expect the Graph step**

Replace only the `vpn_permission_grant` fixture fragments:

```python
"required_fields": [
    {"name": "user_identifier", "label": "目标用户 UPN"},
],
"steps": [
    {"type": "tool", "name": "ad_grant_vpn_access"},
],
```

In the direct-success worker test, patch the registered Graph Tool and assert the sole business argument:

```python
with patch(
    "acp.mcp.server.ad_grant_vpn_access",
    AsyncMock(return_value=ok),
) as fn:
    result = await SRExecutionWorker().execute_ticket(ctx, state, ticket)

fn.assert_awaited_once_with(user_identifier="user@company.example")
assert result.claimed is True
assert result.case_outcome == CaseOutcome.FULFILLED
```

For this test, set `ticket.required_fields` to `{"user_identifier": "user@company.example"}` and return `{"success": True, "data": {"user_identifier": "user@company.example", "already_member": False}}` from the mock. Rename the old gate-suspension VPN test to assert direct execution after ITSM approval; retain generic gate tests for Procedures that actually own KAF gates.

- [ ] **Step 3: Run the changed tests and confirm they reject the legacy document**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest \
  tests/test_vpn_permission_grant_procedure_doc.py tests/test_sr_detect_intent.py tests/test_sr_execution_worker.py -q
```

Expected: FAIL on LDAP/gate/extra-field expectations until the Procedure and fixtures agree.

- [ ] **Step 4: Replace the Procedure with a concise Graph-only source of truth**

Use this complete frontmatter contract:

```yaml
---
intent: vpn_permission_grant
title: VPN 安全组权限授予规范
type: procedure
category: vpn
match_threshold: 0.62
workflow_id: vpn_permission_grant
operation_label: VPN 权限授予
verb_phrase: 授予 VPN 安全组权限
success_message: "VPN 安全组权限已授予：用户 {user_identifier}。"
cancel_message: "VPN 权限授予未执行。"
tags: [VPN, SSLVPN, 远程访问, 安全组, Microsoft Graph, 权限授予]
examples:
  - 为 user@company.example 开通 SSLVPN 远程访问权限
  - 员工申请加入 VPN 安全组
negative_examples:
  - VPN 连接失败
  - VPN 密码错误
  - VPN 客户端安装
  - 取消 VPN 权限
required_fields:
  - name: user_identifier
    label: 目标用户 UPN
    quality_hint: 必须是 Microsoft Entra ID 可解析的用户 UPN。
steps:
  - type: tool
    name: ad_grant_vpn_access
---
```

The Markdown body must state: ITSM BPMN owns approval; KAF uses `IT_BACKEND=graph`; the Tool resolves `VPN_USERS_GROUP_ID`; missing configuration, unresolved user, or invalid group fails explicitly; an existing member is idempotent success. Do not document concrete tenants, domains, group IDs, user fixtures, LDAP routing, expiry, vendor, grant/revoke classification, or another approval gate.

- [ ] **Step 5: Run Procedure, intent, worker, assembler, and pipeline focused tests**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest \
  tests/test_vpn_permission_grant_procedure_doc.py tests/test_sr_detect_intent.py \
  tests/test_sr_execution_worker.py tests/test_hitl.py tests/test_kaf_delegation_pipeline.py -q
```

Expected: PASS; the authoritative Procedure contains one Graph Tool step and the headless flow completes without a KAF interrupt.

- [ ] **Step 6: Commit the Graph-only Procedure slice**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
git add scripts/procedures/vpn_permission_grant.md tests/test_vpn_permission_grant_procedure_doc.py \
  tests/test_sr_detect_intent.py tests/test_sr_execution_worker.py
git commit -m "feat(vpn): make grant procedure graph only"
```

### Task 6: Verify Both Repositories and Prepare the Current-Source Dev Runtime

**Files:**

- Verify: all files changed in Tasks 1–5
- Reuse: `scripts/dev/env-check.sh`
- Reuse: `src/acp/cli/ingest.py`
- Reuse: `src/acp/models/procedure_manifest.py`
- No repository file is created in this task; keep runtime logs under a `mktemp -d` directory.

**Interfaces:**

- Consumes: operator-owned `KAF_DEV_ENV_FILE`, `ITSM_DEV_ENV_FILE`, `ITSM_REQUESTER_TOKEN`, `ITSM_L1_TOKEN`, `ITSM_L2_TOKEN`, `ITSM_KAF_AUTOMATION_TOKEN`, and `ITSM_FOREIGN_KAF_TOKEN` environment variables.
- Produces: current-source ITSM on `127.0.0.1:8090`, current-source KAF on `127.0.0.1:8001`, KAF Alembic revision `036_kaf_completion_replay`, and a Dev Procedure registry whose retrieved step/hash matches PostgreSQL `ProcedureManifest`.
- Secret handling: tests check only presence and equality of non-secret endpoints/group ID; no command echoes token or secret values.

- [ ] **Step 1: Establish a private evidence directory and assert the environment boundary**

```bash
set -euo pipefail
export CLOSEOUT_EVIDENCE_DIR="$(mktemp -d /tmp/kaf-delegation-closeout.XXXXXX)"
printf '%s\n' "$CLOSEOUT_EVIDENCE_DIR" >/tmp/kaf-delegation-closeout.current
chmod 600 /tmp/kaf-delegation-closeout.current
test -n "${KAF_DEV_ENV_FILE:-}" && test -r "$KAF_DEV_ENV_FILE"
test -n "${ITSM_DEV_ENV_FILE:-}" && test -r "$ITSM_DEV_ENV_FILE"
case "$(realpath "$KAF_DEV_ENV_FILE")" in
  /home/administrator/project/itsm/*|/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/*)
    echo "secret-bearing KAF env file must remain outside both repositories" >&2; exit 1 ;;
esac
case "$(realpath "$ITSM_DEV_ENV_FILE")" in
  /home/administrator/project/itsm/*|/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/*)
    echo "secret-bearing ITSM env file must remain outside both repositories" >&2; exit 1 ;;
esac
printf '%s\n%s\n' "$KAF_DEV_ENV_FILE" "$ITSM_DEV_ENV_FILE" >"$CLOSEOUT_EVIDENCE_DIR/env-files"
chmod 600 "$CLOSEOUT_EVIDENCE_DIR/env-files"

set -a
. "$ITSM_DEV_ENV_FILE"
. "$KAF_DEV_ENV_FILE"
set +a

for name in DATABASE_URL ITSM_DATABASE_URL REDIS_URL QDRANT_URL ITSM_KAF_URL \
  ITSM_KAF_AUTOMATION_TOKEN ITSM_KAF_WEBHOOK_SECRET \
  AZURE_TENANT_ID AZURE_CLIENT_ID AZURE_CLIENT_SECRET \
  IT_BACKEND VPN_USERS_GROUP_ID ITSM_REQUESTER_TOKEN ITSM_L1_TOKEN \
  ITSM_L2_TOKEN ITSM_FOREIGN_KAF_TOKEN RLS_TEST_DSN; do
  test -n "${!name:-}" || { echo "missing required variable: $name" >&2; exit 1; }
done
test "$IT_BACKEND" = graph
test "$VPN_USERS_GROUP_ID" = b7c7f066-3042-4a11-9e36-2ea80b979ae3
test "$ITSM_KAF_URL" = http://127.0.0.1:8090
test "${KAF_WEBHOOK_URL:-}" = http://127.0.0.1:8001/webhooks/itsm
test -n "${KAF_WEBHOOK_SECRET:-}"
test "$KAF_WEBHOOK_SECRET" = "$ITSM_KAF_WEBHOOK_SECRET"
for value in "$DATABASE_URL" "$REDIS_URL" "$QDRANT_URL" "$ITSM_KAF_URL" "$KAF_WEBHOOK_URL"; do
  test "${value#*10.128.35.195}" = "$value" || { echo 'PROD endpoint rejected' >&2; exit 1; }
done
PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python - <<'PY'
from urllib.parse import urlparse
import os
assert urlparse(os.environ["DATABASE_URL"].replace("postgresql+asyncpg", "postgresql")).port == 5434
assert urlparse(os.environ["REDIS_URL"]).port == 6380
assert urlparse(os.environ["QDRANT_URL"]).port == 6335
PY
```

Expected: every assertion exits zero. A failure is a pre-mutation blocker; stop before ingest or Graph calls.

- [ ] **Step 2: Run ITSM focused, full, build, and diff verification**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./controller -run 'TestKaf(Context|DelegatedList|Action)' -count=1 -v
go test ./service -run 'Test(Kaf|ExecuteAction)' -count=1 -v
go test ./tests/fixtures -run TestSSLVPN -count=1 -v
go test ./handlers/service_request -run TestServiceRequestKafDelegationSSLVPN -count=1 -v
go test ./... -count=1
go build ./...
cd /home/administrator/project/itsm
git diff --check
```

Expected: every command exits zero. Capture exact durations/counts into the private evidence directory; do not proceed on an ITSM failure.

- [ ] **Step 3: Run KAF focused formatting and test verification**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff check \
  src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py \
  src/acp/workflows/assembler.py src/acp/tools/metadata.py \
  tests/test_kaf_delegation_pipeline.py tests/test_hitl.py tests/test_tool_registry.py \
  tests/test_vpn_permission_grant_procedure_doc.py tests/test_sr_detect_intent.py \
  tests/test_sr_execution_worker.py
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff format --check \
  src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py \
  src/acp/workflows/assembler.py src/acp/tools/metadata.py \
  tests/test_kaf_delegation_pipeline.py tests/test_hitl.py tests/test_tool_registry.py \
  tests/test_vpn_permission_grant_procedure_doc.py tests/test_sr_detect_intent.py \
  tests/test_sr_execution_worker.py
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest \
  tests/test_kaf_delegation_pipeline.py tests/test_itsm_webhook_auth.py tests/test_itsm_webhooks.py \
  tests/test_hitl.py tests/test_tool_registry.py tests/test_vpn_grant_tool.py \
  tests/integration/mcp/test_vpn.py tests/test_vpn_permission_grant_procedure_doc.py \
  tests/test_sr_detect_intent.py tests/test_sr_execution_worker.py -q
git diff --check
```

Expected: focused commands exit zero. Do not run the live mutation if a changed-scope KAF test fails.

- [ ] **Step 4: Record the repository-wide KAF result truthfully**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
set +e
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest -q \
  >"$CLOSEOUT_EVIDENCE_DIR/kaf-full-pytest.log" 2>&1
KAF_FULL_EXIT=$?
set -e
tail -n 30 "$CLOSEOUT_EVIDENCE_DIR/kaf-full-pytest.log"
printf 'exit_code=%s\n' "$KAF_FULL_EXIT" >"$CLOSEOUT_EVIDENCE_DIR/kaf-full-pytest.summary"
```

Expected: store the actual exit code and terminal count line. Compare every modified-scope failure against Step 3; any new modified-scope failure blocks live execution. Never rewrite a nonzero result as “all green.”

- [ ] **Step 5: Bring the KAF Dev data plane to the exact migration head**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
docker compose -f docker/docker-compose.dev.yml -p kaf-dev up -d
./scripts/dev/env-check.sh "$KAF_DEV_ENV_FILE"
PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m alembic upgrade head
PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m alembic heads
docker exec kaf-dev-postgres psql -U ai01 -d control_plane -Atc \
  'select version_num from alembic_version'
```

Expected: source and database both report exactly `036_kaf_completion_replay`; zero alternate heads.

- [ ] **Step 6: Ingest the changed authoritative Procedure through the existing pipeline**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m acp.cli.ingest \
  --procedures-dir scripts/procedures --workspace-id it-support
```

Expected: CLI exit zero and `failed=0`; do not edit Qdrant or PostgreSQL directly.

- [ ] **Step 7: Verify Qdrant retrieval and ProcedureManifest agree**

Run this read-only check with the loaded Dev environment:

```bash
PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python - <<'PY'
import asyncio
from sqlalchemy import select
from acp.database import async_session_factory
from acp.models.procedure_manifest import ProcedureManifest
from acp.procedures.rag import get_by_intent

async def main():
    hit = await get_by_intent("vpn_permission_grant", "it-support")
    assert hit is not None
    assert hit["steps"] == [{"type": "tool", "name": "ad_grant_vpn_access"}]
    assert hit.get("content_hash")
    async with async_session_factory() as session:
        row = (await session.execute(
            select(ProcedureManifest).where(
                ProcedureManifest.workspace_id == "it-support",
                ProcedureManifest.procedure_key == "vpn_permission_grant",
            )
        )).scalar_one()
    assert row.steps == hit["steps"]
    assert row.frontmatter_json["steps"] == hit["steps"]
    print({"procedure": row.procedure_key, "steps": row.steps, "content_hash_present": True})

asyncio.run(main())
PY
```

Expected: one sanitized dictionary with the Graph Tool step; no document body or secret output.

- [ ] **Step 8: Start or prove current-source ITSM/KAF processes and record exact commits**

Use the process working directory, not a container name, to prove an already-running listener is current source. Otherwise start it and persist its PID under the private evidence directory:

```bash
ITSM_PID="$(fuser -n tcp 8090 2>/dev/null | awk '{print $1}' | head -1)"
if test -n "$ITSM_PID"; then
  test "$(readlink -f "/proc/$ITSM_PID/cwd")" = /home/administrator/project/itsm/itsm-backend
  tr '\0' '\n' <"/proc/$ITSM_PID/environ" | grep -qx 'KAF_WEBHOOK_URL=http://127.0.0.1:8001/webhooks/itsm'
  test "$(tr '\0' '\n' <"/proc/$ITSM_PID/environ" | sed -n 's/^KAF_WEBHOOK_SECRET=//p')" = "$KAF_WEBHOOK_SECRET"
else
  (
    cd /home/administrator/project/itsm/itsm-backend
    set -a
    . "$ITSM_DEV_ENV_FILE"
    set +a
    exec go run .
  ) >"$CLOSEOUT_EVIDENCE_DIR/itsm-current-source.log" 2>&1 &
  ITSM_PID=$!
fi
printf '%s\n' "$ITSM_PID" >"$CLOSEOUT_EVIDENCE_DIR/itsm-current-source.pid"

KAF_PID="$(fuser -n tcp 8001 2>/dev/null | awk '{print $1}' | head -1)"
if test -n "$KAF_PID"; then
  test "$(readlink -f "/proc/$KAF_PID/cwd")" = /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
  tr '\0' '\n' <"/proc/$KAF_PID/environ" | grep -qx 'ITSM_KAF_URL=http://127.0.0.1:8090'
  test "$(tr '\0' '\n' <"/proc/$KAF_PID/environ" | sed -n 's/^ITSM_KAF_AUTOMATION_TOKEN=//p')" = "$ITSM_KAF_AUTOMATION_TOKEN"
  test "$(tr '\0' '\n' <"/proc/$KAF_PID/environ" | sed -n 's/^ITSM_KAF_WEBHOOK_SECRET=//p')" = "$ITSM_KAF_WEBHOOK_SECRET"
  tr '\0' '\n' <"/proc/$KAF_PID/environ" | grep -qx 'IT_BACKEND=graph'
  tr '\0' '\n' <"/proc/$KAF_PID/environ" | grep -qx 'VPN_USERS_GROUP_ID=b7c7f066-3042-4a11-9e36-2ea80b979ae3'
else
  cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
  nohup env PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m uvicorn \
    acp.main:app --host 127.0.0.1 --port 8001 \
    >"$CLOSEOUT_EVIDENCE_DIR/kaf-current-source.log" 2>&1 &
  KAF_PID=$!
fi
printf '%s\n' "$KAF_PID" >"$CLOSEOUT_EVIDENCE_DIR/kaf-current-source.pid"

for attempt in $(seq 1 30); do curl -fsS http://127.0.0.1:8090/api/v1/health && break; sleep 1; done
for attempt in $(seq 1 30); do curl -fsS http://127.0.0.1:8001/health && break; sleep 1; done
```

Then record only non-secret facts:

```bash
curl -fsS http://127.0.0.1:8090/api/v1/health
curl -fsS http://127.0.0.1:8001/health
curl -fsS -H "Authorization: Bearer $ITSM_REQUESTER_TOKEN" \
  'http://127.0.0.1:8090/api/v1/service-catalogs?page=1&size=1' >/dev/null
curl -fsS -H "Authorization: Bearer $ITSM_L1_TOKEN" \
  'http://127.0.0.1:8090/api/v1/bpmn/tasks?status=assigned&page=1&pageSize=1' >/dev/null
curl -fsS -H "Authorization: Bearer $ITSM_L2_TOKEN" \
  'http://127.0.0.1:8090/api/v1/bpmn/tasks?status=assigned&page=1&pageSize=1' >/dev/null
curl -fsS -H "Authorization: Bearer $ITSM_KAF_AUTOMATION_TOKEN" \
  'http://127.0.0.1:8090/api/v1/bpmn/process-tasks/kaf-delegated?status=delegated&limit=1' >/dev/null
curl -fsS -H "Authorization: Bearer $ITSM_FOREIGN_KAF_TOKEN" \
  'http://127.0.0.1:8090/api/v1/bpmn/process-tasks/kaf-delegated?status=delegated&limit=1' >/dev/null
git -C /home/administrator/project/itsm rev-parse HEAD
git -C /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery rev-parse HEAD
```

Expected: both health calls succeed; `/proc/$KAF_PID/cwd` is the feature worktree, not the old `acp-backend:8000` container.

### Task 6A: Repair and Verify the Atomic BPMN-to-KAF Handoff

> **Approved live-finding remediation:** Do not add a Web startup migration check, automatic orphan compatibility path, VPN-specific branch, test endpoint, or direct business-table repair. Keep schema migration in the explicit bootstrap job and keep Graph/Procedure/Tool execution outside the ITSM transaction.

**Files:**

- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Modify: `itsm-backend/service/kaf_delegation_service.go`
- Test: `itsm-backend/service/kaf_delegation_service_test.go`
- Test: `itsm-backend/handlers/service_request/kaf_delegation_sslvpn_e2e_test.go`
- Verify: `itsm-backend/internal/bootstrap/post_schema_migrations_test.go`
- Runtime evidence: existing protected closeout evidence directory

**Interfaces:**

- Consumes: the existing BPMN decision path, `CustomProcessEngine`, `KafDelegationService`, Ent transaction, approval-decision schema, audit service, and Outbox repository.
- Produces: one database transaction covering the source task, process variable/version/activity transition, approval decision, KAF delegated task, source/delegation audits, and Outbox event.
- Excludes: runtime auto-migration/readiness gates, synchronous Graph/Procedure/Tool work, a second workflow engine, process-143-specific behavior, and direct database repair.

- [x] **Step 1: Add a failing end-to-end service test for Outbox failure at the L2 handoff**

Extend the existing SSLVPN/service fixture so the KAF Outbox repository fails while the real L2 approval is submitted. Before implementation, prove the test fails because the current engine leaves the source task completed or advances process state.

The expected contract after the repair is:

```text
decision returns an explicit persistence error
L2 source task remains in its pre-decision actionable status
source task variables/completed_time are unchanged
process variables, version, and current activity remain unchanged
approval decision count remains zero for L2
delegated KAF task count remains zero
kaf_delegate.created and source-task completed audit counts remain zero
outbox event count remains zero
Graph/KAF runtime is not involved
```

Run the narrow RED test and preserve its expected assertion failure in the private evidence directory.

- [x] **Step 2: Implement one transaction for the persistent KAF handoff**

Refactor the existing KAF delegated-task creation so it can participate in a caller-owned Ent transaction while retaining its current public all-or-nothing entry point. In the BPMN engine, only the transition whose resolved successor is the registered KAF async wait state uses the combined transaction.

Inside that transaction:

1. conditionally complete the source task from a non-terminal state;
2. merge instance variables and increment the authoritative version with optimistic concurrency protection;
3. update the process current activity to the KAF service task;
4. create the approval decision using the same transaction client;
5. create the delegated task, sanitized creation audit, and Outbox event using the same transaction;
6. record source-task completion audit using the same transaction;
7. commit once; on any error, roll back every write.

Do not call Graph, KAF HTTP, Procedure, Tool, or user-task business callbacks inside the transaction. Preserve the existing post-commit callback behavior. Do not add logic for a particular BPMN key, catalog, record ID, UPN, group, or operation kind.

- [x] **Step 3: Prove rollback, retry, concurrency, and cardinality**

After the RED test turns GREEN, restore the Outbox repository and retry the same L2 task. Assert exactly one approval decision, delegated task, creation audit, source completion audit, and Outbox event, with the process waiting at the KAF activity and the source task completed.

Add a concurrent double-decision assertion: one caller succeeds, the other receives the existing conflict/processed error, and all authoritative side-effect counts remain one. Retain the existing invalid `record_class` retry test and all KAF completion replay tests.

Run:

```bash
cd /home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery/itsm-backend
go test ./service -run 'Test.*Kaf.*(Transaction|Rollback|Retry|Concurrent)' -count=1 -v
go test ./handlers/service_request -run 'TestSSLVPN' -count=1 -v
go test ./service ./controller ./handlers/service_request -count=1
go test ./... -count=1
go build ./...
cd ..
git diff --check
```

Expected: all commands exit zero; no tracked file outside the approved remediation scope changes.

- [x] **Step 4: Run the explicit ITSM bootstrap migration with no seed**

Stop only the feature ITSM listener on `127.0.0.1:8090`. Source the private ITSM Dev env without printing values, then execute the repository-supported one-shot bootstrap:

```bash
cd /home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery/itsm-backend
set -a
. "$ITSM_DEV_ENV_FILE"
set +a
ITSM_BOOTSTRAP_ONLY=true ITSM_AUTO_MIGRATE=true ITSM_AUTO_SEED=false \
  ITSM_RELEASE_VERSION=kaf-delegation-closeout go run .
```

Read-only verification must prove:

```text
schema_migrations contains 019_kaf_execution_integrity_rls
outbox_events exists
kaf_task_action_ledgers exists with forced tenant RLS
kaf_task_completion_receipts exists with forced tenant RLS
no seed run was requested
```

Restart the feature ITSM listener with migration flags unset and verify `/proc/<pid>/cwd` plus health. No migration-head check is added to the Web application.

- [x] **Step 5: Close the failed Dev process through the official API**

Renew the short-lived administrative token through the established private helper. Call the existing authorized endpoint:

```text
PUT /api/v1/bpmn/process-instances/<process-instance-key>/terminate
{"reason":"Release-closeout preflight failed before KAF delegation because the ITSM execution-integrity schema was not applied"}
```

Read back process 143 and prove `status=terminated`, Graph membership remains false, and no KAF task/outbox/action exists for the failed process. Preserve SR 34 / WorkItem 17 / process 143 as failure evidence. Do not reset task 197, directly edit any table, or manually start another process.

- [x] **Step 6: Independent review checkpoint**

A fresh reviewer must verify the implementation against this Task 6A contract and `AGENTS.md`: one authoritative transaction boundary, no compatibility layer, no hardcoded VPN/process behavior, tenant-safe writes, explicit errors, and tests demonstrating rollback plus retry. Address every finding before Task 7 resumes.

Completion evidence: ITSM commits `48159722`, `bff2bc13`, `eeaee520`, and `0e7a8701` establish the atomic KAF handoff, fail-closed canonical/gateway dispatch, authoritative route commit, and fenced non-KAF route claim. Final independent review found no findings; focused/race/full tests, `go build ./...`, and `git diff --check` passed. Bootstrap-only applied `019_kaf_execution_integrity_rls` with `ITSM_AUTO_SEED=false`; all three execution-integrity tables exist and both ledger/receipt tables have enabled+forced tenant RLS. Process 143 was terminated through the official API, has no L2 decision/KAF task/KAF audit/Outbox/action/receipt, and Graph readback remains `member=false`.

### Task 7: Execute One Real SSLVPN Main Path, Persist Evidence, Replay, and Restore

**Files:**

- Runtime-only: `$CLOSEOUT_EVIDENCE_DIR` created in Task 6
- Reuse: official ITSM APIs and existing KAF `ad_grant_vpn_access`/`remove_vpn_access` Tools
- No repository file is modified until Task 8 writes the sanitized report.

**Interfaces:**

- Consumes: official Service Request, process-instance variables, BPMN decision, attachment, KAF context/list, and task-action endpoints.
- Produces: one newly created successful WorkItem/BPMN path after the preserved, officially terminated preflight failure; one published outbox event, one completed KAF delivery, one applied ledger/receipt, one Graph grant external action with sanitized audit rows, one exact-payload replay returning `already_applied`, and final Graph `member=false`.
- Stop rule: after cleanup is armed, every failure path runs the Graph membership check and `remove_vpn_access(user_identifier)` when necessary; cleanup failure stops all further real-change work and marks closeout failed.

- [x] **Step 1: Define read-only membership and controlled cleanup helpers**

Keep the target fixture in shell variables, but let the Tool obtain the group from configuration:

```bash
set -euo pipefail
export CLOSEOUT_EVIDENCE_DIR="$(cat /tmp/kaf-delegation-closeout.current)"
mapfile -t CLOSEOUT_ENV_FILES <"$CLOSEOUT_EVIDENCE_DIR/env-files"
set -a
. "${CLOSEOUT_ENV_FILES[1]}"
. "${CLOSEOUT_ENV_FILES[0]}"
set +a
export TARGET_UPN='Julian@dawnpro.onmicrosoft.com'
export EXPECTED_GROUP_ID='b7c7f066-3042-4a11-9e36-2ea80b979ae3'
export GRAPH_MUTATION_ARMED=0

graph_membership() {
  PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python - <<'PY'
import asyncio, os
from acp.graph.client import get_graph_client
from acp.graph.users import graph_identity_lookup

async def main():
    client = get_graph_client()
    user = await graph_identity_lookup(client, os.environ["TARGET_UPN"])
    assert user is not None
    assert os.environ["VPN_USERS_GROUP_ID"] == os.environ["EXPECTED_GROUP_ID"]
    result = await client.post(
        f'/users/{user["_graph_id"]}/checkMemberGroups',
        {"groupIds": [os.environ["EXPECTED_GROUP_ID"]]},
    )
    member = os.environ["EXPECTED_GROUP_ID"].lower() in {
        str(value).lower() for value in result.get("value", [])
    }
    print(f'user_object_id={user["_graph_id"]} group_object_id={os.environ["EXPECTED_GROUP_ID"]} member={str(member).lower()}')

asyncio.run(main())
PY
}

cleanup_membership() {
  test "$GRAPH_MUTATION_ARMED" = 1 || return 0
  PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python - <<'PY'
import asyncio, os
from acp.mcp.tools.vpn import remove_vpn_access

async def main():
    result = await remove_vpn_access(os.environ["TARGET_UPN"])
    assert result.get("success") is True, result.get("error", {}).get("code", "remove_failed")
    print("cleanup_tool_success=true")

asyncio.run(main())
PY
  graph_membership | tee "$CLOSEOUT_EVIDENCE_DIR/graph-after-cleanup.txt"
  grep -q 'member=false' "$CLOSEOUT_EVIDENCE_DIR/graph-after-cleanup.txt"
  GRAPH_MUTATION_ARMED=0
}

trap 'cleanup_membership' EXIT INT TERM
```

Expected: helpers print only object IDs, membership booleans, and success state; no token or credential.

- [x] **Step 2: Establish and prove the non-member baseline**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
TZ=Asia/Shanghai date --iso-8601=seconds >"$CLOSEOUT_EVIDENCE_DIR/execution-start.txt"
GRAPH_MUTATION_ARMED=1
graph_membership | tee "$CLOSEOUT_EVIDENCE_DIR/graph-before.txt"
if ! grep -q 'member=false' "$CLOSEOUT_EVIDENCE_DIR/graph-before.txt"; then
  cleanup_membership
  GRAPH_MUTATION_ARMED=1
  graph_membership | tee "$CLOSEOUT_EVIDENCE_DIR/graph-before.txt"
  grep -q 'member=false' "$CLOSEOUT_EVIDENCE_DIR/graph-before.txt"
fi
```

Expected: the final baseline file says `member=false`. Failure to remove/confirm is a hard stop.

- [x] **Step 3: Resolve the SSLVPN catalog and create the Service Request through the official API**

```bash
export ITSM_BASE_URL=http://127.0.0.1:8090
CATALOG_RESPONSE="$(curl -fsS -H "Authorization: Bearer $ITSM_REQUESTER_TOKEN" \
  "$ITSM_BASE_URL/api/v1/service-catalogs?page=1&size=100")"
export SSLVPN_CATALOG_ID="$(jq -er '.. | objects | select(.name? == "SSL-VPN 远程办公访问权限申请") | .id' \
  <<<"$CATALOG_RESPONSE" | head -1)"

SR_RESPONSE="$(curl -fsS -X POST \
  -H "Authorization: Bearer $ITSM_REQUESTER_TOKEN" \
  -H 'Content-Type: application/json' \
  "$ITSM_BASE_URL/api/v1/service-requests" \
  --data "$(jq -nc --argjson catalogId "$SSLVPN_CATALOG_ID" --arg upn "$TARGET_UPN" '{
    catalogId: $catalogId,
    title: ("SSLVPN access for " + $upn),
    reason: "KAF delegation release closeout",
    formData: {user_identifier: $upn},
    complianceAck: true
  }')")"
export SERVICE_REQUEST_ID="$(jq -er '.data.id' <<<"$SR_RESPONSE")"
export WORK_ITEM_ID="$(jq -er '.data.ticketId' <<<"$SR_RESPONSE")"
printf 'service_request_id=%s work_item_id=%s\n' "$SERVICE_REQUEST_ID" "$WORK_ITEM_ID" \
  | tee "$CLOSEOUT_EVIDENCE_DIR/itsm-work-item.txt"
```

Expected: one catalog, positive Service Request ID, and positive WorkItem ID. Do not start a second process manually.

- [x] **Step 4: Resolve the unique process and freeze the intake before approval**

```bash
for attempt in $(seq 1 30); do
  PROCESS_RESPONSE="$(curl -fsS -G \
    -H "Authorization: Bearer $ITSM_REQUESTER_TOKEN" \
    --data-urlencode "businessKey=ticket:$WORK_ITEM_ID" \
    --data-urlencode 'page=1' --data-urlencode 'pageSize=20' \
    "$ITSM_BASE_URL/api/v1/bpmn/process-instances")"
  PROCESS_TOTAL="$(jq -er '.data.pagination.total' <<<"$PROCESS_RESPONSE")"
  test "$PROCESS_TOTAL" -le 1
  test "$PROCESS_TOTAL" = 1 && break
  sleep 1
done
test "$PROCESS_TOTAL" = 1
export PROCESS_INSTANCE_ID="$(jq -er '.data.data[0].id' <<<"$PROCESS_RESPONSE")"
export PROCESS_INSTANCE_KEY="$(jq -er '.data.data[0].processInstanceId' <<<"$PROCESS_RESPONSE")"

curl -fsS -X PUT \
  -H "Authorization: Bearer $ITSM_REQUESTER_TOKEN" \
  -H 'Content-Type: application/json' \
  "$ITSM_BASE_URL/api/v1/bpmn/process-instances/$PROCESS_INSTANCE_KEY/variables" \
  --data "$(jq -nc --arg upn "$TARGET_UPN" '{variables:{intake_snapshot:{
    operationKind:"vpn_permission_grant",
    collected_fields:{user_identifier:$upn}
  }}}')" | jq -e '.code == 0'
printf 'process_instance_id=%s process_instance_key=%s\n' \
  "$PROCESS_INSTANCE_ID" "$PROCESS_INSTANCE_KEY" \
  | tee "$CLOSEOUT_EVIDENCE_DIR/itsm-process.txt"
```

Expected: the numeric ID and string key are both nonempty and the update returns code zero.

- [x] **Step 5: Upload one harmless attachment and complete L1 approval**

```bash
ATTACHMENT_FILE="$(mktemp)"
printf 'KAF closeout attachment disclosure probe\n' >"$ATTACHMENT_FILE"
ATTACHMENT_RESPONSE="$(curl -fsS -X POST \
  -H "Authorization: Bearer $ITSM_REQUESTER_TOKEN" \
  -F "file=@$ATTACHMENT_FILE;filename=closeout-probe.txt;type=text/plain" \
  "$ITSM_BASE_URL/api/v1/tickets/$WORK_ITEM_ID/attachments")"
rm -f "$ATTACHMENT_FILE"
export ATTACHMENT_ID="$(jq -er '.. | objects | select(has("id")) | .id' <<<"$ATTACHMENT_RESPONSE" | tail -1)"

L1_TASKS="$(curl -fsS -G -H "Authorization: Bearer $ITSM_L1_TOKEN" \
  --data-urlencode "processInstanceId=$PROCESS_INSTANCE_ID" --data-urlencode 'status=assigned' \
  "$ITSM_BASE_URL/api/v1/bpmn/tasks")"
export L1_TASK_ID="$(jq -er '.. | objects | select(.taskDefinitionKey? == "UserTask_DeptManagerApproval") | .id' \
  <<<"$L1_TASKS" | head -1)"
curl -fsS -X POST -H "Authorization: Bearer $ITSM_L1_TOKEN" -H 'Content-Type: application/json' \
  "$ITSM_BASE_URL/api/v1/bpmn/tasks/$L1_TASK_ID/decisions" \
  --data '{"action":"approve","comment":"KAF closeout L1 approval"}' | jq -e '.code == 0'
```

Expected: attachment upload succeeds and the L1 decision advances to `UserTask_L2NetworkOpsApproval`.

- [x] **Step 6: Stop KAF before L2, approve L2, and identify the delegated task without a Graph race**

Stop only the current-source Uvicorn `:8001` process from Task 6; leave KAF PostgreSQL/Redis/Qdrant and ITSM running. Then:

```bash
KAF_PID="$(cat "$CLOSEOUT_EVIDENCE_DIR/kaf-current-source.pid")"
test "$(readlink -f "/proc/$KAF_PID/cwd")" = /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
kill -TERM "$KAF_PID"
for attempt in $(seq 1 30); do
  ! curl -fsS http://127.0.0.1:8001/health >/dev/null 2>&1 && break
  sleep 1
done
! curl -fsS http://127.0.0.1:8001/health >/dev/null 2>&1

L2_TASKS="$(curl -fsS -G -H "Authorization: Bearer $ITSM_L2_TOKEN" \
  --data-urlencode "processInstanceId=$PROCESS_INSTANCE_ID" --data-urlencode 'status=assigned' \
  "$ITSM_BASE_URL/api/v1/bpmn/tasks")"
export L2_TASK_ID="$(jq -er '.. | objects | select(.taskDefinitionKey? == "UserTask_L2NetworkOpsApproval") | .id' \
  <<<"$L2_TASKS" | head -1)"
curl -fsS -X POST -H "Authorization: Bearer $ITSM_L2_TOKEN" -H 'Content-Type: application/json' \
  "$ITSM_BASE_URL/api/v1/bpmn/tasks/$L2_TASK_ID/decisions" \
  --data '{"action":"approve","comment":"KAF closeout L2 approval"}' | jq -e '.code == 0'

for attempt in $(seq 1 30); do
  DELEGATED="$(curl -fsS -H "Authorization: Bearer $ITSM_KAF_AUTOMATION_TOKEN" \
    "$ITSM_BASE_URL/api/v1/bpmn/process-tasks/kaf-delegated?status=delegated&limit=100")"
  KAF_TASK_ID="$(jq -r --arg pi "$PROCESS_INSTANCE_KEY" \
    '.data.items[]? | select(.waitingPoint.processInstanceId == $pi) | .taskId' <<<"$DELEGATED")"
  test -n "$KAF_TASK_ID" && break
  sleep 1
done
export KAF_TASK_ID
test -n "$KAF_TASK_ID"
```

Expected: KAF is down, Graph still says `member=false`, and exactly one delegated task is found for this process.

- [x] **Step 7: Run live task-scope, tenant, list-audit, and attachment-disclosure breakers**

```bash
curl -sS -o "$CLOSEOUT_EVIDENCE_DIR/wrong-subject.json" -w '%{http_code}\n' \
  -H "Authorization: Bearer $ITSM_REQUESTER_TOKEN" \
  "$ITSM_BASE_URL/api/v1/bpmn/process-tasks/$KAF_TASK_ID/kaf-context" \
  >"$CLOSEOUT_EVIDENCE_DIR/wrong-subject.status"
grep -Eq '^(403|404)$' "$CLOSEOUT_EVIDENCE_DIR/wrong-subject.status"

curl -sS -o "$CLOSEOUT_EVIDENCE_DIR/cross-tenant.json" -w '%{http_code}\n' \
  -H "Authorization: Bearer $ITSM_FOREIGN_KAF_TOKEN" \
  "$ITSM_BASE_URL/api/v1/bpmn/process-tasks/$KAF_TASK_ID/kaf-context" \
  >"$CLOSEOUT_EVIDENCE_DIR/cross-tenant.status"
grep -q '^404$' "$CLOSEOUT_EVIDENCE_DIR/cross-tenant.status"

CONTEXT_RESPONSE="$(curl -fsS \
  -H "Authorization: Bearer $ITSM_KAF_AUTOMATION_TOKEN" \
  "$ITSM_BASE_URL/api/v1/bpmn/process-tasks/$KAF_TASK_ID/kaf-context")"
jq -e --argjson attachmentId "$ATTACHMENT_ID" '
  .data.intakeSnapshot == {
    operationKind:"vpn_permission_grant",
    collected_fields:{user_identifier:"Julian@dawnpro.onmicrosoft.com"}
  } and
  .data.attachments == [{id:$attachmentId}] and
  ((tostring | contains("closeout-probe.txt")) | not) and
  ((tostring | contains("filePath")) | not) and
  ((tostring | contains("fileUrl")) | not)
' <<<"$CONTEXT_RESPONSE"

jq -e --arg task "$KAF_TASK_ID" \
  '.data.items | map(select(.taskId == $task)) | length == 1' <<<"$DELEGATED"
graph_membership | grep -q 'member=false'
```

Expected: ordinary/cross-tenant actors cannot read context; the valid response exposes only attachment ID; no Graph mutation has occurred. The valid list and context calls each create their designed aggregate/single audit.

- [x] **Step 8: Restart current-source KAF and wait at most five minutes for end-to-end convergence**

Restart the exact current-source Uvicorn command on `127.0.0.1:8001`, verify its working directory, then poll read-only state:

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
nohup env PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m uvicorn \
  acp.main:app --host 127.0.0.1 --port 8001 \
  >>"$CLOSEOUT_EVIDENCE_DIR/kaf-current-source.log" 2>&1 &
KAF_PID=$!
printf '%s\n' "$KAF_PID" >"$CLOSEOUT_EVIDENCE_DIR/kaf-current-source.pid"
for attempt in $(seq 1 30); do curl -fsS http://127.0.0.1:8001/health && break; sleep 1; done
test "$(readlink -f "/proc/$KAF_PID/cwd")" = /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery

for attempt in $(seq 1 150); do
  DELIVERY_STATE="$(docker exec kaf-dev-postgres psql -U ai01 -d control_plane -AtF '|' -c \
    "select status, coalesce(completion_payload->'execution'->>'idempotencyKey','')
       from kaf_delegation_deliveries where task_id = '$KAF_TASK_ID'")"
  test "${DELIVERY_STATE%%|*}" = completed && break
  sleep 2
done
test "${DELIVERY_STATE%%|*}" = completed
graph_membership | tee "$CLOSEOUT_EVIDENCE_DIR/graph-after-grant.txt"
grep -q 'member=true' "$CLOSEOUT_EVIDENCE_DIR/graph-after-grant.txt"
```

Expected: outbox retry delivers after KAF restart, KAF completes, and the real group membership becomes true. Timeout or delivery failure jumps to cleanup.

- [x] **Step 9: Read and assert authoritative side-effect cardinalities**

Use the already-loaded `ITSM_DATABASE_URL` without printing it:

```bash
psql "$ITSM_DATABASE_URL" -v ON_ERROR_STOP=1 -AtF '|' <<SQL \
  >"$CLOSEOUT_EVIDENCE_DIR/itsm-cardinality.txt"
select 'outbox', count(*), string_agg(status, ',')
  from outbox_events where aggregate_id = '$KAF_TASK_ID' and event_type = 'kaf_delegate_requested';
select 'ledger', count(*), string_agg(result_status, ',')
  from kaf_task_action_ledgers where task_id = '$KAF_TASK_ID' and action = 'complete_bpmn_task';
select 'receipt', count(*), string_agg(status, ',')
  from kaf_task_completion_receipts where task_id = '$KAF_TASK_ID';
select 'completion_audit', count(*), min(status_code)::text
  from audit_logs where action = 'kaf_delegate.complete_bpmn_task'
    and request_body like '%' || '$KAF_TASK_ID' || '%';
select 'context_audit', count(*), min(status_code)::text
  from audit_logs where action = 'kaf_delegate.context_read'
    and request_body like '%' || '$KAF_TASK_ID' || '%';
SQL

docker exec kaf-dev-postgres psql -U ai01 -d control_plane -AtF '|' -c \
  "select 'delivery', count(*), string_agg(status, ',')
     from kaf_delegation_deliveries where task_id = '$KAF_TASK_ID';
   select 'graph_grant_action', count(*), string_agg(status, ',')
     from external_actions where tool_name = 'ad_grant_vpn_access'
       and params->>'user_identifier' = '$TARGET_UPN'
       and created_at >= (select min(received_at) from kaf_delegation_deliveries where task_id = '$KAF_TASK_ID');
   select 'graph_grant_audit_rows', count(*), count(*) filter (where params::text like '%client_secret%')
     from audit_logs where tool_name = 'ad_grant_vpn_access'
       and created_at >= (select min(received_at) from kaf_delegation_deliveries where task_id = '$KAF_TASK_ID');" \
  >"$CLOSEOUT_EVIDENCE_DIR/kaf-cardinality.txt"

grep -q '^outbox|1|published$' "$CLOSEOUT_EVIDENCE_DIR/itsm-cardinality.txt"
grep -q '^ledger|1|applied$' "$CLOSEOUT_EVIDENCE_DIR/itsm-cardinality.txt"
grep -q '^receipt|1|callback_succeeded$' "$CLOSEOUT_EVIDENCE_DIR/itsm-cardinality.txt"
grep -q '^completion_audit|1|200$' "$CLOSEOUT_EVIDENCE_DIR/itsm-cardinality.txt"
grep -q '^delivery|1|completed$' "$CLOSEOUT_EVIDENCE_DIR/kaf-cardinality.txt"
grep -q '^graph_grant_action|1|succeeded$' "$CLOSEOUT_EVIDENCE_DIR/kaf-cardinality.txt"
grep -Eq '^graph_grant_audit_rows\|[1-9][0-9]*\|0$' "$CLOSEOUT_EVIDENCE_DIR/kaf-cardinality.txt"
```

Expected: one outbox, ledger, receipt, completion audit, delivery, and Graph external action. Governance may persist separate pre/post/decorator audit rows, so those audit rows must be present and sanitized rather than incorrectly used as the invocation cardinality. Context audit count may be greater than one because the explicit disclosure probe and KAF runtime are two separate successful reads; each row must remain sanitized.

- [x] **Step 10: Replay the exact persisted completion payload and prove no duplicate effects**

Read the payload without displaying it, submit it with the KAF automation token, then repeat cardinality queries:

```bash
docker exec kaf-dev-postgres psql -U ai01 -d control_plane -Atc \
  "select completion_payload::text from kaf_delegation_deliveries where task_id = '$KAF_TASK_ID'" \
  >"$CLOSEOUT_EVIDENCE_DIR/completion-payload.json"
jq -e '.action == "complete_bpmn_task" and .execution.idempotencyKey != ""' \
  "$CLOSEOUT_EVIDENCE_DIR/completion-payload.json" >/dev/null

curl -fsS -X POST \
  -H "Authorization: Bearer $ITSM_KAF_AUTOMATION_TOKEN" \
  -H 'Content-Type: application/json' \
  "$ITSM_BASE_URL/api/v1/bpmn/process-tasks/$KAF_TASK_ID/actions" \
  --data-binary @"$CLOSEOUT_EVIDENCE_DIR/completion-payload.json" \
  >"$CLOSEOUT_EVIDENCE_DIR/replay-response.json"
jq -e '.data.resultStatus == "already_applied"' "$CLOSEOUT_EVIDENCE_DIR/replay-response.json"

graph_membership | tee "$CLOSEOUT_EVIDENCE_DIR/graph-after-replay.txt"
grep -q 'member=true' "$CLOSEOUT_EVIDENCE_DIR/graph-after-replay.txt"
```

Re-run Step 9’s count queries and compare them byte-for-byte after excluding the allowed context-read count:

```bash
test "$(psql "$ITSM_DATABASE_URL" -Atc "select count(*) from kaf_task_action_ledgers where task_id='$KAF_TASK_ID' and action='complete_bpmn_task'")" = 1
test "$(psql "$ITSM_DATABASE_URL" -Atc "select count(*) from kaf_task_completion_receipts where task_id='$KAF_TASK_ID'")" = 1
test "$(psql "$ITSM_DATABASE_URL" -Atc "select count(*) from audit_logs where action='kaf_delegate.complete_bpmn_task' and request_body like '%$KAF_TASK_ID%'")" = 1
test "$(docker exec kaf-dev-postgres psql -U ai01 -d control_plane -Atc "select count(*) from external_actions where tool_name='ad_grant_vpn_access' and params->>'user_identifier'='$TARGET_UPN' and created_at >= (select min(received_at) from kaf_delegation_deliveries where task_id='$KAF_TASK_ID')")" = 1
```

Expected: `already_applied`; one Graph grant external action and one ITSM business-effect set remain. The replay itself must not call the Procedure or Tool.

- [x] **Step 11: Restore Julian and clear the trap only after read-only confirmation**

```bash
cleanup_membership
test "$GRAPH_MUTATION_ARMED" = 0
grep -q 'member=false' "$CLOSEOUT_EVIDENCE_DIR/graph-after-cleanup.txt"
TZ=Asia/Shanghai date --iso-8601=seconds >"$CLOSEOUT_EVIDENCE_DIR/execution-end.txt"
trap - EXIT INT TERM
```

Expected: final Graph state is non-member. If the Tool or read-only confirmation fails, leave the trap armed, stop all further real-change work, and record the exact user/group object IDs for manual recovery; do not claim closeout success.

### Task 8: Run Deterministic Breakers, Real PostgreSQL RLS, and Publish the Closeout Addendum

**Files:**

- Verify: `itsm-backend/service/kaf_delegation_service_test.go`
- Verify: `itsm-backend/controller/kaf_delegation_controller_test.go`
- Verify: `tests/test_kaf_delegation_pipeline.py`
- Modify: `docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md`
- Verify: `docs/testing/kaf-delegation-release-closeout-fixture.md`

**Interfaces:**

- Consumes: Task 6 test logs and commits; Task 7 sanitized IDs, booleans, counts, replay response, and final cleanup state.
- Produces: deterministic callback/lease/auth recovery evidence, a zero-skip real PostgreSQL RLS result, and one `Live Dev Closeout Addendum` consistent with command output and persistent records.

- [x] **Step 1: Run the deterministic ITSM callback, idempotency, auth, tenant, and attachment breakers**

```bash
set -euo pipefail
export CLOSEOUT_EVIDENCE_DIR="$(cat /tmp/kaf-delegation-closeout.current)"
mapfile -t CLOSEOUT_ENV_FILES <"$CLOSEOUT_EVIDENCE_DIR/env-files"
set -a
. "${CLOSEOUT_ENV_FILES[1]}"
. "${CLOSEOUT_ENV_FILES[0]}"
set +a
export TARGET_UPN='Julian@dawnpro.onmicrosoft.com'
export EXPECTED_GROUP_ID='b7c7f066-3042-4a11-9e36-2ea80b979ae3'
graph_membership() {
  cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
  PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python - <<'PY'
import asyncio, os
from acp.graph.client import get_graph_client
from acp.graph.users import graph_identity_lookup

async def main():
    client = get_graph_client()
    user = await graph_identity_lookup(client, os.environ["TARGET_UPN"])
    assert user is not None
    assert os.environ["VPN_USERS_GROUP_ID"] == os.environ["EXPECTED_GROUP_ID"]
    result = await client.post(
        f'/users/{user["_graph_id"]}/checkMemberGroups',
        {"groupIds": [os.environ["EXPECTED_GROUP_ID"]]},
    )
    member = os.environ["EXPECTED_GROUP_ID"].lower() in {
        str(value).lower() for value in result.get("value", [])
    }
    print(f'user_object_id={user["_graph_id"]} group_object_id={os.environ["EXPECTED_GROUP_ID"]} member={str(member).lower()}')

asyncio.run(main())
PY
}
cd /home/administrator/project/itsm/itsm-backend
go test ./service -run 'TestExecuteAction_RealEngineCallbackFailureRecoversWithoutSecondBPMNCompletion' -count=1 -v
go test ./controller -run 'TestKaf(Context_Rejects|Context_ReturnsOnlyOpaque|Action_CompleteIsIdempotent|Action_Rejects|DelegatedList)' -count=1 -v
go test ./handlers/service_request -run TestServiceRequestKafDelegationSSLVPN -count=1 -v
```

Expected: PASS; callback recovery does not perform a second BPMN completion, exact action replay is idempotent, and tenant/attachment boundaries fail closed.

- [x] **Step 2: Run deterministic KAF lease/recovery/replay breakers**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest \
  tests/test_kaf_delegation_pipeline.py::test_webhook_and_recovery_interleaving_keeps_one_active_execution \
  tests/test_kaf_delegation_pipeline.py::test_sqlite_concurrent_claim_has_one_authoritative_owner \
  tests/test_kaf_delegation_pipeline.py::test_sqlite_duplicate_webhooks_commit_one_unique_identity_and_one_execution \
  tests/test_kaf_delegation_pipeline.py::test_sqlite_stale_owner_cannot_finalize_after_lease_theft \
  tests/test_kaf_delegation_pipeline.py::test_sqlite_lease_theft_fences_remote_action \
  tests/test_kaf_delegation_pipeline.py::test_sqlite_remote_applied_lease_stolen_crash_replays_without_delegated_listing \
  tests/test_kaf_delegation_pipeline.py::test_sqlite_pending_completion_replay_never_reexecutes_listed_procedure -q
```

Expected: PASS; the last test proves `procedure_calls == 1`, and all post-payload recovery is replay-only.

- [x] **Step 3: Run the real PostgreSQL RLS probe and reject skips**

```bash
cd /home/administrator/project/itsm/itsm-backend
RLS_OUTPUT="$(RLS_TEST_DSN="$RLS_TEST_DSN" go test -tags integration_rls ./database/rls -count=1 -v 2>&1)"
printf '%s\n' "$RLS_OUTPUT" | tee "$CLOSEOUT_EVIDENCE_DIR/rls-test.log"
grep -q -- '--- PASS:' "$CLOSEOUT_EVIDENCE_DIR/rls-test.log"
if grep -Eq -- '--- SKIP:|no tests to run|SKIP' "$CLOSEOUT_EVIDENCE_DIR/rls-test.log"; then
  echo 'RLS probe skipped; closeout fails' >&2
  exit 1
fi
```

Expected: exit zero, at least one PASS, and zero skip markers. SQLite or deterministic SQL is not acceptable evidence.

- [x] **Step 4: Reconfirm final external state and repository cleanliness before writing the report**

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
graph_membership | tee "$CLOSEOUT_EVIDENCE_DIR/graph-final.txt"
grep -q 'member=false' "$CLOSEOUT_EVIDENCE_DIR/graph-final.txt"
git status --short

cd /home/administrator/project/itsm
git status --short
git diff --check
```

Expected: `member=false`; KAF contains only intended committed changes; ITSM may still show the protected untracked `docs/implementation/` and the report edit that follows, with no unrelated files.

- [x] **Step 5: Append the Live Dev Closeout Addendum from sanitized evidence**

Use `apply_patch` to append these four headings to the existing report: `## Live Dev Closeout Addendum — 2026-08-31`, `### Environment and revisions`, `### Automated verification`, `### Live Service Request and replay evidence`, and `### Breakers and residual status`.

Populate every value with a literal observed fact from the named source below; do not leave angle brackets, ellipses, empty cells, or prose promises in the report:

| Report field | Authoritative source | Required representation |
|---|---|---|
| Execution window | shell timestamps captured immediately before Task 7 Step 2 and after Step 11 | Asia/Shanghai ISO-8601 start and end |
| ITSM/KAF commits | Task 6 Step 8 `git rev-parse HEAD` | complete 40-character hashes |
| Migration/runtime | Task 6 Steps 5 and 8 | `036_kaf_completion_replay`, `127.0.0.1:8090`, `127.0.0.1:8001` |
| ITSM focused/full/build | Task 6 Step 2 output | command scopes, exit codes, exact test counts/durations |
| KAF focused/full | Task 6 Steps 3 and 4 | command scopes, exit codes, exact passed/failed/error/skipped/xfailed counts |
| PostgreSQL RLS | `$CLOSEOUT_EVIDENCE_DIR/rls-test.log` | exact pass count and `0 skips` |
| WorkItem/process/task | `itsm-work-item.txt`, `itsm-process.txt`, `KAF_TASK_ID` | sanitized numeric/string IDs |
| Outbox/delivery/ledger/receipt | Task 7 Step 9 query rows | IDs when queried and terminal states; cardinality `1` for each authoritative record |
| Membership lifecycle | `graph-before.txt`, `graph-after-grant.txt`, `graph-after-replay.txt`, `graph-after-cleanup.txt`, `graph-final.txt` | `false → true → true → false → false` |
| Exact replay | `replay-response.json` and repeated counts | `already_applied`; ledger/receipt/completion-audit/Graph external-action counts remain `1`; Graph audit rows remain sanitized |
| Breakers | Task 7 Step 7 and Task 8 Steps 1–3 | HTTP results, opaque attachment result, exact deterministic test results, zero-skip RLS |
| Verdict | all design §9 completion criteria | literal `PASS` only if all succeeded and final membership is false; otherwise literal `FAIL` followed by the observed blocker |

If the repository-wide KAF suite remains nonzero, list its exact counts and classify each modified-scope failure against the passing focused suite; do not call the suite green. Do not include token values, environment-file paths, raw Tool payloads, raw intake, attachment storage data, or unredacted exceptions. IDs and membership booleans are allowed.

- [x] **Step 6: Validate the report against evidence and the design completion criteria**

```bash
cd /home/administrator/project/itsm
rg -n 'Live Dev Closeout Addendum|036_kaf_completion_replay|member=false|member=true|already_applied|PostgreSQL RLS|Closeout verdict' \
  docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md
git diff --unified=0 -- docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md \
  | rg '^\+' >"$CLOSEOUT_EVIDENCE_DIR/report-added-lines.txt"
if rg -ni 'Authorization: Bearer [A-Za-z0-9._-]+|(client_secret|access_token|webhook_secret)["=: ]+[A-Za-z0-9._-]+|signed-secret|fileUrl|filePath' \
  "$CLOSEOUT_EVIDENCE_DIR/report-added-lines.txt"; then
  echo 'report contains forbidden secret-bearing vocabulary' >&2
  exit 1
fi
git diff --check
```

Manually cross-check each reported count against `$CLOSEOUT_EVIDENCE_DIR`, confirm the final Graph file says `member=false`, and confirm no completion criterion is marked PASS from a skipped or nonzero command.

- [x] **Step 7: Run final verification and commit the closeout documentation**

```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./controller ./service ./handlers/service_request ./tests/fixtures -count=1
cd /home/administrator/project/itsm
git diff --check
git add \
  docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md \
  docs/superpowers/plans/2026-08-31-kaf-delegation-release-closeout.md \
  docs/superpowers/specs/2026-08-31-kaf-delegation-release-closeout-design.md
git diff --cached --check
git status --short
git commit -m "docs(kaf): record live delegation closeout"
```

Expected: focused verification exits zero; the staged set contains only the
closeout report and its design/plan status updates; protected historical review
files remain untracked and unstaged.

Completion evidence: the real path used SR 35 / WorkItem 18 / process 144 and
completed with one Outbox, KAF delivery, ledger, receipt, completion audit, and
Graph external action. Exact-payload replay returned `already_applied`; final
Graph membership is false. Task 8 breakers passed (ITSM 19, KAF 7), real
PostgreSQL RLS passed 15 tests with zero skips after TDD fixes `821388ef` and
`fa577192`, independent review approved the final fix, ITSM full tests/build and
the 139-test KAF delegation scope passed, and the Live Dev Addendum records a
Dev `PASS`. The design, plan, and report are committed together so their status
does not contradict the evidence; protected historical review files remain
untracked and unstaged.

## Execution Gate

Do not begin a separate unified Intake brainstorm until Task 8 records either a supported `PASS` or an explicit `FAIL`/blocker and Task 7 proves final Graph membership is false. A Dev `PASS` is a stable-baseline result, not approval for KAF PROD or a production deployment.
