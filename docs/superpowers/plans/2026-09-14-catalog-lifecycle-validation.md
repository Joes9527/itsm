# 现有目录与 Helpdesk 生命周期验收

- 日期：2026-09-14
- 状态：accepted；执行中。目录盘点完成，候选路由修复已通过回归，真实解决/关闭路径仍存在 P1 阻塞。
- 初始盘点分支：codex/test/catalog-lifecycle-audit，基于 c3347992；初始盘点仅文档与本地验收。后续候选路由修复及部署见第 6 节。
- 上游：[UI 核心路径计划](2026-09-14-ui-core-journey-recovery.md)。本任务延续 UI 重构，不重设计 E2E、WorkItem 或审批引擎。

## 1. 用户确认的业务分工

2026-09-14 用户明确：通用服务目录由 Helpdesk 统一受理，再由该工单已分配的处理人执行。用户确认步骤保留申请人职责。该确认是业务分工，不是将所有节点指派给同一用户，也不授权批量改写历史任务。

范围优先级：现有目录分配盘点与整改 → Helpdesk 合法解决/关闭及审批驳回验收 → 真实领取、专用表单、受控进度与姓名展示。KB/智能建单及团队负载维持 backlog。

## 2. 只读盘点证据

环境：当前开发 API 8080 / 前端 3001，生产源码 80349336，前端 build wlcY0limNxk71JkwpGrDC。使用当前管理账号所属租户读取，不代表所有租户。完整获取 22 个目录（15 个 enabled/active）、55 个流程定义版本、19 条绑定。目录列表使用 size=100；定义列表使用 Page=1&PageSize=100，并核对实际总数，未把默认第一页当作全量。

| 目录 | 配置与判断 | 后续处理 |
| --- | --- | --- |
| #1 账号申请、#2 权限变更、#3 软件安装、#4 网络接入、#6 数据库申请、#7 安全扫描、#8 代码仓库、#13 新电脑初始化、#14 内存升级更换 | service_request_item，无目录专属流程 key，按该业务类型绑定 service_request_flow。其 1.4.0 的受理、执行、确认、改进节点均未显式指定参与人。#3/#8 不要求审批，但仍有受理/执行节点 | 九项生产目录是首批通用分工整改对象；不更改各自审批要求和专业类型。先修权威参与人解析，再发布配置 |
| #10 SR统一验证-测试服务 | 相同通用绑定，测试目录 | 单独管理测试配置，不作为正式服务承诺 |
| #24 Copilot采购申请 | 专用 copilot_procurement_flow 1.1.0；部门负责人→汇报链总经理→IT总监→许可证执行。执行节点未显式指定处理人 | 保留专用审批顺序，单独核验执行归属与组织关系，不能套用统一受理步骤 |
| #25 SSL-VPN 远程办公访问权限申请 | 专用 sslvpn_approval_flow 1.3.0；dept_manager、network_eng 候选组，后接 KAF | 不改成受理前置；审批组有效成员及外部交付另验，不执行外部授权 |
| #26 SSL-VPN WSL 专项验证 | 专用测试流程固定人员 ID，后接 KAF | 不是生产分配模板；人员有效性和可见范围另行核对 |
| #12 特权账号申请 | targetClass=change_request，经 Change 绑定 | 走 Change 评估/CAB/实施生命周期，不能用普通 Ticket resolved/closed 替代 |
| #23 E2E Incident 测试目录 | targetClass=incident，经 Incident 绑定 | 测试项单列，不能当作 Requested Item 统一整改 |

19 条绑定中，service_request 通用绑定存在不同优先级的同 key 条目；另有 ticket/service_request 子类型绑定。必须依据现有 Resolver 的业务身份选择，不能仅按名称挑选高优先级条目。以上九项是配置推导，#14 的 Activity_Accept 分给申请人另有此前真实创建证据；没有声称逐一创建全部目录。

