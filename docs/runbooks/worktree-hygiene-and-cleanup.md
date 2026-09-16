# Worktree 卫生与安全清理运行手册

状态：**implemented**（2026-09-16 首次执行并验证）

## 1. 目的与适用范围

本手册规定如何在**不丢失任何提交**的前提下清理本机 Git worktree，并让每一个删除决定可复核、
可追溯。

适用对象：

- 主仓库 `/home/administrator/project/itsm` 下注册的 linked worktree（`.worktrees/`、`.claude/worktrees/`）；
- 跨仓库运行副本（见 [KAF / ITSM 本机 WSL 开发环境](../development-environment.md)）。

适用者：需要执行清理的维护者，以及需要复核或延续本次清理的 Coding Agent。

**非目标**（明确不做）：

- 不删除任何分支 ref；
- 不改写历史、不 `gc --prune=now` 影响他人复现；
- 不停止应用、不动数据库、不动 Docker volume；
- 不处理 CI runner checkout 与归档入口（见 `docs/development-environment.md` 第 2 节）。

## 2. 不变式（Invariants）

以下条件在任何一步都必须成立；任一条不满足即**停止**，不要用 `--force` 绕过。

| # | 不变式 | 理由 |
|---|---|---|
| I1 | 不删除分支 ref，只删工作区目录 | 提交与分支是可复现资产；工作区只是检出副本 |
| I2 | 删除前必须存在完整 bundle 备份，且已通过还原演练 | "分支还在"不等于"有第二份副本" |
| I3 | 删除前必须归档未提交文件与**被忽略**的证据文件 | 被忽略文件不在任何 commit 里，删了就永久消失 |
| I4 | 不触碰 `.claude/worktrees/` 下的 harness 管理项 | 用 git 删除会产生 harness 看不见的幽灵状态 |
| I5 | 不删除运行副本及其 Git 数据目录 | `apps/itsm-kaf/itsm` 承载在役应用源码，文档明令不可删 |
| I6 | 每一步删除后逐项比对分支 SHA 与全量 ref 集合 | "未删除分支"必须被证明，而不是被声明 |
| I7 | 未提交内容非空即停，交人工裁定 | 见 §6 陷阱 6 |

## 3. 判定分类

按"删掉会失去什么"分类，而不是按新旧或体积分类。

| 层级 | 判据 | 可直接删除 | 说明 |
|---|---|---|---|
| T0 | `git worktree prune` 报告 `prunable`（目录已消失） | 是 | 只清注册表，不涉及内容 |
| T1 | 运行中/运行副本/文档依赖项 | **否** | 见 §5 Step 0 |
| T2 | `git rev-list --count origin/main..<branch>` == 0，且无未跟踪的被忽略证据 | 是 | 分支 tip 已是 main 祖先，代码字面意义上在主线里 |
| T3 | 同 T2，但存在 `.superpowers` 等被忽略证据 | 归档后是 | 先归档再删 |
| T4 | 分支存在未并入 `origin/main` 的提交 | **否** | 这是"未收尾的工作线"，不是垃圾；需人工定性 |
| T5 | T4 ∩ 已推送到 origin ∩ 长期未动 | 归档后是 | 分支在 origin 有完整副本，作废风险低 |
| T6 | T4 ∩ 满足 §4 的作废判据 | 归档后是 | 需逐条列证据，见 §4 |

关键前提：**"未合并" ≠ "可删除"**。T4 不得自动判为可删。

## 4. 作废判据与取数命令

判定一条分支是否"内容已落地/已作废"，用以下四条证据，**不要只看提交数**。

### 4.1 补丁是否已在上游

```bash
git cherry origin/main <branch>
```

- `-` 开头：该提交的补丁**已在上游**（squash / cherry-pick 等价）；
- `+` 开头：补丁**不在上游**。

注意：**merge commit 不参与 cherry**，输出可能为空但分支仍有独立内容（实测
`codex/chore/source-baseline-20260914` 即为此类）。

