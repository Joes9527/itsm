# 数据迁移验证 Runbook（迁移验证工具包）

工具入口：`python3 -m scripts.migration <command> --profile <yaml>`，**在仓库根目录执行**。
状态：只读核验与离线测试已集成；真实补建尚未准入。复用来源为
`5f9877772d5ff6fbca80c4e02871bb4aa86d692a` 的工具包路径，不合并其部署分支。
本工具只处理 users/departments，不替代五批配置对账或数据库迁移器。

## 1. 准备 profile

以 [`../../scripts/migration/profiles/legacy-itsm-2026-08.yaml`](../../scripts/migration/profiles/legacy-itsm-2026-08.yaml)
为历史规则模板，复制到受保护目录；其中目标是占位符，旧规则不是当前批准规则。必须显式传 `--profile`，修改并复核以下内容：

1. **源文件**：`source.dir` 与 `source.files`（`file` / `sha256` / `records` / `id_field`）
2. **目标**：`target.*`（`container`/`user`/`database`，或 `access: dsn` + `dsn_env`）
3. **范围**：`scope: tenant` 必须提供正整数 `tenant_filter`；只有明确全库审计才使用 `full-database`，不能同时提供租户过滤。
4. **实体清单**：`entities[]`，每个实体的 `keys`、`filter`、`field_checks`、`structure_checks`

**凭据只写 `from_env` / `from_file`**，绝不写明文。旧示例保留历史摘要前缀；新批必须填写完整64位SHA256并独立核对。声明了 `sha256` 就必须匹配、声明了 `records`
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
| ④ 补建 | `backfill --profile p.yaml --entity users`（仅 dry-run，`--apply` 阻塞） | 0 完成；2 预检阻塞（拒写） |
| ⑤ 归档 | 把 `e.json` 与结论写入 `docs/migrations/` | — |

**退出码优先级：`1 运行错误 > 2 预检阻塞 > 4 规则漂移 > 3 未归因差异`**。
`3` 表示"有差异且未归因"；差异已记录并接受时用 `--allow-unattributed` 把退出码降为 0，
**但差异仍完整写入证据并以 WARN 列出**，不会被隐藏。

`verify` 默认还会对 profile 里 `lineage` 声明的每个库做同一实体的集合对比（用于判断克隆是否忠实、
是否单批迁移）；某个库不可达或凭据缺失时该项记为 `unavailable` + 原因，不让整次校验失败。
用 `--no-lineage` 跳过。

## 3. 补建当前限制

`backfill --apply` 明确返回2，且不会登录或写入。Cookie客户端已有离线回归测试，
但这不是API目标与只读数据库目标一致的证明。若新克隆身份完整，直接只读verify，无需补建。

恢复apply之前需要独立变更和复核：同数据库/schema/deployment的API绑定证据、可信租户和actor准入、
产品API幂等/并发冲突及未知结果恢复、保护数据的逐条回执与回滚边界、隔离目标真实回归。
不能只移除阻塞分支。dry-run保留预检和计数，输出仅摘要标识及字段名，不输出姓名/邮箱/payload。

数据库读取使用同租户父部门业务键、CSV结构化传输、REPEATABLE READ READ ONLY，
SQL超时30秒、锁超时2秒、进程超时40秒；不同查询/实体仍是不同只读快照，不宣称跨实体原子性。

## 4. 隐私（产出契约）

- 证据只写**计数、结构与 `sha256:<前8位>` 标识**；不写姓名、不写原始邮箱、不写手机号
- 行级明细写**私有目录**，不入仓
- `report.py` 在写出前自检：命中邮箱 / 手机号（含带分隔符与国家码的写法）/ 工号明文即**拒写**（退出码 1）
- 人读文档中的示例不得用 `sha256:` 标识充当可执行参数，改用占位符

## 5. CI

CI 只跑离线部分，两者都不连库、不联网：

```bash
python3 -m pytest scripts/__tests__/test_migration_*.py -q
python3 -m scripts.migration self-test
```

真实回归锚点默认不运行；必须同时显式设置`ITSM_MIGRATION_LIVE_PROFILE`与`ITSM_MIGRATION_LIVE_ANCHOR`，不能复用历史计数冒充新克隆结果。真实校验需要导出文件与目标库，按本手册手工执行；执行后同步更新回归锚点
（`scripts/__tests__/test_migration_regression_anchor.py` 会拿新证据与
`docs/migrations/2026-09-15-legacy-migration-validation-evidence.json` 的计数与结构逐项比对）。
锚点基线只在数据确实变化时才重设，并要写明原因。

## 6. 常见差异的处置口径

| 差异形态 | 常见原因 | 处置 |
| --- | --- | --- |
| 仅源有、且源记录缺关键关联字段 | 迁移口径（例如只迁有 HR 关联的账号） | 在 profile 里写清 `filter`，并在报告里记为口径而非缺陷 |
| 仅源有、且各项字段齐备 | 真遗漏 | 用 `backfill` dry-run形成缺口，真实补建按§3另行准入 |
| 仅目标有、且名称在源中出现 | 源快照更旧或口径不同 | 用 `tree` / `discriminate` 归因；对不上再记录为不可复现 |
| 字段级不匹配集中在某个字段 | 该字段的改写规则或源语义变了 | 先跑 `verify-profile` 看是否规则漂移（退出码 4） |
| 计数完全对不上 | 拿错了导出文件 | 核对 `sha256` 与 `records`（profile 会直接拒绝） |
