# 旧 ITSM / KAF EHR / 新 ITSM 数据对账与全景审计

- 日期：2026-09-18
- 状态：**只读对账记录，非迁移授权、非目标启用证据**。本记录不改变任何产品契约，也不构成对共享库、目标库或旧系统的写操作授权。
- 范围：组织/人员主数据、领导与汇报线、流程与路由、分类/字典/SLA/优先级/模块的"旧→新"完整性核对。
- 对接：`AGENTS.md`（单一权威、fail-closed、迁移/兼容边界）、[WorkItem 契约](../../AGENTS.md)、[2026-09-14 差异报告](../migrations/2026-09-14-legacy-vs-new-itsm-diff.md)、[旧配置迁移 GAP 设计](../superpowers/specs/2026-09-14-legacy-config-migration-gap-and-solution-design.md)。

## 0. 结论摘要

1. **新 ITSM 的组织与人员来自 eHR，不是来自旧 ITSM。** 当前 baseline 的 7975 部门 / 7871 用户与 eHR 导出（org 7961 + 14 产品种子、person USR 7825）对齐；旧 ITSM 的 9014 活跃用户与 5202 部门并未落入当前库。
2. **旧 ITSM 的直属领导（`leaderId`）与领导标记（`whetherLeader`）没有迁移，且新 ITSM 的 `manager_id` 与旧 ITSM 存在 614 条缺空 + 404 条冲突。** 这是审批路由断流的直接数据根因之一。
3. **旧 ITSM 的部门负责人同样稀疏（205/5202 = 3.9%），能回填新库的仅 164 条。** 当前新库 `departments.manager_id` 只有 1 条，且该值不是分公司负责人。因此"全量回填部门负责人"在两侧数据上都不成立。
4. **人员组织放置粒度存在结构性差距**：eHR 把员工放在 **175 个 9 位分公司级节点**，旧 ITSM 放在 **2657 个叶子节点**（平均 18.3 字符）。新库忠实导入了 eHR，因此只知道分公司级归属。
5. **旧 ITSM 生产环境的流程/路由/分类/字典与既有 2026-09-14 报告口径不同**：本次从生产环境重新抓取为流程定义 144 / 模型 23、路由 **1085**、CTI **107**、部门 5202、节假日 960；既有报告基于 test 环境（145/24、720、82、3175、611）。**G1–G9 台账数量需按生产口径重算。**
6. **路由可迁移性是最大功能缺口**：旧 ITM 1085 条 CTI 授权只落在 **4 个流程定义**（orderRequest 634、sj002 321、knowledgeProcess 128、SJ001 2）的 **11 个 taskDefKey** 上；新库 `ticket_assignment_rules = 0`，即旧派单路由 **0% 迁移**。其中 97 条（23 个授权对象 id）在当前旧系统身份表中已查无此人/角色，属失效引用。
7. 数据修复必须先在"权威口径"上做决策（eHR vs 旧 ITSM；eHR 是否细化到叶子部门），否则任何回填都会改写业务语义。

风险分级（沿用 `product-feature-validation` 口径）：P0 安全/租户/数据丢失；P1 主流程实质错误；P2 次要流程或重要缺陷；P3 打磨项。

---

## 1. 数据源、证据与边界

### 1.1 本次实际读取的数据源

| 侧 | 位置 / 端点 | 规模 | 抓取/观测时间 |
| --- | --- | --- | --- |
| 新 ITSM（对照样本） | PostgreSQL `itsm_config_baseline_20260908`，`tenant_id=1` | 部门 7975；用户 7871；`ticket_categories` 183；`process_definitions` 59（tenant 1）+19（tenant 2）；`process_bindings` 36；`process_deployments` 42；`ticket_assignment_rules` 0；`ticket_automation_rules` 0；`sla_definitions` 14；`field_definitions` 137 | 2026-09-18 |
| 旧 ITSM 生产配置/流程/路由 | `https://keas-itsm.gazellio.com`，只读 `list.do`；dump 落 `/home/administrator/project/kaf/data/legacy_itsm/prod/`（gitignored） | 10/10 资源，全部 `rows == total`，见附录 A | 2026-09-18T00:58:36Z |
| 旧 ITSM 生产身份对象（只读解析） | `/sysUser/list.do` / `/sysRole/list.do` / `/sysGroup/list.do`；原始 dump 落 `data/legacy_itsm/identity/prod/`（gitignored） | sys_user 14478；sys_role 28；sys_group 31；仅输出 id/计数摘要 | 2026-09-18 |
| 旧 ITSM 人员/组织主数据 | `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/data/itsm_users.json`（14393）、`itsm_departments.json`（5272） | 生产环境抓取 | 2026-08-19 / 2026-08-04 |
| KAF EHR | `kaf_config_baseline_20260908` / `kaf_baseline_20260908` 的 `md_ehr_person` / `md_ehr_org` / `md_ehr_post` | 表存在但**均为 0 行**；`ehr_sync_log` 仅 2 条（2026-08-06/07） | 2026-09-18 |
| eHR 导出 | `service-support/ehr-data.xlsx`（person 27112、org 7961） | 新 ITSM 的实际导入源 | 文件时间 2026-08-19 |

