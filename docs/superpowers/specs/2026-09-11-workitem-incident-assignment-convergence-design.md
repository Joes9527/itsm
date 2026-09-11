# WorkItem Incident 分配与处理动作收敛设计

> 状态：accepted（业务决策已获维护者确认；书面设计待维护者复核；未实施）
> 日期：2026-09-11
> 负责人：项目维护者；记录与后续实现：Codex
> 源码观察基线：4660633019f10ec23835e23c7fa43578daa7ff57
> 范围：WorkItem 重构剩余工作中的 Incident 分配／转派／开始处理契约

## 1. 定位与权威来源

本设计补充 [WorkItem 收敛设计](2026-09-09-workitem-convergence-design.md)，承接 [独立评审第 9 节及 R7](../../review/2026-09-11-workitem-convergence-review-report.md)。它仅明确已经讨论确认的 Incident 动作语义，不替代整个 A/B/C 计划，不表示其他剩余工作已完成或已设计确认。

继续遵守 AGENTS.md：WorkItem 拥有基础身份和公共事实，Incident 域拥有专业动作；HTTP、BPMN、自动化共享专业规则。不得新增通用专业状态机、第二套审批引擎或兼容分配实现。旧计划、旧测试若与本文件已确认语义冲突，应依据本决策更新，不以旧实现反推需求。

## 2. 为什么讨论这个问题

固定基线中，HTTP `/incidents/:id/assign` 经 IncidentService.AssignIncident 只改负责人；BPMN 的 assign_incident 经 ApplyIncidentCommand 改负责人并进入 assigned。前者未接收调用方观察版本和动作键，活动记录在写入后另行生成；后者使用统一版本与事务回执，但要求状态变化，不能直接覆盖同状态改派场景。

这是 WorkItem 全入口收敛的缺口，不能靠增加一层转发隐藏差异。先明确首次分配与转派的业务语义，再让所有入口一致执行。

## 3. 决策过程

| 顺序 | 讨论与维护者反馈 | 形成的决定 |
|---|---|---|
| 1 | 维护者强调 task 本质是 WorkItem 重构，要求再次核对 | 评审从 B/C1 局部缺陷扩展到目标→入口→权威事实→旧路径的完整收敛；不重做已验证成果 |
| 2 | 对分配如何改变状态不确定，要求参考 ServiceDesk Plus、HaloITSM、Freshservice | 查询官方资料；区分产品可配置能力、默认描述和项目选择，不把某厂商行为当作统一行业标准 |
| 3 | 对比“保留已分配阶段”和“分配完全独立状态” | 建议保留当前 Incident 的已分配阶段：首次分配推进，新阶段中的转派保留进度 |
| 4 | 维护者回复“agree with you suggestion，响应及时作为 backlog，不考虑考核的问题” | 接受上述方向；首次响应／考核口径不进入本轮实现 |
| 5 | 再展示动作表及“普通处理直接开始，不强制先确认接单”的建议 | 维护者回复“认可”，并要求记录决策过程以供后续开发参考 |

## 4. 行业依据与备选方案

查阅日期：2026-09-11。仅依据官方公开资料，没有操作厂商实际租户验证；版本和租户配置可导致行为差异。

