# UI 核心路径复核与后续修复顺序

- 日期：2026-09-14
- 状态：accepted（用户确认复核既有实现并调整本轮 UI 计划；下列未勾选项尚未实施）
- 核查基线：`f121a2f3`，生产代码继承 `4f5d1a8a`；文档分支 `codex/docs/ui-core-journey-recheck`。
- 上游：[UI 工作台补齐设计](../specs/2026-09-14-ui-workbench-completion-design.md)。本文是后续顺序的当前来源，取代先启动主管 2B 的建议。

## 1. 范围与决策背景

本次主题是上一轮 UI 重构的完成度与回归修复。用户明确优先关注 end user 建单、Helpdesk 处理工单、IT 管理层审批。团队可见范围、负载统计和主管运营页面仍有价值，但推迟；不因为主管页面有原型就扩展为团队领域建设。

已有 E2E 设计、实现和验收，不能按缺失重新建设。保留现有 WorkItem/统一 Intake、专业生命周期和 BPMN 审批权威边界；只修 UI 遗留、已证实的操作问题，并复用现有测试。IT 管理层审批与主管负载看板不是同一能力。

## 2. 证据与分类

以下源码路径相对仓库根目录。源码存在、历史验收、本轮测试与当前部署可用性分别判断。

| 路径 | 已有实现与历史证据 | 本次结论 |
| --- | --- | --- |
| 门户与目录建单 | `itsm-frontend/src/app/(main)/portal/page.tsx` 读取发布目录；`service-catalog/request/[id]/page.tsx` 经 ServiceCatalogApi 提交 `/service-requests`，携带确认版本及字段，按 receipt.workItemId 导航；后端 `handlers/service_request/handler.go` 进入统一 Intake。`e9d5f52c` 修门户真实数据，`25306d3f` 统一创建，`6f106fd2` 保留重载表单答案 | 已实现，应保护和回归；不是新建 E2E 项目 |
| 门户搜索 | `itsm-frontend/src/components/portal/HeroSearchBar.tsx:47` 使用 GET knowledge/search，但 `itsm-backend/router/router.go` 注册 POST，handler 要求 JSON query；同组件固定 VPN/Copilot 目录、92% 匹配度和本地 setState 的“已记录自愈成功” | `b838464b` 初次角色 UI 接入的未完成实现，此后未改；是本轮 UI 完成度问题，不是后来后端修改造成的回归。按用户后续确认移除演示行为，知识能力转入 KAF backlog |
| 搜索失败后的建单可达性 | HeroSearchBar 的人工提单入口依赖有搜索结果；读取失败只有 console.error，未隔离迟到响应 | 搜索失败可隐藏该入口，但门户目录卡片仍可提单；不能夸大为全站无法建单 |
| Helpdesk 列表与详情 | 原 `/tickets`、TicketList、TicketDetail 仍在；分配、评论、附件、编辑调用真实 API。详情状态编辑经 PUT tickets/:id；TicketService.UpdateTicket 校验流转及解决方案，拒绝以普通更新推进审批 | 不能因为缺独立 resolve 按钮就断言没有处理能力。前端 isValidTransition 从 `f1e00efa` 已存在，不能归因于当前工作台重构；专业流转的实际操作仍需按场景验证 |
| 工程师角色入口 | `/workspace/tickets` 的原型来自 `b838464b`；2A 已替换为本人真实分页队列并复用 TicketDetail | 已完成的 UI 接入，保留 2A；历史验收覆盖队列、评论、转派后移除与 guest 拒绝，不代表所有专业生命周期均已浏览器验收 |
| 管理层审批 | `components/portal/ManagerPendingApprovals.tsx`、`app/(main)/approvals/page.tsx` → BpmnWorkflowApi list/claim/decisions → BPMN 控制器和引擎；SSLVPN 定义两级审批节点 | 已实现。两处 UI 相对 `30cfbce1` 无差异，未证实本轮工作台重构破坏审批 |
| 审批列表完整性 | 审批中心先取所有状态前 100 条再筛选待办，可能遗漏较早待办；未像门户组件按 taskPurpose=approval 过滤。门户最多四项且无完整列表入口 | 既有问题，非当前重构回归；待办完整性属于核心任务相关后续修复。更宽任务读取授权不能被 UI 当作“全是待我审批” |
| 团队工作台、关系写入 | 主管 2B 仍是盘点草稿；1B 尚无已满足的主线/部署依赖 | 2B 暂缓；1B 保留已登记依赖，不借本次 UI 修复增建领域契约 |