### 1.2 读取方式与安全边界

- 旧系统只调用了登录端点与 `list.do` 读取端点，**未做任何写操作**；未改动旧源数据。
- 原始 dump 与身份明细只落在 KAF 仓库 `.gitignore` 覆盖的 `data/legacy_itsm/**`；本文与仓库不放凭据、不放姓名/邮箱/工号等 PII。
- 未对新 ITSM 任何数据库执行写操作；`itsm_config_baseline_20260908` 是基线样本，不代表已授权的 GA 目标。

### 1.3 连接键

- 用户：新 `users.username` ↔ 旧 `userName` ↔ eHR `employee_id`。
- 组织：新 `departments.code` ↔ 旧 `departmentId` ↔ eHR `org_code`。
- 路由授权对象：旧 `authorizedType`（0/2）→ `sys_role` / `sys_user` 的主键域。

---

## 2. 人员主数据对账

### 2.1 记录级

| 指标 | 值 |
| --- | --- |
| 旧 ITSM 用户总数 | 14393 |
| 旧 ITSM 活跃（`userstatus01`） | 9014 |
| 旧 ITSM 停用（`userstatus04`） | 5379 |
| 旧 ITSM 活跃用户名去重 | 9011 |
| 其中能在新库匹配 | 7753（记录级） |
| 旧活跃但**新库缺失** | **1261** |
| 新库用户 | 7871 |
| 新库用户名在旧全量中不存在 | 55（其中 active 18） |

对缺失 1261 的归因（与 eHR 导出比对）：

| 归因 | 数量 |
| --- | --- |
| 在 eHR person 中完全不存在 | **1252** |
| eHR 中为 RET（离退） | 4 |
| eHR 中为 DRZ | 3 |
| eHR 中为 GEF | 2 |

结论：**1252 人"旧 ITSM 标活跃、HR 主数据查无此人"**。需 HR 裁定是"离职未在旧系统停用"还是"eHR 导出不完整"，不能默认补录（P2，需业务输入）。

**与派单迁移的交叉影响（2026-09-18 补充核算）**：

- 1085 条旧派单规则中，**13 条的目标人正是这 1261 人**（例：`handleWith`/`sjcl002` 的 `kehao.zhang`、`sjsubmit002` 的 `t_eng01`）；这些规则在人员裁定前不能算"可迁"。
- 落在 1261 人缺口上的 13 条里，**有 12 条属于"条件可表达"的 18 条**；因此**首期真正可落地的只有 6 条**（详见[《流程与路由》设计](../superpowers/specs/2026-09-18-process-routing-migration-design.md) §4）。
- 那 97 条"授权对象已失效"（23 个 id）与这 1261 人**零交集**：它们是旧系统 `sys_user` 中已不存在的账号，属真·已删除，作废判断成立。

新库 55 个"旧侧不存在"用户混合了真实新入职（含分公司总经理、商务总监等职级）与 QA 测试账号；属正常增量，不构成缺口。

### 2.2 字段级（重叠用户 7753）

