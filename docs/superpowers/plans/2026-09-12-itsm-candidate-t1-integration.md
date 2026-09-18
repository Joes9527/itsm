# T1 — Mac 候选代码集成

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to execute this plan step by step in your assigned computer and isolated worktree. Do not spawn extra implementation agents. Steps use checkbox (`- [ ]`) syntax for tracking.

状态：accepted（双机双 Agent 分工已确认；实施未开始）。

**Goal:** 产出可由 WSL 构建的固定候选 SHA 和 G1 证据。

**Architecture:** 先纳入 WorkItem，再纳入主题；语义解决重叠文件，复用现有实现。

**Tech Stack:** Go/Gin/Ent、Next.js/TypeScript、PostgreSQL、Redis、对象存储、Git worktree、WSL。

## Global Constraints

- 先读本任务、[总计划](2026-09-12-itsm-candidate-two-agent-delivery.md)、[设计](../specs/2026-09-12-itsm-candidate-integration-delivery-design.md)、AGENTS.md 和 docs/agent-engineering-governance.md。
- 两个长期执行者：A 在 Mac 拥有候选代码和业务验收，B 在 Windows/WSL 拥有环境变更。每个任务独立分支/worktree，不移动、清理或改写他人 checkout。
- 外部企业写入、R(038)、历史 backfill、源环境重置和基础设施合并不在范围。完整迁移语义在 P 前核验，后台写入隔离在 API 首次启动前核验。
- 只通过不可变 Git 提交和脱敏交接文件交换事实，不复制 .env、备份或生产凭据到 Git。源停写须形成具体窗口再确认；不推送/合并 main。
- 代码缺口按真实失败建立测试，再做最小修复；业务、权限、迁移机制新增设计需独立批准。不能删测试/降断言/伪造回执换取通过。
- 本任务完成后更新自己的交接文件，列 SHA、证据、阻塞和未验证项；不得由两人同时编辑总计划状态。

## Files 与接口

- Read: docs/superpowers/specs/2026-09-12-itsm-candidate-integration-delivery-design.md、AGENTS.md、CLAUDE.md。
- Modify: 合并差异中的 itsm-backend/、itsm-frontend/；直接涉及当前交付状态的既有设计/计划入口。
- Create: docs/review/2026-09-12-candidate-t1-handoff.md。
- Test: itsm-frontend/tests/e2e/flows/visual-theme.spec.ts、itsm-frontend/tests/e2e/business-flows/workitem-convergence.spec.ts，以及受影响模块相邻测试。真实环境 E2E 由 T4 执行。
- Consumes: origin/main 与设计列出的两个业务来源、T2 可异步送达的阻塞证据。
- Produces: CandidateSHA、来源包含关系、冲突决议、G1、未验证真实环境范围。

## 执行步骤

- [ ] 只读检查当前状态和 worktree，fetch 后记录远端 SHA；从最新 main 建立 codex/feat/candidate-integration 独立 worktree。不得复用正在工作的主题/WorkItem checkout。
- [ ] 固定当前来源；设计观察值为 WorkItem 8152ee668a6096c98f90349fd7e43ebcd4f1c587、主题 eb76c3bca6a4cee4809711477231f486d01d04c5。若变化，先解释新差异。
- [ ] 检查依赖和交集：

```bash
git merge-base --is-ancestor origin/codex/fix/workitem-classification-contracts origin/codex/refactor/workitem-next-stage
git merge-base --is-ancestor origin/codex/fix/problem-rca-authority origin/codex/refactor/workitem-next-stage
git diff --name-only origin/main...origin/codex/refactor/workitem-next-stage
git diff --name-only origin/main...origin/codex/feat/visual-theme-unification
```

- [ ] 在独立来源工作副本建立受影响测试基线，记录 node/npm/go 版本和锁文件。使用项目锁文件安装，不升级依赖。确认测试环境不会读取共享服务配置。
- [ ] 在任务 worktree 先合并固定 WorkItem 提交，再合并固定主题提交。逐个审查共享文件，保留 API、version、权限、生命周期、SLA 与主题 token 语义；禁止整文件选边覆盖。
- [ ] 对集成失败先运行最小复现测试，保留失败日志，再最小修复并重跑。新增机制移入独立前置设计，不在冲突解决中引入。
- [ ] 使用以下静态/构建检查；分别在对应目录运行。数据库测试需先核对 fixture 目标，不能直接继承本机 .env：

```bash
# itsm-backend
go build ./...
# itsm-frontend
npm run theme:check
npm run type-check
npm run lint:check
npm run test:unit -- --runInBand
npm run build
```

- [ ] 在源码中查询并选择受影响后端测试，按明确 package/test 名执行；迁移、RLS 与业务真实 PG 覆盖交给 T4 的隔离测试目标，不能把未运行算通过。
- [ ] 纠正当前入口中的 not-yet-implemented 状态和错误迁移验收顺序，保留历史报告并链接后继；AGENTS 与 CLAUDE 对应摘要同步。只修改本轮直接依赖。
- [ ] 运行 git diff --check；完成独立审阅/维护者复核。记录没有 CI 证据的事实，不用可合并状态代替。
- [ ] Conventional Commit 提交候选；创建 T1 交接，列固定 SHA、构建/测试证据及每个未验证项。生成交接 bundle 或经批准推送独立分支。

## 完成/停止

G1 未通过不标记 completed。T2 的代码阻塞由 A 接收并按总计划处理。Mac 构建成功不代表 Linux 二进制已构建或 WSL 可运行；T3 必须在 WSL 构建并核验。
