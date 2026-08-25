# 完整性审计整改 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-08-16 完整性审计报告（流程引擎/审批链配置/自定义表单）里"建议优先处理顺序"章节列出的 6 条问题，按报告顺序逐条修复并验证。

**Architecture:** 9 个任务（Task 4 因规模拆成 4a/4b/4c/4d 四个子任务，其余按报告原始顺序 1:1 对应）。每个任务独立可测试、独立提交。Task 4 系列是这轮最大的一块：把 BPMN serviceTask 的 metaData 解析补齐，并把三个打桩 handler（ServiceRequest/Generic/Release）改成真实 Ent 写入。

**Tech Stack:** Go 1.x + Ent ORM + Gin（后端），Next.js App Router + TypeScript + Ant Design（前端），`stretchr/testify` + `enttest`（后端测试），Jest + React Testing Library（前端测试）。

**Spec:** `docs/superpowers/specs/2026-08-16-completeness-audit-remediation-design.md`

## Global Constraints

- Controller 必须返回 DTO，禁止直接返回 Ent 模型；DTO 响应字段用 camelCase。
- 用 `common.Success(c, data)` / `common.Fail(c, code, msg)` 做响应包装；日志用 `zap.S()`/注入的 `*zap.SugaredLogger`，不用 `fmt.Println`。
- 新增/修改的查询必须显式带 `tenant_id` 过滤（租户隔离）。
- 后端测试用 `stretchr/testify` 表驱动风格 + `enttest.Open(t, "sqlite3", "file:<unique>?mode=memory&cache=shared&_fk=1")`（本仓库既有约定，不用 `enttest.NewClient` 那种写法）。
- 前端测试用 Jest + React Testing Library，mock API 调用，不打真实网络请求。
- Go 文件用 snake_case；新建文件遵循 `<resource>_<role>.go` 命名（如 `release_handler.go`）。
- 每个任务改完之后跑该任务涉及包的窄范围测试；全部任务完成后跑一次 `go test ./...` 和 `npm run type-check` 做整体回归（见 Task 9 之后的"全量回归"章节）。
- 这轮修复在独立分支 `worktree-itsm-completeness-remediation`（base `origin/main`）上做，不合并进 `track4-change-approval-bpmn-migration`。

---

## File Structure

| 文件 | 任务 | 作用 |
|---|---|---|
| `itsm-backend/service/ticket_workflow_service.go` | 1 | 新增 `GetApprovalDecisions` 方法 |
| `itsm-backend/service/ticket_workflow_service_test.go` | 1 | 新增测试 |
| `itsm-backend/controller/ticket_workflow_controller.go` | 1 | 新增 `GetApprovalDecisions` handler |
| `itsm-backend/router/router.go` | 1 | 新增路由 `GET /tickets/:id/approval-decisions` |
| `itsm-frontend/src/lib/api/ticket-approval-api.ts` | 1 | 删除死接口方法，新增 `getApprovalDecisions` |
| `itsm-frontend/src/components/business/detail-tabs/ApprovalWorkflowPanel.tsx` | 1 | 改用真实接口渲染 `ApprovalTimeline` |
| `itsm-frontend/src/components/business/detail-tabs/__tests__/ApprovalWorkflowPanel.test.tsx`（新建） | 1 | 新增测试 |
| `itsm-frontend/src/app/(main)/admin/approvals/page.tsx` | 1 | 删除（死链页面） |
| `itsm-frontend/src/app/(main)/settings/approvals/page.tsx` | 1 | 删除（重定向到已删除页面） |
| `itsm-frontend/src/components/layout/sidebar/menu-config.ts` | 1 | 删除 `/admin/approvals` 菜单项 |
| `itsm-backend/service/bpmn_version_service.go` | 2 | `CreateVersion`/`ActivateVersion` 补 `is_latest` 维护 |
| `itsm-backend/service/bpmn_version_service_test.go`（新建） | 2 | 新增测试 |
| `itsm-frontend/src/components/workflow/BPMNDesigner.tsx` | 3 | `handleValidate` 补不支持元素检测 |
| `itsm-frontend/src/components/workflow/__tests__/BPMNDesigner.validate.test.tsx`（新建） | 3 | 新增测试 |
| `itsm-backend/service/bpmn_types.go` | 4a | `BPMNServiceTask` 补 `ExtensionElements` + `ServiceTaskType()`/`ServiceTaskAction()` |
| `itsm-backend/service/bpmn_process_engine.go` | 4a | `handleElement` ServiceTask 分支补 metaData 优先分发 |
| `itsm-backend/service/bpmn_process_engine_ext_test.go` | 4a | 新增测试 |
| `itsm-backend/service/bpmn/generic_handler.go` | 4b | 4 个真实动作分支 + 直接 Ent 通知写入 |
| `itsm-backend/service/bpmn/generic_handler_test.go`（新建） | 4b | 新增测试 |
| `itsm-backend/service/bpmn/service_request_handler.go` | 4c | 8 个动作改真实 Ent 写入（经关联 Ticket） |
| `itsm-backend/service/bpmn/service_request_handler_test.go`（新建） | 4c | 新增测试 |
| `itsm-backend/service/bpmn/release_handler.go`（新建） | 4d | 新建 `ReleaseServiceTaskHandler` |
| `itsm-backend/service/bpmn/release_handler_test.go`（新建） | 4d | 新增测试 |
| `itsm-backend/service/bpmn/bpmn_callback_registry.go` | 4d | 注册 `ReleaseServiceTaskHandler` |
| `itsm-backend/service/field_value_service.go` | 5 | `CreateValues`/`CreateAdHocValues` 补类型/格式校验 |
| `itsm-backend/service/field_value_service_test.go` | 5 | 新增测试 |
| `itsm-backend/service/ticket_template_service.go` | 5 | `validateTemplateFields` 补 `field_type` 允许值校验 |
| `itsm-backend/service/ticket_template_service_test.go` | 5 | 新增测试 |
| `itsm-backend/service/ticket_service.go` | 6 | `CreateTicket` 调整校验/落库顺序 |
| `itsm-backend/service/ticket_service_test.go` | 6 | 新增测试 |

---

### Task 1: 工单审批链 Tab 改用真实 BPMN 审批决策数据

**Files:**
- Modify: `itsm-backend/service/ticket_workflow_service.go`
- Test: `itsm-backend/service/ticket_workflow_service_test.go`
- Modify: `itsm-backend/controller/ticket_workflow_controller.go`
- Modify: `itsm-backend/router/router.go`
- Modify: `itsm-frontend/src/lib/api/ticket-approval-api.ts`
- Modify: `itsm-frontend/src/components/business/detail-tabs/ApprovalWorkflowPanel.tsx`
- Test: `itsm-frontend/src/components/business/detail-tabs/__tests__/ApprovalWorkflowPanel.test.tsx`
- Delete: `itsm-frontend/src/app/(main)/admin/approvals/page.tsx`
- Delete: `itsm-frontend/src/app/(main)/settings/approvals/page.tsx`
- Modify: `itsm-frontend/src/components/layout/sidebar/menu-config.ts`

**Interfaces:**
- Produces: `TicketWorkflowService.GetApprovalDecisions(ctx context.Context, ticketID, tenantID int) ([]*ent.ProcessApprovalDecision, error)`
- Produces: `TicketWorkflowController.GetApprovalDecisions(c *gin.Context)`，路由 `GET /api/v1/tickets/:id/approval-decisions`，响应 `common.Success(c, dto.ToProcessApprovalDecisionResponseList(decisions))`
- Produces: 前端 `TicketApprovalApi.getApprovalDecisions(ticketId: number): Promise<ProcessApprovalDecisionResponse[]>`
- Consumes（已存在，不用改）: `dto.ToProcessApprovalDecisionResponseList` (`dto/process_approval_decision_dto.go`)、`ent/processapprovaldecision` 生成的查询谓词 `BusinessType`/`BusinessID`/`TenantID`

- [ ] **Step 1: 写后端失败测试**

在 `itsm-backend/service/ticket_workflow_service_test.go` 末尾追加：

```go
func TestTicketWorkflowService_GetApprovalDecisions_ReturnsOrderedByCreatedAt(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_workflow_get_approval_decisions?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Test Tenant").SetCode("test-gad").SetDomain("test-gad.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	actor, err := client.User.Create().
		SetUsername("approver-gad").SetEmail("approver-gad@test.com").SetPasswordHash("x").
		SetName("Approver GAD").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	tkt, err := client.Ticket.Create().
		SetTitle("测试工单").SetTicketNumber("T-GAD-1").SetStatus("open").
		SetRequesterID(actor.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	instance, err := client.ProcessInstance.Create().
		SetProcessInstanceID("PI-ticket_general_flow-gad-1").
		SetProcessDefinitionKey("ticket_general_flow").
		SetBusinessKey(fmt.Sprintf("ticket:%d", tkt.ID)).
		SetStatus("running").SetTenantID(tenant.ID).SetVariables(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	task, err := client.ProcessTask.Create().
		SetTaskID("TASK-gad-1").SetProcessInstanceID(instance.ID).
		SetTaskDefinitionKey("Activity_Approve").SetTaskType("user_task").
		SetStatus("completed").SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	older, err := client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(instance.ID).SetProcessTaskID(task.ID).
		SetProcessInstanceKey(instance.ProcessInstanceID).SetTaskID(task.TaskID).
		SetProcessDefinitionKey("ticket_general_flow").SetNodeKey("Activity_Approve").
		SetBusinessType("ticket").SetBusinessID(strconv.Itoa(tkt.ID)).
		SetActorID(actor.ID).SetActorName(actor.Name).SetAction("approve").SetDecision("approved").
		SetComment("同意").SetTenantID(tenant.ID).
		SetCreatedAt(time.Now().Add(-time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	newer, err := client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(instance.ID).SetProcessTaskID(task.ID).
		SetProcessInstanceKey(instance.ProcessInstanceID).SetTaskID(task.TaskID).
		SetProcessDefinitionKey("ticket_general_flow").SetNodeKey("Activity_Approve2").
		SetBusinessType("ticket").SetBusinessID(strconv.Itoa(tkt.ID)).
		SetActorID(actor.ID).SetActorName(actor.Name).SetAction("approve").SetDecision("approved").
		SetComment("二级同意").SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 另一个工单的决策不应该混进来。
	_, err = client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(instance.ID).SetProcessTaskID(task.ID).
		SetProcessInstanceKey(instance.ProcessInstanceID).SetTaskID("TASK-other").
		SetProcessDefinitionKey("ticket_general_flow").SetNodeKey("Activity_Approve").
		SetBusinessType("ticket").SetBusinessID(strconv.Itoa(tkt.ID+999)).
		SetActorID(actor.ID).SetAction("approve").SetDecision("approved").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	svc := NewTicketWorkflowService(client, zaptest.NewLogger(t).Sugar())
	decisions, err := svc.GetApprovalDecisions(ctx, tkt.ID, tenant.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 2)
	assert.Equal(t, older.ID, decisions[0].ID, "应该按 created_at 升序返回")
	assert.Equal(t, newer.ID, decisions[1].ID)
}
```

若文件顶部缺少 `strconv`、`time`、`fmt` 或 `"itsm-backend/ent/enttest"`、`go.uber.org/zap/zaptest` 的 import，一并加上。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestTicketWorkflowService_GetApprovalDecisions -v`
Expected: FAIL，报 `svc.GetApprovalDecisions undefined`。

- [ ] **Step 3: 实现 `GetApprovalDecisions`**

在 `itsm-backend/service/ticket_workflow_service.go` 里，`ApproveTicket` 方法之后新增：

```go
// GetApprovalDecisions 返回某个工单在 BPMN 引擎里留下的全部审批决策记录，按时间升序。
// 工单的审批状态完全由 BPMN 驱动（ApproveTicket -> BPMNApprovalBridge -> CompleteTask ->
// recordApprovalDecision），这里直接读 ProcessApprovalDecision，不依赖 TicketApproval 表——
// 后者只在委派场景下才会写入（见 ApproveTicket 的 delegate 分支），首次审批完全不经过它。
func (s *TicketWorkflowService) GetApprovalDecisions(ctx context.Context, ticketID, tenantID int) ([]*ent.ProcessApprovalDecision, error) {
	return s.client.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.BusinessType("ticket"),
			processapprovaldecision.BusinessID(strconv.Itoa(ticketID)),
			processapprovaldecision.TenantID(tenantID),
		).
		Order(ent.Asc(processapprovaldecision.FieldCreatedAt)).
		All(ctx)
}
```

在文件顶部 import 块里加 `"strconv"` 和 `"itsm-backend/ent/processapprovaldecision"`（若已存在则跳过）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestTicketWorkflowService_GetApprovalDecisions -v`
Expected: PASS

- [ ] **Step 5: 加 controller handler**

在 `itsm-backend/controller/ticket_workflow_controller.go` 里，任意已有 handler（如 `GetTicketWorkflowHistory`）之后新增：

```go
// GetApprovalDecisions handles GET /api/v1/tickets/:id/approval-decisions
func (c *TicketWorkflowController) GetApprovalDecisions(ctx *gin.Context) {
	ticketID, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		common.Fail(ctx, 1001, "无效的工单ID")
		return
	}
	tenantID := ctx.GetInt("tenant_id")
	decisions, err := c.workflowService.GetApprovalDecisions(ctx.Request.Context(), ticketID, tenantID)
	if err != nil {
		common.InternalError(ctx, "获取审批记录失败: "+err.Error())
		return
	}
	common.Success(ctx, dto.ToProcessApprovalDecisionResponseList(decisions))
}
```