| 字段 | 旧 ITSM 填充 | 新 ITSM 填充 | 说明 |
| --- | --- | --- | --- |
| 岗位头衔（旧 `userPost` / 新 `job_title`） | 7751 | 7442 | 双方都有 7441，**完全一致 7288（98%）**；迁移质量良好 |
| 上级（旧 `leaderId` / 新 `manager_id`） | 7683 | 7042 | 见 §3 |
| 领导标记（旧 `whetherLeader` / 新 `is_leader`） | 14393（100%） | 0（100% false） | 见 §3.3 |
| 条线/分组（旧 `userFenzu` / 新 `function_line`） | 13132 | 7775 | 新库 `function_line` 由 eHR `depart_line` 单独回填，**来源不同、不可当同一字段对账** |
| 机构（旧 `departmentUnit`） | 13147 | 无对应列 | 未迁移 |
| 电话 | 14237 | 7798 | 新库来自 eHR mobile |

旧 ITSM 存在但新库无落点的字段（登记，不自动补）：`HR_USERID`（91%）、`isHR_USER`（91%）、`userTypeId`（100%）、`loginType`（41%）、`dataAccessType`（100%）、`leader`（0.16%）。

---

## 3. 领导与汇报线对账

### 3.1 一致性

以重叠用户 7753 为样本：

| 指标 | 数量 |
| --- | --- |
| 新库 `manager_id` 有值 | 7042 |
| 旧 `leaderId` 有值 | 7683 |
| 旧 `leaderId` 可映射到新库用户 | 7649（另有 34 无法解析） |
| 双方都有值且**一致** | 6631（85.8%） |
| 双方都有值且**冲突** | **404** |
| 仅新库有值 | 7 |
| **仅旧库有值（新库丢空）** | **614** |
| 双方都无 | 97 |

处置要点（P1）：
- 614 条是"可补的空洞"，但**必须业务裁定 eHR `direct_supervisor` 与旧 ITSM `leaderId` 谁是权威**后才能补。
- 404 条冲突不能自动覆盖，应逐条出具差异清单。
- 新库存在 **31 条 `manager_id = 自己`** 的自引用。
  - **准确表述（2026-09-18 复核代码后修正）**：BPMN 引擎的三个审批解析调用点**都已排除申请人本人**（`bpmn_process_engine.go:2124` 部门经理、`:2159` 总经理链、`:2286` 固定范围），因此**当前不存在可直接利用的自审批漏洞**。这 31 条的真实危害是：数据歧义、报表错误，以及解析器被其他调用方复用时失去保护。
  - 处置：迁移前清洗这 31 条，并在数据库/Ent 层补防御性非自引用约束。

### 3.2 关键溯源风险（P1，可追溯性）

- KAF 的 EHR 模型（`md_ehr_person`，来源 SOAP `getUserInfo`）**没有上级字段**：`_PERSON_FIELD_WHITELIST` 只含 `a0100/a0101/a0105/a0107/a0177/username/sdate/nbase/nbase_0/unique_id/jd/phone/email/dept/title/ad_code/abbae/userpassword/a0144/a0146/a0148/ad_email`。
- `ehr-data.xlsx` 的 `direct_supervisor` 与 `depart_line` 列**在本工作区的 KAF/ITSM 代码中找不到生成脚本或来源记录**。
- 也就是说：**新 ITSM 审批路由的关键输入（`users.manager_id`）无法用权威 EHR 管道复核**。建议把汇报线改为直接消费 KAF EHR 同步（若上游能提供该字段），或明确该列的人工/报表口径并留档。

### 3.3 领导标记（`is_leader`）

- 旧 ITSM `whetherLeader`：全量 `whether01` 仅 **100**（活跃 63）；与"是否真有下属"不吻合——包含 0 下属的 `客户服务专员`(9)、`单证助理`(4)、`高级单证员`(3) 以及测试账号，同时部分真实总监有标记。
- 新库 `is_leader` 100% false，且**全仓库除 `/admin/users` 展示外无后端/引擎消费**。
- 建议：以"被引用为直属上级（in-degree > 0）"为可解释口径（本样本去重 **1112** 人），或由业务维护；**不要**用 `job_title` 关键字推断（`主管` 在嘉里头衔中大量为非管理岗，如单证主管/客服主管/关务主管/操作主管/仓库主管）。

---

## 4. 组织/部门对账

