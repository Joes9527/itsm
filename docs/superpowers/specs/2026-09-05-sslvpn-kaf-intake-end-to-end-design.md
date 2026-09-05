# SSLVPN：KAF 对话受理、统一创建与授权闭环设计

> 状态：draft — 会话中的业务范围与边界已确认，本文整体设计待用户 Review。
>
> 日期：2026-09-05。
>
> ITSM 核查基线：`5b2dd2c62358fd6e7f07d1886a2c67f750d8422f`。
>
> KAF 核查基线：`d07a178`，本地路径 `/home/administrator/actions-runner/_work/kaf/kaf`。核查时 `uv.lock` 有既有未提交修改，未改动该文件。旁侧 `kaf-unified-intake-client` 的 Git worktree 元数据失效，不作为已交付实现依据。
>
> 目标读者：产品负责人、ITSM/KAF 实现与审查人员、QA、部署与外部系统验收负责人。

## 1. 目标与成功定义

以 SSLVPN 为真实贯穿场景，完成：

```text
KAF Web 用户表达需求
  → 现有 Agent 理解意图、收集字段
  → 现有确认卡片展示并确认申请
  → KAF 以当前用户身份调用 ITSM Intake
  → 原子创建 WorkItem + Requested Item 及关联事实
  → ITSM BPMN 部门主管审批、网络运维审批
  → ITSM Worker 可靠投递 KAF 委派
  → KAF 选择受治理 Procedure/Tools 并执行外部用户组授权
  → 外部查询确认成员关系
  → ITSM 校验回执、完成业务流程和审计
  → KAF 展示 ITSM 权威结果
```

成功终点是获批用户已成为获批外部用户组成员，且 ITSM 的回执、业务状态和审计一致。VPN 登录、网关连通性和网段访问不属于本期验收。

本期不是新增一条 SSLVPN 专用创建路径。交付包括统一创建基础归并、全部创建入口的清单与契约验收、Catalog 发布校验，以及两端真实运行验收。

## 2. 已确认范围与 Backlog

### 2.1 本期包含

- KAF Web 入口；复用已有意图理解、字段收集、确认卡片和交互恢复。
- 当前用户身份交换、租户与 workspace 映射、稳定创建幂等键。
- 现有 Unified Intake 设计归并，统一编号器、Creator Registry 和专业领域规则。
- `service_request_item` 完整创建逻辑收敛；不保留旧计划对该分支的阶段性排除。
- HTTP、Catalog、BPMN、邮件、AI/工具、连接器及扫描发现的其他创建入口逐一处理与验收。
- Catalog 发布/启用组合校验以及提交、执行时的必要复验。
- SSLVPN 两级审批、KAF 委派、外部授权验证、回执恢复、用户状态展示。
- 租户/对象授权、动态字段、审计与流程启动记录的创建一致性。
- 两个 Worker、真实 PostgreSQL、KAF 与受控外部用户组的运行验收。
- 有限期授权的获批期限、首次确认生效时间和到期时间记录。

### 2.2 明确延后

- 到期自动回收的调度、移组、回收任务、重叠授权判定、回收告警和时效指标。
- Teams/WeCom 渠道接入；后续复用本期契约。
- VPN 登录及实际网段访问测试。
- 通用 Worker 平台的全部任务迁移、其他领域完整状态机改造、全量 RLS enforce。
- 全量 Langfuse 数据治理与邮件值守建设，沿用既有独立 Backlog；本期仍要求任务失败可见且敏感信息不泄露。

延期不代表已有数据保护约束被取消。自动回收将来只能处理本系统明确管理的授权，必须保护申请前已有成员关系及其他仍有效的授权。本期保存可信的基线与执行证据，不实施自动回收状态机。

产品显示“申请有效期”；自动回收交付前，不承诺到期会自动失效或移组。

## 3. 权威来源与对既有设计的修订

架构约束以 [ITSM AGENTS.md](../../../AGENTS.md)、KAF 仓库 `AGENTS.md` 为准，工程纪律遵循 [Agent 工程协作规范](../../agent-engineering-governance.md)。

