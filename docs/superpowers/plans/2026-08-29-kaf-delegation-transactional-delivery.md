# KAF 委派事务性投递与任务 API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 BPMN `kaf_delegate` 在同一 ITSM 数据库事务中创建委派任务、审计和 Outbox，通过可重试的签名 webhook 通知 KAF，并让 KAF 以任务范围 API 安全地补拉上下文和完成委派任务。

**Architecture:** ITSM 使用持久化 `OutboxEvent` 作为唯一的交付记录；`createDelegatedTask` 不再分别写任务和流程状态，而是通过一个事务应用服务原子写入 `ProcessTask`、`AuditLog` 和 `KafDelegateRequested`。后台 dispatcher 使用配置的 KAF webhook URL 将最小事件以 HMAC-SHA256 发送给 KAF；KAF webhook 按 `eventId` 去重，启动独立的 headless delegation pipeline，并通过 ITSM 的 task-scoped API 获取上下文。传输失败只更新 Outbox 重试状态，KAF 也可按 `delegated` 状态补拉，不建立第二套任务表或轮询旧工单正文。

**Tech Stack:** Go, Gin, Ent, PostgreSQL/SQLite enttest, `net/http`, HMAC-SHA256; KAF FastAPI, SQLAlchemy/Alembic, httpx, pytest.

**Spec:** [KAF-ITSM 自主 WorkItem 委派设计](../specs/2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md)

## Global Constraints

- 已实现的 `AsyncServiceTaskHandler`、`kaf_delegate` 暂停/恢复和 `CompleteTask` 人工任务路径必须保持行为不变；不得 fork BPMN 引擎或新增第二个自动化任务实体。
- `ProcessTask`、显式 `AuditLog` 与 `OutboxEvent` 必须由同一 Ent transaction 写入；HTTP 调用绝不能出现在数据库事务内。
- KAF 只可通过 `taskId` 操作 `taskType="kaf_delegate"` 且状态为 `delegated` 的任务；认证主体必须是同租户 `kaf_automation` 用户，不能使用通用 WorkItem 写接口。
- 新 KAF webhook 以 HMAC-SHA256 服务间签名认证；不能复用当前 `require_admin` 的交互式管理员 JWT 依赖。
- 事件只含 `eventId`、tenant/work item/task 标识、`recordClass`、时间、版本和 `correlationId`，不含完整 WorkItem 正文、Prompt、Tool 输出或凭据。
- `OutboxEvent.event_id`、KAF 侧已接收 `event_id`、以及 `(tenantId, taskId, runId, stepId)` 动作键必须唯一；重复投递返回成功且不重复执行业务副作用。
- 首期动作 API 只实现 `complete_bpmn_task`、`update_progress` 与 `record_execution_failure`；`assign`、`resolve`、`close` 由后续 typed-action 计划处理，不能在本计划中绕过专业领域服务。
- 每个 Go 任务完成后运行相关 `go test` 和 `go build ./...`；每个 KAF 任务完成后运行指定 pytest；最后运行跨系统合同测试和 SSLVPN sandbox E2E。

---

## File Structure

| 文件 | 职责 |
|---|---|
| `itsm-backend/ent/schema/outbox_event.go` | 租户隔离的可靠事件、投递状态、重试调度与唯一键。 |
| `itsm-backend/service/kaf_delegation_service.go` | 原子创建委派、任务范围授权、上下文读取、首期 typed actions 与显式审计。 |
| `itsm-backend/service/kaf_outbox_dispatcher.go` | 查询待投递事件、签名 HTTP 投递、指数退避与状态写回。 |
| `itsm-backend/controller/kaf_delegation_controller.go` | 薄 HTTP 绑定层，仅派生认证上下文并调用委派服务。 |
| `itsm-backend/router/router.go` | 在 tenant 路由组注册 KAF task API，初始化 dispatcher 生命周期。 |
| `itsm-backend/config/config.go` | `KAF_WEBHOOK_URL`、`KAF_WEBHOOK_SECRET`、dispatcher 批大小/轮询间隔配置。 |
| `itsm-backend/service/*_test.go` | 内存 Ent 事务、授权、幂等、投递重试和 HTTP 契约测试。 |
| `kaf-main/src/acp/routers/itsm_webhooks.py` | 验证服务签名并按事件类型分发，不能把委派事件误当成旧 `approved` 生命周期事件。 |
| `kaf-main/src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py` | 无会话轮次依赖的 KAF 委派入口：去重、读取 ITSM context、创建执行上下文。 |
| `kaf-main/src/acp/models/kaf_delegation_delivery.py` | KAF 已接收事件账本，按 `event_id` 去重。 |
| `kaf-main/alembic/versions/*_kaf_delegation_deliveries.py` | KAF 接收账本迁移。 |
| `kaf-main/tests/test_itsm_webhook_auth.py`、`kaf-main/tests/test_kaf_delegation_pipeline.py` | 签名、去重、错误不确认和 context 补拉测试。 |