- [ ] **Step 6: 注册路由**

在 `itsm-backend/router/router.go` 里，紧跟第 611 行 `tickets.GET("/:id/workflow_records", ...)` 之后新增一行：

```go
				tickets.GET("/:id/approval-decisions", middleware.RequirePermission("workflow", "read"), config.TicketWorkflowController.GetApprovalDecisions)
```

- [ ] **Step 7: 跑后端全部相关测试确认通过、编译通过**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./controller/... ./router/... -run "TestTicketWorkflowService|TestRouter" -v`
Expected: PASS，编译无报错。

- [ ] **Step 8: 提交后端部分**

```bash
cd itsm-backend
git add service/ticket_workflow_service.go service/ticket_workflow_service_test.go controller/ticket_workflow_controller.go router/router.go
git commit -m "feat(ticket): 新增按工单查询 BPMN 审批决策记录的只读接口"
```

- [ ] **Step 9: 精简前端 API 客户端**

把 `itsm-frontend/src/lib/api/ticket-approval-api.ts` 整个替换为：

```typescript
import { httpClient } from './http-client';

export interface ProcessApprovalDecision {
  id: number;
  processInstanceId: number;
  processInstanceKey: string;
  processTaskId: number;
  taskId: string;
  processDefinitionKey: string;
  nodeKey: string;
  businessType?: string;
  businessId?: string;
  actorId: number;
  actorName?: string;
  action: string;
  decision: string;
  comment?: string;
  variablesSnapshot?: Record<string, unknown>;
  createdAt: string;
}

export interface SubmitApprovalRequest {
  approvalId: number;
  ticketId: number;
  action: 'approve' | 'reject' | 'delegate';
  comment?: string;
  delegateToUserId?: number;
}

/**
 * 工单审批相关接口。旧版 /api/v1/approval-workflows、/api/v1/tickets/approval/records
 * 在后端下线 legacy ApprovalWorkflow 引擎时已经被删除（router.go 已确认无此路由），
 * 这里只保留真实存在的两个接口：读 BPMN 审批决策历史、提交审批动作。
 */
export class TicketApprovalApi {
  static async getApprovalDecisions(ticketId: number): Promise<ProcessApprovalDecision[]> {
    const res = await httpClient.get<ProcessApprovalDecision[]>(
      `/api/v1/tickets/${ticketId}/approval-decisions`
    );
    return res || [];
  }

  static async submitApproval(data: SubmitApprovalRequest): Promise<void> {
    await httpClient.post('/api/v1/tickets/workflow/approve', data);
  }
}

export default TicketApprovalApi;
```

（用 `Read` 先确认 `httpClient` 的实际导出方式和 `get`/`post` 的返回值形状跟本文件其它同目录 API 客户端一致，比如 `change-api.ts` 或 `bpmn-workflow-api.ts` 的写法，按仓库实际约定调整 import 路径和调用方式——上面这段是目标行为，不是要求逐字节匹配。)

- [ ] **Step 10: 重写 `ApprovalWorkflowPanel.tsx` 的数据获取与渲染**

先用 `Read` 工具读取 `itsm-frontend/src/components/business/detail-tabs/ApprovalWorkflowPanel.tsx` 全文。保留文件里所有跟审批无关的部分不变（如果有的话），只替换以下三处：

**(a) 数据获取部分**：把原来 `loadAll` 里对 `TicketApprovalApi.getWorkflows(...)` 和 `TicketApprovalApi.getApprovalRecords(...)` 的两次调用（都包着 `.catch()` 静默吞错误），替换成一次真实调用：

```typescript
const [decisions, setDecisions] = useState<ProcessApprovalDecision[]>([]);
const [loading, setLoading] = useState(true);

const loadAll = useCallback(async () => {
  setLoading(true);
  try {
    const data = await TicketApprovalApi.getApprovalDecisions(ticketId);
    setDecisions(data);
  } catch (error) {
    message.error(getErrorMessage(error) || '加载审批记录失败');
    setDecisions([]);
  } finally {
    setLoading(false);
  }
}, [ticketId]);
```

删除跟 `getWorkflows`/`hasWorkflowMeta`/`workflows` 状态相关的所有代码——没有真实接口能提供"审批链定义全貌"这个概念了，不要用假数据伪造它。`getErrorMessage` 从 `@/lib/utils/error-message-handler` 导入（本仓库既有工具，见 `ChangeDetail.tsx` 的用法）。

**(b) 决策数据到 `ApprovalStep[]` 的映射**：在同文件里新增一个纯函数：

```typescript
import type { ApprovalStep, ApprovalStepStatus } from './types';

function decisionStatusToStepStatus(decision: string): ApprovalStepStatus {
  switch (decision) {
    case 'approved':
      return 'approved';
    case 'rejected':
      return 'rejected';
    case 'delegated':
      return 'delegated';
    case 'timeout':
      return 'timeout';
    default:
      // withdrawn / system_decision 等在 ApprovalStepStatus 里没有对应值，
      // 归到 skipped——保留记录可见，但不暗示这是一次正常的通过/拒绝决策。
      return 'skipped';
  }
}

function toApprovalSteps(decisions: ProcessApprovalDecision[]): ApprovalStep[] {
  return decisions.map((d, index) => ({
    id: d.id,
    level: index + 1,
    step: d.nodeKey,
    status: decisionStatusToStepStatus(d.decision),
    approverId: d.actorId,
    approverName: d.actorName,
    comment: d.comment,
    processedAt: d.createdAt,
    createdAt: d.createdAt,
  }));
}
```

**(c) 渲染部分**：删除原来"Steps 全景"那一块（用 `hasWorkflowMeta` 控制显示/隐藏的那部分），只保留（或改为）渲染 `ApprovalTimeline`：

```tsx
import { ApprovalTimeline } from './ApprovalTimeline';

// ...

if (loading) {
  return <Spin />;
}

const steps = toApprovalSteps(decisions);

if (steps.length === 0) {
  return <Empty description="该工单未走审批流程" />;
}

return (
  <ApprovalTimeline
    approvals={steps}
    formatDateTime={formatDateTime}
  />
);
```

`ApprovalTimeline` 的 `onApprove`/`onReject`/`onDelegate`/`canApprove`/`showApprovalActions` 这几个 prop 是可选的（见 `ApprovalTimeline.tsx:36-40`）——这个 Tab 是只读历史展示，不需要传，不要为了"看起来功能完整"而伪造审批动作入口（提交审批走的是工单详情页别处已有的操作入口，不是这个 Tab 的职责）。

`Spin`/`Empty` 从 `antd` 导入（若原文件未导入，需要补上）。

- [ ] **Step 11: 写前端测试**

新建 `itsm-frontend/src/components/business/detail-tabs/__tests__/ApprovalWorkflowPanel.test.tsx`：

```tsx
import React from 'react';
import { render, screen, waitFor } from '@/lib/test-utils';

const mockGetApprovalDecisions = jest.fn();

jest.mock('@/lib/api/ticket-approval-api', () => ({
  TicketApprovalApi: {
    getApprovalDecisions: (...args: unknown[]) => mockGetApprovalDecisions(...args),
  },
}));

import ApprovalWorkflowPanel from '../ApprovalWorkflowPanel';

describe('ApprovalWorkflowPanel — 真实审批决策展示', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('接口返回真实决策记录时，渲染审批时间线而不是"未走审批流程"', async () => {
    mockGetApprovalDecisions.mockResolvedValue([
      {
        id: 1,
        processInstanceId: 10,
        processInstanceKey: 'PI-ticket_general_flow-1',
        processTaskId: 100,
        taskId: 'TASK-1',
        processDefinitionKey: 'ticket_general_flow',
        nodeKey: 'Activity_Approve',
        businessType: 'ticket',
        businessId: '5',
        actorId: 7,
        actorName: '张三',
        action: 'approve',
        decision: 'approved',
        comment: '同意',
        createdAt: '2026-08-16T10:00:00Z',
      },
    ]);

    render(
      <ApprovalWorkflowPanel
        ticketId={5}
        isTicketFinal={false}
      />
    );

    await waitFor(() => expect(mockGetApprovalDecisions).toHaveBeenCalledWith(5));
    expect(await screen.findByText('张三')).toBeInTheDocument();
    expect(screen.queryByText('该工单未走审批流程')).not.toBeInTheDocument();
  });

  it('接口返回空数组时，展示"未走审批流程"（真实的空，不是吞错误后的假空）', async () => {
    mockGetApprovalDecisions.mockResolvedValue([]);

    render(
      <ApprovalWorkflowPanel
        ticketId={6}
        isTicketFinal={false}
      />
    );

    await waitFor(() => expect(mockGetApprovalDecisions).toHaveBeenCalledWith(6));
    expect(await screen.findByText('该工单未走审批流程')).toBeInTheDocument();
  });
});
```

先用 `Read` 确认 `ApprovalWorkflowPanel` 实际必填 props（`ticketId`/`isTicketFinal` 等，按 fact-finding 记录的 `ApprovalWorkflowPanelProps` 补齐/调整），以及 `@/lib/test-utils` 的 `render` 导出方式（本仓库既有测试的标准用法，参照 `ChangeDetail.test.tsx`）。

- [ ] **Step 12: 跑前端测试确认通过**

```bash
cd itsm-frontend
nohup npx jest src/components/business/detail-tabs/__tests__/ApprovalWorkflowPanel.test.tsx --runInBand > /tmp/jest-task1.log 2>&1 &
disown
```

等待后台任务结束（这个沙箱环境下 jest 多进程模式会挂起，必须用 `--runInBand` 且必须 `nohup ... & disown` 到后台跑，见本仓库已知环境问题），然后读 `/tmp/jest-task1.log` 确认两个用例都 PASS。

- [ ] **Step 13: 删除死链页面和菜单项**

```bash
cd itsm-frontend
rm src/app/\(main\)/admin/approvals/page.tsx
rm src/app/\(main\)/settings/approvals/page.tsx
```

用 `Edit` 工具在 `src/components/layout/sidebar/menu-config.ts` 里删除 `{ key: '/admin/approvals', label: '审批管理', path: '/admin/approvals', permission: 'approval:manage' }` 这一整条菜单项（先 `Read` 确认精确文本，因为 fact-finding 只给了内容摘要没有给精确行号）。

- [ ] **Step 14: 跑 type-check 确认删除页面没有遗留引用**

Run: `cd itsm-frontend && npm run type-check`
Expected: 无报错。如果有其它文件还在 import 被删除的两个 page 组件，按报错逐一清理引用（正常情况下 Next.js page 文件不会被其它模块 import，只会通过路由访问）。

- [ ] **Step 15: 提交前端部分**

```bash
cd itsm-frontend
git add src/lib/api/ticket-approval-api.ts \
  src/components/business/detail-tabs/ApprovalWorkflowPanel.tsx \
  src/components/business/detail-tabs/__tests__/ApprovalWorkflowPanel.test.tsx \
  src/components/layout/sidebar/menu-config.ts
git rm src/app/\(main\)/admin/approvals/page.tsx src/app/\(main\)/settings/approvals/page.tsx
git commit -m "fix(ticket): 工单审批链Tab改用真实BPMN审批决策数据，删除死链的审批管理页面"
```

---

### Task 2: BPMN 版本管理修复 `is_latest` 破坏问题

**Files:**
- Modify: `itsm-backend/service/bpmn_version_service.go`
- Test: `itsm-backend/service/bpmn_version_service_test.go`（新建）

**Interfaces:**
- Consumes（已存在）: `s.client.ProcessDefinition.Query()/.Update()/.UpdateOne()`，`processdefinition.Key`/`processdefinition.TenantID`/`processdefinition.IsLatest`
- Produces: `CreateVersion`/`ActivateVersion` 的行为契约变化——同一 `(tenant_id, key)` 任何时刻至多一行 `is_latest=true`

- [ ] **Step 1: 写失败测试**

新建 `itsm-backend/service/bpmn_version_service_test.go`：

```go
package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processdefinition"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestBPMNVersionService_CreateVersion_DemotesOldLatest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn_version_create_demotes?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("bvc-1").SetDomain("bvc-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	svc := NewBPMNVersionService(client, logger)

	v1, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow",
		Name:                 "测试流程",
		BPMNXML:              "<bpmn:definitions/>",
		TenantID:             tenant.ID,
		CreatedBy:            "tester",
	})
	require.NoError(t, err)

	v2, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow",
		Name:                 "测试流程",
		BPMNXML:              "<bpmn:definitions/>",
		TenantID:             tenant.ID,
		CreatedBy:            "tester",
	})
	require.NoError(t, err)
	require.NotEqual(t, v1.Version, v2.Version)

	latestCount, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.Key("test_flow"),
			processdefinition.TenantID(tenant.ID),
			processdefinition.IsLatest(true),
		).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, latestCount, "同一 key 任何时刻只应该有一行 is_latest=true")

	stillLatest, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("test_flow"), processdefinition.TenantID(tenant.ID), processdefinition.IsLatest(true)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, v2.Version, stillLatest.Version, "最新的应该是最后一次创建的版本")
}

