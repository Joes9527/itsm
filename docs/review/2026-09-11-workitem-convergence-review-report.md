# WorkItem 收敛 B/C1 独立评审与接手记录

日期：2026-09-11。状态：reviewed — changes required；不是部署验收。

更新：按维护者要求重新以 **WorkItem 重构本身** 为基准复核，见第 9 节。第 9 节的主线排序取代第 6、7 节原先仅从 B/C1 缺陷出发的接手排序；R1–R6 仍然有效。

总体开发输入：[WorkItem 重构后续开发输入与决策记录](../superpowers/specs/2026-09-11-workitem-convergence-development-input.md)。维护者明确继续覆盖 Change、Problem 与公共能力，Incident 分配仅为子决策。

后续决策：R7 的业务语义已经维护者确认，见 [Incident 分配与处理动作设计](../superpowers/specs/2026-09-11-workitem-incident-assignment-convergence-design.md)。该设计记录讨论依据及 backlog；代码缺陷尚未修复。

## 1. 结论与评审边界

保留已有收敛成果，但不接受 B2、C1 当前版本无条件签收。确认 4 项 P1 和 2 项 P2：流程启动身份可覆盖、默认种子仍写旧身份、MSP 通知在生产 RLS 下阻塞、临时错误失去重试，以及通知授权和正文问题。其中启动覆盖属于原有缺陷但落在 C1 明确承诺的收敛范围；不是声称所有问题均由 C1 新增。

输入为 `docs/workitem-convergence-B-C1-report-2026-09-11.md`，并阅读 WSL 实际 `AGENTS.md`（未找到项目自己的 `AGENT.md`）、工程治理、收敛设计、C 子计划及历史评审记录。依据源码、真实装配路径和新增反例判断，不以实现者的 Ready to merge 代替独立结论。

本次重点：B 关系/联动及 C1；A1–A4、B1 更早实现仅核对依赖与影响，未做全部逐行重新签收。历史完成工作不重做。未执行 C2/C3、共享数据库迁移、业务服务重启、推送或合并。

## 2. 已固定的源码与接手位置

| 项目 | 本次观察 |
|---|---|
| WSL | administrator@192.168.31.66:22222，Linux WSL2 |
| 原 worktree | `/home/administrator/project/itsm/.worktrees/workitem-convergence-relations` |
| 原分支 | `codex/refactor/workitem-convergence-relations` |
| HEAD | `4660633019f10ec23835e23c7fa43578daa7ff57`，报告提交 |
| C1 | `b06845f5..9dcb36f9` |
| B | `73b7ba69..b06845f5` |
| WSL main | `2fa93c1b`；原分支相对此 main 领先 51 提交 |
| squash 内容 | `9dcb36f9^{tree}` 与 `c1-pre-squash^{tree}` 同为 `e6c183c65d643fcf08d541777ebd61fb85a92a33` |
| 接手评审 worktree | `/home/administrator/project/itsm/.worktrees/workitem-convergence-review-20260911` |
| 接手评审分支 | `codex/docs/workitem-convergence-review-20260911`，从上述精确 HEAD 建立 |

没有 fetch 或将旧 main 当作最新远端；51 是相对本地 WSL main 的事实。报告称“未推送”是原交付记录，本轮没有查询远端以独立确认。原业务分支保持不变，原 `.superpowers/sdd`、安全标签及已有 worktree 保留。新分支只承载本轮评审与接手文档。

## 3. 必须先处理的发现

以下文件和行号均相对原 WSL worktree、固定 HEAD。

### R1 — P1：流程启动仍允许覆盖规范身份

位置：`itsm-backend/service/bpmn_process_trigger_service.go:323-325`。

`buildProcessVariables` 先写入标准 `business_type/business_id/business_key`，随后无过滤合并请求的 Variables。HTTP `controller/bpmn_process_trigger_controller.go:93-105` 将该输入传入 TriggerProcess；引擎 `service/bpmn_process_engine.go:403` 持久化变量。顶层身份验证不能保护随后被覆盖的变量。

动态反例：在原 `TestTriggerProcess_PopulatesStructuredBusinessIdentity` 上通过 Go overlay 注入三项变量，启动成功，但持久化为：

