# Incident/Problem/Change actions 计算设计

- 日期：2026-08-28
- 状态：已实现并完成最终审查加固（2026-08-30）
- 依赖：`docs/superpowers/plans/2026-08-28-workitem-parity-phase4-actions-spec-scoping.md`
  （本设计的头脑风暴记录，含全部决策的推导过程）、
  `docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md` §5.3、§7 point 4
  （父 spec，声明本项"工作量待评估，可能需要拆成独立 spec"）、`AGENTS.md`

## 1. 背景与目标

Phase 3 已经把 `WorkItemShell` 的操作栏做出来了（`WorkItemActionBar`，遍历
`useWorkItemContext().actions` 渲染按钮），但 Incident/Problem/Change 三个详情页目前全部
硬编码 `actions={{}}`——操作栏永远是空的，真正的按钮仍然分别长在
`IncidentDetail.tsx`/`ProblemDetail.tsx`/`ChangeDetail.tsx` 各自的 header 里，靠内联的
`status === X` 判断决定显示/隐藏，不读取任何后端计算的权限。

本设计的目标：为这三个域计算真实的 `actions` 映射（`Record<string, {allowed, reason}>`），
复用 Ticket 域已有的 `dto.ActionPermission` 契约和 Phase 3 已锁定的前端类型
（`WorkItemActionState`/`WorkItemContextValue`），让三个域的按钮可见性/可点性从"前端猜"
变成"后端算"。`actions` 是后端领域规则的**读时投影**，不是写接口的安全边界：凡是本设计
新增或收紧的动作规则，真实 POST/PUT 服务方法必须复用同一条规则并拒绝非法直连请求。

**不是本设计目标**：重新设计三个域的 RBAC 模型、新建细粒度权限、实现 Change
rollback/cancel 或 Incident cancel（这三个动作从未有过前端入口，参见 §2.3）、给 Problem
补齐 investigate/root-cause/solution 的真实表单（那些接口存在但前端整体是 stub，是独立的
更大功能）。

## 2. 现状审计

### 2.1 Ticket 域既有模式（本设计复用的基础）

`itsm-backend/service/ticket_authorization.go`：

```go
type ActionActor struct {
    Client   *ent.Client
    TenantID int
    UserID   int
    Role     string
}

func CanApprove(actor ActionActor, t *ticket.Ticket) dto.ActionPermission {
    if isRequester(t, actor.UserID) {
        return dto.ActionPermission{Allowed: false, Reason: "不能审批自己提交的工单"}
    }
    if isFinalStatus(t.Status) { ... }
    if !middleware.HasResourcePermission(actor.Client, actor.Role, "ticket", "update", actor.TenantID) { ... }
    return dto.ActionPermission{Allowed: true}
}

func BuildTicketActions(ctx context.Context, actor ActionActor, t *ticket.Ticket) map[string]dto.ActionPermission {
    return map[string]dto.ActionPermission{
        "approve": CanApprove(actor, t),
        "reject":  CanReject(actor, t),
        "assign":  CanAssign(actor, t),
        "edit":    CanEdit(actor, t),
        "cc":      CanCC(ctx, actor, t),
        "delete":  CanDelete(ctx, actor, t),
    }
}
```

`dto.ActionPermission`（`itsm-backend/dto/ticket_dto.go:118-121`）：`{Allowed bool, Reason
string}`。挂载点：`ToTicketResponseWithCustomFieldsAndActions`
（`itsm-backend/service/ticket_service.go:1487-1498`）——在基础 mapper
（`ToTicketResponseWithCustomFields`，供 list 场景复用）之外单独包一层，只在真正的详情响应
里调用 `BuildTicketActions`，**list 响应不计算 actions**（注释："只用于真正的详情响应场景"）。
第二个先例 `handlers/service_request/handler.go:92-118` 的 `toDTOWithCustomFields` 做法更
轻量：不建聚合函数，直接在 DTO 组装处内联赋值 `resp.Actions = map[string]dto.ActionPermission{
"provision": service.CanProvision(...)}`。

**本设计采用哪种先例**：Incident/Problem/Change 三个域动作数量（4-8 个）比
ServiceRequest（1 个）多，比 Ticket（6 个）相当，且各自的判定逻辑比 Ticket 复杂（要查
`record_class`/`status`/`escalation_level`/`is_major_incident`/`problem_id` 等多个字段）——
采用 Ticket 的 `BuildXActions` 聚合函数模式，不采用 ServiceRequest 的内联模式。

### 2.2 三个域现有动作清单与真实后端判定（审计发现）

以下清单已经过逐个动作核实**真实服务端方法**的判定逻辑（不是只看状态迁移表——见下方
"表 vs 实际方法"一栏，多个动作的真实判定和通用状态迁移表并不一致）。

#### Incident（当前渲染于 `IncidentDetail.tsx:572-621`）

| 动作 | 真实服务端方法 | 真实判定 | 前端现状 | 结论 |
|---|---|---|---|---|
| `edit` | 无专属方法，纯导航 | — | 一律显示 | RBAC-only：`incident:write` |
| `resolve` | `ResolveIncident`（`incident_service.go:1399-1450`） | `isValidIncidentStatusTransition(status, "resolved")` → 唯一合法来源是 `in_progress` | 只要不是 resolved/closed 就显示（**7 种状态点了会失败**） | 改为只在 `status == in_progress` 时 allowed |
| `close` | `CloseIncident`（`incident_service.go:1453-1487`） | 唯一合法来源 `resolved` | `status === resolved` 才显示 | 前后端一致，直接照抄 |
| `reopen` | `ReopenIncident`（`incident_service.go:1680-1710`） | **不查共享表**，独立判断：`status ∈ {resolved, closed}` | `status ∈ {resolved, closed}` 才显示 | 前后端一致；但共享表 `constants.go:109` 说 `closed` 是终态——`CanReopen` 必须照抄 `ReopenIncident` 自己的判断，不能查共享表 |
| `escalate`（升级级别） | `EscalateIncident`（`incident_service.go:926-966`） | 不改 `status`，只写 `escalation_level`/`escalated_at`；判定是 `status ∉ {closed, cancelled}` + `level > current_level` | 一律显示，不管状态 | 改为 `status ∉ {closed, cancelled}` 才 allowed（`level > current` 是请求体校验，不进 `allowed`，见 §3 原则 3） |
| `assign` | `AssignIncident`（`incident_service.go:572-615`） | **后端完全不查状态** | `status ∉ {resolved, closed}` 才显示 | 把前端现有限制固化进后端：提取 `canAssignIncidentStatus`，由 `CanAssignIncident` 和 `AssignIncident` 共同调用，要求 `status ∉ {resolved, closed}`（新增的后端规则，用户已确认）。**另需修复一个既有 bug（评审发现，与本设计的判定逻辑无关）**：路由 `POST /incidents/:id/assign` 现在挂的是 `middleware.RequirePermission("incident","assign")`（`router.go:798`），但 seeder 里从未定义过 `incident:assign` 这个权限，除硬编码的 `super_admin` 外没有任何角色能通过——今天所有非 super_admin 用户点这个按钮都会被后端 403，前端却从未做过任何权限判断。本设计把该路由的权限改为 `RequirePermission("incident","write")`，和 `CanAssignIncident` 查的权限对齐，也符合本设计"复用粗粒度权限、不新增权限种子数据"的既定方向（用户已确认）。 |
| `mark_major_incident` | `EscalateToMajorIncident`（`incident_service.go:1714-1765`） | `!IsMajorIncident && status ∉ {resolved, closed}` | 同上 | 前后端一致，直接照抄 |
| `convert_to_problem` | 旧 `RootCauseAnalysisService.CreateProblemFromIncident`（`root_cause_analysis_service.go:137-217`） | 旧实现直接建 `problems` 行，**既不查状态/重复，也绕过 Problem 的 WorkItem 事务创建路径** | `!problemId && status !== closed` 才显示 | 本设计替换旧写路径：Problem 域新增 `CreateFromIncident` 用例，在一个事务中校验 Incident、执行 `status != closed` 与未存在 `investigated_by` 关系的防重复规则、创建目标 WorkItem + Problem 扩展、写入 `Incident WorkItem → Problem WorkItem` 的 `investigated_by` 关系和审计事件。`CanConvertToProblem` 复用相同的只读资格判定；旧 `RootCauseAnalysisService.CreateProblemFromIncident` 和 Controller 对它的调用在同一次改动删除，不保留平行创建路径。 |