| 来源 | 本设计处理 |
| --- | --- |
| [架构评估报告](../../review/2026-09-05-architecture-product-assessment-report.md) | 将创建、身份、可靠执行与体验问题转为验收条件；不将全部 roadmap 强行纳入本期 |
| [08-28 KAF 受理与委派](2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md) | 保持身份、智能决策、领域裁定、Procedure 选择与任务范围 API 边界 |
| [09-02 Unified Intake 归并设计](2026-09-02-unified-intake-workitem-creator-remediation-design.md) | 取消 `service_request_item` 排除项；扩充创建入口清单；将 KAF 对话式建单纳入本期 |
| [09-02 归并计划](../plans/2026-09-02-unified-intake-p1-reconciliation.md) | 复用已核实的冲突、字段与幂等摘要问题；不能直接照旧执行，需据本设计重编任务 |
| [09-03 Worker 平台设计](2026-09-03-modular-monolith-worker-platform-design.md) | 遵循可靠执行原则，不为 SSLVPN 新建平行队列或全面迁移其他任务 |
| [09-03 SSLVPN 生产化设计](2026-09-03-sslvpn-worker-production-readiness-design.md) | 保留运行、密钥、数据库边界与重放约束；从单纯投递生产化扩展到 KAF 对话入口 |
| [09-03 准入报告](../../reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md) | 原有本地结果作为历史证据；当前跨进程与外部链路必须重新验收 |

本文在整体批准前不替换既有权威设计。批准后更新相关旧设计的范围说明与链接，不复制维护另一套冲突规则。若实施设计改变 AGENTS.md 的架构约束，必须同步 CLAUDE.md；本设计当前遵循已有约束。

## 4. 现有能力与已核实缺口

| 能力 | 当前代码证据 | 实施含义 |
| --- | --- | --- |
| KAF 对话理解 | `src/acp/orchestration/workers/unified_agent.py` | 扩展结构化受理结果，不新增第二个聊天分类器 |
| KAF 字段收集、预览和恢复 | `src/acp/orchestration/workers/service_request_worker.py`、`src/acp/workflows/shared/field_collection.py` | 复用现有收集与快照链路 |
| KAF 通用确认卡片 | `frontend/src/chat/components/ChatView.tsx` 的 `InteractionCard` | 不新增 SSLVPN 专用卡片组件 |
| KAF 确认持久化 | `src/acp/pending_interactions.py` | 已有持久化机制；确认完成和远程建单成功需要分别处理 |
| KAF 非对话 Intake | `src/acp/routers/intake.py` 明确非 Chat Agent 用途 | 不在聊天 Agent 后再调用 `/intake/analyze` |
| KAF 新 ITSM 创建客户端 | 主仓库未检索到 `CreateWorkItemCommand`、`/intake/work-items` 客户端 | 需接入用户身份交换和受理契约 |
| KAF 会话认证 | `src/acp/auth/azure_oidc.py` 交换 OIDC 结果后签发 KAF JWT | 不能将 KAF 会话 JWT 直接当作 ITSM 用户凭据 |
| KAF 委派客户端 | `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py` | 保留任务范围 context/actions 与 durable execution 恢复 |
| 旧 ITSM 建单工具 | `src/acp/mcp/tools/itsm.py`、`src/acp/itsm/` | 属遗留系统路径，不能用于本期新 ITSM 创建，也不能以其邮件 fallback 代替建单 |
| ITSM Service Request 创建 | [service.go](../../../itsm-backend/handlers/service_request/service.go) | 基表/扩展已原子创建，但动态字段在提交后保存，失败只告警；流程也在提交后触发 |
| ITSM 编号 | [workitemnumber](../../../itsm-backend/repository/workitemnumber/allocator.go) | 唯一编号分配实现，所有 Creator 复用 |
| Catalog 类型 | [repository_impl.go](../../../itsm-backend/handlers/service_catalog/repository_impl.go) | 仍从 `itsm_type` 推导目标类，需完成单一权威字段切换 |
| SSLVPN 回收材料 | KAF `scripts/procedures/vpn_permission_revoke.md` | 采用 LDAP，不能作为 Graph 授权回收能力已存在的证据；本期回收为 Backlog |

上述是源码核查结果，不等于浏览器、真实数据库或外部系统运行通过。

