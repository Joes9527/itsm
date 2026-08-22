# 真实组织数据落地：基于个人汇报链的"总经理"审批人动态解析（嘉里物流试点）

日期：2026-08-20
状态：待实施

## 背景

[[2026-08-19-org-role-gm-modeling-design]] 撤销了误建的 `company_gm` 角色，改用组织架构（部门树固定节点）建模总经理/分公司负责人，落地了 `assigneeDeptId`（固定部门审批人）+ `DeptManagerResolver`（沿部门树 `parent_id` 向上找最近的有 `manager_id` 的祖先）机制，并已全部实施完成（`0984f10d` 到 `7bef9bd9`）。

本轮讨论从"当前组织架构/角色/流程引擎是否支持'集团-总公司-分公司'四级审批"这个问题出发，用户要求先分析现状再讨论，本质是一次针对已上线机制的真实数据可用性验证。验证对象是本环境实际存在的嘉里物流（Kerry Logistics）eHR 主数据（`tenant_id=1`，7975 个部门、7826 个用户，来自 `itsm-backend/cmd/sync_ehr_master_data`）。

验证结论：**已上线的机制代码没有问题，但设计时假设会被回填的 `department.manager_id` 在真实数据上 0% 填充，且嘉里物流是矩阵组织——同一个部门节点（分公司/总公司）上常年并存多条业务线的平级总经理，`department.manager_id`（单值字段）在结构上无法表达这种情况。** 需要把审批人解析的基准，从"部门树上的固定节点"改为"提交人自己的真实汇报链"。这份文档记录调研过程中沉淀下来的数据事实和新的设计方案。

## 调研结论（现状，作为设计依据）

- **部门树现状**：7975 条记录，`tenant_id=1`。真实组织树只有 1 个有效根——`id=16`「公司架构」（eHR `org_code="1"`，源数据里 `parent_code` 自引用，是设计上的真根，`sync_ehr_master_data/main.go:219-225` 的注释里有说明）。另有 25 个"根节点"里剩下的 24 个是噪音：14 个是 ITSM 产品自带的默认部门种子（`id 1-14`，`IT`/`OPS`/`RD`/`HR`/`FIN`/`ADMIN` 等短编码，跟嘉里物流的真实组织无关，已确认 `users`/`tickets` 引用数都是 0，可安全隔离）；10 个是 eHR 原始 Excel（`org` sheet，7961 条有效行）里父级记录真实缺失（3 个）或父级名称存在 2-7 个同名候选、无法安全自动认领（7 个）导致的孤儿，占比 0.13%，需要 HR 方补充源数据或人工指定，本次不处理。
- **`department.manager_id` 全表 0% 填充**（7975 条无一例外）——已上线的 `assigneeDeptId` + `DeptManagerResolver` 机制目前没有任何真实数据可解析，无法验证端到端场景。
- **`user.manager_id`（个人汇报线）90.4% 填充**（7076/7826），来自 eHR `person` sheet 的 `direct_supervisor` 字段（格式"姓名:工号"），由 `sync_ehr_master_data/main.go:392-423`（"Direct Supervisor Linking" 第二遍扫描）回填。**但目前没有任何 BPMN 审批 resolver 消费这个字段**——现有的动态审批路径（`resolveApprovalAssignee`/`resolveFixedScopeAssignee`）全部走 `DeptManagerResolver`，即部门维度，不是个人维度。
- **矩阵组织，部门节点无法唯一定位负责人**：以嘉里物流「嘉顺达物流有限公司」子树（eHR `org_code=11D03`，DB `id=17`，5481 个组织节点、4679 名在职员工，`sync_ehr_master_data` 已回填 `area_name`/`org_type`）为样本，`employee_post`（岗位头衔）里带"总经理"字样的在职员工有 99 人，但**同一个部门节点上常年并存多个平级总经理**：例如"北京分公司"（`depart_code=11D030304`）节点下同时有国际货代总经理、综合物流总经理（还各自再细分"-北京片区"）、业务拓展助理总经理、主客户管理副总经理、商务副总经理、项目管理副总经理共 7 人；"总公司-上海"（`depart_code=11D030105`）节点下同时有财务、人力资源、安全质量、审计、企业发展、集团财务等 5+ 个平级 SPT（共享职能）线总经理。`department.manager_id` 是单值字段，结构上无法表达"一个节点多个平级负责人"。
- **`employee_post`（岗位头衔）没有被导入 `users` 表**——`sync_ehr_master_data/main.go` 里 `EHRPerson.Post` 字段被从 Excel 解析出来（`main.go:150-153`），但从未在 `client.User.Create()` 调用里写入任何列，是解析即丢弃的死数据。`function_line`（职能条线，格式为 `[业务线前缀]_[子部门]_[职能]`，如 `IFF_空运进口部_操作`/`SPT_财务及会计部`）字段虽然已经存在于 `users` 表（近期 commit 加的），但本轮讨论确认目前没有 resolver 读取它。
- **`is_leader` 字段 100% 为 `false`**——跟 `job_title`（本设计新增，见下）是同一类问题：字段已经声明在 schema 里，但 `sync_ehr_master_data` 从未对它赋值，是另一个死字段。本次不处理 `is_leader`，只处理职位头衔。
- **业务语义澄清（用户在讨论中确认）**："总经理"审批环节的语义是**跟随提交人自己的真实位置**——分公司员工的"总经理"是他自己所在分公司/业务线的负责人；只有当提交人自己就在总公司时，才直接找总公司总经理。也就是说解析基准必须是"以提交人为起点、顺着他自己的真实汇报关系找到的那个人"，而不是"不管提交人是谁，都指向写死的某个部门节点"。这正是 `department.manager_id`（同一个部门节点对所有申请人给出同一个 answer）在矩阵节点上做不到、而 `user.manager_id` 个人链（天然按人区分）能做到的地方。

