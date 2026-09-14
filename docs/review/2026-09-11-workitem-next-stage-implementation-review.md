# WorkItem 下一阶段实施复核与交接

日期：2026-09-11。实施分支：codex/refactor/workitem-next-stage。业务代码提交：ed80f99b；最终 V1 测试提交：dada445d。

## 结论

B1–B5、F1–F3 和 V1 已完成代码、独立审查与对应验证。最终隔离环境从 ed80f99b 构建，9 项端到端验收全部通过（3.6 分钟），无跳过或降低断言。原实现、设计工作树和最初的本地设计草稿均保留；未推送、合并或部署。

V2 仅完成自动退役阻断、只读预检、备份恢复及部分隔离验证。历史迁移受控替换、允许删除路径、删除后完整恢复演练，以及实际环境盘点、部署和观察仍未完成。因此本轮不能标记为整体运行交付完成。

权威执行记录：[总计划](../superpowers/plans/2026-09-11-workitem-next-stage.md)、[后端记录](../superpowers/plans/2026-09-11-workitem-next-stage-backend.md)、[页面与验收记录](../superpowers/plans/2026-09-11-workitem-next-stage-experience-validation.md)、[切换与恢复手册](../deployment/workitem-convergence-cutover.md)。

## 已保留的业务决定

- WorkItem 统一身份、公共字段与协作能力；Incident、Problem、Change 保留各自专业生命周期，不建设通用万能状态机。
- Incident 首次分派进入 assigned，合法后续转派保持进度；可以直接开始处理，不额外强制 acknowledge，不把 assign/start 记为首次响应。
- Change owner 跟进整条变更，协调评估、排期、实施和关闭。转派不改变审批决定、审批任务人员或实施任务执行人；同一人可以承担不同职责，权限仍分别核验。
- Problem 转派保留 RCA、方案和有效验证证据；实际正文或证据变化按专业规则使验证失效。Change 成功不自动解决 Problem，仍须显式验证和解决动作。
- 已有负责人实际变更时要求原因；首次分配不额外强制原因。观察版本、操作键、当前授权、审计与事务共同约束所有对应入口。
- BL-RESP-01 响应及时性语义调整继续作为 backlog；本轮不做考核，不改变现有首次响应事实。BL-CHG-WO-01 多 WorkOrder 拆分继续作为 backlog。

决策来源与备选方案保存在[开发输入记录](../superpowers/specs/2026-09-11-workitem-convergence-development-input.md)，不以实现记录替代原业务决定。

## 主要发现与修复

| 发现 | 最终处理 |
|---|---|
| Incident 分派、规则、升级、BPMN 与普通编辑存在不同写路径 | 统一专业命令、观察版本、操作键、原因与审计；删除被替代事务，普通编辑拒绝负责人旁路 |
| 通用 Ticket 可绕过三域专业核心写规则 | service/repository/BPMN 均执行专业边界，混合批次先完整预检；保留共享评论、附件等能力 |
| Problem 转派错误影响验证，调查与候选方案仍存在无 Meta 写入口 | 验证依赖内容摘要，证据写入归属同一专业事务；保留历史审计，删除无 actor 的旧写签名 |
| MSP 通知在错误客户端中读取身份，暂时错误被当成撤权 | 窄目录身份与 Tenant 业务读写分开；确定性拒绝 blocked，基础设施失败保留原因并重试 |
| 真实 seed 与规范流程身份不一致 | 磁盘与嵌入配置、初始化及 CLI 使用一致校验和唯一绑定服务 |
| 旧 SLA 周期违规可触发新周期升级，页面操作后未刷新 | 以当前周期、截止时间及违规时间核验；页面按权威 version 刷新，展示当前和历史事实 |
| 公共转派目录只读取前100人 | 按实际 totalPages 完整查询授权目录；后续页失败时不展示不完整名单 |
| Change 创建后台启动与 submit 双启动 | 创建仅冻结流程定义及输入，submit 同事务唯一启动；返回 awaiting_submit，不新增审批引擎或队列状态 |
| 通用 engine/trigger、空业务身份 HTTP 可绕过 Change 提交 | 规范 WorkItem 必须实际存在且可见；独立流程不得占用规范业务键或保留身份变量 |
| 草稿 Type 与冻结路由依据可发生漂移 | 提交前比对冻结 change_type，不一致明确拒绝；不改快照、不按最新配置回退 |
| 旧 Change 创建事件进入无意义重试 | 按统一启动时机明确 blocked，保留处理事实；不 publish，普通基础设施错误仍可重试 |
| 旧022含CASCADE且checksum不可改；未限定schema存在误操作风险 | 022/027自动退役先拒绝旧对象及schema回落；新增036精确限定schema，不回填旧行或修改历史checksum |

新增036在原不可变 intake snapshot 保存定义摘要与输入变量；变量标记 Sensitive。紧急变更使用既有子类型绑定能力。显式 no_process 创建仍返回 not_required，但不能满足 Change 提交所需的审批流程门禁；缺失冻结证据另行拒绝。

## 验证证据

