# 迁移验证工具包 v1 — 设计（Migration Validation Toolkit）

- **状态**：`accepted`（2026-09-15 经用户逐节确认）
- **日期**：2026-09-15
- **背景来源**：[`../migrations/2026-09-15-legacy-migration-validation-report.md`](../../migrations/2026-09-15-legacy-migration-validation-report.md)、
  [`../migrations/2026-09-15-legacy-migration-input-and-rule-archive.md`](../../migrations/2026-09-15-legacy-migration-input-and-rule-archive.md)、
  [`../review/2026-09-15-environment-and-migration-closure.md`](../../review/2026-09-15-environment-and-migration-closure.md)

## 1. 目标

2026-08-19 的旧 ITSM 主数据迁移只被验证过一次，用的是**一次性脚本 + /tmp 分析脚本**。
目标是把这次的方法固化为**可复用能力**：下一次同源迁移（不同批次/环境/导出文件）只需写一份
profile 即可完成「口径确认 → 集合匹配 → 差异归因 → 字段/结构校验 → 必要时补建 → 证据归档」。

**复用目标**（用户 2026-09-15 裁定）：**同源系统（旧 ITSM → 新 ITSM）的后续批次**。
不引入源系统适配层；不同源系统不在 v1 范围内。

## 2. 范围

**做**
- 可配置的**实体清单**（v1 内置 `users`、`departments` 两个实体；新增实体=新增模块）
- 声明式 profile（YAML）驱动：源文件与哈希、目标库/接口坐标、过滤口径、字段规则、结构检查、写操作
- 三个分析模式：`derive-map`（从已迁移行反推映射）、`tree`（层级/前缀/路径分段归因）、
  `discriminate`（已迁 vs 未迁的判别字段）
- **补建**写操作（受 profile 驱动，默认关闭）：dry-run → 预检 → 执行 → 验收 → 证据与回滚
- 统一证据 JSON + 文本摘要；离线自检；离线单元测试；runbook 与开发文档收录

**不做**
- 其它源系统的适配层（不同导出格式/字段名）
- `create_missing` 之外的写动作（更新、删除、字段修正）
- 真实校验进 CI（需要导出文件与目标库；CI 只跑离线自检与单元测试）
- 工单/服务请求/知识/CMDB 实体（v1 只留扩展位）
- 承担迁移本身的执行（本工具只做验证与补建）

## 3. 关键决策（含出处）

| 决策 | 取值 | 出处 |
| --- | --- | --- |
| 复用目标 | 同源系统后续批次 | 用户 2026-09-15 |
| 实体范围 | 可配置实体清单，v1 实现 users + departments | 用户 2026-09-15 |
| 写操作 | 包含补建，受 profile 驱动 | 用户 2026-09-15 |
| 结构方案 | 方案 B：包 + 实体插件 | 用户 2026-09-15 |
| profile 格式 | YAML（PyYAML 6.0.3 已在环境内） | 本设计 §5 |
| CI 范围 | 仅离线（`self-test` + 单元测试） | 本设计 §9 |

## 4. 架构与职责

```
scripts/
  __init__.py                 # 使 scripts 成为可导入包（新增，空文件）
  migration/
    __init__.py
    profile.py                # 载入并校验 profile；未识别规则/实体 → 加载即失败
    sources.py                # 读导出文件：哈希与条数校验、按源键建索引
    target.py                 # 目标库只读访问（docker exec psql，BEGIN READ ONLY）+ 产品 API 客户端（仅写操作用）
    checks/
      __init__.py             # 实体注册表 REGISTRY: name -> EntityCheck
      base.py                 # EntityCheck 协议 + ReconcileResult / WriteIntent 数据结构
      users.py
      departments.py
    analyze.py                # derive-map / tree / discriminate
    backfill.py               # 写操作五步（§8）
    report.py                 # 证据 JSON + 文本摘要
    __main__.py               # CLI 入口（python3 -m scripts.migration <command> …）
```

