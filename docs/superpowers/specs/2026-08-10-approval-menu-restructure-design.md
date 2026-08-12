# 审批菜单重构 & 待审批列表 & 审批步骤展示 — 设计方案

> **日期：** 2026-08-10
> **状态：** 待实施
> **上下文：** SR-to-Ticket 统一 + BPMN 审批迁移已完成。旧 `审批管理` 菜单管理的是被废弃的 ApprovalWorkflow，需要重构为面向 BPMN 的新审批体验。

## 1. 背景与动机

### 1.1 当前问题

| # | 问题 | 影响 |
|---|---|---|
| P1 | 无统一「待审批列表」| 审批人登录后不知道哪些工单/变更在等待审批 |
| P2 | Ticket/SR 详情页无审批步骤展示 | 申请人看不到审批进度，审批人不知道审批链结构 |
| P3 | `审批管理` 菜单管理旧 ApprovalWorkflow，与 BPMN 功能重叠 | 用户混淆，两个入口做同一类事 |
| P4 | BPMN 审批任务分散在工单/变更各自的详情页中 | 无法批量查看、批量审批 |

### 1.2 已完成的前置工作

- SR-to-Ticket 统一：SR 审批委托给 Ticket → BPMN
- BPMN 审批迁移：旧 ApprovalWorkflow 模板迁移到 BPMN ProcessDefinition
- ApprovalChain resolver：SR 创建时解析审批链步骤，写入 `form_data._approval_chain`
- ITSM 类型路由：catalog.itsm_type → Ticket.type → ProcessBinding → BPMN

### 1.3 非目标（不在本次范围）

- BPMN 流程编辑器
- 审批链规则的批量导入导出
- 移动端审批

## 2. 设计方案

### 2.1 菜单重构

```
Before:                              After:

 审批管理 (/admin/approvals)          ❌ is_visible=false（软删除）
 └── ApprovalWorkflow CRUD
                                      📋 待审批 (/my-approvals) ← NEW
 工作流 (/workflow)                   └── 聚合所有 BPMN 待审批任务
 └── (隐式子页)
                                      工作流 (/workflow)
                                      ├── BPMN流程定义 (现有)
                                      ├── BPMN节点分析 (现有)
                                      └── 审批链规则 (/workflow/approval-chains) ← NEW
```

DB 操作：
```sql
-- 隐藏旧菜单
UPDATE menus SET is_visible = false WHERE path = '/admin/approvals';

-- 新增待审批菜单
INSERT INTO menus (name, path, icon, permission_code, sort_order, tenant_id)
VALUES ('待审批', '/my-approvals', 'CheckCircle', 'approval:read', 6, <tenant_id>);

-- 新增审批链规则子菜单（parent_id 指向工作流菜单）
INSERT INTO menus (name, path, icon, permission_code, sort_order, tenant_id, parent_id)
VALUES ('审批链规则', '/workflow/approval-chains', 'GitMerge', 'approval:read', 3, <tenant_id>, <workflow_menu_id>);
```

### 2.2 待审批列表（Task 1）

**后端 API：** `GET /api/v1/my-approvals?page=1&pageSize=10`

数据源：`process_tasks` 表
```sql
SELECT pt.*, t.title AS ticket_title, t.type AS ticket_type, t.ticket_number,
       u.name AS requester_name
FROM process_tasks pt
JOIN tickets t ON pt.business_id = t.id AND pt.business_type = 'ticket'
LEFT JOIN users u ON t.requester_id = u.id
WHERE pt.status = 'pending'
  AND pt.tenant_id = <tenant_id>
  AND (
    pt.assignee_id = <user_id>
    OR pt.candidate_groups @> ARRAY[<user_roles>]
  )
ORDER BY pt.created_at DESC
```

响应格式：
```json
{
  "code": 0,
  "data": {
    "items": [{
      "taskId": 1,
      "ticketId": 9,
      "ticketNumber": "TK-20260810-0001",
      "title": "申请数据库管理员权限",
      "type": "change",
      "requesterName": "张三",
      "taskName": "部门主管审批",
      "createdAt": "2026-08-10T10:00:00Z"
    }],
    "total": 1,
    "page": 1,
    "pageSize": 10
  }
}
```

**实现位置：**
- Controller: `controller/approval_controller.go` 或新建 `handlers/approval/`
- 复用 BPMN 引擎已有的 `process_tasks` 查询能力（`service/bpmn_process_engine.go` 已有 task query）

