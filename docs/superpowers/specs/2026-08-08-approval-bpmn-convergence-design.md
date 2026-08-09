# 审批收敛到 BPMN — 设计文档

## 背景与问题

这次会话早前修复了 `ticket_general_flow.bpmn` 审批节点的自己批自己漏洞（见 `docs/superpowers/specs/2026-08-08-ticket-approval-self-approval-fix-design.md`），最终评审时把"审批收敛"列为下一轮的独立 backlog。调查发现审批相关的机制不是"新旧两套"，而是**五套并存**：

1. `ApprovalWorkflow` + `ApprovalRecord`（JSON 节点配置，`/admin/approvals`、`/workflow/ticket-approval` 管理界面，`approval_service.go` 驱动）
2. `TicketApproval`（独立表，`ApprovalWorkflowPanel.tsx`"提交审批"按钮走这条，已经桥接到 BPMN——`bpmn_approval_bridge_service.go`）
3. `ApprovalChain`（通用 entity_type 链，`/admin/approval-chains`，`approval_chain_controller.go`）
4. Change/CAB 审批（完全独立的原生 SQL 表 `change_approvals`，不经过 Ent）
5. `process_approval_decision`（BPMN 原生审计表）

这次设计只处理第 1 套（`ApprovalWorkflow`/`ApprovalRecord`/`approval_service.go`），第 3、4 套明确排除在外（见"非目标"）。第 2、5 套已经算"在 BPMN 这边"，不需要迁移，但会被第 1 套的退休动作间接影响（见"现状核实"里的双重触发发现）。

## 现状核实（代码走查 + 本地 dev 库查证，2026-08-08）

### `ApprovalWorkflow` 是产品默认种子数据，不是没人用的遗留代码——但"每个新租户自动拿到"这个说法不准确，已订正

`config/seed/default.json` 的 `ApprovalWorkflows`/`ProcessBindings` 数组通过 `pkg/seeder/seeder.go` 的 `loadSeedConfig → seedApprovalWorkflows/seedProcessBindings` 在**进程启动时**（`internal/bootstrap/app.go` 的初始化引擎，受 `AutoSeed` 配置门控）写入 `tenant.code == "default"` 这个模板租户。本地 dev 库查证（`docker exec itsm-postgres-dev`）确认 `approval_workflows` 表的 4 条记录内容跟这个 JSON 文件逐字节一致，证实这确实是活的种子源，不是死配置。

**订正（2026-08-08 复审后核实）**：原文说这 4 条记录会"克隆给每一个新建租户"，这不准确。真正的租户创建 API（`service/tenant_service.go` 的 `CreateTenant`）完全不做任何 Approval/ProcessBinding 相关的种子克隆——通过这个 API 建的新租户默认没有这 4 个工作流，直接落到 `ticket_general_flow` 兜底。克隆动作只发生在 `pkg/seeder/tenant_provisioner.go` 的 `ProvisionTenant`，而这是一个独立的运维 CLI 工具（`cmd/provision_tenant/main.go`），不是自动挂在租户创建流程上的。也就是说这 4 条记录的实际影响面是：①"default" 模板租户自己（每次启动都会重新核对种子）；②任何被运维手工跑过 `provision_tenant` 工具的租户。不是"绝对每一个新租户"。这个订正不影响后面"要不要迁移这 4 个模板"的结论（default 模板租户本身也需要这次修复，运维流程未来还会继续用它作为克隆源），但纠正了受影响范围的描述。

### 种子默认模板一旦触发就会报错——不是行为不对，是完全跑不起来

种子节点 JSON 形状是 `{"name": "...", "role": "dept_manager", "type": "approval", "timeout": 480, "approver_type": "role"}`，但强类型解析用的 `dto.ApprovalNodeConfig`（`dto/approval_types.go:68-83`）期望的 JSON key 是驼峰的 `assigneeType`/`assigneeValue`/`approverType`/`timeoutHours`。种子数据用的 `role`/`type`/`approver_type`/`timeout` 一个都对不上，`mapsToNodes`（`service/approval_type_converters.go:33`，序列化再按 struct tag 反序列化）会让 `AssigneeType`/`ApproverType` 全部解析成空字符串。`resolveApprover`（`approval_service.go`）在 `assigneeType == ""` 时落到 `default` 分支，直接返回 `不支持的审批人类型: `（空字符串）。这是字段命名约定升级成驼峰之后没有回填种子数据留下的漂移，不是这次会话改动引入的存量 bug。

