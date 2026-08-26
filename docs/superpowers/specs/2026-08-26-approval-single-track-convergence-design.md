# 审批机制单轨化 — Change 域收口（续 2026-08-13 方案）

> **状态更新（2026-08-26 当晚）：本设计已被 origin/main 上已合并的 PR#6
> "Track4: 变更审批状态机迁移到 BPMN" 实现覆盖，不再需要按本文档执行。**
>
> 写作本文档时，本地工作分支 `fix/portal-approval-fake-success-and-real-data` 落后
> `origin/main` 24 个提交而不自知（分支于 2026-08-19 从 `7bef9bd9` 分叉，此后 origin/main
> 独立合并了 PR#6，2026-08-25 09:07），导致读到的 `handlers/change/service.go` 是分叉前的
> 旧状态，本文档"现状核实"一节的结论在写作时是真实的，但只对分叉点之前的代码成立。
> 事后把 `origin/main` merge 回本分支（commit `69ab3461`）并重新核实，确认 PR#6 已经完整
> 实现了本文档"变更点 1-4"的全部内容，而且比本设计更完善：
>
> - `SubmitChange` 已经是同步触发 BPMN + 失败即报错（`handlers/change/service.go:123-`），
>   还补上了本设计没覆盖的边界情况：并发重复提交的幂等守卫（按 `business_key` 查运行中实例）、
>   触发/评估节点自动完成失败时的补偿回滚（取消已创建的流程实例）、上线切换时刻存量 pending
>   变更的 BPMN 回填路径。
> - `TransitionStatus` 的 approve/reject 已完全交给 BPMN
>   （`completeChangeApprovalTask`/`authorizeTaskActor`），不再读 `change_approvals`；
>   变更进入终态时还会顺带取消挂起的运行中流程实例，避免孤儿实例堆积。
> - `GetApprovalHistory` 已经改读 `ProcessApprovalDecision`（`handlers/change/repository_impl.go:327-`），
>   `change_approvals`/`change_approval_chains` 的写入路径已删除。
> - `POST /changes/:id/approvals`（`SubmitApproval`）已经被整个删除——核实结论（零前端调用方）
>   和处置方式（直接删除不迁移）跟本设计"变更点 3"完全一致。
> - **本设计标记为"唯一需要动 BPMN 引擎本身"的技术缺口（运行时候选人列表）被绕开而非解决**：
>   实际实现把"提交人手动指定审批人"这个产品行为直接改成了"CAB 审批人按 `assigneeRole=
>   change_manager` 角色解析"（`dto/change_dto.go:378-379` 有明确说明，`ApproverIDs`
>   字段已删除），复用引擎已有的静态角色候选人能力，不需要新增变量驱动候选人分支。这是一个
>   更简单、风险更低的方案，值得作为以后遇到类似"提交时动态候选人"需求的默认参考解法。
>
> 这份文档保留作为历史记录（诊断过程和踩坑经验仍有参考价值，尤其是"如何核实一个大型状态机
> 是否真的收敛"的方法），但**不要按本文档的"变更点"重新实现**——去读
> `handlers/change/service.go`/`repository_impl.go`/`service/bpmn/change_handler.go` 的现状
> 和其中的注释即可。此事的更大教训见
> `docs/architecture-and-roadmap-assessment-2026-08-26.md` 的更新说明：核实代码现状前，先确认
> 当前分支相对 `origin/main` 是否存在未同步的分叉。

## 背景

`docs/architecture-and-roadmap-assessment-2026-08-26.md` 第一节把"审批仍是多轨并存"列为 P0，
枚举了 4 条活跃写路径：`ApprovalChain`、`TicketApproval`、BPMN `ProcessApprovalDecision`、change
域自有 `ApprovalRecord`。本文档写之前先做了一轮独立代码核实（见下方"现状核实"），结论是：这不是
一个全新问题，而是 `docs/superpowers/specs/2026-08-13-approval-bpmn-convergence-completion-design.md`
（以下简称"08-13 方案"）规划的"剩余工作④：Track 4 —— `handlers/change` 迁移到 BPMN"从未完整执行完。
08-13 方案里的其余 5 项剩余工作（①CAB 声明式属性、②`need_approval`/`approval_required` 命名统一、
③批量迁移+下线旧引擎、⑤Track2b/5/6 孤儿代码清理、⑥前端核实）已经在这之后的迭代里落地或部分落地
（见下表）。本文档只覆盖仍未完成的"剩余工作④"，并根据这次核实发现的新细节调整了实现方式。

Ticket 域的审批**不在本文档范围内**——这次核实发现 `TicketApproval.Create()` 全仓库只在委派
分支出现过一次，没有任何代码创建初始待审批记录，`ApproveTicket`/`/tickets/workflow/approve` 对
一张新工单实际不可达，是孤儿代码。Ticket 的审批早已活在 BPMN 里（`ProcessApprovalDecision` +
`/approvals` 中心），唯一的活 bug 是 `TicketDetail.tsx` 的审批按钮调用了错误的接口
（`updateTicketStatus` 而非 `approveTicket`），这个前端接线问题在
`docs/architecture-and-roadmap-assessment-2026-08-26.md` 第八节 P0-3 里单独跟踪，不与本文档
重复处理。`ApprovalChain`（`ent/schema/approvalchain.go` 通用表）同样不在范围内——它只是给
BPMN 流程变量做路由解析的配置元数据（`handlers/service_request/service.go:142-163`），08-08/
08-13 方案已经明确定位为"仅展示、不驱动执行"，不是竞争的写路径。

## 现状核实（2026-08-26，直接读码 + git log 确认）

08-13 方案定义的 6 项剩余工作，逐条重新核实当前落地程度：

| 剩余工作 | 内容 | 状态 |
|---|---|---|
| ① | CAB 审批节点补齐声明式属性 | 未重新核实细节，不影响本文档范围，維持原判断留待后续确认 |
| ② | `need_approval`/`approval_required` 命名统一（4 个 BPMN XML 文件） | **已完成**——`service/bpmn/*.bpmn` 全量 grep 无 `need_approval` 残留，`change_normal_flow.bpmn:127,130` 等网关条件均已是 `approval_required` |
| ③ | 批量迁移 CLI 执行 + 下线旧引擎 | **已完成**——migration `014_drop_legacy_approval_workflow`（`migration/migrations.go:78`），`approval_controller.go`/`approval_service.go` 已从 git 移除 |
| ④ | Track 4：`handlers/change` 独立审批状态机迁移到 BPMN | **未完成，本文档范围**——见下方详细现状 |
| ⑤ | Track 2b/5/6 孤儿代码清理 | **已完成**——`service/bpmn/approval_handler.go`、`service/change_approval_service.go`、`service/cab_service.go`、`service/change_service.go`、`controller/change_approval_controller.go` 均已从仓库移除 |
| ⑥ | 前端核实与收敛 | 未重新核实，不影响本文档范围 |

### 剩余工作④现状细节（比 08-13 方案描述的更接近完成，但核心问题未解决）

08-13 方案写作时，`handlers/change/service.go` 完全没有 `processTriggerSvc`。现在已经有了
（`service.go:26,44-49`），说明中间有一次未写入 spec 的增量修复。当前实际状态：

- `SubmitChange`（`POST /changes/:id/submit`，`service.go:113-172`）：
  1. 先调用 `s.repo.SubmitForApproval(...)`（`service.go:146`）——原生 SQL 写
     `change_approvals`/`change_approval_chains` 两张表（迁移 `006_add_change_approvals`），
     approver 列表来自调用方直接传入的 `req.ApproverIDs`（**不经过任何策略解析**，是提交人
     手动指定审批人，不是 `ApprovalChainResolver` 式的自动路由——这与 Ticket/SR 的模型不同，
     是 Change 域一直以来的既有行为，本次设计需要保留这个"手动指定审批人"的能力，不是要
     新增一个 CAB 策略解析器）。
  2. 再调用 `s.processTriggerSvc.TriggerByBusinessType(...)`（`service.go:163-169`）触发
     `change_normal_flow`/`change_emergency_flow`，**fail-soft**：触发失败只
     `s.logger.Warnw(...)`，不阻断提交，注释原文"变更生命周期本身不依赖流程实例，
     approvalBridge 对'无关联流程实例'回退纯业务路径"（`service.go:156-158`）。
  3. `Change.Status` 直接置为 `"pending"` 并返回成功，与流程实例是否触发成功无关。
- `TransitionStatus`（`service.go:566-596`）：approve/reject 时，`isApprover` 判断读
  `s.repo.GetApprovalHistory(...)`（即 `change_approvals` 表），**不查 BPMN**；判完权限后才调用
  `s.approvalBridge.CompleteBusinessApprovalTask(...)` 同步 BPMN 任务，**若同步失败会中止**
  （`service.go:587-594`），但"谁能批"这个判断本身完全不依赖 BPMN 是否存在关联实例。
- `SubmitApproval`（`POST /changes/:id/approvals`，`handler.go:344-372` → `service.go:180-`）：
  一个**独立于 `SubmitChange` 的第二入口**，直接调用 `s.repo.CreateApprovalRecord(...)`
  新增一条 `change_approvals` 记录，完全不碰 `processTriggerSvc`/`approvalBridge`。用途不明确
  （可能是"提交后追加审批人"），需要在实现前确认这是否是仍在使用的真实业务能力。
- 阶段流转（scheduled/implementing/verifying/closing 等，`service.go:622-636`）已经桥接到
  `CompleteBusinessStageTask`，且失败会中止（`service.go:628-635`）——这部分已经是"BPMN 为权威"
  的正确模式，不需要改动，本文档只处理审批（approve/reject）这一段。

**结论**：`change_approvals`/`change_approval_chains` 目前既是审批人名单的唯一来源，又是"谁能批"
判断的唯一依据；BPMN 只在批准/驳回**发生之后**被追认同步，且流程实例本身能否成功绑定是"尽力而为、
静默失败"的。这正是评估文档里"审批机制多轨并存"在 Change 域的具体体现。

## 目标设计

**原则**：复用 Ticket/SR 已经验证过的模式——审批人名单和流程绑定在**提交阶段就必须成功**，
approve/reject 阶段的"谁能批"判断改为查 BPMN 待办任务，不再依赖独立表提前判权。`change_approvals`/
`change_approval_chains` 从"权威判权表"降级为"由 BPMN 决策事实回填的历史/展示投影"。

### 变更点

1. **`SubmitChange` 改为同步 + 硬约束**：
   - 先调用 `processTriggerSvc.TriggerByBusinessType`（同步，不再是 fire-and-forget），把
     `req.ApproverIDs` 连同 `approval_required` 一起作为流程变量注入（**沿用现有变量注入方式,
     不新增 `ApprovalChainResolver.ResolveForChange`**——因为 Change 域的审批人是提交人手动指定，
     不是策略解析出来的，这与 Ticket/SR 的路由场景不同）。
   - 触发失败（流程定义未部署、`ProcessTriggerService` 报错等）时，`SubmitChange` 整体返回错误，
     不再置 `Change.Status = "pending"`，不再写 `change_approvals`（原子性：要么流程绑定和审批人
     记录都成功，要么都不成功）。
   - `s.repo.SubmitForApproval` 的调用顺序移到流程触发**成功之后**，此时它的角色变成"把已经
     生效的审批人名单落一份历史投影"，不再是独立判权的来源。
   - **技术缺口（已核实为真实缺口，非待验证假设）**：读了 `bpmn_process_engine.go:775-940`
     的 `createUserTask` 完整分支逻辑，确认候选人解析目前只有三种来源，且互斥、按优先级短路：
     ① BPMN XML 静态声明的 `candidateGroups`/`candidateUsers`（一旦声明，跳过其余所有分支，
     见 `service.go:819` 附近注释"说明这个节点的路由方式是配置驱动的，不触发下面任何自动
     解析"）；② `assigneeRole` 按角色查询该租户下所有该角色用户；③ `assigneeDeptId`/
     `assigneeGmChain` 等单一动态 assignee（解析出唯一一个人，走部门树/个人汇报链，供
     `DeptManagerResolver`/`PersonalManagerResolver` 用）。**没有任何一条路径支持"运行时传入
     一组任意用户 ID 作为候选人列表"**——这正是 Change 域"提交人手动指定审批人"这个既有能力
     需要的。这意味着本设计必须新增一个变量驱动的候选人分支（例如识别 XML 属性
     `assigneeType="fromVariable"` 或约定流程变量 `candidate_user_ids` 数组，在
     `createUserTask` 里新增一个与现有三种互斥的第四分支），是本设计里**唯一需要动 BPMN 引擎
     本身（而不只是 `handlers/change`）的部分，也是工作量和风险最大的一块**，需要在写实施
     计划时单独拆一个任务、给足够的引擎层测试覆盖（不能只在 `handlers/change` 里测，要验证
     引擎新分支不影响现有三种候选人解析路径的行为）。
2. **`TransitionStatus` 的 `isApprover` 判断改为查 BPMN**：approve/reject 时，改成调用
   `approvalBridge` 同款的"按业务键查运行中实例的当前待办任务"逻辑，校验 `userID` 是否为该任务的
   assignee/candidate，而不是查 `change_approvals` 表。判权通过后，仍然调用
   `CompleteBusinessApprovalTask` 完成 BPMN 任务（失败则中止，这部分逻辑不变），**额外**把
   `change_approvals` 对应记录同步更新为 approved/rejected（作为历史投影回填，供列表/审计展示，
   不再参与判权）。
3. **删除 `SubmitApproval`（`POST /changes/:id/approvals`）**：已核实 `itsm-frontend` 里
   `change-api.ts:316` 只调用了 `GET /api/v1/changes/${id}/approvals`（查历史），全仓库
   grep 没有任何地方调用这个 POST 端点——是孤儿入口。直接删除 `handler.go:344-372` 的
   `SubmitApproval` handler、`service.go:180-` 的 `SubmitApproval` service 方法、
   `router.go:934` 的路由注册，不做迁移/兼容。
4. **收尾清理**：迁移删除 `ent/schema/ticket_type.go:24-25` 的 `approval_workflow_id`/
   `approval_chain` 残留字段（这两个字段与本文档范围内的 Change 改动无直接关系，但同属
   "审批单轨化"P0 项的收尾项，一并处理成本更低）；清理 `docs/docs.go` 中过期的
   `/approval-workflows` swagger 声明（`docs.go:722,809,1755`）。

### 不改动的部分（已经是正确模式，本次不动）

- 阶段流转（scheduled/implementing/verifying/closing）的 `CompleteBusinessStageTask` 桥接——
  已经是"失败则中止"的硬约束模式，不需要改。
- Release 域（`service/release_service.go`）如果存在相同的 fail-soft 触发模式，**不在本次范围**，
  评估文档没有把 Release 列为 P0 审批问题，待后续单独评估是否需要同样改造。

## 测试计划

- `SubmitChange`：流程定义不可用时（mock `processTriggerSvc` 返回错误），整个提交动作报错，
  `Change.Status` 保持 `draft`，`change_approvals` 无新记录写入（验证原子性，替代原来"静默通过、
  只记 Warning"的行为）。
- `SubmitChange` 正常路径：`req.ApproverIDs` 正确转化为 BPMN userTask 的候选人（需要一个真实
  部署 `change_normal_flow` 并断言候选人列表的集成测试，不能只测变量是否被设置——08-13 方案
  里"网关变量名不一致"的教训是必须验证到执行结果，不能停在断言函数调用参数）。
- `TransitionStatus`：非候选人/非 assignee 的用户尝试 approve/reject 应该被拒绝（判权来源已切换
  到 BPMN 任务，需要覆盖"legacy 表里存在这个人但 BPMN 任务候选人里没有"这种此前会被放行、现在
  应该被拒绝的场景，这正是"单一权威源"要修复的安全边界）。
- 回归：现有 change 审批相关测试（`handlers/change/service_test.go` 等）在改动后应继续通过或
  按新语义更新，尤其是 CAB 多级/角色审批相关用例。
- `go test ./handlers/change/... ./service/... -run Approval` 通过；`go build ./...` 通过。

## 非目标（本次不做）

- Ticket 域审批清理（孤儿代码删除 + `TicketDetail.tsx` 接线修复）——单独在
  `docs/architecture-and-roadmap-assessment-2026-08-26.md` 第八节 P0-3 里跟踪，不在本文档重复。
- `ApprovalChain` 通用表——继续保持"仅展示、不驱动执行"定位，不改成执行驱动。
- Release 域是否有相同的 fail-soft 触发问题——不在本次评估范围，待后续单独立项确认。
- 事件总线化的历史投影回填（用 `Publish`/订阅方式把 BPMN 决策事实同步到 `change_approvals`）——
  本次先用同步直接写的方式满足"投影更新"，事件总线统一是评估文档里单独的 P1 项，等它完成后
  `change_approvals` 的回填方式可以顺势切换成订阅模式，不在本次重复设计。
- CAB 审批人从"提交人手动指定"改成"策略引擎自动解析"——如果未来产品需要这个能力，是一个独立的
  需求变更，不是本次"单轨化"的范围（本次是收敛判权来源，不是重新设计审批人选取方式）。

## 未决问题

均已在写本文档过程中核实清楚，不再有遗留的未决问题：

1. ~~`createUserTask` 是否支持变量驱动候选人列表~~ → 已核实**不支持**，已并入"变更点 1"作为
   本设计的核心工作项（新增引擎分支），不是可选项。
2. ~~`SubmitApproval` 是否仍有前端调用方~~ → 已核实**没有**，已并入"变更点 3"作为直接删除。