附带发现：两个启用的云运维流程 cloud_public_ops_flow、cloud_private_ops_flow 的 XML 不能通过标准 XML 解析，需独立核对其实际使用范围；没有将其算作这九项目录的已确认阻塞。可解析的启用定义未发现 formKey 样本，不能宣称真实专用表单入口已验收。

## 3. P1 参与人解析与配置联合问题

源码 `service/bpmn_process_engine.go:1747` 对未指定 assignee 的非审批任务，按 requester_id → triggered_by → assignee_id 回退；即使配置 candidateUsers/candidateGroups，也先把申请人设为 assignee，之后才扩展候选人。因此“增加 Helpdesk 候选组”本身不会使其变成真正待领取任务。

已有授权是 assignee 或合法候选人可匹配。候选 Helpdesk 仍可能完成任务，不能夸大为所有操作均被拒绝；但负责人/队列语义错误，已有 assignee 会使领取不可用。当前普通服务请求节点连候选人也未显式配置，#14 的实测为申请人受理。

后续修复边界：

1. 在既有 createUserTask 内使明确候选配置优先于无配置回退，保留既有租户、参与人、生命周期及命令授权，不新建派单/审批引擎。
2. 通用流程分别表达 Helpdesk 受理、当前 WorkItem 已分配处理人执行、申请人确认。现有 BPMNUserTask 没有已核验的动态 assignee 变量契约，不能把 `${assignee_id}` 当作已支持的配置，更不能写死测试账号 ID。需在既有 BPMN 扩展契约内补齐并审查，取当前业务负责人而非过期创建快照。
3. 没有有效处理人时明确等待分配/人工介入，不静默退回申请人或报告完成。后端应决定可执行动作。
4. 新定义版本只影响新实例；历史运行实例/任务先逐条盘点并制定补救，不批量转派。用户未批准任何历史数据改写。

## 4. P1 普通工单解决/关闭 UI 实测

复用仓库真实认证与页面动作。技能提及的 business-flows/ticket-lifecycle.spec.ts 在当前仓库不存在；现有 ticket-flow.spec.ts 已有编辑状态用例，但会取列表第一条工单。本次采用相同入口、独立标记记录，避免修改已有业务记录。

真实样本 UI-LIFECYCLE 对应 WorkItem #24：end_user 经求助页创建，管理员设置 Helpdesk 分配前置条件，l1_support 打开详情编辑。

| 操作 | 请求与重新读取 | 结论 |
| --- | --- | --- |
| 处理中 | PUT /tickets/24 HTTP 200，重新 GET 为 in_progress | 通过 |
| 已解决 | PUT /tickets/24 HTTP 500/code5001：解决工单时必须填写解决方案；重新 GET 仍 in_progress | P1：编辑表单只有标题、描述、优先级、状态，没有解决方案输入 |
| 已关闭 | 解决步骤阻塞后没有绕过执行 | 未验收，不能报告通过 |

独立源码复核：TicketDetail.tsx 的编辑路径没有 resolution；后端 TicketService.UpdateTicket 强制解决方案。既有 TicketApi.resolveTicket 和 POST /tickets/:id/resolve 已存在，但没有找到已挂载生产页面调用它。批量操作组件不能作为已验证的可达替代入口。

最小修复方向：在原详情交互内收集解决方案，调用现有 resolve API，普通状态选择不得提交不完整解决命令；保留领域校验，将预期参数错误映射为客户端错误而非 500。随后按角色验证 resolved→closed、重开与错误反馈。Requested Item、Incident、Change、Problem 的专业动作仍由所属领域处理，不直接复用普通工单的状态菜单作为其验收结果。

首次 #23 样本因 AntD 虚拟 option 测试选择器不可见而中断，修正为可见下拉内容后 #24 重跑取得上述真实结果。#23/#24 的本次实例均先终止、工单删除后 GET404；账号 #7921–#7924 均停用并读取确认。没有后台补 resolution、改库或伪造流程成功。

## 5. P2 与未完成边界