既有跨系统设计与验收入口：

- [2026-08-24 SSLVPN E2E 计划](2026-08-24-sslvpn-approval-e2e-verification.md)与[验收报告](../../archive/testing-reports/2026-08-24-sslvpn-approval-e2e-verification-report.md)。
- [2026-09-05 SSLVPN 实施计划](2026-09-05-sslvpn-end-to-end-implementation.md)与[最终状态及分段验收记录](../../review/2026-09-05-sslvpn-end-to-end-verification-report.md)。报告包含真实 provider/worker 验收，但其分段浏览器验证不能描述为一场连续浏览器测试，也不能证明当前部署仍健康。
- 维护用例：`itsm-frontend/tests/e2e/sslvpn-approval-flow.spec.ts`、`flows/sslvpn-kaf-intake.spec.ts`、`flows/sslvpn-postcheck.spec.ts`、`ticket-flow.spec.ts`，以及 1A/2A 已交付用例。复用对应场景，避免重复建立审批或创建机制。

## 3. 按依赖顺序执行，每项完成后审查

- [x] 1A：附件、读取状态、分配搜索恢复，见[原验收](2026-09-14-ui-workbench-1a-recovery.md)。PR #19 尚不等同已合并。
- [x] 2A：工程师真实队列与详情复用，见[原验收](2026-09-14-engineer-workspace-real-data.md)。PR #20 依赖 #19；合并时先 #19，再调整 #20 base 并检查差异。
- [x] 核对建单、处理、审批已有设计和实现，修订优先级。
- [x] 当前项：保留门户布局，搜索位置明确显示暂未开放；“提交问题 / 寻求帮助”与“申请服务”始终可达，通用求助通过 `/tickets/create?entry=help` 进入现有普通工单表单，跳过专业类型选择，分类/模板可选，由 Helpdesk 后续分类；此入口隐藏已有 AI 分类卡片。移除硬编码推荐、固定匹配度、虚假自愈成功和本轮尝试的知识查询接入。沿用真实目录、表单和近期请求，聚焦可访问性、状态反馈与移动端操作。KB 与智能建单均转入下述 KAF 集成 backlog。
- [x] 然后：核验并修复审批中心待办遗漏和审批/普通任务展示边界，沿用已有分页、任务类型、claim/decision 契约；不把所有 user task 改成审批任务，不扩大角色授权。为门户增加明确的完整审批列表入口。测试超过 100 条及混合状态、非审批任务、认领、驳回理由、失败重试和后端拒绝。后端字段若不足，先记录具体契约阻塞，不前端猜测。
- [ ] 最后：在上述 UI 修复版本复用三角色真实路径验收。end user 从门户进入目录并持久化建单；实际 Helpdesk 账号打开可见工单、处理/评论/附件并重新读取；被配置的审批人在既有流程完成认领与决策并核对历史。按工单类别验证合法处理动作，不将审批绕到普通状态更新。发现的问题先分类，再补直接阻塞 UI 操作的修复。

每个实现交付使用独立可审查变更和相邻行为测试。聚焦回归之后运行相称的类型、静态、构建及契约门禁，完成独立审查再推进下一项。初次复核仅更新文档；后续实现与验证另记于下文，不宣称未执行项通过。

## 4. 环境、验证与完成边界

用户已授权当前开发环境进行真实浏览器验收。后续使用独立前端端口与标记测试记录，记录前后端实际版本；不更换共享 API、不迁移/重置数据库，不触发无关外部授权或消息。复用外部 SSLVPN 测试前核对其副作用和清理边界，不能把历史测试直接无差别重跑。普通 IT 管理账号、菜单和行权限需实际核验，管理员成功不能替代。

本轮源码/历史复核无共享 API 写入。新运行窄测试：门户与目录 API 3 suites / 34 tests；审批组件、审批中心、BPMN API 和 persona 4 suites / 21 tests 均通过。首次误用 Jest 复数筛选参数启动的扩大运行已中止，不作为验收证据；该运行中申请页 15 tests PASS 仅作观察。存在 standalone 模块名碰撞和 AntD 弃用提示。Helpdesk 的 TicketDetail、workflow-state-machine、useAssignedTickets 窄测试 3 suites / 44 tests 通过，退出码 0。本轮三个定向运行合计 10 suites / 99 tests 通过；不代表全局覆盖率门禁。