## 5. 架构决策与职责

### 5.1 方案选择

| 方案 | 评估 | 决策 |
| --- | --- | --- |
| 分工作包交付统一创建与 SSLVPN 闭环，复用现有 KAF UI 和委派 | 能关闭共用创建缺口，变更有明确验收边界 | 采用 |
| 先完成整个模块化单体和通用 Worker 平台重构 | 会扩大首个业务验收范围，延后获得真实链路反馈 | 不作为本期前置条件 |
| 本期扩展到 Teams/WeCom 多渠道 | 契约可复用，但增加渠道认证和交互恢复验收面 | 后续阶段 |

### 5.2 两端边界

| 事实/行为 | 唯一权威 |
| --- | --- |
| 对话、缺失字段、候选、待确认草稿 | KAF |
| 意图、CTI/Catalog/recordClass 识别 | KAF Agent 的结构化结果 |
| Catalog、表单定义、目标类、SLA 与流程配置 | ITSM |
| 用户与 tenant 授权裁定 | ITSM 认证与领域边界；KAF 校验其自身会话和 workspace 权限 |
| WorkItem、专业生命周期、审批与业务审计 | ITSM |
| Procedure/Tool 注册、选择、风险与执行治理 | KAF |
| BPMN 等待点、允许动作、委派记录 | ITSM ProcessTask |
| KAF run/step、Tool 调用恢复与外部执行台账 | KAF |
| 当前外部成员关系 | 外部目录系统；ITSM 保存验证结果和证据引用，不声称掌控全部外部授权来源 |

ITSM 不再次分类 KAF 已输出的语义。BPMN 不固定 Procedure/Tool。KAF 不直写 ITSM 数据库、不保存第二份可变 WorkItem 状态、不以用户确认卡片替代 ITSM 审批。

## 6. KAF 收集、确认与用户身份提交

1. 从现有 `UnifiedConversationalAgent` 和 ServiceRequestWorker 接入。目录候选来自当前用户可见的 ITSM 数据，字段与期限约束以 ITSM 定义为准；KAF 负责理解、收集、解释。
2. 复用 `HitlPreview`、`action.required`、`InteractionCard` 和持久化 pending interaction。预览携带目录/表单版本、已收集字段及申请摘要；确认恢复不重新读取另一个 Procedure 版本来改变用户已确认内容。
3. 用户修改授权目标、期限等申请内容后，产生新预览并重新确认；旧确认不能提交新内容。取消不建单，过期或归属不匹配的确认拒绝执行。
4. 复用既有 identity exchange 设计：由认证后的可信用户身份建立受限 ITSM 用户会话；用户、tenant/workspace 映射需验证，不能由请求体任意指定。KAF 会话凭据、用户 Intake 凭据和 `kaf_automation` 凭据互不替代。
5. 当前终端用户作为 requester/opener/创建 actor；`source=kaf_web` 记录来源。自动执行时 actor 为受限技术主体，原申请人不变。
6. 每个已确认申请快照分配一次稳定幂等键并持久化。重复点击、网络重试、重启恢复均复用原键。同键不同内容返回冲突，不重新生成键掩盖未知结果。
7. 确认与建单结果分开表达。当前确认后先 resolve pending 的路径需要保证已确认提交上下文仍可恢复；复用现有持久化记录并扩展提交结果字段，不并行创建第二份草稿系统。
8. 超时后通过原 Intake 幂等请求恢复结果；验证失败反馈结构化字段错误；认证失效要求重新认证后恢复同一提交。只有拿到 ITSM 已创建结果才能展示申请编号与创建成功。
9. 已创建后仅保留 WorkItem 引用与提交回执；展示/动作通过受权 ITSM API 读取权威状态。提交回执是历史事实，不充当当前状态副本。

新 ITSM 用户受理客户端与遗留 `src/acp/itsm/` 保持明确系统边界；复用当前新 ITSM 委派契约，不复制一套 KAF context/actions 客户端。SSLVPN 确认后的注册执行处理程序切换为提交 Intake，不能直接开通或退回遗留邮件建单。

## 7. 统一创建应用服务与事务

### 7.1 依赖方向

