# Review：《ITSM 架构可信化、Agent 平台化与规模化演进总设计》

> 审阅对象：[2026-09-01-architecture-hardening-agent-platform-evolution-design.md](./2026-09-01-architecture-hardening-agent-platform-evolution-design.md)（状态：已完成对话设计，待书面审阅）
> 审阅方法：逐项对照当前 worktree 代码（`f1129031` + 未提交状态一致）、`AGENTS.md`、`docs/architecture-product-assessment-2026-09-01.md`、以及更早的 `docs/superpowers/specs/2026-08-30-architecture-assessment-remediation-execution-plan-design.md`，凡文档给出"已核实"判断的地方均重新读代码验证，不直接采信文中结论。
> 审阅日期：2026-09-01

## 结论摘要

文档整体质量高，"二、已核实的当前基线"表格里的技术论断**逐项核查后基本准确**，其中至少两处比我自己 2026-09-01 撰写的 `architecture-product-assessment-2026-09-01.md` 更精确（见"对既有报告的订正"一节——包括我自己报告里的一处真实错误）。文档的 P0/P1/P2 分期、依赖图、wave 拆分和"不可变架构约束"与 `AGENTS.md` 没有发现冲突。

但发现 **1 项需要在书面审阅通过前处理的遗漏**（工单审批后端双轨，见下）和 **1 项需要文档明确澄清的结构性风险**（代码基线未合并入 main），以及若干可以在子设计阶段处理的次要问题。建议这两项处理完再进入"书面审阅通过"状态。

---

## 一、已核实为准确的关键论断