```text
实例列:   (change_request, 1, change_request:1)
实例变量: (ticket, 999, ticket:999)
```

增强的身份一致断言失败（期望 change_request，实际 ticket）。网关表达式读取变量；因此至少会形成双身份和错误分支输入，不能据此扩大宣称已经发生任意跨租户写入。当前预检只核对结构字段与 WorkItem，不能发现这一变量污染。

建议：在外部触发边界复用保留变量契约，拒绝或剥离覆盖，最终从已授权规范身份注入可信变量；不新增第二份词表。保留内部完成管线合法传递语义。补 HTTP→实例列/变量→网关的测试，以及污染变量的预检处置。验收必须包括旧词、另一个合法 ID、普通变量正常透传。

### R2 — P1：默认 JSON 种子绕过 C1，重新写入退役绑定

位置：`itsm-backend/config/seed/default.json:377,392,397`；`pkg/seeder/seeder.go:328-336,380-381,1111-1118`。

JSON 仍含 ticket/change/ticket+service_request。正常项目配置加载会整体覆盖已改为规范词表的内置 ProcessBindings；Seeder 直接 Ent 创建，绕过 CreateBinding 的新校验。从后端工作目录初始化空租户时即可把旧绑定重新启用，规范 generic/change_request/service_request_item 无法依赖这些旧绑定匹配，切换预检也会阻塞。

建议：收敛实际加载的数据源和导入边界；新增“读取真实默认 JSON→最终配置→创建绑定→规范启动”的测试。不能仅修改内置配置、仅扫描 Go 常量，或通过关闭预检解决。此项由静态完整加载链确认，本次未对共享租户运行初始化。

### R3 — P1：MSP 修复在生产 RLS 下仍失败，测试使用了错误客户端

位置：`itsm-backend/service/work_item_relation_delivery.go:87-89`；同类问题见 `work_item_relation_outcome_delivery.go:72-74`、`problem_resolved_delivery.go:66-68`。

生产 `internal/bootstrap/app.go:265,694` 给消费者注入 Tenant client；worker `service/outbox_delivery_worker.go:121` 使用事件客户租户 context。此时查询 provider 原生租户 actor，即使 SQL 改用 ActorTenantID，仍被 users 的 RLS 隐藏。

原 MSP 测试本身在 `tests/integration/workitem_relation_events_postgres_test.go:238-239` 证明 tenant client 看不到 provider actor，却在 helper `:123-127` 给消费者和通知服务注入管理员 `f.client`，掩盖了真实装配问题。

本次精确对照：同一条 MSP 测试原样 PASS；仅通过 `/tmp` overlay 将 helper 的消费者/通知服务改为 `f.runtime.Tenant`，worker 队列仓库仍保留管理员边界，结果 FAIL：

```text
Should not be: "blocked"
relation actor unavailable in tenant
```

测试使用既有 disposable PostgreSQL 17，端口仅 127.0.0.1:36444；两轮独立 schema 和临时 LOGIN 角色清理均 remaining=0。

建议：复用受限 directory/当前租户会话授权能力解析 MSP 身份，业务读写继续使用目标租户客户端；不能把所有消费者改为系统客户端来“修复”。测试必须复制真实 composition root 的 client 角色，覆盖三类消费者、allocation 撤销和目标租户不泄漏。

### R4 — P1：临时数据库错误被转为不可重试的 blocked

位置：`itsm-backend/service/work_item_relation_delivery.go:87-89,102-111`；同类路径见 outcome `:106-108`、problem resolved `:98-100`。

这些调用把 actor/端点查询的全部错误包装为 blockOutboxDelivery，包含连接错误、查询超时和 handler deadline。worker `outbox_delivery_worker.go:131-143` 对此调用 MarkBlocked，绕过 `:152-154` 的重试路径。故障恢复后，事件不会自动恢复投递。B2 已修复的“优先保留可重试扇出错误”无法挽救上游已经错误分类的失败。

建议：仅将明确不存在、权限拒绝、契约错误列为 blocked，基础设施错误保留可重试类别并携带安全上下文。补验证/回执读取成功后注入端点查询故障的测试，断言 retry→故障恢复→恰好一次通知。不得通过统一吞错或把所有权限错误改成重试解决。本项为源码路径确认，本轮未执行故障注入。