| 指标 | 值 |
| --- | --- |
| 新库部门（tenant 1） | 7975 |
| 新库顶层节点 | 25 = 14 产品种子（id 1–14）+ 真根 + 10 孤儿/歧义 |
| 旧 ITSM 生产部门导出 | 5202（2026-09-18）；同一源 2026-08-04 为 5272（漂移 70） |
| 编码交集 | 4899 |
| 新库独有 | 3076 |
| 旧库独有 | 303 |
| 共同节点父子一致 | 4897 |
| 共同节点父子不一致 | 2（`code=1` 公司架构：旧父级为虚拟根 UUID、新为 NULL；`15D0601010704` 资讯科技服务部） |

结论：**eHR 组织树与旧 ITSM 树在 99.96% 上结构一致，新库建树质量良好**。旧部门导出不是完整闭包：旧 `departmentId` 含 185 个 UUID 形态（非编码），且 9014 活跃用户中 626 个引用的编码不在旧部门导出中（约 7% 悬空）。

### 4.1 部门负责人

| 指标 | 值 |
| --- | --- |
| 旧 ITSM 部门中有 `userId/realName` | 205/5202（3.9%） |
| 可映射回新库（部门编码存在且用户存在） | **164** |
| 新库 `departments.manager_id` 有值 | **1**（且该用户职位为"单证主管"，非分公司负责人） |
| 新库存在该部门但负责人为空 | 余下全部 |

含义：**旧系统本身也只有约 4% 的部门负责人数据**；"从旧 ITSM 提取权威部门负责人"只能覆盖极少数节点。同时，矩阵组织下单个部门节点天然多负责人（例：北京分公司 328 名在职、104 个经理级头衔、多条业务线并列；全库有成员的部门里，成员直属上级去重后仅 22 个部门恰好 1 人），**单值 `manager_id` 结构上无法表达**。

### 4.2 人员放置粒度（P1，审批精确度根因）

| 指标 | 值 |
| --- | --- |
| eHR USR 使用的不同 `depart_code` | **175 个，全部 9 字符**（分公司/公司级） |
| 旧 ITSM 活跃用户引用的不同部门 | 2657 个，平均 18.3 字符（叶子级） |
| 新库用户部门编码平均长度 | 9.0 |
| 新库部门编码 == eHR `depart_code` | 7822/7825（99.96%） |
| 新节点是旧叶子的祖先 | 7411/7442 |
| 新节点与旧叶子无关 | 31 |

即：**新系统知道员工在哪个分公司，但不知道在哪个组/科室**。这是"部门经理精确审批"的数据前提缺口，需与 HR 确认 eHR `person.depart_code` 能否细化；短期只能依赖个人汇报链补偿。

---

## 5. 流程对账（本次重新抓取生产环境）

### 5.1 规模对比

| 维度 | 旧 ITSM（生产） | 新 ITSM（tenant 1 基线） |
| --- | --- | --- |
| 流程定义行 | 144 | 59（另 tenant 2 有 19，合计 78） |
| 不同流程 key | 25 | 38 |
| 流程模型 | 23 | 无对应概念（用 definition + deployment 表达） |
| 部署 | — | 42 |
| 绑定 | — | 36 |
| key 交集 | **0** | |
| name 交集 | **0** | |

旧侧 25 个 key（含版本数）：`orderRequest`(26)、`changeProcess`(26)、`knowledgeProcess`(15)、`problemProcess`(15)、`sj002`(15)、`standardProcess`(11)、`SJ001`(7)、`childProTest`(4)、`multiTask`(3)、`rejectLoop`(3)、`OAQJ`(2)、`changeTaskProcess`(2)、`child`(2)、`parallelChange`(2) 等，另有 `CopyOfchildProTest`、`model_*`、`testSub` 等测试模型。全部 `suspended=false`。

旧侧 23 个流程模型以 `metaInfo` 描述名称/描述（如"事件主流程""请求工单流程""变更流程""问题流程"及若干子流程），其中 1 组名称重名，`key` 仅 7 个有值（其余为空），且 `moduleId/companyId` 全空。

### 5.2 结论

- 旧 key 是 Activiti 键（`orderRequest`/`sj002` 等），新 key 是产品英文键（`service_request_flow` 等），**不存在可直接按 key/name 对齐的映射**；这与既有 G9 结论一致。
- 既有设计明确**不导入旧 BPMN**，只对照"被路由引用的流程定义"。因此流程本体不是迁移对象，**流程对账的价值在于确定路由落点**（见 §6）。
- 新库当前流程以产品默认 + SSL-VPN/copilot 测试流程为主，不含任何旧 ITSM 流程；`process_bindings` 里同时存在旧词汇（`ticket`/`change`）与新词汇（`generic`/`change_request`/`service_request_item`），属 WorkItem 收敛过渡态，需要按契约确认最终绑定集合。

