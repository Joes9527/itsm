# 审批收敛到 BPMN — 收尾设计（续 2026-08-08 方案）

## 背景

`docs/superpowers/specs/2026-08-08-approval-bpmn-convergence-design.md` 已经定了核心执行模型：**单个通用审批 UserTask + BPMN 声明式属性**（`assigneeRole`/`assigneeDeptId`/`assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId`/`candidateGroups`/`candidateUsers`），多级审批用多个 UserTask 节点在 XML 里静态排列，不是运行时数组驱动一个节点循环。`ApprovalChain`/`ApprovalChainResolver` 明确定位为"预解析元数据，仅供前端展示，不驱动执行"。

本文档写之前，先走查了这次会话早前误判为"待设计"的 MI（multi-instance）/子流程执行方向——那个方向已经被放弃：它跟 08-08 已经定的架构不是同一回事，08-08 的静态多节点方案已经部分落地提交，继续按原计划收尾比重新引入一套运行时动态执行机制风险更低、工作量更小。这次纯粹是把 08-08 没做完的部分补完，外加一个 08-08 没覆盖到的独立缺口（`handlers/change` 自己的审批状态机）。

## 现状核实（2026-08-13，直接读码 + git log 确认）

08-08 文档定义的四个组件，逐条核实当前落地程度：

| 组件 | 内容 | 状态 |
|---|---|---|
| ① | BPMN 引擎声明式属性解析（`assigneeRole`/`assigneeDeptId` 等） | **已完成**——`createUserTask`（`service/bpmn_process_engine.go`）已经实现完整优先级链 |
| ②2a | 退休 `CreateTicket` 双重触发 | **已完成**——`ticket_service.go` 只剩 `processTriggerSvc` 一条触发路径，`approvalSvc.TriggerApproval` 全仓库零调用 |
| ②2b | 补 `change_emergency_flow.bpmn` 缺文件 | **已完成**——文件存在 |
| ②2c | 修 `ProcessBinding` 种子数据 `business_type` | **已完成**——`config/seed/default.json` 里已经是 `business_type="ticket"` + 对应 subType 的形状 |
| ②2d | 4 个默认模板取舍（变更两个删、服务请求两个复用+新建 urgent） | **已完成**（写 spec 时验证不足，误判为未完成，实施计划前已订正）——commit `0366a6f8` 已经清空种子 `approval_workflows: []`、给 `service_request_flow.bpmn`/`service_request_urgent_flow.bpmn` 的 `Activity_Approval` 打上 `taskPurpose="approval"`，`process_resolver.go` 的 `ResolveWithPriority` 也已有 `service_request_flow`→`service_request_urgent_flow` 特判。唯一还没做的是 CAB 审批节点的声明式属性，见下方"剩余工作①（已改写）" |
| ③ | `buildLegacyApprovalBPMN` 字段映射修复 | **已完成**——`service/legacy_approval_migration_service.go` 已经用 `mapsToNodes` 强类型解析，7 种 `assigneeType` 映射表跟 08-08 设计一致 |
| ③ | 批量迁移 CLI | **代码已完成，未执行**——`cmd/migrate_legacy_approvals/main.go` 是完整的、默认 dry-run 的命令行工具，git log 显示只有一次"新增"提交，没有后续"迁移完成"的痕迹，需要真正跑一遍 |
| ④ | 下线 legacy `ApprovalWorkflow`/`ApprovalRecord` 引擎 | **未完成**——`router/router.go` 里 `/approval-workflows`、`/approvals` 的 CRUD + `/tickets/approval/submit` 全部仍是完全可写状态，不是只读，也没删 |

08-08 文档"非目标"里排除的是 `ApprovalChain`（`/admin/approval-chains`）和 Change 的原生 SQL 审批（本文档的 Track 5）。它**没有提到** `handlers/change/service.go` 自己维护的独立审批状态机（本文档的 Track 4）——核实结果是这条路径今天依然独立于 BPMN 之外运行。**订正（写实施计划时发现）**：Track4 的 `ApprovalRecord`/`ApprovalChain` 也不是 Ent 支撑，`handlers/change/repository_impl.go` 直接手写 SQL 操作 `change_approvals`/`change_approval_chains` 两张表（迁移 `006_add_change_approvals`）——跟 Track5 用的是同一批物理表，只是通过两套完全独立的代码路径读写，此前把 Track4/Track5 的区分描述成"Ent 支撑 vs 原生 SQL"是不准确的，实际区别只是"接了路由、有人调用"vs"零调用的孤儿代码"：

