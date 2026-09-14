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
| 门户搜索 | `itsm-frontend/src/components/portal/HeroSearchBar.tsx:47` 使用 GET knowledge/search，但 `itsm-backend/router/router.go` 注册 POST，handler 要求 JSON query；同组件固定 VPN/Copilot 目录、92% 匹配度和本地 setState 的“已记录自愈成功” | `b838464b` 初次角色 UI 接入的未完成实现，此后未改；是本轮 UI 完成度问题，不是后来后端修改造成的回归。下一项修复 |
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
- [ ] 下一项：修复 HeroSearchBar 的真实知识搜索接入，复用 `src/lib/api/knowledge-base-api.ts` 的现有搜索方法与 DTO；移除硬编码目录和虚假置信度/持久化成功。真实目录复用既有发布目录入口，不新建关键词推荐器；没有推荐契约时直接提供目录导航。人工提单入口在空结果和失败时仍可达。增加 loading/empty/error/retry、键盘可用性及查询/会话/租户变化后的迟到响应隔离。不得新增自愈反馈服务或 AI 引擎。
- [ ] 然后：核验并修复审批中心待办遗漏和审批/普通任务展示边界，沿用已有分页、任务类型、claim/decision 契约；不把所有 user task 改成审批任务，不扩大角色授权。为门户增加明确的完整审批列表入口。测试超过 100 条及混合状态、非审批任务、认领、驳回理由、失败重试和后端拒绝。后端字段若不足，先记录具体契约阻塞，不前端猜测。
- [ ] 最后：在上述 UI 修复版本复用三角色真实路径验收。end user 从门户进入目录并持久化建单；实际 Helpdesk 账号打开可见工单、处理/评论/附件并重新读取；被配置的审批人在既有流程完成认领与决策并核对历史。按工单类别验证合法处理动作，不将审批绕到普通状态更新。发现的问题先分类，再补直接阻塞 UI 操作的修复。

每个实现交付使用独立可审查变更和相邻行为测试。聚焦回归之后运行相称的类型、静态、构建及契约门禁，完成独立审查再推进下一项。本轮仅更新文档，不宣称上述未执行项通过。

## 4. 环境、验证与完成边界

用户已授权当前开发环境进行真实浏览器验收。后续使用独立前端端口与标记测试记录，记录前后端实际版本；不更换共享 API、不迁移/重置数据库，不触发无关外部授权或消息。复用外部 SSLVPN 测试前核对其副作用和清理边界，不能把历史测试直接无差别重跑。普通 IT 管理账号、菜单和行权限需实际核验，管理员成功不能替代。

本轮源码/历史复核无共享 API 写入。新运行窄测试：门户与目录 API 3 suites / 34 tests；审批组件、审批中心、BPMN API 和 persona 4 suites / 21 tests 均通过。首次误用 Jest 复数筛选参数启动的扩大运行已中止，不作为验收证据；该运行中申请页 15 tests PASS 仅作观察。存在 standalone 模块名碰撞和 AntD 弃用提示。Helpdesk 的 TicketDetail、workflow-state-machine、useAssignedTickets 窄测试 3 suites / 44 tests 通过，退出码 0。本轮三个定向运行合计 10 suites / 99 tests 通过；不代表全局覆盖率门禁。

未重新执行浏览器或后端流程测试；历史验收不能证明当前 API 8080 源码版本、所有角色或所有专业流程。未运行全量构建，因为本次变更仅文档；1A/2A 的完整门禁仍以各自记录为准。
