# B2 字典选项落地执行证据

- 状态：**EXECUTED**（2026-09-14）。目标：`ga-itsm-20260914 / itsm_ga_ready`，租户 `tenant_id=1`。
- 依据：`docs/review/2026-09-14-dictionary-option-reconciliation.md`（第 2 项已采纳）。
- 生成器：`scripts/migrate_config_seed/generate_b2_sql.py`；生成 SQL sha256 `6509ef3b0ddc5737f745ffaff93c51d77aa71b6f469216f519a0d6fbcc872797`（30 行 / 25 条追加）。
- 写入前备份：`itsm_ga_ready-pre-b2.pgdump`，sha256 `48782611c1de88618d0757cb4f2c1bf72a3c9e37c419ba03b4a242f7a6b20383`。
- 契约：备份 → 事务回滚预演 → 单事务执行 → 幂等复跑 → 校验；仅改 `field_definitions.options`。

## 1. 追加的选项（“新增选项”处置）

| 模板 | 字段 | 追加选项（label=value） | 前 → 后 |
| --- | --- | --- | ---: |
| 账号申请 | `target_system` | BMS系统=bms、KAMS=kams、Yonyou=yonyou、FLUX=flux、HR休假系统=hr_leave、K3.5系统=k3_5、SQR系统=sqr | 9 → **16** |
| 业务系统服务申请 | `target_system` | 同上 7 项 | 9 → **16** |
| 通用服务申请 | `target_system` | 同上 7 项 | 9 → **16** |
| 通用服务申请 | `service_type` | 数据导出=data_export、资产采购=asset_purchase | 6 → **8** |
| 邮箱服务申请 | `operation` | 群组邮箱=group_mailbox、公共邮箱=public_mailbox | 5 → **7** |

## 2. 归并处置（不写目标，仅登记映射；语义已由现有选项承载）

| 旧字典组 | 旧值 | 归并目标 |
| --- | --- | --- |
| 系统名称 | KWMS1.0 / KWMS2.0 / KWMS365 | `target_system` = `kwms` |
| 系统名称 | K3/K5 | `target_system` = `k3_5` |
| 请求类型 | 数据修改 | 业务系统服务申请/`operation` = `data_fix` |
| 请求类型 | 信息咨询 | 业务系统服务申请/`operation` = `consult` |
| 请求类型 | 投诉 / 建议 | IT咨询请求/`consult_type` = `feedback` |
| 请求类型 | 密码问题 | 账号申请/`operation` = `reset` |
| 请求类型 | id增删改 | 账号申请/`operation` = `create`/`modify` |
| 请求类型 | 增删改查等服务请求 | 通用服务申请/`service_type`（通用） |
| 邮箱申请类别 | 个人 | 邮箱服务申请/`operation` = `create` |
| 邮箱申请类别 | 共享 | 邮箱服务申请/`operation` = `shared_mailbox` |

## 3. 排除处置（不写目标，不得再纳入）

| 旧字典组 | 旧值 | 原因 |
| --- | --- | --- |
| 请求类型 | 异常、中断或报错 | 事件语义，应走 recordClass=incident / 故障报修模板 |
| 请求类型 | 优化设置 / 下单 / 派单 | 非 IT 服务选项 |
| 请求类型 | 发布或变更请求 | 应映射变更 recordClass/流程 |
| 影响范围 | 严重 / 普通 / 轻微 / 无 | 旧为严重度；seed `impact` 是影响对象范围，语义不同，交优先级/严重度承载 |
| 通用占位 | 其他 / 其它 / 无 | 通用占位值 |

## 4. 校验

- 幂等复跑：`UPDATE 1` 计数 = **0**（含 jsonb 包含性守卫 `f.options @> '[{...}]'`）。
- 仅 `field_definitions` 被更新；未触碰身份、历史、分类、CI、流程与账本。
- Phase 1 不变量（depts 7975 / users 7862）与账本（36）不变；CI 仍 46、分类仍 185。
- 未采纳的 178 组字典保持未接纳（见第 2 项对账表 §3），本次不产生任何新字段。