func TestBPMNVersionService_ActivateVersion_DoesNotBreakIsLatest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn_version_activate_islatest?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("bvc-2").SetDomain("bvc-2.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	svc := NewBPMNVersionService(client, logger)
	v1, err := svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow2", Name: "流程2", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.NoError(t, err)
	_, err = svc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow2", Name: "流程2", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.NoError(t, err)

	// 激活回第一个版本，is_latest 不应该跟着 is_active 一起被搞乱——
	// 激活的是旧版本，但"最新版本"这个概念不应该因为激活操作而改变。
	require.NoError(t, svc.ActivateVersion(ctx, "test_flow2", v1.Version, tenant.ID))

	latestCount, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key("test_flow2"), processdefinition.TenantID(tenant.ID), processdefinition.IsLatest(true)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, latestCount)
}

func TestBPMNVersionService_GetLatestProcessDefinition_ReturnsMostRecentlyCreated(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn_version_get_latest?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("bvc-3").SetDomain("bvc-3.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	versionSvc := NewBPMNVersionService(client, logger)
	_, err = versionSvc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow3", Name: "流程3", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.NoError(t, err)
	v2, err := versionSvc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: "test_flow3", Name: "流程3", BPMNXML: "<x/>",
		TenantID: tenant.ID, CreatedBy: "tester",
	})
	require.NoError(t, err)

	defSvc := bpmnProcessDefinitionService{client: client}
	latest, err := defSvc.GetLatestProcessDefinition(newTenantCtx(ctx, tenant.ID), "test_flow3")
	require.NoError(t, err)
	assert.Equal(t, v2.Version, latest.Version)
}
```

在文件末尾加一个小 helper（供最后一个测试用，因为 `GetLatestProcessDefinition` 从 `ctx.Value(bpmn.BPMNTenantIDContextKey)` 取租户）：

```go
func newTenantCtx(ctx context.Context, tenantID int) context.Context {
	return context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
}
```

并在 import 里加 `"itsm-backend/service/bpmn"`（若跟包名 `bpmn` 冲突，按现有 `bpmn_process_engine.go` 里的 import 别名习惯处理——查一下 `bpmn_process_engine.go` 顶部 `bpmn` 包是怎么 import 的，照抄）。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestBPMNVersionService -v`
Expected: FAIL —— `latestCount` 断言会失败（实际会是 2，不是 1），因为 `CreateVersion` 目前不降级旧版本。

- [ ] **Step 3: 实现修复**

在 `itsm-backend/service/bpmn_version_service.go` 的 `CreateVersion` 里，`newVersion := incrementSemver(currentVersion)` 之后、创建 `ProcessDeployment` 之前，插入降级旧版本的逻辑：

```go
	newVersion := incrementSemver(currentVersion)

	// 把当前 is_latest=true 的旧版本降级——不这样做的话，每次 CreateVersion 都会
	// 让同一个 key 同时存在多行 is_latest=true（新行靠 schema 默认值天生是 true，
	// 旧行从来没人主动改成 false），GetLatestProcessDefinition/StartProcess 的
	// .First() 会取到不确定的一行。跟 bpmnProcessDefinitionService.CreateProcessDefinition
	// （service/bpmn_process_engine.go）已经写对的降级逻辑保持一致。
	if err := s.demoteCurrentLatest(ctx, req.ProcessDefinitionKey, req.TenantID); err != nil {
		return nil, err
	}

	// 先创建部署记录（因为ProcessDefinition需要deployment_id）
```

在 `CreateVersion` 方法后面新增一个私有辅助方法：

```go
// demoteCurrentLatest 把某个 (tenant, key) 当前 is_latest=true 的那一行改成 false。
// 没有旧版本（首次创建）时 First 返回 not-found，直接当作无需处理。
func (s *BPMNVersionService) demoteCurrentLatest(ctx context.Context, key string, tenantID int) error {
	existing, err := s.client.ProcessDefinition.Query().
		Where(
			processdefinition.Key(key),
			processdefinition.TenantID(tenantID),
			processdefinition.IsLatest(true),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("查询当前最新版本失败: %w", err)
	}
	if _, err := s.client.ProcessDefinition.UpdateOne(existing).SetIsLatest(false).Save(ctx); err != nil {
		return fmt.Errorf("降级旧版本失败: %w", err)
	}
	return nil
}
```

并在创建新 `ProcessDefinition` 那段显式加 `SetIsLatest(true)`（目前依赖 schema 默认值，改成显式设置，跟 `bpmnProcessDefinitionService.CreateProcessDefinition` 的写法保持一致，避免"正确性依赖 schema 默认值恰好是 true"这种隐式耦合）：

```go
	processDef, err := s.client.ProcessDefinition.Create().
		SetKey(req.ProcessDefinitionKey).
		SetName(req.Name).
		SetDescription(req.Description).
		SetBpmnXML([]byte(req.BPMNXML)).
		SetVersion(newVersion).
		SetTenantID(req.TenantID).
		SetIsActive(false).
		SetIsLatest(true).
		SetDeploymentID(deployment.ID).
		Save(ctx)
```

确认 `ent.IsNotFound` 需要 `"itsm-backend/ent"` 这个 import（文件里如果已经在别处用了 `*ent.ProcessVersion`/`processdefinition` 包，大概率已经导入，检查一下）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestBPMNVersionService -v`
Expected: PASS

- [ ] **Step 5: 跑整个 service 包测试确认没有破坏其它用例**

Run: `cd itsm-backend && go build ./... && go test ./service/... -run "TestBPMN" -v`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
cd itsm-backend
git add service/bpmn_version_service.go service/bpmn_version_service_test.go
git commit -m "fix(bpmn): 版本管理 CreateVersion 补上 is_latest 降级，修复多行同时为最新版本的问题"
```

---

### Task 3: BPMN 设计器给不支持的元素类型加校验提示

**Files:**
- Modify: `itsm-frontend/src/components/workflow/BPMNDesigner.tsx`
- Test: `itsm-frontend/src/components/workflow/__tests__/BPMNDesigner.validate.test.tsx`（新建）

**Interfaces:**
- Consumes: `elementRegistry.filter(...)`（bpmn-js，已有用法）
- Produces: `handleValidate` 返回值里新增 `{ type: 'warning', message: string }` 条目，对应引擎不支持真正执行的元素类型

- [ ] **Step 1: 写失败测试**

由于 `BPMNDesigner` 深度依赖真实 bpmn-js modeler 实例（`modelerRef.current`），直接渲染整个组件成本很高。改成对 `handleValidate` 里新增的纯逻辑部分做单元测试——先看 Step 3 抽出的纯函数 `checkUnsupportedElements`，测试直接针对这个函数写，不用挂载整个组件。

新建 `itsm-frontend/src/components/workflow/__tests__/BPMNDesigner.validate.test.tsx`：

```tsx
import { checkUnsupportedElements } from '../BPMNDesigner';

describe('checkUnsupportedElements — 检测引擎不支持真正执行的元素类型', () => {
  it('并行网关触发警告，说明引擎会退化成单分支执行', () => {
    const elements = [
      { id: 'Gateway_1', type: 'bpmn:ParallelGateway', businessObject: { name: '并行网关' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues).toHaveLength(1);
    expect(issues[0].type).toBe('warning');
    expect(issues[0].message).toContain('并行网关');
    expect(issues[0].message).toContain('不支持');
  });

  it('包容网关同样触发警告', () => {
    const elements = [
      { id: 'Gateway_2', type: 'bpmn:InclusiveGateway', businessObject: { name: '包容网关' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues).toHaveLength(1);
    expect(issues[0].message).toContain('包容网关');
  });

  it('子流程触发警告', () => {
    const elements = [
      { id: 'Sub_1', type: 'bpmn:SubProcess', businessObject: { name: '子流程' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues).toHaveLength(1);
    expect(issues[0].message).toContain('子流程');
  });

  it('排他网关、用户任务等受支持的元素不触发这条规则', () => {
    const elements = [
      { id: 'Gateway_3', type: 'bpmn:ExclusiveGateway', businessObject: { name: '排他网关' } },
      { id: 'Task_1', type: 'bpmn:UserTask', businessObject: { name: '用户任务' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues).toHaveLength(0);
  });

  it('每个不支持的元素都带上 elementId，方便定位', () => {
    const elements = [
      { id: 'Gateway_4', type: 'bpmn:ParallelGateway', businessObject: { name: '并行网关4' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues[0].elementId).toBe('Gateway_4');
    expect(issues[0].elementType).toBe('bpmn:ParallelGateway');
    expect(issues[0].elementName).toBe('并行网关4');
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd itsm-frontend
nohup npx jest src/components/workflow/__tests__/BPMNDesigner.validate.test.tsx --runInBand > /tmp/jest-task3-fail.log 2>&1 &
disown
```
等待结束后读 `/tmp/jest-task3-fail.log`。
Expected: FAIL —— `checkUnsupportedElements` 未导出/不存在。

- [ ] **Step 3: 实现 `checkUnsupportedElements` 并接入 `handleValidate`**

用 `Read` 打开 `itsm-frontend/src/components/workflow/BPMNDesigner.tsx`，在文件里（`handleValidate` 函数定义之前，模块级别）新增并导出一个纯函数：

```tsx
interface ElementLike {
  id: string;
  type: string;
  businessObject?: { name?: string; $type?: string };
}

interface ValidationIssueLike {
  type: 'error' | 'warning';
  message: string;
  elementId?: string;
  elementType?: string;
  elementName?: string;
}

// 引擎目前会静默单分支执行（并行/包容网关）或完全忽略（定时器/消息事件/子流程/
// 边界事件）这些元素类型——不是报错，是"看起来配置成功，实际不会按 BPMN 语义执行"。
// 校验器本身之前对这些类型完全没有感知，反而可能引导用户在并行网关上补条件表达式
// （真实 BPMN 语义里这是错的，而且并不能解决"引擎不支持并行"这个根本问题）。
const UNSUPPORTED_ELEMENT_TYPES: Record<string, string> = {
  'bpmn:ParallelGateway': '并行网关：引擎会退化成单分支执行（跟排他网关一样只走一条路径），不会真正并行/汇合',
  'bpmn:InclusiveGateway': '包容网关：引擎会退化成单分支执行，不会按包容语义多路径触发',
  'bpmn:SubProcess': '子流程：引擎不支持子流程节点，会被直接忽略',
  'bpmn:BoundaryEvent': '边界事件：引擎不支持边界事件节点，会被直接忽略',
};

export function checkUnsupportedElements(elements: ElementLike[]): ValidationIssueLike[] {
  const issues: ValidationIssueLike[] = [];
  for (const el of elements) {
    const type = el.type || el.businessObject?.$type;
    if (!type) continue;
    const reason = UNSUPPORTED_ELEMENT_TYPES[type];
    if (!reason) continue;
    issues.push({
      type: 'warning',
      message: `元素 "${el.businessObject?.name || el.id}" 使用了当前引擎不支持真正执行的类型 —— ${reason}`,
      elementId: el.id,
      elementType: type,
      elementName: el.businessObject?.name || el.id,
    });
  }
  return issues;
}
```

再在 `handleValidate` 函数体内，紧跟着已有的"检查网关是否有默认分支和条件"那一段（`gateways.forEach(...)` 结束之后、`// 显示验证结果` 之前），插入调用：

```tsx
    // 检查是否使用了引擎目前不支持真正执行的元素类型（并行/包容网关、子流程、边界事件）
    const allElements = elementRegistry.filter(() => true);
    errors.push(...checkUnsupportedElements(allElements));
```

同时给每个已有的 `errors.push({ type: ..., message: ... })` 调用补上 `elementId`/`elementType`/`elementName` 字段（用户任务/服务任务/网关那三段循环里已经拿到了 `task`/`gateway` 元素本身），让父组件 `WorkflowDesigner.tsx` 的"点击定位到该元素"功能对这些既有警告也能生效——比如用户任务那段改成：

```tsx
        errors.push({
          type: 'warning',
          message: `用户任务 "${bo.name || task.id}" 未配置受理人或候选人`,
          elementId: task.id,
          elementType: 'bpmn:UserTask',
          elementName: bo.name,
        });
```

服务任务、网关两段照同样方式补齐（`elementId: task.id`/`elementId: gateway.id`，`elementType` 用各自的 `bo.$type` 或对应字面量，`elementName: bo.name`）。

- [ ] **Step 4: 跑测试确认通过**

```bash
cd itsm-frontend
nohup npx jest src/components/workflow/__tests__/BPMNDesigner.validate.test.tsx --runInBand > /tmp/jest-task3-pass.log 2>&1 &
disown
```
等待结束后读日志确认 5 个用例全部 PASS。

- [ ] **Step 5: 跑 type-check**

Run: `cd itsm-frontend && npm run type-check`
Expected: 无报错。

- [ ] **Step 6: 提交**

```bash
cd itsm-frontend
git add src/components/workflow/BPMNDesigner.tsx src/components/workflow/__tests__/BPMNDesigner.validate.test.tsx
git commit -m "fix(workflow): 流程设计器校验器识别引擎不支持真正执行的元素类型"
```

---

### Task 4a: BPMN 引擎给 serviceTask 补 metaData 解析和优先分发

