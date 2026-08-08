# 修复工单默认审批流程"自己批自己" + 补齐缺失的 ticket_urgent_flow - 设计文档

## 背景与问题

上一轮 ServiceRequest→Ticket 委托重构的最终评审发现：`service/bpmn/ticket_general_flow.bpmn`（所有工单在没有匹配到更具体 `ProcessBinding` 时的兜底流程）的"工单审批"节点没有配置任何 `candidateGroups`/`candidateUsers`，导致审批任务的 assignee 在自动分配兜底逻辑下会落到申请人自己身上——申请人可以批准自己提交的工单。旧的 ServiceRequest 专属审批代码里有明确的"申请人不能审批自己的请求"校验，这次委托给 ticket 之后这条校验没有对应物。

调查过程中确认了两个相关但独立的问题：

1. **`ticket_general_flow` 是所有工单类型的兜底流程**（`process_resolver.go`：没有匹配到 `ProcessBinding` 时，incident/problem/change/service_request/普通工单都会落到这条流程），所以这个缺口影响面是全体工单，不只是服务请求。
2. **`process_resolver.go` 的 `ResolveWithPriority` 会把高/紧急优先级的兜底工单指向流程 key `"ticket_urgent_flow"`，但这个文件根本不存在**（`service/bpmn/` 目录下没有，`BPMNTemplateService` 的部署清单也没有这个 case）——任何没有特定流程绑定的高优先级工单，创建时会因为找不到流程定义直接报错失败。这是完全独立于 SR 委托重构的既有 bug，本次一并修复。

## 现状核实（代码走查，2026-08-08）

- **`authorizeTaskActor`（`service/bpmn_process_engine.go:400`）用 OR 逻辑**：`allowed(task.Assignee) || allowed(task.CandidateUsers)`。这意味着**光给 BPMN 文件加 `candidateGroups` 并不能关掉自己批自己的口子**——`Assignee` 字段是独立解析的，只要它还落到申请人身上，申请人依然能通过 `allowed(task.Assignee)` 这一支操作任务。
- **`createUserTask`（`service/bpmn_process_engine.go:617`）的 assignee 解析优先级**：BPMN 显式声明 > `requester_id`（流程变量）> `triggered_by` > `assignee_id` > `getDefaultAssigntee`（数据库规则匹配兜底）。因为工单创建时 `requester_id` 几乎总能解析成功，`getDefaultAssigntee` 这套已经写好、更合理的兜底逻辑实际上从未被执行到。
- **`Activity_Approval` 节点没有打 `taskPurpose="approval"` 标记**——这个属性 schema 层面已经支持（`service/bpmn_types.go:86`，`xml:"taskPurpose,attr"`），但当前所有 BPMN 文件都没用，代码没法专门区分"这是审批任务"。
- **这是系统性缺口，不是个别文件的疏漏**：全仓库 12 个 BPMN 文件、20+ 个"审批"命名的用户任务节点（CAB审批、发布审批、事件主管审批、方案审批等）都没有配置 `taskPurpose`/`candidateGroups`。
- **`GroupResolver`（`service/bpmn/bpmn_group_resolver.go`）是已经写好但完全未被引用的基础设施**——`ExpandGroupsToUsers`/`MergeCandidateUsers`/`GetUserGroupNames` 把 BPMN candidateGroups（组名 CSV）展开成具体用户，配套的 `Group` 实体有完整的 CRUD（`service/group_service.go`）和管理界面（`/admin/groups`），只是至今没有任何 BPMN 文件用到。
- **`Department.manager_id`（`ent/schema/department.go:28`）已存在**，`User` 有 `department_id` + `department_ref` 边（`ent/schema/user.go:37,79`），支持"申请人所在部门 → 部门负责人"这条查询路径。
- **`ticket_urgent_flow` 除了 `process_resolver.go` 里的字符串常量外，全仓库零引用**——没有 `.bpmn` 文件，没有部署清单条目，找不到就直接返回"获取流程定义失败"。
- **引擎目前没有真正可用的定时器/SLA 差异化能力**（`service/bpmn_process_engine.go`只处理 `handleElement` 里明确分支的 UserTask/EndEvent/ExclusiveGateway/ServiceTask 四种，边界定时器不执行）——所以"紧急流程"目前无法通过真实的超时/升级差异来体现紧急程度。

## 目标架构

### 部分一：审批任务不再回退到申请人自己

