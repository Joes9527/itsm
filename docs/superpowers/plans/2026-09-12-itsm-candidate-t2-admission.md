# T2 — WSL 只读环境与数据准入

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to execute this plan step by step in your assigned computer and isolated worktree. Do not spawn extra implementation agents. Steps use checkbox (`- [ ]`) syntax for tracking.

状态：accepted（双机双 Agent 分工已确认；实施未开始）。

**Goal:** 确定真实源、完整迁移与后台隔离可行性，不修改运行环境。

**Architecture:** B 盘点实际配置、连接与数据对象，提前发现候选运行阻塞。

**Tech Stack:** Go/Gin/Ent、Next.js/TypeScript、PostgreSQL、Redis、对象存储、Git worktree、WSL。

## Global Constraints

- 先读本任务、[总计划](2026-09-12-itsm-candidate-two-agent-delivery.md)、[设计](../specs/2026-09-12-itsm-candidate-integration-delivery-design.md)、AGENTS.md 和 docs/agent-engineering-governance.md。
- 两个长期执行者：A 在 Mac 拥有候选代码和业务验收，B 在 Windows/WSL 拥有环境变更。每个任务独立分支/worktree，不移动、清理或改写他人 checkout。
- 外部企业写入、R(038)、历史 backfill、源环境重置和基础设施合并不在范围。完整迁移语义在 P 前核验，后台写入隔离在 API 首次启动前核验。
- 只通过不可变 Git 提交和脱敏交接文件交换事实，不复制 .env、备份或生产凭据到 Git。源停写须形成具体窗口再确认；不推送/合并 main。
- 代码缺口按真实失败建立测试，再做最小修复；业务、权限、迁移机制新增设计需独立批准。不能删测试/降断言/伪造回执换取通过。
- 本任务完成后更新自己的交接文件，列 SHA、证据、阻塞和未验证项；不得由两人同时编辑总计划状态。

## Files 与接口

- Read: docs/DEVELOPMENT_GUIDE.md；业务来源中的 itsm-backend/internal/bootstrap/app.go、kaf_worker.go；itsm-backend/migration/{migration_plan.go,work_item_retirement_guard.go,service_request_work_item_authority.go,catalog_target_class_authority.go}。
- Create: docs/review/2026-09-12-candidate-t2-handoff.md。
- Private: ~/.local/state/itsm-candidate-delivery/t2/ 中的资源清单，不存明文秘密到仓库。
- Consumes: 设计、实际 WSL、当前业务基线；不依赖 T1 完成。
- Produces: SourceMapping、MigrationSemanticInventory、BackgroundWriterInventory、ResourcePlan、Blockers；T1 变化后标记需重新核验部分。

## 执行步骤

- [ ] 以 /home/administrator/project/itsm 为仓库发现入口；检查 main、所有 worktree、未提交文件。新建 codex/docs/candidate-admission 文档 worktree，不移动 apps/itsm-kaf 固定运行目录。
- [ ] 记录本机、资源及端口元数据：

```bash
git status --short --branch
git worktree list --porcelain
docker ps --format '{{.Names}} {{.Image}} {{.Status}} {{.Ports}}'
ss -ltn
```

- [ ] 定向核对实际启动器、配置来源、进程 cwd/exe 与数据库连接；禁止输出全量 env、命令行秘密或 docker inspect Config.Env。若应用未运行，分别记录“配置指向”与“连接未验证”，不声称已找到实际活动库；候选源需维护者确认。
- [ ] 对每个可能源建立映射：database/schema、角色用途、Redis 实例/DB、bucket、本地附件、队列、API/Worker/其他消费者、CI 依赖。多个数据集不合并，来源歧义列 blocked。
- [ ] 使用现有受保护身份仅执行只读查询，获取 migration ledger/checksum、目标结构、角色/RLS、对象引用和大小；不调用会 Prepare/CreateSchema 的启动命令来做盘点。
- [ ] 按实际 pending 版本逐项列 DDL、删除、UPDATE/backfill、权限影响。特别核对 012/013/014/017/028/029 和 037/038；已注册不等于获准，保留 blockHistoricalDestruction，禁止伪造回执。
- [ ] 按代码入口逐项核对所有后台写入：API outbox、callback、notification、embedding 初始扫描、SLA/escalation、独立 KAF Worker 以及实际启动器其他任务。每项列禁用方式、作用域证据、历史记录不变验证；没有现成机制就明确缺口，不猜开关。
- [ ] 设计任务独立资源、角色/权限、端口、存储卷与网络出站拒绝清单；估算备份+两次恢复+构建空间，不占用旧端口/实例。提出具体源备份一致性策略；需要停源写入时列窗口和影响供确认。
- [ ] 核对 Redis 撤销与候选 HMAC 依赖，列必需安全验证；不自动扩大为实现任务。
- [ ] 将发现分为可继续的准备、阻塞迁移、阻塞应用启动、阻塞验收。写 T2 交接给 A，并保持环境不变。
- [ ] diff --check、文档链接检查、只读交叉审阅后提交本任务分支。

## 完成/停止

T2 完成是盘点闭合，不代表所有准入通过。交接必须明示 T3 可执行/阻塞及理由。A 可以在等待期间继续 T1，但不得因集成通过忽略这里的阻塞。