**决策依据**：既然默认模板从来没有真正跑通过，没有"必须保持原样兼容"的负担，这次直接用 BPMN 重新 authoring 这 4 个模板，不修旧字段名、不迁移默认模板本身的数据（见"目标架构·组件②"）。

### `TicketService.CreateTicket` 对所有工单类型都双重触发审批，不止服务请求

`service/ticket_service.go:212-224`（同步）无条件调用 `s.approvalSvc.TriggerApproval(...)`，`:257-263`（异步 goroutine）无条件调用 `s.processTriggerSvc`/BPMN 触发——对**所有**工单类型，没有类型过滤。两处代码注释分别写着"V1 缺失的 Phase 1 #1 缺陷修复：V2 必须让工单进入审批链路"和"V1 缺失的 Phase 1 #1 缺陷修复：V2 必须让工单进入 BPMN 引擎"——像是两轮独立的补缺陷改动，互相不知道对方已经把"工单需要某种审批/工作流机制"这个缺口堵上了，结果两套同时留下来跑。**这是本次设计里唯一真正的"退休调用点"**，其余大部分工作是搭桥（组件①③）和补文件（组件②的旁支）。

### Change 已经 100% 在 BPMN 上，从来没有走过旧系统

`change_service.go` 里 `approvalService` 字段只有 setter（`SetApprovalService`）被调用，`TriggerApproval` 从未被真正调用过。Change 创建（`change_service.go:842-851`）硬编码直接用 `change_normal_flow`/`change_emergency_flow` 作为 `ProcessDefinitionKey`，不经过 `ProcessBinding` 查询。这意味着旧系统里"普通变更审批"/"紧急变更审批"这两个默认模板从来没有被真正触发过，是纯冗余。

### `change_emergency_flow.bpmn` 文件缺失——跟这次会话修的 `ticket_urgent_flow.bpmn` 是同一类问题

`change_service.go:844` 硬编码 `processKey = "change_emergency_flow"`（当 `ch.Type == "emergency"` 时），但 `service/bpmn/` 目录下没有这个文件（只有 `change_normal_flow.bpmn`/`change_normal_flow_cn.bpmn`）。`bpmn_template_service.go:116` 的 `case "change_emergency_flow":` 因此是死代码，从未被执行到（`listTemplates()` 靠 `fs.WalkDir` 遍历实际存在的文件驱动，不存在的文件不会进入这个 switch）。创建紧急变更时 `TriggerProcess` 会报"流程定义 change_emergency_flow 不存在"，错误在异步 goroutine 里被 `Warnw` 吞掉（`change_service.go:133-137`），不会让创建请求本身失败，但紧急变更永远进不了审批流程。

### `service_request_flow.bpmn` 存在但够不着——`ProcessBinding` 种子数据的 `business_type` 跟 resolver 实际查询方式不匹配

`config/seed/default.json:110-114` 里的 `ProcessBinding` 种子数据用顶层 `business_type="service_request"`/`business_type="change"`。但 `ProcessResolver`（`process_resolver.go:34-42`）实际查询是 `FindBestBinding(BusinessType="ticket", subType="service_request"/"change")`——`bpmn_process_binding_service.go:227-237` 按精确 `BusinessType` 过滤，`"service_request"` 类型的种子行永远匹配不上 `"ticket"` 类型的查询，resolver 因此总是落到兜底的 `ticket_general_flow`。`service_request_flow.bpmn` 文件本身没问题，只是种子绑定数据写错了 `business_type` 的值。**这条诊断只对 service_request 成立——`business_type="change"` 那行虽然是同样的写法问题，但对 Change 当前行为没有任何影响，因为上一节已经确认 Change 创建根本不查 `FindBestBinding`（硬编码 `ProcessDefinitionKey`），这行种子数据现在对不对都不影响 Change 的真实路由，见组件②2c 的处理方式。**

### `FindBestBinding` 不读 `Conditions`，工单这条路径也走不到真正会求值 `Conditions` 的地方

`ProcessBindingService.FindBestBinding`（`bpmn_process_binding_service.go:227-267`）只按 `BusinessType`+`BusinessSubType`+`TenantID`+`IsActive` 过滤，排序用 `priority`/`is_default` 两个普通整数字段，完全不读取 `Conditions`（JSON 字段）。真正会对 `Conditions` 求值的逻辑在另一个服务里——`ProcessRoutingService.FindBestRoute`/`calculateMatchScore`/`evaluateConditions`（`service/process_routing_service.go:54-196`）。