```
createUserTask() 遇到 task.TaskPurpose == "approval" 时，走专门分支：

  1. 主路径：解析申请人所在部门的 manager_id 作为 assignee
     User.department_ref → Department.manager_id
     （如果解析出的 manager_id == requester_id 本人，视为"未解析出有效审批人"，
      走兜底路径——避免部门负责人审批自己提交的工单这个边界情况）

  2. 兜底路径（部门未配置 / 没有 manager_id / manager 就是申请人自己）：
     走固定 candidateGroups="ticket-approvers"（复用已有 GroupResolver，
     租户需要在 /admin/groups 里建这个组、加审批人进去，否则任务无人可领——
     这是已知的、可接受的初始状态，不在这次修复范围内自动建组/加人）

  3. 无论走哪条路径，taskPurpose="approval" 的任务永远不会把 assignee
     解析为 requester_id / triggered_by（这两步在 approval 分支里整个跳过，
     不是"跳过之后还有几率兜底回同一个值"）
```

### 部分二：`ticket_general_flow.bpmn` 打上 `taskPurpose="approval"` 标记

只改这一个文件的 `Activity_Approval` 节点。其余 11 个文件的同类节点（CAB审批、发布审批等）现状不变，明确记录为已知问题、留到后续单独处理（见"非目标"一节）——Go 代码层面的修复是通用机制，那些文件只要以后补上 `taskPurpose="approval"` 就能立刻受益，不需要再改 Go 代码。

### 部分三：补齐 `ticket_urgent_flow.bpmn`

新建 `service/bpmn/ticket_urgent_flow.bpmn`，内容是 `ticket_general_flow.bpmn` 的副本（含本次的 `taskPurpose="approval"` 标记），只改 `process id`/`name`/`metaData` 中的描述性字段（比如流程名改成"紧急工单流程"）。不引入任何实质行为差异——`process_resolver.go` 里高/紧急优先级路由过去的这条流程，现在会是一条真正可部署、结构上等价于通用流程的独立流程定义，而不是指向一个不存在的 key。同时补上 `bpmn_template_service.go` 部署清单里对应的 case（比照 `ticket_general_flow` 那一条）。

## 实施前提

- 需要在 `/admin/groups` 手动创建 `ticket-approvers` 组并添加成员，否则"部门没配 manager"这种情况下审批任务会无人可领。这不是代码要处理的事，写进部署/运维文档里说明即可。

## 测试计划

- `createUserTask` 遇到 `taskPurpose="approval"` 且无 BPMN 显式 assignee 时：申请人所在部门有 `manager_id` 且不是申请人自己 → assignee 正确解析为该 manager。
- 部门没有 `manager_id`，或 `manager_id` 就是申请人自己 → assignee 不落到申请人，`candidateGroups` 展开的 `ticket-approvers` 组成员进入 `candidate_users`。
- 明确写一条断言："即使流程变量里 `requester_id` 有效，`taskPurpose="approval"` 的任务 assignee 也绝不等于 `requester_id`"——这是本次修复最核心的安全断言。
- `authorizeTaskActor`：申请人尝试对自己提交工单的审批任务调用完成接口 → 拒绝（`当前用户不是该任务的审批人或候选人`）。
- `ticket_urgent_flow` 能被 `BPMNDeploymentService`/`BPMNTemplateService` 正常发现和部署，`StartProcess("ticket_urgent_flow", ...)` 能成功创建流程实例（不再报"获取流程定义失败"）。
- 跨租户：部门/manager 解析、candidateGroups 展开都要带 tenantID 过滤，补跨租户隔离测试。

## 非目标（本次不做）

- 不修复其余 11 个 BPMN 文件（change_normal_flow、release_approval_flow、incident_emergency_flow 等）里同类的"审批节点没有 taskPurpose/candidateGroups"问题——明确记录为已知的系统性缺口，Go 代码修好后这些文件后续补标记即可受益，但本次不主动去改。
- 不给 `ticket_urgent_flow` 设计任何区别于 `ticket_general_flow` 的真实行为差异（超时/升级规则等）——引擎目前没有可用的定时器能力支撑这类差异，属于更大的、需要单独讨论的工作。
- 不自动创建 `ticket-approvers` 组或往里加成员——这是部署/运维步骤，不是代码逻辑。
- 不涉及"审批收敛"更大范围的工作（legacy approval_controller/approval_chain_controller 清理、change CAB 会签建模等）——这些在更早的最终评审里已经被记录为独立的后续工作。