## 范围

**本次设计覆盖**：

1. `users` 表新增职位头衔字段，`sync_ehr_master_data` 同步回填（`employee_post` 已经解析出来，只是没有落库，改动量很小）。
2. 新增 `PersonalManagerResolver`（`service/approver/`），沿 `user.manager_id` 向上爬，遇到职位头衔命中"总经理"关键字的人即停止；比照 [[2026-08-19-org-role-gm-modeling-design]] 里 `DeptManagerResolver` 的模式补环路检测。
3. BPMN 引擎接入新 resolver：给 BPMN UserTask 增加一种"总经理审批（个人链）"的路由方式，与现有的 `assignee`/`assigneeRole`/`assigneeDeptId` 并列，走 `PersonalManagerResolver` 而不是 `DeptManagerResolver`。
4. 隔离 14 个产品默认种子部门（`description` 打标记，不删除，已确认零引用）。
5. 嘉里物流「嘉顺达物流有限公司」子树（`id=17`）作为试点，验证"Copilot 采购申请"端到端场景：提交人 → 直属上级（部门经理，`user.manager_id` 一跳） → 分公司/总公司总经理（`PersonalManagerResolver` 沿链爬到"总经理"关键字命中） → 总公司 IT 总监（沿用已上线的 `assigneeRole=it_director`，角色路由，不改动）。

**明确不在本次范围内**：

- 10 个真实孤儿部门节点的父级修复——需要 HR 从源头补数据或人工指定，本次留档不处理。
- 全量 7961 个部门的 `department.manager_id` 回填——`DeptManagerResolver`/`assigneeDeptId` 机制本身保留、不废弃，仍适用于不涉及矩阵歧义的场景（例如单一负责人明确的部门级审批），只是不再是"总经理"这一层级的首选解析路径。
- 业务线感知的精确匹配规则（比如显式按提交人 `function_line=IFF` 去匹配同为 IFF 线的总经理）——`PersonalManagerResolver` 顺着真实汇报链爬，天然达到等价效果（提交人的真实上级本来就在同一条业务线上），不需要额外写业务线匹配规则。
- `is_leader` 字段的回填与启用。
- RBAC 权限判定——不涉及，本次改动全部是 BPMN 审批候选人解析范畴，跟 [[2026-08-19-org-role-gm-modeling-design]] 一样不碰 `middleware/rbac.go`。

## 设计一：`users` 表新增职位头衔字段

`ent/schema/user.go` 新增：

```go
field.String("job_title").Comment("职位头衔，来自HR系统岗位字段，用于按关键字识别审批层级（如"总经理"）").Optional(),
```

`itsm-backend/cmd/sync_ehr_master_data/main.go` 在创建/更新用户时补一行 `SetJobTitle(p.Post)`（`p.Post` 已经从 Excel 解析出来，`main.go:150-153`，之前只是没调用对应的 Set 方法）。这是纯增量字段，不影响任何现有查询。

对应迁移文件：`itsm-backend/migrations/<date>_add_user_job_title.sql`：

```sql
ALTER TABLE users ADD COLUMN IF NOT EXISTS job_title VARCHAR(255);
```

## 设计二：`PersonalManagerResolver`