| 文档论断 | 核查方式 | 结论 |
|---|---|---|
| "BPMN ServiceTask dispatch：未注册 handler 已返回显式错误，不再是 NoOp" | 读 `service/bpmn_process_engine.go:953,997`，两处均为 `return fmt.Errorf("ServiceTask %s 声明的处理器未注册", elementID)`，调用链一路上抛不吞错误 | ✅ 准确。**这与我自己 09-01 报告的结论相反**——我当时引用的 `bpmn_process_engine.go:788,832` 是基于 `main`（`fda84251`），而这个 worktree 的分支比 `main` 多 49 个提交，其中大量是 `fix(bpmn):` 系列commit已经修复了这个问题。两份文档都对，只是基线不同——这恰恰印证了下面"结构性风险"里提到的基线披露问题。 |
| "BPMN callback：已有 durable callback outbox，但 Generic/CC/通知 handler 仍可能在目标缺失…时返回成功" | 读 `service/bpmn/notification_handler.go`：`sendEmail`/`sendSMS` 已经硬失败（"适配器未配置，不能声明发送成功"），`sendInAppNotification` 对空 `user_ids` 也硬失败——这部分实际上已经比文档描述的更好。但 `service/bpmn/generic_handler.go:143,160` 的 `notifyRequester` 在"非工单业务类型"和"查不到工单"两种情况下**仍然返回 `Success: true`**，代码注释原文承认这是有意为之："查不到工单按空态跳过而不是硬失败……让它把整条流程卡在通知节点上……得不偿失"，仅打 `Warnw` 日志，没有结构化审计/metric，也不是流程定义里预先声明的 optional。 | ✅ 论断成立，且精确命中了一个具体、可复现的代码位置（`generic_handler.go` 的 `notifyRequester`），是对 AGENTS.md Fail-Closed Dispatch 条款（"A step is legitimately optional only when it is declared optional ahead of time…not inferred at runtime"）的真实违反。文档 §5.2 提出的 `applied/idempotent/skipped_optional/blocked` 四态方案能直接对症下药。 |
| "RLS：Web 启动已安装可配置 driver，当前 driver 只观测并透传；真正设置 tenant session 的连接路径未接入" | 读 `database/database.go:225-253`（`InitDatabaseWithRLS`，本身即在 `off` 模式走 no-op passthrough，`shadow`/`enforce` 模式装饰 `entsql` driver 但**不主动 `SET LOCAL`**）；确认 `internal/bootstrap/app.go:234,1091` 两处都调用 `InitDatabaseWithRLS`（而非普通 `InitDatabase`）；`rls.AcquireConn`（`database/rls/rls.go:67`，真正设置 SESSION 变量的函数）在全仓库**零处**被业务代码调用，唯一"引用"是文档注释里的示例代码 | ✅ 完全准确，且是本文档里**最重要的一处精确判断**——见下方订正。 |
| "Outbox：通用 `outbox_events` 的生产写入主要是 KAF；BPMN callback 使用另一套持久 outbox" | `ent/schema/outbox_event.go`（KAF 专用写入点，`service/kaf_delegation_service.go:974`）与 `service/bpmn_callback_outbox.go`（`ProcessCallbackOutbox`，独立 schema，2026-08-26~08-31 的 `feat(bpmn): add durable callback outbox schema` 等提交引入）确认是两套完全独立的 outbox 实现 | ✅ 准确，是文档里对"重复实现"最有价值的一处诊断——直接支撑 §6.3 "一条 outbox row 只对应一个 destination…禁止两个 dispatcher 竞争同一 status" 的整合目标。 |
| "Audit：`AuditMiddleware` 已定义但未挂载；部分领域直接写 `AuditLog`，BPMN 又有自己的审计服务" | `router/router.go` 全文无 `AuditMiddleware` 挂载点（与我 09-01 报告一致）；`service/bpmn_audit_service.go` 写入独立的 `ProcessAuditLog` 表，与通用 `AuditLog`（`ent/schema/auditlog.go`）是两张表、两条写路径 | ✅ 准确。 |
| "process-trigger status、dashboard/audit 等入口仍缺同租户对象授权" | 读 `controller/bpmn_process_trigger_controller.go` + `service/bpmn_process_trigger_service.go`：`GetProcessStatus`/`CancelProcess`/`SuspendProcess`/`ResumeProcess` 只做 `tenantID` 相等性过滤（`processinstance.TenantID(tenantID)`），**没有**参与者/发起人/候选人级别的对象授权——而这正是同一分支里 `ListUserTasks`/`GetTask` 等主 API 已经补齐的那层策略。换句话说：同租户下任何持有粗粒度 BPMN 角色的用户都能 cancel/suspend/resume 别人发起的流程实例。 | ✅ 准确，且比一句"缺对象授权"更值得展开——这是一个真实、当前存在、影响面明确的授权漏洞，建议在 P1-C 的验收标准里明确列出"process-trigger 的 cancel/suspend/resume 必须补齐参与者授权"这一条，而不只是笼统的"完成所有入口收口"。 |
| "前端菜单：本地主 worktree 有未提交改动，不属于 `f1129031`" | 对照主 worktree（`/home/administrator/project/itsm`）的 `git status`，确认 Sidebar/route-config 改动确实未提交、且不在这个 worktree 分支历史里 | ✅ 准确，边界划得很清楚。 |

## 二、对既有报告的订正（含我自己的一处错误）

**这次审阅发现我自己 2026-09-01 撰写的 `docs/architecture-product-assessment-2026-09-01.md` 在 RLS 一节上有一处实质性错误，需要在此明确订正**：

我当时的报告（以及据此写入的记忆 `project_rls_not_wired_into_connection_path.md`）依据一个后台核查 agent 的 grep 结果，断言"`internal/bootstrap/*.go` / `main.go` 中没有任何地方引用 `rls` 包……从未被接入实际的数据库连接路径"。这个结论是**错的**：`internal/bootstrap/app.go` 在两处（含 `main` 分支本身）都调用 `database.InitDatabaseWithRLS(&cfg.Database, &cfg.RLS, sugar)`，这个函数本身就是 RLS 包的接入点，而且**这不是新代码**——我核实了 `main` 的历史版本，这个接线本来就存在，不是这个 worktree 新增的。当时那次 grep 大概率是只搜了字面量 `rls\.`（小写句点）之类的窄模式，漏掉了 `InitDatabaseWithRLS`/`RLSConfig` 这类大小写不同的实际调用点。