**依赖方向**：`__main__` → (`profile`, `sources`, `target`, `checks`, `analyze`, `backfill`) → `report`。
下层不反向调用；`checks/*` 不直接读文件或连库，只接收 `sources`/`target` 提供的数据。

**各单元一句话职责**
- `profile`：把 YAML 变成校验过的不可变配置；未知键/未知规则/哈希不符 → 报错退出
- `sources`：把导出文件变成「按源键索引的记录字典」并核对哈希与条数
- `target`：只读取数；写操作时提供产品 API 客户端（登录、CSRF、POST）
- `checks/*`：一个实体一个模块，实现 §6 接口
- `analyze`：三个可独立运行的归因分析
- `backfill`：唯一允许写库（实际是写产品接口）的模块，实现五步纪律
- `report`：统一输出形状，供人读与机读

## 5. Profile schema

```yaml
version: 1
name: legacy-itsm-2026-08
description: 旧 ITSM 主数据 → 新 ITSM（克隆库）迁移校验

source:
  dir: /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/data
  files:
    departments: {file: itsm_departments.json, sha256: f8b3fbda…, records: 5272, id_field: departmentId}
    users:       {file: itsm_users.json,       sha256: fe794d35…, records: 14393, id_field: userName}

target:
  access: docker                       # docker | dsn（v1 两种都实现；docker 为本环境已验证方式）
  container: ga-itsm-20260914          # access=docker 时必填
  user: ga_owner
  database: itsm_ga_ready
  credential: {from_env: ITSM_TARGET_DB_PASSWORD}
  # access=dsn 时改为：{dsn_env: ITSM_TARGET_DSN}（psql 客户端，DSN 由环境变量提供）
  api_base: http://localhost:3010      # 仅写操作需要
  api_credential: {user_env: ITSM_ADMIN_USER, from_env: ITSM_ADMIN_PASSWORD}

lineage:                               # 可选；只读对照，用于判断是否单批/克隆忠实
  - {label: "itsm (DEV)", container: itsm-postgres-dev, user: itsm_user, database: itsm,
     credential: {from_env: DEV_DB_PASSWORD}}
# lineage 某项凭据缺失或不可达时：跳过该项并在证据里记 unavailable + 原因，不让整个校验失败

entities:
  - name: users
    source: users
    target_table: users
    keys: {source: userName, target: username}
    filter: {source_field: status, op: equals, value: userstatus01}   # v1 支持 op: equals | in | present
    field_checks:
      - {name: name,    source: realName, target: name}
      - {name: email,   source: email,    target: email, rule: rewrite_local_part, domain: keas.kln.comm}
      - {name: active,  source: status,   target: active, rule: map,
         map: {userstatus01: true, userstatus04: false}}
      - {name: manager, source: leaderId, target: manager_id, rule: unresolvable}
    structure_checks: [unique_username, no_cross_tenant, password_is_bcrypt]
    write:
      enabled: false                    # 默认关闭；必须显式改为 true 才允许写
      action: create_missing
      endpoint: POST /api/v1/users
      fields:
        username: userName
        name: realName
        email: "rewrite:keas.kln.comm"
        departmentId: "department_by:departmentUnit"
        role: end_user
        tenantId: 1
      password: {from_env: MIGRATED_DEFAULT_PASSWORD}
      preflight: [username_absent, email_absent, department_resolves]
      rollback: {disable: "PUT /api/v1/users/:id/status"}

  - name: departments
    source: departments
    target_table: departments
    keys: {source: departmentId, target: code}
    structure_checks: [tree_single_root, no_cycles, parents_resolvable, prefix_levels]
    analysis: [tree, prefix_closure, path_segment_attribution]
```

**字段规则（`field_checks[].rule`）v1 支持四种**
`equals`（直接相等）、`map`（源值→目标值映射表）、`rewrite_local_part`（保留局部名、替换域名，
后缀插入局部名的情况单独判定）、`unresolvable`（**显式声明该项不可解析**，证据里记为已知差异而非缺陷）。
不写 `rule` 时默认 `equals`。