**前端页面：** `/my-approvals/page.tsx`
- 列表展示：工单编号、标题、类型、申请人、任务名称、提交时间
- 操作按钮：通过 / 驳回（调用 BPMN task complete API）
- 点击行 → 跳转 Ticket 详情页

### 2.3 Ticket/SR 详情页审批步骤展示（Task 2）

**位置：** `itsm-frontend/src/components/ticket/TicketDetail.tsx`

**新增「审批进度」板块：**

数据来源：
1. 审批链步骤 → `service_requests.form_data._approval_chain`（创建时写入）
2. BPMN 任务状态 → `process_tasks`（当前审批到哪一步）

展示逻辑：
```
┌─ 审批进度 ─────────────────────────────┐
│  ○ 部门主管审批  → 张经理               │  ← 已完成 (approved)
│  ● IT审批        → 李IT [审批中]        │  ← 当前节点 (pending)
│  ○ 安全审批      → 王安全               │  ← 待处理
└────────────────────────────────────────┘
```

**实现：**
- 修改 `TicketDetail.tsx`：根据 `ticket.source === 'service_catalog'` 判断是否挂载审批步骤
- 新增 API：`GET /api/v1/tickets/:id/approval-progress` 返回审批步骤 + 当前状态
- ServiceRequestPanel 中展示审批链摘要

### 2.4 审批链规则管理（从旧审批管理迁移）

**位置：** `/workflow/approval-chains/page.tsx`

复用 `controller/ApprovalChainController` 已有 API：
- `GET /api/v1/approval-chains`
- `POST /api/v1/approval-chains`
- `PUT /api/v1/approval-chains/:id`
- `DELETE /api/v1/approval-chains/:id`

前端页面功能：
- 列表：名称、实体类型（ticket/change/service_request）、步骤数、状态
- 新建/编辑：步骤编辑器（level、role、amountThreshold、groupControlled）
- 启用/停用

## 3. 实施计划

### Task 1: 待审批列表 API + 前端页面

| 步骤 | 文件 | 说明 |
|---|---|---|
| 1a | `dto/approval_dto.go` | 新增 `MyApprovalItem`、`MyApprovalListResponse` |
| 1b | `service/bpmn_process_engine.go` 或新文件 | `ListPendingTasksForUser(ctx, userID, roles, page, size)` |
| 1c | `controller/approval_controller.go` | 新增 `GetMyApprovals` handler |
| 1d | `router/router.go` | 注册 `GET /my-approvals` |
| 1e | `src/app/(main)/my-approvals/page.tsx` | 前端待审批列表 |
| 1f | `src/lib/api/approval-api.ts` | 前端 API client |

### Task 2: Ticket/SR 详情页审批步骤展示

| 步骤 | 文件 | 说明 |
|---|---|---|
| 2a | `dto/ticket_dto.go` | `TicketDetailResponse` 加 `ApprovalProgress` 字段 |
| 2b | `service/ticket_service.go` | `GetTicketDetail` 查审批进度 |
| 2c | `components/ticket/TicketDetail.tsx` | 「审批进度」板块 |
| 2d | `handlers/service_request/` 或 handler | API 返回 `_approval_chain` 给前端 |

### Task 3: 菜单重构

| 步骤 | 文件 | 说明 |
|---|---|---|
| 3a | migration SQL | 隐藏旧菜单、新增待审批+审批链规则菜单 |
| 3b | `src/app/(main)/workflow/approval-chains/page.tsx` | 审批链规则管理页面 |

### 验证

| 验证项 | 方法 |
|---|---|
| 待审批列表显示正确的任务 | 提交 SR → 检查 BPMN task 是否出现在列表中 |
| 审批操作有效 | 点击通过/驳回 → 检查 Ticket 状态变化 |
| 详情页审批步骤正确 | 打开 Ticket 详情 → 检查时间线展示 |
| 旧菜单已隐藏 | 刷新页面 → 审批管理不再出现 |

## 4. 与 AGENTS.md 对齐

| 原则 | 对齐 |
|---|---|
| 不引入第二审批引擎 | ✅ 全部基于 BPMN process_tasks |
| 不要新增路由层保留旧层 | ✅ 隐藏旧菜单，不移除旧代码（渐进迁移） |
| 前端不重复业务规则 | ✅ 审批步骤只在后端写入，前端只展示 |
| 企业级正确性 | ✅ 审批操作通过 BPMN task complete，有审计记录 |