```text
HTTP / KAF / 邮件 / BPMN / 工具 / Connector 入口
  → 认证或受信执行上下文 + CreateWorkItemCommand
  → Intake Application Service
  → Creator Registry
  → 专业域创建规则与领域持久化接口
  → 同一数据库事务
```

Creator Registry 只分发专业类型，不实现所有专业状态机。专业服务暴露事务内创建能力，由 Intake 持有事务；避免出现 `Intake → Service → Intake` 递归，也不允许领域服务在收到外层事务后重新开启独立事务。

旧公开创建接口可以保留其领域 API 形式，但实现必须调用同一应用服务；保留的是业务入口，不是长期兼容别名或另一套创建算法。禁止跨域调用 Repository 实现。

### 7.2 原子提交集合

以下事实在同一事务内成功或回滚：

- 幂等请求唯一占位、规范化请求摘要版本及成功结果关联。
- `workitemnumber.Allocator` 分配编号与 WorkItem 创建。
- 与 recordClass 匹配的唯一专业扩展。
- 动态字段，沿 WorkItem 的唯一读取归属保存。
- 经校验的目录/表单/流程输入版本、KAF 结构化受理快照。
- SLA 绑定及必要的专业创建审计。
- 持久化流程启动记录。

外部查询、模型调用、KAF 投递和通知发送不进入长数据库事务。创建前外部解析返回的身份/关联必须在提交边界做必要版本与归属检查。已有云资源/CI 关联逻辑不能因迁移遗漏；数据库内关联与创建应纳入事务，真实外部资源创建必须走受治理异步执行。

幂等唯一范围包含 tenant、受信创建主体、操作与 key；摘要包含全部影响创建的字段及 schema 版本。并发同请求只创建一份；同键不同请求明确冲突；查重/重放不得跨用户泄露结果。

### 7.3 流程启动

复用并归并已有 Intake 的持久化启动设计。Worker 经注册处理器调用流程领域服务启动 BPMN；使用创建请求与 WorkItem 关联的稳定启动标识防重复实例。启动记录提交后即使进程退出也能恢复。

启动失败时业务展示“申请已创建，流程启动待处理/受阻”，不能显示审批已启动。未知处理器、失效必需配置与确定性错误显式阻塞；不存在同步 fallback。是否无需流程必须在目录/流程配置中预先声明。

### 7.4 字段与编号收敛

- 复用现有统一编号器，删除 Intake 分支重复编号实现。
- 保留现有专业优先级、初始状态、分类和关联校验规则；字段逐项定义映射或明确拒绝，不静默丢弃。
- Catalog Change 创建真实 Change 扩展；Requested Item 创建真实 Service Request 扩展。
- WorkItem 共享字段保持唯一写入来源；Service Request 剩余重复字段的 schema 收敛与所有读写调用方同批切换。
- `target_class` 成为目录目标类的唯一权威；删除依赖 `itsm_type` 的推导后再执行相应列迁移。

## 8. 全部创建入口与验收清单

实施计划的第一个工作包产出完整的静态调用清单，覆盖生产 Go/Python/前端调用、Ent 创建、Repository 和内部服务调用；生成代码、测试夹具、历史回填分别标识，不混为线上入口。

以下是当前已定位种子清单，不能把它当作穷尽证明：

| 入口 | 已定位代码或核查方向 | 必须验收 |
| --- | --- | --- |
| Ticket HTTP 与快速/模板创建 | `controller/ticket_controller.go`、`service/ticket_service.go` | 两类路径均走权威创建服务，字段、权限、重复提交一致 |
| Incident HTTP | `controller/incident_controller.go` | 专业字段完整，优先级/初始状态与原规则一致 |
| Catalog / Service Request | `handlers/service_request/service.go` | 三类目标分支、动态字段、审批输入、SLA、CI 关联 |
| Change 与标准变更 | `handlers/change/handler.go`、`handlers/standard_change/handler.go` | 专业扩展正确，标准模板不绕过权限及创建契约 |
| Problem | `handlers/problem/repository_impl.go` 的基表创建调用链 | 共享创建能力与事务收敛，保持专业规则；不要求新增 KAF Problem 功能 |
| BPMN 内部创建 | `service/bpmn/incident_handler.go` 及其他任务处理器 | 受信流程主体/来源、稳定任务幂等键、重放不重复创建 |
| 邮件 | 邮件 Connector 消费及建单调用链 | 可信发件身份解析、消息去重、附件引用与事务失败恢复 |
| ITSM AI/工具 | `service/tool_queue.go`、ToolRegistry 的创建调用 | 权限与任务主体正确，走统一命令，不自行实例化残缺创建服务 |
| 飞书等 Connector | `service/feishu_sync_service.go` 等直接 Ticket 创建调用 | 外部身份和消息来源可信，稳定幂等键、无直写绕过 |
| KAF Web | 现有 ServiceRequestWorker 确认恢复链路 | 同一快照、用户身份、提交恢复与状态展示 |
| 其他内部生产创建 | 扫描新增发现 | 写入清单后按同一标准迁移或证明已复用权威路径 |

