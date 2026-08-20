# 工单执行分配"审批后置"设计：从创建时同步分配改为 BPMN 节点驱动的团队工作负载均衡

日期：2026-08-20
状态：待实施

## 背景

本轮 Copilot 采购申请审批链的真实手工测试（提交人 → 部门负责人审批 → 总经理审批 → IT总监审批，参见
[[2026-08-20-personal-manager-chain-approval-design]]）过程中，顺带发现两个独立但相关的问题：

1. **工单一创建就被同步"自动分配"给一个跟工单内容毫不相关的人**——嘉里物流真实 eHR 数据环境下（租户内
   7826 个 active 用户），一张 Copilot 采购申请单在还没有任何人审批之前，就被分配给了"人力资源助理总经理"
   （因为分配算法把全体 active 用户都当候选人，没有人有工单负载，评分打平，谁"赢"基本随机）。
2. **通知系统有一个未触发但真实存在的"广播全员"兜底分支**——如果自动分配连个人都分不出来，代码会给除申请人
   外的全体 7826 个真实员工发通知邮件（真实连了 Microsoft Graph，会真的发到真实企业邮箱）。

深挖这两个问题的根因，发现它们本质上是同一件事：**当前架构里，"谁来处理这张工单"这件事在工单刚创建、审批
流程甚至还没开始时就被决定了。** 用户明确的业务预期是：分公司用户提交 → 部门经理审批 → 总经理审批，**通过
之后才进入 L1/L2/L3 处理流程（路由、分配）**；不需要审批的请求（例如吊销 VPN 权限）则直接进入 L1/L2/L3
自动分配，不用等谁批准。现有代码完全没有体现这个"审批网关在前、执行分配在后"的顺序。

## 调研结论（现状，作为设计依据）

- **Bug① 根因**：`service/ticket_service.go:192-199`，`TicketService.CreateTicket` 在
  `s.repo.Create` 刚成功、工单还没有任何审批发生时，同步调用
  `s.assignmentSmartService.AutoAssign(ctx, tkt.ID, tenantID)`。这个调用链最终落到
  `service/ticket_assignment_service.go:148-184` 的 `getAvailableUsers`——`s.client.User.Query()`
  只按 `tenant_id`+`active` 过滤，**没有任何"这个人是不是支持/IT人员"的业务过滤**，把租户下全部 active 用户
  当候选人。
- **Bug② 根因**：`service/ticket_notification_service.go:229-249`，`NotifyTicketCreated` 里
  "如果只有创建人（没有处理人），广播给所有 admin" 的分支，注释写的是"广播给 admin"，但实际代码
  `s.client.User.Query().Where(user.TenantID(...)).Where(user.IDNEQ(ticket.RequesterID))` 没有任何角色
  过滤，会把除申请人外的全部用户都拉进收件人列表。
- **两个 Bug 共同的根本问题——触发时机错了**：现有 BPMN 引擎已经有一套成熟的"节点创建前先解析出 assignee，
  直接写库"的声明式路由机制（`service/bpmn_process_engine.go:817-869` 的 `createUserTask` 内
  switch），但这套机制**只服务 `taskPurpose="approval"` 的节点**（`assigneeRole`→`resolveRoleCandidates`，
  `assigneeDeptId`/`assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId`→`resolveFixedScopeAssignee`，
  `assigneeGmChain`→`resolveGmChainAssignee`）。"这张工单该由谁执行"这件事完全没有对应的声明式路由方式，
  只能退回工单创建时那个不区分身份的兜底分配。
- **现有"自动分配"BPMN 节点都是执行者，不是决策者**：`service/bpmn/ticket_handler.go:301-354`
  （`Activity_Assign`/`action=assign`）和 `service/bpmn/incident_handler.go:121-163`
  （`Activity_AutoAssign`/`action=assign_incident`）两个 ServiceTask 处理器，都要求调用方已经把
  `assignee_id` 当流程变量传进来，自己完全不做候选人计算——真正会计算"该分给谁"的只有
  `TicketAssignmentService`（就是 Bug① 那个），而它跟 BPMN 完全不连。
