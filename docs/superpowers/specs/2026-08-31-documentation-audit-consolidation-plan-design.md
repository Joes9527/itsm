# 文档审查与归档整理计划

> 状态：Executed（用户已确认，2026-08-31 当天完成执行）
> 日期：2026-08-31
>
> **执行结果与本文档下方"计划"部分的 3 处出入**（均按用户在执行前的明确答复调整，
> 而非本文档原计划）：
>
> 1. `docs/rbac/regression-report.md` 原列在"C 类：需要用户判断"，用户答复"RBAC 已重新设计，
>    这个应该已经过时了"，改为按 A 类归档处理（归入 `docs/archive/testing-reports/`）。
> 2. `docs/review/module-function-retrospective-2026-07-10.md` 与
>    `docs/testing/system-test-report-2026-05-17.md` 原列在"C 类"，用户答复"都已经过时了，
>    不需要它们"，按 A 类归档处理（不做勘误，原文保留，归入 `docs/archive/testing-reports/`）。
> 3. `docs/ITSM_Legacy_Master_Data_Migration_SOP.md` 按用户选择**不处理**，用户会自行确认是否为
>    192.168.31.66 上未合并的工作。
>
> 因此最终归档 49 个文件（原计划清单实际是 46 个，正文"44 个"是计数笔误 + 上述 3 个），
> 已用 `git status`/`ls docs/archive/*` 核实一致；下文各分类清单以此为准做了补充。
> B 类 3 处内容修正、2 处一致性修正均已执行——覆盖率门槛
> 那一处经复核后判断三份文档其实在描述不同指标（整体包覆盖率 vs 新增代码增量覆盖率 vs 后端整体
> 覆盖率），不是简单的"谁对谁错"，未做机械修改，留给团队做口径澄清。全部移动均使用 `git mv`
> 保留历史；移动后修复了 `docs/README.md`、`docs/documentation-style-guide.md`、`mkdocs.yml`、
> `docs/index.md`、`docs/testing/README.md`、`docs/contributing.md`、`CONTRIBUTING.md`、
> 顶层 4 个语言版本 README、`docs/architecture-product-assessment-2026-08-30.md` 中指向被移动文件的链接。
> 方法：5 个并行 agent 对 `docs/` 下全部 228 个 md 文件做只读审查，以 2026-08-17 至 2026-08-31
> 提交的文档为基准，逐条对照当前代码库核实（而非仅凭文档日期判断），产出
> KEEP / SUPERSEDED-BY / NEAR-DUPLICATE-OF / STALE-CONTRADICTED-BY-CODE / ARCHIVE-CANDIDATE / UNCERTAIN
> 六种判定。本文档是核实结果的汇总与执行计划，本身不重新做核实。

## 一、结论概览

228 个文档中：约 178 个判定 KEEP（不动，含全部 `docs/superpowers/plans+tasks`
39 个、`docs/superpowers/specs` 40/41 个、`docs/testing/test-cases/` 全部 19 个）；
约 49 个建议归档（`git mv` 进 `docs/archive/`，内容不改动，符合
`docs/archive/README.md` 已声明的"归档文档可能与当前代码不完全一致"的既定政策）；
5 个是仍在被引用为权威来源、但内容已被代码证伪的文档，需要真正修正内容而非归档；
4 个需要用户判断，不由本次审查自行处理。

**没有发现需要硬删除的文件**——最接近"重复"的案例（`itst-test-plan-v1.md`
系列 vs `docs/testing/test-cases/TC-*.md`）内容并非完全重叠（不同 ID 体系、不同粒度），
按项目既有政策处理为归档而非删除，历史仍可通过 git 追溯。

## 二、A 类：归档（git mv 进 docs/archive/，内容不改动）

`docs/archive/README.md` 已有 5 个分类目录：`bug-reports/`、`reviews/`、`plans/`、
`testing-reports/`、`workflow-reports/`。以下按此分类归档，不新建目录。

### → docs/archive/reviews/（阶段性架构/产品/商用就绪/交付复盘）

- docs/architecture-and-roadmap-assessment-2026-08-26.md — 已被 08-30 评估核实并订正，移动前在文件顶部加一行指向
  `docs/architecture-product-assessment-2026-08-30.md` 的说明
- docs/architecture-assessment-2026-08.md — 同上，同样加指向说明
- docs/business-completeness-assessment-2026-08.md — 同上（08-26 文档已逐条重查过它，08-30 文档又更新了其中的事件总线、
  Incident→Problem UI 结论），同样加指向说明