既有外部 HTTP 契约不要求改成同一个 URL，但所有创建必须落到同一应用服务。所有需新增幂等要求的活跃客户端同批修改；内部调用直接调用服务并派生稳定来源键，不绕行 HTTP、不以每次重试时间生成新键。

邮件/BPMN/Connector 使用其经过验证的执行上下文和身份映射，不套用 KAF Web 的确认卡片规则，也不为方便调用而冒充终端用户。新 Intake 必须保留这些入口的既有合法业务语义；无法解析身份、权限或来源时显式阻塞。

若仍存在生产创建路径绕过统一编号、事务、领域规则或审计，则共享创建工作包不能验收。各专业域只迁移创建及其必要回归，不扩展无关生命周期功能。

## 9. Catalog 发布与运行时校验

发布/启用由后端应用服务校验，前端只展示结构化问题：

1. 目标类已注册，Creator 与专业扩展存在。
2. 表单字段类型、必填项、动态选项与有限期限策略有效。
3. 流程定义可执行，所需 ITSM handler 已注册，配置版本明确。
4. 两级审批的组织/候选人解析配置有效，不能因无审批人静默放行。
5. SLA 策略存在、可用且符合租户与适用范围。
6. 授权级别到外部系统及目标组的租户配置完整；目标组不能由自由文本替代可信标识。
7. KAF 委派模式、允许动作和现有外部系统能力配置有效。

ITSM 只验证已有受权接口/配置可表达的 KAF 委派契约和能力状态，不新建 Procedure 镜像或第二套工具策略。无法验证必需条件时报告无法验证或阻塞，不能默认为可执行。KAF 在实际委派时选择 Procedure 并检查 Tool 治理，选择失败则阻塞任务。

发布校验不能替代提交和执行校验。目录/表单版本在确认后变化时，拒绝按不同含义静默提交；返回更新后的字段问题，引导重新确认。已启动实例保留其流程版本；人员与权限在审批/执行时重新校验。

本期被服务层强制的是目标类、表单、SLA、流程及委派可执行性的契约，不能只在 SSLVPN 名称匹配分支中实现。

## 10. SSLVPN 审批、授权与完成

### 10.1 审批

- 两级顺序审批：部门主管、网络运维。候选人来自 ITSM 组织与角色配置，不写固定账号。
- 用户确认是提交许可，不是业务审批。拒绝后不创建授权委派；重复审批只生效一次。
- 已获批申请内容冻结，改变用户、权限级别、目标组或期限需要重新走对应审批，不能在 KAF 执行时改写。
- 现有业务审批证据通过任务上下文提供给 KAF；KAF 按现有治理验证证据，不通过全局关闭 Tool 风险门禁消除重复确认。

### 10.2 外部授权

- ITSM 在同事务创建 ProcessTask、审计与委派 Outbox；API 不执行 KAF 投递。
- 至少两个独立 KAF Worker 通过既有 claim/lease 竞争消费；无 API fallback。
- KAF 使用技术主体获取 task-scoped context，选择 Procedure/Tools；保存执行版本、run/step、幂等键及脱敏证据。
- 执行对象来自可信身份映射及获批目标组快照。工具不得将不认识的目标组回退为另一个组。
- 外部授权前查询目标成员关系；申请前已有成员时不重复添加，记录“权限已存在”，不认领为本系统新增授权。
- 新增授权后通过外部查询确认成员关系。HTTP 添加请求成功本身不是本期完整成功证据。
- 采用既有 Graph 场景和受控夹具；不能因 KAF 存在 LDAP VPN 工具而悄然切换授权系统。

