# CTI 治理受控启用清单（v1.1 / 2026-09-17）

> 状态：**代码已交付、目标未启用**。本清单是"可审阅的启用计划"，**尚未在任何共享/生产库执行**。
> 没有目标写入授权时只交付清单，不执行任何写入。
>
> 分支 `codex/feat/cti-governance`，PR #48。计划与逐步执行记录见
> `docs/superpowers/plans/2026-09-17-cti-governance.md`。

## 1. 变更内容（启用后会影响什么）

| 能力 | 启用条件 | 影响 |
|---|---|---|
| 分类结构治理（三级/租户内编码唯一/被引用不可删或移/停用不追溯） | 迁移 `048_cti_governance` 应用后立即生效 | 分类维护动作被收紧；不动历史数据 |
| 服务目录默认分类 | 迁移 048 后，目录发布时可选/必填默认分类 | 目录申请由目录默认分类决定，不再询问申请人 |
| **完成质量门禁** | 需在管理端显式启用（保留键 `cti_governance_v1`） | 截止时间之后创建的单据，**关闭**时必须有完整三级分类 |
| 分类纠正留痕 | 迁移 048 后立即生效 | 改分类必须填原因；前后完整路径写入各域既有证据通道 |
| 规则分类范围（仅当前/包含下级） | 迁移 048 后立即生效，**旧规则缺省仍是精确语义** | 只有显式声明 `subtree` 的规则才会命中下级 |
| 分类引用清单 | 迁移 048 后立即生效 | 详情页"关联与引用"；明细按 RBAC 过滤 |

**门禁的生效边界是"每租户一次"的截止时间**：首次启用时写入不可变 `effectiveFrom`；
暂停再启用**不会**重置该时间，因此升级前的在途单据不会被追溯要求。

## 2. 目标与唯一写入者

| 项 | 值（启用时填写） |
|---|---|
| 环境 | ☐ 开发 ☐ 预发 ☐ 生产 |
| 实例 / 容器 | |
| 数据库 / schema | |
| 租户 ID 列表 | |
| 迁移写入者（唯一） | 仅迁移作业；**禁止**人工执行 048 的 SQL |
| 门禁启用人（唯一） | 管理端 `系统配置` 权限持有者，经由 `PUT /api/v1/system-configs/governance/cti` |
| 观察窗口 | 建议 ≥ 1 个完整工作日 |

## 3. 前置检查（在目标库只读执行）

```sql
-- 3.1 迁移是否会通过（048 预检等价查询，只读）
-- a) 租户内重复编码：必须为 0 行
SELECT tenant_id, code, count(*) FROM ticket_categories GROUP BY tenant_id, code HAVING count(*) > 1;
-- b) 层级越界：必须为 0 行
SELECT count(*) FROM ticket_categories WHERE level < 1 OR level > 3;
-- c) 父级缺失或跨租户：必须为 0 行
SELECT count(*) FROM ticket_categories child LEFT JOIN ticket_categories parent ON parent.id = child.parent_id
WHERE child.parent_id IS NOT NULL AND child.parent_id <> 0
  AND (parent.id IS NULL OR parent.tenant_id <> child.tenant_id);
-- d) 层级与父级不连续：必须为 0 行
SELECT count(*) FROM ticket_categories child JOIN ticket_categories parent ON parent.id = child.parent_id
WHERE child.parent_id IS NOT NULL AND child.parent_id <> 0 AND child.level <> parent.level + 1;
-- e) 根节点 level 必须为 1：必须为 0 行
SELECT count(*) FROM ticket_categories WHERE (parent_id IS NULL OR parent_id = 0) AND level <> 1;
-- f) 保留键重复：必须为 0 组
SELECT tenant_id FROM system_configs WHERE key = 'cti_governance_v1' AND deleted_at IS NULL GROUP BY tenant_id HAVING count(*) > 1;
```

> **脏数据必须由分类所有者人工修正**：048 预检会**拒绝迁移**，不会自动截断或合并。
> 预检覆盖：重复编码、层级越界、父级缺失/跨租户、层级不连续、深度超过三级、父链成环、保留键重复。

- ☐ 备份：数据库全量备份完成，记录备份标识与恢复演练结果
- ☐ 独立恢复验证：在隔离实例用该备份恢复并抽样核对（不得只在同一实例上验证）
- ☐ 维护窗口与回滚负责人已确认
- ☐ 确认当前 `schema_migrations` 最新版本与待应用版本

## 4. 迁移执行（唯一写入者）

```bash
# 1) 生成/检查迁移计划
make db-migrate            # 或按 deployments 文档的等价命令
# 2) 应用 048（必须使用部署流水线，禁止手工执行 SQL 片段）
# 3) 校验（048 自带 DO $cti_verify$ 块，仍建议独立复核）
```

迁移后独立复核（只读）：

```sql
-- 租户内唯一索引已建立、旧的全局唯一索引已移除
SELECT indexname FROM pg_indexes WHERE tablename = 'ticket_categories' ORDER BY indexname;
-- 目录默认分类列与外键已存在
SELECT column_name FROM information_schema.columns
WHERE table_name = 'service_catalogs' AND column_name = 'default_ticket_category_id';
SELECT conname FROM pg_constraint WHERE conname = 'service_catalogs_ticket_categories_default_catalogs';
-- 保留键局部唯一索引已存在
SELECT indexname FROM pg_indexes WHERE tablename = 'system_configs' AND indexname = 'system_configs_cti_governance_reserved_key_uq';
-- 迁移台账有回执
SELECT version, checksum FROM schema_migrations WHERE version = '048_cti_governance';
```