### R5 — P2：投递“当前可读”只检查行范围，遗漏专业 RBAC/租户会话撤销

位置：`itsm-backend/service/work_item_relation_delivery.go:100-104` 及两个 outcome 消费者的对应路径。

WorkItemReadScope 只判断 requester/assignee 等行条件；`authorization/workitem_read_scope.go:8` 明确指出专业权限是独立要求。消费者没有完整复查专业 read permission、有效 role、MSP allocation。事件提交后撤销 actor 的专业读取权限但保留 active/行归属，消费者仍可能执行宣称须当前授权的通知。

建议与 R3 同批处理当前身份/授权边界，复用现有权限能力。验收包括权限/role/allocation 撤销，不用客户端角色字符串证明授权。此项不等同于已证明跨租户数据泄露。

### R6 — P2：关系通知正文把接收端写成发生关联的另一端

位置：`itsm-backend/service/work_item_relation_delivery.go:76,121-124`。

从 A 操作 A→B 时，通知归属 B，同时正文也使用 B 的编号，形成“工作项 B 与当前记录关联”，未告诉 B 的处理人 A 是谁；解除关联后尤其无法追溯。建议正文使用 mutation endpoint A，通知归属/接收人仍为 B，测试覆盖从两端操作及删除关系。

## 4. 对原报告判断的复核

| 原判断 | 本次判断 |
|---|---|
| 关系行、回执、Outbox 同事务 | 所审路径支持该判断，未发现应另建机制的理由 |
| 复用 delivery_key 幂等 | 保留；需要补生产客户端及故障恢复测试，不能只用 happy path 证明可靠性 |
| B 批已签收 | B1 已有成果保留；B2 的 R3–R6 使无条件签收不成立 |
| C1 所有身份边界收敛 | R1/R2 反例推翻“所有”；已实现的叶子词表、绑定校验、回调修复保留 |
| 完成表单剥离、实例变量端点拒绝 | 可以是合理的边界分工；不能从 22 条内部流程失败反推所有外部入口都应放行 |
| 预检只读、截断即阻塞 | 设计正确，静态复核未发现新增确定问题；本轮未重跑 11 项 PG 预检测试 |
| Swagger 另开变更 | 同意拆分审查，但必须作为契约发布依赖；5000 行不是可以带旧词表发布的理由 |
| 3 个失败都是既有基线 | 仅支持“C1 前存在”，不能据此免责整个收敛交付，见下文 |

迁移计数测试期望 24、实际 28，本次重现。`migration/migrations.go:457-460` 的 032–035 正是 A 阶段引入，故至少这个失败属于整个待交付分支的测试维护债务。应验证准确版本集合、顺序、SQL 和重复初始化行为，不能只机械加数字。

另外两项本次也重现：冻结 Incident 分配回调 expected completed / actual blocked；Problem HTTP 创建因 category 旧字段被拒绝（201→400）。尚未在 main 与各阶段做同环境逐项归因，不能先把它们归为“环境问题”或直接放宽业务校验。尤其冻结回调可能代表真实入口回归，需要先检查 last_error 与授权契约。

原报告中的“49 个提交=B 批”不精确：main→HEAD 还包含分类、RCA 依赖与 A 阶段。报告第 9 节的 50 和顶部的 51、复现步骤预期 HEAD=9dcb36f9 也有文档漂移；真实 HEAD 已是报告提交 46606330。

## 5. 本次验证与明确未验证项

全部定向验证在固定 WSL 代码上执行，未以旧日志冒充新测试。

| 验证 | 结果 |
|---|---|
| `go build ./...` | exit 0 |
| `go test ./dto ./common/workitemidentity ./service/bpmn ./handlers/problem ./handlers/change -count=1` | exit 0；identity 叶子包无测试文件，DTO/其余包通过 |
| 报告中的三个失败用例定向重跑 | 三项均失败，具体差异如上；同次包含 B 事件注册/扇出定向测试 |
| approvals / WorkItemRelations / workitem-relations 三个前端套件 | 3/3 套件、19/19 断言通过；coverage=false，非全量覆盖率门禁 |
| 规范启动原测试与 reserved helper 原测试 | 通过；增强启动身份一致性断言失败 |
| MSP 原测试与生产客户端 overlay 对照 | 原始通过、overlay 精确失败，临时 schema/角色清理完成 |
| `git diff --check b06845f5..HEAD` | 通过 |
| `git diff --check 2fa93c1b..HEAD` | 存在 inherited RCA SQL 行 95 的尾部空白；不能宣称整体干净 |