### 4.2 改动文件是否被某个已合并 PR 完全覆盖

```bash
# 分支改动文件集
git diff --name-only origin/main...<branch> | sort -u > /tmp/branch_files.txt
# 已合并 PR 的改动文件集（必须用 merge commit 的两个父，不能用分支名）
git diff --name-only <merge_sha>^1 <merge_sha> | sort -u > /tmp/pr_files.txt
comm -12 /tmp/branch_files.txt /tmp/pr_files.txt | wc -l
```

若分支改动文件**全部**落在某个已合并 PR 的文件集内，该分支极可能已被该 PR 取代。

### 4.3 分支之间是否互为重复副本

```bash
git diff --shortstat <branch_a> <branch_b>   # 无输出 = tree 完全相同
```

实测：`ga-canonical-request-flow` 与 `ga-workbench-task-contract` tree 完全相同（纯重复）；
`cookie-transport-validation`、`ga-canonical-request-flow`、`ga-workbench-task-contract`
共享 18 个改动文件（同一工作的三份副本）。

### 4.4 改动面是文档还是代码

```bash
git diff --name-only origin/main...<branch> | grep -c '^docs/'
```

纯 `docs/` 的交接记录类分支，其价值是"过程记录"，删除前应确认记录已在别处留档。

## 5. 标准流程（SOP）

### Step 0：冻结检查（删除任何东西之前）

```bash
# 1) 是否有进程 cwd 落在 worktree 内
for pid in $(ls /proc | grep -E '^[0-9]+$'); do
  cwd=$(readlink /proc/$pid/cwd 2>/dev/null) || continue
  case "$cwd" in /home/administrator/project/itsm/.worktrees/*|/home/administrator/apps/itsm-kaf/*)
    echo "PID=$pid CWD=$cwd";; esac
done

# 2) 容器是否挂载了 worktree 源码（只挂 volume 才是安全的）
for c in $(docker ps --format '{{.Names}}'); do
  docker inspect -f '{{.Name}} {{range .Mounts}}{{.Source}} {{end}}' "$c"
done | grep -E 'itsm|kaf'

# 3) 是否有未提交内容（任何一条非空都要停下）
git -C <worktree> status --porcelain -uall

# 4) 被忽略但可能重要的文件
git -C <worktree> status --porcelain --ignored -uall | grep '^!!'
```

同时确认保留清单：运行副本、文档点名的交付源码、harness 管理项。

### Step 1：建立备份层

```bash
B=/home/administrator/.local/state/itsm-worktree-cleanup-<YYYYMMDD>
mkdir -p "$B/evidence" && chmod 700 "$B"

# 1) 权威 ref 清单
git for-each-ref --format='%(refname) %(objectname)' | sort > "$B/refs-before.txt"

# 2) 全部 ref 的完整 bundle（显式列出所有 ref，不要用 --remotes 简写）
git bundle create "$B/itsm-all-refs.bundle" $(git for-each-ref --format='%(refname)')

# 3) 分支清单：sha / subject / upstream / 相对 main 的独有提交数 / 所属 worktree
```

**必须显式列出全部 ref。** 实测 `--branches --tags --remotes refs/codex/*` 组合会静默
排除部分 ref（`warning: ref ... is excluded by the rev-list options`）。

### Step 2：验证备份（不可跳过）

```bash
git bundle verify "$B/itsm-all-refs.bundle"          # 期望：records a complete history
git bundle list-heads "$B/itsm-all-refs.bundle" | awk '{print $2" "$1}' | sort > "$B/refs-in-bundle.txt"
diff "$B/refs-before.txt" "$B/refs-in-bundle.txt"     # 期望：无差异

# 独立还原演练：证明真的能恢复
git init --bare /tmp/restore.git
git -C /tmp/restore.git fetch "$B/itsm-all-refs.bundle" '+refs/*:refs/*'
git -C /tmp/restore.git for-each-ref --format='%(refname) %(objectname)' | sort | diff - "$B/refs-before.txt"
git -C /tmp/restore.git fsck --no-progress           # 期望：0 个 error/missing/corrupt
```