---

## 6. 路由对账（本次重新抓取生产环境）

### 6.1 规模与结构

| 指标 | 旧 ITSM（生产） | 新 ITSM |
| --- | --- | --- |
| CTI 授权路由行 | **1085** | `ticket_assignment_rules` = **0**；`ticket_automation_rules` = 0 |
| 授权对象类型 | `authorizedType=2`（用户）1004；`=0`（角色）81 | — |
| 去重 `ctiId` | 62（其中 **14 个不在 CTI 树 107 中**） | — |
| 去重 `definitionId` | 7 | — |
| 去重流程 key | **4**：`orderRequest` 634、`sj002` 321、`knowledgeProcess` 128、`SJ001` 2 | — |
| 去重 `taskDefKey` | 11 | — |
| 模块分布 | moduleId 3（SR）634；1（IN）323；UUID 128 | — |
| 角色授权解析 | 81/81 命中 `sys_role` | — |
| 用户授权解析 | 907 命中 `sys_user`（14478）；**97 行 / 23 个 id 未命中** | — |

主要落点：

| definitionId | taskDefKey | 行数 |
| --- | --- | --- |
| `orderRequest:61:25417508` | `handleWith` | 325 |
| `sj002:22:5565130` | `sjcl002` | 318 |
| `orderRequest:31:5565134` | `handleWith` | 306 |
| `knowledgeProcess:15:5867051` | `KNOWLEDGE_ADMIN`/`establish`/`KNOWLEDGE_MANAGER` | 128 |
| 其余 3 个 definitionId | — | 8 |

### 6.2 结论（P1）

- 旧派单路由**高度集中**在"请求工单流程的 handleWith（受理）"和"事件流程 sj002 的 sjcl002"两个节点；这正是"提单后自动路由到处理组/角色"的旧实现。
- 新库 **0 条** assignment rules，等于旧路由 **0% 迁移**；新系统目前只能靠 BPMN 节点上的 `candidateGroups`/`candidateRole`/`candidateUsers` 与流程绑定近似表达。
- 97 条路由（23 个授权对象 id）指向已删除用户/角色，属**失效引用**，应显式登记 void 而不是补造账号。
- 旧路由的 `taskDefKey`（`handleWith`/`sjcl002`/`KNOWLEDGE_ADMIN`…）是 Activiti 键，**不能直接写入新 BPMN 的 taskDefinitionKey**，必须做逐条映射。
- 既有 test 环境 B5 dry-run 的结论（720 条中 14 条可近似、之后 void 后 9 条）**基于 test 口径，不能直接套用到生产的 1085 条**；生产口径下需要重跑可表达性评估。

---

## 7. 分类 / 字典 / SLA / 优先级 / 模块对账

| 维度 | 旧 ITSM（生产） | 新 ITSM（tenant 1） | 名称/键匹配 |
| --- | --- | --- | --- |
| 服务分类 | CTI 107（深度分布 0/1/2 层 = 39/44/24；重名 3 组） | `ticket_categories` 183（层级 1/2/3 = 9/38/136） | **0** |
| 配置字典 | 990 行 / 803 个不同 `nameCn`（重名 86 组；`dictionariesCode` 重复 4 组） | `field_definitions` 137，选项 label 181 | **5** |
| 优先级 / SLA | `priority_levels` 15（重名 4 组）+ 矩阵 86 | `sla_definitions` 14 | 名称直配 0 |
| 模块 | 4（IN 事件 / SR 请求 / KN 知识 / SERVER 服务台） | `ticket_types` 12 + `recordClass` | 语义层不同，按 recordClass 映射 |
| 节假日 | 960 | `sla_definitions.business_hours` | 需按全国基线 + 企业口径核对 |

### 7.1 源侧阻塞项（生产口径：7 blockers / 12 conflicts）