---

### Task 1: 锁定跨系统事件与服务认证合同

**Files:**
- Create: `docs/contracts/kaf-delegate-requested.openapi.yaml`
- Create: `itsm-backend/service/kaf_webhook_contract_test.go`
- Modify: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/src/acp/routers/itsm_webhooks.py`
- Test: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/tests/test_itsm_webhook_auth.py`

**Interfaces:**
- Produces: `KafDelegateRequested` HTTP body and headers `X-Webhook-Signature: sha256=<hex>` and `X-Event-ID: <uuid>`.
- Produces: KAF `POST /webhooks/itsm` branch `event_type == "kaf_delegate_requested"`, authenticated by a valid shared-secret signature without `require_admin`.
- Consumes: configured `KAF_WEBHOOK_SECRET`; all event fields listed in the spec §4.2.

- [ ] **Step 1: 写 ITSM 侧序列化与签名失败测试**

在 `itsm-backend/service/kaf_webhook_contract_test.go` 写入最小事件和断言：

```go
func TestSignKafDelegateRequest_ProducesStableHMACAndMinimalPayload(t *testing.T) {
    event := KafDelegateRequested{
        EventID: "evt-001", TenantID: 7, WorkItemID: "42", TicketID: "42",
        TaskID: "TASK-42", RecordClass: "service_request_item",
        Timestamp: "2026-08-29T12:00:00Z", Version: 3, CorrelationID: "corr-42",
    }
    body, signature, err := SignKafDelegateRequest(event, "test-secret")
    require.NoError(t, err)
    assert.JSONEq(t, `{"eventId":"evt-001","tenantId":7,"workItemId":"42","ticketId":"42","taskId":"TASK-42","recordClass":"service_request_item","timestamp":"2026-08-29T12:00:00Z","version":3,"correlationId":"corr-42"}`, string(body))
    assert.Equal(t, "sha256="+expectedHMAC(body, "test-secret"), signature)
    assert.NotContains(t, string(body), "description")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service -run TestSignKafDelegateRequest_ProducesStableHMACAndMinimalPayload -v`

Expected: FAIL，因为 `KafDelegateRequested` 与 `SignKafDelegateRequest` 尚未定义。

- [ ] **Step 3: 写 OpenAPI 合同并实现签名值对象**

在 `docs/contracts/kaf-delegate-requested.openapi.yaml` 定义 `POST /webhooks/itsm`，请求体使用下列字段和 `event_type` 常量；`eventId` 是 UUID，`tenantId` 为正整数，`recordClass` 仅允许 `service_request_item` 或 `incident`：

```yaml
KafDelegateRequested:
  type: object
  required: [event_type, eventId, tenantId, workItemId, ticketId, taskId, recordClass, timestamp, version, correlationId]
  properties:
    event_type: { type: string, enum: [kaf_delegate_requested] }
    eventId: { type: string, format: uuid }
    tenantId: { type: integer, minimum: 1 }
    workItemId: { type: string }
    ticketId: { type: string }
    taskId: { type: string }
    recordClass: { type: string, enum: [service_request_item, incident] }
    timestamp: { type: string, format: date-time }
    version: { type: integer, minimum: 1 }
    correlationId: { type: string, minLength: 1 }
```

在 `itsm-backend/service/kaf_outbox_dispatcher.go` 定义 exported DTO 和纯函数；使用 `json.Marshal` 的结构体字段顺序，而非 map，确保签名主体稳定：

```go
type KafDelegateRequested struct {
    EventType     string `json:"event_type"`
    EventID       string `json:"eventId"`
    TenantID      int    `json:"tenantId"`
    WorkItemID    string `json:"workItemId"`
    TicketID      string `json:"ticketId"`
    TaskID        string `json:"taskId"`
    RecordClass   string `json:"recordClass"`
    Timestamp     string `json:"timestamp"`
    Version       int    `json:"version"`
    CorrelationID string `json:"correlationId"`
}

func SignKafDelegateRequest(event KafDelegateRequested, secret string) ([]byte, string, error) {
    body, err := json.Marshal(event)
    if err != nil { return nil, "", err }
    mac := hmac.New(sha256.New, []byte(secret))
    _, _ = mac.Write(body)
    return body, "sha256=" + hex.EncodeToString(mac.Sum(nil)), nil
}
```

- [ ] **Step 4: 写 KAF webhook 分发失败测试**

在 `kaf-main/tests/test_itsm_webhook_auth.py` 添加：