- `handlers/change/service.go` 的 `CreateChange`/`SubmitApproval`/`ProcessApproval`/`checkAndTransitionChange` 是一套完整的、自己维护 `ApprovalRecord`/`ApprovalChain`（change 包内自己的类型，不是 `ent/schema/approvalchain.go` 那个通用表）的状态机，创建阶段完全不碰 BPMN。
- `TransitionStatus` 里有一段标了 `P0-1` 的补丁（commit `f7e38cd1`，注释写"修复 WorkBuddy 审计报告 Critical/High 级业务安全缺陷"），审批通过/驳回时调用 `BPMNApprovalBridge.CompleteBusinessApprovalTask` 尝试追认一个"可能存在"的 BPMN 任务，找不到就静默回退成纯业务审批。这是打了补丁、没有真正重构的状态，正是 AGENTS.md "禁止桥接服务/新旧模型转换层" 想要防止的形态。
- `change_normal_flow.bpmn` 的 `Activity_CABApproval` 节点**没有配置任何声明式属性**——即使走 `itsm_type=Change` 的服务目录路径（会经过 Ticket→BPMN），CAB 审批也只会落到"申请人自己部门负责人"的默认兜底，不是真正的 CAB 成员组。这跟 AGENTS.md 要求变更管理保留 CAB 概念直接冲突，是一个独立于 Track 4 状态机问题之外的、需要一起修的数据配置缺口。

另外三个跟审批/变更相关、08-08 没提到的孤儿代码，这次一并处理：

- **Track 2b**：`service/bpmn/approval_handler.go` 已注册进 callback registry（`bpmn_callback_registry.go:134`），但 `submitApproval`/`approve`/`reject`/`delegate`/`escalateApproval` 全部只打日志、返回编造的成功结果。如果有流程节点声明 `taskType="approval_task"`，会得到假成功。
- **Track 5**：`controller/change_approval_controller.go` + `service/change_approval_service.go` + `service/cab_service.go`，原始 SQL 操作 `change_approvals`/`change_approval_chains` 表，全仓库搜索构造调用为零，是完全未接线的第三套变更审批实现。
- **Track 6（这次 spec 自查时新发现，08-08 也没提到）**：`service/change_service.go`（`NewChangeService`）同样全仓库零构造调用，是第四套变更实现的残骸，跟 Track4/5 一起清理。变更相关的孤儿实现比预期多，符合"同一类缺陷会在代码库里多处重复出现"的经验——这次借着 spec 自查顺带核实清楚，避免只删 Track5 漏了 Track6。

还有一个跟执行模型选型无关、独立的功能性 bug：

- **`need_approval`/`approval_required` 命名不一致**：`ticket_service.go`/`handlers/service_request/service.go` 设置的流程变量是 `approval_required`，但 `service_request_flow.bpmn`、`service_request_urgent_flow.bpmn`、`change_normal_flow.bpmn`、`change_emergency_flow.bpmn` 四个已部署种子流程的网关条件读的是 `need_approval`。全仓库没有任何变量别名/归一化逻辑，`need_approval` 从未被任何 Go 代码设置过——**任何应该走审批的工单/服务请求/变更，网关条件永远为 false，直接跳过审批**。这是已经部署到生产默认模板里的判断条件失效，不是能力缺口。

## 目标架构

```
剩余工作① 补齐 CAB 审批节点的声明式属性
         │
         ▼
剩余工作② need_approval/approval_required 命名统一（4 个种子文件 + 回归测试）
         │
         ▼
剩余工作③ 组件③收尾：批量迁移 CLI 验证执行 + 组件④ 下线 legacy 引擎
         │
         ▼
剩余工作④ Track 4：handlers/change 独立审批状态机迁移到 BPMN
         │
         ▼
剩余工作⑤ 清理 Track 2b / Track 5 / Track 6
         │
         ▼
剩余工作⑥ 前端核实与收敛
```

### 剩余工作① — 补齐 CAB 审批节点的声明式属性