- `mixed_tenant`：`config_dictionaries`、`cti_authorized`、`departments`、`priority_levels` 的 `companyId` 不唯一（多租户命名空间混用）。
- `orphan_reference`：14 个 `ctiId` 不在 CTI 树中（对应路由需 void）；1 个 `priorityId` 不在优先级集合。
- `unknown_enum_semantics`：`authorizedType ∈ {0,2}` 的语义未在 dump 中声明，须上游确认。
- `natural_key_collision`：CTI 名称 3 组、字典 `nameCn` 86 组、优先级名 4 组、流程模型名 1 组重名——**禁止按名称自动合并**。

---

## 8. 与既有 2026-09-14 报告 / G1–G9 台账的关系

既有 [差异报告](../migrations/2026-09-14-legacy-vs-new-itsm-diff.md) 与 [GAP 设计](../superpowers/specs/2026-09-14-legacy-config-migration-gap-and-solution-design.md) 建立在 **test 环境** dump（`keas-itsm-test.gazellio.com`，2026-09-14）之上，其固定制品与 G-A/G-B 门禁同样指向该批数据。本次生产口径与之的差异：

| 资源 | test（既有报告） | 生产（本次） |
| --- | --- | --- |
| cti_tree | 82 | 107 |
| cti_authorized | 720 | 1085 |
| departments | 3175 | 5202 |
| holidays | 611 | 960 |
| process_definitions | 145 | 144 |
| process_models | 24 | 23 |
| config_dictionaries | 988 | 990 |
| priority_matrix | 86 | 86 |

因此：
- **G1–G9 的数量、可表达性、void 范围都必须在生产口径下重算**，不能沿用 test 结论（尤其是 B5 路由"14→9 条"）。
- G-A/G-B 的隔离目标、映射修订、批次检查点都绑定 test 摘要；生产迁移需要新的源 manifest 摘要与新修订，不得复用旧检查点（符合既有 §5"源/映射摘要变化拒绝复用旧检查点"的约定）。
- 组织/人员 Phase 1 不变量（departments 7975 / users 7862）是 eHR 口径，与旧 ITSM 5202/9014 不是同一套；两者对账是"跨源核对"，不是"导入校验"。

---

## 9. 差异性质分类

### A. 真导入/清洗缺口（可在明确口径后修复）

| # | 缺口 | 证据 | 级别 |
| --- | --- | --- | --- |
| A1 | 旧 `leaderId` 未导入：614 条新库为空、404 条冲突未处置 | §3.1 | P1 |
| A2 | 旧 `whetherLeader` 未导入 | §3.3 | P3（仅展示；且旧值不可靠） |
| A3 | 旧部门负责人未导入：164 条可映射 | §4.1 | P2 |
| A4 | 旧派单路由 0% 迁移（1085 → 0） | §6.1 | P1 |
| A5 | 14 个 ctiId / 1 个 priorityId 悬空，97 条路由授权对象失效 | §6、§7.1 | P2（需 void 登记） |
| A6 | 分类/字典/SLA 名称零匹配，无映射台账落点 | §7 | P2 |
| A7 | `sync_ehr_master_data` 部门创建非幂等（部门无 `(tenant_id, code)` 唯一约束） | 代码/DB 核对 | P1（阻塞安全重跑） |
| A8 | 14 个产品种子部门标记只在本环境 DB，未进入版本化迁移 | DB/仓库核对 | P2 |
| A9 | 新库存在 31 条 `manager_id = 自己` 自引用（引擎已有自我保护，非漏洞；属数据歧义，需清理 + 加防御约束） | §3.1 | P2 |
| A10 | 首期可迁路由受人员缺口牵连：18 条中 12 条目标人属于 1261 人缺口，实际可落地 6 条 | §2.1、§6 | P1（阻塞首期） |

### B. 源侧差异 / 口径不同（不能当 bug 直接"修平"）

| # | 差异 | 证据 |
| --- | --- | --- |
| B1 | 组织放置粒度：eHR 175 个分公司节点 vs 旧 ITSM 2657 个叶子 | §4.2 |
| B2 | 1261 旧活跃用户不在新库，其中 1252 在 eHR 中不存在 | §2.1 |
| B3 | 404 条汇报线冲突（eHR `direct_supervisor` vs 旧 `leaderId`） | §3.1 |
| B4 | 旧部门导出非完整闭包（185 UUID 部门、626 个悬空引用） | §4 |
| B5 | test vs 生产环境体量差异 | §8 |

### C. 待上游确认 / 可追溯性风险