```python
async def test_kaf_delegate_event_requires_valid_hmac_and_does_not_require_admin(client, monkeypatch):
    monkeypatch.setattr(settings, "itsm_webhook_secret", "test-secret")
    body = delegate_event_body(event_id="evt-001")
    response = await client.post("/webhooks/itsm", content=json.dumps(body), headers={
        "X-Webhook-Signature": sign(body, "test-secret"),
        "X-Event-ID": "evt-001",
    })
    assert response.status_code == 202
    assert response.json() == {"status": "accepted", "ticket_id": "42"}
```

- [ ] **Step 5: 实现 KAF webhook 的鉴权和分发边界**

在 `itsm_webhooks.py` 将路由拆成两条明确依赖：旧 lifecycle event 保持 `_verify_webhook_signature + require_admin`，新 `kaf_delegate_requested` 仅使用 `_verify_webhook_signature`。先解析并验证 `event_type`，再选择依赖，禁止“缺失 event_type 默认 approved”应用到新事件。新分支调用 `KafDelegationPipeline.accept(event)`，并仅在它持久化接收账本后返回 HTTP 202。

- [ ] **Step 6: 运行合同测试并提交**

Run:

```bash
cd itsm-backend && go test ./service -run 'TestSignKafDelegateRequest' -v
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main && pytest tests/test_itsm_webhook_auth.py -q
git -C /home/administrator/project/itsm add docs/contracts/kaf-delegate-requested.openapi.yaml itsm-backend/service/kaf_outbox_dispatcher.go itsm-backend/service/kaf_webhook_contract_test.go
git -C /home/administrator/project/itsm commit -m "docs: define KAF delegation webhook contract"
git -C /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main add src/acp/routers/itsm_webhooks.py tests/test_itsm_webhook_auth.py
git -C /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main commit -m "feat: accept signed KAF delegation events"
```

### Task 2: 增加 tenant-scoped OutboxEvent 持久化与事务仓储

**Files:**
- Create: `itsm-backend/ent/schema/outbox_event.go`
- Modify: `itsm-backend/ent/` generated files via `go generate ./ent`
- Create: `itsm-backend/service/outbox_event_repository.go`
- Test: `itsm-backend/service/outbox_event_repository_test.go`

**Interfaces:**
- Produces: `OutboxEvent` fields `eventID`, `eventType`, `tenantID`, `aggregateType`, `aggregateID`, `payload`, `status`, `attemptCount`, `nextAttemptAt`, `publishedAt`, `lastError`, timestamps.
- Produces: `OutboxEventRepository.Enqueue(ctx, tx, NewOutboxEvent) (*ent.OutboxEvent, error)` and `ClaimDue(ctx, now, limit) ([]*ent.OutboxEvent, error)`.
- Consumes: Task 1 serialized event payload; `eventID` is immutable and unique.

- [ ] **Step 1: 写 schema 与重复事件失败测试**

```go
func TestOutboxEventRepository_EnqueueDeduplicatesEventID(t *testing.T) {
    repo, client := newOutboxRepository(t)
    tx, err := client.Tx(context.Background())
    require.NoError(t, err)
    first, err := repo.Enqueue(context.Background(), tx, NewOutboxEvent{EventID: "evt-1", EventType: "kaf_delegate_requested", TenantID: 1, AggregateType: "process_task", AggregateID: "TASK-1", Payload: json.RawMessage(`{"eventId":"evt-1"}`)})
    require.NoError(t, err)
    require.NoError(t, tx.Commit())
    assert.Equal(t, "pending", first.Status)
    _, err = repo.Enqueue(context.Background(), nil, NewOutboxEvent{EventID: "evt-1", EventType: "kaf_delegate_requested", TenantID: 1, AggregateType: "process_task", AggregateID: "TASK-1", Payload: json.RawMessage(`{"eventId":"evt-1"}`)})
    require.ErrorIs(t, err, ErrDuplicateOutboxEvent)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service -run TestOutboxEventRepository_EnqueueDeduplicatesEventID -v`

Expected: FAIL，因为 schema、repository 和 `ErrDuplicateOutboxEvent` 尚不存在。

- [ ] **Step 3: 实现 Ent schema 与 repository**

创建 schema，使用以下不可为空字段和值域：

```go
field.String("event_id").Unique().NotEmpty(),
field.String("event_type").NotEmpty(),
field.Int("tenant_id").Positive(),
field.String("aggregate_type").NotEmpty(),
field.String("aggregate_id").NotEmpty(),
field.JSON("payload", json.RawMessage{}),
field.String("status").Default("pending"), // pending, publishing, published
field.Int("attempt_count").Default(0),
field.Time("next_attempt_at").Default(time.Now),
field.Time("published_at").Optional(),
field.Text("last_error").Optional(),
```

