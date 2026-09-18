# B 组工作线处置记录（2026-09-18）

**范围：** B 组「遗留数据/配置迁移与数据库治理」5 条工作分支的落地与处置结果。
**裁定人：** 仓库维护者。**执行：** 本次任务。**状态：** 3 条已开 PR 等复核合并，2 条判定为「被取代 / 不落地」。

> 本记录只描述**工作线处置**，不改变任何数据库、配方或运行环境。所有分支 ref **全部保留**，未删除、未改写；本记录不是删除授权。

## 一、落地结果（3 条）

| 分支 | 提交数 | 落地方式 | 内容与验证 |
| --- | ---: | --- | --- |
| `codex/fix/config-migration-review` | 28 | PR #61（`codex/chore/land-config-migration-review`） | B0–B5 迁移评审整改、配置迁移脚本、批次原子回执、Makefile 校验接线；37 文件 +4710，**无 Go 改动**。验证：Node 生成器测试 30/30；真 PG 语义回归 15/15（用既有一次性容器 `gb-remediation-test-pg-20260914`，测试后已恢复停止状态，未动共享库） |
| `codex/chore/database-reconciliation` | 7 | PR #62（`codex/chore/land-database-reconciliation`） | 数据库总清单、工单数据保全审计、配置迁移独立复审、开发环境数据库入口；3 个新文档 + 3 处索引/入口。冲突裁决：以 main 为准、只增不删（**+83 / −0**） |
| `codex/feat/config-launch-integration` | 40 | PR #63（`codex/chore/land-config-launch-closure-docs`） | **只落地 11 个文档**（收口/交接/校验工具链设计，+4805）。其运行时能力已在 main（`bpmn_task_ui_actions`、DTO `uiActions`、SLA 日历时区、工单类型初始化），分支**无独有 Go 文件**，故不重新引入代码 |

**#62 的冲突裁决细节：** `docs/development-environment.md` 中"数据库快照 / 目录整理 / 备份回滚"段落属**旧结构**，main 已在 `8295b2cf docs(dev): define WSL runtime version and endpoint authority` 用英文小节 `## Shared infrastructure and rollback` 重写覆盖（端口清单也已完整保留于新落地的数据库总清单）。因此丢弃该旧段落，只保留分支真正的新增（共同祖先中不存在，已核对）。

**#63 的文档安全扫描：** 11 个文件逐个 grep 凭据类关键字，命中项全部是**策略声明或环境变量名**（如 `from_env: ITSM_TARGET_DB_PASSWORD`）；证据 JSON 的邮箱/手机号/行级数据匹配数为 0。根目录 `HANDOFF.md` 按仓库既有约定更名为 `docs/review/2026-09-15-itsm-data-adaptation-handoff.md`。

## 二、处置结果（2 条不落地）

### 1. `codex/feat/workitem-config-migration`（23 提交）→ 被 PR #61 取代

| 证据 | 数值/事实 |
| --- | --- |
| 相对 `codex/fix/config-migration-review` 的净差异 | **+161 / −1966**（它是同名文件的旧版本） |
| 其"独有"新增文件 | 27 个，**全部是同名 B0–B5 文档**（#61 中为更新文本） |
| 生成器写法 | 旧形式（如 `ON CONFLICT (code) DO NOTHING`，未含租户维度） |

**处置：** PR #61 合并后，该分支标记为**已被取代**。**保留 ref**（`086f42ad`），不删除；如需复核，用 `git worktree add <path> codex/feat/workitem-config-migration` 重建工作区。

### 2. `codex/chore/source-baseline-20260914`（1 提交）→ 不落地，仅保留快照 ref

| 证据 | 数值/事实 |
| --- | --- |
| 提交自述 | "Commits the batch that was already staged… 167 files… **the content was not reviewed or modified**… 158 commits behind origin/main" |
| 内容构成 | 横跨后端授权/服务/handler 与前端源码，并含 **regenerated `itsm-frontend/test-results/junit.xml` 构建产物** |
| 相对 main 的独有新增文件 | **0** |

**处置：** 不进入 PR（未评审的暂存快照 + 构建产物，违反"一个 PR 一个可评审目标、不提交构建产物"的治理要求）。**保留 ref**（`8e2219d2`）作为历史快照；其内容如确有需要，应按主题拆分后单独评审。

## 三、保留与恢复

- 5 条分支 ref **全部保留**；本次未执行任何 `branch -D`、未改写任何分支。
- 恢复任一工作区：`git worktree add <path> <branch>`。
- A/E 组 8 条工作线的 backlog 登记见 `ROADMAP.md` → `Backlog — Unscheduled`（PR #59）。
- 与本次并行的清理与证据归档记录：`/home/administrator/.local/state/itsm-worktree-cleanup-20260918/README.md`（仓库外，含 bundle 与 SHA256SUMS）。

## 四、未决

- 三个 PR（#61/#62/#63）待维护者复核合并；合并后清理 3 个临时落地工作区。
- `main` 的 `docs` 工作流 `Deploy GitHub Pages` 仍为 404（仓库未启用 Pages），与本次处置无关。
