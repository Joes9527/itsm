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
