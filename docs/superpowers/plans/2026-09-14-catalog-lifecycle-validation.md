# 现有目录与 Helpdesk 生命周期验收

- 日期：2026-09-14
- 状态：accepted；执行中。目录盘点完成，候选路由修复已通过回归，真实解决/关闭路径仍存在 P1 阻塞。
- 分支：codex/test/catalog-lifecycle-audit，基于 c3347992；此次仅文档与本地验收，没有修改生产代码或现有目录配置。
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

此项仅源码修复，尚未部署或发布目录配置，也没有证明浏览器真实领取通过。动态当前 WorkItem 处理人解析、九项目录配置、解决/关闭 UI 及第 5 节边界仍待完成。