真实情况（本文档的表述）更精确：driver **已经**接到 DB 初始化路径上，只是 `off` 模式下是纯 no-op，`shadow`/`enforce` 模式下也只做观测（`Exec`/`Query` 层面的装饰），**不会**主动执行 `SET LOCAL`/`SET SESSION` 设置 tenant 变量——这一步被有意设计成由 `rls.AcquireConn` 或 middleware 显式调用（`database/database.go:216-224` 的注释解释了原因：一个 request 可能复用同一连接触发多次查询，装饰器内部没法安全地每次查询前后 SET/RESET），而 `AcquireConn` 全仓库零调用点。**结论相同（RLS 目前不提供真实防护），但机制描述不同**：不是"从未接入"，而是"接入了但只是观测层，真正的 enforcement 钩子从未被调用"。

已同步处理：更新 `docs/architecture-product-assessment-2026-09-01.md` 的 RLS 相关表述，并修正对应的记忆文件（见本轮审阅之后的 memory 更新）。**这个订正也是本次 review 最值得吸取的教训**：子 agent 的 grep 式核查即使"跑过了"也不能完全信任，尤其是像 `RLS`/`InitDatabaseWithRLS` 这种大小写、驼峰/下划线混用的场景，窄模式的字面量搜索容易漏掉真实调用点。

## 三、发现的问题

### 3.1（需要处理才能书面审阅通过）遗漏：后端仍存在"第二套审批引擎"，文档完全未提及

`docs/superpowers/specs/2026-08-30-architecture-assessment-remediation-execution-plan-design.md`（同一仓库、两天前、状态"Approved for planning"）在其"当前代码核实结论"表里明确写道："`TicketDetail.tsx` 仍用普通状态更新模拟批准/拒绝；`TicketApprovalApi.submitApproval` 后端仍查询、更新和创建 `TicketApproval`，再桥接 BPMN"，并判定"**不是单纯前端接线问题，运行态审批仍是双轨**"。

本次审阅重新核实了这一点，**在当前 worktree 基线上依然成立**：`service/ticket_workflow_service.go`（约 440-590 行，`ProcessApproval` 一类的方法）在同一次审批操作里，**既**调用 `s.approvalBridge`（`ApproveBusinessApprovalTask`/`DelegateBusinessApprovalTask`）桥接到 BPMN，**又**在同一事务里独立维护 `TicketApproval` 表的增删改（569-583 行）、并**直接**根据"待审批记录数是否为 0"这个自己的业务逻辑把 `tickets.status` 改成 `"approved"`/`"rejected"`（552-559、563-565 行）——这条状态写入路径完全不经过 BPMN 的流程token/网关判断。这正是 `AGENTS.md`"BPMN 是审批…唯一编排层，不创建第二套审批…引擎"这条不可变约束要禁止的模式，而且是**当前真实在跑**的代码，不是历史遗留的死代码。

**这件事在新设计文档里完全没有出现**——不在"二、已核实的当前基线"表格里，不在 §5.2 的 BPMN 授权/生命周期/效果语义范围内（那一节只讲对象授权和 callback 效果语义，不涉及"审批决策本身有没有第二个权威写入口"这个问题），也不在十二节的 11 个子项目清单里。考虑到：

1. 这是一个当前活跃、影响核心 Ticket 审批链路正确性的 P0 级问题（工单状态可能被 `ticket_workflow_service.go` 和 BPMN 两条路径互相竞争地改写）；
2. 08-30 的既有文档已经把它明确记录为"不能只当前端 bug 处理"；
3. 它与本设计文档 §5.1 的"WorkItem 公共状态只在共享写入口修改"这条架构决策直接相关——`tickets.status` 目前至少有 `ticket_workflow_service.go` 和 BPMN 两个写入口，正是 §5.1 试图消灭的那类问题，只是发生在"审批"这个具体场景下；

建议：要么在 §5.1（WorkItem 状态单一写入口）里显式把这个场景纳入范围并给出处理方式（例如："审批决策的 `tickets.status` 写入收敛到 BPMN 单一路径，`ticket_workflow_service.go` 的直接状态写入与 `TicketApproval` 独立生命周期维护在同一 wave 删除"），要么单独列一个 P1-E 子项目并说明为什么不在 Phase 1 处理。**不建议保持沉默**——沉默会让读者误以为"审批"这条线已经被 §5.2 的 BPMN 授权工作覆盖了，实际上没有。