### 10.3 时间与证据

本期只开放有限期限。获批的是期限，生效时间为首次通过外部查询确认本次授权成功的时间；该时间随执行证据持久化并在重放中复用。ITSM 校验受权任务和证据后，按目录期限策略计算到期时间，使用 UTC 存储。

恢复时若只能在稍后确认成功，必须标明这是首次确认时间而非外部系统实际变更时间，不伪造更早时间。已持久化确认结果的重放不能延长期限。申请前已有成员的记录应区分本次验证时间和未知的原始授权时间，不把验证当作新授权生效。

ITSM 保存授权结果、获批目标、来源申请、基线分类、首次确认时间与证据引用；不复制 KAF 完整执行台账，不在日志中写 payload、凭据或用户敏感明文。

### 10.4 完成与恢复

- 回执经过 tenant、技术主体、task 状态、允许动作、版本、现有 lease/fencing 和 action ledger 校验。
- 完成回执与业务状态、授权记录、审计保持原子性；执行结果已成功但回执响应丢失时只重放持久化 completion payload。
- 外部添加请求超时先查询成员关系。无法确认时进入结果未知/人工对账，不重跑整个 Procedure。
- KAF 展示 ITSM 当前业务状态；审批中、履约中、受阻、结果未知、已完成语义明确。“已确认卡片”与“已创建申请”不同。
- 用户组已存在是幂等的业务成功，但证据必须明确没有本次新增副作用。

## 11. 失败与并发验收矩阵

| 场景 | 必须结果 |
| --- | --- |
| 重复卡片点击/同键同内容 | 一个 WorkItem、一个扩展、一个启动记录 |
| 同键不同字段或目标 | 明确冲突，不覆盖原申请 |
| 确认后 KAF 退出或 Intake 响应丢失 | 从原确认快照和幂等键恢复，无新建单 |
| 动态字段/SLA/审计/启动记录写入失败 | 创建事务整体回滚，无残缺申请 |
| 提交成功后 Worker 退出 | 持久化记录恢复流程，实例不重复 |
| 错误 tenant/workspace、伪造申请人 | 拒绝且不泄露对象或执行副作用 |
| 目录过期、目标类不匹配、缺 handler | 明确错误或阻塞，不走默认其他流程 |
| 任一级拒绝、无审批人、重复审批 | 无越级开通、无静默跳过、无重复推进 |
| 两 Worker 竞争、lease 丢失 | 只有有效执行者可更新投递结果，旧执行者不能覆盖 |
| Graph 已成功但响应/回执丢失 | 查询或回执重放收敛，不重复添加，不刷新生效时间 |
| 原已是成员 | 不添加，记录已有权限，不冒认为本系统新授权 |
| Graph 状态不可确认 | 结果未知、可定位证据、禁止盲目重发 |
| 用户刷新/重新进入聊天 | 从 ITSM 恢复当前状态，显示与实际结果一致 |

## 12. 工作包与依赖

本节为交付分解，不是逐文件执行计划。设计批准后使用 writing-plans 生成详细任务、测试命令和提交边界。

| 工作包 | 交付内容 | 依赖与完成门槛 |
| --- | --- | --- |
| W0 基线与入口清单 | 两端分支差异、全部创建调用、schema 与真实 UI 调用方清单 | 后续任务的固定基线；失效 worktree 不作为实现证据 |
| W1 统一创建归并 | Registry、编号、专业创建事务、SR 富校验、动态字段、审计、SLA、启动记录 | W0；全部创建入口迁移与故障测试通过 |
| W2 Catalog 发布校验 | 目标类权威、表单/审批/流程/SLA/履约组合校验 | W1 契约稳定；错误配置拒绝发布，运行时复验通过 |
| W3 用户身份与 KAF 提交 | 身份交换、复用确认卡片、持久化提交恢复、Intake 客户端 | W1；用户/自动化身份隔离、重复与超时测试通过 |
| W4 SSLVPN 执行收敛 | 两级审批、任务范围执行、Graph 验证、授权时间证据与回执 | W1-W3；已有可靠执行机制回归，不新增平行队列 |
| W5 真实端到端验收 | 浏览器、PostgreSQL、双 Worker、KAF 和受控 Graph 变更 | W1-W4；正反例和外部清理证据完整 |

