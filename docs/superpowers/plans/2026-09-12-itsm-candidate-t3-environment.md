# T3 — WSL 副本恢复、迁移与候选环境

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to execute this plan step by step in your assigned computer and isolated worktree. Do not spawn extra implementation agents. Steps use checkbox (`- [ ]`) syntax for tracking.

状态：accepted（双机双 Agent 分工已确认；实施未开始）。

**Goal:** 在批准的独立资源上恢复真实数据副本，并提供可验收候选。

**Architecture:** B 是环境唯一写入者；完整迁移和全部后台隔离先准入，再启动应用。

**Tech Stack:** Go/Gin/Ent、Next.js/TypeScript、PostgreSQL、Redis、对象存储、Git worktree、WSL。

## Global Constraints

- 先读本任务、[总计划](2026-09-12-itsm-candidate-two-agent-delivery.md)、[设计](../specs/2026-09-12-itsm-candidate-integration-delivery-design.md)、AGENTS.md 和 docs/agent-engineering-governance.md。
- 两个长期执行者：A 在 Mac 拥有候选代码和业务验收，B 在 Windows/WSL 拥有环境变更。每个任务独立分支/worktree，不移动、清理或改写他人 checkout。
- 外部企业写入、R(038)、历史 backfill、源环境重置和基础设施合并不在范围。完整迁移语义在 P 前核验，后台写入隔离在 API 首次启动前核验。
- 只通过不可变 Git 提交和脱敏交接文件交换事实，不复制 .env、备份或生产凭据到 Git。源停写须形成具体窗口再确认；不推送/合并 main。
- 代码缺口按真实失败建立测试，再做最小修复；业务、权限、迁移机制新增设计需独立批准。不能删测试/降断言/伪造回执换取通过。
- 本任务完成后更新自己的交接文件，列 SHA、证据、阻塞和未验证项；不得由两人同时编辑总计划状态。

## Files 与接口

- Read: T1/T2 交接及候选版本内 docs/deployment/workitem-controlled-retirement-target-runbook.md、workitem-convergence-cutover.md。
- Create: docs/deployment/itsm-candidate-runtime.md；docs/review/2026-09-12-candidate-t3-handoff.md。
- Private: ~/.local/state/itsm-candidate-delivery/t3/ 的备份、清单、配置和逐步命令；权限 0700，秘密文件 0600。
- Consumes: T1 CandidateSHA、T2 明确源/目标映射、完整语义和后台门禁；所需具体环境操作批准。
- Produces: CandidateSHA、LinuxBuildDigest、EnvironmentRevision、访问入口、角色/消费者清单、恢复/P/普通迁移证据、T4 测试目标。

## 执行步骤

- [ ] 核对交接 SHA 和 bundle 摘要；在单独构建 worktree 固定候选 SHA。环境文档放自己的 codex/chore/candidate-environment 分支。
- [ ] 重查资源/并行写入状态。把精确容器、卷、角色、database/schema、端口、配置来源和补救步骤写入任务私有执行清单；缺任何身份或授权就不执行对应变更，不用旧默认值填补。
- [ ] 获取源受保护备份并记录哈希/时间/一致性界限；只读源或已确认停写窗口。先验证备份可读，再恢复到任务专属目标，绝不覆盖源。
- [ ] 核对业务记录/序列/权限/流程/附件引用和关键摘要；另外建立可重置自动化测试目标，人工候选目标禁止被 fixture 清理。
- [ ] 保持所有应用入口停止；根据目标账本执行完整只读迁移语义核验，匹配 P 证据、控制身份和恢复证明。源码中的实际 CLI 参数以该候选的 help/解析定义核实；禁止将测试签名或空证据用于目标。
- [ ] 仅在准入后执行必要的 P 和获准普通迁移；每步记录退出码、真实回执及前后摘要。遇到旧列删除、回填或账本冲突，保持 blocked，禁止执行 R、fresh/reset 或手动 RCA SQL。
- [ ] 完成全部后台写入者的启动前门禁。当前 API 内自动任务不能靠“不启动独立 Worker”隔离；若机制缺失，交给 A 前置设计，整个相关应用入口不启动。
- [ ] 在 WSL 按锁文件和项目 runtime 版本构建后端/前端，保存二进制/构建摘要。只提供候选专属凭据；网络仅允许候选依赖和显式测试接收端，不允许真实企业写入或源依赖。
- [ ] 对具备门禁的 API/消费者运行受限角色验证，保存启动与至少一次有关周期扫描前后历史行/任务摘要对比。若出现非批准历史写入，停止候选、保全证据并恢复任务副本，不改源。
- [ ] 提供只读就绪和前端入口；说明 Mac 到 WSL 的实际可达地址、认证交付渠道、测试库与人工候选库差别。凭据经受保护渠道提供，不写交接。
- [ ] 更新 runtime 手册与 T3 交接；独立/维护者复核后提交。B 在 T4 期间不得迁移、重置或改变部署 SHA，除非发布新 EnvironmentRevision 并通知 A 暂停测试。

## 完成/停止

源、备份、迁移、应用准入任何一项 blocked 就不能宣称 T3 完成。未执行消费者不是通过；不要为了产出 URL 放宽门禁。具体命令只能由核验后的 T2 资源身份生成，本计划不提供可误打共享库的通用迁移命令。