工单创建这条路径（`triggerWorkflowForTicket`，`ticket_service.go:554-560`）用 `ProcessResolver.ResolveWithPriority` 解析出一个 `processKey`——`Resolve`（`process_resolver.go:26-48`）最差情况也会兜底成字面量 `"ticket_general_flow"`，**从不返回空字符串**。这个非空 key 被塞进 `ProcessTriggerRequest.ProcessDefinitionKey` 传给 `TriggerProcess`（`bpmn_process_trigger_service.go:49-67`），而 `TriggerProcess` 只在 `req.ProcessDefinitionKey == ""` 时才会调用 `processRoutingSvc.FindBestRoute`（真正读 `Conditions` 的那个）——对工单来说这个分支永远不会触发。也就是说，**给 `ProcessBinding` 配 `Conditions` 对工单创建这条路径完全不起作用**，`ResolveWithPriority` 里硬编码的 `if processKey == "ticket_general_flow" { 按优先级换成 ticket_urgent_flow }` 特判，才是当前唯一真正在跑的"按优先级路由"机制。

### `handlers/service_request/service.go` 没有硬编码审批链（订正一条过期的会话内 memory）

早期（2026-08-07 之前）的项目 memory 记录过这里有一套硬编码的 manager→IT→security 三步审批链，直接读取代码（`handlers/service_request/service.go:60-170`）确认**这套链路已经不存在**——`Create` 方法只是创建关联 Ticket（`Type: "service_request"`, `Source: "service_catalog"`），审批/状态/工作流全部委托给 Ticket 自己的路径，正是这次会话早前 ServiceRequest→Ticket 委托重构（PR #2）做的事。这条 memory 需要标记为已解决，不再是活的 backlog。

## 目标架构

```
组件① BPMN 引擎扩展：createUserTask 的 approval 分支新增两类声明式解析方式
         │
         ▼
组件② 退休双重触发 + 补两个"绑定/文件缺失"旁支 bug + 4 个默认模板的取舍
         │
         ▼
组件③ 修复 legacy_approval_migration_service.go + 批量迁移存量自定义工作流
         │
         ▼
组件④ 旧系统下线路径
```

### 组件① — BPMN 引擎新增两类候选人/assignee 解析方式

现状（这次会话已经做完的部分）：`createUserTask` 的 approval 分支只有两级——BPMN 声明的 `candidateGroups`/`candidateUsers`（如果有，跳过自动解析）→ 否则解析**申请人自己**所在部门的负责人（`resolveApprovalAssignee`，复用 `service/approver.DeptManagerResolver`）→ 失败则兜底 `ticket-approvers` 候选组。

新增两类：

**1a. 按角色查人**（BPMN 新增自定义属性 `assigneeRole="ops_manager"`，非标准 BPMN 属性，跟已有的 `taskPurpose`/`approvalMode` 一样是这个引擎自己的扩展）

```
优先级链变成：
  1. BPMN 声明了 candidateGroups 或 candidateUsers → 维持现状
  2. 否则，BPMN 声明了 assigneeRole → 查这个租户里所有 active 且 role = assigneeRole 的用户，
     作为候选人（排除申请人自己），assignee 留空——查询语义复用 approval_service.go
     resolveApprover "role" 分支的 user.RoleEQ + TenantID + Active，但结果处理方式不同：
     旧实现是 .First() 只挑一个人直接当 assignee；这里改成全部作为候选人（同角色多人都能领）。
     旧实现从来没跑通过（见"现状核实"），没有"必须保持原样"的兼容负担，改成候选人列表
     更符合这次会话"审批任务优先给多个候选人、避免单点指派"的思路——同一个角色如果有
     多个人，谁先领谁审批，不会因为引擎武断选中的那个人离职/请假就卡死。
  3. 否则，BPMN 声明了下面 1b 的固定范围属性之一 → 走 1b
  4. 否则（以上都没声明）→ 维持现状：解析申请人自己所在部门负责人，失败则候选组兜底
```

**1b. 固定范围组织路由**（BPMN 新增 4 个自定义属性：`assigneeDeptId`/`assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId`，值是具体的部门/团队/项目/临时团队 ID）