- 真实领取：优先用修复后的明确候选配置验证，覆盖两个候选人并发领取、领取后刷新和无权账号；此前真实样本未发生领取，不能替代。
- 专用表单：当前 formKey 任务不会显示空参数完成按钮；需先取得真实表单契约/样本，再接入既有表单入口。
- 申请人进度：现有面板仅显示当前账号可见任务，空列表不代表没有流程。需要受控进度投影，不开放全任务权限。
- 姓名展示：当前 assignee 原值可能为 ID。优先由后端输出可显示的参与人信息，避免前端逐条读取全量用户及扩大 user:read 权限。
- 审批驳回及专业闭环未在本次重跑。现有 SR 驳回路径含 notify_rejection，SSLVPN 含外部 KAF，需先核对回调与通知边界。P1 阻塞未修复前，不将三类临时纯人工流程完成扩大为全部目录/专业生命周期通过。

本轮没有生产代码修改、目录发布、任务改派、数据库迁移或外部交付。私有原始证据保留在 ~/.local/state/itsm-kaf-baseline-20260908/evidence/catalog-lifecycle-20260914/；本文件维护可复用验收结论，不提交原始日志/账号凭据。

## 6. 候选路由修复进展

在 `codex/fix/bpmn-human-task-routing`（基于目录盘点分支）修改既有 `createUserTask`：显式 candidateUsers/candidateGroups 的非审批任务保持未分配，交由现有领取命令处理。显式 assignee、审批分配和完全未配置参与人的原有回退不变。候选组无法解析时保持未分配，不退回申请人；本次不增加配置校验或历史修复路径。

新增回归先在原实现复现三项失败，修复后通过；覆盖候选用户、候选组、无有效成员、显式处理人、申请人确认，以及真实服务领取时对非候选人和跨租户账号的拒绝。执行 `go test ./service -run TestFulfillment -count=1`、审批任务相关测试及 `GOMAXPROCS=4 go test -p 2 ./service ./controller ./tests/integration ./tests/rbac` 均通过，独立审查未发现阻塞项。

源码提交 `7ed97de4` 已构建并部署当前开发 API；`/api/v1/readyz` 和前端代理 CSRF 检查均为 200。首次健康检查地址错误与启动就绪等待不足均触发回滚，修正检查后切换成功。保留旧二进制与启动配置备份，迁移和 seed 均关闭；前端构建未改。

真实浏览器验证使用临时纯人工流程：WorkItem #26 经员工目录提交 → Helpdesk 页面出现领取按钮 → PUT claim 成功 → 刷新后领取消失、完成可用 → 完成受理 → 管理层批准 → 审批意见重新读取存在 → 实例到 end/completed。协作评论、附件、窄屏及员工普通求助创建也通过。脚本要求领取按钮真实出现，不能把跳过领取视为通过。

本次 #25/#26 工单均删除并重新读取 404；#25 未结束实例先终止，#26 完成审计保留；临时目录 #31 删除、定义停用，账号 #7925–#7927 停用并重新读取确认。私有验证脚本和证据保留在 `~/.local/state/itsm-kaf-baseline-20260908/evidence/bpmn-routing-20260914/`。

动态当前 WorkItem 处理人解析、九项生产目录配置、解决/关闭 UI 及第 5 节其他边界仍待完成；此次浏览器验证不包括两个候选人的并发领取或外部交付。

## 7. 当前处理人绑定实施与源码验收

2026-09-15 更新：用户确认的“工单 A 改派 B，未结束执行任务随之转交”已在 `codex/feat/work-item-task-assignment` 实施。设计见[已接受的执行任务绑定设计](../specs/2026-09-14-work-item-task-assignment-design.md)。Task 1–7 已分别审查；整分支终审提出 I1–I4 与 M1，本轮集中修复后仍需独立复审。**本计划保持执行中，尚未进行共享迁移、此分支部署或生产目录发布。**