- ServiceDesk Plus：技术人员／组分配与 Request Life Cycle 的状态转换分别描述，状态可通过条件和动作配置。[分配说明](https://help.servicedeskplus.com/requests/assigning-technician.html)、[生命周期说明](https://www.manageengine.com/products/service-desk/automation/onpremises-request-life-cycle.html)。
- HaloITSM：Action 分别配置 Status After Action、Default Team、Default Agent，可组合责任转移与状态变化。[Actions](https://www.usehalo.com/guides/1419)。
- Freshservice：Agent/Pick Up 用于分配；默认状态文档以 Open、Pending、Resolved、Closed 为主，并不要求 Assigned 作为必经阶段。[分配说明](https://support.freshservice.com/support/solutions/articles/234438-assigning-tickets)、[状态说明](https://support.freshservice.com/support/solutions/articles/155560--understanding-custom-ticket-statuses)。

推论：责任归属与处理进度是不同业务事实，联动需要明确规则。“首次分配进入已分配”是本项目决定，并非三家共同强制标准。

| 方案 | 收益与代价 | 结论 |
|---|---|---|
| 保留已分配阶段，首次分配推进、后续转派保留状态 | 延续现有模型，可区分已派单与处理中；需统一首次分配和转派规则 | 采用 |
| 分配完全独立状态，取消对 assigned 阶段的依赖 | 状态更精简；需要同时调整已有流程、页面、报表和状态合同 | 本轮不采用 |
| 每次转派都重置为已分配 | 容易实现但会丢失当前处理进度，可能制造不真实的状态回退 | 不采用 |

## 5. 已确认的业务契约

| 操作与当前状态 | 结果 |
|---|---|
| 新建事件首次成功分配 | 设置负责人，new → assigned |
| 已分配事件改派 | 更换负责人，保持 assigned；不能因状态未变化而拒绝合法改派 |
| 已确认或处理中事件转派 | 更换负责人，保留当前专业状态 |
| 其他已有非终态下的合法转派 | 遵循现有专业可操作状态约束；若允许转派则保留状态，不借本轮开放原先禁止的状态 |
| 开始处理 | 通过明确动作进入 in_progress，仍校验既有专业前置条件；不要求用户必须先执行 acknowledge |
| 确认接单 | 保留现有 acknowledge 作为可选动作，不要求普通处理连续点击“确认”与“开始” |
| 已解决／已关闭／已取消 | 保持禁止直接分配；按既有专业规则处理，不因指定新负责人自动重开 |

首次分配推进状态属于 Incident 专业规则，不能在 WorkItem 公共分配能力中硬编码并影响 Problem、Change、Requested Item。界面可以区分“分配”和“转派”，不能因此保留两套后台规则。

## 6. 实现约束与验收输入

本节约束后续实施计划，不指定必须新增函数或文件，优先收敛现有 Incident 命令实现。

- HTTP、BPMN、自动化调用同一 Incident 领域规则。适配层只解析输入及可信 actor/tenant/source，不自行推导另一套状态转换。
- 调用方观察版本参与校验，不能用服务器重新读取的最新版本代替陈旧输入；操作键与规范命令摘要沿用现有幂等回执机制。
- 负责人、必要状态变化、WorkItem 版本、审计回执及该动作要求的可靠事件处于同一事务。原先写入后忽略活动记录错误的行为不保留。
- 陈旧版本返回冲突，前端保留操作意图并要求显式刷新确认，不自动覆盖重试。相同动作键同命令重放返回既有结果，不重复产生效果；同键异命令冲突。
- 对同一负责人再次提交，遵循既有无变化语义与回执约束，不借无变化请求绕过当前权限；实施计划应覆盖该路径。
- 转派不作为 SLA 重开，不重置当前周期或已完成的计时事实；原有合法重开归零和历史保留要求继续有效。

验收至少覆盖：new 首次分配、assigned 改派、acknowledged/in_progress 转派、无需先确认即可开始、终态拒绝、非法处理人／租户／权限拒绝、陈旧版本、同键重放与异命令冲突、审计失败整体回滚。对 HTTP/BPMN/自动化做相同业务输入的持久化结果对照，不能只分别测试各自 happy path。

## 7. 明确的 backlog 与范围边界

**BL-RESP-01：首次响应与考核口径。**

背景：当前 acknowledge 写入 FirstResponseAt；Freshservice 常规人工文档把回复或公开备注作为首次响应依据。[来源](https://support.freshservice.com/support/solutions/articles/50000000630-how-to-modify-the-due-date-of-a-ticket-)。这提示“内部接单”和“对用户响应”可能不同，但维护者明确本轮不考虑考核问题。

本轮不重定义首次响应、不新增考核指标或通知动作、不修改 acknowledge 现有 FirstResponseAt 行为，也不因为确认改为可选就让 assign/start 自动代填首次响应。开始处理后若未经过既有响应记录路径，现有 SLA 展示仍可能没有首次响应完成事实；该口径留待 BL-RESP-01 讨论，不在本轮暗中修正。

此 backlog 仅延期首次响应／考核设计，**不是取消既有 SLA 周期正确性、合法重开归零、历史事实保留或 C2 SLA 回归要求**。

其余 WorkItem 身份、通知 RLS/重试、公共前端旅程及 C3 退役设计仍按收敛主线继续讨论。本文件不构成共享环境迁移、删除历史数据或部署批准。

## 8. 后续开发如何使用

1. 先读本文件和评审 R7，再形成唯一 Incident 分配契约的实施计划；不要原样执行旧计划的动作假设。
2. 实施计划明确所有实际入口及旧实现删除清单，并沿用现有事务/授权/回执能力。
3. 书面设计经维护者复核后再进入 writing-plans；本轮记录不表示开始编码。
4. 完成实现及独立验证后才更新 implemented；发现需改变本节已确认语义时，记录新决定和取代关系，不静默改变。