对应旧系统 `dept_manager`/`team_leader`/`project_manager`/`temp_team_leader` 四种类型——这四种在旧系统里 `assignee_value` 存的是一个**固定的**范围 ID（不是"申请人自己所在的"，是工作流作者配置时钉死的），跟组件①现有的"解析申请人自己部门负责人"（动态、随申请人变化）是不同语义，不能合并成一条路径。

```
BPMN 声明了这四个属性中的任意一个：
  - assigneeDeptId="42"      → approver.NewDeptManagerResolver()，DepartmentID=42（固定值，不取申请人的）
  - assigneeTeamId="7"       → approver.NewTeamLeaderResolver()，TeamID=7
  - assigneeProjectId="3"    → approver.NewProjectMgrResolver()，ProjectID=3
  - assigneeTempTeamId="9"   → approver.NewTempTeamResolver()，TeamID=9（临时团队复用同一个 resolver）

四个 resolver 全部是已有、已测试的代码（service/approver/*.go），这里只是新增 BPMN 声明式
入口，不重新实现解析逻辑。解析出来的人如果正好是申请人自己，同样要排除、转
candidateGroups 兜底（复用 excludeUserFromCandidates 同一套保护，不新写一套）。
```

**明确不支持（跳过 + 警告，不是这轮的目标）**：`amount_based`（按金额阈值选审批人）——现有 BPMN 流程实例变量里没有金额这个概念，需要额外从工单/变更实体接一个金额字段进流程变量才能支持，跟前面四种固定范围类型不是同一个难度级别。批量迁移工具遇到 `amount_based` 类型的节点时跳过整个工作流的迁移，输出明确警告，留给管理员人工处理。

**改动文件**：`service/bpmn_types.go`（`BPMNUserTask` 加 `AssigneeRole`/`AssigneeDeptId`/`AssigneeTeamId`/`AssigneeProjectId`/`AssigneeTempTeamId` 五个字段）、`service/bpmn_process_engine.go`（`createUserTask` 扩展优先级链 + 新增对应的解析辅助函数）。

### 组件② — 退休双重触发 + 补两个旁支 bug + 4 个默认模板的取舍

**2a. 退休 `CreateTicket` 里的双重触发**（本次设计里唯一真正的"退休调用点"，是整个"审批收敛"最核心的一步）

删除 `service/ticket_service.go:212-224` 那段同步调用 `s.approvalSvc.TriggerApproval(...)` 的代码块，只保留已经在跑的 `processTriggerSvc`/BPMN 异步触发（:257-263）不变。`TicketService` 对 `ApprovalService` 的依赖（字段、setter）留到组件④删除旧代码时一并清理，这一步先只删调用点。

**2b. 补 `change_emergency_flow.bpmn` 缺文件**

新建 `service/bpmn/change_emergency_flow.bpmn`。跟这次会话修 `ticket_urgent_flow.bpmn` 时不一样——不做零差异副本。"紧急变更"在 Change 管理场景下有真实的业务含义（CLAUDE.md 要求变更管理保留风险/CAB/发布窗口概念），但这次设计的范围不包括重新设计紧急变更的审批链路本身，先做跟 `change_normal_flow.bpmn` 结构等价的副本（只改 `process id`/`name`/`description`），把"文件缺失导致创建失败"这个硬 bug 堵上，行为差异（更短审批链/更高级别直批）作为明确记录的后续事项，不在这轮做（跟 `ticket_urgent_flow` 保持一致的节奏，避免两个"紧急流程"用不一致的标准）。

**2c. 修 `service_request_flow.bpmn` 绑不上的问题（顺手修，对 Change 当前行为无影响）**

修种子数据，不改 resolver 逻辑：把 `config/seed/default.json` 里 `business_type="service_request"`/`business_type="change"` 的 `ProcessBinding` 种子行改成 `business_type="ticket"` + 对应 `subType`，照着 resolver 实际查询方式（`FindBestBinding(BusinessType="ticket", subType=...)`）写，不动 `bpmn_process_binding_service.go`/`process_resolver.go` 的匹配逻辑本身（改逻辑的影响面覆盖所有已经在正常工作的绑定，风险比改种子数据大得多）。**这一步对 `business_type="service_request"` 那行是真正的修复（解开 `service_request_flow.bpmn` 够不着的问题）；对 `business_type="change"` 那行只是顺手把种子数据改一致，Change 今天的路由不受它影响（见现状核实"`FindBestBinding` 不读 Conditions"一节）——Change 真正的路由 bug 是 2b 的缺文件问题，不是这里。**