实现使用不可变 `assigneeSource=work_item_assignee`，持续从 WorkItem 当前处理人投影；绑定任务的持久化 assignee/candidate 字段为空。专业权限、当前租户/有效 MSP 分配、WorkItem 行可见范围与 BPMN 能力取交集；终态由精确任务/版本审计固定负责人与实际操作人。改派、工作项版本、审计和 `work_item.assigned` Outbox 同事务写入，事件包含经专业类注册表验证的 `recordClass`，消费时与不可变改派审计核对。

Task 7 修正 migration 032 引入的 bootstrap 注册数量断言（明确校验第 25 项为 032），补充事件类身份契约，并修复绑定任务列表将 RBAC 数据库错误当作普通无权结果的问题。既有布尔权限接口仍为失败关闭返回 false；共用同一读取实现，错误不写入缓存，绑定路径使用可返回错误的接口。终审修复将列表及 DTO/actions 放在同一只读 Repeatable Read 响应快照中；当前身份、工单解析和行可见性只在最多 128 条任务的当前批次复用成功结果，选中页保留已授权负责人/审计投影。目录快照按需打开一次并与业务事务一致；错误不缓存、不返回部分列表。缓存不跨请求，命令事务仍重新校验当前权限和负责人。

### 7.1 已取得证据（2026-09-15）

- 后端 `GOMAXPROCS=4 go test -p 2 ./... -count=1` 全包通过；最终终审修复源码再次执行全包门禁通过（controller 17.040s、service 30.864s、integration 17.878s，exit 0），包含最后的审计 JSON 粗筛修正。PostgreSQL 条件测试另行执行，不能用普通套件未启用的测试替代。
- 隔离 PostgreSQL：`go test -p 2 -tags=integration_postgres ./tests/integration -run 'TestBPMNAssignmentSourceMigration|TestPostgresWorkItemAssignment|TestPostgresBoundLifecycle|TestPostgresAssignmentCaller|TestPostgresAssignmentReview' -count=1 -v`，58.306s，31 个顶层测试通过，无跳过。随后只调整审计批量查询的 JSON 粗筛：避免将不可信 taskId 强转 PostgreSQL int；使用 JSON 包含/规范字符串比较，仍由既有精确证据匹配器决定终态归属。新增真实 PG 反例先复现 22P02，最终针对 `TestPostgresBoundLifecycleTerminalBatchAuditMetadata|TestPostgresBoundLifecycleReadSnapshot` 再验证，2 个顶层测试通过、3.375s、无跳过；合法旧负责人不随当前工单改派变化，畸形审计被忽略。仅使用自有容器 `codex-workitem-assignment-test-20260914` / 端口 36444 / 数据库 `sslvpn_test`，凭据保存在私有本地配置；每例使用独立 schema，生命周期/并发与 MSP 正向测试使用非超级用户、无 BYPASSRLS 的普通运行角色。受限目录能力用于既有身份验证；无共享数据库或真实外部发送。
- 同一 PostgreSQL 并发夹具覆盖 user-task callback 与 service-task callback 两种推进路径、改派先/推进先两个顺序。验证阻塞、可显式重试的 40001、重试后新任务进入改派审计、无死锁/无中间改派事件、终态负责人不漂移。回调为测试内纯本地效果。
- 事件契约先复现缺失 `recordClass`；真实数据库覆盖持久化字段和伪造合法类与原审计不一致时阻止投递。删除消费端类校验的反事实运行实际失败；恢复后通过。
- 前端 `npm test -- --runInBand --watch=false`：231 suites、3284 tests 通过，13 项既有 skipped；384.561s。`npm run type-check`、`npm run lint:check`、`npm run build` 通过。lint 保留 `BPMNDesigner.tsx` 的一项既有 unused-disable warning，未执行自动修复。全量 Jest 仍有既有 Ant Design Alert message→title 等 deprecation console.error 和 React act 更新提示；本次 231 suites 通过不代表零控制台告警。原始日志保留，UI 告警整治另行跟踪，不在本轮加 blanket console suppression 或修改无关组件。
- 后端 API 与 migration CLI 使用 `-buildvcs=false -p 2` 构建，避免包围 checkout 的错误 Git 元数据；精确提交/tree/文件 SHA256 以 manifest 为准。前端 standalone 已刷新，BUILD_ID `SmgybwvWwNlbdezRXwLLB`，归档排除 .env/.env.*/.npmrc。构建物、SHA256 清单、源码清单、migration 032 只读 preflight SQL 位于工作树的已忽略 `.superpowers/artifacts/work-item-assignment/`，不提交二进制/日志/凭据，不依赖后续可删除的 SDD scratch。
- 对 `ec1901f8` 的 76 个既有 migration 文件逐字节比较无改动；migration 注册表只新增 032，旧 SQL 分支不变。032 SQL SHA256：`cd4ecbe146e7fb1173b8e1d50fb2805e44bdd28a0b59214cbb9f5f8b3dec9343`；verify SQL：`10e3c5a71ae639408e444e955f3dd76e2ab0745c57062a39363f3dc5edd55f6a`。修复 worker 未连接共享 ledger；控制器只读对账见下文，不能把源码比较或只读对账说成数据库迁移完成。