| # | 事项 |
| --- | --- |
| C1 | `direct_supervisor`/`depart_line` 的来源与生成口径（KAF EHR 模型无上级字段） |
| C2 | KAF `md_ehr_person/org/post` 当前为空；权威 EHR 仅在 HR SOAP 服务 |
| C3 | `authorizedType ∈ {0,2}` 语义确认 |
| C4 | 旧 ITSM 多租户 `companyId` 混用的归属裁定 |
| C5 | 决定"旧 ITSM 的 `leaderId`/部门负责人"是否作为新库权威补充 |

---

## 10. 解决方向（建议，不含代码改动）

> 以下为方向建议；任何写入、迁移、流程改绑都需另行授权，并遵循 `AGENTS.md` 的迁移/兼容与租户边界。

1. **先定权威口径（阻塞项）**
   - 组织与人员：以 eHR 为权威、旧 ITSM 仅作差异对照与空洞补充，还是以旧 ITSM 为准？
   - 汇报线：eHR `direct_supervisor` 与旧 `leaderId` 冲突时以谁为准？
   - 组织粒度：eHR 是否能把 `person.depart_code` 细化到部门/组。
2. **在组织/汇报线权威确定后**，只对"新库为空 + 旧侧可解析 + 业务确认"的 614 条补录；404 条冲突逐条清单交业务裁定，禁止自动覆盖。
3. **`is_leader` 降级**：以 in-degree 等可解释口径生成并标注来源，或由业务维护；不按关键字推断。
4. **部门负责人窄口径回填**：只处理可映射的 164 条 + 清理唯一脏值；不做全量（两侧都只有约 4%）。
5. **路由迁移**：以生产口径重跑可表达性评估，先把 4 个 definition/11 个 taskDefKey 映射到新流程节点，再决定哪些可由 `ticket_assignment_rules` 表达；无法表达的显式登记差额，不做静默丢弃。
6. **流程**：沿用"不导入旧 BPMN、只对照被路由引用定义"的方向；新库绑定集合按 WorkItem 契约收敛（`generic`/`change_request`/`service_request_item`）。
7. **分类/字典/SLA/优先级**：按既有 D1–D4 决策，重新以生产口径生成映射/差额台账；`mixed_tenant`、悬空引用、重名先处置。
8. **工具与可重复性**：`sync_ehr_master_data` 部门 upsert 幂等化；种子部门隔离与数据修复脚本纳入版本化迁移、可重入。
9. **可观测性**：把审批人解析来源（部门/个人链/兜底）落库并度量，避免"断流→组广播"无感发生（对齐 fail-closed 与可观测要求）。

---

## 11. 未决输入

1. 组织/人员与汇报线的权威口径决策（§10.1）。
2. `direct_supervisor` 的来源说明，或改走 KAF EHR 管道的可行性。
3. `authorizedType ∈ {0,2}` 语义确认。
4. 旧 ITSM 多租户 `companyId` 的归属裁定。
5. 生产迁移如启动，需要新的 source manifest 摘要、映射修订与授权批次；本记录不提供该授权。

---

## 附录 A：生产环境抽取清单（`data/legacy_itsm/prod/manifest.json`）

- env `prod`，base `https://keas-itsm.gazellio.com`，`fetched_at 2026-09-18T00:58:36.958549+00:00`，`page_size 200`，全部 `status=ok`。

| key | endpoint | rows | sha256 |
| --- | --- | --- | --- |
| cti_tree | `/cTI/list.do` | 107 | `ea8abf971c2b55b04e388548390e036c85819b014ea1f6760cc14bafbb6a9c0e` |
| config_dictionaries | `/configDictionaries/list.do` | 990 | `492ad6da7f9fa8fbbf55871fc7b7e1b03b93098a430337c6196e6ff541d29aa2` |
| priority_levels | `/priorityLevel/list.do` | 15 | `62c6411f477752e451b1427265b7ac2450cee4a0bf592557c14fe8795615b450` |
| priority_matrix | `/rulePriorityMatrix/list.do` | 86 | `f8cb73b4c69d1acc42c213d308037c162958712e8fe6478bb8fff347e86c2e48` |
| cti_authorized | `/ruleCtiauthorized/list.do` | 1085 | `bf74453663d19b67287e9cafe61f8d0fb2dd08d5c79dcdc4ac6ae9e83adb1b3f` |
| modules | `/configModule/list.do` | 4 | `accce29ed0c277c6505c94cbdc1209cd952d45adacab78b91ad398c987073180` |
| holidays | `/holidays/list.do` | 960 | `3c02f07b0f6a4d2c1f2266cf2cd9ef3363cda5e86dc7622dbf2724108ccaed7e` |
| process_definitions | `/process-definition/list.do` | 144 | `a6ca3c5c40a028af4cc5a98353f9ce7bf99ad9cc9852dc36235d77fa06c20356` |
| process_models | `/model/list.do` | 23 | `0c22f82ad16994a4a18c98005a3f1768a1fce29e59062048a39e2d478f2f7429` |
| departments | `/sysDepartment/list.do` | 5202 | `7ebe0f950fae1ec14aa320c9080c88f595383abf17aa9a5aca5f9dd5074bea57` |

