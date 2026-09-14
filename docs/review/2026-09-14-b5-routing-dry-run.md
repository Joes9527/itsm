# B5 路由批次 dry-run（**未写目标**）

- 状态：**dry-run 完成；不写入**。目标 `ticket_assignment_rules` 当前 0 行。
- 日期：2026-09-14。目标：`ga-itsm-20260914 / itsm_ga_ready`，租户 `tenant_id=1`。
- 依据：`cti_authorized.json`（720 行）、`docs/review/2026-09-14-cti-mapping-worksheet.md`、KAF `fetch_legacy_identity_objects.py` 解析结果。

## 1. 目标规则的能力边界（代码证据）

| 维度 | 目标支持 | 旧路由字段 | 结论 |
| --- | --- | --- | --- |
| 条件字段 | `status` / `priority` / `category_id` / `department_id` / `requester_id` / `assignee_id`（`service/ticket_rule_conditions.go`） | `ctiId`、`definitionId`、`taskDefKey`、`moduleId` | **旧维度均不可表达**，只能按 `category_id` 近似 |
| 动作 | `user` / `round_robin` / `load_balance`（均为**用户 id**，`service/ticket_assignment_rule_service.go`） | `authorizedType/authorizedId`（角色或用户） | **角色动作不支持**；用户动作需旧→新用户 id 映射 |
| 命中语义 | `priority` 降序 + `id` 升序，**首条命中即返回** | — | 需显式优先级排序 |

## 2. 可表达性量化

- 路由总数：**720**；去重 `ctiId`：**60**。
- 落在**已映射目标分类**的 `ctiId` 上：**14** 条（4 个 ctiId）。
- 落在**其它** `ctiId`（业务系统/排除项等）：**706** 条 → **无对应条件字段，不可表达**。
- 按 `authorizedType`：type=2 用户 **638** 条（100 个 id，其中 **20 个未解析**）；type=0 角色 **82** 条（**角色动作不支持**）。
- **孤立 `ctiId`**（不在旧 CTI 树内）：**14** 个 → **118** 条路由，**无目标**。

> 结论：**最多约 14 / 720 条**路由可映射为「分类 + 用户」规则，且仍依赖旧→新用户 id 映射；其余 **706** 条在当前规则模型下**无法表达**。

## 3. 可表达子集（逐条，供审查）

| 旧 ctiId | 旧节点名 | 目标分类 | definitionId | taskDefKey | authorizedType | authorizedId |
| --- | --- | --- | --- | --- | --- | --- |
| `8c81b8d98ed5470db217389d8436f66c` | AD账户申请（Windows账户） | `ACC-AD-001` | `orderRequest:31:5565134` | handleWith | 2 | `0b2a73c5c1724e66a84445f63ad62307` |
| `5df87242c8594dfa903290bbde570438` | O365邮箱导出申请 | `新建分类之一（COL-MAIL-004/ACC-LCM-003/APP-GEN-SVC-001）` | `orderRequest:31:5565134` | handleWith | 2 | `1d69f343f0d64edc97fe6f1a319c86f3` |
| `f01241a4136d455680999c8759e94283` | O365邮箱账户申请 | `COL-MAIL-001` | `orderRequest:31:5565134` | handleWith | 2 | `6be607160f8d493fb4bbddf269beb197` |
| `8c81b8d98ed5470db217389d8436f66c` | AD账户申请（Windows账户） | `ACC-AD-001` | `orderRequest:31:5565134` | handleWith | 2 | `0b2a73c5c1724e66a84445f63ad62307` |
| `f01241a4136d455680999c8759e94283` | O365邮箱账户申请 | `COL-MAIL-001` | `orderRequest:31:5565134` | handleWith | 2 | `6be607160f8d493fb4bbddf269beb197` |
| `8c81b8d98ed5470db217389d8436f66c` | AD账户申请（Windows账户） | `ACC-AD-001` | `orderRequest:31:5565134` | handleWith | 2 | `6be607160f8d493fb4bbddf269beb197` |
| `8c81b8d98ed5470db217389d8436f66c` | AD账户申请（Windows账户） | `ACC-AD-001` | `orderRequest:31:5565134` | handleWith | 2 | `caee46022e214cae91edc046a9790957` |
| `f01241a4136d455680999c8759e94283` | O365邮箱账户申请 | `COL-MAIL-001` | `orderRequest:31:5565134` | handleWith | 2 | `0b2a73c5c1724e66a84445f63ad62307` |
| `05fae6bef2aa40ba90218daaf33ebd43` | SSLVPN账号申请 | `NET-VPN-001` | `orderRequest:31:5565134` | handleWith | 2 | `eeff9d26c57749d49f97af751690557b` |
| `f01241a4136d455680999c8759e94283` | O365邮箱账户申请 | `COL-MAIL-001` | `orderRequest:31:5565134` | handleWith | 2 | `0b2a73c5c1724e66a84445f63ad62307` |
| `f01241a4136d455680999c8759e94283` | O365邮箱账户申请 | `COL-MAIL-001` | `orderRequest:31:5565134` | handleWith | 2 | `caee46022e214cae91edc046a9790957` |
| `8c81b8d98ed5470db217389d8436f66c` | AD账户申请（Windows账户） | `ACC-AD-001` | `orderRequest:31:5565134` | handleWith | 2 | `6be607160f8d493fb4bbddf269beb197` |
| `f01241a4136d455680999c8759e94283` | O365邮箱账户申请 | `COL-MAIL-001` | `orderRequest:31:5565134` | handleWith | 2 | `0b2a73c5c1724e66a84445f63ad62307` |
| `8c81b8d98ed5470db217389d8436f66c` | AD账户申请（Windows账户） | `ACC-AD-001` | `orderRequest:31:5565134` | handleWith | 2 | `0b2a73c5c1724e66a84445f63ad62307` |

## 4. 阻塞项与需决策

1. **维度缺失**：`definitionId`/`taskDefKey`/`moduleId`/流程版本无对应字段 → 707 条业务系统类路由无法表达；需产品侧决定是否扩展规则条件或改用流程绑定承载。
2. **角色动作缺失**：82 条 `authorizedType=0`（角色）无法作为动作；需扩展 `role` 动作或映射为具体用户组。
3. **旧→新用户 id 映射缺失**：100 个用户 id 中 20 个在旧 `sysUser` 无法解析；其余需 Phase 1 身份映射支持（当前无可用对外映射表）。
4. **14 个孤立 ctiId（118 条）**：目标无对应节点，需确认删除/归并。

## 5. 本批无写入确认

- 未对 `ticket_assignment_rules` 或任何目标表执行写入。
- 目标不变量保持：depts 7975 / users 7862 / roles 36 / ledger 36 / CI 46 / 分类 185 / 流程绑定 7。