- **`teams` 与 `groups` 是两套互不相通的机制**：`groups`/`GroupResolver.ExpandGroupsToUsers`
  （`service/bpmn/bpmn_group_resolver.go:68`）是审批候选组兜底用的，读的是 `groups` 表（当前只有
  `ticket-approvers` 一条）；`teams` 表（10 个真实团队：服务台-L1/L2/L3、服务器运维、网络运维、数据库运维、
  云平台运维、应用支持、安全运营、变更委员会）目前唯一的消费者是 `service/approver/team_leader_resolver.go`
  的 `TeamLeaderResolver`，语义是"团队负责人审批"（读 `team.manager_id`，解析出**一个人**），不是"团队成员
  池"。真实 eHR 导入的 7826 个用户，`teams`（`users.team_users`）和 `roles`（`user_roles`）两套机制都是
  0 人绑定——这是数据准备缺口，不是本次代码改动范围。
- **逐个核对现有 BPMN 流程里"分配节点"相对"审批节点"的实际位置**（这是本次设计能不能成立的关键前提，逐个
  读了 `.bpmn` 源文件确认，不是猜的）：
  - `service/bpmn/service_request_flow.bpmn`（服务目录默认走的通用流程，`service_request_urgent_flow.bpmn`
    结构相同）：`Activity_Execute`（"执行服务"）的三条入边——`Gateway_Approval` 的"不需要审批"分支
    （`Flow_4`）、`Gateway_ApprovalResult` 的"通过"分支（`Flow_Approved`）——**位置正确**，需要审批的必须先
    过 `Activity_Approval`（`taskPurpose="approval"`）才能到达，不需要审批的直接到达；只是这个节点本身没打
    任何路由属性，创建时落到通用兜底逻辑。
  - `service/bpmn/ticket_general_flow.bpmn`：**位置错误**。`Activity_Assign`（"任务分配"）在
    `Gateway_Approval`（"是否需要审批?"）**之前**（`Flow_1: StartEvent→Activity_Assign`，
    `Flow_ApprovalCheck: Activity_Assign→Gateway_Approval`）——先分配、再判断要不要审批，跟业务预期的顺序
    完全相反。真正在审批之后的是 `Activity_Handle`（"工单处理"，"不需要审批"/"审批通过"/"审批驳回"三条边都
    汇入它），位置正确。
  - `service/bpmn/incident_emergency_flow.bpmn`（含 `_v1.1`/`_cn` 两个变体）：`Activity_AutoAssign`
    在 `Activity_ManagerApproval`（"主管审批"）**之前**（`Flow_1: StartEvent→Activity_AutoAssign`，
    `Flow_Manager: Activity_AutoAssign→Activity_ManagerApproval`）。但 `Activity_ManagerApproval`
    **没有 `taskPurpose="approval"`**，根本不是正式审批链的一部分——这是事件响应本来就该有的设计（先有人
    响应，"审批"是别的用途，不是准入门槛），跟服务请求/工单这条线的业务规则不是一回事，**本次不改动**。
  - `copilot_procurement_flow`（本次 Copilot 试点用的专属流程）：纯审批链（部门负责人→总经理→IT总监），
    IT 总监批准后直接 `EndEvent_1`，**没有执行环节**——买许可证这个动作目前假设在 ITSM 系统外完成，但按本轮
    讨论结论，应该补一个执行环节（比如"开通许可证账号"）接入新机制。

## 范围

**本次设计覆盖**：

1. 新增 BPMN 声明式路由属性 `taskPurpose="fulfillment"`，与既有 `taskPurpose="approval"` 并列——`createUserTask`
   识别到这个值时，走一条新的、独立于审批 switch 的分支，触发团队工作负载均衡解析，不复用审批那套
   `assigneeRole`/`assigneeDeptId`/`assigneeGmChain` 属性（语义不同：审批解析的是"谁有资格批"，这里解析的是
   "谁来干活"）。
