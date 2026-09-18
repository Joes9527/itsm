# ITSM 双机双 Agent 候选交付总计划

> **2026-09-14 范围收口：** 当前剩余任务、优先级及完成状态唯一入口为[候选交付剩余清单](2026-09-14-itsm-candidate-remaining-delivery.md)。本文件保留技术合同与历史证据；旧未勾选复合项不能直接用于重新立项，后续待办不在此追加。

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to execute this plan step by step in your assigned computer and isolated worktree. Do not spawn extra implementation agents. Steps use checkbox (`- [ ]`) syntax for tracking.

状态：accepted（双机双 Agent 分工已确认；实施未开始）。

**Goal:** 形成固定版本、可访问且通过验收的 WSL 候选环境。

**Architecture:** A 集成代码与验收，B 负责隔离数据与运行；通过固定 SHA 和证据交接。

**Tech Stack:** Go/Gin/Ent、Next.js/TypeScript、PostgreSQL、Redis、对象存储、Git worktree、WSL。

## Global Constraints

- 先读本任务、[总计划](2026-09-12-itsm-candidate-two-agent-delivery.md)、[设计](../specs/2026-09-12-itsm-candidate-integration-delivery-design.md)、AGENTS.md 和 docs/agent-engineering-governance.md。
- 两个长期执行者：A 在 Mac 拥有候选代码和业务验收，B 在 Windows/WSL 拥有环境变更。每个任务独立分支/worktree，不移动、清理或改写他人 checkout。
- 外部企业写入、R(038)、历史 backfill、源环境重置和基础设施合并不在范围。完整迁移语义在 P 前核验，后台写入隔离在 API 首次启动前核验。
- 只通过不可变 Git 提交和脱敏交接文件交换事实，不复制 .env、备份或生产凭据到 Git。源停写须形成具体窗口再确认；不推送/合并 main。
- 代码缺口按真实失败建立测试，再做最小修复；业务、权限、迁移机制新增设计需独立批准。不能删测试/降断言/伪造回执换取通过。
- 本任务完成后更新自己的交接文件，列 SHA、证据、阻塞和未验证项；不得由两人同时编辑总计划状态。

## 1. 入口与并行顺序

| 任务 | 所有者 | 开始条件 | 分支建议 | 独立交付 |
| --- | --- | --- | --- | --- |
| [T1 代码集成](2026-09-12-itsm-candidate-t1-integration.md) | A / Mac | 现在即可开始 | codex/feat/candidate-integration | G1、候选 SHA |
| [T2 环境准入](2026-09-12-itsm-candidate-t2-admission.md) | B / WSL | 现在即可开始 | codex/docs/candidate-admission | 源映射、资源与阻塞清单 |
| [T3 恢复迁移](2026-09-12-itsm-candidate-t3-environment.md) | B / WSL | T2 准入及 T1 候选 SHA | codex/chore/candidate-environment | 可验收环境、恢复和迁移证据 |
| [T4 业务验收](2026-09-12-itsm-candidate-t4-acceptance.md) | A / Mac | T1/T3 通过 | codex/test/candidate-acceptance | G2 测试及维护者旅程 |
| [T5 稳定交付](2026-09-12-itsm-candidate-t5-delivery.md) | B 执行、A 复核 | T4 通过 | codex/docs/candidate-delivery | G3、稳定入口和恢复手册 |

实际节奏：T1∥T2；A 可在 T3 期间准备 T4 测试，但不能先对未准入环境执行。T4 时 B 监测环境并按固定 SHA 部署 A 的修复，暂停迁移/重置。T5 时 A 只读复核 B 的证据。

以上是五个可交接工作包，不要求同时创建五个运行会话。两台机器各使用一个长期 Agent，依次执行属于自己的任务。用户若另开会话，必须携带任务文档和交接文件。

## 2. 代码和文件所有权

| 所有者 | 允许修改 | 不允许同时修改 |
| --- | --- | --- |
| A | itsm-backend/、itsm-frontend/；与集成直接相关的 AGENTS.md/CLAUDE.md 状态、旧计划当前入口；T1/T4 交接 | B 的环境配置、迁移执行记录、T2/T3/T5 交接 |
| B | docs/deployment/itsm-candidate-runtime.md；T2/T3/T5 交接；WSL 任务私有运行配置/脚本 | 业务代码、共享测试 fixture、A 的候选分支 |
| 协调者/维护者 | 本总计划、设计范围裁决与最终验收 | 不代替执行者虚构测试或运行证据 |

代码包中每个任务独立 worktree。A 的 T4 从审核过的 T1 版本派生。B 的环境与交付文档分支按依赖纳入上一交接提交，不重新实现候选代码。构建 checkout 单独固定到 CandidateSHA，不在其中维护环境文档。

## 3. 交接协议（操作记录，不是业务模型）

每个任务只写 docs/review/2026-09-12-candidate-tN-handoff.md。所有交接必须包含：

- TaskID、Owner、Status（ready/blocked/completed）、SourceSHA、CandidateSHA（尚未产生时明确 not-produced）。
- ConsumedHandoffs：采用的上游交接提交和文件摘要；Resources：准确资源身份或 not-created；Evidence：命令、结果、输出文件摘要和实际位置。
- Blockers：失败事实、影响任务、责任人、解除条件；NotValidated；NextAllowedAction。
- 测试通过必须关联实际构建/源码指纹。任何 CandidateSHA 更新都产生新交接；旧环境/验收证据不能自动沿用。

Git 传递优先使用用户批准的远端分支；未批准推送时，用 `git bundle create` 生成指定分支的离线包，通过已有 SSH 安全传输，接收端先 `git bundle verify`，仅导入新任务引用。禁止覆盖接收端 main 或当前分支。证据原文保留在各机任务私有目录，交接只记录脱敏摘要。

## 4. 阻塞与复审

- B 发现 API 自动消费者无法隔离、迁移语义冲突、RCA 依赖或撤销安全缺口：写 blocked 交接，指定证据与影响入口。不能启动应用试运气，也不能自己改业务代码。
- A 核对后提出最小前置修复设计，由维护者确认范围；必要时暂停 T1 集成并另建 fix 分支，不把新功能伪装为环境配置。
- 恢复后重新验证原阻塞断言，再发布新 CandidateSHA。B 基于新 SHA 重做受影响准入，A 重做受影响 G2。
- WorkItem/迁移/权限的实现需独立审查者或维护者复核；两个 Agent 可交叉只读审阅对方未参与实现的部分，不得自审替代；不能独立时交维护者。

## 5. 设计覆盖与全局完成

- [ ] T1 覆盖版本、依赖、单一业务与主题权威、文档状态和 G1。
- [ ] T2 覆盖真实源、全部写入者、资源、完整迁移语义与访问边界。
- [ ] T3 覆盖副本、恢复、P/普通迁移、启动前隔离、受限角色和候选入口。
- [ ] T4 覆盖三域、generic/Requested Item、权限、SLA、附件、真实消费者、主题、安全负向与 G2。
- [ ] T5 覆盖重启、60 分钟观察、增量数据保全、稳定入口、恢复手册、G3 和维护者验收。
- [ ] 三道门禁全部通过才声明候选交付完成；main 合并、外部履约、R、生产上线分别报告，不混算。