- ☑ **2026-09-17 共享 dev 已准备目标实测**：`itsm_config_baseline_20260908` 应用 048 成功
  （预检在真实数据上通过；执行前全量备份 `/tmp/cti-baseline-before-048.sql.gz`）。
  **生产/其他环境仍为 ☐ 未应用。**
- ☐ 上述四项全部符合预期（共享 dev 侧索引与列已核对存在）
- ☐ 分类维护界面：新增/移动/停用/删除各做一次负向验证（被引用必被拒绝）
- ☐ 目录发布界面：默认分类可选且启用状态必填；旧目录显示"未配置默认分类"

## 5. 已发布目录补配默认分类（分阶段）

**规则**：启用状态（`status in ('active','enabled')`）且 `default_ticket_category_id IS NULL` 的目录，
在补配完成前其申请仍不会被门禁追溯；**建议先补配再启用门禁**。

```sql
-- 待补配清单（启用前导出并逐条确认）
SELECT c.id, c.name, c.status, c.tenant_id
FROM service_catalogs c
WHERE c.deleted_at IS NULL AND c.status IN ('active','enabled') AND c.default_ticket_category_id IS NULL
ORDER BY c.tenant_id, c.id;
```

| 目录 ID | 名称 | 租户 | 拟绑定三级分类 ID | 责任人 | 状态 |
|---|---|---|---|---|---|
| | | | | | ☐ |

- ☐ 清单已导出并附在本次启用记录中（不得只写在文档里）
- ☐ 每条补配都指向**完整三级**节点（后端会拒绝部分分类与跨租户节点）
- ☐ 补配通过页面完成（保留发布版本与审计），不使用 SQL 直改

## 6. 分阶段开关与启用顺序

1. ☐ **阶段 1：结构治理**（迁移 048 后自动生效）— 观察 1 个工作日
   - 观察项：分类维护拒绝率、目录发布失败原因、有无"被引用无法停用"的工单
2. ☐ **阶段 2：完成门禁（按租户逐个启用）**
   ```bash
   PUT /api/v1/system-configs/governance/cti
   { "catalogEnforced": true, "completionEnforced": true }
   ```
   - 首次启用写入**不可变** `effectiveFrom`（服务端时间）
   - 先小租户、后大租户；每租户启用后核对 `effectiveFrom` 与审计行
3. ☐ **阶段 3：暂停/恢复演练**
   ```bash
   PUT /api/v1/system-configs/governance/cti
   { "completionEnforced": false }     # 暂停：恢复原行为，但不重置 effectiveFrom
   ```
   - 验证：暂停期间可关闭未分类单据；重新启用后 `effectiveFrom` **不变**，在途单据不被追溯

## 7. 暂停与回退

| 场景 | 动作 | 影响 |
|---|---|---|
| 门禁误伤业务 | 置 `completionEnforced=false` | 立即恢复原行为；不丢数据；截止时间保留 |
| 目录默认分类配错 | 页面调整目录默认分类并重新发布 | 旧确认自动失效（版本冲突），不会按新默认静默建单 |
| 规则范围误配 | 页面把 `scope` 改回"仅当前分类" | 命中集合回到精确语义；规则定义有审计 |
| 需要回滚迁移 | 执行 048 的 `RollbackSQL` | 仅在未启用门禁且未补配目录时可用；**必须先导出分类与目录默认值** |

- ☐ 回退演练已在隔离环境完成（记录耗时与数据核对结论）
- ☐ 回滚前导出：`ticket_categories`（含 level/parent）、`service_catalogs.default_ticket_category_id`、`system_configs` 保留键

## 8. 复验清单（启用后逐项确认）

- ☐ 未分类普通报障仍可提交（"不确定"保留）
- ☐ 目录申请不再询问分类，且工单只存最深节点 ID
- ☐ 事件"恢复服务"不被拦截；关闭未分类事件被拦截并给出可读原因
- ☐ 补齐分类后可关闭；恢复时间/SLA 周期未被改写
- ☐ 工程师改分类必须填原因；证据（前后完整路径）出现在该域既有证据通道
- ☐ 问题/变更保留专业证据字段（根因、计划/回滚等）未被分类操作影响
- ☐ 升级前的在途单据不被追溯要求
- ☐ 规则"仅当前/包含下级"命中符合预期；旧规则命中集合与升级前一致
- ☐ 分类详情"关联与引用"：无权账号只看到"存在引用"，不出现计数与名称
- ☐ 分类移动/停用/删除的负向路径均有可读拒绝原因
- ☐ 审计：`audit_logs` 中存在纠正与启用的回执行；无敏感信息

## 9. 仍未验证/未完成（不得宣称已完成）

- ☐ Playwright 端到端（`tests/e2e/flows/cti-governance.spec.ts`，36 项）**未运行**：需要隔离前端+后端部署与两租户夹具
- ☐ 迁移 048 **未在任何共享/生产库应用**
- ☐ 变更单界面仍无分类字段（属新增界面能力，待产品确认）
- ☐ `disabled`/`default_resolver` 等历史描述字段的界面收敛未做
- ☐ 本清单中的目标列（实例/库/schema/租户/责任人）均为空，需启用时填写

## 10. 证据索引

| 证据 | 位置 |
|---|---|
| 迁移与门禁实现 | `itsm-backend/migration/cti_governance.go` |
| 完成门禁策略与启用记录 | `itsm-backend/service/cti_governance.go` |
| 纠正契约 | `itsm-backend/service/cti_correction.go` |
| 引用扫描与授权视图 | `itsm-backend/service/ticket_category_references.go`、`ticket_category_reference_view.go` |
| 真实 PostgreSQL 用例（8 套） | `itsm-backend/tests/integration/cti_*_postgres_test.go` |
| 执行记录与逐步证据 | `docs/superpowers/plans/2026-09-17-cti-governance.md` |