未重新执行浏览器或后端流程测试；历史验收不能证明当前 API 8080 源码版本、所有角色或所有专业流程。未运行全量构建，因为本次变更仅文档；1A/2A 的完整门禁仍以各自记录为准。

## 5. KAF 集成 backlog（2026-09-14 用户确认）

- 状态：backlog，暂不实施；不影响上述核心 UI 交付。
- KB / 知识搜索由 KAF 提供。本轮不建设 ITSM 自有搜索、推荐、自愈反馈或检索引擎，也不接入现有知识查询作为临时替代。门户预留原位置并诚实显示未开放；已有其他页面的知识功能不在本次变更范围。
- 智能建单由 KAF 提供。本轮不深入意图识别、智能填表、自动分类等场景，保留手工求助及目录申请。后续复用已有 KAF → 统一 Intake 设计与验收资产。
- 集成准入时再核验 KAF 的真实契约、身份/租户/可见范围、请求上下文、错误反馈、结果来源与审计。知识检索需核验检索前与输出前权限过滤；建单副作用继续通过既有 Intake/领域流程，不新建审批或状态机。
- 集成验收包括真实知识结果、空/失败状态、跨身份结果隔离，以及用户确认建单、唯一 WorkItem 回执与重试语义。本轮不实现这些能力，不展示模拟结果或伪造成功。

产品约束：用户认可现有员工门户布局；参考 Freshservice/HaloITSM/Jira Service Management 的入口与反馈实践，保持搜索与提单并列，不强制先搜索，也不要求员工先掌握 ITSM 分类。

### 门户入口修订验证

- 分支：`codex/fix/portal-search-entry`，基于文档提交 `49b022a9`。仅修改门户、现有建单页入口模式、相邻测试与浏览器导航用例；无后端/API/权限策略变更。
- HeroSearchBar 不再调用 KB 或 AI；保留搜索区域的未开放说明及两条真实导航。求助模式只显式选择已有普通工单目标，不推断专业分类、不改变创建回执或后端授权。
- TDD：不可用搜索状态和直接求助表单用例先失败；修复后 4 suites / 19 tests 通过，包含无分类/模板时提交标题与描述、普通工单 payload 与 WorkItem 回执跳转。类型检查通过；lint 无错误，保留 BPMNDesigner 既有未使用 eslint-disable 警告；最终生产构建通过。
- 独立审查提出求助入口仍要求专业类型选择，已修复并复审，无剩余发现。
- Chromium 导航用例 `tests/e2e/flows/portal-help-entry.spec.ts` 1 passed：管理员登录当前 API，390/1440 宽度无横向溢出，键盘进入普通工单表单、AI 卡片不显示、目录跳转正常，无 pageerror/知识搜索请求。检查了移动端截图。此验证不替代 end user/Helpdesk/审批三角色真实流程验收。
- 浏览器服务为本分支最终生产构建，私有 3016 前端、3017 临时同源代理到现有 8080 API；未替换共享服务。仅登录和读取/导航，无建单、审批、KB/KAF 调用或数据库迁移。浏览器完成后停止本次私有服务。截图与日志留系统临时目录，不提交。

### 审批待办 UI 修订（implemented）

- 分支：`codex/fix/approval-pending-ui`，基于 `47b1b8f5`。沿用已有审批中心和门户布局、BPMN list/claim/decisions；不新增审批引擎、后端接口、权限或数据模型，不包含 KB、智能建单或团队看板。
- 后端仅支持单状态过滤。共享 UI 查询依次读取各状态内全部分页，状态间独立查询；中心仅呈现明确 taskPurpose=approval 的活动任务，门户保持最多四条预览并链接完整审批中心。任务范围由后端决定，标题不将管理员的授权范围误称为全部“待我审批”，也不把四项预览当作待办总数。
- 成功决策后重新读取真实列表并补齐预览。领取/决策错误保留可恢复操作；拒绝须有意见；提交期间防重复和误关闭。初次读取失败不显示零计数/空态，刷新故障保留数据并标明失败、禁用写入，权限拒绝清数据/计数/弹窗。切换身份/租户/权限、卸载及旧请求不能恢复过期结果或在认证重试时换会话提交。
- 独立审查的两项问题已修复并复审：并行请求中晚到的 403 不被早到的网络错误掩盖；末页为空、重复或长度不完整不能报告完整成功。相应回归先失败、后通过，另覆盖旧请求的 403 不清除较新结果。
- 分页列表是刷新时的读取结果，不承诺跨状态查询的数据库原子快照；分页异常显式报错并允许重试，不截断或以部分总数冒充完整结果。
- 验证分开记录：真实开发 API 仅登录与读取；浏览器的领取/拒绝/批准测试使用拦截响应，禁止向共享真实审批任务发送这些命令。该 UI 验证不能代替下一个三角色真实业务流程验收。