2. 新增 `TeamWorkloadResolver`（`service/approver/team_workload_resolver.go`），跟
   `DeptManagerResolver`/`PersonalManagerResolver`/`TeamLeaderResolver` 同一个家族。算法：候选人 =
   `users.team_users` 指向"服务台-L1"（固定 team，不做分类→team 映射，所有需要 L1/L2/L3 执行的工单统一先进
   L1，L1 内部再人工升级转发给专精团队——复用现有 `Activity_AutoEscalate` 模式，不新建升级机制）的 active
   用户；工作负载查询复用本次会话已经修好的批量聚合写法（`service/ticket_assignment_service.go` 里新增的
   `batchGetUserWorkloads`/`assigneeAggRow` 那套 `GroupBy`+`Aggregate(ent.Count())`，只是把候选人来源从
   "全体 active 用户"换成"服务台-L1 成员"），挑 `ActiveTickets` 最少的一个直接写 `ProcessTask.assignee`——
   不做"候选组展开、人工领取"（`candidateGroups`）那一套，因为已经决定自动挑最闲的人，不需要人工认领。
   候选池为空（当前 100% 会命中，因为服务台-L1 还没有真实成员）时，只记 `Warnw` 日志，**不落到
   `approvalFallbackCandidateGroup`（"ticket-approvers"）那个候选组兜底**，也不触发任何通知——跟设计二
   的"无 assignee 不广播"策略是同一件事的两面。
3. 删除 `TicketService.CreateTicket`（`service/ticket_service.go:192-199`）里工单一创建就同步分配的
   逻辑；`TicketAssignmentService.autoAssignTicket`/`getAvailableUsers` 以及它们专用的私有方法
   （`calculateUserScore`/`calculateSkillScore`/`calculateWorkloadScore`/
   `calculateCategoryExperienceScore`/`calculatePerformanceScore`/`checkUserSkills`/
   `checkUserCategoryAccess`/`getMaxActiveTickets`/`batchGetUserWorkloads` 等，含本次会话为修性能新增的
   批量查询方法——那批优化后的查询逻辑不浪费，直接迁到 `TeamWorkloadResolver` 里复用，只是候选人来源换了）
   整体删除。`GetUserWorkload`/`GetTeamWorkload`/`AssignTicket`（手动指定 `PreferredUser` 那条路径）/
   `ReassignTicket`/`GetTicketsByAssignee`/`AssignTickets` 这些只读统计或人工操作接口保留——
   `controller/ticket_assignment_controller.go` 还在用，跟"自动分配该怎么选人"这件事无关。
4. BPMN 文件改造：
   - `service_request_flow.bpmn`、`service_request_urgent_flow.bpmn`：`Activity_Execute` 加
     `taskPurpose="fulfillment"`。
   - `ticket_general_flow.bpmn`：**删除 `Activity_Assign` 节点**（`Flow_1` 直连 `Gateway_Approval`，
     不再经过它），`Activity_Handle` 加 `taskPurpose="fulfillment"`——分配职责完全交给审批之后的
     `Activity_Handle`，避免"审批前分配一次、审批后 `Activity_Handle` 又要处理一次"的语义重复。
   - `copilot_procurement_flow`：IT 总监审批通过后新增一个 `taskPurpose="fulfillment"` 的
     `Activity_Execute`（如"开通 Copilot 许可证账号"）节点，再到 `EndEvent_1`。
5. `NotifyTicketCreated`（`service/ticket_notification_service.go`）按新的时机模型重写：
   - 有 `assignee`（意味着流程已经走到 `Activity_Execute`/`Activity_Handle` 且 `TeamWorkloadResolver`
     成功解析出人）→ 通知 assignee + 申请人，**不变**。
   - 没有 `assignee`，是因为工单刚创建、还没走到执行分配这一步（现在是**正常状态**，不是异常）→ 只通知
     申请人，**不再有"广播全体/admin"这条路**。
   - `TeamWorkloadResolver` 到了 `Activity_Execute` 却解析不到人（服务台-L1 是空的）→ 沿用设计一定的策略，
     只记警告日志，不发任何通知——这是真正的异常信号，靠后台监控/日志发现，不该用邮件轰炸的方式暴露。

**明确不在本次范围内**：