**Files:**
- Modify: `itsm-backend/service/bpmn_types.go`
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`

**Interfaces:**
- Produces: `(*BPMNServiceTask).ServiceTaskType() string`、`(*BPMNServiceTask).ServiceTaskAction() string`（跟 `BPMNUserTask` 同名方法语义一致）
- Produces: `handleElement` 对声明了 `service_task_type` metaData 的 serviceTask 节点，改用 `findHandlerByTaskType` 分发（跟 UserTask 走同一套注册表查找），并把 metaData 的 `action` 注入变量
- Consumes（已存在，不改签名）: `findHandlerByTaskType`、`mergeServiceTaskVariables`、`bpmnMetaDataServiceTaskType`/`bpmnMetaDataAction` 常量

- [ ] **Step 1: 写失败测试**

在 `itsm-backend/service/bpmn_process_engine_ext_test.go` 末尾追加：

```go
func TestBPMNServiceTask_ServiceTaskType_ReadsExtensionElementsMetaData(t *testing.T) {
	task := &BPMNServiceTask{
		ID: "svc1",
		ExtensionElements: &BPMNExtensionElements{
			MetaData: []BPMNMetaData{
				{Name: "service_task_type", Value: "generic_task"},
				{Name: "action", Value: "notify"},
			},
		},
	}
	assert.Equal(t, "generic_task", task.ServiceTaskType())
	assert.Equal(t, "notify", task.ServiceTaskAction())
}

func TestBPMNServiceTask_ServiceTaskType_NilExtensionElementsReturnsEmpty(t *testing.T) {
	task := &BPMNServiceTask{ID: "svc2"}
	assert.Equal(t, "", task.ServiceTaskType())
	assert.Equal(t, "", task.ServiceTaskAction())
}

func TestHandleElement_ServiceTask_DispatchesByMetaDataOverAttributeGuessing(t *testing.T) {
	engine, baseCtx := newApprovalDecisionTestEngine(t)
	tenantID, actorID := setupApprovalDecisionFixture(t, engine)
	ctx := context.WithValue(baseCtx, bpmn.BPMNTenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actorID)

	tkt, err := engine.client.Ticket.Create().
		SetTitle("svc-task-dispatch-test").SetTicketNumber("T-SVC-1").SetStatus("open").
		SetRequesterID(actorID).SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	instance, err := engine.client.ProcessInstance.Create().
		SetProcessInstanceID("PI-svc-dispatch-test").
		SetProcessDefinitionKey("svc_dispatch_test_flow").
		SetBusinessKey(fmt.Sprintf("ticket:%d", tkt.ID)).
		SetStatus("running").SetTenantID(tenantID).
		SetVariables(map[string]interface{}{"business_type": "ticket", "business_id": tkt.ID}).
		Save(ctx)
	require.NoError(t, err)

	process := &BPMNProcess{
		ServiceTasks: []*BPMNServiceTask{
			{
				ID:             "Activity_UpdateStatus",
				Name:           "更新状态",
				Implementation: "##WebService", // 内置模板里的占位符属性，不应该被用来查 handler
				ExtensionElements: &BPMNExtensionElements{
					MetaData: []BPMNMetaData{
						{Name: "service_task_type", Value: "ticket_task"},
						{Name: "action", Value: "update_status"},
					},
				},
			},
		},
		EndEvents: []*BPMNEndEvent{{ID: "End_1", Name: "结束"}},
		SequenceFlows: []*BPMNSequenceFlow{
			{ID: "Flow_1", SourceRef: "Activity_UpdateStatus", TargetRef: "End_1"},
		},
	}

	err = engine.handleElement(ctx, instance, process, "Activity_UpdateStatus")
	require.NoError(t, err)

	updated, err := engine.client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "in_progress", updated.Status, "ticket_task 的 update_status（默认目标状态）应该真实生效")
}
```

如果 `BPMNProcess`/`BPMNEndEvent`/`BPMNSequenceFlow` 的字段名（`EndEvents`/`SequenceFlows`/`SourceRef`/`TargetRef`）跟本文件其它已有测试（比如 `TestBPMNProcessEngine_FindServiceTask` 用到的 `BPMNProcess{ServiceTasks: [...]}`）的写法对不上，以 `itsm-backend/service/bpmn_types.go` 里 `BPMNProcess` struct 的真实字段名为准调整。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run "TestBPMNServiceTask_ServiceTaskType|TestHandleElement_ServiceTask_DispatchesByMetaData" -v`
Expected: FAIL —— `ExtensionElements` 字段不存在（编译错误）。

- [ ] **Step 3: 给 `BPMNServiceTask` 补字段和方法**

在 `itsm-backend/service/bpmn_types.go` 里，`BPMNServiceTask` struct 定义（`Type 164-180` 那段）里加一个字段：

```go
// BPMNServiceTask 服务任务
type BPMNServiceTask struct {
	ID                 string `xml:"id,attr"`
	Name               string `xml:"name,attr"`
	Type               string `xml:"type,attr"`
	OperationRef       string `xml:"operationRef,attr"`
	Implementation     string `xml:"implementation,attr"`
	Class              string `xml:"class,attr"`
	Expression         string `xml:"expression,attr"`
	DelegateExpression string `xml:"delegateExpression,attr"`
	CCType             string `xml:"ccType,attr"`
	CCUserIDs          string `xml:"ccUserIds,attr"`
	CCGroupIDs         string `xml:"ccGroupIds,attr"`
	CCRoleIDs          string `xml:"ccRoleIds,attr"`
	CCVariable         string `xml:"ccVariable,attr"`
	CCNotify           string `xml:"ccNotify,attr"`
	NotifyChannels     string `xml:"notifyChannels,attr"`

	// ExtensionElements 承载 <bpmn:metaData>，用法跟 BPMNUserTask 完全一样——
	// 之前这里没有这个字段，encoding/xml 会静默丢弃 <bpmn:extensionElements> 子元素，
	// 导致内置模板里所有 serviceTask 声明的 service_task_type/action 完全读不到，
	// handleElement 只能退回按 implementation/class 等属性猜 handler ID，而内置模板
	// 这些属性要么是占位符 "##WebService" 要么整个不写，猜测必然落空。
	ExtensionElements *BPMNExtensionElements `xml:"extensionElements"`
}

// ServiceTaskType 返回该服务任务声明的 service_task_type metaData，未声明时返回空串。
func (e *BPMNServiceTask) ServiceTaskType() string {
	return e.ExtensionElements.GetMetaData(bpmnMetaDataServiceTaskType)
}

// ServiceTaskAction 返回该服务任务声明的 action metaData，未声明时返回空串。
func (e *BPMNServiceTask) ServiceTaskAction() string {
	return e.ExtensionElements.GetMetaData(bpmnMetaDataAction)
}
```

- [ ] **Step 4: 跑测试确认结构体部分通过、分发部分仍失败**

Run: `cd itsm-backend && go test ./service/... -run "TestBPMNServiceTask_ServiceTaskType" -v`
Expected: PASS

Run: `cd itsm-backend && go test ./service/... -run "TestHandleElement_ServiceTask_DispatchesByMetaData" -v`
Expected: FAIL —— ticket 状态还是 `open`（走的是旧的属性猜测分支，`##WebService` 匹配不到任何 handler，静默跳过）。

- [ ] **Step 5: 改 `handleElement` 的 ServiceTask 分支**

在 `itsm-backend/service/bpmn_process_engine.go` 里，把 ServiceTask 分支（`} else if serviceTask := e.findServiceTask(process, elementID); serviceTask != nil {` 开始的整段）替换成：

```go
	} else if serviceTask := e.findServiceTask(process, elementID); serviceTask != nil {
		// 优先按 metaData 里的 service_task_type/action 分发——跟 UserTask 走
		// dispatchUserTaskCallback 时用的是同一套 findHandlerByTaskType 查找口径，
		// 保证"模板声明了 service_task_type 就一定能找到对应 handler"这条规则
		// 在 UserTask 和 ServiceTask 两种节点类型上表现一致。
		if serviceTaskType := serviceTask.ServiceTaskType(); serviceTaskType != "" {
			if handler := e.findHandlerByTaskType(serviceTaskType); handler != nil {
				callbackVars := mergeServiceTaskVariables(instance.Variables, serviceTask)
				if action := serviceTask.ServiceTaskAction(); action != "" {
					callbackVars[bpmnMetaDataAction] = action
				}
				e.logger.Infow("执行 ServiceTask 回调（metaData 分发）", "serviceTaskType", serviceTaskType, "elementID", elementID)
				if _, err := handler.Execute(ctx, nil, callbackVars); err != nil {
					return fmt.Errorf("ServiceTask %s 执行失败: %w", elementID, err)
				}
				return e.executeStep(ctx, instance, process, elementID, instance.Variables)
			}
			// 声明了类型但没有注册对应 handler（比如未来新增了类型但忘了注册）：
			// 按既有约定视为 NoOp，只告警不阻断流程，跟 dispatchUserTaskCallback
			// 遇到同样情况时的处理方式保持一致。
			e.logger.Warnw("ServiceTask 声明的 service_task_type 没有注册处理器，跳过执行", "elementID", elementID, "serviceTaskType", serviceTaskType)
			return e.executeStep(ctx, instance, process, elementID, instance.Variables)
		}

		// 没有声明 metaData 时，保留原有按 implementation/class/expression/operationRef
		// 属性猜 handler ID 的兜底逻辑——这是历史行为，目前没有任何内置模板会走到这里
		// （全部改成了 metaData 声明），但不删除它，避免破坏可能存在的自定义模板。
		serviceRef := serviceTask.ID
		if serviceTask.Name != "" {
			serviceRef = serviceTask.Name
		}
		if serviceTask.Implementation != "" {
			serviceRef = serviceTask.Implementation
		} else if serviceTask.Class != "" {
			serviceRef = serviceTask.Class
		} else if serviceTask.DelegateExpression != "" {
			serviceRef = serviceTask.DelegateExpression
		} else if serviceTask.OperationRef != "" {
			serviceRef = serviceTask.OperationRef
		}

		if e.callbackRegistry != nil {
			handler := e.callbackRegistry.GetHandler(serviceRef)
			if handler == nil {
				handler = e.callbackRegistry.GetHandler(serviceTask.GetType())
			}
			if handler != nil {
				e.logger.Infow("执行 ServiceTask 回调", "serviceRef", serviceRef, "elementID", elementID)
				taskVariables := mergeServiceTaskVariables(instance.Variables, serviceTask)
				if _, err := handler.Execute(ctx, nil, taskVariables); err != nil {
					return fmt.Errorf("ServiceTask %s 执行失败: %w", serviceRef, err)
				}
			} else {
				e.logger.Warnw("未注册的 ServiceTask，跳过执行", "serviceRef", serviceRef, "elementID", elementID)
			}
		}
		return e.executeStep(ctx, instance, process, elementID, instance.Variables)
	}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run "TestBPMNServiceTask_ServiceTaskType|TestHandleElement_ServiceTask_DispatchesByMetaData|TestBPMNProcessEngine_FindServiceTask|TestMergeServiceTaskVariables" -v`
Expected: PASS，且既有的 `TestBPMNProcessEngine_FindServiceTask`/`TestMergeServiceTaskVariables` 不受影响。

- [ ] **Step 7: 跑整个 service 包确认没有破坏别的用例（尤其是变更域，它的 UserTask 分发路径完全没动，但共用了同一批常量/helper）**

Run: `cd itsm-backend && go build ./... && go test ./service/... -v 2>&1 | tail -60`
Expected: 全部 PASS。

- [ ] **Step 8: 提交**

```bash
cd itsm-backend
git add service/bpmn_types.go service/bpmn_process_engine.go service/bpmn_process_engine_ext_test.go
git commit -m "fix(bpmn): serviceTask 补 extensionElements 解析，metaData 声明的类型优先按注册表分发"
```

---

### Task 4b: GenericServiceTaskHandler 补真实动作

**Files:**
- Modify: `itsm-backend/service/bpmn/generic_handler.go`
- Test: `itsm-backend/service/bpmn/generic_handler_test.go`（新建）

**Interfaces:**
- Consumes: Task 4a 产出的 metaData 分发（`variables[bpmnMetaDataAction]` 会被注入进 `variables["action"]`——注意 `bpmnMetaDataAction` 常量值就是字符串 `"action"`，见 `bpmn_types.go`）
- Consumes（已验证的真实写入模式，抄自 `service/bpmn/cc_handler.go` 的 `createCCNotifications`）: `h.client.TicketNotification.Create()...Save(ctx)`、`h.client.Notification.Create()...Save(ctx)`
- Consumes: `h.client.Ticket.UpdateOneID(id).Where(ticket.TenantID(tenantID)).SetStatus(...).Save(ctx)`（抄自 `TicketServiceTaskHandler.updateTicketStatus`）
- 覆盖的真实模板动作（已在 `.bpmn` 文件里核实存在）: `notify_rejection`（`service_request_flow.bpmn`/`service_request_urgent_flow.bpmn` 的 Activity_RejectNotify）、`complete_service`（同两个模板的 Activity_Complete）、`notify`（`incident_emergency_flow.bpmn` 的 Activity_Notify）、`update_kb`（`problem_management_flow.bpmn` 的 Activity_KnowledgeBase——范围说明见 Step 3 注释）
- 未匹配到已知 action 时保留原有透传行为（不破坏未来自定义模板用 `generic_task` 做变量透传的用法）

- [ ] **Step 1: 写失败测试**

新建 `itsm-backend/service/bpmn/generic_handler_test.go`：