验证环境记录：本分支最终 Next.js production build ID 为 `TylCbDLpAzlT0CwuWFLEy`；8080 API 二进制仍为 `/home/administrator/.local/state/itsm-kaf-baseline-20260908/bin/itsm-api-support-handoff-20260911`，SHA256 `3fa2ab3ef156ddd435b8cf8a378dd68d529a754ab727cc7e144ec2287d811fe2`。后端源码 SHA 未核验，不将运行指纹等同当前源码。私有 3016/3017 服务已停止并确认端口关闭。

浏览器最终结果：`tests/e2e/flows/approval-center-ui.spec.ts` Chromium 2 passed，分别覆盖真实 API 只读（当前管理员返回 0 项审批）与隔离任务响应的分页/操作/补位；390/1440 无 body 横向溢出，截图留临时目录。首次只读断言误匹配 Next.js 空白 route announcer，已限定审批内容区域并重跑通过。领取、拒绝、批准均由 Playwright 响应拦截完成，不能据此声称后端真实审批流转已验收。

测试环境问题与修正：首次全量测试扫描正在生成的 `.next/standalone/package.json`，在执行用例前失败；后续命令通过 `--modulePathIgnorePatterns=<rootDir>/.next/` 排除构建产物，未改变源码测试集合/覆盖率阈值。全量运行发现一处旧门户测试只模拟用户名、没有认证/租户状态，导致第二个重试按钮；已改为真实 auth store 的完整登录 fixture，4 项定向测试通过，最终全量 225 suites / 3209 tests 通过，13 项既有 skip，退出码 0；覆盖率 Statements 80.67%、Branches 66.76%、Functions 82.5%、Lines 82.13%，仓库门槛通过。


交付门禁：类型检查及最终生产构建通过；lint 无错误，仅 BPMNDesigner 既有未使用 eslint-disable 警告。独立复审通过，`git diff --check` 通过。最后修订仅补齐门户测试 fixture，未改变已构建和浏览器验证的生产代码。全量命令为 `npm run test:ci -- --runInBand --forceExit --reporters=default --modulePathIgnorePatterns=<rootDir>/.next/`。后续仍为三角色真实路径验收，当前结果不代表该项或整体 UI 补齐计划已经完成。


### 三角色真实路径验收（2026-09-14，存在阻塞）

本轮前端/审查工作树为 `codex/test/ui-core-journey-acceptance`，HEAD `c4b35ab7`；后端使用既有 8080 部署，运行源码 SHA 未确认。结论是**部分路径通过，三角色业务验收未通过**；上方最后一项继续保持未勾选。未修改生产代码、角色权限、目录绑定或 BPMN 定义。先检查当前部署定义，再使用现有目录 #14「内存升级更换」；没有执行 SSLVPN、KAF 授权、资源交付或真实审批决策。

#### 实测结果和问题归属