`git clone <bundle>` **只带出 `refs/heads` 与 `refs/tags`**，必须用上面的 `fetch '+refs/*:refs/*'`
才能恢复 `refs/remotes/*`、`refs/codex/*` 等命名空间。

### Step 3：分层筛选

按 §3 分类，按 §4 取证据，生成清单，并对每一条跑 I7 检查。输出清单文件
（`t2_list.txt` 等）留在备份目录，作为删除范围的唯一依据。

### Step 4：归档证据

```bash
tar -czf "$B/evidence/<name>.superpowers.tar.gz" -C "<worktree>" .superpowers
```

校验口径必须是 **文件数 + 符号链接数**（见 §6 陷阱 3），并把 sha256 写入
`evidence-manifest.tsv`。

单独抢救的未提交文件（例如只在某个分支工作区里存在的测试用例）复制到
`$B/rescued-tests/`，并用 `md5sum` 双向比对确认逐字节一致。

### Step 5：删除

```bash
git worktree remove "<path>"     # 不加 --force
```

`git worktree remove` 对被忽略文件**不会拒绝**，所以 I3 的核对必须由人先完成。

### Step 6：验证与记录

```bash
# 分支 SHA 逐项比对（用删除前快照）
while read -r br sha; do
  cur=$(git rev-parse -q --verify "refs/heads/$br")
  [ "$cur" = "$sha" ] || echo "MISMATCH $br"
done < "$B/t<N>-branch-shas.txt"

# 全量 ref 集合与备份时一致
git for-each-ref --format='%(refname) %(objectname)' | sort | diff - "$B/refs-before.txt"

git worktree prune -n -v      # 期望：空
git worktree list | wc -l
du -sh .worktrees
df -h /home | tail -1
```

最后更新备份目录的 `README.md` 与 `SHA256SUMS`，并重新校验：
`sha256sum -c SHA256SUMS` 必须 0 失败。

## 6. 已知陷阱

以下均为本次执行中**实际踩到**并修正的问题。

1. **`git -C <path> ... HEAD` 漏掉 `-C`**。`git rev-list --count origin/main..HEAD` 或
   `git log -1 HEAD` 若不带 `-C <worktree>`，会读到**主仓库自己的 HEAD**，导致所有 worktree
   显示同一个 `ahead` 和同一个提交标题。本次因此产出了一份全错的分支清单，必须修正后重跑。
   用分支名（而非 `HEAD`）查询可规避。
2. **`git clone <bundle>` 不恢复非标准命名空间**。见 Step 2。
3. **`find -type f` 不计符号链接，`tar` 计入**。归档条目数校验若只用文件数，会出现
   "+1 但未丢文件"的假告警。正确口径：`文件数 + 符号链接数`。
4. **对已合并分支做 `origin/main...<branch>` 会得到空 diff**。已合并分支的 merge-base 就是
   分支 tip，因此文件集为空。比较已合并 PR 时必须用 `git diff --name-only <merge>^1 <merge>`。
5. **`.gitignore` 可能把源码目录一起忽略**。本仓库 `.gitignore` 的 `/release/` 会匹配任意层级
   的 `release/`，使 `itsm-frontend/src/components/release/` 下的新文件"隐身"。因此
   `--ignored` 的结果必须逐条看，不能一律当构建产物删除。
6. **被忽略文件不构成 `worktree remove` 的拒绝条件**。该命令只对已跟踪改动/未跟踪文件报错，
   被忽略文件会被静默一并删除。I3 的核对不能指望工具拦住。
7. **删除后要检查"磁盘上有目录但未注册"**。本次发现孤儿目录
   `.worktrees/org-role-gm-modeling`（无 `.git`、无注册项，仅 71 MB 的 `.next` 构建缓存）。
   遍历 `git worktree list` **不会**发现它，必须反向遍历磁盘目录：