```go
package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketnotification"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupGenericHandlerFixture(t *testing.T) (*ent.Client, *GenericServiceTaskHandler, int, *ent.Ticket) {
	client := enttest.Open(t, "sqlite3", "file:generic_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("gh-1").SetDomain("gh-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester-gh").SetEmail("requester-gh@test.com").SetPasswordHash("x").
		SetName("申请人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	tkt, err := client.Ticket.Create().
		SetTitle("生成通用handler测试工单").SetTicketNumber("T-GH-1").SetStatus("open").
		SetRequesterID(requester.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewGenericServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	return client, handler, tenant.ID, tkt
}

func TestGenericServiceTaskHandler_CompleteService_ResolvesTicket(t *testing.T) {
	client, handler, tenantID, tkt := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "complete_service",
		"business_id": float64(tkt.ID),
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	updated, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "resolved", updated.Status)
	assert.NotNil(t, updated.ResolvedAt)
}

func TestGenericServiceTaskHandler_NotifyRejection_CreatesNotificationForRequester(t *testing.T) {
	client, handler, tenantID, tkt := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "notify_rejection",
		"business_id": float64(tkt.ID),
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	count, err := client.TicketNotification.Query().
		Where(ticketnotification.TicketID(tkt.ID), ticketnotification.UserID(tkt.RequesterID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "应该给申请人真实创建一条驳回通知，不是只打日志")
}

func TestGenericServiceTaskHandler_Notify_CreatesNotificationForRequester(t *testing.T) {
	client, handler, tenantID, tkt := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "notify",
		"business_id": float64(tkt.ID),
	})
	require.NoError(t, err)

	count, err := client.TicketNotification.Query().
		Where(ticketnotification.TicketID(tkt.ID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestGenericServiceTaskHandler_UnknownAction_KeepsPassthroughBehavior(t *testing.T) {
	_, handler, tenantID, _ := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "some_future_custom_action",
		"foo":    "bar",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "bar", result.OutputVars["foo"], "未识别的 action 应该保留原有透传行为，不破坏自定义模板")
}

func TestGenericServiceTaskHandler_MissingBusinessID_ReturnsError(t *testing.T) {
	_, handler, tenantID, _ := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "complete_service"})
	assert.Error(t, err)
}
```

先用 `Read` 确认 `ticket` 包和 `ticketnotification` 包的实际 import 路径、`TicketNotification` 的字段访问器名字（`TicketID`/`UserID` 这两个 predicate 名跟 `cc_handler.go` 里 `SetTicketID`/`SetUserID` 对应），以及本包（`service/bpmn`）里 `BPMNTenantIDContextKey` 的实际定义位置（应该已经在 `handler_base.go` 或类似文件里，直接引用不用重新定义）。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/bpmn/... -run TestGenericServiceTaskHandler -v`
Expected: FAIL —— `complete_service`/`notify_rejection`/`notify` 分支不存在，走的是默认透传逻辑，`updated.Status` 断言失败。

- [ ] **Step 3: 实现**

把 `itsm-backend/service/bpmn/generic_handler.go` 整个替换为：

```go
package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

// GenericServiceTaskHandler 通用服务任务处理器
type GenericServiceTaskHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewGenericServiceTaskHandler 创建通用处理器
func NewGenericServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *GenericServiceTaskHandler {
	return &GenericServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *GenericServiceTaskHandler) GetTaskType() string {
	return "generic_task"
}

// GetHandlerID 返回处理器标识
func (h *GenericServiceTaskHandler) GetHandlerID() string {
	return "generic_service_handler"
}

// Execute 执行通用服务任务。已知的 action 对应内置模板（service_request_flow /
// service_request_urgent_flow / incident_emergency_flow）里真实声明的动作，做真实的
// Ticket/Notification 写入；未识别的 action 保留原有的变量透传行为，不强行猜测语义——
// generic_task 这个类型本身的定位就是给未来自定义模板留的通用出口。
func (h *GenericServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "complete_service":
		return h.completeService(ctx, variables)
	case "notify_rejection":
		return h.notifyRequester(ctx, variables, "服务请求已被驳回")
	case "notify":
		return h.notifyRequester(ctx, variables, "有新的处理进展，请查看")
	default:
		operation, _ := variables["operation"].(string)
		result := &dto.ServiceTaskResult{
			Success:    true,
			Message:    fmt.Sprintf("通用任务 %s 执行完成", operation),
			OutputVars: make(map[string]interface{}),
		}
		for k, v := range variables {
			result.OutputVars[k] = v
		}
		return result, nil
	}
}

// Validate 验证配置
func (h *GenericServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// completeService 对应"服务完成"节点（service_request_flow.bpmn 的 Activity_Complete）：
// 把关联的工单状态置为 resolved，跟 TicketServiceTaskHandler.updateTicketStatus 同款写法。
func (h *GenericServiceTaskHandler) completeService(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	ticketID := GetIntFromVars(variables, "business_id")
	if ticketID <= 0 {
		return nil, fmt.Errorf("无效的 business_id")
	}
	tenantID := GetTenantIDFromVars(variables)
	update := h.client.Ticket.UpdateOneID(ticketID)
	if tenantID > 0 {
		update = update.Where(ticket.TenantID(tenantID))
	}
	if _, err := update.SetStatus("resolved").SetResolvedAt(time.Now()).SetUpdatedAt(time.Now()).Save(ctx); err != nil {
		return nil, fmt.Errorf("完成服务请求失败: %w", err)
	}
	h.logger.Infow("Service request completed via BPMN generic handler", "ticket_id", ticketID)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("工单 %d 已完成", ticketID)}, nil
}

// notifyRequester 对应"驳回通知"/"通知相关方"这类纯通知节点：给工单申请人真实创建一条
// 通知（站内消息 + 统一 Notification），不是只打日志。写法直接抄
// CCTaskHandler.createCCNotifications 已经验证过的模式。
func (h *GenericServiceTaskHandler) notifyRequester(ctx context.Context, variables map[string]interface{}, defaultContent string) (*dto.ServiceTaskResult, error) {
	ticketID := GetIntFromVars(variables, "business_id")
	if ticketID <= 0 {
		return nil, fmt.Errorf("无效的 business_id")
	}
	tenantID := GetTenantIDFromVars(variables)

	ticketEntity, err := h.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("获取工单失败: %w", err)
	}

	content := defaultContent
	if reason, ok := variables["reject_reason"].(string); ok && reason != "" {
		content = fmt.Sprintf("%s：%s", defaultContent, reason)
	}
	content = fmt.Sprintf("工单 %s「%s」：%s", ticketEntity.TicketNumber, ticketEntity.Title, content)

	now := time.Now()
	if _, err := h.client.TicketNotification.Create().
		SetTicketID(ticketID).
		SetUserID(ticketEntity.RequesterID).
		SetType("workflow").
		SetChannel("in_app").
		SetContent(content).
		SetTenantID(tenantID).
		SetStatus("sent").
		SetSentAt(now).
		Save(ctx); err != nil {
		return nil, fmt.Errorf("创建工单通知失败: %w", err)
	}
	if _, err := h.client.Notification.Create().
		SetTitle("工单进展通知").
		SetMessage(content).
		SetType("info").
		SetUserID(ticketEntity.RequesterID).
		SetTenantID(tenantID).
		SetActionURL(fmt.Sprintf("/tickets/%d", ticketID)).
		SetActionText("查看工单").
		Save(ctx); err != nil {
		h.logger.Warnw("Failed to create unified notification via BPMN generic handler", "error", err, "ticket_id", ticketID)
	}

	return &dto.ServiceTaskResult{Success: true, Message: "通知已发送"}, nil
}

// 确保 GenericServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*GenericServiceTaskHandler)(nil)
```

范围说明（写进这个 commit 的 body 里，不是代码注释）：`update_kb`（问题管理流程的"更新知识库"节点）没有在这次一起做真实实现——它需要 Problem 域和 Knowledge 域之间的关联写入，这次的事实核查没有覆盖 Knowledge 域 schema，贸然写会是没有验证过的猜测代码。`update_kb` 目前会走上面 `default` 分支（透传变量、返回成功），行为跟改动前一致，没有变得更差，只是没有变得更好——留给后续单独核实 Knowledge 域 schema 之后再做。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/bpmn/... -run TestGenericServiceTaskHandler -v`
Expected: PASS

- [ ] **Step 5: 跑整个 bpmn 包确认没有破坏别的 handler 测试**

Run: `cd itsm-backend && go build ./... && go test ./service/bpmn/... -v 2>&1 | tail -80`
Expected: 全部 PASS。

- [ ] **Step 6: 提交**

```bash
cd itsm-backend
git add service/bpmn/generic_handler.go service/bpmn/generic_handler_test.go
git commit -m "fix(bpmn): GenericServiceTaskHandler 补真实动作（服务完成/驳回通知/相关方通知），未识别action保留透传兜底"
```

---

### Task 4c: ServiceRequestServiceTaskHandler 补真实动作

**Files:**
- Modify: `itsm-backend/service/bpmn/service_request_handler.go`
- Test: `itsm-backend/service/bpmn/service_request_handler_test.go`（新建）

**Interfaces:**
- Consumes: `ent.ServiceRequest` schema 字段（`ticket_id`/`processor_id`/`started_at`/`completed_at`/`completion_note`/`last_error`），`h.client.Ticket.UpdateOneID(...)`（同 Task 4b 的写法，用于把状态变化同步到关联工单，因为 ServiceRequest 自己没有 status 字段，状态委托给关联 Ticket——见 `ent/schema/servicerequest.go` 的字段注释）
- 说明：该 handler 的 `GetTaskType()` 目前返回 `"service_request_task"`，没有任何已发布模板的 `service_task_type` 用这个值（都用 `generic_task`/`ticket_task` 代替了），这次改动让它对"未来会用到这个类型的自定义模板"是真实可用的，不依赖任何现存模板才能验证——测试直接调用 `Execute`，不需要走完整 BPMN 流程

- [ ] **Step 1: 写失败测试**

新建 `itsm-backend/service/bpmn/service_request_handler_test.go`：

```go
package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/servicerequest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupServiceRequestHandlerFixture(t *testing.T) (*ent.Client, *ServiceRequestServiceTaskHandler, int, *ent.Ticket, *ent.ServiceRequest) {
	client := enttest.Open(t, "sqlite3", "file:service_request_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("srh-1").SetDomain("srh-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester-srh").SetEmail("requester-srh@test.com").SetPasswordHash("x").
		SetName("申请人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	tkt, err := client.Ticket.Create().
		SetTitle("服务请求关联工单").SetTicketNumber("T-SRH-1").SetStatus("open").
		SetRequesterID(requester.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	sr, err := client.ServiceRequest.Create().
		SetTenantID(tenant.ID).SetTicketID(tkt.ID).SetCatalogID(1).SetRequesterID(requester.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewServiceRequestServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	return client, handler, tenant.ID, tkt, sr
}

func TestServiceRequestHandler_AssignRequest_SetsProcessor(t *testing.T) {
	client, handler, tenantID, _, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "assign_request",
		"request_id":  float64(sr.ID),
		"assignee_id": float64(42),
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	updated, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.ProcessorID)
	assert.Equal(t, 42, *updated.ProcessorID)
}

func TestServiceRequestHandler_CompleteRequest_UpdatesRequestAndLinkedTicket(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":          "complete_request",
		"request_id":      float64(sr.ID),
		"completion_note": "已开通",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	updatedSR, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedSR.CompletedAt)
	assert.Equal(t, "已开通", updatedSR.CompletionNote)

	updatedTicket, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "resolved", updatedTicket.Status)
}

func TestServiceRequestHandler_RejectRequest_UpdatesLinkedTicketStatus(t *testing.T) {
	client, handler, tenantID, tkt, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":        "reject_request",
		"request_id":    float64(sr.ID),
		"reject_reason": "预算不足",
	})
	require.NoError(t, err)

	updatedTicket, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "closed", updatedTicket.Status)

	updatedSR, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	assert.Contains(t, updatedSR.CompletionNote, "预算不足")
	_ = tkt
}

func TestServiceRequestHandler_ProvisionResource_SetsStartedAt(t *testing.T) {
	client, handler, tenantID, _, sr := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":        "provision_resource",
		"request_id":    float64(sr.ID),
		"resource_type": "vm",
	})
	require.NoError(t, err)

	updated, err := client.ServiceRequest.Get(ctx, sr.ID)
	require.NoError(t, err)
	assert.NotNil(t, updated.StartedAt)
}

func TestServiceRequestHandler_CreateRequest_ReturnsExplicitUnsupportedError(t *testing.T) {
	_, handler, tenantID, _, _ := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "create_request",
		"title":  "新请求",
	})
	require.Error(t, err, "服务请求在流程启动前就已经存在（先创建 ServiceRequest 才会触发 BPMN），"+
		"从流程内部再\"创建\"一个于架构不符——这里应该是明确报错而不是假装成功")
}

func TestServiceRequestHandler_InvalidRequestID_ReturnsError(t *testing.T) {
	_, handler, tenantID, _, _ := setupServiceRequestHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "assign_request"})
	assert.Error(t, err)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/bpmn/... -run TestServiceRequestHandler -v`
Expected: FAIL —— 现有实现全部只打日志返回固定成功，`updated.ProcessorID`/`CompletedAt` 等断言会失败，`create_request` 不会返回 error。

- [ ] **Step 3: 实现**