W1 含所有受影响创建入口，不将 Problem、标准变更、邮件、Connector 的共用创建修复误列为“无关业务功能”。各领域完整状态机、到期回收和多渠道界面不在其中。

高冲突面（Ent schema/migration、bootstrap、router、BPMN 核心）由明确负责人串行集成。专业域接口和契约确定后，独立包可分开实现，但不能同时变更共享数据库。

## 13. 迁移、验证和发布

### 13.1 数据与部署

- 依据当前迁移注册表重新核对编号，不照搬旧分支 `020/021/022` 或默认认为 `023/024/025` 仍可直接使用。
- 字段删除前完成读写调用方切换和数据一致性检查；不能先删列后部署仍访问该列的服务。
- 共享字段只保留 WorkItem 权威来源，不添加长期双读/双写。存在冲突历史数据时停止迁移并形成明确修复清单，不能任取一份覆盖。
- 空库与生产等价恢复库均验证完整迁移；执行前记录目标环境、备份和恢复边界。
- API/Worker/KAF 使用各自受限凭据与运行角色，迁移账号不进入常驻服务。沿用同实例、独立逻辑数据库的既定部署方向。
- 本期没有“启用全部 RLS enforce”前置承诺，但新表、查询、运行账号与 tenant 边界必须有真实拒绝测试，不能把 RLS 默认关闭等同于应用权限可省略。

### 13.2 测试层次

1. 单元/契约：Creator 分发、身份、字段摘要、Catalog preflight、时间计算、动作授权。
2. PostgreSQL 集成：事务回滚、幂等并发、流程启动恢复、双 Worker claim、实际角色拒绝。
3. KAF 回归：已有字段收集、卡片确认、取消/恢复、新 Intake 客户端与已有委派 pipeline；多文件重构按 KAF AGENTS.md 执行完整测试并报告结果。
4. 浏览器：KAF 用户收集/确认，ITSM 两级审批，KAF 查询结果，刷新与错误恢复。
5. 外部受控演练：真实用户组基线、一次新增、查询确认、回执重放、最终清理。

ITSM 交付前执行受影响测试、后端全量测试与构建、前端类型检查/构建及相关 E2E。带 tag 或外部 DSN 的测试分别报告通过、跳过和未运行，不用默认测试通过替代集成证据。

### 13.3 受控外部演练

复用 [KAF 发布收口夹具](../../testing/kaf-delegation-release-closeout-fixture.md)，不将用户、组或凭据复制到设计。使用专用测试对象和明确演练标识，按既有要求确认环境、身份、权限及外部恢复责任。

验收顺序：非成员基线 → KAF Web 提交 → 两级审批 → 一次外部新增 → 查询确认 → ITSM 完成 → 重放无重复副作用 → 恢复为非成员并确认。

演练后的受控清理是测试环境恢复要求，不代表到期自动回收功能已交付。外部结果未知或清理失败时，不宣布验收成功，也不通过手工改 ITSM 状态绕过问题。

## 14. 设计自检与 Review

- 已确认的范围：KAF Web、复用收集与卡片、外部组授权作为终点、统一创建四项要求、有限期限及授权确认起算。
- 已延后的范围：到期自动回收及其时效讨论、其他对话渠道、VPN 实际登录与网络访问。
- 未增加第二个分类器、审批引擎、专业状态机、编号器、草稿系统或 SSLVPN 专用队列。
- 创建与执行身份分离；业务领域状态和 KAF 执行状态分离；本地事务和外部副作用分离。
- 已补入旧 SR 事务后的字段保存/流程触发缺口、KAF 确认后的提交恢复缺口及遗漏创建入口。
- 代码核查、历史测试、待执行运行验收分别标注；本文不宣称任何新增功能已完成。

下一步是用户 Review 本设计。整体确认后形成按工作包分解的实施计划，再进入代码实现。