组件②2d 的 4 个默认模板取舍在写 spec 时误判为未完成，实际已经在 commit `0366a6f8` 完成（种子 `approval_workflows` 已清空、`service_request_flow`/`service_request_urgent_flow` 已打上 `taskPurpose="approval"`、优先级路由已接好）。真正剩下的缺口只有一个：08-08 组件①定义了声明式属性机制，但从没把它应用到 `change_normal_flow.bpmn`/`change_emergency_flow.bpmn` 的 `Activity_CABApproval` 节点上——两个文件的这个节点目前没有任何 `assigneeRole`/`candidateGroups` 类属性，落到"申请人自己部门负责人"的默认兜底，不是真正的 CAB 成员组。

`service_request_flow.bpmn`/`service_request_urgent_flow.bpmn`/`ticket_general_flow.bpmn` 的审批节点没有配置声明式属性、落到部门负责人兜底，核实过是预期行为（服务请求/工单的通用审批走"申请人部门负责人"本来就是合理默认值），不需要动。

### 剩余工作② — `need_approval`/`approval_required` 命名统一

统一成 `approval_required`（应用代码已经在两个地方用这个名字，改代码成本更高、影响面更大；改 4 个 XML 文件的网关条件表达式成本最低）。改动：

- `service/bpmn/service_request_flow.bpmn`
- `service/bpmn/service_request_urgent_flow.bpmn`
- `service/bpmn/change_normal_flow.bpmn`
- `service/bpmn/change_emergency_flow.bpmn`

四个文件里 `variables['need_approval']` 全部替换成 `variables['approval_required']`。

**回归测试（这个 bug 之所以活到现在没被发现，是因为验证只测过内部函数调用/变量设置，没有走过真实部署的 XML）**：新增一个端到端测试，真实部署这几个模板、启动流程实例、设置 `approval_required=true`，断言流程确实进入审批节点而不是跳过。不能只测"变量被正确设置"，必须测"网关真的按这个变量做出了正确的路由决定"。

### 剩余工作③ — 组件③④收尾：执行迁移 + 下线旧引擎

1. 对 "default" 模板租户和所有已配置真实 `ApprovalWorkflow` 的租户，先 `-dry-run=true` 跑 `cmd/migrate_legacy_approvals`，核对生成的 BPMN XML 和 `ProcessBinding` 是否符合预期（尤其检查有没有 `amount_based` 节点被正确跳过并记录警告，不能静默丢弃）。
2. 确认 dry-run 结果无误后，`-dry-run=false` 真正执行，落地部署+建绑定。
3. 验证：抽查迁移后的租户，创建一个会触发审批的工单/服务请求，确认走的是新迁移出来的 BPMN 流程，不是旧引擎。
4. **下线旧引擎**（你已经确认历史审批记录不需要保留，所以这里不是"改只读"，是验证迁移完成后直接删）：删除 `controller/approval_controller.go`、`approval_chain_controller.go` 里跟 `ApprovalWorkflow` 相关的部分（`ApprovalChain` 通用表的端点不动，那是独立机制不在这次范围）、`service/approval_service.go`、`service/legacy_approval_migration_service.go`（迁移工具本身，全部迁完后历史使命结束）、`cmd/migrate_legacy_approvals`、`router.go` 里对应路由、`ent/schema` 里 `ApprovalWorkflow`/`ApprovalRecord` 表定义 + 迁移文件里的 DROP TABLE。

### 剩余工作④ — Track 4：`handlers/change` 迁移到 BPMN

复用组件①已经验证过的同一套模式，不新发明机制：

1. `handlers/change/service.go` 的 `CreateChange`/`SubmitChange` 改成调用 `ProcessTriggerService`，用 `change_normal_flow`/`change_emergency_flow`（`Type=="emergency"` 时）作为 `ProcessDefinitionKey`，不再调用 `s.repo.CreateApprovalRecord`。
2. `Activity_CABApproval` 完成时（通过/驳回），需要一个回调把结果写回 `Change.Status`——参照工单/服务请求"BPMN 流程完成时更新业务实体状态"已有的回调模式（`bpmn_process_engine.go` 的 ServiceTask callback registry 或等价的完成钩子），不新发明一套回写机制。
3. 删除 `handlers/change` 包内自己的 `ApprovalRecord`/`ApprovalChain` 类型、`checkAndTransitionChange` 状态机、`SubmitApproval`/`ProcessApproval` 里所有直接操作这两个类型的代码。
4. 删除 `BPMNApprovalBridge`/P0-1 补丁（`service/bpmn_approval_bridge_service.go`）——创建阶段本身已经在 BPMN 上，不再需要事后追认。
5. `router.go` 里 `changes.GET/POST /:id/approvals`、`/:id/approval-summary` 这几个端点改成读 BPMN 任务/`ProcessApprovalDecision`，不再读 change 自己的表。

