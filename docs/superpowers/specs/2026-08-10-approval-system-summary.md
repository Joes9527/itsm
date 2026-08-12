# ITSM 审批体系重构 — 工作汇总 (2026-08-10)

> **状态：** 代码已完成，待最终 review 后统一 commit
> **环境：** 前端 3010 / 后端 8090 / PostgreSQL + Redis (Docker)

---

## 1. 已完成的变更

### 1.1 阶段零：收敛两套 SR 实现

**问题：** ServiceRequest 有新旧两套并行实现（`service/service_request_service.go` + `controller/service_controller.go` vs `handlers/service_request/`）

**改动：**
- 删除：`controller/service_controller.go`、`service/service_request_service.go`、`service/service_catalog_service.go`
- 修改：`router.go` 移除 12 条旧 `/services/*` 路由
- 现在 `/service-requests` 是唯一 SR 入口

### 1.2 Worktree 合并：SR-to-Ticket 统一

**问题：** SR 有自己的硬编码三段式审批（manager→IT→security），与 Ticket 的 BPMN 审批是两个独立体系

**改动（40+ commits from `feat/service-request-ticket-unification`）：**
- `servicerequest` 表新增 `ticket_id` FK，删除 `status`/`title`/`current_level` 等自有审批字段
- 删除整张 `service_request_approvals` 表
- `handlers/service_request/service.go` Create：先创建 Ticket（type 根据 catalog.itsm_type）→ 再创建 SR 关联
- 前端 SR 独立详情页退役，合并进 Ticket 详情 + ServiceRequestPanel
- Ticket 新增 `source` 字段（manual / service_catalog）
- BPMN 审批迁移：旧 ApprovalWorkflow 迁移到 BPMN ProcessDefinition + 批量迁移 CLI

### 1.3 阶段二：ApprovalChain 扩展 + 解析器

**新增文件：**
- `service/approval_chain_resolver.go` — 审批链解析服务
- `service/approval_chain_resolver_test.go` — 5 个单元测试

**改动：**
- `ent/schema/approvalchain.go` ApprovalChainStep 扩展三个字段：
  - `OrgScope` — 分公司 / 集团层级
  - `AmountThreshold` — 金额门槛
  - `GroupControlled` — 集团管控强制追加
- SR 创建时调用 `ResolveForServiceRequest()`，解析结果写入 `form_data._approval_chain`

### 1.4 ITSM 类型路由

**问题：** 所有 SR 创建出的 Ticket 都是 `type=service_request`，不同 catalog 无法走不同审批

**改动：**
- `ent/schema/servicecatalog.go` 新增 `itsm_type` 字段（Request / Incident / Change）
- `Service.Create` 分支逻辑：
  - `itsm_type=Request` → Ticket(type=service_request) → BPMN 标准审批
  - `itsm_type=Change` → Ticket(type=change) → BPMN 变更审批
  - `itsm_type=Incident` → 直接创建 Incident，跳过审批
- `internal/bootstrap/app.go` 新增 `srIncidentBridge` 适配器

### 1.5 审批菜单重构

**改动：**
- 隐藏旧「审批管理」菜单（管理已废弃的 ApprovalWorkflow）
- 新增「待审批」菜单 (`/my-approvals`) — 聚合 BPMN 待审批任务
- 新增「审批链规则」子菜单 (`/workflow/approval-chains`) — 管理 ApprovalChain
- 新增 `ServiceCatalogApprovalChain` 组件 — Ticket 详情「审批链」tab 中展示解析出的审批步骤

---

## 2. 当前架构

### 2.1 实体关系

```
                    ┌──────────────┐
                    │    Ticket    │ ← 审批/状态/工作流 的唯一载体
                    │ type:        │
                    │  service_req │
                    │  change      │
                    │  incident    │
                    │ source:      │
                    │  manual      │
                    │  service_cat │
                    └──┬───┬───┬──┘
          ┌────────────┘   │   └──────────┐
          ▼                ▼              ▼
   ┌──────────┐    ┌──────────┐    ┌──────────┐
   │ServiceRq │    │ Change   │    │ Incident │
   │ticket_id →──→│ (独立)   │    │ (独立)   │
   │catalog_id│    └──────────┘    └──────────┘
   │特有字段: │
   │form_data │
   │cost_ctr  │
   │compliance│
   └──────────┘
```

### 2.2 审批流程（三层结构）

```
第1层：Service Catalog
  itsm_type → 决定 Ticket.type（service_request / change / incident）

第2层：ProcessBinding
  business_type + business_sub_type → 路由到具体 BPMN 流程定义

第3层：BPMN ProcessDefinition
  userTask 节点 → 实际审批执行（谁批、会签串签、超时）
  
辅助层：ApprovalChainResolver
  预解析审批步骤 → 写入 _approval_chain → 前端展示
  （不驱动审批执行，只是元数据）
```

### 2.3 自定义字段

```
field_definitions 表：
  entity_type = "service_catalog"  ← 属于 catalog
  entity_id   = <catalog_id>      ← 具体哪个 catalog
  name/label/type/required        ← 字段定义

SR 创建时：
  1. 加载 catalog 的 field_definitions
  2. 校验 required 字段
  3. 存储值到 field_values（entity_type="ticket", entity_id=ticketID）
```

---

## 3. 测试环境

### 3.1 用户

| 用户名 | 密码 | 角色 |
|---|---|---|
| admin | admin123 | super_admin |
| zhangsan | Test@12345678 | end_user |
| manager1 | Test@12345678 | manager |
| servicedesk | Test@12345678 | agent |
| desktop | Test@12345678 | technician |
| infra | Test@12345678 | admin |

### 3.2 测试 Catalog

| id | 名称 | itsm_type | 说明 |
|---|---|---|---|
| 12 | 特权账号申请 | Change | 变更审批路径 |
| 13 | 新电脑初始化 | Request | 标准服务请求路径 |
| 14 | 内存升级更换 | Request | 标准服务请求路径 |

### 3.3 测试审批链

| id | 名称 | entity_type | 步骤 |
|---|---|---|---|
| 1 | 服务请求标准审批链 | service_request | L1:部门主管(manager) → L2:IT审批(it_admin) → L3:安全审批(security_admin) |

---

## 4. 待讨论 / 待实施

| # | 事项 | 状态 |
|---|---|---|
| 1 | BPMN 流程定义为 service_request 和 change 各自创建不同流程 | 未做 |
| 2 | 测试 catalog 的自定义字段配置 | 未做 |
| 3 | 前端「审批链规则」管理页面 (`/workflow/approval-chains`) | 未做 |
| 4 | 后端 `GET /my-approvals` 改为返回 BPMN task（已完成路由，需要更多测试） | 路由已改 |
| 5 | 审批操作（通过/驳回）集成到待审批列表 | 未做 |
| 6 | Excel 中的 100+ catalog 批量导入 | 未做 |
| 7 | AI 功能已禁用（`LLM_API_KEY=` 置空） | 已做 |

---

## 5. 关键设计决策

1. **审批不建第二引擎** — 全部基于 BPMN/ProcessBinding，ApprovalChain 仅做预解析元数据
2. **ITSM 类型决定审批路由** — catalog.itsm_type → Ticket.type → ProcessBinding → BPMN
3. **Incident 不走审批** — itsm_type=Incident 直接创建事件分派 Resolver
4. **不新建配置表** — 扩展已有 ApprovalChainStep 字段，复用 ProcessBinding 机制
5. **渐进迁移** — 旧代码隐藏而非删除（`is_visible=false`），保证回滚能力