#### Problem（当前渲染于 `ProblemDetail.tsx:124-160`，全部走通用 `PUT /problems/:id`）

| 动作 | 真实服务端判定 | 前端现状 | 结论 |
|---|---|---|---|
| `edit` | 无专属方法，纯导航 | 一律显示 | RBAC-only：`problem:write` |
| `start_processing`（前端现有 key） | `isValidProblemStatusTransition`（`handlers/problem/service.go:175-194`）：`in_progress` 从来不是合法**目标**，只是历史数据兼容的**来源**桶 | `status === open` 时显示，目标状态传 `in_progress`——**100% 会被后端拒绝** | **改名为 `start_investigation`**，目标状态改为真正合法的 `investigating`（`open → investigating` 合法）。用户已确认改名。 |
| `resolve` | 合法来源：`investigating`/`identified`/`in_progress`（兼容桶） | `status === in_progress` 才显示 | 改为 `status ∈ {investigating, identified, in_progress}` 时 allowed——`in_progress` 兼容桶必须保留，否则历史数据卡在该状态的 Problem 无法再被 resolve |
| `close` | 合法来源：`open`/`investigating`/`identified`/`resolved`（表面上比前端宽松） | `status === resolved` 才显示 | **保持前端现状**：只在 `status === resolved` 时 allowed。跳过调查阶段直接关闭是新产品能力，不在本设计里顺带打开（用户已确认） |

Problem 没有 assign/escalate 路由，不需要新增这两个动作。`investigate`/`root-cause`/
`solution`/`close`（专属接口）在 §2.3 排除。

#### Change（当前渲染于 `ChangeDetail.tsx:298-330`，除 `submit` 外全部走
`POST /changes/:id/{approve,reject,start,complete}` → 共享的 `TransitionStatus`）

| 动作 | 真实服务端判定 | 前端现状 | 结论 |
|---|---|---|---|
| `submit_for_approval` | `SubmitChange`（`handlers/change/service.go:188-298`）：`status == draft` | `status === draft` | 前后端一致 |
| `approve` | `TransitionStatus`：`status ∈ {submitted, pending}`（`pending` 会被规范化为 `submitted`）；额外要求存在一个真实运行中的 `Activity_CABApproval` BPMN 任务（`completeChangeApprovalTask`，`service.go:667-737`，本设计**不**在 `allowed` 里加这层校验，见下方决策） | `status === pending`（=`canApprove`） | 前后端状态判定一致；**新增**：无条件的自我审批排除（见下） |
| `reject` | 同 approve 的状态判定 + 要求非空 `comment`（`service.go:850-852`，属于请求体校验，不影响 `allowed`） | 同 approve（`canApprove`） | 同上 |
| `start_implementation` | `TransitionStatus`，`IsValidChangeStatusTransition` **按 `change.type` 分表**：standard `approved/scheduled→in_progress` 均合法；emergency 只有 `approved→in_progress` 合法（**没有 `scheduled` 分支**）；normal 只有 `scheduled→in_progress` 合法（**`approved→in_progress` 不合法**） | 只要 `status ∈ {approved, scheduled}` 就显示，**不管 `type`** | **normal 类型在 approved 状态点击会 100% 失败**——改为按 `type` 分支判定（见 §4.6 具体代码） |
| `complete_implementation` | `TransitionStatus`：三种类型统一 `in_progress → completed` | `status === in_progress` | 前后端一致 |

**新增决策——Change 审批自我排除缺口（用户已确认，方案 A）**：Change 目前唯一的"防止自己批自己"
机制是 BPMN 引擎在创建候选人列表时"尽量"排除提交人（`bpmn_process_engine.go` 的
`excludeUserFromCandidates`），这个排除是**有条件的**——如果流程节点写死了
`candidateGroups`/`candidateUsers`，或者 `requester_id` 这个流程变量解析失败，排除就不会
发生。不像 Ticket 的 `CanApprove` 有一条无条件的 `isRequester` 硬编码检查。本设计给
`CanApproveChange`/`CanRejectChange` 加上同样无条件的 `CreatedBy == actor.UserID` 检查，
独立于 BPMN 引擎那边有没有排除成功；同时 `ChangeService.TransitionStatus` 在调用
`completeChangeApprovalTask` **之前**执行同一检查。这样手工构造 `POST /approve` 或 `/reject`
请求也不能绕过自我审批禁令。

**新增决策——Change 审批不做 BPMN 存活性查询（用户已确认，方案 A）**：`allowed` 只按
`status`/`type`/自我排除计算，不额外查一次"这个变更现在是否真的有一个待处理的
CABApproval 任务"。理由：这和 Ticket 域 `CanApprove` 的判定深度一致（Ticket 也不做类似的
流程实例存活性校验，只有 `CanDelete` 因为要防误删有专门查询），保持这次改动的复杂度和范围
一致。`status===submitted` 但流程实例已经不存在，属于数据不一致的极端情况，本来就应该
"点了才发现"，不是本设计要消灭的问题类别。

Change 没有 assign 按钮渲染在当前 UI 里（`AssignChange` 后端也没有状态限制，但没有前端入口
可以固化）——不新增。风险评估/影响分析/回滚计划的"保存"操作是专业 Tab 内的数据编辑，不是
工单级动作，不进 `actions`。

### 2.3 已确认排除的范围

- **Change `rollback`/`cancel`、Incident `cancel`**：路由和权限都存在，但 `rollback` 的真实
  实现只是把 `status` 字段翻成 `rolled_back`，不读取"回滚计划"里的内容、不恢复任何状态、
  没有审计记录（`AuditMiddleware` 全局从未注册）；`cancel`/`rollback` 从未有过任何前端触发
  入口（`ChangeApi.rollbackChange`/`cancelChange` 只在测试里被调用）；Incident 更是连专属
  接口都没有。全仓库没有任何文档说明"什么时候该用 rollback，什么时候该用 failed"。现在补上
  按钮属于新造一个从未存在过的产品能力，不是把已有能力接上权限——留给未来独立设计的阶段。
- **Problem 的 `investigate`/`root-cause`/`solution`/`close` 专属接口**：服务端方法和路由
  都在（`handlers/problem/service.go:135-164`），但前端 API client 对应方法全部是
  `throw '功能开发中'` 的 stub——接上真实表单是独立的、更大的功能，不属于本设计。