- `teams.team_users` 的批量回填（谁属于服务台-L1）——数据准备问题，不是代码改动范围；试点验证阶段需要先手动
  往服务台-L1 加几个测试账号（跟 [[2026-08-20-personal-manager-chain-approval-design]] 里手动给 GM
  链测试账号发角色是同一类操作）。
- L1/L2/L3 之间怎么升级转发——沿用 incident 流程里已有的 `Activity_AutoEscalate` 模式，本次不新建独立机制，
  也不做"工单分类 → 具体是哪个专精团队"的映射（服务台-L1 收到后人工判断转给哪个团队）。
- `assigneeTeamId`（团队负责人审批用的既有属性，`TeamLeaderResolver`）不受影响，语义和实现都不改——
  它是"团队负责人当审批人"，跟本次"团队成员池当执行人"是两个不同场景，命名上刻意不复用避免混淆。
- `incident_emergency_flow`（含 `_v1.1`/`_cn`）三个变体一律不改——`Activity_AutoAssign` 在非正式审批节点
  之前是这条线本来的设计意图，不违反"先审批后分配"规则（因为它压根没有正式审批节点）。

## 设计一：`taskPurpose="fulfillment"` + `TeamWorkloadResolver`

`bpmn_types.go` 的 `BPMNUserTask.TaskPurpose` 字段已经存在（现在只有 `"approval"` 一个有效值被消费），
不需要加新字段，只需要在 `createUserTask` 里 `task.TaskPurpose == "approval"` 的 `if` 分支旁边，加一个
`task.TaskPurpose == "fulfillment"` 的并列分支：

```go
} else if task.TaskPurpose == "fulfillment" {
    assignee = e.resolveFulfillmentAssignee(ctx, instance)
    if assignee == "" {
        e.logger.Warnw("执行任务未在服务台-L1解析到可用处理人，工单暂不分配",
            "taskID", task.ID, "instanceID", instance.ID)
        // 不落候选组兜底，不触发任何通知——留空 assignee，等团队有真实成员后
        // 由人工在"我的待办"/工单列表里认领，或后续补一个定时扫描重试（不在本次范围）。
    }
}
```

`resolveFulfillmentAssignee` 调用 `approver.NewTeamWorkloadResolver().Resolve(ctx, e.client, &approver.ApproverContext{TenantID: instance.TenantID})`——
不需要 `RequesterID`/`DepartmentID`（跟审批解析器不同，执行分配不看"申请人是谁"，只看"服务台-L1 团队现在
谁最闲"）。`TeamWorkloadResolver` 内部：

1. 先按 `teams.name = "服务台-L1" AND tenant_id = ?` 查出这个租户下服务台-L1 团队的 `id`（不能跨租户硬编码
   `team.id`，`teams` 是租户隔离的，每个租户的服务台-L1 是不同的行），再查
   `users WHERE team_users = <上一步查到的 team.id> AND tenant_id = ? AND active = true`，得到候选人
   ID 列表。
2. 候选人列表为空直接返回错误（调用方据此记警告日志，不再往下走）。
3. 复用 `TicketAssignmentService` 里已经修好的批量 `GroupBy`+`Aggregate(ent.Count())` 查询（把这部分代码
   从 `TicketAssignmentService` 迁移过来，不是复制一份——避免两份几乎一样的批量查询代码分别维护），一次查出
   全部候选人的 `ActiveTickets` 计数，选最小的一个。

## 设计二：`NotifyTicketCreated` 重写

```go
func (s *TicketNotificationService) NotifyTicketCreated(ctx context.Context, ticket *ent.Ticket) error {
    var userIDs []int
    if ticket.AssigneeID > 0 {
        userIDs = append(userIDs, ticket.AssigneeID)
    }
    if ticket.RequesterID > 0 && ticket.RequesterID != ticket.AssigneeID {
        userIDs = append(userIDs, ticket.RequesterID)
    }
    if len(userIDs) == 0 {
        return nil
    }
    // 不再有"只有申请人就广播全体/admin"的分支——没有 assignee 现在是正常状态
    // （审批还没走完，或走完了但服务台-L1 暂时没人），不是需要额外通知谁的异常。
    ...
}
```

