# 任务一：ITSM / KAF 源码与数据库对账执行计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 固定两仓库目标制品，完成实际数据库差异处置，并交付可供配置迁移使用的G-A验收交接。
**Architecture:** 源库只读盘点，在独立目标验证必要结构收敛。分别比较当前运行制品和未来目标制品，不用迁移编号代替结构证据。
**Tech Stack:** Go/Gin/Ent migration、PostgreSQL、KAF/Alembic、Git。
**Spec:** [总设计](../specs/2026-09-14-itsm-kaf-database-convergence-design.md)，§4为本任务权威范围。
**Status:** draft；可独立开始只读盘点，实际结构变更须先完成本计划准入。

## Global Constraints

- 不导入历史ticket，不执行ITSM R(038)，不改历史SQL/checksum，不清空历史或队列。
- 不操作生产、不停源、不启动未准入应用，不合main；所有写入使用明确隔离目标。
- 不把KAF 038与ITSM R(038)混为一谈；两仓库独立迁移链、角色及业务所有权。
- 每批最多60分钟报告；共享环境同时只允许一个协调后的写入任务。

## 输入、归属和独立完成条件

无需等待任务二或三。入口仓库为`/home/administrator/project/itsm`与`/home/administrator/project/kaf`；开始先读各仓库AGENTS.md和治理文档，检查remote、HEAD、status、worktree，保留已有改动。独立分支建议`codex/chore/database-reconciliation`；从当前origin/main建立独立worktree，目标候选代码用额外独立worktree或固定制品读取，不覆盖入口checkout。

固定参考：ITSM候选`0788a9bb196ab37a8389b3f366bed9877b2f72c3`；旧对账方案在ITSM提交`660087795e6efe49e89b759d2527ad1b8320a651`的`docs/migrations/2026-09-14-schema-ledger-reconciliation-plan.md`。它的031推荐、dev默认可重建及CLI示例不作为直接执行依据。KAF目标必须从运行制品与源码取证，不预设head。

**文件边界：**读取ITSM `itsm-backend/cmd/migrate/main.go`、`itsm-backend/migration/{migrations.go,migrator.go,migration_plan.go,runtime_inspection.go,runtime_structure.go}`及KAF实际Alembic配置/versions；仅在确认差额后修改拥有该差额的迁移/检查代码及同包测试。新增唯一交接`docs/review/2026-09-14-database-reconciliation-handoff.md`。原始导出、DSN、用户数据不提交Git。

## 执行步骤

- [ ] 记录两仓库与运行制品SHA/摘要；列出dev、合并基线、候选、迁移克隆库的实例、库/schema、PG/扩展版本及写入者。未能验证的环境单独标记，不外推生产。
- [ ] 逐库只读取完整账本及真实对象定义：列/类型、约束、索引、函数/触发器、序列、RLS、owner/runtime/migration权限。缺账本作为结果，不自动初始化。
- [ ] 审查状态CLI实际配置解析、连接目标及初始化副作用；用数据库强制只读账号和固定制品核对身份后运行。不得直接复制旧方案的DB_DSN命令。无法证明只读则先以只读SQL取证，状态CLI标阻塞。
- [ ] 生成环境×运行制品×目标制品×账本×对象×权限×前置数据矩阵；解释010 Legacy及015/016重复，区分preflight与迁移后verify，禁止校验和重写。
- [ ] 将差额分成代码不一致、账本历史、对象缺损、角色问题、数据前置条件；每项记录证据、影响、处置和验收命令。精确固定两仓库目标SHA及允许迁移清单。
- [ ] 对确需修复项形成小批可审阅变更：先用实际缺损/权限边界写失败用例，再最小修复并跑对应包测试。命令必须在确认模块及运行环境后记录到交接，不对源库运行测试。任务一不承担与差额无关的功能整合或鉴权A3/A4补齐。
- [ ] 在写入前记录独立目标身份、隔离网络/角色、备份与失败保留方法、拟执行命令及授权范围；独立复核通过后，在空库或明确数据清单的恢复库演练目标初始化/前向升级，按迁移语义验证，不执行ITSM R(038)。
- [ ] 用固定制品验证目标完整账本、结构、RLS/权限与数据前置条件；验证失败退出及重复运行行为。源库仍保持原版本，不要求全部原地升级。
- [ ] 完成交接及独立审查，执行`git diff --check`，只提交本任务文件。不存在写入准入时交付只读成果并明确G-A未通过，不能伪造目标验证。

## 输出接口：G-A

交接必须记录：`Gate: G-A`、`Status: PASS|BLOCKED`、两仓库完整SHA/制品摘要、源/目标实例与库/schema身份、目标迁移清单、结构/角色摘要、数据前置条件、证据路径/摘要、命令及退出码、未决差额、审查者和时间。DSN只给受保护凭据引用。

将交接文件所在提交记为`GARevision`；它是交接提交SHA，不是运行代码SHA。源/目标数据或制品发生影响性变化时标记失效，重新验证并发布新修订。

**完成：**G-A PASS即本任务完成，不等待任务二或三上线。任务二只消费固定GARevision；对上游差额回传，不自行改写任务一结论。