未重跑：后端全量、前端全量 test:ci 及其 b06845f5 对照、完整类型检查/前端 build、全部 PG B 套件、11 项 cutover PG、真实浏览器跨域旅程、C3 观察/恢复/删除。原报告的服务 readiness/迁移漂移描述没有在本轮复核，不作为当前已证实环境事实。这里的 WorkItem C1 不与此前 SSLVPN 同名阶段混淆。

## 6. 下一步重构输入与接手顺序

不要重做 A1–A4/B1，也不要在现有权威边界外增加补丁框架。按以下单目标顺序推进，每项单独保留回归证据与独立评审：

1. **流程身份补齐（R1/R2）**：外部触发变量、真实默认配置、种子写入和预检形成同一契约；同时评估其他脚本/import 对 ProcessBinding 的直写。验收包含真实 HTTP 输入、实际 JSON 加载及规范/旧身份反例。
2. **通知投递边界（R3–R6）**：目录身份与目标租户数据边界分开；当前授权完整；暂时失败可重试；消息明确另一端。用生产 RLS 客户端执行 MSP、权限撤销、部分失败重试和幂等测试。
3. **关闭可交付性债务**：三个后端失败逐项归因/修复；固定 Swagger 工具版本并单独生成审查；前端 baseline 与 HEAD 用相同命令、同样 workers/coverage、顺序执行比较，区分断言/超时/覆盖率失败。归档命令和退出码，不改阈值换绿色。
4. **C2 完整用户旅程**：从现有 DTO/页面继续，不机械执行旧计划重复造 mapper。Problem 页已经取 workItemId/number，但公共投影仍未带 version；后端 Problem 实体已经有 Number/Version。先盘点实际消费者和 action projection，再补 expectedVersion、稳定操作键、409 保留输入、明确重新确认。用专业 ID≠WorkItem ID 数据验证 Incident→Problem→Change、永久方案验证、失败 Change、SLA 重开历史、评论附件和无权限 API。
5. **C3 部署与退役门禁**：C2 全部通过后才进入隔离切换/备份恢复演练。实际共享环境迁移和旧结构删除仍需环境批准；本报告及用户的接手要求不替代删除批准。不自动取消旧流程、不迁移历史业务数据、不仅回滚二进制而遗漏数据库新写入。观察退出条件必须来自真实业务动作、回调重试及无未解释错误。

保留的架构决定：专业域掌握状态机；WorkItem 掌握身份/版本/公共能力；关系只表达关系；Change 成功只促成验证而非自动解决 Problem；Incident 恢复不依赖 Problem 完成；现有 BPMN、Outbox、目录和 SLA 能力继续作为唯一实现。

## 7. 接手台账

| 阶段 | 接手状态 | 下一动作 |
|---|---|---|
| A1–A4 | 历史已有完成/评审记录；本轮未全部重新签收 | 保留，追踪迁移测试与冻结分配失败来源 |
| B1 | 历史已完成；本次关系写入和前端定向核对支持保留 | 不重新实施；纳入 C2 集成回归 |
| B2 | needs fixes | R3–R6 后用真实装配独立复核 |
| C1 | needs fixes | R1/R2 与 API 契约债务闭环 |
| C2 | not started | 在上述阻塞处理后完成差量计划与完整旅程 |
| C3 | not started | C2、隔离演练和观察证据是前置 |

原 `.superpowers/sdd/.../progress.md` 顶部任务勾选只到 A3，后续正文记录了 A4/B1 完成，顶部与报告不一致。接手以本表的固定版本复核结果解释这些差异，保留原始历史；之后每个实现提交必须更新当前台账，不能只在长日志尾部追加一句“完成”。正式设计/计划的 draft、accepted、代码尚未实施标记也应按真实完成范围逐项修正，避免一次性把整个 C 阶段标为 implemented。

本轮交付是评审和接手基线，业务修复尚未实施。接手的第一项实现工作明确为 R1/R2，随后 R3–R6；没有直接跳到 C2 或把全量失败豁免。