### 3.2（需要文档明确澄清）结构性风险：代码基线是未合并分支，文档未披露

文档头部写"代码基线：`f11290317499b958ba93d85689286fdccccfe697`"，但没有说明这个 commit **不在 `main` 上**——`git merge-base --is-ancestor main f1129031` 确认 `main`（`fda84251`）是 `f1129031` 的严格祖先，中间隔着 **49 个未合并的提交**（`docs/architecture-agent-platform-evolution-20260901` 分支，worktree `bpmn-instance-authorization`），内容正是本文档"已核实的当前基线"表格里大量引用的 BPMN 授权/callback outbox/通知合同修复。

这不是说这些修复不存在或不可信——本次审阅逐项核实过，它们是真实、高质量的代码。问题在于：**文档把这个 worktree 分支的状态当成"当前基线"来推导后续 Phase 1 的剩余范围，但没有把"这 49 个提交先合并回 main"列为任何一个子项目的前置依赖**。如果这次合并被搁置、冲突后部分回退，或者与另一个并行 worktree（比如同样在改 BPMN/notification 的其他 wave）产生语义冲突，"二、已核实的当前基线"整张表就会失真，而 Phase 1 门禁（§5.3）里"BPMN ServiceTask dispatch 不重复修复"这类判断也会连带失效。

建议：在文档开头补一句明确声明（例如："本设计假设 `bpmn-instance-authorization` worktree 的 49 个提交已经或即将合并入 `main`；若合并未完成，§2 基线表格与 §5.3 门禁需要重新核实"），并在 §12 的依赖关系或 §8 的 wave 分配里给这次合并一个显式的位置（哪怕只是"P1-C 的第一步是把这个 worktree rebase/merge 到 main，而不是从零开始"）。这与本文档自己在 §8.2 强调的"文件所有权与集成"原则是一致的——只是目前这条"最大的一次待集成"没有被同等对待。

### 3.3（次要，建议在子设计阶段处理）

- **§5.1 "WorkItem 创建能力必须接受调用方事务"** 与当前 `TicketService.CreateTicket` 会自行提交事务的事实冲突已经点明，但没有说明这个改动对**现有调用方**（目前所有走 `CreateTicket` 的路径，包括纯 Ticket 创建本身）的兼容性处理方式——如果 `CreateTicket` 改成"不自行提交，要求调用方传入事务"，所有现有调用点（controller 层直接调用的场景）都需要同步改造。这不是错误，只是 P1-B 的设计范围应该显式包含"现有 `TicketService.CreateTicket` 调用方清单"，避免遗漏。
- **§6.3 唯一 Outbox 平台**里"KAF completion ledger 等具有独立业务语义的数据可以保留"这个例外条款写得比较宽——建议在 P2-C 子设计里明确列出"哪些现有专用状态可以保留、哪些必须收口"的具体清单（例如 `ProcessCallbackOutbox` 是否属于"可以保留的独立业务语义"还是必须收口的重复调度实现，目前文档两处表述之间有一点张力：§6.3 说"删除…BPMN callback 专用 worker/table"，但又说"具有独立业务语义的数据可以保留"）。
- 文档全篇没有引用到 `docs/architecture-product-assessment-2026-09-01.md` 里 KPI 一节（租户隔离验证覆盖率、WorkItem 数据一致性事故数等指标），而 §11.2"指标"一节其实覆盖了类似维度但口径不完全对应——建议对齐口径或说明这是有意的独立指标体系。

## 四、总体建议

文档达到"可以进入子项目详细设计"的技术质量门槛，**但建议在标记"书面审阅通过"之前**，至少处理 3.1（工单审批双轨遗漏）和 3.2（基线合并依赖披露）——两者都不需要重新设计，只需要在文档里补充范围声明和前置依赖，不影响已经写好的 Phase 1-3 整体架构。3.3 的三点可以留到对应子设计阶段处理，不阻塞本文档的书面审阅。