- docs/review/architecture-review-2026-06-14.md
- docs/review/servicenow-benchmark-2026-06-18.md — 其中"工单看板缺失"的判断已被同日期的
  browser-e2e-test-report 证伪，归档时保留原文，不做修正（历史记录的价值就在于如实保留，即便当时判断有误）
- docs/review/commercial-readiness-acceptance-report-2026-06-18.md
- docs/review/deep-business-test-report-2026-06-18.md
- docs/review/frontend-ux-review-2026-06-19.md
- docs/architecture/commercial-ready-architecture.md
- docs/architecture/ADR-001-modular-monolith.md — 提议的 Kafka + `domain/<entity>` 方案未被采纳（实际走了
  Watermill+Redis 和 `handlers/<domain>/`），`docs/architecture/overview.md` 的决策表已记录这一事实
- docs/architecture/ARCHITECTURE_DESIGN.md — 声称的"按租户 Postgres Schema 隔离"在代码中不存在（生产代码是纯行级
  tenant_id），`docs/architecture/overview.md` 是更准确、更新的版本，作为规范入口保留在原位
- docs/prd/commercial-ready-prd.md
- docs/delivery/production-readiness-program.md
- docs/delivery/cicd-review.md
- docs/ci/coverage-v1.1.md
- docs/ci/postmortem-v1.0-GA.md
- docs/sqlx/inventory.md
- docs/v1-ga/capability-matrix.md
- docs/initialization-release-certification.md
- docs/release-v1.5.0-certification-evidence.md
- docs/生产就绪审计报告-2026-07-12.md
- docs/superpowers/specs/2026-08-26-approval-single-track-convergence-design.md — 文档自述"已被 PR#6 覆盖……保留作为历史记录"

### → docs/archive/testing-reports/（旧版多角色/多端测试报告）

- docs/review/browser-e2e-test-report-2026-06-18.md
- docs/review/browser-functional-test-report-2026-06-20.md
- docs/test-plan/itst-similar-bugs-test-plan-v1.md
- docs/test-plan/itst-test-plan-v1.md — 与 `docs/testing/test-cases/TC-*.md` 覆盖模块重叠但 ID 体系、粒度不同，
  见五、需要团队关注的协调缺口
- docs/test-plan/itst-test-report-p0-v1.md
- docs/test-plan/itst-test-report-p1-v1.md
- docs/test-plan/itst-test-report-p2-v1.md
- docs/test-plan/itst-test-summary-v1.md
- docs/testing/full-product-smoke-2026-06-06.md
- docs/testing/coverage-audit.md
- docs/testing/test-cases/TEST-PROGRESS.md
- docs/rbac/regression-report.md — 原列 C 类，用户答复 RBAC 已重新设计、本报告已过时，改判归档
- docs/review/module-function-retrospective-2026-07-10.md — 原列 C 类（其中"Kanban 缺失"结论已被证伪），
  用户答复"已经过时了，不需要它们"，改判归档，不做勘误，原文保留
- docs/testing/system-test-report-2026-05-17.md — 原列 C 类（空模板），同上，改判归档
- docs/BPMN 整改遗留项 E2E 测试计划.md
- docs/eventbus-e2e-verification-2026-08-14.md
- docs/eventbus-p4-p5-verification-2026-08-14.md
- docs/reports/2026-08-24-sslvpn-approval-e2e-verification-report.md
- docs/ITSM 系统测试报告 v1.0.md

### → docs/archive/bug-reports/（历史缺陷报告、修复报告）

- docs/ITSM Bug 完整清单 v1.1（字段校验专项）.md
- docs/ITSM 部署问题与处理总结.md

### → docs/archive/plans/（历史增强/迁移计划）

- docs/implementation/permission-fill-tracker.md — 全部 checklist 已勾选完成
- docs/cmdb-optimization-20250704.md
- docs/workflow-enhancement-20250704.md
- docs/approvals-enhancement-20250704.md — 文档描述的批量/卡片/抽屉式审批 UI 已被完全重写为 BPMN 任务式 UI，无残留代码
- docs/ARCHITECTURE_CLEANUP.md
- docs/DEPLOYMENT_OPTIMIZATION.md — Docker 部分仍准确，但 CI/CD 部分描述的 `.github/workflows/ci.yml` 已不存在；
  未被任何权威文档引用，按整体归档处理而非拆分修正