| 场景 | 本轮真实证据 | 结论与下一步 |
| --- | --- | --- |
| end user 普通求助 | 两次以新建 `end_user` 登录，从门户按钮进入 `/tickets/create?entry=help`，填写标题/描述后 POST `/tickets` 均返回 HTTP 403、code 2003、`PermissionDenied`、`permission denied for ticket:write`。真实会话有 `ticket:read/create/update`，无 `ticket:write` | **P1 核心入口阻塞**。当前统一 Intake 授权与角色/路由的操作名不一致；不是入口不可达，也没有创建成功。`authorization/work_item_creation.go` 对目标专业资源统一要求 write/read；修复须对齐后端权威授权及角色契约，不能在 UI 绕过或为验收给普通用户增加宽权限。尚不能把运行二进制对应源码提交认定为本轮 UI 回归 |
| end user 目录申请 | 门户「申请服务」导航正常，随后直接进入现有目录 #14 申请页，提交真实表单 HTTP 201、code 0。最终运行回执 WorkItem #12 / ServiceRequest #6，`recordClass=service_request_item`、`workflowStartStatus=pending`。跳转 `/tickets/12` 并刷新后仍显示真实标题 | **创建、回执跳转与详情回读通过**。目录列表选卡到申请页的完整点击未覆盖；当前只验证目录导航和指定目录表单。早一次 #11 / SR #5 的创建同样成功，详情断言因标题含编号而失败；截图确认真实详情存在，修正断言后的 #12 路径通过 |
| 实际 Helpdesk 处理 | 将仅本轮 #12 通过管理员分配 API 分配给临时 `l1_support`（这是测试准备，不算 UI 分配通过）。该账号在 `/workspace/tickets` 真实本人队列打开 #12 成功；输入并发送评论后 POST 返回非成功响应，界面显示「权限不足」 | **P1 协作操作阻塞**。该角色有 `service_request:read/provision` 和 `ticket:create/update`，无 `service_request:create/write`；当前共享评论路由对 Requested Item 要求 `service_request:create`，并未像 incident/problem/change 一样把 create/update 映射为 write。需要先确定共享协作动作策略，再对齐动作映射及角色定义；单补 write 或仅改变映射均不足。该次只保存了非成功断言及「权限不足」截图，没有保存评论响应的精确 HTTP 状态/原始响应体，不能将源码推断当作已捕获回执。不能从 ops_engineer 对普通工单的历史成功推导 l1_support 对 Requested Item 同样可操作。附件、状态处理和响应式步骤因前置评论失败未执行，不记通过 |
| 管理层审批 | 临时 `dept_manager` 登录 `/approvals`，页面及任务 API HTTP 200 正常。目标 #12 实际为 `service_request_flow` 1.4.0，实例 #12 running，当前 `Activity_Accept`，任务 #16「请求受理」created、taskPurpose 为空、assignee 为申请人 #7903；实例 `approval_required=true` 且配置有部门主管/IT审批链 | **审批页面加载及任务列表读取通过，真实认领/决策未覆盖**。目标尚未进入审批节点，审批中心不应把这个普通任务展示为审批。当前源码没有 UI 调用 `BPMNWorkflowApi.completeTask`；这不代表后端没有任务命令。先核验现有受理入口/流程配置与处理人，不改 taskPurpose，不直接写状态假装推进，不新建测试审批引擎 |
| 详情流程说明 | 在 #12 的 running 受理阶段，右侧显示「该工单未走审批流程」。`ApprovalMiniStepper` 等组件仅查询审批决策历史，空记录（或部分组件读取失败）即显示此文案 | **P2 UI 信息错误**。该次页面的决策接口原始响应未单独保存，尚不能区分正常空数组和读取失败。审批决策历史为空不能证明没有流程，应明确「暂无审批记录」，读取失败应独立展示；历史展示不能冒充当前完整流程进度。归属既有 UI/契约消费问题，后续修复仍在本轮 UI 范围内 |

修复顺序据实补充：先对齐普通求助创建和 Requested Item 协作的后端权限契约；再修详情的审批记录空态/错误态；核实既有受理路径后重跑真实审批。三项使用既有 Intake、共享 WorkItem 协作和 BPMN 权威边界，不引入新 E2E 业务模型。KB/智能建单仍为 KAF backlog，团队负载继续暂缓。因运行后端源码 SHA 未确定且不更换共享 API，本轮不将源码修改或测试角色提权冒充线上问题已解决。

#### 环境和清理

