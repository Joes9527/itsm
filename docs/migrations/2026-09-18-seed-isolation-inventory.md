# 新 ITSM seed / 测试数据隔离清单（`itsm_migration_20260914`）

- 日期：2026-09-18
- 状态：**清单与只读盘点（proposed）**。本文件是隔离方案的输入，**不是删除授权**。
- 目标库：`itsm_migration_20260914`（当前 Dev 的可证来源克隆，见[克隆证据](./2026-09-18-clone-identity-evidence.md)）
- 分类器：`scripts/migration/seed_isolation_plan.py`；回归测试：`scripts/__tests__/test_seed_isolation_plan.py`（3/3 通过）
- 隐私：本文件只写**计数、角色与不可逆标识**（`sha256:<前8位>`），不写姓名、邮箱、手机号、工号。行级明细用第 6 节的 SQL 现场重放，不入仓。

## 1. 为什么必须清

目标库是从 Dev 克隆的，因此**同时带着 Dev 的开发期数据**：产品种子部门、测试与脚手架账号、验证用服务目录/流程定义，以及 41 个验证工单。这些对象与即将迁入的旧 ITIL 真实组织/人员数据混在一起后，会出现"同名部门两套""真人挂着测试角色""测试工单污染统计"等问题，必须在迁移前隔离。

## 2. 盘点结果

| 类别 | 数量 | 处置 |
| --- | --- | --- |
| A. 产品种子部门（id 1–14） | **14** | 隔离（打标，不参与业务树） |
| B. 测试 / 脚手架账号 | **35** | 隔离（停用或重命名打标），**不删除** |
| C. 测试审批组 | **3** | 隔离（保留供流程引用重定向） |
| D. **角色被污染的真实员工** | **3** | **纠正角色**，绝不删除 |
| E. 验证用服务目录 | **5** | 待定（第 5 节） |
| F. 验证用流程定义 | **8** | 待定（第 5 节） |
| G. 验证工单 / 流程实例 | **41 / 40** | 待定（第 5 节） |

B + D = 38，与 `users.role <> 'end_user'` 的总数一致（A/B/C/D 互不重叠）。

### 2.1 A. 产品种子部门（14）

id 1–14：`IT / IT-INFRA / IT-APP / IT-SEC / IT-PMO / OPS / OPS-SD / OPS-NOC / OPS-SEC / RD / QA / HR / FIN / ADMIN`。

**引用面实测（关键）**：

| 检查 | 结果 |
| --- | --- |
| 属于这 14 个部门的用户 | **0** |
| 落在这些部门的工单 | **0** |
| 以它们为父节点的子部门 | **0** |

→ 这 14 个节点**完全无引用**。按设计仍然只做隔离（打标），是否物理移除留待单独授权；因为无引用，届时风险极低。

### 2.2 B. 测试 / 脚手架账号（35）

按形态分三组：

| 形态 | 数量 | 说明 |
| --- | --- | --- |
| `qa_*` 角色测试账号 | 4 | `qa_workspace_l1`、`qa_manager_ops`、`qa_executive_itd`、以及 `*_test` 类 |
| `*_test` / `*_closeout_*` / `admin` | 5 | `admin`、`supervisor_test`、`lixin_test`、`it_director_test`、`kaf_closeout_t1/t2` |
| `ui-runtime-*` / `ui-core-journey-*` / `ui-lifecycle-*` / `engineer-workspace-*` 脚手架 | 26 | **全部 `active=false`**，UI/E2E 用例生成物 |

**引用面实测**：

| 检查 | 结果 |
| --- | --- |
| 由这些账号提单 | **18 / 41** |
| 指派给这些账号的工单 | **23 / 41** |
| 指派给这些账号的流程任务 | **32** |

→ 被大量开发期单据引用，**因此只能隔离（停用/打标），不能删除**：删除会打断既有工单与任务的外键与审计链。

### 2.3 C. 测试审批组（3）

`ticket-approvers`、`dept_manager`、`network_eng`。三者都是**单成员脚手架**，用于早期验证"固定角色/组"审批方式。迁入真实负责人体系后需要重定向到真实组，不能保留为审批落点。

