# 分支保护与工作流路径过滤的组合问题（2026-09-19）

**类型**：仓库治理缺陷（不是代码缺陷）。**现状**：未修，暂以 `--admin` 绕过，待决。

**一句话**：任何**不改 `itsm-backend/**`** 的 PR（纯文档、脚本、配置）都**永远无法满足分支保护**——缺的不是"检查失败"，而是**必需检查根本不会被创建**。

---

## 1. 证据

分支保护要求的必需检查是三项：

```
GET /repos/:owner/:repo/branches/main/protection
→ required_status_checks.contexts = ["Lint","Build","Test"]
```

而这三项是 `backend-ci.yml` 的 job，该工作流的触发被限定在 `itsm-backend/**`：

```yaml
# .github/workflows/backend-ci.yml
on:
  pull_request:
    paths:
      - 'itsm-backend/**'
      - '.github/workflows/backend-ci.yml'
```

**实测**（PR #91：只改 `docs/`、`scripts/deploy-dev.sh`、`.env*.example`、`CHANGELOG.md`）产生的检查：

```
Build documentation, Deploy GitHub Pages, Go Security Scan, Go Vulnerability Check,
NPM Security Audit, Secret Detection, Trivy, Trivy Gate (Critical Only),
Trivy Vulnerability Report, gosec, pnpm Lockfile Guard
```

—— **没有** Lint / Build / Test。于是：

| 字段 | 实测值 |
| --- | --- |
| `mergeable` | `MERGEABLE` |
| 已运行的检查 | 全部 SUCCESS |
| `mergeStateStatus` | **`BLOCKED`**（长时间不变） |

`gh pr merge` 的报错也印证了这一点（它只会提示"未满足要求 / 可用 `--admin`"）。

## 2. 影响

1. **所有文档类 PR 都必须 `--admin` 才能合并**。本仓库有大量文档/流程类改动（设计、计划、审计、runbook），因此这条会被频繁触发。
2. **后果比"没有保护"更糟**：团队会习惯性强合，导致**真正需要代码门禁的 PR** 也可能被顺手强合——保护规则被日常行为稀释。
3. 排查成本：`BLOCKED` 与"等 CI"外观相同。本次我就在 #91 上先误判为"CI 还没跑完"，直到核对必需检查清单才发现**它们从未被创建**。

## 3. 可选修法（需治理决定）

| 方案 | 做法 | 代价 |
| --- | --- | --- |
| A. 去掉路径过滤 | Lint/Build/Test 在每个 PR 上都跑 | 文档 PR 也跑完整后端 CI（耗时） |
| B. 不设为必需 | 保留过滤，把这三项从必需检查里去掉 | 后端 PR 失去必需门禁 |
| **C. 汇总 job（推荐）** | 增加一个**始终运行**的同名聚合 job，内部按路径决定是否真正执行后端步骤；把**聚合 job** 设为必需 | 需要改 workflow；但兼顾成本与保护 |

> GitHub 原生不支持"按路径条件判定必需检查"，所以只能用 C 这种聚合方式，或退回 A/B。

## 4. 现场记录

- 首次遇到：**PR #91**（2026-09-19），以 `--admin` 合并，合并提交 `22345b3b`。
- 该仓库 **Issues 已关闭**，因此本缺陷以文档形式记录在本文件，而非 issue。
- 相关：[规范化迁移准入与克隆库约束](../deployment/canonical-migration-admission-and-clone-constraints.md)。

## 5. 待决

请选定修法（或明确"就靠 `--admin`"）。未决定前，文档类 PR 合并时需显式使用 `--admin`，并在此记录一次。