把 `itsm-backend/service/bpmn/service_request_handler.go` 整个替换为：

```go
package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

// ServiceRequestServiceTaskHandler 服务请求服务任务处理器
type ServiceRequestServiceTaskHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewServiceRequestServiceTaskHandler 创建服务请求处理器
func NewServiceRequestServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *ServiceRequestServiceTaskHandler {
	return &ServiceRequestServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *ServiceRequestServiceTaskHandler) GetTaskType() string {
	return "service_request_task"
}

// GetHandlerID 返回处理器标识
func (h *ServiceRequestServiceTaskHandler) GetHandlerID() string {
	return "service_request_handler"
}

// Execute 执行服务请求任务。ServiceRequest 自身没有 status 字段——状态/审批/工作流全部
// 委托给关联的 Ticket（见 ent/schema/servicerequest.go 的字段注释），所以这里凡是涉及
// "状态"语义的动作都改成更新关联 Ticket 的状态，跟 GenericServiceTaskHandler 的写法一致；
// ServiceRequest 自己的字段（processor_id/started_at/completed_at/completion_note）
// 只用来记录资源交付过程本身的信息。
func (h *ServiceRequestServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "create_request":
		return nil, fmt.Errorf("服务请求必须先通过服务目录申请创建，流程实例只能在请求已存在之后触发——不支持从流程内部创建新请求")
	case "update_request":
		return h.updateRequest(ctx, variables)
	case "approve_request":
		return h.setLinkedTicketStatus(ctx, variables, "in_progress", "")
	case "reject_request":
		reason, _ := variables["reject_reason"].(string)
		return h.rejectRequest(ctx, variables, reason)
	case "assign_request":
		return h.assignRequest(ctx, variables)
	case "provision_resource":
		return h.provisionResource(ctx, variables)
	case "complete_request":
		return h.completeRequest(ctx, variables)
	case "cancel_request":
		reason, _ := variables["cancel_reason"].(string)
		return h.cancelRequest(ctx, variables, reason)
	default:
		return &dto.ServiceTaskResult{Success: true, Message: "无操作执行"}, nil
	}
}

// Validate 验证配置
func (h *ServiceRequestServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// getServiceRequest 按 request_id + 租户取出服务请求，找不到时返回明确错误。
func (h *ServiceRequestServiceTaskHandler) getServiceRequest(ctx context.Context, variables map[string]interface{}) (*ent.ServiceRequest, int, error) {
	requestID := GetIntFromVars(variables, "request_id")
	if requestID <= 0 {
		return nil, 0, fmt.Errorf("无效的请求ID")
	}
	tenantID := GetTenantIDFromVars(variables)
	query := h.client.ServiceRequest.Query().Where(servicerequest.ID(requestID))
	if tenantID > 0 {
		query = query.Where(servicerequest.TenantID(tenantID))
	}
	sr, err := query.Only(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("查询服务请求失败: %w", err)
	}
	return sr, tenantID, nil
}

func (h *ServiceRequestServiceTaskHandler) updateRequest(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	sr, _, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	h.logger.Infow("Service request updated via BPMN", "request_id", sr.ID)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("服务请求 %d 已更新", sr.ID)}, nil
}

// setLinkedTicketStatus 把服务请求关联工单的状态改成 newStatus，可选附一条完成备注。
func (h *ServiceRequestServiceTaskHandler) setLinkedTicketStatus(ctx context.Context, variables map[string]interface{}, newStatus, note string) (*dto.ServiceTaskResult, error) {
	sr, tenantID, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	update := h.client.Ticket.UpdateOneID(sr.TicketID)
	if tenantID > 0 {
		update = update.Where(ticket.TenantID(tenantID))
	}
	if newStatus == "resolved" || newStatus == "closed" {
		update = update.SetResolvedAt(time.Now())
	}
	if _, err := update.SetStatus(newStatus).SetUpdatedAt(time.Now()).Save(ctx); err != nil {
		return nil, fmt.Errorf("更新关联工单状态失败: %w", err)
	}
	if note != "" {
		if _, err := sr.Update().SetCompletionNote(note).Save(ctx); err != nil {
			return nil, fmt.Errorf("记录服务请求备注失败: %w", err)
		}
	}
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("服务请求 %d 对应工单状态已更新为 %s", sr.ID, newStatus)}, nil
}

func (h *ServiceRequestServiceTaskHandler) rejectRequest(ctx context.Context, variables map[string]interface{}, reason string) (*dto.ServiceTaskResult, error) {
	note := reason
	if note == "" {
		note = "已驳回"
	}
	return h.setLinkedTicketStatus(ctx, variables, "closed", note)
}

func (h *ServiceRequestServiceTaskHandler) assignRequest(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	sr, _, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	assigneeID := GetIntFromVars(variables, "assignee_id")
	if assigneeID <= 0 {
		return nil, fmt.Errorf("无效的 assignee_id")
	}
	if _, err := sr.Update().SetProcessorID(assigneeID).Save(ctx); err != nil {
		return nil, fmt.Errorf("分配服务请求失败: %w", err)
	}
	h.logger.Infow("Service request assigned via BPMN", "request_id", sr.ID, "assignee_id", assigneeID)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("服务请求 %d 已分配", sr.ID)}, nil
}

func (h *ServiceRequestServiceTaskHandler) provisionResource(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	sr, _, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	resourceType, _ := variables["resource_type"].(string)
	if _, err := sr.Update().SetStartedAt(time.Now()).Save(ctx); err != nil {
		return nil, fmt.Errorf("记录资源开通开始时间失败: %w", err)
	}
	h.logger.Infow("Resource provisioning via BPMN", "request_id", sr.ID, "resource_type", resourceType)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("资源 %s 开始供应", resourceType)}, nil
}

func (h *ServiceRequestServiceTaskHandler) completeRequest(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	sr, tenantID, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	completionNote, _ := variables["completion_note"].(string)
	if _, err := sr.Update().SetCompletedAt(time.Now()).SetCompletionNote(completionNote).Save(ctx); err != nil {
		return nil, fmt.Errorf("记录服务请求完成信息失败: %w", err)
	}
	update := h.client.Ticket.UpdateOneID(sr.TicketID)
	if tenantID > 0 {
		update = update.Where(ticket.TenantID(tenantID))
	}
	if _, err := update.SetStatus("resolved").SetResolvedAt(time.Now()).SetUpdatedAt(time.Now()).Save(ctx); err != nil {
		return nil, fmt.Errorf("更新关联工单状态失败: %w", err)
	}
	h.logger.Infow("Service request completed via BPMN", "request_id", sr.ID)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("服务请求 %d 已完成", sr.ID)}, nil
}

func (h *ServiceRequestServiceTaskHandler) cancelRequest(ctx context.Context, variables map[string]interface{}, reason string) (*dto.ServiceTaskResult, error) {
	note := reason
	if note == "" {
		note = "已取消"
	}
	return h.setLinkedTicketStatus(ctx, variables, "closed", note)
}

// 确保 ServiceRequestServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*ServiceRequestServiceTaskHandler)(nil)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/bpmn/... -run TestServiceRequestHandler -v`
Expected: PASS

- [ ] **Step 5: 跑整个 bpmn 包**

Run: `cd itsm-backend && go build ./... && go test ./service/bpmn/... -v 2>&1 | tail -100`
Expected: 全部 PASS。

- [ ] **Step 6: 提交**

```bash
cd itsm-backend
git add service/bpmn/service_request_handler.go service/bpmn/service_request_handler_test.go
git commit -m "fix(bpmn): ServiceRequestServiceTaskHandler 补真实实现，状态变化同步到关联工单"
```

---

### Task 4d: 新建 ReleaseServiceTaskHandler 并注册

**Files:**
- Create: `itsm-backend/service/bpmn/release_handler.go`
- Test: `itsm-backend/service/bpmn/release_handler_test.go`（新建）
- Modify: `itsm-backend/service/bpmn/bpmn_callback_registry.go`

**Interfaces:**
- Produces: `NewReleaseServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *ReleaseServiceTaskHandler`，`GetTaskType() == "release_task"`，`GetHandlerID() == "release_service_handler"`
- Consumes: `dto.ReleaseStatus*` 常量（`draft/scheduled/in-progress/completed/cancelled/failed/rolled_back`）；`ent/release` 生成的查询谓词
- **重要的包依赖约束**：`service/bpmn` 包不能 import `itsm-backend/service`——`service` 包本身在 `bpmn_process_engine.go` 里 import 了 `itsm-backend/service/bpmn`（`callbackRegistry *bpmn.CallbackRegistry`），反向 import 会形成循环依赖，编译不过。所以这个 handler **不能**直接调用 `service/release_service.go` 的 `ReleaseService.UpdateReleaseStatus`，只能直接操作 Ent，并在本文件内复制一份状态机白名单校验（参照 `ChangeServiceTaskHandler` 已经采用的"和 `handlers/change` 手动保持同步"注释约定）。
- 5 个节点的 action 值（已在 `release_approval_flow.bpmn` 核实）：`tech_review`、`approval`、`schedule`、`execute`、`verify`
- 关键设计约束：`approval` 动作**不**在这个 handler 里做状态转换——`ReleaseService.ApplyReleaseApproval`（审批的真正业务入口）会先调用 `approvalBridge.CompleteBusinessApprovalTask` 完成对应的 BPMN 任务（这一步会触发 `dispatchUserTaskCallback` 从而进到这个 handler），然后才在自己函数体后半段把 `Release.Status` 设成 scheduled/cancelled——也就是说 handler 执行 `approval` 动作的这一刻，权威的状态转换还没发生，这里再猜一次目标状态是多余且有出错风险的（不知道这次是 approve 还是 reject）。这个动作在这个 handler 里必须是有文档说明的空操作，不是缺陷。

- [ ] **Step 1: 写失败测试**

新建 `itsm-backend/service/bpmn/release_handler_test.go`：

```go
package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupReleaseHandlerFixture(t *testing.T) (*ent.Client, *ReleaseServiceTaskHandler, int, *ent.Release) {
	client := enttest.Open(t, "sqlite3", "file:release_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("rh-1").SetDomain("rh-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	creator, err := client.User.Create().
		SetUsername("creator-rh").SetEmail("creator-rh@test.com").SetPasswordHash("x").
		SetName("发布负责人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	release, err := client.Release.Create().
		SetReleaseNumber("REL-RH-1").SetTitle("测试发布").SetStatus("draft").
		SetCreatedBy(creator.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewReleaseServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	return client, handler, tenant.ID, release
}

func TestReleaseHandler_TechReview_AppendsReleaseNotes(t *testing.T) {
	client, handler, tenantID, release := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "tech_review",
		"business_id": float64(release.ID),
		"comment":     "技术评审通过，无阻塞项",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	updated, err := client.Release.Get(ctx, release.ID)
	require.NoError(t, err)
	assert.Contains(t, updated.ReleaseNotes, "技术评审通过，无阻塞项")
	assert.Equal(t, "draft", updated.Status, "技术评审不改变发布状态")
}

func TestReleaseHandler_Approval_IsDocumentedNoop(t *testing.T) {
	_, handler, tenantID, release := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "approval",
		"business_id": float64(release.ID),
	})
	require.NoError(t, err, "approval 动作是有意的空操作，权威状态转换在 ReleaseService.ApplyReleaseApproval 里")
	assert.True(t, result.Success)
}

func TestReleaseHandler_Execute_AdvancesThroughStatuses(t *testing.T) {
	client, handler, tenantID, release := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	// 手工把状态先推到 scheduled，模拟 approval 动作已经在 ApplyReleaseApproval 里
	// 真正生效之后的状态——execute/verify 动作要在这个前提下工作。
	_, err := client.Release.UpdateOneID(release.ID).SetStatus("scheduled").Save(ctx)
	require.NoError(t, err)

	_, err = handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "execute",
		"business_id": float64(release.ID),
	})
	require.NoError(t, err)
	afterExecute, err := client.Release.Get(ctx, release.ID)
	require.NoError(t, err)
	assert.Equal(t, "in-progress", afterExecute.Status)

	_, err = handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "verify",
		"business_id": float64(release.ID),
	})
	require.NoError(t, err)
	afterVerify, err := client.Release.Get(ctx, release.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", afterVerify.Status)
	assert.False(t, afterVerify.ActualReleaseDate.IsZero())
}

func TestReleaseHandler_Schedule_IsIdempotentOnAlreadyScheduled(t *testing.T) {
	client, handler, tenantID, release := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := client.Release.UpdateOneID(release.ID).SetStatus("scheduled").Save(ctx)
	require.NoError(t, err)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "schedule",
		"business_id": float64(release.ID),
	})
	require.NoError(t, err, "已经是 scheduled 时重复调用应该是幂等成功，不是状态机错误")
	assert.True(t, result.Success)
}

func TestReleaseHandler_InvalidBusinessID_ReturnsError(t *testing.T) {
	_, handler, tenantID, _ := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "execute"})
	assert.Error(t, err)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/bpmn/... -run TestReleaseHandler -v`
Expected: FAIL —— `NewReleaseServiceTaskHandler` 不存在（编译错误）。

- [ ] **Step 3: 实现 `ReleaseServiceTaskHandler`**

新建 `itsm-backend/service/bpmn/release_handler.go`：