- 前端复用前述最终 production build `TylCbDLpAzlT0CwuWFLEy`，私有 3016/3017 指向既有 8080。真实 Chromium 操作使用仓库 `auth-utils.ts`，未拦截业务 API；一次性脚本、截图和脱敏步骤记录保留在 `/tmp/core-ui-*`，不提交临时浏览器脚本。此轮是探索验收，不报告为自动化套件全部通过。
- 初次随机密码未满足已有复杂度规则，创建用户失败；修正为随机值加必需字符类型后成功。另一次试跑把按钮误写为 link，修正选择器后再跑；这两项是验收脚本问题，不是产品缺陷。
- 三次实际账号组 #7897–#7905 全部通过状态 API 停用，并逐一重新 GET 核对 `active=false`。没有改变已有用户、角色或全局配置。
- 本轮只创建了目录 WorkItem #11/#12（SR #5/#6），没有普通求助工单成功创建；未取得评论创建成功回执；附件未执行。清理先转派给管理员，再走现有 DELETE，均返回 200，之后 GET 两条记录均为 404。
- 额外清理核验发现 DELETE 后这两条记录的 BPMN 实例 #11/#12 仍 running。核对 businessKey 分别为 `service_request:11` / `service_request:12` 和流程定义后，仅终止这两个本轮实例，均返回 HTTP 200、code 0，并重新读取确认为 `terminated`；保留审计，不删除流程记录。这一清理行为不算业务审批完成，也不证明业务删除与流程终止已具备一致性。
- 未修改共享 API、重启共享服务、执行迁移或清理既有业务记录。未运行全量 Jest/构建：本轮仓库变更仅验收文档，生产构建门禁仍引用上一节已验证结果。本轮不能覆盖所有角色、专业生命周期、跨租户负例或真实审批动作。

独立只读复核已完成：普通求助的 create/write 冲突、SR 共享动作映射、审批历史假空及受理节点缺少 UI 完成入口均有源码依据；复核纠正了 SR 评论实际要求 create 而非 write。源码差异归属需与后端运行指纹分开，附件与审批决策未实测不计通过。当前仅记录验收与阻塞，不标记这些生产问题已修复。

最终清理复核：任务 #15/#16 均为 `cancelled`，私有前端 3016/代理 3017 已停止并确认无监听；8080 后端二进制 SHA256 与上一节一致。`git diff --check` 通过，仓库仅此验收文档发生变更。


### 普通求助创建权限修复（源码 implemented，待部署验收）

- 分支 `codex/fix/ui-core-permission-contracts`，基于 `93a5b58c`。统一 Intake 的 generic 创建改为与现有路由/角色一致的 `ticket:create` + `ticket:read`；专业域仍使用各自 write/read，保留实时事务授权、MSP/租户、代申请和流程覆盖权限检查。没有向任何角色添加宽权限。
- 独立审查发现飞书既有映射更新复用创建授权，已在该更新分支补充事务内 `ticket:update` 检查；创建权限不再能单独授权更新已有关联工单。未调用飞书或共享环境执行同步。
- 回归先红后绿：最小 create/read 用户创建成功；只读、update-only、write-only 不能替代 create；创建后持久化及幂等重放成功，撤权后重放拒绝且无重复记录；专业域/未支持类型保持边界。飞书 create-only 更新拒绝、授予 update 后成功、撤权再次拒绝，拒绝前后标题/版本不变。
- 修正 MSP、流程覆盖及 PostgreSQL generic 场景的旧 write fixture，避免负例因错误缺少 create 而掩盖原本要验证的租户/流程权限。PostgreSQL 专项仅调整 fixture，未执行该数据库专项，不冒充已验证。
- 授权与 Intake 包、完整 contract/RBAC 已通过；筛选的真实 Intake/飞书本地集成测试通过，使用隔离 fixture，无外部 provider 调用。独立复审无剩余发现。前端/API payload 未变化；共享 8080 尚未替换，当前修复不能记作开发环境普通求助已通过浏览器验收。
- 用户确认后续服务请求协作范围：Helpdesk 仅处理本人当前已分配的服务请求，团队范围暂缓。该项另行实现，不与普通求助授权混为一项。


### Requested Item 协作范围修复（源码 implemented，待部署验收）