| 层次 | 实际结果与边界 |
|---|---|
| 后端构建及受影响包 | 构建通过；Change、Intake、Service、controller、BPMN、authorization、DTO、seeder、migration、bootstrap 等对应检查通过。没有宣称全仓所有测试均已执行 |
| 广泛 PostgreSQL | 单归属基线 111 顶层、169 子测试通过，0 FAIL/0 SKIP，410.627s；最后的 subtype/HTTP/旧事件门禁另以下列定向结果闭环 |
| Change 提交 | 最终7顶层/15场景通过，32.094s；包含启动顺序/并发、冻结配置、身份、显式no_process、类型漂移与旧事件 |
| 实际 BPMN HTTP | 9 场景通过，16.476s；8种冒用拒绝且无实例/任务/审计副作用，合法独立流程保留 |
| 036 schema | 原SQL误改独立诱饵表的RED后，缺表拒绝、重复执行、历史NULL与诱饵不变通过，0.049s；NULL schema保护未单独演练 |
| 前端 | ed80f99b 生产构建通过；类型检查、41项创建 API/hook 测试通过；先前真实页面、转派、目录、SLA 等测试通过；lint 0错误/1项既有警告 |
| V1 完整旅程 | 9/9通过，3.6m；三域 API和双浏览器冲突、直接越权、跨域恢复、真实审批、generic/Requested Item协作与附件持久化 |
| V1 流程持久化 | 5个Change，其中2个草稿无实例、3个已提交各1实例且冻结证据不变；Change启动事件0，重复规范实例0；Requested Item真实事件published且无残留错误 |
| V2 已验证部分 | 真实CLI exit2/0及前后摘要不变；自动退役拒绝；隔离dump/restore和备份后新增记录缺失的补偿反例。测试备份不是实际环境恢复备份 |

V1 的负责人和审批人使用不同测试账号，用于证明协调责任不会自动授予审批资格；这不是新增角色互斥规则。评估摘要、审批与任务事实均先真实建立再比较，没有用空值相等代替治理证据。

## 失败如何闭合

此前完整运行曾有9项通过；随后冻结检查出现8通过/1失败，Change submit返回500，因此未沿用早先通过结果验收。真实 worker Deliver→submit 确定性复现了重复实例错误，修复后重新运行全部9项。

双浏览器测试曾把正常CSRF刷新当成额外业务操作。测试现区分同操作键/版本/正文的一次特定CSRF传输重试与业务请求，仍拒绝409自动重发，并验证仅一次成功变更。连续调试曾触及真实限流；最终使用独立Redis并保留生产限流，没有降低限流或忽略429。

旧事件测试初次未使用System repository，事件实际未claim，不能作为有效RED。最终使用真实运行角色组合，旧实现消费后仍pending为RED；修复后按专业启动原因blocked、未publish、零实例，后续submit成功。旧BPMN单测改为按需建立真实WorkItem，未加入NotFound成功兜底，保留授权、审计、事务和回调断言。

## 环境偏差与清理

早期手工临时环境继承了宿主 OPENAI_API_KEY、LLM_API_KEY 和 LLM_MODEL，启动出现 embedding 探测超时。代码中的探测输入为常量 test，该路径不查询 WorkItem 业务记录；超时不能证明远端没有收到探测。该环境已停止清理，偏差记录保留。

最终 runner 使用环境白名单、私有配置目录，排除前端.env文件；实际进程变量名检查未发现继承的AI/KAF/SMTP/云业务配置。业务角色 super=false、bypass=false、ownedTables=0，RLS enforce；系统角色只承担既有系统队列/目录边界。

最终三个自建容器逐个确认不存在，19490–19494端口释放，无自有后台/前端进程，私有配置、原始报告和trace已删除；原既有PostgreSQL测试容器保留。脱敏证据保存在实施工作树已忽略的 .superpowers/sdd/workitem-next-stage/，最终V1材料位于 v1-reviewed-final，早期失败材料保留在 v1-reviewed。

## 提交与版本

| 提交 | 范围 |
|---|---|
| c14f7e69 | Incident 分派收敛 |
| cc1aa1e1 | bootstrap/Intake 原失败基线 |
| 2d2344b6 | 关系通知授权及实际RLS |
| 754d6126 | Change 转派与三域通用写边界 |
| c6cc7a62 | 规范流程身份与真实seed |
| e20ea2de | Problem metadata及调查证据 |
| 0b67feb6 | 完整调查旧测试请求契约 |
| caff73de | 自动退役阻断、CLI、备份恢复及runbook |
| 18856ba7 | 当前/历史SLA |
| 7da5569c | 权威详情与公共转派 |
| a021c3fa | 022/027 search_path保护 |
| ed80f99b | Change唯一启动、冻结上下文、旁路门禁与036 |
| dada445d | 最终隔离端到端测试及runner |

最终V1源码：ed80f99bbb818c156cb3f6fd788c4274fb2bf911。后台二进制SHA256：f5d842de68cfee8e00cd959c270aa9c0f7eda9d56e02b4079440101c0812365f。源码、前端副本及完整运行指纹随脱敏resources记录保存。后续提交只整理测试/文档，不表示部署版本变化。

WSL工作树：/home/administrator/project/itsm/.worktrees/workitem-next-stage。

## 下一步开发输入

1. 先决策历史022/027的受控替换与允许退役路径。保持历史checksum和现有阻断，不增加跳过校验或直接运行CASCADE的入口；不得把当前“拒绝删除通过”当成“允许删除通过”。
2. 在获准目标环境逐项盘点旧消费者、Change草稿/既有启动事件/运行实例/缺冻结证据记录。当前cutover exit0不覆盖全部新增冻结上下文；未解释冲突仍阻断切换，不自动取消、补造或迁移历史。
3. 按运行手册制定可审查的结构安装、可恢复备份、写入暂停、新路径验收、观察、精确RESTRICT删除和删除后恢复方案。备份后的新数据、附件及外部副作用需明确补偿责任。
4. 完成上述设计与环境准入后再安排实际切换；响应及时性语义和多WorkOrder保持已确认backlog，不混入本轮退役工作。