**2d. 4 个默认模板的取舍**

- "普通变更审批"/"紧急变更审批"（`ApprovalWorkflow` 种子行）——直接删，不迁移。Change 已经在 BPMN 上跑（`change_normal_flow.bpmn`/修复后的 `change_emergency_flow.bpmn`），这两个模板从来没被触发过，纯冗余。
- "服务请求审批"/"权限申请审批"——这两个映射到 Ticket type=service_request 场景。核实过 `service_request_flow.bpmn` 现有结构（只有一个通用的 `Activity_Approval`/"请求审批"节点，没有区分优先级的分支；`Activity_AccessApproval`/`Activity_HardwareApproval` 是在 `_cn` 变体文件里，跟这次要处理的默认场景无关，不要混用）：复用 `service_request_flow.bpmn`，给它的 `Activity_Approval` 打上 `taskPurpose="approval"` 标记，通过 2c 修好的 `ProcessBinding` 覆盖"服务请求审批"这个默认场景；"权限申请审批"（`priority="high"` 的服务请求）新建 `service_request_urgent_flow.bpmn`——跟这次会话 `ticket_general_flow.bpmn`→`ticket_urgent_flow.bpmn` 同样的模式（内容等价副本，仅 `process id`/`name`/`description` 不同）。**路由方式不是配 `ProcessBinding.Conditions`（上面已经确认这条路径工单走不到）——照着 `ResolveWithPriority`（`process_resolver.go:50-63`）里 `ticket_general_flow`→`ticket_urgent_flow` 那个硬编码特判的写法，显式加一条 `service_request_flow`→`service_request_urgent_flow`，同样按 `ticket.Priority == "high" || "urgent"` 判断。**`tenant_provisioner.go` 改成部署+绑定这两个模板，不再克隆 `ApprovalWorkflow` 行。

### 组件③ — 修复 `legacy_approval_migration_service.go` + 批量迁移存量自定义工作流

**现状 bug**：`buildLegacyApprovalBPMN`（`service/legacy_approval_migration_service.go`）对 `assigneeType` 不是 `role`/`group` 的情况，一律把 `assignee_value` 原样写进 BPMN `assignee` 属性。对 `user` 类型这是对的（`assignee_value` 就是用户 ID）。但对 `dept_manager`/`team_leader`/`project_manager`/`temp_team_leader` 类型，`assignee_value` 是一个**组织范围 ID**（部门/团队/项目 ID），不是用户 ID——直接写进 `assignee` 属性会让 `authorizeTaskActor` 把这个数字当成用户 ID 比对，几乎必然匹配到错误的人或者谁都匹配不上。另外，对 `role` 类型，现有代码把它跟 `group` 类型混在一起都写成 `candidateGroups`——但 `Role`（`User.role` 枚举字段）和 `Group`（`/admin/groups` 的成员制实体）是两套不同的东西，只有真的存在一个同名 `Group` 且成员正好跟角色同步时才会碰巧工作，这是第二个独立的映射错误。

**修复**：按 `assigneeType` 生成正确的声明式属性（依赖组件①新加的属性）：

```
assigneeType == "user"           → assignee="<value>"（不变，本来就对）
assigneeType == "group"          → candidateGroups="<value>"（不变，本来就对）
assigneeType == "role"           → assigneeRole="<value>"（新增，修正跟 group 混用的问题）
assigneeType == "dept_manager"   → assigneeDeptId="<value>"（新增，修正塞错 assignee 的问题）
assigneeType == "team_leader"    → assigneeTeamId="<value>"（新增）
assigneeType == "project_manager"→ assigneeProjectId="<value>"（新增）
assigneeType == "temp_team_leader"→ assigneeTempTeamId="<value>"（新增）
assigneeType == "amount_based"   → 不生成该节点，整个工作流迁移中止，返回明确错误，
                                    列出具体是哪个节点用了不支持的类型
```

**批量迁移**：新增一次性迁移任务（沿用项目已有的 `-tags migrate` build tag 模式，不新发明触发机制），遍历所有租户的所有 `ApprovalWorkflow` 记录：