- 分支 `codex/fix/service-request-collaboration-scope`，基于 `e630d66b`。用户已明确 Helpdesk 仅对本人当前已分配的服务请求协作。共享评论创建/编辑及附件上传在原后端权限边界读取真实 Requested Item：要求 service_request:read，申请人须有 write；Helpdesk 须为当前 assignee 且有 provision。已有显式 service_request:* 管理权限继续有效。读、删除、专业字段更新、交付和审批命令未改；没有给任何角色增权，没有用 ticket 权限替代服务请求权限。
- 前端附件仅做 service_request 权限的粗粒度入口判断；身份/分配范围以后端判断为准。原 ticket 附件适配器和接口保持复用。该政策仅应用于 Requested Item，其它 recordClass 保留现有策略。
- 回归先红后绿：middleware 验证 requester/assigned Helpdesk 的 create/update、未分配/只读/缺 read/缺 actor/跨租户/旧 create-update 权限拒绝，以及资源管理员例外。真实 Intake + Gin/controller/service + SQLite/临时文件存储验证评论与附件持久化、自己的评论 PUT 编辑成功；转派完成后的新 POST/PUT 请求均 403，内容和记录数不变。这里不承诺请求执行途中并发转派的数据库原子撤权。
- 前端 3 suites / 29 tests 通过；类型检查和生产构建通过；lint 无错误，仅保留 BPMNDesigner 既有 warning。后端 authorization/middleware/contract/RBAC 完整通过，Requested Item 定向 HTTP 集成通过。最初附件 fixture 使用默认 octet-stream，被既有类型验证拒绝，改用浏览器文本上传对应的 text/plain 后通过，未放宽生产文件验证。
- 独立审查未发现阻断问题；已补其建议的真实 PUT、非 requester/assignee 的管理员，以及旧专业动作和通用 ticket 权限不能替代协作授权的回归。无共享数据库或账号权限修改，8080 仍是原部署；这些结果不能替代后续部署后的三角色浏览器验收。


### 审批决策历史状态修复（源码与独立前端验证完成）

- 分支 `codex/fix/approval-history-states`，基于 `5862d9f7`。三个既有只读展示组件共用审批决策读取 hook，复用 `useDetailResource` 与 `DetailReadState`，没有新增审批或流程状态规则。
- 空数组显示「暂无审批决策记录」，不再推断「未走审批流程」；右侧标题改为「审批决策历史」。首次错误明确提示和重试；刷新失败保留历史并标明旧数据；401/403 清空旧记录。切换工单或租户后，迟到响应不能恢复旧记录；非数组响应显式报错。
- TDD 初始 9 项行为测试失败；最终 3 suites / 22 tests 通过，覆盖三处展示的错误重试、刷新旧数据、权限拒绝、响应格式错误、工单和租户切换。类型检查通过，lint 无错误（仅既有 BPMNDesigner warning），最终生产构建通过。独立审查无阻塞，已补其建议的同工单切换租户用例。
- Chromium `approval-history-ui.spec.ts` 1 passed：通过环境变量指定既有可读工单 #10，真实登录与详情读取，审批决策 GET 使用隔离的 500/空数组响应，验证错误→重试→空态；390/1440 无页面横向溢出，并查看两张截图。没有提交评论、附件、审批决策或修改既有工单。该测试默认无指定工单时 skip，不依赖固定共享数据。
- 浏览器运行本次 production build `OyguDF_KkyvUHmCORrE33`，私有 3016 前端、3017 同源代理；结束后停止本次私有服务。日志及截图位于 `/tmp/approval-history-*`，不提交。共享 8080 未更换；前两项后端修复仍待部署验证，本项浏览器结果不是三角色真实审批验收。

### 通用 Ticket 与流程任务 UI 衔接（范围 accepted，具体接入设计 draft）

用户已确认：关注通用 Ticket 流程，不限于 SSLVPN。根据服务目录或流程绑定，允许提交后直接审批、先受理后审批以及无需审批的处理路径；不能将「请求受理」设为所有 Ticket 的必经步骤。保持当前 UI 重构主题，不重建 E2E 业务模型或审批引擎。

#### 复核事实

