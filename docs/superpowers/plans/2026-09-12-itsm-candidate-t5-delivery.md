# T5 — WSL 稳定观察与交接

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to execute this plan step by step in your assigned computer and isolated worktree. Do not spawn extra implementation agents. Steps use checkbox (`- [ ]`) syntax for tracking.

状态：accepted（双机双 Agent 分工已确认；实施未开始）。

**Goal:** 交付固定候选入口、恢复措施和 G3 证据。

**Architecture:** B 执行运行操作，A 只读复核；观察期间使用固定版本和专属资源。

**Tech Stack:** Go/Gin/Ent、Next.js/TypeScript、PostgreSQL、Redis、对象存储、Git worktree、WSL。

## Global Constraints

- 先读本任务、[总计划](2026-09-12-itsm-candidate-two-agent-delivery.md)、[设计](../specs/2026-09-12-itsm-candidate-integration-delivery-design.md)、AGENTS.md 和 docs/agent-engineering-governance.md。
- 两个长期执行者：A 在 Mac 拥有候选代码和业务验收，B 在 Windows/WSL 拥有环境变更。每个任务独立分支/worktree，不移动、清理或改写他人 checkout。
- 外部企业写入、R(038)、历史 backfill、源环境重置和基础设施合并不在范围。完整迁移语义在 P 前核验，后台写入隔离在 API 首次启动前核验。
- 只通过不可变 Git 提交和脱敏交接文件交换事实，不复制 .env、备份或生产凭据到 Git。源停写须形成具体窗口再确认；不推送/合并 main。
- 代码缺口按真实失败建立测试，再做最小修复；业务、权限、迁移机制新增设计需独立批准。不能删测试/降断言/伪造回执换取通过。
- 本任务完成后更新自己的交接文件，列 SHA、证据、阻塞和未验证项；不得由两人同时编辑总计划状态。

## Files 与接口

- Modify: docs/deployment/itsm-candidate-runtime.md。
- Create: docs/review/2026-09-12-candidate-t5-handoff.md。
- Consumes: T4 G2、当前 CandidateSHA/EnvironmentRevision、T3 启停及恢复来源。
- Produces: 稳定 URL、SHA/构建摘要、重启与观察证据、恢复清单、维护者验收状态；交协调者更新设计状态。

## 执行步骤

- [ ] 与 A 冻结版本及验收窗口；核对现运行二进制/前端指纹、资源、凭据来源和唯一消费者归属。
- [ ] 保全候选验收后新增数据库/对象/队列状态，记录恢复点。停止/重启命令只针对清单中的候选进程/容器，禁止 pkill 通用名称和全局 compose down/prune。
- [ ] 执行一次受控候选重启，验证 readiness、前端登录/关键读取、允许消费者恢复和历史记录保全；失败按 T3 恢复手册处理。
- [ ] 重启恢复后观察至少连续 60 分钟，每 60 秒低频只读采样就绪/关键响应，每 5 分钟采样数据库连接、任务年龄和错误/重复效果证据。工具等待不超过 60 秒，期间向用户更新关键变化。
- [ ] 非预期重启、持续不可就绪、任务非预期停滞/重复执行、越界历史写入或资源异常均判失败；修复后重新开始完整观察窗口。预期外部阻断与异常分别统计。
- [ ] 验证恢复手册的备份可读性和既有恢复证据与当前版本一致；不为演练覆盖现用源或丢失候选新写入，不执行 R。未经验证的恢复边界明确写出。
- [ ] 提供稳定访问 URL、适用网络、认证交付渠道、固定 SHA、构建摘要、配置来源、所有资源、启停顺序、备份/恢复步骤和人工处置方式，禁止写入秘密。
- [ ] A 复核 G1/G2/G3 的版本一致性、未覆盖项和历史保全证据；维护者确认代表性旅程结果后才宣布候选交付完成。
- [ ] B 提交 T5 脱敏交接和 runtime 文档，协调者据证据更新总计划与设计状态。分别报告 main 是否合并、外部动作未验收、R 未执行、生产未部署。

## 完成/停止

仅“服务能打开”不构成稳定交付。必要消费者未准入、60 分钟观察未完成或维护者验收未取得时，维持部分完成状态，列唯一下一步，不自动扩大目标或创建定时任务。