终审 I1 的旧“711 次查询、全量物化可留待优化”处置已被替代。任务按 createdTime/id 键集以 128 条分批扫描；参与实例按任务 id 分批，终态审计也分批读取并只保留每任务最多两条有效证据（两条即明确歧义）。混合/绑定路径先授权再累计精确 total，只保存请求页；统计先应用 SQL 日期条件再流式累计。仅当同一 RR 快照证明匹配候选没有任何非空 assigneeSource（包含未知值）时，actor-free/elevated 独立列表使用 SQL Count 和 Offset/Limit；没有独立事务探测后回退的竞争窗口。

新的长期测试证据：独立 1000 条、页大小 10，仅物化 10 条、3 次 Ent 查询；同一实例绑定 301 条、页大小 10，单批最多 128 条、19 次查询（授权仍处理全部 301 候选），返回仅 10 条。混合 270 条及不可见记录覆盖跨批次排序/总数、后续批次错误丢弃已选页、统计排除日期外损坏关联；270 条终态任务/271 条审计证据按最多 128 条读取，页 DTO 复用选中证据，歧义显示 unavailable。真实 PostgreSQL 证明独立路径探测后并发新增绑定任务、本已绑定列表并发改派均保持本次 total/page/DTO 一致，下次请求可见变更；有效/撤销 MSP 读取与目录快照一次打开均覆盖。此处为有限夹具的查询/物化证据，不是生产延迟/SLO 保证。绑定/参与扫描仍是 O(候选数)；内存为 O(批大小 + 输出页/输出实例ID/统计维度)，不同工单仍需各自现行权限解析，长响应会持有 RR 快照，后续可据真实负载评估 set-based 查询。

I2 的 MSP 分配、智能分配、接单、升级和子任务更新均在控制器泛化错误前映射包装的 SQLSTATE 40001 为 409、retryable=true，不暴露数据库细节、不伪造 currentVersion、不自动重试写入。I3 由后端动作投影给出安全的执行拒绝原因，页面不复制授权规则；已分配可读但不可执行及完整权限正例均覆盖。M1 增加 completed/cancelled 文案，已授权终态缺失/歧义证据仍可见为“历史处理人记录不可用”，不回退当前负责人。I4 的 032 verifier 使用 IS DISTINCT FROM 拒绝缺失默认值；真实 PG 覆盖 DROP DEFAULT、错误默认值、去掉 NOT NULL、恢复后的 legacy insert 及不可变约束。