- 统一 Intake 的 `resolver.go:ResolveWorkflow` 使用目录绑定或命令/流程解析规则选择流程；创建事务通过 Outbox 安排启动。创建回执不是流程已进入某节点的证明，页面应读取真实启动/任务状态。
- `service/bpmn/sslvpn_approval_flow.bpmn` 是提交→主管初审→网络运维复审→KAF 授权验证；没有前置 Activity_Accept。先前真实测试卡住的目录请求走 `service_request_flow`，其 Activity_Accept 位于审批网关之前。这是两条不同流程，不能把后者的缺口推广到 SSLVPN。
- `TicketWorkflowService.AcceptTicket` 修改工单分配、状态及响应时间并记录流转，不完成 BPMN ProcessTask。工单处理人与流程任务执行人是不同职责字段，可以是同一人，也可以不同。此前 2026-08-25 设计及 2026-08-30 实施计划已经规定任务 assignee/candidateUsers/candidateGroups 和 elevated 权限的统一服务端授权；这是已有边界，不是本轮新缺口，不需重新设计或增加另一套权限判定。
- 既有 BPMN 接口支持按 businessType/businessId 或 processInstanceId 查询任务、领取任务及完成任务。服务端限制租户、参与人和生命周期；机器委派任务另有执行边界。`ListUserTaskViews` 返回当前身份可见范围，空列表不能说明当前工单不存在流程。现有任务 DTO 包含 taskPurpose/formKey/参与人等，但未提供通用 allowedActions 投影，不能把「可读取」当作「可完成」。
- Ticket 详情主要消费审批决策历史，`BPMNWorkflowApi.completeTask` 目前无页面调用者；审批中心已支持专门的领取与决策。历史记录不能替代当前任务展示，也不能把普通受理任务放进审批待办。
- 意见契约二次复核：审批使用既有 `/decisions`，controller 将 comment 转为 approvalComment，引擎写入 ProcessApprovalDecision.Comment；普通 `/complete` 接收的 variables 也由引擎合并保存。只有前端未被页面调用的 CompleteTaskRequest 顶层 comment 声明不被该 controller 绑定。不能把这个窄范围类型不一致概括为「意见无法保存」或已发生数据丢失，也不应以此阻塞整个 UI 接入；接线时复用对应的既有决策、变量或评论契约，不另建意见存储。

#### 建议的最小接入顺序

1. 在既有详情布局中呈现「当前流程任务」，与「审批决策历史」区分。通过权威业务关联读取，展示实际节点、状态、处理人及启动/读取错误；无权限与无任务不得混同。申请人可见进展需要复用或补齐受控的进展投影，不能放宽全任务列表权限。
2. 为 Helpdesk 接入后端允许的人工任务动作。沿用 BPMN 查询/领取/完成，不根据节点名称硬编码「受理」权限；先核对任务 action 投影、业务 ID 语义和 formKey/必要输入，未知或缺契约时显示明确限制。审批仍使用 decisions；自动化任务不可通过普通完成按钮执行。动作后重新读取真实任务与工单状态。
3. 使用三类路径验收：直接审批（SSLVPN 仅作为示例）、先受理后审批、无需审批。覆盖未授权、转派后权限变化、重复提交、读取失败和待办刷新。第一阶段不执行真实外部授权；KAF/KB、团队负载仍保持既有范围。

上述顺序是具体接入建议，尚未实现新任务面板或修改 BPMN 配置。完整可执行任务面板涉及服务端权限/输入投影时，应先确认契约设计，不能当成单纯加按钮。

#### 本次核验

只读源码复核并执行隔离 Go 测试：`go test ./service -run 'TestTaskServiceCompleteTask|TestBPMNTaskTerminalMutations|TestBPMNKafDelegatedTaskRejectsHumanMutations' -count=1 -v`，6 个顶层测试通过，包含领域状态校验、终态禁止变更及人工不能操作 KAF 委派任务。日志 `/tmp/ui-bpmn-task-contract-check.log`。没有改生产代码、共享 API、目录/角色/流程配置，没有执行真实审批或外部授权；三角色真实环境验收继续保持未完成。


二次复核依据：`docs/superpowers/specs/2026-08-25-bpmn-task-instance-authorization-design.md` 第 2 节、`docs/superpowers/plans/2026-08-30-bpmn-instance-authorization.md`；历史提交 `28e5c6da`（工单审批契约收敛）、`5898e224`（BPMN 唯一审批权威）。当前源码 `SubmitTaskDecision`、`recordApprovalDecision` 与任务变量合并路径交叉核验，避免将旧设计稿的「待实现」状态当作当前实现缺失。此修订仅纠正核查结论，不修改既有授权与意见行为。

二次复核验证：SubmitTaskDecision 五项 controller 测试、任务变量合并保存及审批历史租户/唯一性两项 service 测试，共 7 项通过；日志 `/tmp/ui-approval-recheck.log`。无共享环境变更。