添加 `(tenant_id, status, next_attempt_at)` 索引和 `event_id` 唯一索引。`ClaimDue` 必须在一个 transaction 中把最多 `limit` 条 `pending` 记录条件更新为 `publishing`，条件包含原 `status="pending"` 和 `next_attempt_at <= now`；条件更新 0 行时不得返回该记录，防止多个 dispatcher 重复发送。

- [ ] **Step 4: 增加并发 claim 与重试状态测试**

```go
func TestOutboxEventRepository_ClaimDueOnlyClaimsOnceAndSchedulesRetry(t *testing.T) {
    repo, _ := newOutboxRepository(t)
    seedPendingEvent(t, repo, "evt-2")
    first, err := repo.ClaimDue(context.Background(), time.Now(), 10)
    require.NoError(t, err)
    second, err := repo.ClaimDue(context.Background(), time.Now(), 10)
    require.NoError(t, err)
    require.Len(t, first, 1)
    assert.Empty(t, second)
    require.NoError(t, repo.MarkRetry(context.Background(), first[0].ID, "timeout", time.Now().Add(time.Minute)))
    assertEventState(t, repo, "evt-2", "pending", 1)
}
```

- [ ] **Step 5: 运行测试并提交**

Run:

```bash
cd itsm-backend && go generate ./ent
cd itsm-backend && go test ./service -run 'TestOutboxEventRepository_' -v
cd itsm-backend && go build ./...
git add itsm-backend/ent/schema/outbox_event.go itsm-backend/ent/ itsm-backend/service/outbox_event_repository.go itsm-backend/service/outbox_event_repository_test.go
git commit -m "feat(outbox): persist tenant-scoped delegation events"
```