控制器本轮只读共享预检（未由修复 worker 执行）：API 8080 readyz、前端 3001 login 均 200；API 仍为 7ed97de4，前端仍旧 BUILD wlcY0limNxk71JkwpGrDC。配置目标为 5432 的 itsm_config_baseline_20260908/public，24 项已应用 canonical checksum 一致，仅 032 pending，auto-migrate/seed=false。未执行共享写入、重启或外部发送；应用角色无作用域 count=0 不是空库证明。相关只读证据位于保留 artifacts/evidence/shared*20260915*；该预检不能替代下面的备份/窗口/目标进程所有权和实际运行授权。


### 7.2 准备好的浏览器夹具与明确未执行项

长期测试位于 `itsm-frontend/tests/e2e/flows/work-item-assignment.spec.ts`，配套纯人工定义在 `tests/e2e/fixtures/work-item-assignment.bpmn`：Helpdesk 候选组 → WorkItem 绑定执行 → 独立经理候选组 → 申请人确认。经理节点用于确认职责独立，并非代替专门审批决策流程的验收。Go contract 使用真实 BPMN parser 校验定义没有 service/script/call/subprocess 或委托 handler。Playwright `--list`、类型和 lint 已通过；独立 preflight 测试使用注入的只读响应和 POST 计数器，在无浏览器/网络/共享服务环境下验证错误目录、定义身份、模糊分页及 script/business-rule/subprocess 等 XML 不匹配均阻止 POST；**未运行浏览器，不宣称其运行成功**。

部署获批后，由既有用户/组/角色/流程/目录 API 建立带独立标记的临时定义与 Requested Item 目录，为本次验收生成唯一 `work_item_assignment_acceptance_<唯一标记>` key，不修改现有九项生产目录。当前 intake 对目录的非空 processDefinitionKey 按 active 定义的 deployed_at DESC、id DESC 选择，latest 或 GET 的 version 参数不能固定 intake 版本。因此必须确保目录 processDefinitionKey 非空且等于该 owned key，该 key 恰好只有一条 active 定义；测试读取 Key/IsActive 过滤列表，校验完整分页 total=1/data=1，以及预先批准的 definitionId、definitionVersion、tenantId、deploymentId 和仓库纯人工 XML 的逐字节内容/SHA256。任何未解析、不同或多条 active 记录都在 intake POST 前终止。由唯一执行人冻结目录及该 key 下所有定义直至运行结束，期间不发布、编辑、激活或停用；`configurationFrozen=true` 是受控运行的前提声明，不是 API 锁，也不构成原子选择保证。Helpdesk/经理组名分别为 `assignment_acceptance_helpdesk`、`assignment_acceptance_manager`；申请人、Helpdesk、A、B、经理必须为五个不同有效账号。A/B 只赋实际需要的专业 read/provision 与 BPMN read/update 权限，不能用全局管理员身份替代 A 失权证据。Helpdesk 需具备现行合法分配、读流程/工单及清理权限；不静默授予额外权限。仅允许 in-app 或 fake 通知 transport，无真实邮件/Graph/KAF/VPN 调用。

`WORK_ITEM_ASSIGNMENT_FIXTURE` 指向权限 0600 的私有 JSON，包含 `baseURL`、`tenantId`、`catalogId`、`definitionKey`、`definitionId`、`definitionVersion`、`definitionSha256`、`configurationFrozen=true`、`actors` 和 `states`；后二者以 `requester/helpdesk/a/b/manager` 为键，值分别为实际用户 ID 和经真实登录取得的 storageState 文件路径。`PLAYWRIGHT_BASE_URL` 必须等于 fixture.baseURL。不存在 fixture 时测试明确失败，不跳过为通过。运行命令：

```sh
PLAYWRIGHT_EXTERNAL_SERVER=1 PLAYWRIGHT_SKIP_CHANNELS=1 \
  npx playwright test tests/e2e/flows/work-item-assignment.spec.ts --project=chromium --workers=1
```