## 8. 复现证据位置

在接手评审 worktree 的忽略目录 `.superpowers/sdd/2026-09-11-workitem-review/` 保存：

- `targeted.log`：三个既有失败的本轮实际输出。
- `frontend.log`：3 套件、19 项通过。
- `trigger/overlay.json`、`trigger/trigger_test.go`、`trigger/overlay.log`：身份分裂反例。
- `msp/baseline.log`、`msp/runtime-overlay.log`、`msp/overlay.json` 及替换测试文件：管理员客户端与生产客户端同测试对照，以及 schema/角色清理证明。

overlay 映射指向原固定 worktree，只替换测试文件，不替换生产源码；复现时必须先确认原 HEAD 仍为 46606330。触发反例命令为 `go test -overlay=<评审证据目录>/trigger/overlay.json ./service -run '^TestTriggerProcess_PopulatesStructuredBusinessIdentity$' -count=1 -v`，预期失败。MSP 命令为 `go test -overlay=<评审证据目录>/msp/overlay.json -tags=integration_postgres ./tests/integration -run '^TestWorkItemRelationEventsDeliversForMspProviderActor$' -count=1 -v`，预期失败；DSN 必须仅指向已经核验的 disposable 库，并经环境注入，不打印凭据。

反例失败是确认缺陷的证据，不计入业务测试通过数；修复后应将对应用例变成正式、可维护的回归测试。

## 9. 二次复核：回到 WorkItem 重构的本质需求

### 9.1 需求基准与范围

本任务是 WorkItem 重构的延续，B/C1 报告只是过程输入。验收权威是 AGENTS.md 中的 WorkItem 领域合同、2026-08-26 原模型的目标/完成定义，以及 2026-09-09 accepted 收敛设计。原模型的“当前断点”是历史描述，不能当作当前缺陷；后续 accepted 决策对本轮历史免迁移、SLA 重开、旧结构观察后删除的规定优先。

本质目标是：每个已纳入范围的专业记录有唯一 WorkItem 基础身份；公共事实一处持久化；所有入口遵循同一专业动作契约；公共协作和运行能力使用这条基础记录；跨域通过关系协作；旧权威路径最终退出。

不能把目标替换为统一页面外观、批量改名、修几个 BPMN/通知 bug，或把所有专业服务合并成一个通用状态机。“同一权威写入位置”指同一事实及持久化位置，并不意味着所有专业动作都必须移进一个 WorkItemService。专业 DTO 联查投影公共字段也不等于重复持久化。

当前 accepted 收敛任务主攻 Incident/Problem/Change，并回归 generic/Requested Item；Catalog Task 的完整创建能力、新专业 SLA 目标、多条目 Request Header 明确另行设计。原模型覆盖更广，因此不能把本轮完成宣称为所有长期 WorkItem 能力完成，也不应为了符合原总图擅自扩建 Catalog Task。

### 9.2 目标→代码→剩余工作的核对

| 重构成果 | 当前代码证据 | 本次判断与剩余工作 |
|---|---|---|
| WorkItem 唯一基础身份 | Incident/Problem/Change schema 的 work_item_id 必填、唯一；Requested Item 使用必填唯一 ticket_id；intake/service.go:240-248 在同一 tx 创建 base 与 extension | 核心数据模型方向成立。保留现有表名；仍需全入口与 PostgreSQL 约束/初始化证据，不能由 schema 声明推断所有路径已覆盖 |
| 公共事实单一权威 | 三域 schema 已移除标题、状态、租户等重复公共列；专业服务更新 Ticket 公共字段 | 无需重建模型。逐项盘点生产 writer/reader 和 DTO 来源；不能为“统一”再加同步层 |
| 专业动作拥有状态机、共用版本/审计/事务 | incident_commands.go、problem/lifecycle.go、change/commands.go 使用共同 Meta/Result、Ticket CAS 和 RecordTx | 新命令主干成立，但旧 HTTP 分配入口仍并存，见 R7。不能仅凭 A 阶段勾选完成免除入口审计 |
| 关系表达协作，不转换生命周期 | WorkItemRelation 同事务写入及事件；Problem resolve 查询 required fix outcome；结果事件不批量关闭 Incident | 关系权威成果保留；B2 R3–R6 是这一共享能力的运行完整性问题 |
| 公共运行能力使用同一记录 | SLA ResetCycleTx 被 Incident/Problem 重开调用；规范身份叶子包和 BPMN 绑定已改造 | R1/R2 表明身份仍未全入口一致。SLA/评论/附件/审批/时间线仍需 C2 跨域完整旅程证明 |
| 页面使用统一身份、版本和动作投影 | Problem 页使用 workItemId/number；WorkItemCommon 没有 version，WorkItemSLAState 没有周期身份/历史投影字段 | 不能把 Shell 已存在当作前端收敛完成。C2 应补实际消费者，不重复建设已存在的 mapper/DTO |
| 旧路径与结构退役 | 旧关系字段/调用已有删除；deferred/work_item_legacy_relations.sql 留给 C3 | 脚本存在不等于退役完成。需完整旧消费者清单、切换、观察及恢复证据；不得提前执行 DROP |