**A 类合计：49 个文件**（含 C 类改判归入的 `docs/rbac/regression-report.md`、
`docs/review/module-function-retrospective-2026-07-10.md`、
`docs/testing/system-test-report-2026-05-17.md` 三个，均已并入对应分组的清单中）。

## 三、B 类：需要修正内容（仍是当前指南，不能归档了事）

这 3 个文档仍被当前文档体系引用为权威来源，或是面向真实操作/公众的说明，
内容错误会直接误导使用者，需要修正而非归档：

| 文档 | 问题 | 修正方式 |
|---|---|---|
| `docs/e2e-testing-guide.md` | 被 AGENTS.md/CLAUDE.md 引用为权威 E2E 指南；声称已在 `.github/workflows/ci.yml` 新增 `e2e` job，但该文件不存在，当前 11 个 workflow 里没有一个包含 e2e job | 移除/更新这段过时描述，改为指向当前实际运行 E2E 的地方（若已无 CI 中的 e2e job，需明确说明现状） |
| `docs/feishu.md` | Webhook 回调地址写的是 `/api/v1/feishu/webhook`，但实际路由是 `/api/v1/feishu/webhook/:instance_id`（`router/feishu_routes.go:28`），照做会直接失败 | 补上 `:instance_id` 路径段 |
| `docs/articles/09-multi-tenant-approval-practice.md` | 公开文章声称"按客户类型在中间件里选择 Schema 隔离或行级隔离两种策略并行"，但代码里除测试 helper 外没有任何 Schema 选择/`search_path` 逻辑，只有行级隔离 | 删除/改写这一段不存在的"双策略"描述，改为准确描述当前唯一的行级隔离策略（`docs/articles/05-multi-tenant-msp-operations.md` 的写法是准确的，可参考） |

另外发现两处低风险的事实性漂移，建议顺手统一（不需要单独决策）：

- Go 版本：`docs/development.md` 写 "Go 1.24+"，`itsm-backend/Dockerfile.prod` 实际固定
  `golang:1.25.12-alpine`，`docs/code-review-guide.md` 的 CI 示例又写 go 1.21 —— 三处不一致，
  以 Dockerfile.prod 的实际版本为准统一。
- 覆盖率门槛：`docs/code-review-guide.md` §5.2 写 service 60%/controller 40% 最低门槛，
  `docs/contributing.md`（与 `docs/roadmap.md` 一致）写的是分阶段 1%→40%→70%——以
  contributing.md/roadmap.md 的分阶段口径为准，更新 code-review-guide.md。

## 四、C 类：需要用户判断，本次不代为处理

用户已就其中 3 项给出明确答复（见文首"执行结果"），改判归入 A 类归档，不在此重复列出。
唯一仍待用户自行处理、本次未触碰的一项：

- **`docs/ITSM_Legacy_Master_Data_Migration_SOP.md`**——批次里日期最新的文件（2026-08-30），
  却记录了两个仓库里根本不存在的 CLI 工具（`cmd/migrate_legacy_itsm`、`cmd/migrate_legacy_users`），
  只有一个字段结构完全不同的 `cmd/sync_ehr_master_data` 存在。结合我们刚确认的"两台机器"开发拓扑
  （本机 Mac + 192.168.31.66），这份 SOP 有可能是对着另一台机器上未合并的分支写的，也可能单纯写错了。
  用户选择先自行确认是否有未合并的工作，本次不归档、不改写，原文原位置保留。

## 五、需要团队关注但不改文档的协调缺口

`docs/test-plan/itst-*` 系列（2026-07-04～07-09）和 `docs/testing/test-cases/TC-*.md`
是两套独立维护、覆盖模块高度重叠的测试用例集，彼此没有合并，后者的 ID 体系和用例粒度都更完整。
归档 itst-* 系列后，建议后续新增测试用例统一扩展 TC-*.md，避免第三套用例集再次出现。

## 六、执行方式

1. 先执行 A 类（49 个文件的 `git mv`），3 个 SUPERSEDED-BY 文档在移动前各加一行指向说明。
2. 执行 B 类的 3 处内容修正 + 2 处一致性小修。
3. C 类 4 项仅呈报，等待用户逐条答复后再决定是否处理。
4. 全部执行完毕后更新 `docs/archive/README.md`（如新增了目录用途）与 `docs/README.md`
   （如有链接指向被移动的文件）。
5. 归档全程使用 `git mv`（保留历史），不使用 `rm`。