### 为什么不是扩展 `DeptManagerResolver`

`DeptManagerResolver` 的解析基准是部门节点，对所有挂在同一部门节点下的申请人给出同一个 answer；矩阵组织里一个部门节点有多个平级总经理时，这个"同一个 answer"没有办法选出唯一正确的那个人。`PersonalManagerResolver` 的解析基准是申请人自己，天然按人区分——申请人的真实上级链路本来就唯一确定他应该被哪条业务线的总经理审批，不需要额外的业务线匹配规则。

### 算法

沿 `user.manager_id` 向上爬，每一步检查当前人的 `job_title` 是否包含"总经理"子串（覆盖"总经理"/"副总经理"/"助理总经理"/"XX总经理"等所有变体——业务上只要求"找到一个总经理级别的人"，不区分正副/助理，如果后续需要精确到"必须是正职总经理"，属于对本设计的关键字匹配规则做精化，不改变整体结构）；命中则返回，同链路不重复访问同一个人（环路检测，比照 [[2026-08-19-org-role-gm-modeling-design]] 里 `DeptManagerResolver` 的 `visited map[int]bool` 模式）；爬到链路顶端（`manager_id` 为空/0）仍未命中，返回错误，由调用方按现有约定回退到 `ticket-approvers` 候选组。

```go
package approver

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// PersonalManagerResolver resolves approvers by climbing the submitter's real
// reporting chain (user.manager_id) until a job title containing "总经理" is found.
type PersonalManagerResolver struct{}

func NewPersonalManagerResolver() *PersonalManagerResolver {
	return &PersonalManagerResolver{}
}

func (r *PersonalManagerResolver) GetType() string {
	return "personal_manager_gm"
}

func (r *PersonalManagerResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error) {
	return r.resolve(ctx, client, appCtx.UserID, appCtx.TenantID, make(map[int]bool))
}

func (r *PersonalManagerResolver) resolve(ctx context.Context, client *ent.Client, userID, tenantID int, visited map[int]bool) ([]*ApproverInfo, error) {
	if userID == 0 {
		return nil, fmt.Errorf("user_id is required for personal_manager_gm resolver")
	}
	if visited[userID] {
		return nil, fmt.Errorf("reporting chain cycle detected at user %d", userID)
	}
	visited[userID] = true

	u, err := client.User.Query().
		Where(user.IDEQ(userID), user.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("user not found: %d", userID)
		}
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	if u.ManagerID == 0 {
		return nil, fmt.Errorf("no GM-level manager found in reporting chain starting at user %d", userID)
	}

	manager, err := client.User.Query().
		Where(user.IDEQ(u.ManagerID), user.TenantIDEQ(tenantID), user.Active(true)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("manager user not found or inactive: %d", u.ManagerID)
		}
		return nil, fmt.Errorf("failed to query manager: %w", err)
	}

	if strings.Contains(manager.JobTitle, "总经理") {
		return []*ApproverInfo{{
			UserID:    manager.ID,
			UserName:  manager.Name,
			UserEmail: manager.Email,
			Role:      "personal_manager_gm",
			Source:    fmt.Sprintf("reporting_chain:%d", userID),
		}}, nil
	}

	return r.resolve(ctx, client, manager.ID, tenantID, visited)
}
```

`ApproverContext` 需要携带 `UserID`（提交人 ID）——现有 `ApproverContext` 已经有 `TenantID`/`DepartmentID` 等字段（参见 `service/approver/resolver.go`），补一个 `UserID` 字段即可，不影响其他 resolver 的现有用法。

调用方（`bpmn_process_engine.go`）在"部门经理审批"这一步（默认路径，无需改动）之后，如果流程节点声明了新的"总经理审批（个人链）"路由方式，改用 `PersonalManagerResolver` 而不是 `resolveFixedScopeAssignee`/`DeptManagerResolver`。"部门经理审批"这一步本身语义上就是"提交人的直属上级"，等价于 `user.manager_id` 爬一跳，不需要额外解析逻辑，可以直接读 `submitter.ManagerID`。

### BPMN 设计器接入

参照 [[2026-08-19-org-role-gm-modeling-design]] 里 `assigneeDeptId` 的接入方式，新增一个互斥选项 `assigneeMode="personalChainGM"`（或等价的布尔/枚举属性，具体命名留给实现阶段跟现有 `assignee`/`assigneeRole`/`assigneeDeptId` 三选一的模式对齐，扩成四选一）。`itsm-moddle-descriptor.ts` 补声明；`WorkflowNodeInspector.tsx` 加对应的开关/说明文案（"沿提交人真实汇报链找总经理，适合矩阵组织——跟'固定部门审批人'的区别是：这里按提交人自己是谁给出不同人，那里对所有人给出同一个人"）。