### 2.4 D. 角色被污染的真实员工（3）——**这一类最危险**

| 标识 | 被改成什么角色 |
| --- | --- |
| `sha256:b52ecfae` | `dept_manager` |
| `sha256:4668efcf` | `network_eng` |
| `sha256:8d8081f7` | `sysadmin` |

这三条是**真实员工账号（工号形态 `D` + 数字）**，不是测试账号；它们的角色被开发期用例改成了测试角色。其中 `network_eng` 那位正是 SSL-VPN 审批流里"网络运维复审"的落点人物。

**处置：纠正角色为 `end_user`（或按业务确认的真实角色），保留账号与全部历史引用。严禁归入 seed 删除名单。**

> 计划原文的分类器会把"所有非 `end_user` 账号"整体视为 seed，这会把这 3 个真人一并列进待隔离清单。分类器已按"工号形态不参与 seed 判定"修正，并新增回归测试
> `test_real_employees_with_test_roles_are_flagged_not_disposed` 固定该行为。

## 3. 分类器的输出（对真实库数据运行）

```
departments (A)            : 14
users (B)                  : 35
groups (C)                 : 3
polluted_employee_roles (D): 3
```

## 4. 隔离方式（不删除）

| 类别 | 做法 |
| --- | --- |
| A 种子部门 | 打标隔离，不进入业务树；迁移时不参与节点对齐 |
| B 测试账号 | 停用 + 打标；保留行与历史引用 |
| C 测试组 | 打标；审批配置重定向到真实组后才可停用 |
| D 污染真人 | **纠正角色**，无隔离动作 |

隔离标记必须进入**版本化迁移**（canonical migration），不能只改本机数据库——否则换库即失效。

## 5. 待定：验证用目录 / 流程 / 工单（E/F/G）

已识别的候选：

- 服务目录 5 个：含"Task11验证""SR统一验证×2""E2E测试-Incident分支验证""SSL-VPN 远程办公访问权限申请（WSL 专项验证）"。
- 流程定义 8 个：`process_1787042003593`、`process_1787532226525`、`s6_unsupported_flow`、`s8_complete_request_flow`、`s8_reject_request_flow`、`s8_update_request_flow`、`sdd_assignee_dept_test`、`sdd_assignee_role_test`。
- 工单 41 个、流程实例 40 个。

**尚未决定处置**，因为需要先确认：

1. SSL-VPN 相关目录/流程是**唯一在用的业务路径**（切片验证要用），不能删；应保留并区分"WSL 专项验证"副本。
2. 其余验证工单/实例是否需要在迁移验收中作为对照样本保留。
3. 删除动作需要独立授权，且必须先确认无外键与审计依赖。

## 6. 现场重放（行级明细不入仓）

```sql
-- 部门（A）
SELECT id, code, name FROM departments WHERE id BETWEEN 1 AND 14 ORDER BY id;

-- 账号（B/D 的区分由分类器完成：工号形态 ^D\d+$ 归 D，其余归 B）
SELECT username, role, active FROM users WHERE role <> 'end_user' ORDER BY role, username;

-- 组（C）
SELECT id, name FROM groups ORDER BY id;

-- 引用面
SELECT count(*) FROM users      WHERE department_id BETWEEN 1 AND 14;
SELECT count(*) FROM tickets    WHERE department_id BETWEEN 1 AND 14;
SELECT count(*) FROM departments WHERE parent_id    BETWEEN 1 AND 14;
SELECT count(*) FROM tickets t JOIN users u ON u.id = t.requester_id WHERE u.role <> 'end_user';
SELECT count(*) FROM tickets t JOIN users u ON u.id = t.assignee_id  WHERE u.role <> 'end_user';
SELECT count(*) FROM process_tasks pt JOIN users u ON u.id::text = pt.assignee WHERE u.role <> 'end_user';
```

## 7. 未完成 / 需授权

- 执行隔离写入（打标/停用/纠正角色）需要单独授权，且必须与旧数据迁移同批、可回滚。
- E/F/G 的处置决定。
- 隔离标记落进 canonical migration 的具体版本号，随《部门》/《人员》计划一并确定。
