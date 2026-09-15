# WorkItem 任务分配：实现决策与交接

- 状态：implemented（源码）；共享迁移、部署及真实浏览器验收待执行。
- 日期：2026-09-15。
- 负责人：维护者决定业务与发布范围；Coding Agent 实现；独立审查者复核。
- 证据基线：源码 `a6be9417`，最终业务复审 `be4c01db`，格式整理 `0f10ddec`；[PR #27](https://github.com/Joes9527/itsm/pull/27)。这些是历史验证锚点，不是最新 main 或运行版本声明。
- 权威业务契约：[执行任务绑定当前工单处理人](../superpowers/specs/2026-09-14-work-item-task-assignment-design.md)。本文记录背景、取舍和证据，不维护第二份业务规则。
- 后续执行入口：[现有目录与 Helpdesk 生命周期验收](../superpowers/plans/2026-09-14-catalog-lifecycle-validation.md)。

## 1. 背景与已确认范围

本轮来自上一轮 UI 重构后的真实数据与操作链路补齐。当前优先保证员工建单、Helpdesk 处理和 IT 管理层审批；不重新设计既有 E2E 或增加第二套审批引擎。通用受理型目录由 Helpdesk 统一受理、当前已分配处理人执行、申请人确认；直接审批型目录保留原首节点。SSLVPN 是场景之一，不能把它的角色和外部交付规则硬编码为通用模型。

工单负责人不等于所有流程任务执行人。只有显式绑定的执行节点随工单改派；经理审批、申请人确认、候选领取和外部委派分别遵循既有职责。服务请求评论和附件首批开放范围为本人已分配的服务请求。主管团队可见范围、负载与智能派单不是本轮最高优先级；KB、智能建单由 KAF 后续集成，当前聚焦界面、数据和体验。

## 2. 实现与审查证据

- 任务逐项审查及最终独立复审已完成。最终 I1–I4、M1 已关闭：批量列表读取与一致性、五类入口序列化冲突反馈、执行拒绝原因、迁移默认值校验及终态历史展示。
- 最终后端全量 Go 测试通过。真实隔离 PostgreSQL 先跑 31 个顶层测试，最终审计查询修正后另跑 2 个；两轮覆盖 32 个不同顶层测试，无跳过，不能写成单轮 32 项。
- 前端 231 套件、3,284 测试通过，13 项既有跳过；lint、类型检查与生产构建通过。28 项纯人工夹具预检通过，预检未启动浏览器或调用共享服务。
- `a6be9417` 仅整理测试归属，独立审查确认 2,698 个测试身份保持、无空测试或门禁豁免；远端 Source/Test Coverage Guard 和 Frontend 通过。
- 读取回归验证了独立任务 SQL 分页、绑定任务 128 条分批、授权后精确总数、混排、终态审计及并发快照。绑定任务仍随候选数扫描，未证明生产 SLO。
- 76 个既有迁移资产相对本轮基线未修改。集成最新 main 后必须重新核对迁移序列，不能沿用旧“仅 032 pending”的结论。

这些结果仅证明上述提交和测试环境。后续主干冲突处理产生新的集成树，必须重新验证；不能直接继承本报告的通过结论。

## 3. CI、运行与合并边界

`a6be9417` 的 GitHub 检查显示 Lint、Go Vulnerability Check、Trivy Gate、NPM Security Audit 和 Go Security Scan 失败。前两项安装的工具要求 Go 1.26，而 runner 使用 Go 1.25.12 / GOTOOLCHAIN=local；依赖扫描报告既有 Next.js 15.5.22 critical 及 sharp 等问题。工具安装失败不等于漏洞扫描通过，报告上传成功也不等于安全门禁通过。Go Security Scan 最新运行日志亦在 00:46:17 UTC 收到 runner shutdown，缺少有效扫描结论；该事实来自本次日志核验，不沿用前一次失败原因。

最新拉取的 `origin/main` 为 `14292cb4`，已包含 `c26e57f0`、`69fc7a07`、`88f3f5ef`、`467b94dd` 等 CI/依赖修复。因此旧分支失败不能直接视为主干当前缺口，应保留这些修复并核验集成提交。合并预检存在跨领域冲突：main 已有 workitemmutation 的 operationId/expectedVersion 与 execution scope，旧专业 Update 路径被替换；main 亦已占用迁移编号 032。必须复用主干权威命令并核对迁移顺序，不能盲选任一侧或覆盖既有迁移。集成实现将从未部署的 feature032 改为 047_bpmn_assignment_source，置于受控退休注册项之前；旧032测试/校验和不自动构成047验证证据，运行计划已相应更新。

维护者先确认草稿交付、基础门禁单独处理，随后要求拉取并合入 main。合并请求不代表共享数据库迁移、服务重启或目录发布已经执行。主干集成与基础门禁修复分别记录，必须确认最终集成提交的检查结果。

历史只读预检中 API 仍为 `7ed97de4`，前端 BUILD 为 `wlcY0limNxk71JkwpGrDC`；应用角色无租户作用域时 count=0 不证明空库。运行前重新核实目标环境和迁移账本；详细预检、夹具及回滚边界只维护在验收计划第 7 节。私有凭据、storageState、数据库配置与二进制不进入本报告或 Git。

### 主干集成验证补记

集成树保留 main 的依赖与工具链修复。使用 CI 既有的 `npm ci --legacy-peer-deps` 安装后 audit 为 0，lint、类型检查和生产构建通过。前端完整 CI 测试命令在独占验证时通过：251 个套件、3,427 个测试，13 项既有跳过。此前与 Go 编译并行运行时，同一 PIR 创建用例触发 10 秒超时；限定页面查询的测试修正保留可访问名称、可见性、可操作性与回执/重新读取断言，未放宽超时或关闭用户交互检查。独占全量结果是本次前端测试证据，早先失败记录保留，不能据此宣称所有运行环境均不存在时序敏感性。

集成后的后端完整 `go test -p2 ./... -count=1`、`go build -p2 ./...` 和 gofumpt 检查通过。真实隔离 PostgreSQL 完整一轮 34 个顶层测试通过，无失败、无跳过，覆盖 047 迁移、改派/完成/创建竞争、详情一致快照、MSP 混合编辑、当前撤权、通知偏好、回滚与投递重放。main 原有 82 个迁移资产（81 个 SQL 和 assets.go）逐字节保持不变。工程契约 7/7、API 路径 789/789、源码/测试关联与关联映射测试均通过。

集成复审发现的五项问题已修正并回归：混合编辑的当前改派权限、绑定任务的当前有效授权、详情的一致读取快照、MSP 通知的统一身份与持久偏好、Incident 序列化冲突反馈。测试文件重命名同步修正了现有覆盖映射；没有添加豁免或削弱断言。最终独立复审与远端合并检查以 PR #27 为准；本节不表示运行环境已升级。

PR #27 首轮主干 CI 的静态检查发现未使用的旧辅助声明及 pq 废弃类型别名。后续清理删除无调用方的旧校验/helper，并使用等价的 pqerror.Code；受影响测试的 54 个测试身份与实际断言保持不变，没有添加 lint 豁免。修正后的完整 Go 测试和 CI 同版本 staticcheck 均通过，独立复审通过；远端最终检查仍须针对更新后的提交确认。

另有独立基础门禁记录：main `8f8d34fe` 的 [GA Gate](https://github.com/Joes9527/itsm/actions/runs/34841437201) 在空库初始化时返回 `runtime requires migration 037_work_item_structure_preparation`；main `14292cb4` 的[文档流水线](https://github.com/Joes9527/itsm/actions/runs/34842574381)构建成功，但 GitHub Pages 创建部署返回 404。前者需按既有初始化/迁移阶段契约单独修复并通过组装门禁；后者需核实 Pages 发布配置，不能写成文档构建失败，也不能用它替代应用运行验收。

## 4. 建议继续顺序

1. 完成 main 集成的独立复核和最终提交检查；按既有初始化契约单独解决空库初始化的 GA 门禁，保留主干工具链/安全修复，门禁通过后合并。
2. 按验收计划核实目标、备份与唯一操作人，执行匹配版本迁移/部署后，验证纯人工 A→B 改派、A 失权、B 执行、经理/申请人职责及审计。
3. 在新配置上核验九项通用目录的 Helpdesk 受理和已分配处理人执行；保留专用审批目录的原有流程。新配置只影响新实例，不批量改写历史任务。
4. 补验 Helpdesk 专业解决/关闭及审批驳回的 UI 与持久化反馈，不把流程任务“完成”当作专业工单“解决”。
5. 再补真实待领取任务、专用表单入口、申请人可见进度和处理人姓名；团队负载、KB/智能建单集成维持 backlog。

## 5. 执行裁定档案

以下为本轮 22 项原始裁定，按发生顺序保留。它们解释实现取舍和误判代价，不替代当前权威规范。第 20 项已由第 21 项取代；主干集成后的权限读取遵循当前事务的有效授权，缓存仅限同一读取快照/分块，第 19 项不得解释为允许跨请求缓存继续授权已撤权用户；第 8 项初期 ActorValidator 后续扩展为同时校验 actor/assignee 的 IdentityValidator；第 5 项是当时执行范围，后续发布需记录实际授权和证据。

1. Ruling: User invocation of SDD authorizes execution of the revised design; no repeated execution-choice prompt — explicit user selection — cost if wrong: reversible source work.

2. Ruling: Use existing isolated worktree and stack on ec1901f8 instead of origin/main — required runtime/UI dependencies are not all merged — cost if wrong: later rebase effort.

3. Ruling: Schema and source validation commit is not deployable before all gates — prevent intermediate source policies reaching runtime — cost if wrong: an early deployment must be rolled back.

4. Ruling: Shared writer lives in handlers/common/workitemassignment with injected Outbox enqueue — AGENTS placement and downward dependencies — cost if wrong: interface refactoring.

5. Ruling: Task 7 prepares but does not execute shared migration/catalog publication — explicit spec gate; complete all independent work first — cost if wrong: delayed live acceptance.

6. Ruling: AssigneeID zero is explicit unassignment and professional mixed updates retain one aggregate version increment — preflight found clear-owner and unconditional repository writes — cost if wrong: callers require version-contract rework.

7. Ruling: Reuse existing assignment source vocabulary; if none authoritative exists, validate bounded nonempty provenance strings rather than introduce restrictive dispatch enum — source is audit provenance, Task4 has multiple existing producers — cost if wrong: provenance normalization may require later caller cleanup.

8. Ruling: Writer requires constructor-injected ActorValidator alongside Enqueue — target-scoped RLS cannot validate native MSP identity, so owning service must reuse existing directory/session snapshot policy instead of bypass or trusting ActorTenantID — cost if wrong: constructor/caller wiring rework. Plan and Task3 brief updated; no user-facing policy change.

9. Ruling: Existing SessionSnapshot gets a narrow active-write validation method with private minted actor/tenant/tx binding and callback lifetime — exported fields alone allow fabricated/expired snapshots and cannot prove trusted MSP actor provenance — cost if wrong: authorization helper/caller integration rework; no new session engine.

10. Ruling: Workflow assignment callbacks receive explicit validated execution actor/source from command boundary, never fallback to instance requester/InitiatorID or variables — current executor differs from original requester and audit provenance must be true — cost if wrong: async paths missing trusted recorded actor block until their producer contract is completed; no invented system principal.

11. Ruling: Remove routed escalation hardcoded user IDs1/2/3; use existing escalation-config selector if present, otherwise existing configured assignment policy on escalated WorkItem; no eligible configured target errors/rolls back — tenant-blind IDs violate AGENTS and shared target validation — cost if wrong: deployments without suitable policy cannot escalate until configured; no new routing engine.

12. Ruling: Preserve existing allocated MSP self-assignment; native tenant equality is narrower than authorized target scope, so shared identity validation must cover actor AND assignee through existing directory/allocation policy — AGENTS mandates MSP support and existing endpoint is in Task4 scope — cost if wrong: writer/session/resolver interface rework and additional runtime RLS tests. No blanket foreign-owner allowance. Task5 must integrate same eligibility into effective owner/task authority.

13. Ruling: Task4 includes affected assignment-notification recipient identity lookup through existing directory/allocation policy — otherwise newly supported valid MSP owner persists but durable notification fails or leaks after allocation revocation — cost if wrong: notification dependency wiring/rework; no new transport/engine, fake transport only.

14. Ruling: Persist callback execution provenance in existing ProcessAuditLog atomically with ProcessCallbackOutbox, uniquely correlated by executionKey/outboxID/instance/task — current callback row lacks authoritative actor and Variables/initiator/latest-audit are unsafe — cost if wrong: audit correlation/producer rework; old missing/ambiguous evidence visibly blocks, no data rewrite or new engine/schema.

15. Ruling: Existing shared assign endpoint dispatches through a typed recordClass→professional-owner registry in existing bootstrap, reusing one verified caller transaction and professional lifecycle guards — generic-only guard broke existing UI/MSP and RequestedItem route — cost if wrong: owner-port/bootstrap integration rework; no duplicate professional state machine.

16. Task5 real PG RED: RR assignment snapshot waiting on WorkItem lock misses newly created instance/task and records incomplete affectedTaskIds. Ruling: narrowly fence locked WorkItem physical row during process/task creation, preserving business owner/version/public UpdatedAt, so stale RR writer aborts40001 and retry sees new task — SELECT FOR UPDATE alone does not refresh RR snapshot — cost if wrong: extra WAL/row contention and lock-helper rework; no new table or isolation-policy change. Require both creation order tests and no partial aborted effects.

17. Ruling: Expected SQLSTATE40001 on affected assignment/BPMN boundaries maps to existing HTTP conflict/retry response using typed wrapped-error classification, no invented version values or automatic retry — new serialization protection must not present expected contention as raw internal DB failure — cost if wrong: API client conflict handling/mapper rework; scope remains affected paths.

18. Ruling: Fix three existing navigation-test fixture icon fields to satisfy mandatory frontend type gate without production changes — Task6 cannot claim type-check pass while known fixture errors remain — cost if wrong: minor fixture rework, no sidebar behavior change.

19. Ruling: Existing boolean RBAC predicates and bound-task checked permission API must share one authoritative loader/policy/cache, with checked errors propagated and no caching failed reads — concrete integration regression shows storage failures hidden as empty authorized list; broad caller signature migration is outside this task — cost if wrong: API/caller refactoring; existing boolean callers retain deliberate fail-closed denial and limited diagnostics, not a second permission implementation. Final review to verify boundary.

20. Ruling: Task7 removes duplicate per-task effective-assignment resolution between authorization and DTO projection, but does not introduce cross-task read caching or new repeatable-read list transactions — measured101 bound tasks require1014 queries; broader memoization pins live identity/owner across current separate READ COMMITTED reads and requires a separate consistency design — cost if wrong: residual all-row/N+1 latency remains and may require pre-merge list redesign; final reviewer must triage severity independently, no readiness waiver.

21. Ruling: Final I1 supersedes Task7 narrow-only list optimization: use one read-only repeatable-read response transaction and existing directory snapshot, bounded keyset chunks with per-chunk authority/evidence reuse, exact authorization-before-total/page and pushed-down statistic dates; restore independent SQL pagination only with same-snapshot proof excluding every nonempty source — full review identified unbounded history+N+1 as source-readiness blocker — cost if wrong: read transaction/snapshot contention, query/cache consistency rework and more integration coverage; mutation authority remains fresh, no global owner cache or duplicated policy.

22. Ruling: After clean finalreview, delivery gofmt check found7branch-introduced formatter gaps; apply gofmt-only and prove exact canonical equivalence, without reopening business logic or another review-fix wave — user engineering conventions require formatted touched code — cost if wrong: source/build provenance refresh and mechanical format rework; no intended semantic change. Sameworker handles mechanical gate, fullGo refresh, unchangedfrontend evidence retained.