- **不新增细粒度 RBAC 权限**：所有新动作复用现有的 `incident:write`/`problem:write`/
  `change:write`（Change 审批用现有的 `change:approve`）粗粒度权限，不给 seeder 新增
  `incident:resolve` 之类的按动作权限行。

## 3. 设计原则

1. **权威来源是动作真正调用的那个服务端方法，不是无脑查通用状态迁移表。** 多数动作
   （resolve/close/submit_for_approval/approve/reject/start_implementation/
   complete_implementation）恰好就是查共享的 `IsValidXxxStatusTransition` 表，但
   `ReopenIncident` 是反例——它有自己独立的判断，和共享表的说法矛盾。`CanXxx` 必须照抄
   它实际调用的方法的真实逻辑，共享表只是"通常情况下"的权威来源，不是无条件的。
2. **前端不得保留任何平行的业务判断。** `itsm-frontend/src/lib/utils/workflow-state-machine.ts`
   里 `INCIDENT_STATUS_TRANSITIONS`/`isValidIncidentTransition`（`IncidentDetail.tsx:262`
   唯一调用点）和 `CHANGE_STATUS_TRANSITIONS`（当前未在任何组件里被调用，已是死代码）必须
   在各自域的改动里删除，不留作"以后再清"——`AGENTS.md`："当新路径替换旧路径时，必须在
   同一次改动里删除旧路径，除非有明确的向后兼容要求"。这两套表本身还和后端权威词汇不一致
   （Incident 用 `investigating/monitoring`，后端用 `acknowledged/assigned/triaged/
   escalated/on_hold`；Change 用驼峰 `inProgress`，后端用下划线 `in_progress`），是本次要
   收敛掉的重复实现，不是可以保留的独立能力。
3. **`allowed` 回答"这个动作现在能不能点"，不回答"这次具体的请求参数会不会成功"。**
   `escalate` 的"目标级别必须大于当前级别"、`reject` 的"意见不能为空"，这些是针对**具体请求
   载荷**的校验，属于 API 调用时的常规参数校验，不编码进 `allowed` 布尔值——`allowed` 只表达
   "这个动作类别现在合法"，不预测每一种具体输入的成败。
4. **不重新设计三个域的 RBAC 模型。** 复用现有粗粒度权限（`incident:write` 等），真正决定
   按钮此刻能不能点的是状态/字段判定，不是新增的细粒度权限。
5. **`actions` 只在真正的单条详情响应里计算，不进 list 响应**——照抄 Ticket 域
   `ToTicketResponseWithCustomFieldsAndActions` vs `ToTicketResponseWithCustomFields` 的
   区分，避免给列表接口的每一行都算一遍权限。
6. **动作投影绝不替代命令校验。** `CanXxx` 可以为 UI 提供不允许原因，但凡本设计把现有 UI
   约束提升为产品/安全规则，必须提取纯领域谓词或命令前置校验，让 `CanXxx` 与真正的写方法
   共用；不得只在 GET 响应里新增 `allowed=false` 后让直连 API 继续成功。
7. **共享的 `ActionActor` 结构体从 `ticket_authorization.go` 提取出来，供四个域共用**，不让
   Problem/Change 各自重新定义一份形状相同的 actor 结构——这类跨域基础设施允许
   `handlers/<domain>/` 包直接 import 使用（已有先例：`FieldDefinitionService`）。

## 4. 后端变更

### 4.1 提取共享 `ActionActor`

新文件 `itsm-backend/service/action_actor.go`，把 `ticket_authorization.go` 里的
`ActionActor` 结构体定义移过来（`ticket_authorization.go` 改为引用同包的这个类型，行为不变）：

```go
package service

import "itsm-backend/ent"

// ActionActor 打包一次权限判定所需的执行者上下文，供 Ticket/Incident/Problem/Change
// 四个域的 CanXxx / BuildXActions 函数共用，避免每个域各自重复定义同样形状的结构体。
type ActionActor struct {
    Client   *ent.Client
    TenantID int
    UserID   int
    Role     string
}
```

`handlers/problem/authorization.go`、`handlers/change/authorization.go`（下方 4.5/4.6）
直接 `import "itsm-backend/service"` 使用 `service.ActionActor`。

### 4.2 响应挂载模式

三个域都不新增字段到共享 mapper 函数本身，而是在**服务/handler 层的单条详情读取路径**里，
拿到 mapper 已经建好的 DTO 后再补一行 `resp.Actions = BuildXActions(...)`——list
路径完全不受影响，不用改共享 mapper 的签名。三个 GET 端点必须从 Gin context 读取
`user_id`、`role`、`tenant_id`，构造 `service.ActionActor`；缺失任一身份上下文时返回认证错误，
不产生默认允许的 actions。

- `dto.IncidentResponse`（`itsm-backend/dto/incident_dto.go:102-135`）新增
  `Actions map[string]ActionPermission \`json:"actions,omitempty"\``；保留现有
  `IncidentService.GetIncident(ctx, id, tenantID)` 作为不计算 actions 的通用读取入口（现有多个
  内部调用依赖它），新增仅供详情 HTTP 使用的
  `GetIncidentWithActions(ctx, id, actor ActionActor)`。先抽取私有
  `getIncidentEntity(ctx, id, tenantID) (*ent.Incident, error)`，由它完成唯一一次 tenant-scoped
  实体读取；`GetIncident` 用该实体调用既有 mapper，`GetIncidentWithActions` 则用**同一个实体**
  mapper 后挂载 `BuildIncidentActions`。不得先调 `GetIncident` 再查询一次实体，否则并发更新时
  响应状态与 actions 会来自不同快照；`IncidentController.GetIncident` 构造 actor 后改调它。
- `dto.ProblemResponse`（`itsm-backend/dto/problem_dto.go:59-83`）同样新增 `Actions` 字段；
  挂载点 `Handler.Get`（`handlers/problem/handler.go:122-140`），在调用
  `h.toDTO(p)` 之后补一行。`problem.Handler` 新增 `client *ent.Client` 构造参数，bootstrap 和
  handler 测试 fixture 一并传入同一个 Ent client；这是 `BuildProblemActions` 调用
  `middleware.HasResourcePermission` 所需的依赖，不能假定现有 `Problem Service` 能暴露它的
  私有 repository client。
- `dto.ChangeResponse`（`itsm-backend/dto/change_dto.go:93-123`）同样新增 `Actions` 字段；
  挂载点 `Handler.GetChange`（`handlers/change/handler.go:102-117`），在调用
  `toDTO(c)` 之后补一行。Change handler 与 `Service` 同包，使用已有 `h.svc.entClient` 构造
  actor，不新增第二个 client 或 repository 暴露口。

`actor.UserID`/`actor.Role`/`actor.TenantID` 统一由详情 GET 显式提取；不能假定这些读取端点
已经为了其它用途取过 user/role。Incident 使用 `middleware.GetUserID` 和 `ctx.GetString("role")`；
Problem/Change 校验 `user_id` 为正整数且 `role` 非空后构造 actor，测试 fixture 必须设置这两个
context 值。

### 4.3 `BuildIncidentActions`（新文件 `itsm-backend/service/incident_authorization.go`）