```bash
for d in .worktrees/*/; do
  a="/home/administrator/project/itsm/${d%/}"
  git worktree list --porcelain | grep -qx "worktree $a" || echo "UNREGISTERED: $a"
done
```

## 7. 执行记录（2026-09-16）

### 7.1 汇总

清理前：78 个 worktree 注册、`.worktrees` 77 GB、`.claude/worktrees` 7.5 GB、宿主已用 348 GB。

| 层级 | 动作 | 数量 | 判据 |
|---|---|---:|---|
| T0 | `git worktree prune` | 2 | 目录已消失的失效注册（`/tmp/itsm-client-review-*`，内容已由 `.rescue/2026-09-15/` 抢救） |
| T2 | 删除 | 27 | 分支 tip 已是 `origin/main` 祖先，且无被忽略证据 |
| T3 | 归档证据后删除 | 8 | 同 T2，但有 `.superpowers` 证据 |
| T5 | 归档证据后删除 | 11 | T4 ∩ 已推送到 origin ∩ 最后提交早于 2026-09-10 ∩ 非 harness |
| T6 | 归档证据后删除 | 4 | §7.3 作废判据 |
| — | 删除孤儿目录 | 1 | `.worktrees/org-role-gm-modeling`，无 git 元数据 |

清理后：**26 个 worktree 注册**、`.worktrees` 17 GB、宿主已用 288 GB，**累计回收 60 GB**。

分支安全（每次删除后均实测）：

- 全量 **276 个 ref**（126 本地分支 + 117 远端分支 + 22 tag + 11 `refs/codex/*`）
  在全部操作前后 `git for-each-ref` **逐项比对完全一致**；
- 每个被删 worktree 的分支 SHA 与删除前快照**逐条比对，0 mismatch**；
- 全部 276 ref 的 bundle 备份已验证（`complete history` + 独立还原演练 `fsck` 0 错误）。

> 备份位于本机私有目录 `/home/administrator/.local/state/itsm-worktree-cleanup-20260916/`
> （权限 700，含敏感上下文，**不入仓、不上传**）。还原命令见该目录 `README.md`。

### 7.2 已删除 worktree 的分支（分支 ref 全部保留，可随时 `git worktree add` 重建）

T2（27）：`codex/security/candidate-auth-state-windows`、`codex/fix/candidate-a-windows-delivery`、
`codex/test/candidate-acceptance`、`codex/fix/candidate-incident-status`、
`codex/fix/candidate-migration-order`、`codex/feat/candidate-release-windows`、
`codex/test/catalog-lifecycle-audit`、`fix/frontend-style-system`、`codex/fix/ga-runtime-fixture`、
`codex/fix/header-brand-alignment`、`codex/fix/incident-category-contract`、
`codex/chore/local-incident-runtime`、`codex/docs/itsm-kaf-database-program`、
`fix/p1-final-approval-authority`、`codex/fix/problem-rca-authority`、
`codex/chore/local-problem-rca-runtime`、`codex/chore/support-handoff-5173-runtime`、
`codex/feat/ticket-process-task-actions`、`codex/feat/unified-support-ticket-handoff`、
`codex/refactor/work-item-assignment-main`、`codex/fix/workflow-menu-production`、
`codex/fix/workitem-classification-contracts`、`codex/chore/local-classification-runtime`、
`codex/docs/workitem-convergence-design`、`codex/fix/wsl-port-authority`、`codex/fix/wsl-ui-3010`，
外加 1 个 detached HEAD（`eb76c3bc`，已是 `origin/main` 祖先）。

T3（8）：`codex/feat/work-item-task-assignment`、`feat/migration-fresh-bootstrap`、
`codex/fix/ticket-detail-experience`、`test/wave0-change-regression-tests`、
`codex/refactor/workitem-convergence-lifecycle`、`codex/refactor/workitem-convergence-relations`、
`codex/docs/workitem-convergence-review-20260911`、`codex/refactor/workitem-next-stage`。