## 附录 B：复现命令

```bash
# 1) 旧 ITSM 生产主数据只读抽取（凭据来自环境变量，原始 dump 落 gitignored 目录）
cd /home/administrator/project/kaf
ITSM_URL=https://keas-itsm.gazellio.com \
ITSM_USERNAME=<user> ITSM_PASSWORD=<password> ITSM_ENV_NAME=prod \
  .venv/bin/python scripts/fetch_itsm_master_data.py

# 2) 抽取完整性核验
cd /home/administrator/project/kaf-worktrees/config-migration-review
python3 scripts/verify_legacy_master_data.py \
  --dir /home/administrator/project/kaf/data/legacy_itsm/prod \
  --json-out /tmp/prod_verify.json

# 3) 源冲突与阻塞项
python3 scripts/report_legacy_config_conflicts.py \
  --legacy-dir /home/administrator/project/kaf/data/legacy_itsm/prod \
  --json-out /tmp/prod_conflicts.json --out /tmp/prod_conflicts.md

# 4) 路由授权对象解析（只读；PII 落 gitignored 目录，摘要只含 id/计数）
ITSM_URL=https://keas-itsm.gazellio.com \
ITSM_USERNAME=<user> ITSM_PASSWORD=<password> ITSM_ENV_NAME=prod \
  python3 scripts/fetch_legacy_identity_objects.py \
    --authz /home/administrator/project/kaf/data/legacy_itsm/prod/cti_authorized.json \
    --authz-manifest /home/administrator/project/kaf/data/legacy_itsm/prod/manifest.json \
    --out-dir /home/administrator/project/kaf/data/legacy_itsm/identity \
    --summary-out /tmp/prod_identity_summary.json
```

## 附录 C：新 ITSM 侧核对用查询（只读）

```sql
-- 部门负责人填充率
SELECT count(*) AS total,
       count(*) FILTER (WHERE manager_id IS NOT NULL AND manager_id <> 0) AS with_manager
FROM departments WHERE tenant_id = 1;

-- 用户关键字段填充率
SELECT count(*) AS total,
       count(*) FILTER (WHERE department_id IS NOT NULL AND department_id <> 0) AS with_dept,
       count(*) FILTER (WHERE manager_id IS NOT NULL AND manager_id <> 0)       AS with_manager,
       count(*) FILTER (WHERE job_title <> '')                                  AS with_job_title,
       count(*) FILTER (WHERE is_leader)                                        AS leaders
FROM users WHERE tenant_id = 1;

-- 自引用汇报线
SELECT count(*) FROM users WHERE tenant_id = 1 AND manager_id = id;

-- 派单路由落点现状
SELECT count(*) FROM ticket_assignment_rules;
SELECT business_type, process_definition_key, is_active, count(*)
FROM process_bindings WHERE tenant_id = 1
GROUP BY business_type, process_definition_key, is_active ORDER BY 1, 2;
```

## 附录 D：本次未做 / 明确排除

- 未向旧 ITSM 或新 ITSM 任何库执行写入；未修改流程定义、绑定、用户、部门或任何业务数据。
- 未导入旧 BPMN 流程本体；未创建分类/字典/SLA/路由目标行。
- 未把本记录作为目标启用、迁移批次或验收授权；真实目标部署与写入需单独授权与证据。
- 未在本文包含姓名、工号、邮箱、凭据等 PII/密钥；原始明细仅存于 gitignored 的受保护目录。