```go
package service

import (
    "itsm-backend/common"
    "itsm-backend/dto"
    "itsm-backend/ent"
    "itsm-backend/middleware"
)

func canWriteIncident(actor ActionActor) bool {
    return middleware.HasResourcePermission(actor.Client, actor.Role, "incident", "write", actor.TenantID)
}

func CanEditIncident(actor ActionActor) dto.ActionPermission {
    if !canWriteIncident(actor) {
        return dto.ActionPermission{Allowed: false, Reason: "无权限编辑事件"}
    }
    return dto.ActionPermission{Allowed: true}
}

func CanResolveIncident(actor ActionActor, i *ent.Incident) dto.ActionPermission {
    if i.Status != common.IncidentStatusInProgress {
        return dto.ActionPermission{Allowed: false, Reason: "只有处理中的事件可以解决"}
    }
    return CanEditIncident(actor)
}

func CanCloseIncident(actor ActionActor, i *ent.Incident) dto.ActionPermission {
    if i.Status != common.IncidentStatusResolved {
        return dto.ActionPermission{Allowed: false, Reason: "只有已解决的事件可以关闭"}
    }
    return CanEditIncident(actor)
}

func CanReopenIncident(actor ActionActor, i *ent.Incident) dto.ActionPermission {
    // 照抄 ReopenIncident 自己的判断（incident_service.go:1688-1690），不查共享的
    // IsValidIncidentStatusTransition 表——该表认为 closed 是终态，但 ReopenIncident
    // 实际允许 closed → in_progress。
    if i.Status != common.IncidentStatusResolved && i.Status != common.IncidentStatusClosed {
        return dto.ActionPermission{Allowed: false, Reason: "只有已解决或已关闭的事件可以重新打开"}
    }
    return CanEditIncident(actor)
}

func CanEscalateIncident(actor ActionActor, i *ent.Incident) dto.ActionPermission {
    // 只判定"这个动作类别现在合法"（未终态），不判定"下一次具体请求的 level 是否合法"——
    // 那是 EscalateIncident 请求体自己的校验（level > current_level），不进这里。
    if i.Status == common.IncidentStatusClosed || i.Status == common.IncidentStatusCancelled {
        return dto.ActionPermission{Allowed: false, Reason: "终态事件不能升级"}
    }
    return CanEditIncident(actor)
}

func CanAssignIncident(actor ActionActor, i *ent.Incident) dto.ActionPermission {
    if !canAssignIncidentStatus(i.Status) {
        return dto.ActionPermission{Allowed: false, Reason: "已解决或已关闭的事件不能重新指派"}
    }
    return CanEditIncident(actor)
}

func CanMarkMajorIncident(actor ActionActor, i *ent.Incident) dto.ActionPermission {
    if i.IsMajorIncident {
        return dto.ActionPermission{Allowed: false, Reason: "已经是重大事件"}
    }
    if i.Status == common.IncidentStatusResolved || i.Status == common.IncidentStatusClosed {
        return dto.ActionPermission{Allowed: false, Reason: "已解决或已关闭的事件不能标记为重大事件"}
    }
    return CanEditIncident(actor)
}

func CanConvertToProblem(actor ActionActor, i *ent.Incident) dto.ActionPermission {
    if i.Status == common.IncidentStatusClosed {
        return dto.ActionPermission{Allowed: false, Reason: "已关闭的事件不能转为问题"}
    }
    // 是否已存在 investigated_by 关系需要查询；BuildIncidentActions 在详情读取时执行该只读
    // 查询。真正的 CreateFromIncident 命令会在同一事务内重新检查，不能信任这里的瞬时结果。
    converted, err := hasIncidentProblemRelation(actor, i)
    if err != nil {
        return dto.ActionPermission{Allowed: false, Reason: "无法确认事件是否已转为问题"}
    }
    if converted {
        return dto.ActionPermission{Allowed: false, Reason: "已经转为问题"}
    }
    return CanEditIncident(actor)
}

func BuildIncidentActions(actor ActionActor, i *ent.Incident) map[string]dto.ActionPermission {
    return map[string]dto.ActionPermission{
        "edit":                CanEditIncident(actor),
        "resolve":             CanResolveIncident(actor, i),
        "close":                CanCloseIncident(actor, i),
        "reopen":              CanReopenIncident(actor, i),
        "escalate":            CanEscalateIncident(actor, i),
        "assign":              CanAssignIncident(actor, i),
        "mark_major_incident": CanMarkMajorIncident(actor, i),
        "convert_to_problem":  CanConvertToProblem(actor, i),
    }
}
```

`ent.Incident` 没有 `ProblemID` 字段，且 "是否已转换" 不能由临时派生字段代表。实现使用
`hasIncidentProblemRelation(actor, i) (bool, error)` 做 tenant-scoped 的 `WorkItemRelation` 查询，
确认当前 Incident WorkItem 是否已有 live `investigated_by` 关系；缺少 `work_item_id` 的存量 Incident 返回
`allowed=false` 并提示先完成回填。`hasIncidentProblemRelation` 的错误必须 fail closed，不能把
数据库错误当成"未转换"。

### 4.4 命令端规则与 Incident → Problem 原子创建

本节是 actions 计算的配套命令规则，避免任何调用方绕过详情页后获得不同结果：

- `IncidentService.AssignIncident` 在读取 Incident 后、写入 assignee 前调用
  `canAssignIncidentStatus(current.Status)`；不满足时返回与 `CanAssignIncident` 一致的领域错误。
  路由仍以 `incident:write` 作为 ACL，服务校验状态，二者职责不混淆。
- Change 的 `TransitionStatus` 在 `targetStatus ∈ {approved, rejected}` 时、调用 BPMN 前拒绝
  `c.CreatedBy == userID`。`CanApproveChange`/`CanRejectChange` 调用一个共享的
  `canApproveChange(actorUserID, c)` 谓词，确保 GET 和 POST 的自我审批规则没有两份实现。
- 将 `RootCauseAnalysisService.CreateProblemFromIncident` 删除，`IncidentController` 改为调用
  一个窄的 `ProblemConversionService` 接口，其实现归属 `handlers/problem`。接口形状为
  `CreateFromIncident(ctx context.Context, tenantID, incidentID, actorUserID int, req dto.ConvertIncidentToProblemRequest) (*problem.Problem, error)`；
  Controller 只负责绑定请求、取 actor 和映射 DTO，不能重新写 Ent 创建逻辑。为使该映射在包边界
  可用，`handlers/problem` 提供导出的 `problem.ToResponse(*problem.Problem) *dto.ProblemResponse`；
  现有 `Handler.toDTO` 复用该 mapper 并仅补充其自身详情需要的关联数据，Incident controller 也用
  它映射转换结果，不调用私有 `toDTO`，也不复制 DTO 映射。
- `ProblemConversionService.CreateFromIncident` 在 repository 的**一个数据库事务**内完成：
  读取并 tenant-scope 校验 Incident；拒绝 closed Incident；要求源 Incident 已有 WorkItem；查询
  live `investigated_by` 关系并拒绝重复；沿用 Problem repository 的 WorkItem + Problem 创建逻辑；
  写入 `source=incident.work_item_id`、`target=problem.work_item_id`、
  `relation_type="investigated_by"`、`created_by_id=actorUserID` 的关系；写入转换活动/审计。
  `EntRepository` 将现有 `Create` 中的 WorkItem + Problem 写入抽为私有
  `createInTx(ctx, tx, p)`：普通 `Create` 自己开启事务后调用它，`CreateFromIncident` 则只开启
  **一笔**事务，调用 `createInTx` 后写 relation、`tx.IncidentEvent` 和 `tx.AuditLog`，最后统一 commit。
  转换的 IncidentEvent 必须带 source `incident_conversion`、actor、目标 Problem/WorkItem ID；AuditLog
  必须带 tenant、actor、`resource=incident`、`action=convert_to_problem`，并使用规范命令元数据
  `path=/api/v1/incidents/:id/convert-to-problem`、`method=POST`；在 `request_body` 中记录源/目标
  WorkItem ID 和请求参数的脱敏摘要。领域服务不得依赖 Gin context 来取得这些字段。任一步失败（包括关系或两类审计写入）
  都完整回滚。不得再写旧的 `Problem<->Incident` Ent edge，也不得留下旧 RCA 创建路径。
