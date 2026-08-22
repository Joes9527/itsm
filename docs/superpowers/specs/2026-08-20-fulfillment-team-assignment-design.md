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
   "谁来干活"）。同时新增 `fulfillmentTeamCode` 属性，指定按 `teams.code`（不是 `teams.name`——理由见下）
   路由到哪个团队；节点不声明这个属性时，fallback 到一个 Go 常量默认值（跟 `approvalFallbackCandidateGroup
   = "ticket-approvers"`，`bpmn_process_engine.go:773`，是同一种既有的"节点未声明就用常量兜底"写法，不是
   新发明的机制）——**不能把团队名称硬编码进 Go 代码里当匹配条件**：`teams` 表本来就有独立的 `code` 字段
   （唯一约束），`assigneeRole` 也是匹配 `role.code` 而不是 `role.name`（`bpmn_process_engine.go:1104`），
   用可能被改名/翻译的 `name` 去匹配，未来团队重命名或多语言租户下会直接匹配失败；用声明式属性而不是写死在
   resolver 里，也是为了给以后"不同服务目录项路由到不同履约团队"（本次范围明确不做，见下）留出口子，不用
   改代码，只需要在对应节点上填不同的 `fulfillmentTeamCode`。
2. 新增 `TeamWorkloadResolver`（`service/approver/team_workload_resolver.go`），跟
   `DeptManagerResolver`/`PersonalManagerResolver`/`TeamLeaderResolver` 同一个家族。算法：候选人 =
   `users.team_users` 指向 `fulfillmentTeamCode` 对应团队（不做分类→team 映射，所有需要 L1/L2/L3 执行的
   工单统一先进服务台-L1，L1 内部再人工升级转发给专精团队——复用现有 `Activity_AutoEscalate` 模式，不新建
   升级机制）的 active 用户；工作负载查询复用本次会话已经修好的批量聚合写法（`service/ticket_assignment_service.go`
   里新增的 `batchGetUserWorkloads`/`assigneeAggRow` 那套 `GroupBy`+`Aggregate(ent.Count())`，只是把候选人
   来源从"全体 active 用户"换成"目标团队成员"），挑 `ActiveTickets` 最少的一个直接写 `ProcessTask.assignee`
   ——不做"候选组展开、人工领取"（`candidateGroups`）那一套，因为已经决定自动挑最闲的人，不需要人工认领。
   候选池为空（当前 100% 会命中，因为服务台-L1 还没有真实成员）时，只记 `Warnw` 日志，**不落到
   `approvalFallbackCandidateGroup`（"ticket-approvers"）那个候选组兜底**，也不触发任何通知——跟设计二
   的"无 assignee 不广播"策略是同一件事的两面。
3. **解析出 assignee 之后，必须同步做两件现有 BPMN 引擎完全不做的事**（这是审阅时发现的关键缺口，原稿漏了，
   补充说明见"设计一"）：把结果写回 `ent.Ticket.assignee_id`/`status`，并触发一次"你被分配了"的通知。
4. 删除 `TicketService.CreateTicket`（`service/ticket_service.go:192-199`）里工单一创建就同步分配的
   逻辑；`TicketAssignmentService.autoAssignTicket`/`getAvailableUsers` 以及它们专用的私有方法
   （`calculateUserScore`/`calculateSkillScore`/`calculateWorkloadScore`/
   `calculateCategoryExperienceScore`/`calculatePerformanceScore`/`checkUserSkills`/
   `checkUserCategoryAccess`/`getMaxActiveTickets`/`batchGetUserWorkloads` 等，含本次会话为修性能新增的
   批量查询方法——那批优化后的查询逻辑不浪费，直接迁到 `TeamWorkloadResolver` 里复用，只是候选人来源换了）
   整体删除。`GetUserWorkload`/`GetTeamWorkload`/`AssignTicket`（手动指定 `PreferredUser` 那条路径）/
   `ReassignTicket`/`GetTicketsByAssignee`/`AssignTickets` 这些只读统计或人工操作接口保留——
   `controller/ticket_assignment_controller.go` 还在用，跟"自动分配该怎么选人"这件事无关。
5. BPMN 文件改造：
   - `service_request_flow.bpmn`、`service_request_urgent_flow.bpmn`：`Activity_Execute` 加
     `taskPurpose="fulfillment"`，紧跟着新增一个 `serviceTask`（`service_task_type=ticket_task`，
     `action=notify_handler`，复用已经实现好的 `TicketServiceTaskHandler.notifyHandler`，
     `service/bpmn/ticket_handler.go:212`——它读 `ticketEntity.AssigneeID` 发通知，不用写新 Go 代码，
     只要保证它在 `Activity_Execute` 把 assignee 写回 `ent.Ticket` 之后执行）。
   - `ticket_general_flow.bpmn`：**删除 `Activity_Assign` 节点**（`Flow_1` 直连 `Gateway_Approval`，
     不再经过它），`Activity_Handle` 加 `taskPurpose="fulfillment"`，同样在它之后加一个
     `action=notify_handler` 的 `serviceTask`——分配职责完全交给审批之后的 `Activity_Handle`，避免
     "审批前分配一次、审批后 `Activity_Handle` 又要处理一次"的语义重复。
   - `copilot_procurement_flow`：IT 总监审批通过后新增一个 `taskPurpose="fulfillment"` 的
     `Activity_Execute`（如"开通 Copilot 许可证账号"）节点 + 同款 `notify_handler` 通知节点，再到
     `EndEvent_1`。