- 跳过内容等于 4 个默认种子模板的记录（这些直接由组件②处理，不走迁移，避免重复）
- 对每条真正被租户自定义过的记录，调用修复后的 `buildLegacyApprovalBPMN` 生成 BPMN、部署、创建 `ProcessBinding`（复用现有的 `LegacyApprovalMigrationService.Migrate` 方法，这部分部署+建绑定的逻辑已经是对的，只是节点生成那段需要按上面的映射表改）
- 遇到 `amount_based` 节点的工作流：跳过，记录到迁移报告里，不部分迁移、不静默丢弃
- 迁移成功的工作流：标记原 `ApprovalWorkflow` 记录为已迁移（加一个 `migrated_to_bpmn_at` 时间戳字段，不删除原记录——为组件④的历史数据保留做准备）

### 组件④ — 旧系统下线路径

延续这次会话确认过的整体方向（完全下线，历史数据迁存）：

- `ApprovalRecord` 历史审批数据保留，但迁移完成后变成只读——不再产生新记录（因为组件②2a 已经切断了触发源头）
- `/admin/approvals` 等旧管理界面：迁移完成后改成只读历史查看，或者直接跳转到 BPMN 设计器对应位置——具体哪种交互留到写实施计划时结合前端现状再定，这里先明确"不再允许新建/编辑"这个约束
- `controller/approval_controller.go`、`approval_chain_controller.go`（注意：`ApprovalChain` 不在这次范围内，这里只删跟 `ApprovalWorkflow` 相关的部分，不要连 `ApprovalChain` 的端点一起删）、`service/approval_service.go`、`legacy_approval_migration_service.go`（迁移工具本身，迁移全部完成后也是历史使命完成）——确认所有租户都迁移完、旧路径确实没有流量之后再物理删除，不在批量迁移任务跑完当天就删代码

## 测试计划

- 组件①：`assigneeRole` 命中/未命中/申请人自己就是该角色时排除并转候选组兜底；四个固定范围属性各自命中/租户隔离（跨租户不能解析到别的租户的部门/团队）/解析出的人是申请人自己时排除。
- 组件②2a：创建工单只触发一次 BPMN 流程，不再触发 `ApprovalService`（回归断言：mock/spy `approvalSvc.TriggerApproval` 在 `CreateTicket` 调用后断言零调用）。
- 组件②2b：`change_emergency_flow` 能被发现、部署、`StartProcess` 成功创建实例（不再报"流程定义不存在"）。
- 组件②2c：修完 `business_type` 之后，`ProcessResolver` 对 service_request 类型工单能正确解析到 `service_request_flow`，不再落到 `ticket_general_flow` 兜底；同时断言 `change` 类型工单的路由行为在这次修改前后不变（用来验证"对 Change 无影响"这个判断，不是假设）。
- 组件②2d：**这是这次评审直接指出没有被覆盖、需要补上的场景**——`priority="high"`/`"urgent"` 的 service_request 类型工单，`ResolveWithPriority` 要解析到 `service_request_urgent_flow`，不是 `service_request_flow`；普通优先级的要保持解析到 `service_request_flow`。跟现有的 `ticket_general_flow`/`ticket_urgent_flow` 那组测试用同样的断言方式。
- 组件③：`buildLegacyApprovalBPMN` 对 7 种 `assigneeType`（user/group/role/dept_manager/team_leader/project_manager/temp_team_leader）分别生成正确的 BPMN 属性；`amount_based` 类型触发中止+明确错误；批量迁移任务对种子默认模板正确跳过、对自定义工作流正确迁移并标记、租户隔离（迁移只处理对应租户的数据，不跨租户误迁）。
- 组件④：只读断言（旧 API 的创建/编辑端点在标记下线后返回明确的"已下线，请使用 BPMN"错误，而不是继续悄悄写入数据）。

## 非目标（本次不做）

- `ApprovalChain`（`/admin/approval-chains`）——独立机制，不在这次范围。
- Change/CAB 的原生 SQL 审批（`change_approvals`）——独立机制，不在这次范围，CLAUDE.md 对变更管理的 CAB/风险概念要求高，值得单独讨论。
- `amount_based` 类型的组织路由——跳过+警告，不支持迁移，见组件①③。
- 紧急变更审批的真实业务差异化（更短审批链/更高级别直批）——`change_emergency_flow.bpmn` 这轮先做零差异副本，行为差异是明确记录的后续事项。
- 旧系统代码的物理删除时机——组件④只定"完全下线、历史数据只读"这个目标状态，不在这次设计里定具体删除的时间点/灰度策略。