### Task 3: 原子创建委派任务、审计与 Outbox

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Create: `itsm-backend/service/kaf_delegation_service.go`
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`
- Test: `itsm-backend/service/kaf_delegation_service_test.go`

**Interfaces:**
- Produces: `KafDelegationService.CreateDelegatedTask(ctx, instanceID, serviceTask) (*ent.ProcessTask, error)`.
- Produces: one `ProcessTask` with `TaskType == bpmn.KafDelegateTaskType`, `Status == delegated`, nonempty `CorrelationID`; one matching `AuditLog`; one pending `OutboxEvent`.
- Consumes: `BPMNServiceTask.AllowedActions()`, process instance tenant/business identity, Task 2 repository.

- [ ] **Step 1: 写事务回滚失败测试**

```go
func TestCreateDelegatedTask_RollsBackTaskAndAuditWhenOutboxInsertFails(t *testing.T) {
    engine, svc, ctx, instance := newDelegationFixture(t)
    svc.outbox = failingOutboxRepository{err: errors.New("outbox unavailable")}
    err := engine.HandleServiceTask(ctx, instance, kafDelegateTask("resolve"))
    require.ErrorContains(t, err, "outbox unavailable")
    assert.Zero(t, countProcessTasks(t, svc.client, instance.ID))
    assert.Zero(t, countAuditLogs(t, svc.client, instance.TenantID, "kaf_delegate.created"))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service -run TestCreateDelegatedTask_RollsBackTaskAndAuditWhenOutboxInsertFails -v`

Expected: FAIL，因为当前 `createDelegatedTask` 分别写入数据且没有 Outbox。

- [ ] **Step 3: 在 KafDelegationService 中实现唯一的事务写入口**

将 `CustomProcessEngine.createDelegatedTask` 改为只收集 BPMN 节点元数据并调用 `KafDelegationService`。该 service 开启 Ent transaction，按顺序执行：

```go
task, err := tx.ProcessTask.Create().
    SetTaskID(newTaskID()).SetProcessInstanceID(instance.ID).
    SetProcessDefinitionKey(instance.ProcessDefinitionKey).
    SetTaskDefinitionKey(serviceTask.ID).SetTaskName(serviceTask.Name).
    SetTaskType(bpmn.KafDelegateTaskType).SetStatus(common.ProcessTaskStatusDelegated).
    SetTaskVariables(map[string]interface{}{"allowed_actions": serviceTask.AllowedActions()}).
    SetCorrelationID(newCorrelationID()).SetTenantID(instance.TenantID).Save(ctx)
if err != nil { return nil, err }
if err := tx.AuditLog.Create().SetTenantID(instance.TenantID).
    SetUserID(actorIDFromContext(ctx)).SetResource("process_task").
    SetAction("kaf_delegate.created").SetPath("bpmn/kaf_delegate").
    SetMethod("SYSTEM").SetStatusCode(http.StatusCreated).
    SetRequestBody(fmt.Sprintf(`{"taskId":%q,"correlationId":%q}`, task.TaskID, task.CorrelationID)).Save(ctx); err != nil {
    return nil, err
}
event := newKafDelegateOutboxEvent(task, instance, task.CorrelationID)
if _, err := s.outbox.Enqueue(ctx, tx, event); err != nil { return nil, err }
```

任何一步失败都 `Rollback`，成功后才 `Commit`。不要在 transaction 内调用 dispatcher、KAF HTTP 或领域动作；保持流程实例 `CurrentActivityID` 在委派节点，只有 `CompleteTask` 成功后才推进。

- [ ] **Step 4: 写成功路径的完整性测试**

```go
func TestCreateDelegatedTask_CommitsTaskAuditAndOutboxWithSameCorrelationID(t *testing.T) {
    engine, svc, ctx, instance := newDelegationFixture(t)
    require.NoError(t, engine.HandleServiceTask(ctx, instance, kafDelegateTask("complete_bpmn_task,update_progress")))
    task := onlyDelegatedTask(t, svc.client, instance.ID)
    assert.NotEmpty(t, task.CorrelationID)
    assertAuditExists(t, svc.client, instance.TenantID, "process_task", "kaf_delegate.created", task.CorrelationID)
    event := onlyOutboxEvent(t, svc.client, task.TaskID)
    assert.Equal(t, task.CorrelationID, eventCorrelationID(t, event.Payload))
    assert.Equal(t, "pending", event.Status)
}
```

- [ ] **Step 5: 运行 BPMN 回归并提交**

Run:

```bash
cd itsm-backend && go test ./service -run 'Test(CreateDelegatedTask_|HandleElement_AsyncServiceTask|CompleteTask_ResumesDelegatedTask)' -v
cd itsm-backend && go test ./service/... -run 'Test.*UserTask' -v
cd itsm-backend && go build ./...
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/kaf_delegation_service.go itsm-backend/service/bpmn_process_engine_ext_test.go itsm-backend/service/kaf_delegation_service_test.go
git commit -m "feat(bpmn): atomically persist KAF delegation events"
```

### Task 4: 实现可重试 Outbox dispatcher 与运行时配置

**Files:**
- Modify: `itsm-backend/config/config.go`
- Modify: `itsm-backend/router/router.go`
- Modify: `itsm-backend/service/kaf_outbox_dispatcher.go`
- Create: `itsm-backend/service/kaf_outbox_dispatcher_test.go`

**Interfaces:**
- Produces: `KafOutboxDispatcher.Run(ctx context.Context)` and `DispatchOnce(ctx context.Context) error`.
- Consumes: `OutboxEventRepository`, `KAF_WEBHOOK_URL`, `KAF_WEBHOOK_SECRET`, `KAF_OUTBOX_BATCH_SIZE`, `KAF_OUTBOX_POLL_INTERVAL`.
- Produces: `published` on any 2xx response; retryable network/5xx errors become pending at `now + min(2^attemptCount seconds, 5 minutes)`; 4xx becomes `pending` with the same backoff and an error audit log for operator investigation.

- [ ] **Step 1: 写 HTTP 成功与超时重试失败测试**

```go
func TestKafOutboxDispatcher_DispatchesSignedEventAndMarksPublished(t *testing.T) {
    repo, dispatcher, server := newDispatcherFixture(t)
    defer server.Close()
    seedPendingDelegateEvent(t, repo, "evt-dispatch-1")
    require.NoError(t, dispatcher.DispatchOnce(context.Background()))
    assert.Equal(t, "published", eventByID(t, repo, "evt-dispatch-1").Status)
    assert.Equal(t, "sha256="+expectedHMAC(server.LastBody(), "test-secret"), server.LastHeader("X-Webhook-Signature"))
}

func TestKafOutboxDispatcher_SchedulesRetryAfterTransportFailure(t *testing.T) {
    repo, dispatcher := newFailingDispatcherFixture(t)
    seedPendingDelegateEvent(t, repo, "evt-dispatch-2")
    require.NoError(t, dispatcher.DispatchOnce(context.Background()))
    event := eventByID(t, repo, "evt-dispatch-2")
    assert.Equal(t, "pending", event.Status)
    assert.Equal(t, 1, event.AttemptCount)
    assert.True(t, event.NextAttemptAt.After(time.Now()))
    assert.Contains(t, event.LastError, "connection refused")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./service -run TestKafOutboxDispatcher_ -v`

Expected: FAIL，因为 dispatcher 和配置不存在。

- [ ] **Step 3: 实现 dispatcher 和配置校验**

`config.go` 增加以下环境读取：

```go
KAFWebhookURL string        // empty disables dispatcher; startup logs one warning
KAFWebhookSecret string     // required when URL is set; startup fails if absent
KAFOutboxBatchSize int      // default 20, valid 1..100
KAFOutboxPollInterval time.Duration // default 5s, valid >= 1s
```

使用 `http.NewRequestWithContext`、10 秒 client timeout、`Content-Type: application/json`、Task 1 定义的签名头和 `X-Event-ID`。`router/router.go` 或已有 application bootstrap 只启动一个 dispatcher goroutine，并在 application context 取消时等待它退出；测试环境不启动后台 goroutine，直接调用 `DispatchOnce`。

- [ ] **Step 4: 增加未配置和 4xx 的测试**

```go
func TestKafOutboxDispatcher_RejectsURLWithoutSecret(t *testing.T) {
    _, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{WebhookURL: "http://kaf"})
    require.ErrorContains(t, err, "KAF_WEBHOOK_SECRET")
}

func TestKafOutboxDispatcher_RetriesNon2xxWithoutDroppingEvent(t *testing.T) {
    repo, dispatcher, server := newStatusDispatcherFixture(t, http.StatusUnauthorized, `{"detail":"invalid_webhook_signature"}`)
    defer server.Close()
    seedPendingDelegateEvent(t, repo, "evt-dispatch-3")
    require.NoError(t, dispatcher.DispatchOnce(context.Background()))
    event := eventByID(t, repo, "evt-dispatch-3")
    assert.Equal(t, "pending", event.Status)
    assert.Equal(t, 1, event.AttemptCount)
    assert.Contains(t, event.LastError, "401")
    assert.Contains(t, event.LastError, "invalid_webhook_signature")
}
```

- [ ] **Step 5: 运行测试并提交**

Run:

```bash
cd itsm-backend && go test ./service -run 'TestKafOutboxDispatcher_|TestKafOutboxConfig' -v
cd itsm-backend && go build ./...
git add itsm-backend/config/config.go itsm-backend/router/router.go itsm-backend/service/kaf_outbox_dispatcher.go itsm-backend/service/kaf_outbox_dispatcher_test.go
git commit -m "feat(outbox): dispatch KAF delegation webhooks"
```

### Task 5: 实现 KAF 接收账本和 headless delegation pipeline

**Files:**
- Create: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/src/acp/models/kaf_delegation_delivery.py`
- Create: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/alembic/versions/021_kaf_delegation_deliveries.py`
- Create: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py`
- Modify: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/src/acp/routers/itsm_webhooks.py`
- Test: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/tests/test_kaf_delegation_pipeline.py`

**Interfaces:**
- Produces: `KafDelegationDelivery(event_id, task_id, tenant_id, status, received_at, started_at, last_error)` with unique `event_id`.
- Produces: `KafDelegationPipeline.accept(event: KafDelegateRequested) -> None`; duplicate events do not enqueue a second execution.
- Consumes: ITSM `GET /bpmn/process-tasks/{taskId}/kaf-context` from Task 6 and the KAF `kaf_automation` principal configuration.

- [ ] **Step 1: 写 KAF 去重失败测试**

```python
async def test_accept_persists_once_and_enqueues_one_headless_run(session, monkeypatch):
    pipeline = KafDelegationPipeline(session=session, itsm_client=FakeItsmClient())
    event = delegate_event(event_id="evt-100", task_id="TASK-100")
    await pipeline.accept(event)
    await pipeline.accept(event)
    assert await count_deliveries(session, "evt-100") == 1
    assert pipeline.enqueued_task_ids == ["TASK-100"]
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main && pytest tests/test_kaf_delegation_pipeline.py -q`

Expected: FAIL，因为 delivery model 和 pipeline 尚不存在。

- [ ] **Step 3: 创建账本和 pipeline**

账本成功插入前不得确认 webhook。`accept` 对唯一约束冲突直接返回，不重跑。新 pipeline 只做以下确定性工作：使用 `taskId` 调用 ITSM context API、验证返回 `taskType == "kaf_delegate"` 与 `status == "delegated"`、建立 KAF execution context（`taskId`、`correlationId`、tenant、允许动作）并调度 KAF 的自主 Procedure 选择。不得从事件或旧 ticket 文本推导 CTI/Procedure；不得接入对话 `TurnPipeline`。

- [ ] **Step 4: 写 context 拒绝和故障恢复测试**

```python
async def test_pipeline_marks_delivery_retryable_when_context_is_not_active(session):
    client = FakeItsmClient(context_response={"taskType": "user_task", "status": "assigned"})
    pipeline = KafDelegationPipeline(session=session, itsm_client=client)
    await pipeline.accept(delegate_event(event_id="evt-101", task_id="TASK-101"))
    delivery = await delivery_by_event_id(session, "evt-101")
    assert delivery.status == "retryable"
    assert "task_not_active" in delivery.last_error
```

- [ ] **Step 5: 运行测试和迁移校验并提交**

Run:

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main && alembic upgrade head
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main && pytest tests/test_itsm_webhook_auth.py tests/test_kaf_delegation_pipeline.py -q
git add src/acp/models/kaf_delegation_delivery.py alembic/versions/021_kaf_delegation_deliveries.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py src/acp/routers/itsm_webhooks.py tests/test_kaf_delegation_pipeline.py
git commit -m "feat: persist and start KAF delegation runs"
```

### Task 6: 增加 KAF task-context、补拉与首期动作 API

**Files:**
- Create: `itsm-backend/controller/kaf_delegation_controller.go`
- Modify: `itsm-backend/controller/bpmn_workflow_controller.go`
- Modify: `itsm-backend/service/kaf_delegation_service.go`
- Create: `itsm-backend/controller/kaf_delegation_controller_test.go`
- Modify: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py`
- Test: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/tests/test_kaf_delegation_pipeline.py`

**Interfaces:**
- Produces: `GET /api/v1/bpmn/process-tasks/:taskId/kaf-context`.
- Produces: `GET /api/v1/bpmn/process-tasks/kaf-delegated?status=delegated`.
- Produces: `POST /api/v1/bpmn/process-tasks/:taskId/actions` for `complete_bpmn_task`, `update_progress`, `record_execution_failure`.
- Consumes: current authenticated user from Gin context; `KafDelegationService.AuthorizeTask(ctx, task)` extracted from `authorizeKafAutomationActor` semantics.

- [ ] **Step 1: 写跨租户与非委派任务失败测试**

```go
func TestKafContext_RejectsDifferentTenantAutomationActor(t *testing.T) {
    router, taskID := newKafDelegationHTTPFixture(t, fixture{ActorTenantID: 2, TaskTenantID: 1, TaskType: "kaf_delegate"})
    response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", nil)
    assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestKafContext_RejectsNonDelegatedTaskType(t *testing.T) {
    router, taskID := newKafDelegationHTTPFixture(t, fixture{ActorTenantID: 1, TaskTenantID: 1, TaskType: "user_task"})
    response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", nil)
    assert.Equal(t, http.StatusForbidden, response.Code)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd itsm-backend && go test ./controller -run 'TestKafContext_' -v`

Expected: FAIL，因为 controller、路由和 task-scoped authorizer 不存在。

- [ ] **Step 3: 提取授权并实现只读 API**

将 `CustomProcessEngine.authorizeKafAutomationActor` 的角色、租户、`taskType` 与状态规则移动到 `KafDelegationService.AuthorizeTask`；`CompleteTask` 调用该方法，保证已有语义保持。controller 只绑定路径和认证 context，不允许客户端传 tenant、WorkItem ID 或允许动作。

`kaf-context` 响应只包含：`taskId`、`correlationId`、`recordClass`、`allowedActions`、`expectedVersion`、BPMN 等待点摘要、冻结 Intake 快照、当前 WorkItem 的 KAF 必需字段和脱敏附件引用。任务列表只返回同租户、`taskType="kaf_delegate"`、`status="delegated"` 项，分页上限 100。

- [ ] **Step 4: 写动作幂等和领域边界失败测试**

```go
func TestKafAction_CompleteIsIdempotentAndResumesExactlyOnce(t *testing.T) {
    router, taskID := newKafDelegationHTTPFixture(t, activeDelegateFixture())
    body := `{"action":"complete_bpmn_task","expectedVersion":3,"execution":{"runId":"run-1","stepId":"finish","idempotencyKey":"1:` + taskID + `:run-1:finish","correlationId":"corr-1","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"completed","evidenceRefs":[]}}`
    assert.Equal(t, http.StatusOK, doKafRequest(t, router, http.MethodPost, actionURL(taskID), body).Code)
    assert.Equal(t, http.StatusOK, doKafRequest(t, router, http.MethodPost, actionURL(taskID), body).Code)
    assert.Equal(t, 1, completedTaskCount(t, taskID))
}

func TestKafAction_RejectsResolveUntilIncidentTypedActionExists(t *testing.T) {
    response := doKafRequest(t, router, http.MethodPost, actionURL(taskID), `{"action":"resolve"}`)
    assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
}
```

- [ ] **Step 5: 实现首期动作与显式审计**

`complete_bpmn_task` 先验证任务允许动作和 `expectedVersion`，再调用现有 `CompleteTask`；相同 action idempotency key 返回已应用结果而不再次推进。`update_progress` 只向 WorkItem 时间线写脱敏摘要；`record_execution_failure` 只写失败摘要和审计，保留任务 `delegated`。三种动作都写 `AuditLog`，包含 actor user ID、tenant、task ID、procedure/version、run/step、correlation ID、结果码，且不写原始 Tool 输出。

- [ ] **Step 6: 让 KAF 使用 context API 并补拉**

在 `KafDelegationPipeline` 注入一个 typed ITSM client。webhook 接收后调用 `GET kaf-context`；启动时和每个可配置周期调用 `GET kaf-delegated?status=delegated`，对未在 delivery ledger 中完成的 task 调用同一 `accept` 路径。HTTP 401/403 必须标记 delivery 为 `failed_auth` 并报警，不得降级为直接数据库访问。

- [ ] **Step 7: 运行 API、KAF 和 BPMN 回归并提交**

Run:

```bash
cd itsm-backend && go test ./controller -run 'TestKaf(Context|Action|DelegatedList)_' -v
cd itsm-backend && go test ./service -run 'Test(CreateDelegatedTask_|KafDelegation|CompleteTask_ResumesDelegatedTask)' -v
cd itsm-backend && go build ./...
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main && pytest tests/test_kaf_delegation_pipeline.py tests/test_itsm_webhook_auth.py -q
git -C /home/administrator/project/itsm add itsm-backend/controller/kaf_delegation_controller.go itsm-backend/controller/kaf_delegation_controller_test.go itsm-backend/controller/bpmn_workflow_controller.go itsm-backend/service/kaf_delegation_service.go
git -C /home/administrator/project/itsm commit -m "feat(kaf): add task-scoped delegation APIs"
git -C /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main add src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_pipeline.py
git -C /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main commit -m "feat: pull ITSM delegation context"
```

### Task 7: SSLVPN 服务请求和 Incident 合同验收

**Files:**
- Create: `itsm-backend/service/kaf_delegation_sslvpn_e2e_test.go`
- Create: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/tests/test_kaf_delegation_contract.py`
- Modify: `docs/superpowers/specs/2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md` only if the verified API differs from the contract.

**Interfaces:**
- Consumes: all prior task contracts and a deterministic KAF Procedure/test double.
- Produces: evidence that service request and incident creation remain different professional WorkItems while using the same delegation transport.

- [ ] **Step 1: 写 Service Request SSLVPN 链路测试**

```go
func TestSSLVPNRequest_ApprovalDelegationDeliveryAndCompletion(t *testing.T) {
    fx := newSSLVPNDelegationFixture(t, "service_request_item")
    approveBothLevels(t, fx)
    event := dispatchExactlyOneDelegateEvent(t, fx)
    context := kafContext(t, fx, event.TaskID)
    assert.Equal(t, "service_request_item", context.RecordClass)
    completeDelegate(t, fx, event.TaskID, "run-sslvpn", "finish")
    assertProcessAdvancedOnce(t, fx, event.TaskID)
    assertNoSensitivePayload(t, fx)
}
```

- [ ] **Step 2: 写 Incident SSLVPN 链路测试**

```go
func TestSSLVPNIncident_UsesSameDelegationTransportWithoutServiceRequestConversion(t *testing.T) {
    fx := newSSLVPNDelegationFixture(t, "incident")
    delegate := createAndDispatchDelegate(t, fx)
    context := kafContext(t, fx, delegate.TaskID)
    assert.Equal(t, "incident", context.RecordClass)
    assertIncidentExtensionStillExists(t, fx, context.WorkItemID)
    assertNoServiceRequestExtensionCreated(t, fx, context.WorkItemID)
}
```

- [ ] **Step 3: 运行最终验证并提交**

Run:

```bash
cd itsm-backend && go test ./service -run 'TestSSLVPN(Request|Incident)_' -v
cd itsm-backend && go test ./service/... ./controller/...
cd itsm-backend && go build ./...
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main && pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py -q
git -C /home/administrator/project/itsm add itsm-backend/service/kaf_delegation_sslvpn_e2e_test.go docs/superpowers/specs/2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md
git -C /home/administrator/project/itsm commit -m "test: verify SSLVPN KAF delegation contract"
git -C /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main add tests/test_kaf_delegation_contract.py
git -C /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main commit -m "test: cover ITSM delegation contract"
```

## Plan Self-Review

- Spec coverage: §2.3/§3.1/§4.2/§5 的异步等待、事务、Outbox、签名投递、per-task 授权、显式审计和恢复补拉分别由 Tasks 2-6 覆盖；§6/§7/§9 的 Service Request 与 Incident SSLVPN 验收由 Task 7 覆盖。
- Deliberate deferrals: Intake 创建、`assign`/`resolve`/`close` typed actions、服务阶段 UI、请求详情、Workflow Designer 和通知属于独立计划；本计划不以兼容层方式改造 `sr_batch`。
- Type consistency: 事件 ID 使用 `eventId`（HTTP）/`event_id`（存储）；任务 ID 一律为 BPMN `ProcessTask.task_id` 字符串；只允许 `kaf_delegate` + `delegated` 的 KAF API 调用。