6. 前端 BPMN 设计器暴露 `taskPurpose="fulfillment"`/`fulfillmentTeamCode` 这两个新属性——比照
   `assigneeGmChain` 当初的接入方式（`itsm-moddle-descriptor.ts` 声明 + `WorkflowNodeInspector.tsx`
   加开关/输入框，`components/workflow/designer/WorkflowNodeInspector.tsx`），否则管理员没法通过设计器
   配置这两个新属性，只能手改 `.bpmn` XML。
7. `NotifyTicketCreated`（`service/ticket_notification_service.go`）按新的时机模型重写：
   - 有 `assignee`（意味着流程已经走到 `Activity_Execute`/`Activity_Handle` 且 `TeamWorkloadResolver`
     成功解析出人）→ 通知 assignee + 申请人，**不变**。
   - 没有 `assignee`，是因为工单刚创建、还没走到执行分配这一步（现在是**正常状态**，不是异常）→ 只通知
     申请人，**不再有"广播全体/admin"这条路**。
   - `TeamWorkloadResolver` 到了 `Activity_Execute` 却解析不到人（服务台-L1 是空的）→ 沿用设计一定的策略，
     只记警告日志，不发任何通知——这是真正的异常信号，靠后台监控/日志发现，不该用邮件轰炸的方式暴露。
   - 注意：`NotifyTicketCreated` 这次重写跟"被分配时通知处理人"是两件独立的事——后者现在完全由第 5 点新增
     的 `notify_handler` 服务任务节点负责，不是 `NotifyTicketCreated` 的职责范围。

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
// defaultFulfillmentTeamCode 是 taskPurpose="fulfillment" 节点没有声明 fulfillmentTeamCode 时的
// 兜底团队，跟 approvalFallbackCandidateGroup（第 773 行）是同一种"节点未声明就用常量兜底"写法。
const defaultFulfillmentTeamCode = "服务台-l1" // 需在实施阶段跟 pkg/seeder/seeder.go 实际写入的 teams.code 值核对一致

} else if task.TaskPurpose == "fulfillment" {
    teamCode := task.FulfillmentTeamCode
    if teamCode == "" {
        teamCode = defaultFulfillmentTeamCode
    }
    assignee = e.resolveFulfillmentAssignee(ctx, instance, teamCode)
    if assignee == "" {
        e.logger.Warnw("执行任务未在目标团队解析到可用处理人，工单暂不分配",
            "taskID", task.ID, "instanceID", instance.ID, "teamCode", teamCode)
        // 不落候选组兜底，不触发任何通知——留空 assignee，等团队有真实成员后
        // 由人工在"我的待办"/工单列表里认领，或后续补一个定时扫描重试（不在本次范围）。
    } else {
        // 关键：BPMN 引擎从来不会自动回写 ent.Ticket——审批任务只决定"谁批"，本来就不该碰
        // 工单行；但 fulfillment 任务的 assignee 就是工单的处理人，这里必须同步写回
        // ticket.assignee_id/status，否则工单列表/详情/外部 API 会永远显示"未分配"，即使
        // BPMN 任务表里已经有了真实的 assignee。是否被分配的通知交给 BPMN 文件里紧跟着的
        // notify_handler 服务任务节点（读这里刚写好的 assignee_id），引擎本身不直接调用
        // 通知服务——沿用"通知走声明式 ServiceTask 节点"的既有模式（Activity_NotifyRequester/
        // Activity_RejectNotify 都是这样做的），不用给 CustomProcessEngine 加新的服务依赖。
        // business_id 是 reservedInstanceVariableKeys 里的既有约定（bpmn_process_engine.go:2104），
        // ticket_task 的 ServiceTask.Execute 已经在用同一个变量取 ticket id（ticket_handler.go:94-100），
        // 这里复用 createUserTask 顶部已有的 getUserID 辅助函数，不新开取值路径。
        assigneeID, _ := strconv.Atoi(assignee)
        businessTicketID, _ := strconv.Atoi(getUserID("business_id"))
        if err := e.client.Ticket.UpdateOneID(businessTicketID).
            Where(ticket.TenantIDEQ(instance.TenantID)).
            SetAssigneeID(assigneeID).
            SetStatus(common.TicketStatusAssigned). // 跟 ticket_handler.go:328 assignTicket 用的同一个常量
            Exec(ctx); err != nil {
            e.logger.Warnw("fulfillment 任务已解析出 assignee，但回写 ticket 失败",
                "taskID", task.ID, "assignee", assignee, "error", err)
        }
    }
}
```

`resolveFulfillmentAssignee` 调用 `approver.NewTeamWorkloadResolver().Resolve(ctx, e.client, &approver.ApproverContext{TenantID: instance.TenantID, TeamCode: teamCode})`——
不需要 `RequesterID`/`DepartmentID`（跟审批解析器不同，执行分配不看"申请人是谁"，只看目标团队现在谁最闲）。
`TeamWorkloadResolver` 内部：

1. 先按 `teams.code = <appCtx.TeamCode> AND tenant_id = ?` 查出这个租户下目标团队的 `id`（用 `code` 不用
   `name` 匹配，理由见"范围"一节；不能跨租户硬编码 `team.id`，`teams` 是租户隔离的），再查
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

**订正（审阅时发现原稿这里判断错了）**：`NotifyTicketAssigned`（`ticket_notification_service.go:264`）
**不会**自动在分配发生时触发——它只是一个独立函数，现在只被人工重新分配路径
（`TicketService.AssignTicket`/`TicketAssignmentService.assignToSpecificUser`）显式调用。旧代码里
"自动分配的人会收到通知"这个体验，纯粹是因为分配和创建在同一个同步调用链里，工单创建通知
（`ticket_service.go:235`）触发时 assignee 已经写好了，两者顺带合并在一起，从来没有专门的"你被分配了"
通知路径。搬到 `Activity_Execute`/`Activity_Handle` 之后，创建通知和分配之间隔了不定长的审批等待时间，
这个"顺便带上"的机制会彻底失效。这也是为什么设计一里新增了 `notify_handler` 服务任务节点——处理人的
"你被分配了"通知，现在完全由这个新节点负责，不再依赖 `NotifyTicketCreated`/`NotifyTicketAssigned` 里
的任何路径。

## 测试计划

**后端**：

- `service/approver/team_workload_resolver_test.go`（新建）：覆盖按 `teams.code` 正常解析出最闲成员、
  `fulfillmentTeamCode` 指向不存在的团队、候选池为空返回错误三种情况，复用
  `TestTicketAssignmentService_AutoAssign_QueryCountIsBounded` 同款的查询次数断言（证明批量查询迁移过来
  后没有退化回 O(N)）。
- `service/bpmn_process_engine_test.go`：补 `taskPurpose="fulfillment"` 节点创建时正确调用
  `TeamWorkloadResolver`、成功解析出 assignee 时正确回写 `ent.Ticket.assignee_id`/`status`、候选池为空时
  正确留空且不落候选组兜底、也不误写 ticket 的用例。
- `service/bpmn/ticket_handler_test.go`：补验证 `action=notify_handler` 的节点在 `Activity_Execute`/
  `Activity_Handle` 写好 `assignee_id` 之后执行时，能正确读到刚写入的 assignee 并发通知（顺序依赖，不是
  独立单测能覆盖的，需要一个跑完整 process instance 的集成用例）。
- `service/ticket_notification_service_test.go`：补"无 assignee 只通知申请人，不广播"的用例（如果现有测试
  文件里有覆盖旧广播行为的用例，需要同步删除/更新，避免新旧行为断言打架）。
- `go test ./...` 全绿。

**手工/端到端**：

- 往服务台-L1（`teams.code` 实际值以实施时 `pkg/seeder/seeder.go` 或现有 DB 行为准，不是拍脑袋写的
  字符串）加 1-2 个真实测试账号，用真实 UI 重跑一遍 Copilot 采购申请全链路，验证 IT 总监审批通过后新增的
  `Activity_Execute` 节点正确创建、正确分配给服务台-L1 里工作负载最低的那个人、`ent.Ticket` 详情页正确显示
  这个 assignee（不再是"未分配"）、且被分配的人真的收到了一条通知（不是靠"顺便带上"）。
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
- **`defaultFulfillmentTeamCode` 常量的实际值需要跟数据库核对**：当前 tenant 1 里服务台-L1 的
  `teams.code` 实际存的是 `"服务台-l1"`（名称的机械小写化，不是一个干净的英文 code），实施阶段要么直接用
  这个值当默认常量，要么借机把 `pkg/seeder/seeder.go` 里团队的 `code` 改成更规范的值（如
  `"servicedesk-l1"`）——两种做法都行，但要保证常量硬编码的值跟 seeder 实际写入 DB 的值一致，否则默认兜底
  会一直匹配不到团队，等价于候选池永远为空。
- **`notify_handler` 服务任务节点依赖执行顺序**：它读 `ent.Ticket.assignee_id` 发通知，必须确保 BPMN
  引擎对同一个 `ProcessInstance` 里前后两个节点是严格顺序执行（先完成 fulfillment 节点的回写、再触发下一个
  节点），不能是并发/异步执行——实施阶段需要确认现有引擎的节点推进机制满足这个顺序保证，本次没有专门验证。