```go
package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/release"

	"go.uber.org/zap"
)

// ReleaseServiceTaskHandler 发布服务任务处理器，对应 release_approval_flow.bpmn 的 5 个
// 用户任务节点（技术评审/发布审批/计划发布/执行发布/验证确认，metaData 都声明
// service_task_type=release_task）。之前完全没有注册处理器，这 5 个节点走完流程
// 对 Release 实体零真实副作用。
//
// 状态转换直接操作 Ent，不能调用 service/release_service.go 的
// ReleaseService.UpdateReleaseStatus——service 包本身依赖 service/bpmn 做 callback
// 注册（bpmn_process_engine.go 里的 callbackRegistry *bpmn.CallbackRegistry），
// 反向依赖会导致 import 循环。状态机白名单校验规则复制自 isValidReleaseStatusTransition
// （release_service.go），改动状态机规则时两处要一起改。
type ReleaseServiceTaskHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewReleaseServiceTaskHandler 创建发布处理器
func NewReleaseServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *ReleaseServiceTaskHandler {
	return &ReleaseServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *ReleaseServiceTaskHandler) GetTaskType() string {
	return "release_task"
}

// GetHandlerID 返回处理器标识
func (h *ReleaseServiceTaskHandler) GetHandlerID() string {
	return "release_service_handler"
}

// Execute 执行发布任务
func (h *ReleaseServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "tech_review":
		return h.techReview(ctx, variables)
	case "approval":
		// 有意的空操作：ReleaseService.ApplyReleaseApproval 是审批的真正业务入口，
		// 它会先桥接完成这个 BPMN 任务（触发本方法执行），再在自己函数体里把
		// Release.Status 转到 scheduled/cancelled——这一刻权威状态还没写，这里没有
		// 足够信息（不知道是 approve 还是 reject）也不需要重复做这件事。
		return &dto.ServiceTaskResult{Success: true, Message: "审批决策由 ReleaseService.ApplyReleaseApproval 统一处理"}, nil
	case "schedule":
		return h.updateStatus(ctx, variables, string(dto.ReleaseStatusScheduled))
	case "execute":
		return h.updateStatus(ctx, variables, string(dto.ReleaseStatusInProgress))
	case "verify":
		return h.updateStatus(ctx, variables, string(dto.ReleaseStatusCompleted))
	default:
		return &dto.ServiceTaskResult{Success: true, Message: "无操作执行"}, nil
	}
}

// Validate 验证配置
func (h *ReleaseServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

func (h *ReleaseServiceTaskHandler) releaseID(variables map[string]interface{}) (int, error) {
	id := GetIntFromVars(variables, "business_id")
	if id <= 0 {
		return 0, fmt.Errorf("无效的 business_id")
	}
	return id, nil
}

// techReview 记录技术评审意见。评审通过/不通过在这个流程设计里不对应独立的发布状态
// （Release.status 只有 draft/scheduled/in-progress/completed/cancelled/failed/
// rolled_back），所以这里只追加评审记录到 release_notes，不改状态。
func (h *ReleaseServiceTaskHandler) techReview(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	releaseID, err := h.releaseID(variables)
	if err != nil {
		return nil, err
	}
	tenantID := GetTenantIDFromVars(variables)
	comment, _ := variables["comment"].(string)

	entity, err := h.client.Release.Get(ctx, releaseID)
	if err != nil {
		return nil, fmt.Errorf("获取发布记录失败: %w", err)
	}
	if tenantID > 0 && entity.TenantID != tenantID {
		return nil, fmt.Errorf("发布记录不存在")
	}
	notes := entity.ReleaseNotes
	if comment != "" {
		if notes != "" {
			notes += "\n"
		}
		notes += fmt.Sprintf("[技术评审] %s", comment)
	}
	if _, err := entity.Update().SetReleaseNotes(notes).Save(ctx); err != nil {
		return nil, fmt.Errorf("记录技术评审失败: %w", err)
	}
	h.logger.Infow("Release tech review recorded via BPMN", "release_id", releaseID)
	return &dto.ServiceTaskResult{Success: true, Message: "技术评审意见已记录"}, nil
}

func (h *ReleaseServiceTaskHandler) updateStatus(ctx context.Context, variables map[string]interface{}, status string) (*dto.ServiceTaskResult, error) {
	releaseID, err := h.releaseID(variables)
	if err != nil {
		return nil, err
	}
	tenantID := GetTenantIDFromVars(variables)

	query := h.client.Release.Query().Where(release.ID(releaseID))
	if tenantID > 0 {
		query = query.Where(release.TenantID(tenantID))
	}
	current, err := query.Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询发布记录失败: %w", err)
	}

	if current.Status != status && !isValidReleaseStatusTransitionForBPMN(current.Status, status) {
		return nil, fmt.Errorf("非法的发布状态转换: %s -> %s", current.Status, status)
	}

	update := current.Update().SetStatus(status)
	if status == string(dto.ReleaseStatusCompleted) {
		update = update.SetActualReleaseDate(time.Now())
	}
	if _, err := update.Save(ctx); err != nil {
		return nil, fmt.Errorf("更新发布状态失败: %w", err)
	}

	h.logger.Infow("Release status updated via BPMN", "release_id", releaseID, "status", status)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("发布 %d 状态已更新为 %s", releaseID, status)}, nil
}

// isValidReleaseStatusTransitionForBPMN 复制自 service/release_service.go 的
// isValidReleaseStatusTransition。service/bpmn 包不能依赖 service 包（见上方类型
// 注释的循环依赖说明），只能在这里独立维护一份同款规则。
func isValidReleaseStatusTransitionForBPMN(current, newStatus string) bool {
	if current == newStatus {
		return true
	}
	transitions := map[string]map[string]struct{}{
		string(dto.ReleaseStatusDraft): {
			string(dto.ReleaseStatusScheduled): {},
			string(dto.ReleaseStatusCancelled): {},
		},
		string(dto.ReleaseStatusScheduled): {
			string(dto.ReleaseStatusInProgress): {},
			string(dto.ReleaseStatusCancelled):  {},
		},
		string(dto.ReleaseStatusInProgress): {
			string(dto.ReleaseStatusCompleted):  {},
			string(dto.ReleaseStatusFailed):     {},
			string(dto.ReleaseStatusRolledBack): {},
			string(dto.ReleaseStatusCancelled):  {},
		},
		string(dto.ReleaseStatusFailed): {
			string(dto.ReleaseStatusScheduled):  {},
			string(dto.ReleaseStatusRolledBack): {},
			string(dto.ReleaseStatusCancelled):  {},
		},
		string(dto.ReleaseStatusCompleted):  {},
		string(dto.ReleaseStatusCancelled):  {},
		string(dto.ReleaseStatusRolledBack): {},
	}
	allowed, ok := transitions[current]
	if !ok {
		return false
	}
	_, ok = allowed[newStatus]
	return ok
}

// 确保 ReleaseServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*ReleaseServiceTaskHandler)(nil)
```

- [ ] **Step 4: 注册到 callback registry**

在 `itsm-backend/service/bpmn/bpmn_callback_registry.go` 的 `registerDefaultHandlers()` 里，`r.RegisterHandler(NewWebhookHandler(r.client, r.logger))` 那一行之后新增：

```go
	// 注册发布服务任务处理器
	r.RegisterHandler(NewReleaseServiceTaskHandler(r.client, r.logger))
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/bpmn/... -run TestReleaseHandler -v`
Expected: PASS

- [ ] **Step 6: 跑整个 bpmn 包，确认 registry 相关测试（如果有列出 handler 数量的测试）没有因为多注册了一个 handler 而炸**

Run: `cd itsm-backend && go build ./... && go test ./service/bpmn/... -v 2>&1 | tail -100`
Expected: 全部 PASS。如果有类似 `TestCallbackRegistry_ListHandlers` 之类断言 handler 总数的测试因为多了一个而失败，把断言里的数字加 1（这是预期内的行为变化，不是回归）。

- [ ] **Step 7: 提交**

```bash
cd itsm-backend
git add service/bpmn/release_handler.go service/bpmn/release_handler_test.go service/bpmn/bpmn_callback_registry.go
git commit -m "feat(bpmn): 新增 ReleaseServiceTaskHandler，release_approval_flow 的5个节点补上真实状态转换"
```

---

### Task 5: 自定义字段类型校验

**Files:**
- Modify: `itsm-backend/service/field_value_service.go`
- Test: `itsm-backend/service/field_value_service_test.go`
- Modify: `itsm-backend/service/ticket_template_service.go`
- Test: `itsm-backend/service/ticket_template_service_test.go`

**Interfaces:**
- Produces: `isValidFieldType(fieldType string) bool`（新增包级函数，`service` 包内，供 `field_value_service.go` 和 `ticket_template_service.go` 共用）
- Produces: `CreateValues`/`CreateAdHocValues` 在存值前对已知类型做格式校验，校验失败时整体失败（原有事务式写入语义不变，失败即 rollback）
- Produces: `validateTemplateFields` 对每个字段的 `FieldType` 做允许值校验

- [ ] **Step 1: 写失败测试（类型允许值校验）**

在 `itsm-backend/service/ticket_template_service_test.go` 里追加（先用 `Read` 确认这个文件是否已存在、以及测试用的辅助类型/函数名跟下面一致，不一致按实际调整）：

```go
func TestValidateTemplateFields_RejectsUnknownFieldType(t *testing.T) {
	err := validateTemplateFields([]FieldDefinitionInput{
		{Name: "weird_field", Label: "怪字段", FieldType: "banana"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "字段类型")
}

func TestValidateTemplateFields_AcceptsAllDocumentedFieldTypes(t *testing.T) {
	validTypes := []string{"text", "textarea", "number", "date", "select", "multiselect", "boolean", "file"}
	for _, ft := range validTypes {
		err := validateTemplateFields([]FieldDefinitionInput{
			{Name: "f_" + ft, Label: ft, FieldType: ft},
		})
		assert.NoError(t, err, "字段类型 %s 应该是合法的", ft)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestValidateTemplateFields -v`
Expected: FAIL —— `banana` 目前不会被拒绝。

- [ ] **Step 3: 实现字段类型允许值校验**

在 `itsm-backend/service/ticket_template_service.go` 里新增一个包级变量和校验逻辑。在 `validateTemplateFields` 函数定义之前加：

```go
// validFieldTypes 是 field_definitions.field_type 目前支持的全部取值，跟
// ent/schema/field_definition.go 里的字段注释保持一致——那边只是注释，不是真正的
// 枚举约束，这里补上运行时校验，不然拼错的类型字符串会被静默存下来，前端渲染器
// 找不到匹配分支时会退化成普通文本框。
var validFieldTypes = map[string]struct{}{
	"text":        {},
	"textarea":    {},
	"number":      {},
	"date":        {},
	"select":      {},
	"multiselect": {},
	"boolean":     {},
	"file":        {},
}

func isValidFieldType(fieldType string) bool {
	_, ok := validFieldTypes[fieldType]
	return ok
}
```

然后把 `validateTemplateFields` 函数体里的循环改成：

```go
func validateTemplateFields(fields []FieldDefinitionInput) error {
	if len(fields) > 200 {
		return fmt.Errorf("模板字段不能超过 200 个")
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if strings.TrimSpace(field.Name) == "" {
			return fmt.Errorf("模板字段名不能为空")
		}
		if _, dup := seen[field.Name]; dup {
			return fmt.Errorf("模板字段 %s 重复", field.Name)
		}
		if field.FieldType != "" && !isValidFieldType(field.FieldType) {
			return fmt.Errorf("模板字段 %s 的字段类型 %q 不合法", field.Name, field.FieldType)
		}
		seen[field.Name] = struct{}{}
	}
	return nil
}
```

（`field.FieldType != ""` 这个前置判断是因为不确定 `FieldDefinitionInput.FieldType` 是不是必填——如果读代码发现它已经在别处强制非空，可以去掉这个前置判断直接校验；用 `Read` 确认 `FieldDefinitionInput` struct 定义之后再决定。）

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestValidateTemplateFields -v`
Expected: PASS

- [ ] **Step 5: 写失败测试（存值时的格式校验）**

在 `itsm-backend/service/field_value_service_test.go` 末尾追加：

```go
func TestFieldValueService_CreateValues_RejectsNonNumericValueForNumberField(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_reject_number?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	defSvc := NewFieldDefinitionService(client)
	valSvc := NewFieldValueService(client)

	_, err := defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 40, []FieldDefinitionInput{
		{Name: "device_count", Label: "设备数量", FieldType: "number", SortOrder: 0},
	})
	require.NoError(t, err)

	err = valSvc.CreateValues(ctx, 1, "ticket_template", 40, "ticket", 400, map[string]interface{}{
		"device_count": "not-a-number",
	})
	require.Error(t, err)

	count, err := client.FieldValue.Query().Where(fieldvalue.EntityID(400)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "校验失败时不应该留下部分写入的值——事务应该整体回滚")
}

func TestFieldValueService_CreateValues_RejectsSelectValueNotInOptions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_reject_select?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	defSvc := NewFieldDefinitionService(client)
	valSvc := NewFieldValueService(client)

	_, err := defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 41, []FieldDefinitionInput{
		{
			Name: "priority_level", Label: "优先级", FieldType: "select", SortOrder: 0,
			Options: []interface{}{
				map[string]interface{}{"label": "低", "value": "low"},
				map[string]interface{}{"label": "高", "value": "high"},
			},
		},
	})
	require.NoError(t, err)

	err = valSvc.CreateValues(ctx, 1, "ticket_template", 41, "ticket", 401, map[string]interface{}{
		"priority_level": "urgent",
	})
	require.Error(t, err)
}