**写操作 `write.fields` 取值文法（v1）**
`<源字段名>`（直接取源值）｜`"rewrite:<domain>"`（按 §字段规则改写邮箱）｜
`"department_by:<源字段>"`（用该源字段的 code 查目标部门 id）｜`<字面量>`（如 `end_user`、`1`）。
不支持的写法在加载时报错。

**校验规则（加载时执行，任一失败即退出并说明原因）**
1. `version` 必须为已支持版本；未知顶层键报错（防止拼写错误被静默忽略）
2. `source.files` 中声明的文件必须存在；`sha256` 声明则**强校验**；`records` 声明则核对条数
3. 每个 `entities[].name` 必须在实体注册表中；否则报错并列出可用实体
4. `field_checks[].rule` 与 `structure_checks`、`preflight`、`write.action` 必须是已实现的关键字；
   未知关键字 → 报错（fail-closed，不允许"提出没实现的规则"）
5. 凭据一律通过 `from_env` / `from_file` 解析；解析不到 → 拒跑并提示所需变量名；**任何日志与证据都不含凭据值**
6. `write.enabled: true` 时，`endpoint`、`fields`、`password`、`preflight` 缺一即报错

## 6. 实体插件接口

```python
class EntityCheck(Protocol):
    name: str
    def load_source(self, src: SourceIndex, profile: MigrationProfile) -> dict[str, dict]: ...
    def fetch_target(self, target: Target, profile: MigrationProfile) -> list[dict]: ...
    def reconcile(self, src: dict[str, dict], tgt: list[dict]) -> ReconcileResult: ...
    def check_fields(self, src, tgt) -> dict: ...
    def check_structure(self, tgt) -> dict: ...
    def render_write_plan(self, result: ReconcileResult, profile) -> list[WriteIntent]: ...
```

`ReconcileResult` 固定包含：`matched`、`only_source`（可按过滤口径分组）、`only_target`
（按 profile 的属性分桶 + 无法归因的条目）、可选 `alternative_keys`（如按邮箱再匹配的结果）。
`WriteIntent` 固定包含：`key`、`payload`（不含口令）、`preflight`、`rollback`。

**内置实体**
- `users`：键 `userName ↔ username`；字段检查 name/email/active/manager；结构检查唯一性、租户、bcrypt；
  支持 `create_missing`
- `departments`：键 `departmentId ↔ code`；结构检查单根/无环/父可解析/前缀层级；
  支持 tree、prefix_closure、path_segment_attribution 分析

## 7. CLI 与分析模式

```
python3 -m scripts.migration verify        --profile p.yaml [--evidence-out e.json] [--no-lineage]
python3 -m scripts.migration derive-map    --profile p.yaml [--entity users]
python3 -m scripts.migration tree          --profile p.yaml --entity departments
python3 -m scripts.migration discriminate  --profile p.yaml --entity users
python3 -m scripts.migration backfill      --profile p.yaml [--entity users] [--apply]
python3 -m scripts.migration self-test
```

- **默认只读**；唯一写入口是 `backfill --apply`，且要求 `write.enabled: true` 与环境变量口令齐备
- `derive-map` 输出：候选规则 + 在"已迁移行"上的命中率（用于把映射从数据里推导出来，而不是猜）
- `tree` 输出：根/深度分布/深度与键长对应关系/前缀闭包覆盖/路径分段归因
- `discriminate` 输出：区分"已迁 vs 未迁"的字段及其分布（本次据此定位到 `HR_USERID` 口径）
- `self-test`：合成 fixture 跑通纯函数路径，**不连库、不联网**
- **退出码**：`0` 通过 ｜ `1` 运行错误 ｜ `2` 预检阻塞（拒写） ｜ `3` 存在未归因差异
- `--allow-unattributed` **只把退出码 3 降为 0**；未归因差异仍**完整写入证据 JSON**
  （不得隐藏），并在文本摘要里以 `WARN` 列出——用于"差异已记录并接受"的场景