## 设计三：14 个产品种子部门隔离

`description` 字段前缀标记，不改 `name`/`code`/`parent_id`，不删除，完全可逆：

```sql
UPDATE departments
SET description = '[产品默认种子部门-非客户组织] ' || COALESCE(description, '')
WHERE id BETWEEN 1 AND 14 AND tenant_id = 1;
```

（已确认这 14 个部门 `users`/`tickets` 引用数均为 0，此操作不影响任何现有数据关联。）

前端组织架构树/部门选择器可选地按这个前缀过滤，不在本次强制要求，属于后续如果发现这批噪音影响管理员体验时再补的增量。

## 测试计划

**后端**：

- `service/approver/personal_manager_resolver_test.go`（新建）：覆盖"三级汇报链，顶端命中总经理头衔"、"环路数据触发报错而不是无限递归"、"链路顶端都没有总经理头衔时返回错误，交由调用方回退"。
- `service/bpmn_process_engine_test.go`：补"提交人在矩阵节点场景下，`PersonalManagerResolver` 能正确解析到申请人自己业务线的总经理，而不是同一部门节点下的其他平级总经理"的用例（用嘉里物流样本数据构造：同一 `depart_code` 下两个不同业务线的人，各自的汇报链应该分别爬到各自业务线的总经理）。

**数据**：

- `sync_ehr_master_data` 补 `SetJobTitle` 后重跑一次（`-dry-run` 确认变更范围，再正式跑），验证 `users.job_title` 填充率。
- 14 个种子部门隔离的 `UPDATE` 语句执行前后，`SELECT count(*) FROM users WHERE department_id BETWEEN 1 AND 14` 应该恒为 0（验证隔离操作没有破坏任何关联）。

**端到端**：

用 Playwright 重跑"Copilot 采购申请"场景，提交人选嘉里物流「嘉顺达物流有限公司」子树下的真实测试账号（例如北京分公司 IFF 业务线的普通员工），验证：

1. "部门经理审批"环节的候选人正确解析为提交人的 `user.manager_id` 直属上级。
2. "总经理审批"环节的候选人是提交人自己业务线的总经理（用国际货代线的人提交，候选人应该是"国际货代总经理 - 北京片区"，不是同一部门节点下"综合物流总经理"或其他业务线的人）。
3. "IT 总监审批"环节沿用已有的 `assigneeRole=it_director` 机制，候选人是拥有该角色的用户（角色路由本身不需要重新验证，[[2026-08-19-org-role-gm-modeling-design]] 已经验证过）。
4. 换一个总公司层级（而不是分公司）的提交人测试账号，验证"总经理审批"环节直接命中总公司层的总经理，不会多爬一层或漏掉。

## 风险与未决问题

- **职位头衔关键字匹配的精度**：当前设计只要求 `job_title` 包含"总经理"子串，不区分"总经理"/"副总经理"/"助理总经理"三个层级。如果业务上要求"总经理审批"必须是正职、不能是副职/助理，需要更精细的关键字规则（比如排除包含"副"/"助理"前缀的头衔），本设计先按"覆盖所有三级都算数"的宽松规则实现，跑通端到端场景后再看业务是否需要收紧。
- **750 个 `user.manager_id` 链路根节点的数据质量**：这批人里混杂了真实的最高层高管和"HR 同步时汇报线数据本来就没采集全"的缺口（无法用现有数据区分）。如果一个申请人的汇报链爬到某个根节点仍然没有命中"总经理"头衔，`PersonalManagerResolver` 会返回错误，调用方按现有约定回退到 `ticket-approvers` 候选组——这个兜底路径在试点验证阶段需要重点关注命中率，如果发现大量申请人的总经理审批环节都在兜底而不是精确解析到人，说明这 750 个根节点里的数据缺口比预期严重，需要另外评估。
- **`PersonalManagerResolver` 与 `DeptManagerResolver` 的使用边界**：两者都能用于"总经理"语境的审批，但语义不同（个人链 vs 部门节点），需要在 BPMN 设计器的界面文案上讲清楚区别，避免流程设计者选错——已在"设计二"的接入部分加了对比说明。
- **10 个真实孤儿部门节点**、**14 个种子部门隔离后前端过滤**：都是已知的、有意留到后续处理的小尾巴，不阻塞本次核心场景。