T5（11）：`codex/docs/architecture-review-2026-09-05`、`codex/docs/development-environment`、
`feat/p1-a-authority-cutover`、`feat/p1-a-number-allocator`、`feat/p1-a-ticket-transaction`、
`feat/p1-architecture-integration`、`feat/p1-c-bpmn-hardening`、`feat/p1-d-callback-contract`、
`fix/p1-final-alert-delivery`、`fix/p1-final-callback-cas`、
`fix/p1-integration-contract-convergence`。

T6（4）：见 §7.3。

### 7.3 T6 作废判据（本次唯一"按内容判定作废"的一批）

| worktree | 判据 |
|---|---|
| `codex/docs/ticket-detail-experience` | 3/3 改动文件被已合并 PR #35 覆盖；2 个提交的补丁 `git cherry` 全为 `-` |
| `codex/fix/generic-ui-validation` | 7/7 改动文件被已合并 PR #32 覆盖（100%） |
| `codex/fix/ga-workbench-task-contract` | 与 `ga-canonical-request-flow` tree 完全相同（重复副本） |
| `codex/fix/ga-workbench-client-contract` | 单条提交 `f002d2d3` 是**未进 main** 的回归用例；人工裁定为"归档用例后删除" |

`ga-workbench-client-contract` 的用例已单独归档到
`<backup>/rescued-tests/ga-workbench-client-contract__command-actions.test.tsx`
（81 行，md5 `43e1840a2f57f736c090dd5f3e50e5a7`，与源文件逐字节一致）。相关背景见仓库内
`.rescue/2026-09-15/NOTES.txt`。

### 7.4 保留项与原因

- **运行/文档依赖**：`apps/itsm-kaf/itsm`（运行源码副本）、`sslvpn-unified-intake`
  （部署运行手册的 `ITSM_SOURCE`）、`intake-catalog-discovery`（在役二进制源码）、
  `ui-workbench-runtime`（DEV 前端 cwd）、`config-launch-integration`（当前交接目标）、
  `kaf-delegation-transactional-delivery`；
- **harness 管理**：`.claude/worktrees/` 下 5 个，不由 git 处理；
- **未确认作废的 T4（14 个）**：见 §8。

## 8. 未决项

### 8.1 待人工裁定的疑似作废项

`cookie-transport-validation`、`ga-canonical-request-flow`、`workitem-config-migration`、
`config-migration-review`、`database-reconciliation`、`agent-guidance-refactor`、
`sslvpn-runtime-validation`、`bpmn-instance-authorization`、`source-baseline-20260914`、
`candidate-a-remaining-delivery`、`candidate-admission`、`candidate-approval-contract`
以及 `.claude/worktrees/` 下 5 个。

其中已量化的重复关系：`workitem-config-migration` 的 30 个改动文件被
`config-migration-review` 全部包含；`cookie-transport-validation`、
`ga-canonical-request-flow`、`ga-workbench-task-contract` 共享 18 个改动文件。
若后续确认作废，按本文 SOP 处理即可。

### 8.2 本次发现但未处理的两个独立缺陷

1. `.gitignore` 的 `/release/` 规则会匹配任意层级的 `release/` 目录，使
   `itsm-frontend/src/components/release/` 下的新增文件被静默忽略（该目录在 `origin/main`
   已跟踪，故当前只是隐患）。建议改为锚定到仓库根的写法，并单独提交。

2. `docs/migrations/2026-09-14-schema-ledger-reconciliation-plan.md` §3.1 的 D1 结论
   （"`main` 上根本没有 `032–046` 的迁移文件，只存在于候选线 worktree"）**已被推翻**：
   候选线 head `d4bedf7b` 现为 `origin/main` 祖先，`origin/main` 已含 84 个迁移文件
   （含 `047_bpmn_assignment_source`）。该文档应标注为部分 superseded，否则后续 Agent 会
   依据过期前提决策。
