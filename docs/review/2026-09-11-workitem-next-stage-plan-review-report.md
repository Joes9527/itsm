# WorkItem 后续实施计划独立审查记录

> 状态：accepted（修订计划通过独立复核；业务代码尚未实施）
> 日期：2026-09-11
> 审查对象：后续实施总计划及其后端、页面与验收子计划
> 原审查范围：3bc24ffdbb3fd340affb17e82142458dab94780d..485948efc86284e821709b680342f9c50537075f
> 方式：requesting-code-review 技能、独立只读 reviewer；主任务对照实际代码复核与修订

## 结论与范围

原计划方向和批次合理，但存在五项 Important 契约缺口，原稿不宜直接执行；未发现 Critical 问题。审查覆盖计划对设计及代码的符合性，不是对尚未实现代码的验收。reviewer 未修改工作树，也未执行业务测试或共享环境操作。

保留的优点：复用专业服务和公共基础设施；保留历史免迁移、响应考核及多WorkOrder backlog；要求真实Tenant客户端验证MSP通知；已知基线测试与主要测试工具真实存在。

## 问题与处理

以下行号引用原审查 HEAD，修订后的行号会变化。

| 编号 | 原计划位置 | 问题及代码证据 | 修订 |
|---|---|---|---|
| PR1 Important | experience-validation:60 | 要求动作使用WorkItem91，但专业记录ID为4；incident-api.ts:422–423与controller/incident_command.go:15–30使用专业路由ID | 明确公共协作/SLA使用91，专业动作使用4；增加专业91哨兵，验证不会误操作另一记录 |
| PR2 Important | backend:20、88；experience-validation:16 | 统一version与Change严格expectedVersion绑定冲突；handlers/change/command_handler.go:20–27、62–64 | 保留域请求字段，由API适配器映射观察版本；增加严格HTTP载荷断言，不建双别名 |
| PR3 Important | backend:131–133 | Problem字段未清除仍会因全局版本增加而无法解决；lifecycle.go:171–176与authorization.go:35、45均作版本等值比较 | 定义共享内容验证规则、补全投影字段、保留原验证人/时间/版本；内容改变与重开失效；测试verify→转派→resolve→close及A→B→A不能复活旧验证 |
| PR4 Important | backend:118–126、134 | canonical patch缺少Workaround/Resolution，却要求承接现有方案编辑；dto/problem_dto.go:22–44与service.go:92–98 | 补充可选正文；专用请求同步presence-aware字段、nil-only旧solution映射；测试省略保持、明确清空优先及验证失效 |
| PR5 Important | experience-validation:76–89、97 | 公共转派组件依赖后端allowed/reason，但Problem actions没有assign；authorization.go:51–59 | 增加后端assign投影和相同mutation前提；不以edit权限代替合法转派；覆盖终态、撤权与目标资格 |

PR3不通过转派时更新VerifiedVersion伪造再次验证。PR4的空值规则是明确的请求契约细化：旧DTO字符串无法表达省略/清空；改为指针后resolution显式空不再回退到solution。所有行为需要实施时由真实HTTP及持久化测试验证。

## 复核与验证

- 独立reviewer首轮确认五项问题；第二轮确认PR1/PR2/PR3/PR5关闭，并要求补齐PR4专用请求的presence语义。
- 主任务补充PR4的具体DTO要求、映射代码与四类HTTP断言后，独立reviewer最终确认五项问题均在计划层面关闭，修订计划可在既有隔离与环境门禁下执行。
- 文档验证检查相对链接、状态、AGENTS/CLAUDE一致性和git diff --check；不以此声明业务测试通过。

修订计划仍需按[总入口](../superpowers/plans/2026-09-11-workitem-next-stage.md)完成实施准入、隔离验证及每项任务的审查。实际部署与退役需单独环境准入。
