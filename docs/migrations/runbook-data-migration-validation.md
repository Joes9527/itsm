# 数据迁移验证 Runbook（迁移验证工具包）

工具入口：`python3 -m scripts.migration <command> --profile <yaml>`，**在仓库根目录执行**。
设计文档见 [`../superpowers/specs/2026-09-15-migration-validation-toolkit-design.md`](../superpowers/specs/2026-09-15-migration-validation-toolkit-design.md)，
实施计划见 [`../superpowers/plans/2026-09-15-migration-validation-toolkit.md`](../superpowers/plans/2026-09-15-migration-validation-toolkit.md)。

## 1. 准备 profile

以 [`../../scripts/migration/profiles/legacy-itsm-2026-08.yaml`](../../scripts/migration/profiles/legacy-itsm-2026-08.yaml)
为模板，改动三处：

1. **源文件**：`source.dir` 与 `source.files`（`file` / `sha256` / `records` / `id_field`）
2. **目标**：`target.*`（`container`/`user`/`database`，或 `access: dsn` + `dsn_env`）
3. **实体清单**：`entities[]`，每个实体的 `keys`、`filter`、`field_checks`、`structure_checks`

**凭据只写 `from_env` / `from_file`**，绝不写明文。声明了 `sha256` 就必须匹配、声明了 `records`
就必须相符，否则**拒绝运行**——这正是"手上的文件不是当初那份"的检测。

**未知键、未知规则、未知实体、未知结构检查、未知预检、未知写动作一律是加载错误**（fail-closed）。
这条是有意的：拼写错误不会被当成"配置生效了"。

可用的关键字：
- 字段规则：`equals`（默认）、`map`、`rewrite_local_part`、`unresolvable`（显式声明"该项不可比较"）
- 结构检查：`unique_username`、`no_cross_tenant`、`password_is_bcrypt`、`tree_single_root`、
  `no_cycles`、`parents_resolvable`、`prefix_levels`
- 预检：`username_absent`、`email_absent`、`department_resolves`
- 写动作：`create_missing`
- 过滤运算符：`equals`、`in`、`present`

## 2. 五步流程

| 步骤 | 命令 | 退出码含义 |
| --- | --- | --- |
| ① 口径与差异 | `verify --profile p.yaml --evidence-out e.json` | 0 通过；3 有未归因差异 |
| ② 规则是否仍成立 | `verify-profile --profile p.yaml` | 0 成立；4 规则漂移（先查源系统口径） |
| ③ 归因分析 | `derive-map` / `tree` / `discriminate` | 0 正常 |
| ④ 补建 | `backfill --profile p.yaml --entity users`（先 dry-run，再 `--apply`） | 0 完成；2 预检阻塞（拒写） |
| ⑤ 归档 | 把 `e.json` 与结论写入 `docs/migrations/` | — |

**退出码优先级：`1 运行错误 > 2 预检阻塞 > 4 规则漂移 > 3 未归因差异`**。
`3` 表示"有差异且未归因"；差异已记录并接受时用 `--allow-unattributed` 把退出码降为 0，
**但差异仍完整写入证据并以 WARN 列出**，不会被隐藏。

`verify` 默认还会对 profile 里 `lineage` 声明的每个库做同一实体的集合对比（用于判断克隆是否忠实、
是否单批迁移）；某个库不可达或凭据缺失时该项记为 `unavailable` + 原因，不让整次校验失败。
用 `--no-lineage` 跳过。

## 3. 写操作的硬性要求（仅 `create_missing`）

1. profile 里 `write.enabled: true`，且环境变量齐备（管理员口令 + 迁移默认口令），否则拒绝执行
2. **预检阻塞时整批不写**（退出码 2），并列出每条阻塞原因
3. 默认 dry-run；`--apply` 才真正写
4. 执行幂等（键已存在则跳过）、每次成功刷新 CSRF、逐条记录 `http`/`code`/`created_id`
5. 回滚：证据里给出 `created_id` 列表、产品侧停用命令与删除 SQL；执行回滚前先确认目标

## 4. 隐私（产出契约）

- 证据只写**计数、结构与 `sha256:<前8位>` 标识**；不写姓名、不写原始邮箱、不写手机号
- 行级明细写**私有目录**，不入仓
- `report.py` 在写出前自检：命中邮箱 / 手机号（含带分隔符与国家码的写法）/ 工号明文即**拒写**（退出码 1）
- 人读文档中的示例不得用 `sha256:` 标识充当可执行参数，改用占位符

## 5. CI

CI 只跑离线部分，两者都不连库、不联网：

```bash
python3 -m pytest scripts/__tests__ -q
python3 -m scripts.migration self-test
```

真实校验需要导出文件与目标库，按本手册手工执行；执行后同步更新回归锚点
（`scripts/__tests__/test_migration_regression_anchor.py` 会拿新证据与
`docs/migrations/2026-09-15-legacy-migration-validation-evidence.json` 的计数与结构逐项比对）。
锚点基线只在数据确实变化时才重设，并要写明原因。

## 6. 常见差异的处置口径

| 差异形态 | 常见原因 | 处置 |
| --- | --- | --- |
| 仅源有、且源记录缺关键关联字段 | 迁移口径（例如只迁有 HR 关联的账号） | 在 profile 里写清 `filter`，并在报告里记为口径而非缺陷 |
| 仅源有、且各项字段齐备 | 真遗漏 | 用 `backfill` 补建（先 dry-run），补后复跑 `verify` 看差额下降 |
| 仅目标有、且名称在源中出现 | 源快照更旧或口径不同 | 用 `tree` / `discriminate` 归因；对不上再记录为不可复现 |
| 字段级不匹配集中在某个字段 | 该字段的改写规则或源语义变了 | 先跑 `verify-profile` 看是否规则漂移（退出码 4） |
| 计数完全对不上 | 拿错了导出文件 | 核对 `sha256` 与 `records`（profile 会直接拒绝） |