- 为使 Incident controller 不依赖 Problem repository 实现，`NewIncidentController` 接受上述窄接口，
  在 bootstrap 用 Problem domain service 注入；controller 不直接调用另一领域 repository。bootstrap
  必须先构造 `problemRepo`、`problemServiceDomain` 与 `problemHandler`，再构造 `incidentController`
  并注入该窄接口（根因分析服务仍可为其余 RCA 功能保留）；相应更新 controller 的测试构造器，禁止用
  nil 或旧 RCA 创建路径掩盖依赖。

`CanConvertToProblem` 的 `allowed` 只是同一资格的读取投影；并发下两个请求都读到允许时，以事务内
命令侧二次检查和数据库约束作为最终防线。现有 `WorkItemRelation` 的唯一索引包含
`target_work_item_id`，两个并发请求若各自新建不同 target 时**不能**阻止重复，因此本设计新增
只约束 live `investigated_by` 的部分唯一索引：
`(tenant_id, source_work_item_id) WHERE deleted_at IS NULL AND relation_type = 'investigated_by'`。
这明确了本功能的业务语义：一个 Incident 至多由一个当前 Problem 调查；第二个请求因唯一约束
冲突完整回滚，不能留下孤儿 WorkItem 或 Problem。

### 4.5 `BuildProblemActions`（新文件 `itsm-backend/handlers/problem/authorization.go`）

```go
package problem

import (
    "itsm-backend/dto"
    "itsm-backend/middleware"
    "itsm-backend/service"
)

func canWriteProblem(actor service.ActionActor) bool {
    return middleware.HasResourcePermission(actor.Client, actor.Role, "problem", "write", actor.TenantID)
}

func CanEditProblem(actor service.ActionActor) dto.ActionPermission {
    if !canWriteProblem(actor) {
        return dto.ActionPermission{Allowed: false, Reason: "无权限编辑问题"}
    }
    return dto.ActionPermission{Allowed: true}
}

func CanStartInvestigation(actor service.ActionActor, p *Problem) dto.ActionPermission {
    // 目标状态是 investigating（不是前端现有代码错误使用的 in_progress——
    // in_progress 在 isValidProblemStatusTransition 里从来不是合法目标）。
    if p.Status != "open" {
        return dto.ActionPermission{Allowed: false, Reason: "只有待处理的问题可以开始调查"}
    }
    return CanEditProblem(actor)
}

func CanResolveProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
    // in_progress 是历史数据兼容桶，必须保留，否则卡在该状态的存量 Problem 无法再被解决。
    switch p.Status {
    case "investigating", "identified", "in_progress":
        return CanEditProblem(actor)
    default:
        return dto.ActionPermission{Allowed: false, Reason: "当前状态不能标记为已解决"}
    }
}

func CanCloseProblem(actor service.ActionActor, p *Problem) dto.ActionPermission {
    // 保持前端现状（只能从 resolved 关闭），不放宽到表面上后端也允许的
    // open/investigating/identified——跳过调查阶段直接关闭是需要产品认可的新能力。
    if p.Status != "resolved" {
        return dto.ActionPermission{Allowed: false, Reason: "只有已解决的问题可以关闭"}
    }
    return CanEditProblem(actor)
}

func BuildProblemActions(actor service.ActionActor, p *Problem) map[string]dto.ActionPermission {
    return map[string]dto.ActionPermission{
        "edit":                CanEditProblem(actor),
        "start_investigation": CanStartInvestigation(actor, p),
        "resolve":             CanResolveProblem(actor, p),
        "close":               CanCloseProblem(actor, p),
    }
}
```

（`Problem` 是 `handlers/problem` 包内部的领域结构体，字段名以其实际定义为准。）

### 4.6 `BuildChangeActions`（新文件 `itsm-backend/handlers/change/authorization.go`）

```go
package change

import (
    "fmt"
    "itsm-backend/dto"
    "itsm-backend/middleware"
    "itsm-backend/service"
)

func canWriteChange(actor service.ActionActor) bool {
    return middleware.HasResourcePermission(actor.Client, actor.Role, "change", "write", actor.TenantID)
}

func canApproveChangePermission(actor service.ActionActor) bool {
    return middleware.HasResourcePermission(actor.Client, actor.Role, "change", "approve", actor.TenantID)
}

func CanSubmitForApproval(actor service.ActionActor, c *Change) dto.ActionPermission {
    if c.Status != "draft" {
        return dto.ActionPermission{Allowed: false, Reason: "只有草稿状态的变更可以提交审批"}
    }
    if !canWriteChange(actor) {
        return dto.ActionPermission{Allowed: false, Reason: "无权限提交变更审批"}
    }
    return dto.ActionPermission{Allowed: true}
}

func isChangeSubmitted(status string) bool {
    return status == "submitted" || status == "pending" // TransitionStatus 会把 pending 规范化为 submitted
}

// canApproveChange 是 GET actions 与 TransitionStatus 在批准/驳回时共用的领域前置校验；
// RBAC 仍由路由中间件和 CanApproveChange 的展示投影分别处理。
func canApproveChange(actorUserID int, c *Change) error {
    if c.CreatedBy == actorUserID {
        return fmt.Errorf("不能审批自己提交的变更")
    }
    if !isChangeSubmitted(c.Status) {
        return fmt.Errorf("只有已提交待审批的变更可以批准")
    }
    return nil
}

func CanApproveChange(actor service.ActionActor, c *Change) dto.ActionPermission {
    // 无条件自我审批排除——不依赖 BPMN 引擎那边"尽量"排除提交人的候选人逻辑
    // （那个排除是有条件的：写死 candidateGroups/requester_id 解析失败都会让它失效）。
    // 照抄 Ticket 域 CanApprove 的 isRequester 模式。
    if err := canApproveChange(actor.UserID, c); err != nil {
        return dto.ActionPermission{Allowed: false, Reason: err.Error()}
    }
    if !canApproveChangePermission(actor) {
        return dto.ActionPermission{Allowed: false, Reason: "无权限审批变更"}
    }
    return dto.ActionPermission{Allowed: true}
}

func CanRejectChange(actor service.ActionActor, c *Change) dto.ActionPermission {
    // 状态判定和自我排除与 Approve 完全一致，只是文案不同——镜像 Ticket 域
    // CanReject 包装 CanApprove 再改文案的模式。非空意见的校验属于请求体校验，
    // 不在这里判定（见设计原则 3）。
    perm := CanApproveChange(actor, c)
    if !perm.Allowed && perm.Reason == "不能审批自己提交的变更" {
        perm.Reason = "不能驳回自己提交的变更"
    }
    return perm
}

func CanStartImplementation(actor service.ActionActor, c *Change) dto.ActionPermission {
    // 必须按 c.Type 分支——IsValidChangeStatusTransition 本身就是按类型分表的，
    // 三种类型对 "能不能从 approved/scheduled 进 in_progress" 的答案互不相同：
    //   standard：approved 或 scheduled 都可以
    //   emergency：只有 approved 可以（emergency 的表里根本没有 scheduled 分支）
    //   normal（默认）：只有 scheduled 可以，approved 不行
    var allowedSources map[string]bool
    switch c.Type {
    case "standard":
        allowedSources = map[string]bool{"approved": true, "scheduled": true}
    case "emergency":
        allowedSources = map[string]bool{"approved": true}
    default: // normal
        allowedSources = map[string]bool{"scheduled": true}
    }
    if !allowedSources[c.Status] {
        return dto.ActionPermission{Allowed: false, Reason: "当前状态和变更类型不允许开始实施"}
    }
    if !canWriteChange(actor) {
        return dto.ActionPermission{Allowed: false, Reason: "无权限开始实施"}
    }
    return dto.ActionPermission{Allowed: true}
}

func CanCompleteImplementation(actor service.ActionActor, c *Change) dto.ActionPermission {
    if c.Status != "in_progress" {
        return dto.ActionPermission{Allowed: false, Reason: "只有实施中的变更可以标记完成"}
    }
    if !canWriteChange(actor) {
        return dto.ActionPermission{Allowed: false, Reason: "无权限完成实施"}
    }
    return dto.ActionPermission{Allowed: true}
}

func BuildChangeActions(actor service.ActionActor, c *Change) map[string]dto.ActionPermission {
    return map[string]dto.ActionPermission{
        "submit_for_approval":  CanSubmitForApproval(actor, c),
        "approve":              CanApproveChange(actor, c),
        "reject":               CanRejectChange(actor, c),
        "start_implementation": CanStartImplementation(actor, c),
        "complete_implementation": CanCompleteImplementation(actor, c),
    }
}
```