测试通过现有 intake 提交、Helpdesk 领取、授权改派、A/B 页面完成入口和直接命令、经理/申请人独立任务及终态历史投影执行。成功记录经删除 API 清理并读取 404；失败记录保留供审计调查，不能用数据库强制改状态。运行负责人另需在删除前/按事件 ID 以只读方式核对 WorkItem、process_tasks、ticket_workflow_records、process_audit_logs、outbox_events 的持久化字段与投递结果，验证没有外部交付；之后停用/删除本次自有账号、组、目录和定义，保留历史审计。完整生产审批、MSP 会话切换和九项目录发布均未由此纯人工夹具代替。

### 7.3 待批准的共享环境门禁

以下共享操作尚未执行。主干集成将未部署的 feature 032 重编号为 **047_bpmn_assignment_source**，注册在既有受控退休阶段之前；第 7.1 节的 032 校验和与“仅032待执行”仅为旧分支历史证据。main 还有其他迁移和受控阶段，不能把旧 `-up` 方案直接用于集成版本。Task 7 原材料须重新生成并复核：

1. 独立终审通过后，锁定该分支确切提交及构建 manifest；确认目标仍为开发 API 8080 / 前端 3001 所对应环境、进程/容器所有者、备份、迁移唯一操作人和维护窗口。目标若变化，先更新部署记录。不得以读取旧健康端点推断新源码已运行。
2. 使用预备的**只读** SQL 确认数据库/schema、migration ledger 连续前缀与已部署旧 checksum、任务字段、活动事务及现有流程；核对全部待执行项、阶段依赖和已部署校验和，并审查已有 assignee_source/候选冲突；不能假定仅 047 待执行。`itsm-migrate -status/-dry-run` 也会调用 EnsureMigrationsTable，不能当作无写入的预检工具。
3. 获得共享迁移/部署授权后，停止相关写入与工作进程，备份并使用现有 migration runner 执行经确认的迁移序列，随后执行 047 verify。若还包含结构准备、业务验收或受控退休阶段，必须遵循 main 对应契约分别满足前置条件；禁止未经核实直接批量 `-up`。047 增列/约束/触发器可能等待 process_tasks DDL 锁；不得清洗历史实例绕过约束。启动 API 时保持 `ITSM_AUTO_MIGRATE=false`、`ITSM_AUTO_SEED=false`，部署匹配前端，验证 readyz、代理 CSRF 与源码/build ID。
4. 迁移与匹配版本就绪后，再授权纯人工临时夹具配置/浏览器运行及上述只读审计检查。生产目录新版本发布另行批准，且只影响新实例；不重写旧任务。
5. 回滚界限：尚未产生任何依赖新运行时的分配记录/审计格式或 `work_item.assigned` 事件（包括没有绑定任务的普通 WorkItem 改派）时，才可停止新版本并在核实旧版本可读附加列后回退二进制/前端，保留 047；不自动降 schema。已有上述新格式记录、绑定任务或事件后，旧二进制不了解动态负责人及新审计契约，禁止直接回退继续处理；须暂停流量和 worker，以审查后的向前修复或完整备份恢复方案处理。047 无自动 down SQL，不删除绑定字段/触发器/审计换取旧代码运行。

第 4 节解决/关闭 UI、第 5 节其余边界未在 Task 7 解决。源码测试/构建通过不等于目录整改或共享上线验收完成。

### 7.4 源码交付与后续集成

2026-09-15：本轮源码与独立复审完成，`a6be9417` 已推送 PR #27，远端测试关联和前端检查通过；基础 CI/安全门禁仍失败。维护者先选择草稿交付、基础门禁单独处理，随后要求拉取并合入 main。主干冲突处理与验证尚不能用旧分支测试替代，共享迁移、运行切换及第 7.2 节浏览器验收仍未执行。

后续 Agent 先读[实现决策、验证范围与继续顺序](../../review/2026-09-15-work-item-task-assignment-report.md)，再执行本节运行门禁。任何最新 main 合入后均重新确认迁移序列和构建来源，旧预检的“仅 032 pending”不能自动沿用。