## 8. 写操作五步纪律（v1 仅 `create_missing`）

1. **预检（只读）**：逐条核验 `preflight` 声明的条件（键不存在、邮箱不冲突、部门可解析…），
   任一失败 → 退出码 2 并列出阻塞项，**不执行任何写操作**
2. **dry-run（默认）**：打印将要发送的 payload 清单（不含口令）与目标解析结果，退出码 0
3. **执行（`--apply`）**：逐条调用产品接口；幂等（键已存在则跳过并记为 skipped）；
   每次成功后刷新 CSRF；逐条记录 `http`/`code`/`message`/`created_id`
4. **验收**：目标库复核（条数、字段、绑定）+ 抽样登录 + 复跑 `verify` 证明差额下降
5. **证据与回滚**：证据 JSON 记录 payload（不含口令）、结果、`created_id`、回滚命令
   （产品侧停用 + SQL 删除），并写入 `docs/migrations/` 归档

## 9. 测试与 CI

- **离线单元测试**：`scripts/__tests__/test_migration_*.py`（pytest）
  - profile 校验：未知键/未知规则/哈希不符/缺凭据 → 必须报错
  - 规则分派：email `rewrite_local_part`（后缀在局部名内）、`map`、`unresolvable`
  - `discriminate` 的判别逻辑、`tree` 的前缀/路径分段归因、写计划生成（不执行）
- **`self-test`**：合成 fixture 覆盖 verify（users+departments）、derive-map、tree、discriminate 的纯函数路径
- **CI**：只跑 `self-test` 与上述单元测试（离线）。真实校验不进 CI，按 runbook 手工执行
- **回归锚点**：用 2026-08 的真实 profile 运行 `verify`，关键数值与
  `docs/migrations/2026-09-15-legacy-migration-validation-evidence.json` 逐项一致
  （差集、绑定命中、bcrypt 计数、结构检查）；该比对作为验收脚本的一部分运行一次并留证

## 10. 迁移路径与文档

- 把 `scripts/verify_itsm_migration_data.py`、`scripts/backfill_legacy_users.py` 的已验证逻辑
  **迁入包内并删除旧脚本**（AGENTS.md：新路径取代旧路径须同批移除），同步更新引用
- 新增 `docs/migrations/runbook-data-migration-validation.md`：profile 怎么写、五步怎么写、
  CI 跑什么、退出码含义、常见差异的处置口径
- 更新 `docs/DEVELOPMENT_GUIDE.md`（及命令参考）收录命令入口

## 11. 风险与未决

| 风险/未决 | 处置 |
| --- | --- |
| profile 依赖 PyYAML | 环境已验证 6.0.3 存在；runbook 记录依赖 |
| 目标库/接口坐标随环境变化（8080/3010、容器名） | 全部写进 profile；不写死在代码里 |
| 未识别规则会让用户"以为配了就生效" | 加载即失败（fail-closed），并列出可用关键字 |
| 补建误用（未预检就写） | 默认 dry-run；`--apply` 需 `write.enabled: true` + 环境变量；预检阻塞即拒写 |
| v1 只覆盖两个实体 | profile 与注册表已留扩展位；新增实体=新增模块，不改核心 |
| 真实校验无法进 CI | 以 runbook + 回归锚点替代；CI 只保证离线部分 |

## 12. 验收标准

1. `self-test` 在无数据库、无网络环境下通过，并可被 CI 执行
2. 上述离线单元测试全部通过
3. 以 2026-08 的真实 profile 运行 `verify`，关键数值与既有证据逐项一致（见 §9 回归锚点）
4. `backfill --apply` 在缺少环境变量口令时**拒绝执行**；在数据已补全时 dry-run 报告"0 条待建"
5. 旧脚本已删除，代码与 runbook 中不再出现旧路径（历史报告保留引用并加一行指向新入口）
6. runbook 与开发文档已收录命令入口