（`Change`/`c.Type`/`c.CreatedBy` 字段名以 `handlers/change` 包内部领域结构体实际定义为准；
`c.Type` 的三个取值——`standard`/`emergency`/`normal`（或 `default` 分支覆盖的其它值）——需要
在实现阶段核对 `dto.ChangeType*` 常量的准确拼写。）

## 5. 前端变更

三个域的前端改动**各自独立成一次改动**（见 §7），且同一次改动里必须同时完成：
按钮块开始读取 `useWorkItemContext().actions` + 删除该域在
`workflow-state-machine.ts` 里对应的死代码——不拆成"后端先上、前端后补"两步（`AGENTS.md`：
新路径替换旧路径要在同一次改动里删旧路径）。

**关键澄清（评审发现，写第一版 spec 时遗漏）**：`IncidentDetail.tsx`/`ProblemDetail.tsx`/
`ChangeDetail.tsx` **现在完全没有调用过 `useWorkItemContext()`**——不是"把现有调用逻辑换个
数据源"，是这三个组件第一次开始消费这个 hook。而且三个域全部 15 个动作里，没有一个是
"点击后直接无参调用 `onActionDispatch(action)`"这种通用形状——7 个要弹 Modal 收集必填字段
（升级理由、指派人、审批意见等），其余 8 个虽不弹窗，也是直接调用各自专属的 API 方法
（如 `handleClose` 直接调 `IncidentAPI.closeIncident(id)`）。**这次改动只换"能不能点/为什么
不能点"的判断来源（从内联 `status===X` 换成 `actions.X.allowed/reason`），每个按钮原有的
点击处理函数、Modal、专属 API 调用全部保持不变。**

### 5.1 共享基础设施：`WorkItemShell` 新增 `showActionBar` 开关

`WorkItemShell.tsx` 现在无条件渲染 `<WorkItemActionBar />`（`WorkItemShell.tsx:46`，在
`{professionalPanelSlot}` 之前）。一旦三个页面开始把真实 `actions` 传给 `WorkItemShell`
（§5.2），这个顶部通用按钮栏会和下面 §5.3-5.5 改造后的专业 Detail 组件渲染同一批动作——
造成重复按钮。而且通用 Action Bar 的"无参直接 dispatch"模型接不上这三个域任何一个真实动作
（上面已说明），继续渲染它没有意义。

**决策（用户已确认，方案 B）**：给 `WorkItemShellProps`
（`itsm-frontend/src/components/work-item/WorkItemTypes.ts`）新增
`showActionBar?: boolean`（默认 `false`），`WorkItemShell.tsx` 改为
`{showActionBar && <WorkItemActionBar />}`。Incident/Problem/Change 三个页面都不设置这个
prop（保持默认 `false`），顶部通用栏不渲染，`actions` 仍然通过 `WorkItemProvider` 正常
下发到 context，供专业 Detail 组件读取。`WorkItemActionBar.tsx` 组件本身和它的测试不删除、
不改——只是这三个域暂时不挂载它。以后如果出现真正"无需表单、点击即走"的简单动作（某个更
轻量的 WorkItem 子类型），可以把这个 prop 设为 `true`。

这一改动是三个域共用的基础设施，**只需要在三个独立改动中最先落地的那一个里做一次**——
`showActionBar` 默认值是 `false`，向后兼容，不影响还没上线的另外两个域。谁先实现（见 §7），
谁就顺带把这个 prop 加上；后两个域的实现计划里不需要重复这一步，直接消费默认值即可。

原本设计里 `WorkItemActionBar.tsx` 的 `ACTION_LABELS` 缺少本设计新增的 8 个 key
（`escalate`/`reopen`/`mark_major_incident`/`convert_to_problem`/`start_investigation`/
`submit_for_approval`/`start_implementation`/`complete_implementation`）——因为这个组件
对这三个域根本不渲染，这个缺口不再需要修补。

### 5.2 三个页面：拉取并透传真实 `actions`

三个页面（`app/(main)/{incidents,problems,changes}/[id]/page.tsx`）现在硬编码
`actions={{}}`、`onActionDispatch={async () => {}}`——这是本设计要解决的问题本身，但第一版
spec 遗漏了把它列为要做的事。

- 后端响应类型：`itsm-frontend/src/lib/api/incident-api.ts` 的 `Incident` 接口、
  `problem-api.ts` 的 `Problem` 接口、`change-api.ts` 的 `Change` 接口，各自新增
  `actions?: Record<string, WorkItemActionState>` 字段，对应 §4.2 新增的
  `dto.XxxResponse.Actions`。
- 三个页面的 `toWorkItemCommon`（或紧邻它的组装逻辑）取 `incident.actions ?? {}`（对应
  Problem/Change 同理）传给 `<WorkItemShell actions={...} .../>`，替换掉硬编码的 `{}`。
- `onActionDispatch={async () => {}}` **保持不变，不删除**——因为 §5.1 的决策是专业 Detail
  组件自己的按钮块继续用自己原有的点击处理函数/Modal 直接调用专属 API，不经过这个回调；
  它只是 `WorkItemShellProps` 的必填 prop，在 `showActionBar=false` 时实际上不会被调用。

### 5.3 `IncidentDetail.tsx`

