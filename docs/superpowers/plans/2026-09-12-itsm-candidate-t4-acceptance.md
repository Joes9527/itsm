# T4 — Mac 业务与主题集成验收

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to execute this plan step by step in your assigned computer and isolated worktree. Do not spawn extra implementation agents. Steps use checkbox (`- [ ]`) syntax for tracking.

状态：accepted（双机双 Agent 分工已确认；实施未开始）。

**Goal:** 对固定 WSL 候选完成 G2，而非只证明页面可打开。

**Architecture:** A 通过真实候选 UI/API 验证；破坏性 fixture 仅在 B 交付的独立测试目标运行。

**Tech Stack:** Go/Gin/Ent、Next.js/TypeScript、PostgreSQL、Redis、对象存储、Git worktree、WSL。

## Global Constraints

- 先读本任务、[总计划](2026-09-12-itsm-candidate-two-agent-delivery.md)、[设计](../specs/2026-09-12-itsm-candidate-integration-delivery-design.md)、AGENTS.md 和 docs/agent-engineering-governance.md。
- 两个长期执行者：A 在 Mac 拥有候选代码和业务验收，B 在 Windows/WSL 拥有环境变更。每个任务独立分支/worktree，不移动、清理或改写他人 checkout。
- 外部企业写入、R(038)、历史 backfill、源环境重置和基础设施合并不在范围。完整迁移语义在 P 前核验，后台写入隔离在 API 首次启动前核验。
- 只通过不可变 Git 提交和脱敏交接文件交换事实，不复制 .env、备份或生产凭据到 Git。源停写须形成具体窗口再确认；不推送/合并 main。
- 代码缺口按真实失败建立测试，再做最小修复；业务、权限、迁移机制新增设计需独立批准。不能删测试/降断言/伪造回执换取通过。
- 本任务完成后更新自己的交接文件，列 SHA、证据、阻塞和未验证项；不得由两人同时编辑总计划状态。

## Files 与接口

- Read/Test: itsm-frontend/tests/e2e/business-flows/workitem-convergence.spec.ts；tests/e2e/flows/visual-theme.spec.ts；tests/e2e/fixtures/README-workitem-convergence.md（均相对 itsm-frontend）。
- Read/Test: itsm-backend/tests/integration/workitem_*_postgres_test.go。
- Modify: 相关现有测试及修复源文件，仅限 A；新机制先设计。
- Create: docs/review/2026-09-12-candidate-t4-handoff.md。
- Consumes: T1 CandidateSHA、T3 EnvironmentRevision 与隔离测试/人工环境边界。
- Produces: 固定版本 G2、逐旅程结果、外部动作限制、对 T5 的放行/阻塞。

## 执行步骤

- [ ] 从候选 SHA 建立 codex/test/candidate-acceptance 独立 worktree；核对页面/API实际版本与 T3，一致后冻结验收窗口。
- [ ] 阅读 fixture 的创建/清理、DSN 和宿主端口白名单。既有恢复 runner 可执行 R，本轮不得使用其 recovery 全流程模式；不得把原始 fixture 直接指向人工候选副本。
- [ ] 在 B 提供的测试目标准备独立测试身份与配置。需要适配现有 harness 时保留拒绝共享/未知目标的校验，先写真实错误目标拒绝测试，再最小适配，B 部署与审阅后使用。
- [ ] 先列出用例确认选择范围；以下命令在 itsm-frontend，已建立隔离配置后才执行：

```bash
npx playwright test tests/e2e/business-flows/workitem-convergence.spec.ts --project=business-flows --list
npx playwright test tests/e2e/flows/visual-theme.spec.ts --list
```

- [ ] 用上述实际 suite/project 执行真实测试；主题 suite 若使用 mocked transport，单独标 fixture 外观结果，并补真实候选旅程，不能混算。
- [ ] 在后端独立测试配置中选择真实 PG 受影响场景，覆盖 Incident/Problem/Change、关系、SLA、授权、通知及 callback；逐个检查测试是否含 R/清理，排除与本轮边界不符的执行方式但保持该项未验证，不改断言。
- [ ] 在人工候选中新建可归属测试记录验证 Incident 分派/转派/开始/解决/重开；Problem 调查/永久修复验证/生命周期；Change 审批/任务/实施/关闭；generic 和 Requested Item 共用能力。
- [ ] 验证版本冲突、重复请求、错租户/无权访问、SLA 周期、历史审计与附件；数据库断言通过 B 提供的受限只读身份执行，不使用超级用户模拟租户测试。
- [ ] 验证真实 API/BPMN/允许消费者状态推进与持久化；企业外部写入被阻断时必须显示真实 pending/blocked/error，不能据此认定企业动作成功。
- [ ] 双主题、重载持久化、移动导航/焦点、关键表单/弹层和列表/详情/创建/编辑实际业务路径验收；记录所测页面、视口和不可达项。
- [ ] Redis 冷启动故障由 B 在任务专属实例注入、A 验证 revoked token 拒绝或鉴权 fail-closed，恢复后重复验证；候选需启用 HMAC 时补 secret/nonce 恢复与重放负向测试。未满足则 blocked。
- [ ] 出现代码失败，A 先提交复现测试再修复；新 CandidateSHA 交 B 构建部署后重新冻结环境，重跑受影响证据，禁止在旧二进制上验收新源码。
- [ ] 邀请维护者完成代表性业务/页面验收；记录尚未取得的用户验收，不擅自填 PASS。提交 T4 交接供 B 开始 T5。

## 完成/停止

skip、未执行、安全隔离不足或业务失败不能算 G2 通过。已有 27/27 或 144 路由证据仅为历史基线，当前候选独立取证。不使用一次性报告创建新业务规则。