### 剩余工作⑤ — 清理 Track 2b / Track 5 / Track 6

- 确认没有已部署的流程定义引用 `taskType="approval_task"` 后，删除 `service/bpmn/approval_handler.go`（及其在 `bpmn_callback_registry.go` 的注册）。
- 删除 `controller/change_approval_controller.go`、`service/change_approval_service.go`、`service/cab_service.go`、`service/change_service.go`（Track5+Track6，均确认过零构造调用的孤儿代码，删除风险最低，可以最先做，不用等剩余工作④完成）。

### 剩余工作⑥ — 前端核实与收敛

08-10 文档声称已经"隐藏旧「审批管理」菜单"、"新增「待审批」菜单聚合 BPMN 待审批任务"，但这次核实发现 `admin/approvals/page.tsx` 和 `my-approvals/page.tsx` 两个文件都还在。需要先核实现状（是否 `admin/approvals` 已经不在导航菜单里、只是文件没删；还是菜单和页面都还活着），再决定：

- 如果只是文件没删：跟着剩余工作③的旧引擎下线一起删除这个页面和它调用的旧 API。
- 如果菜单/路由仍然可达：需要先做一次真正的菜单收敛（隐藏或删除），并把审批规则配置统一到一个入口（`/workflow/approval-chains`，管理 `ApprovalChain` 元数据展示配置；BPMN 流程本身的编辑走已有的流程设计器，不需要为"审批规则"单独再造一个配置 UI）。

变更审批的前端（`change-api.ts` 里调 `/changes/:id/approvals` 的部分）在剩余工作④完成后，接口语义变了（从读 change 自己的表变成读 BPMN 任务），前端调用点需要同步核实是否要跟着改，还是后端保持相同的响应形状对前端透明。

## 测试计划

- 剩余工作①：CAB 审批节点断言候选人是配置的 CAB 组成员，不是申请人部门负责人；普通变更/紧急变更两条路径都要验证。
- 剩余工作②：见上方"回归测试"——四个模板各自真实部署+启动实例+断言网关正确路由，不能只测变量赋值。
- 剩余工作③：批量迁移 CLI 的 dry-run 输出人工核对；迁移后真实创建工单验证走新流程；`amount_based` 节点正确跳过并有明确警告，不静默丢弃；旧引擎端点下线后返回明确错误而不是 404（区分"从未存在"和"已下线"）。
- 剩余工作④：变更创建只触发一次 BPMN 流程（回归断言，参照剩余工作③"退休双重触发"验证方式）；CAB 审批通过/驳回后 `Change.Status` 正确流转；删除 P0-1 桥接后不再有"回退成纯业务审批"的分支残留。
- 剩余工作⑤：确认删除前无流程定义引用 `approval_task`；Track2b/5/6 各自删除前均已用 `grep` 确认零构造调用；删除后 `go build ./...` 通过。
- 全局：所有新增/修改的审批相关操作（触发、批准、驳回）都要有 `ProcessAuditLog` 写入，覆盖之前发现的"legacy 引擎和 change 自己的状态机都没有审计日志"这个缺口——迁移到 BPMN 之后应该自动满足，因为 BPMN 原生路径已经有审计写入，这里只是确认迁移后不会漏。

## 非目标（本次不做）

- `ApprovalChain`/`/admin/approval-chains`（预解析展示元数据）——继续保持"仅展示、不驱动执行"的定位，不在这次改成执行驱动。
- MI（multi-instance）/子流程动态执行——评估过，放弃。当前"静态多节点+声明式属性"模式已经能覆盖 Track1/3/4 的实际需求，不需要引入子流程执行、multi-instance 解析这类目前完全没有基础的新引擎能力。如果未来出现真实需要"审批级数在运行时可配、不能靠预先画好几种 BPMN 图覆盖"的场景，再单独立项评估。
- 会签/表决引擎（`approvalMode`/`CreateCounterSignTasks`）——现状已经是完整可用的能力，这次不涉及改动，仅在剩余工作①判断某个审批节点是否需要多人表决时可以直接复用，不重新设计。
- 审批规则配置 UI 的全新设计——如果剩余工作⑥发现需要新建配置入口，具体交互留到写实施计划时结合前端现状再定。