- 首次引入 `useWorkItemContext()` 调用，读取
  `actions.{resolve,close,reopen,escalate,assign,mark_major_incident,convert_to_problem}`，
  用 `allowed`/`reason` 替换现有的内联 `status === X` 判断和按钮 `disabled` 逻辑。每个按钮
  原有的 `onClick`（`handleEscalate`/`handleAssignClick`/`handleResolveClick`/`handleClose`/
  `handleConvertToProblem`/`handleReopen`）和对应的 Modal/表单**保持不变**。
- 删除 `handleResolveClick` 里对 `isValidIncidentTransition`（来自
  `workflow-state-machine.ts`）的客户端预检调用（`IncidentDetail.tsx:262`）——这次是
  `actions.resolve.allowed` 说了算，不需要再在前端另判断一次。
- `edit` 目前是纯导航，点击行为不用改，按钮显隐改读 `actions.edit.allowed`。

### 5.4 `ProblemDetail.tsx`

- 首次引入 `useWorkItemContext()` 调用。"开始处理"按钮目标状态从
  `ProblemStatus.IN_PROGRESS` 改为 `ProblemStatus.INVESTIGATING`
  （`itsm-frontend/src/constants/problem.ts` 已有这个枚举值），动作 key 相应改名，点击后
  仍然调用同一个 `handleUpdateStatus`（只是传入的目标状态变了）。
- 三个按钮的 `disabled`/`loading` 逻辑改为读 `actions.{start_investigation,resolve,close}`。
- "编辑"按钮（纯导航）的显隐也改读 `actions.edit.allowed`，理由同 §5.3。

### 5.5 `ChangeDetail.tsx`

- 首次引入 `useWorkItemContext()` 调用。`canApprove`（现在只判断 `status === PENDING`）
  替换为直接读 `actions.approve.allowed`/`actions.reject.allowed`（两者现在可能因为自我
  审批排除而不同，不能再共用同一个布尔值）。
- "开始实施"按钮替换为读 `actions.start_implementation.allowed`——不再需要前端自己判断
  `status ∈ {approved, scheduled}`，这个判断现在依赖 `change.type`，后端已经算好。
- "提交审批"按钮和"完成"按钮的可见性判断也一并改为读 `actions.submit_for_approval.allowed`/
  `actions.complete_implementation.allowed`——虽然这两个动作的判定逻辑本身和前端现状没有
  差异，但按 §3 原则 2，同一个组件里不能只改一部分按钮、留另一部分继续用内联
  `status === X` 判断，那样会让 `ChangeDetail.tsx` 里同时存在两套并行的权威来源。
- 所有按钮原有的点击处理函数（`handleApprove`/`handleReject`/`handleStartImplementation`/
  `handleCompleteImplementation`）和 Modal/表单保持不变。

### 5.6 `workflow-state-machine.ts` 清理

删除 `INCIDENT_STATUS_TRANSITIONS`、`isValidIncidentTransition`（随 §5.3 一起删，因为
`IncidentDetail.tsx:262` 是它唯一的调用点）、`CHANGE_STATUS_TRANSITIONS`（随 §5.5 一起删；
审计未发现任何组件实际调用它，属于已经死掉但一直没清理的代码）。`VALID_TICKET_TRANSITIONS`
（Ticket 域自己的表）不属于本设计范围，不动。

## 6. 明确不做的事

- 不实现 Change `rollback`/`cancel`、Incident `cancel`（§2.3）。
- 不给 Problem 的 `investigate`/`root-cause`/`solution`/`close` 专属接口接前端表单（§2.3）。
- 不新增任何细粒度 RBAC 权限行，不改 seeder；`incident:assign` 路由改成查 `incident:write`
  是修一个既有的 0-可用 bug，不是新增权限（§2.2 评审发现）。
- 不给 Change 的审批判定加 BPMN 任务存活性查询（§2.2 决策）。
- 不放宽 Problem `close` 到 open/investigating/identified 也能直接关闭（§2.2 决策）。
- 不新增 Change 的 `assign` 动作（当前 UI 没有这个按钮，`AssignChange` 后端虽无状态限制但
  没有前端入口可固化）。
- 不改 `dto.ActionPermission`/`WorkItemActionState`/`WorkItemContextValue` 的形状。
- **不让 `WorkItemActionBar` 在这三个域渲染**（§5.1 评审发现并修正）——15 个动作没有一个
  适合它"无参直接 dispatch"的模型，专业 Detail 组件保留自己现有的按钮/Modal/API 调用，
  只是判断来源换成 `actions`。`showActionBar` 默认 `false`，三个页面都不设置它。
- 不重构 `IncidentDetail.tsx`/`ProblemDetail.tsx`/`ChangeDetail.tsx` 里各按钮原有的点击
  处理函数、Modal、专属 API 调用——只换判断来源，不换动作怎么发起。

## 7. 阶段划分

Problem 和 Change 的 actions 可独立实施；Incident 的 `convert_to_problem` 不能再与 Problem 域
完全独立，因为它必须调用 §4.4 的事务化 `ProblemConversionService`。先完成这一窄的跨域命令替换，
再上线 Incident actions；两者作为一个可回滚的功能单元。**有一处共享前端基础设施**（`WorkItemShellProps` 新增
`showActionBar` 开关，§5.1）需要在三个改动里**最先落地的那一个**里完成，后两个直接消费
默认值，不用重复这一步——下面按 Incident 最先假设编号，实际顺序由实施时决定：

1. **Incident → Problem 命令收敛 + Incident actions**：`ent/schema/work_item_relation.go` 新增
   live `investigated_by` 的部分唯一索引；新增 bootstrap 前置检查
   `prepareIncidentProblemRelationMigration`，在 `client.Schema.Create(ctx)` **之前**检查存量
   `work_item_relations` 是否有同一 `(tenant_id, source_work_item_id)` 的多条 live
   `investigated_by` 关系，并在冲突时列出 tenant、source 与冲突 target/relation 后失败。Ent schema
   是该索引唯一的 DDL 权威：前置检查通过后才让 `Schema.Create` 创建索引，不登记第二份
   `RegisteredMigration` 或手工重复建索引；空库/表尚不存在时按现有 bootstrap preflight 模式跳过。
   `handlers/problem` 新增窄的
   `ProblemConversionService.CreateFromIncident` 及 repository 事务实现 + bootstrap 注入到
   `IncidentController` + 删除 `RootCauseAnalysisService.CreateProblemFromIncident` 和旧 Controller
   调用 + `service/action_actor.go`（提取共享结构体）+ `service/incident_authorization.go`（含
   assign 命令共享状态谓词、convert relation 查询）+ `dto.IncidentResponse.Actions` 字段 +
   `IncidentService.GetIncidentWithActions` 挂载（保留原 `GetIncident` 的无 actions 调用约定）+
   修复 `router.go:798` 的
   `RequirePermission("incident","assign")` → `RequirePermission("incident","write")`
   （§2.2 评审发现）+ `WorkItemShellProps.showActionBar` 开关（§5.1，共享基础设施，若
   Incident 最先实施则由这个改动完成）+ `incident-api.ts` 的 `Incident.actions` 字段 +
   `incidents/[id]/page.tsx` 透传真实 `actions`（§5.2）+ `IncidentDetail.tsx` 重构
   （§5.3）+ 删除 `workflow-state-machine.ts` 的 Incident 部分（§5.6）。
2. **Problem actions**：`handlers/problem/authorization.go` +
   `dto.ProblemResponse.Actions` 字段 + `Handler` 注入 Ent client、`Handler.Get` 挂载 + （若尚未存在）
   `WorkItemShellProps.showActionBar` 开关 + `problem-api.ts` 的 `Problem.actions` 字段 +
   `problems/[id]/page.tsx` 透传真实 `actions` + `ProblemDetail.tsx` 重构（含"开始处理"
   改名/改目标状态）。