### 9.3 R7：新增的主线缺口——Incident 分配仍有两套动作语义

已核查实际路由而非仅凭方法名判断：`router/router.go:821` 仍将 `POST /incidents/:id/assign` 接到 `controller/incident_controller.go:718-737`，进而调用 `service/incident_service.go:419-499` 的旧 AssignIncident。请求 DTO `dto/incident_dto.go:91-93` 仅含 assigneeId，没有调用方观察到的 expectedVersion，也没有动作操作键。

旧路径用“服务端刚读到的版本”做 CAS、只更新 assignee，分配活动在写入后另行创建且错误未处理；这能防止本次查询与写入之间的竞争，却不能拒绝用户基于更早页面版本发起的覆盖，也没有新的命令回执原子性。

另一边，BPMN `assign_incident` 走 ApplyIncidentCommand。`service/incident_commands.go:122-128,167-175,230` 的 assign 改为 assigned 状态，要求 ExpectedVersion，并与命令回执在同一事务。两条路径因此不是单纯的 HTTP 与回调适配器，而是有不同副作用和失败条件的动作实现。

此项是静态可证的收敛缺口；本次未增加 stale HTTP 专项动态反例，也不把所有现有 AssignIncident 测试通过当作一致性证明。下一步必须确定并实施唯一业务语义：若“首次分配”和“处理中重分配”确为不同专业动作，应由 Incident 域明确表达并让所有入口一致调用；不能直接把旧入口转发新 assign 后又因同状态拒绝而破坏重分配，也不能保留一个含糊的长期双轨。

二次复核定向运行 `TestIncidentService_AssignIncident_ValidatesAssigneeAndReturnsUpdatedIncident`、`TestAssignIncidentRejectsConcurrentSnapshotChange`、`TestIncidentCommandAcknowledgedStartResolve`，通过（service 包 0.117s）。这些测试分别验证现有路径，尚不验证两条入口语义一致，也不覆盖旧 HTTP 入口的调用方陈旧版本。

### 9.4 修正后的接手方向

**主线应是“完成 WorkItem 收敛”，缺陷修复是各条主线的验收内容。**

1. 建立并核对权威矩阵：每个公共事实的存储位置、专业动作所有者、HTTP/BPMN/规则/Intake 实际入口、旧路径、对应验收。保留已经证明的 A/B 成果，补查未覆盖入口，首先关闭 R7。
2. 完成公共运行能力一致性：将 R1/R2 纳入身份收敛，将 R3–R6 纳入统一通知/授权/重试收敛；不拆成与 WorkItem 无关的无限平台治理任务。
3. 完成 C2：不同专业 ID 与 WorkItem ID 的真实数据贯穿创建、详情、专业动作、关联、评论、附件、审批、SLA 周期、审计/通知。三个已有失败按对应边界归因；Swagger 与真实 API 契约同步。
4. 完成 C3：证明全部旧在线读写退出，完成隔离演练与观察门禁；共享环境变更和物理删除按既定授权规则执行。

上次“先修 R1/R2 再继续、A 阶段只保留”的表述过窄，应以以上顺序替代。修完 R1–R6 不等于重构完成；新状态应表述为：**WorkItem 核心模型和领域命令主干已建立，全部入口及公共运行能力尚未收敛，最终业务旅程与退役验收尚未完成。**