`NotifyTicketAssigned`（工单真正被分配时发通知，`ticket_notification_service.go:264` 起）不受影响，继续
在 `TeamWorkloadResolver` 成功解析出人、`createUserTask` 写入 `assignee` 之后触发——这部分现有机制已经是对的，
只是触发时机现在会推迟到 `Activity_Execute`/`Activity_Handle`，而不是工单创建那一刻。

## 测试计划

**后端**：

- `service/approver/team_workload_resolver_test.go`（新建）：�covers 正常解析出最闲成员、候选池为空返回错误
  两种情况，复用 `TestTicketAssignmentService_AutoAssign_QueryCountIsBounded` 同款的查询次数断言（证明批量
  查询迁移过来后没有退化回 O(N)）。
- `service/bpmn_process_engine_test.go`：补 `taskPurpose="fulfillment"` 节点创建时正确调用
  `TeamWorkloadResolver`、候选池为空时正确留空且不落候选组兜底的用例。
- `service/ticket_notification_service_test.go`：补"无 assignee 只通知申请人，不广播"的用例（如果现有测试
  文件里有覆盖旧广播行为的用例，需要同步删除/更新，避免新旧行为断言打架）。
- `go test ./...` 全绿。

**手工/端到端**：

- 往服务台-L1（`teams.id=1`）加 1-2 个真实测试账号（`UPDATE users SET team_users=1 WHERE id IN (...)`），
  用真实 UI 重跑一遍 Copilot 采购申请全链路，验证 IT 总监审批通过后新增的 `Activity_Execute` 节点正确创建、
  正确分配给服务台-L1 里工作负载最低的那个人，且通知只发给了 assignee + 申请人两个人（不是全租户）。
- 再测一次"不需要审批"的场景（比如吊销 VPN 权限对应的目录项，如果现在没有现成的，需要先建一个
  `requires_approval=false`/走 `ticket_general_flow` 无审批分支的测试目录项），验证提交后不经过任何审批
  节点、直接被服务台-L1 自动分配。
- 验证 `ticket_general_flow.bpmn` 删除 `Activity_Assign` 后，走这条流程的既有测试用例（如果有引用这个节点
  ID 的测试断言）不受影响——实施阶段需要先 grep 一遍确认没有别的地方硬编码依赖这个节点 ID。

## 风险与未决问题

- **服务台-L1 现在没有真实成员**：试点验证阶段候选池必然为空，`TeamWorkloadResolver` 会一直走"记警告日志、
  不分配"这条路径，无法端到端验证"挑最闲的人"这部分逻辑是否选对了人——需要先手动往 `teams.team_users` 里
  加测试账号才能验证完整。
- **`Activity_Assign` 删除的影响面未完全确认**：实施阶段需要先 `grep -rn "Activity_Assign"` 全代码库
  （包括测试、前端 BPMN 设计器里是否有硬编码引用这个节点 ID 的地方），确认删除这个节点不会破坏别的东西，
  再动手改 `.bpmn` 文件。
- **`ticket_task`/`incident_task` 这两个 `service_task_type` 的现状**：`Activity_Handle` 现在同时带着
  `service_task_type=ticket_task`/`action=update_status` 的 metaData（原本是给"完成这个 UserTask 时调用
  哪个回调处理器"用的），加了 `taskPurpose="fulfillment"` 之后，"节点创建时的自动分配"（设计一）和"节点完成
  时的回调处理"（现有 `TicketServiceTaskHandler.updateTicketStatus`）是两个不同阶段、不冲突，但实施时需要
  确认这两套 metaData 不会互相踩踏。
- **`Activity_Escalate`/incident 系列的 `Activity_L1Fix`/`Activity_L2Diagnosis` 等节点是否也要接入
  `TeamWorkloadResolver`**：本次范围明确只覆盖"从无到有"的两个入口（`Activity_Execute`/`Activity_Handle`），
  流程内部后续的升级转发节点沿用现状（人工/`Activity_AutoEscalate`），如果后续发现这些节点也有同样的"广撒网
  分配"问题，需要另开一轮讨论，不在本次 spec 里顺带解决。