3. **Change actions**：`handlers/change/authorization.go`（含共享的 self-approval 谓词）+
   `ChangeService.TransitionStatus` 的命令侧 self-approval 拒绝 + `dto.ChangeResponse.Actions` 字段 +
   `Handler.GetChange` 挂载 + （若尚未存在）
   `WorkItemShellProps.showActionBar` 开关 + `change-api.ts` 的 `Change.actions` 字段 +
   `changes/[id]/page.tsx` 透传真实 `actions` + `ChangeDetail.tsx` 重构 + 删除
   `workflow-state-machine.ts` 的 Change 部分（§5.6）。
4. 每个改动独立验证（`go build`/`go vet`/`go test ./...`、`npm run type-check`、
   `npm test`），不合并成一个大 PR——延续 Phase 1-3 的节奏。
5. 本设计除 `investigated_by` 的部分唯一索引外不新增业务表或历史回填；Incident 指派、Change
   审批和 Incident → Problem 同时包含命令端校验/事务写路径收敛。缺少 WorkItem 的存量 Incident
   必须 fail closed 并沿用现有回填流程，不能由本功能临时补建不完整关系。部署启动时的前置检查
   必须先于 Ent 的 Schema Create 执行；若存量中存在一个 Incident 对多个 live Problem 的冲突关系，
   检查必须失败并输出冲突清单，由数据治理决定保留关系后再重试，禁止静默删除。

## 8. 测试计划

每个域的改动都需要：

- 后端单测覆盖 `BuildXActions` 里每一个动作在"应该 allowed"和"应该 not allowed"两侧至少
  各一个用例，尤其是本设计里发现并修正的几个真实 bug 点：
  - Incident：`resolve` 只在 `in_progress` 时 allowed（覆盖至少一个曾经"前端显示但后端会拒
    绝"的状态，验证现在不再显示/allowed 为 false）；`reopen` 覆盖 `closed → in_progress`
    （验证没有被共享表的"终态"说法挡住）；至少一条路由层集成测试验证
    `POST /incidents/:id/assign` 在只有 `incident:write` 权限（没有超级管理员）时不再
    403（验证 §2.2 的权限修复真的生效，不只是 `allowed` 字段算对了）；直接 POST 指派一个
    `resolved`/`closed` Incident 必须失败；两个并发转换请求只能创建一个 Problem，且成功结果同时
    具备 target WorkItem、唯一 `investigated_by` relation、IncidentEvent 与 AuditLog；任意 relation、
    IncidentEvent 或 AuditLog 写入失败时，断言目标 WorkItem/Problem/relation/两类记录均不遗留。
    前置检查必须在 `Schema.Create` 前发现并拒绝同一 source Incident WorkItem 的多条 live
    `investigated_by` 关系，并在错误中给出冲突 tenant/source/target 信息。
  - Problem：`start_investigation` 目标状态断言为 `investigating` 不是 `in_progress`。
  - Change：`start_implementation` 至少覆盖三种 `type` 各一个用例，尤其是
    `type=normal && status=approved` 必须 `allowed=false`（这是本设计要修的核心 bug）；
    `approve`/`reject` 覆盖 `CreatedBy == actor.UserID` 时 `allowed=false`，并分别验证直接请求
    `POST /approve`、`POST /reject` 同样被服务端拒绝。
- 前端：`npm run type-check` + 各 Detail 组件读取 `actions` 后的渲染断言（allowed/disabled/
  reason 三态），回归确认删除 `workflow-state-machine.ts` 相关代码后其余测试仍然通过；
  `WorkItemShell.test.tsx` 补一条断言——`showActionBar` 未设置（或为 `false`）时
  `WorkItemActionBar` 不渲染，避免以后有人不小心把它打开导致这三个域重复出现按钮。
- 集成测试：至少一条验证单条详情响应（`GET /incidents/:id` 等）包含非空 `actions` 字段，
  list 响应（`GET /incidents`）**不**包含 `actions` 字段（验证 §3 原则 5 的边界）；至少一条
  端到端场景验证页面上看到的按钮可点性和后端 `actions` 字段一致（不是从组件内部状态算出
  一份、从 API 又读出另一份）。Incident 的详情服务测试还必须断言状态 DTO 与 actions 使用同一
  次实体读取的快照，避免通过“先 `GetIncident`、再查实体”的双读取实现造成并发下的自相矛盾响应。

## 9. 最终实现契约与审查加固

本节记录 2026-08-30 最终代码审查后的权威实现，优先级高于前文实施前示例。前文中的
snake_case 动作名称用于描述业务动作或内部审计值时仍然有效；通过 HTTP 返回的 `actions`
对象属性必须遵循 `AGENTS.md` 的 camelCase 约定：

| 域 | 最终公开 Actions key |
|---|---|
| Incident | `edit`、`resolve`、`close`、`reopen`、`escalate`、`assign`、`markMajorIncident`、`convertToProblem` |
| Problem | `edit`、`startInvestigation`、`resolve`、`close` |
| Change | `submitForApproval`、`approve`、`reject`、`startImplementation`、`completeImplementation` |

`IncidentEvent.event_name=convert_to_problem` 和 `AuditLog.action=convert_to_problem` 是内部领域/审计
词汇，不是 JSON 字段名，因此本次不改名。

最终审查加固包含以下约束：

1. Change 与 Problem 的所有客户域 handler，包括创建、详情、列表、关联、生命周期变更和删除，
   统一调用 `middleware.ResolveRequestTenantID`。MSP 选定客户租户后，读写使用同一租户；缺失或
   未授权的租户上下文 fail closed，不回退到裸 JWT `tenant_id`。
2. Incident 的 `closed` 与 `cancelled` 统一由 `common.IsIncidentFinalStatus` 表达。转换为 Problem、
   标记重大事件、升级和指派的读写侧共享该终态语义；指派继续额外禁止 `resolved`。
3. `investigated_by` 关系类型下沉为 `common.WorkItemRelationInvestigatedBy`。删除由 Incident 转换
   得到的 Problem 时，在一个 Ent 事务中软删 Problem 和指向其 WorkItem 的实时关系，使源 Incident
   可以重新发起转换。
4. `CanStartImplementation` 不再维护平行状态集合，而是调用
   `service.IsValidChangeStatusTransition(current, in_progress, type)`；因此标准和紧急变更的合法
   `draft -> in_progress` 快速路径会正确投影为可执行。
5. Incident 指派路由继续使用现有 `incident:write` ACL。代码库没有已定义或已初始化的
   `incident:assign` 权限；如需细粒度指派权限，必须单独完成权限种子、角色迁移、路由和动作投影
   的整体设计，不能只修改路由字符串。
6. 三个专业详情页复用 `WorkItemActionButton`，统一缺失动作隐藏、拒绝原因、后端禁用与请求中
   互斥禁用行为；专业动作的 Modal 和 API 调用仍由各自 Detail 组件拥有。

最终验证证据：后端 `go test ./... -count=1` 通过；前端 5 个动作相关 Jest suite 共 27 个测试
通过；`npm run type-check`、`npm run lint:check`（0 error，3 个既有 warning）、`npm run build`
和 `git diff --check` 通过。