func TestFieldValueService_CreateValues_AcceptsValidNumberAndSelectValues(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_accept_valid?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	defSvc := NewFieldDefinitionService(client)
	valSvc := NewFieldValueService(client)

	_, err := defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 42, []FieldDefinitionInput{
		{Name: "device_count", Label: "设备数量", FieldType: "number", SortOrder: 0},
		{
			Name: "priority_level", Label: "优先级", FieldType: "select", SortOrder: 1,
			Options: []interface{}{map[string]interface{}{"label": "低", "value": "low"}},
		},
	})
	require.NoError(t, err)

	err = valSvc.CreateValues(ctx, 1, "ticket_template", 42, "ticket", 402, map[string]interface{}{
		"device_count":   3,
		"priority_level": "low",
	})
	require.NoError(t, err)
}
```

先用 `Read` 确认 `itsm-backend/service/field_value_service_test.go` 顶部已有的 import 块（`fieldvalue` 包路径等），跟本文件已有测试保持一致的 import 方式。

- [ ] **Step 6: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestFieldValueService_CreateValues_Rejects -v`
Expected: FAIL —— 目前不做任何类型校验，两个"应该拒绝"的用例都不会返回 error。

- [ ] **Step 7: 实现格式校验**

在 `itsm-backend/service/field_value_service.go` 里新增校验函数，放在 `CreateValues` 之前：

```go
// validateFieldValue 按字段定义的 field_type 做最基本的格式/成员校验。只处理有明确
// 判定标准的类型（number 是不是数字、select/multiselect 的值在不在 options 里）；
// text/textarea/date/boolean/file 目前没有额外格式约束，跳过。
func validateFieldValue(def *ent.FieldDefinition, raw interface{}) error {
	switch def.FieldType {
	case "number":
		switch raw.(type) {
		case float64, int, int64, json.Number:
			return nil
		case string:
			if _, err := strconv.ParseFloat(raw.(string), 64); err == nil {
				return nil
			}
		}
		return fmt.Errorf("字段 %q 需要数字类型的值", def.Label)
	case "select":
		if len(def.Options) == 0 {
			return nil
		}
		valueStr := fmt.Sprintf("%v", raw)
		for _, opt := range def.Options {
			if optMap, ok := opt.(map[string]interface{}); ok {
				if fmt.Sprintf("%v", optMap["value"]) == valueStr {
					return nil
				}
			}
		}
		return fmt.Errorf("字段 %q 的值不在允许的选项范围内", def.Label)
	case "multiselect":
		if len(def.Options) == 0 {
			return nil
		}
		values, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("字段 %q 需要数组类型的值", def.Label)
		}
		allowed := make(map[string]struct{}, len(def.Options))
		for _, opt := range def.Options {
			if optMap, ok := opt.(map[string]interface{}); ok {
				allowed[fmt.Sprintf("%v", optMap["value"])] = struct{}{}
			}
		}
		for _, v := range values {
			if _, ok := allowed[fmt.Sprintf("%v", v)]; !ok {
				return fmt.Errorf("字段 %q 包含不在允许范围内的值: %v", def.Label, v)
			}
		}
		return nil
	default:
		return nil
	}
}
```

确认文件顶部 import 块有 `"encoding/json"`、`"strconv"`（没有的话加上）。

然后在 `CreateValues` 方法里，`for _, def := range defs {` 循环内、`encoded, err := json.Marshal(raw)` 那行之前插入校验调用：

```go
	for _, def := range defs {
		raw, ok := values[def.Name]
		if !ok {
			continue
		}
		if err := validateFieldValue(def, raw); err != nil {
			return rollback(tx, err)
		}
		encoded, err := json.Marshal(raw)
```

`CreateAdHocValues` 因为没有关联 `FieldDefinition`（`AdHocFieldValue` 本身不带 `field_type`），这次不做格式校验——它的校验范围本来就跟"有定义的字段"不是一回事，勉强加会需要在 `AdHocFieldValue` 上新增 `FieldType` 字段，改动面超出这次任务范围，不做。

- [ ] **Step 8: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run TestFieldValueService -v`
Expected: PASS，包括这次新增的和原有的全部 `TestFieldValueService_*` 用例。

- [ ] **Step 9: 跑整个 service 包确认没有破坏调用方（ticket_service.go / handlers/service_request）**

Run: `cd itsm-backend && go build ./... && go test ./service/... ./handlers/... -v 2>&1 | tail -100`
Expected: 全部 PASS。

- [ ] **Step 10: 提交**

```bash
cd itsm-backend
git add service/field_value_service.go service/field_value_service_test.go service/ticket_template_service.go service/ticket_template_service_test.go
git commit -m "fix(field): 自定义字段补类型/格式/选项成员校验，字段类型本身在定义时也做允许值校验"
```

---

### Task 6: 工单创建校验失败不再留孤儿行

**Files:**
- Modify: `itsm-backend/service/ticket_service.go`
- Test: `itsm-backend/service/ticket_service_test.go`

**Interfaces:**
- Consumes（已存在）: `s.validateRequiredFields(ctx, tenantID, templateID, formFields)`、`s.repo.Create(ctx, params, tenantID)`、`s.repo.Delete(ctx, id, tenantID)`（软删除，见 fact-finding）
- 行为变化：`req.TemplateID != nil && len(req.FormFields) > 0` 时，必填字段校验挪到 `s.repo.Create` **之前**执行，校验失败时函数直接返回错误，不创建任何工单行——不再是"先创建、校验失败再留着"

- [ ] **Step 1: 写失败测试**

先用 `Read` 确认 `itsm-backend/service/ticket_service_test.go` 里已有测试的 fixture 搭建方式（怎么建 tenant/user/template/FieldDefinition），照同样的方式写。追加：

```go
func TestCreateTicket_RequiredFieldValidationFailure_DoesNotLeaveOrphanTicketRow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:create_ticket_no_orphan?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("ct-orphan").SetDomain("ct-orphan.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester-orphan").SetEmail("requester-orphan@test.com").SetPasswordHash("x").
		SetName("申请人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	template, err := client.TicketTemplate.Create().
		SetName("需要必填字段的模板").SetCategory("general").SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	defSvc := NewFieldDefinitionService(client)
	_, err = defSvc.ReplaceDefinitions(ctx, tenant.ID, "ticket_template", template.ID, []FieldDefinitionInput{
		{Name: "impact_scope", Label: "影响范围", FieldType: "text", Required: true, SortOrder: 0},
	})
	require.NoError(t, err)

	beforeCount, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)

	svc := NewTicketService(TicketServiceConfig{Client: client, Logger: zaptest.NewLogger(t).Sugar()})
	templateID := template.ID
	_, err = svc.CreateTicket(ctx, &dto.CreateTicketRequest{
		Title:       "缺必填字段的工单",
		Priority:    "medium",
		RequesterID: requester.ID,
		TemplateID:  &templateID,
		FormFields:  map[string]interface{}{},
	}, tenant.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少必填字段")

	afterCount, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, beforeCount, afterCount, "校验失败不应该在数据库里留下工单行")
}

func TestCreateTicket_RequiredFieldValidationPasses_CreatesTicket(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:create_ticket_valid?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("ct-valid").SetDomain("ct-valid.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester-valid").SetEmail("requester-valid@test.com").SetPasswordHash("x").
		SetName("申请人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	template, err := client.TicketTemplate.Create().
		SetName("模板").SetCategory("general").SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	defSvc := NewFieldDefinitionService(client)
	_, err = defSvc.ReplaceDefinitions(ctx, tenant.ID, "ticket_template", template.ID, []FieldDefinitionInput{
		{Name: "impact_scope", Label: "影响范围", FieldType: "text", Required: true, SortOrder: 0},
	})
	require.NoError(t, err)

	svc := NewTicketService(TicketServiceConfig{Client: client, Logger: zaptest.NewLogger(t).Sugar()})
	templateID := template.ID
	created, err := svc.CreateTicket(ctx, &dto.CreateTicketRequest{
		Title:       "字段齐全的工单",
		Priority:    "medium",
		RequesterID: requester.ID,
		TemplateID:  &templateID,
		FormFields:  map[string]interface{}{"impact_scope": "全公司"},
	}, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, created)
}
```

`NewTicketService(TicketServiceConfig{...})` 的精确构造方式（字段名是不是 `Client`/`Logger`）用 `Read` 确认 `ticket_service.go` 里 `TicketServiceConfig` 的定义和本文件其它已有测试的写法，按实际调整。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./service/... -run TestCreateTicket_RequiredFieldValidationFailure -v`
Expected: FAIL —— `afterCount` 会比 `beforeCount` 多 1（孤儿行被创建了）。

- [ ] **Step 3: 调整校验/落库顺序**

在 `itsm-backend/service/ticket_service.go` 的 `CreateTicket` 里，把必填字段校验从"创建之后"挪到"创建之前"。具体做法：把这一段：

```go
	// 通过 Repository 创建工单
	tkt, err := s.repo.Create(ctx, params, tenantID)
	if err != nil {
		s.logger.Errorw("Failed to create ticket", "error", err)
		return nil, err
	}

	if tkt.AssigneeID == nil && s.assignmentSmartService != nil {
		assignment, err := s.assignmentSmartService.AutoAssign(ctx, tkt.ID, tenantID)
		if err != nil {
			s.logger.Warnw("Automatic ticket assignment failed", "error", err, "ticket_id", tkt.ID)
		} else {
			tkt.AssigneeID = assignment.AssignedTo
		}
	}

	// 验证必填自定义字段（模板字段定义中标记为 required 的字段必须在提交中存在且非空）
	if req.TemplateID != nil && len(req.FormFields) > 0 {
		if missing, err := s.validateRequiredFields(ctx, tenantID, *req.TemplateID, req.FormFields); err != nil {
			s.logger.Errorw("Required field validation error", "error", err, "template_id", *req.TemplateID)
		} else if len(missing) > 0 {
			return nil, fmt.Errorf("缺少必填字段: %s", strings.Join(missing, ", "))
		}
	}
```

改成：

```go
	// 验证必填自定义字段（模板字段定义中标记为 required 的字段必须在提交中存在且非空）。
	// 必须在 s.repo.Create 之前做——之前的顺序是先落库再校验，校验失败时已经落库的
	// 工单行（连同工单编号）没有任何回滚，会在数据库里留下一个永远失败创建了的孤儿行，
	// 污染报表/SLA统计/工单号序列。
	if req.TemplateID != nil && len(req.FormFields) > 0 {
		if missing, err := s.validateRequiredFields(ctx, tenantID, *req.TemplateID, req.FormFields); err != nil {
			s.logger.Errorw("Required field validation error", "error", err, "template_id", *req.TemplateID)
		} else if len(missing) > 0 {
			return nil, fmt.Errorf("缺少必填字段: %s", strings.Join(missing, ", "))
		}
	}

	// 通过 Repository 创建工单
	tkt, err := s.repo.Create(ctx, params, tenantID)
	if err != nil {
		s.logger.Errorw("Failed to create ticket", "error", err)
		return nil, err
	}

	if tkt.AssigneeID == nil && s.assignmentSmartService != nil {
		assignment, err := s.assignmentSmartService.AutoAssign(ctx, tkt.ID, tenantID)
		if err != nil {
			s.logger.Warnw("Automatic ticket assignment failed", "error", err, "ticket_id", tkt.ID)
		} else {
			tkt.AssigneeID = assignment.AssignedTo
		}
	}
```

不用改动函数里其它任何部分——后面写自定义字段值、算 SLA、发通知等逻辑全部维持原样，只是校验这一段被挪到了前面。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-backend && go test ./service/... -run "TestCreateTicket_RequiredFieldValidation" -v`
Expected: PASS

- [ ] **Step 5: 跑整个 ticket_service 相关测试确认没有破坏既有用例（尤其是不带模板/不带表单字段的普通建单路径）**

Run: `cd itsm-backend && go build ./... && go test ./service/... -run "TestCreateTicket|TestTicketService" -v 2>&1 | tail -150`
Expected: 全部 PASS。

- [ ] **Step 6: 提交**

```bash
cd itsm-backend
git add service/ticket_service.go service/ticket_service_test.go
git commit -m "fix(ticket): 必填字段校验挪到落库之前，校验失败不再留孤儿工单行"
```

---

## 全量回归（全部 9 个任务完成之后）

- [ ] **Step 1: 后端全量测试**

```bash
cd itsm-backend
nohup go test ./... > /tmp/full_regression_backend.log 2>&1 &
disown
```
等待后台任务结束，读日志确认没有 `FAIL`。

- [ ] **Step 2: 前端全量类型检查**

Run: `cd itsm-frontend && npm run type-check`
Expected: 无报错。

- [ ] **Step 3: 前端相关测试**

```bash
cd itsm-frontend
nohup npx jest src/components/business/detail-tabs src/components/workflow --runInBand > /tmp/full_regression_frontend.log 2>&1 &
disown
```
等待后台任务结束，读日志确认全部 PASS。

- [ ] **Step 4: 用 superpowers:finishing-a-development-branch 收尾**

全量回归通过之后，用 finishing-a-development-branch 技能决定这个分支怎么处理（本地合并 / 推远程开 PR / 保留原样），不要自己临时决定。
