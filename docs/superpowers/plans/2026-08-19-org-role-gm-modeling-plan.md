# 组织架构与角色建模：总经理 / 分公司负责人 / 一人多角色 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 撤销上一轮误加的 `company_gm` 角色，改用组织架构（部门树固定节点）建模总经理/分公司负责人，并启用一人多角色（仅影响 BPMN 候选资格，不影响 RBAC）。

**Architecture:** 后端复用已实现但设计器未暴露的 `assigneeDeptId` 固定部门路由（`resolveFixedScopeAssignee` + `DeptManagerResolver`），给 `DeptManagerResolver` 补环路检测；启用 `User↔Role` 已声明但从未使用的多对多边，扩展 `resolveRoleCandidates` 的候选人查询。前端在设计器属性面板加"固定部门审批人"选择器，在用户编辑表单加"附加角色"多选。

**Tech Stack:** Go 1.25 + Gin + Ent ORM + PostgreSQL（后端），Next.js 15 + TypeScript + Ant Design 5 + bpmn-js（前端）。

**Spec:** `docs/superpowers/specs/2026-08-19-org-role-gm-modeling-design.md`

## Global Constraints

- 后端 DTO 响应字段一律 camelCase，Ent Schema/数据库字段 snake_case（CLAUDE.md 字段命名规范）。
- Controller 不直接返回 Ent 模型，必须走 DTO/Mapper。
- 不引入新的租户级隔离概念（本次范围明确排除真正的多租户集团-分公司隔离）。
- 不触碰 `middleware/rbac.go`（一人多角色不影响 RBAC 权限判定，只影响 BPMN 候选资格）。
- `service_catalogs.process_definition_key` 和设计器已有的 `assigneeRole`（按角色指派）保留，不在本次改动范围内。
- Go 文件改动后跑 `gofmt -w` 保持格式一致；每个任务改完跑对应包的 `go test`。
- 前端改动跑 `npm run type-check` 和 `eslint`。

---

## Task 1: 撤销 `company_gm` 角色（数据 + 代码 + 白名单）

**Files:**
- Modify: `itsm-backend/config/seed/default.json:124-130`
- Modify: `itsm-backend/pkg/seeder/seeder.go:464-466`（Roles 字面量）
- Modify: `itsm-backend/pkg/seeder/seeder.go:1704-1710`（rolePermissionMap）
- Modify: `itsm-backend/dto/user_dto.go:17`（CreateUserRequest.Role oneof）
- Modify: `itsm-backend/dto/user_dto.go:30`（UpdateUserRequest.Role oneof）
- Database: 直接对开发库执行 SQL（无迁移文件，参照上一轮"手动 ALTER TABLE"的处理方式，避免触发 Ent 全库 schema diff 撞上 `connector_configs` 的已知漂移问题）

**Interfaces:**
- 无新增接口。此任务只是纯删除，不影响任何调用方签名。

- [ ] **Step 1: 确认 DB 里 `company_gm` 角色没有被任何用户实际持有**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
source .env 2>/dev/null; export PGPASSWORD="$DB_PASSWORD"
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c \
  "SELECT count(*) FROM users WHERE role = 'company_gm';"
```
Expected: `count` 为 `0`。如果不是 0，停下来跟人类确认——说明这一轮开始之前已经有人手动把某个账号设成了这个角色，不能贸然删除。

- [ ] **Step 2: 从数据库删除 `company_gm` 角色及其权限映射**

Run:
```bash
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c \
  "DELETE FROM role_permissions WHERE role_id IN (SELECT id FROM roles WHERE code = 'company_gm');
   DELETE FROM roles WHERE code = 'company_gm';"
```
Expected: 两条 `DELETE` 均执行成功（第一条应删除 160 行，第二条应删除 1 行，对应上一轮种子日志里 `role permissions ensured {"role": "company_gm", "created": 160}` 和角色 id=37）。

- [ ] **Step 3: 从 `config/seed/default.json` 移除 `company_gm` 条目**

`itsm-backend/config/seed/default.json` 第 124-130 行当前内容：
```json
  "roles": [
    {
      "name": "总经理",
      "code": "company_gm",
      "description": "公司总经理，用于跨部门高层审批场景（如大额采购）"
    },
    {
      "name": "IT总监",
```
改成：
```json
  "roles": [
    {
      "name": "IT总监",
```
（即删掉 `company_gm` 那个 JSON 对象和它后面的逗号，`"IT总监"` 条目原样保留、往前紧接 `"roles": [`）。

验证 JSON 仍合法：
```bash
python3 -c "import json; json.load(open('config/seed/default.json'))" && echo "JSON合法"
```
Expected: `JSON合法`

- [ ] **Step 4: 从 `pkg/seeder/seeder.go` 移除 `company_gm` 的两处引用**

第 464-466 行当前内容：
```go
		Roles: []RoleSeed{
			{Name: "总经理", Code: "company_gm", Description: "公司总经理，用于跨部门高层审批场景（如大额采购）"},
			{Name: "IT总监", Code: "it_director", Description: "IT部门总监"},
```
改成：
```go
		Roles: []RoleSeed{
			{Name: "IT总监", Code: "it_director", Description: "IT部门总监"},
```

第 1704-1710 行当前内容：
```go
	rolePermissionMap := map[string][]string{
		// 系统管理员：所有权限
		"sysadmin": allPermissionCodes(),
		// 总经理：全局读写（不含系统管理），跟 it_director 同一档——高层审批角色，
		// 主要靠 BPMN UserTask 的 assigneeRole 路由到审批任务，不依赖细分的业务写权限。
		"company_gm": allExcept([]string{"system:write", "msp:write", "msp_allocation:write"}),
		// IT总监：全局读写（不含系统管理）
		"it_director": allExcept([]string{"system:write", "msp:write", "msp_allocation:write"}),
```
改成：
```go
	rolePermissionMap := map[string][]string{
		// 系统管理员：所有权限
		"sysadmin": allPermissionCodes(),
		// IT总监：全局读写（不含系统管理）
		"it_director": allExcept([]string{"system:write", "msp:write", "msp_allocation:write"}),
```

- [ ] **Step 5: 从 `dto/user_dto.go` 的两个 oneof 白名单移除 `company_gm`**

第 17 行和第 30 行都把：
```
oneof=super_admin sysadmin company_gm it_director ops_director ...
```
改成：
```
oneof=super_admin sysadmin it_director ops_director ...
```
（只删掉 `company_gm ` 这一个 token，其余角色码顺序不变。两行内容完全相同，`replace_all` 一次改完。）

- [ ] **Step 6: 编译并跑受影响的测试**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
gofmt -w pkg/seeder/seeder.go dto/user_dto.go
go build ./... 2>&1 | grep -v "permission denied"
go test ./pkg/seeder/... ./dto/... ./controller/... 2>&1 | grep -Ev "^ok|permission denied|no test files"
```
Expected: `go build` 无输出（成功）；`go test` 无 FAIL 输出。

- [ ] **Step 7: 重新跑种子确认角色数量恢复**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
ITSM_BOOTSTRAP_ONLY=true ITSM_AUTO_SEED=true go run main.go 2>&1 | grep "roles seeded"
```
Expected: `roles seeded {"count": 18}`（不是上一轮的 19）。

- [ ] **Step 8: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/config/seed/default.json itsm-backend/pkg/seeder/seeder.go itsm-backend/dto/user_dto.go
git commit -m "revert: 撤销 company_gm 角色，总经理改走部门树固定节点建模

上一轮把总经理建模成一个 BPMN 按角色路由的角色，但角色是全租户范围的候选人查询，
无法表达组织架构里的层级位置（哪个分公司的负责人）。改用引擎已支持、只是设计器未暴露的
assigneeDeptId 固定部门审批人路由（下个任务补 UI）。"
```

---

## Task 2: `DeptManagerResolver` 环路检测

**Files:**
- Modify: `itsm-backend/service/approver/dept_manager_resolver.go`
- Test: `itsm-backend/service/approver/approver_test.go`（追加用例，跟现有 `TestDeptManagerResolver_*` 系列放在一起，不新建文件——这是这个 resolver 已有测试的固定位置）

**Interfaces:**
- `DeptManagerResolver.Resolve(ctx, client, appCtx) ([]*ApproverInfo, error)` 签名不变，调用方（`resolveFixedScopeAssignee`、`ResolverRegistry`）不需要改动。

- [ ] **Step 1: 写失败的环路检测测试**

在 `itsm-backend/service/approver/approver_test.go` 里，紧接在现有 `TestDeptManagerResolver_Resolve_NoManagerNoParent`（第 332-349 行）后面追加：

```go
func TestDeptManagerResolver_Resolve_CycleDetected(t *testing.T) {
	fx := newApproverFixture(t)
	defer fx.client.Close()

	deptA, err := fx.client.Department.Create().
		SetName("Dept A").
		SetCode("dept-a-cycle").
		SetTenantID(fx.tenant.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	deptB, err := fx.client.Department.Create().
		SetName("Dept B").
		SetCode("dept-b-cycle").
		SetTenantID(fx.tenant.ID).
		SetParentID(deptA.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	// 手工造一个环：A 的 parent 指向 B，B 的 parent 指向 A，两者都没有 manager。
	_, err = fx.client.Department.UpdateOneID(deptA.ID).SetParentID(deptB.ID).Save(fx.ctx)
	require.NoError(t, err)

	resolver := NewDeptManagerResolver()
	_, err = resolver.Resolve(fx.ctx, fx.client, &ApproverContext{
		TenantID:     fx.tenant.ID,
		DepartmentID: deptA.ID,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}
```

- [ ] **Step 2: 运行测试确认失败（因为还没实现环路检测，会栈溢出或超时，不是预期的错误信息）**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./service/approver/... -run TestDeptManagerResolver_Resolve_CycleDetected -v -timeout 10s
```
Expected: 测试 FAIL（要么因为 `err.Error()` 不包含 `"cycle detected"` 断言失败，要么因为无限递归导致 goroutine 栈溢出而 panic/超时——两种失败都说明现在没有环路保护，符合预期）。

- [ ] **Step 3: 实现环路检测**

`itsm-backend/service/approver/dept_manager_resolver.go` 完整替换为：

```go
package approver

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/department"
	"itsm-backend/ent/user"
)

// DeptManagerResolver resolves approvers based on department manager
type DeptManagerResolver struct{}

// NewDeptManagerResolver creates a new DeptManagerResolver
func NewDeptManagerResolver() *DeptManagerResolver {
	return &DeptManagerResolver{}
}

// GetType returns the resolver type
func (r *DeptManagerResolver) GetType() string {
	return "dept_manager"
}

// Resolve resolves department manager as approver
func (r *DeptManagerResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error) {
	return r.resolve(ctx, client, appCtx, make(map[int]bool))
}

// resolve 是 Resolve 的内部实现，多带一个 visited 集合用来检测部门树里的环——
// parent_id 理论上不该成环，但一旦脏数据/误操作真的造成了环，原来的纯递归会无限
// 循环到栈溢出。总经理/分公司负责人这类固定部门审批人现在也复用这条链路
// （见 resolveFixedScopeAssignee），触发概率比过去只做"部门经理审批"兜底时更高，
// 所以补上这层保护。
func (r *DeptManagerResolver) resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext, visited map[int]bool) ([]*ApproverInfo, error) {
	if appCtx.DepartmentID == 0 {
		return nil, fmt.Errorf("department_id is required for dept_manager resolver")
	}
	if visited[appCtx.DepartmentID] {
		return nil, fmt.Errorf("department hierarchy cycle detected at department %d", appCtx.DepartmentID)
	}
	visited[appCtx.DepartmentID] = true

	// Get department with manager
	dept, err := client.Department.Query().
		Where(
			department.IDEQ(appCtx.DepartmentID),
			department.TenantIDEQ(appCtx.TenantID),
			department.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("department not found: %d", appCtx.DepartmentID)
		}
		return nil, fmt.Errorf("failed to query department: %w", err)
	}

	// If no manager, try parent department
	if dept.ManagerID == 0 {
		if dept.ParentID > 0 {
			// Recursive call with parent department
			parentCtx := *appCtx
			parentCtx.DepartmentID = dept.ParentID
			return r.resolve(ctx, client, &parentCtx, visited)
		}
		return nil, fmt.Errorf("no manager found for department %d or its ancestors", appCtx.DepartmentID)
	}

	// Get manager user info
	manager, err := client.User.Query().
		Where(
			user.IDEQ(dept.ManagerID),
			user.TenantIDEQ(appCtx.TenantID),
			user.Active(true),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("manager user not found or inactive: %d", dept.ManagerID)
		}
		return nil, fmt.Errorf("failed to query manager: %w", err)
	}

	return []*ApproverInfo{
		{
			UserID:    manager.ID,
			UserName:  manager.Name,
			UserEmail: manager.Email,
			Role:      "department_manager",
			Source:    fmt.Sprintf("department:%d", appCtx.DepartmentID),
		},
	}, nil
}
```

（唯一的实质变化：新增 `resolve` 私有方法带 `visited map[int]bool`，`Resolve` 变成薄包装负责初始化这个 map；原来的业务逻辑一字不动地搬进 `resolve`，递归调用从 `r.Resolve(ctx, client, &parentCtx)` 改成 `r.resolve(ctx, client, &parentCtx, visited)`。）

- [ ] **Step 4: 运行测试确认通过，并确认没有破坏其他既有用例**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
gofmt -w service/approver/dept_manager_resolver.go
go test ./service/approver/... -v -run TestDeptManagerResolver 2>&1 | tail -40
```
Expected: 全部 `TestDeptManagerResolver_*`（包括新的 `_CycleDetected` 和已有的 `_Success`/`_ParentFallback`/`_MissingDeptID`/`_DeptNotFound`/`_NoManagerNoParent`）都是 PASS。

- [ ] **Step 5: 跑整个 approver 包和依赖它的 bpmn 引擎测试**

Run:
```bash
go test ./service/approver/... ./service/... 2>&1 | grep -Ev "^ok|permission denied|no test files"
```
Expected: 无 FAIL 输出。

- [ ] **Step 6: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/service/approver/dept_manager_resolver.go itsm-backend/service/approver/approver_test.go
git commit -m "fix(approver): DeptManagerResolver 部门树递归查找加环路检测

总经理/分公司负责人审批现在也走这条固定部门解析链路，触发频率比过去只做
部门经理审批兜底时更高；parent_id 一旦脏数据成环，原实现会无限递归到栈溢出。"
```

---

## Task 3: 设计器暴露 `assigneeDeptId`（固定部门审批人）

**Files:**
- Modify: `itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts`
- Modify: `itsm-frontend/src/components/workflow/__tests__/itsm-moddle-descriptor.test.ts`
- Modify: `itsm-frontend/src/components/workflow/designer/WorkflowNodeInspector.tsx`

**Interfaces:**
- 消费 `itsm-frontend/src/lib/services/department-service.ts` 已有的 `departmentService.getDepartmentTree(): Promise<Department[]>`（`Department` 含 `id/name/parentId/children`）。
- 不新增/修改任何后端接口——`resolveFixedScopeAssignee`（`itsm-backend/service/bpmn_process_engine.go:1114-1139`）已经支持 `task.AssigneeDeptId`，本任务只是把这个已有能力接到设计器 UI 上。

- [ ] **Step 1: moddle descriptor 声明 `assigneeDeptId`**

`itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts` 里：
```ts
      { name: 'assignee', isAttr: true, type: 'String' },
      { name: 'assigneeRole', isAttr: true, type: 'String' },
      { name: 'candidateUsers', isAttr: true, type: 'String' },
```
改成：
```ts
      { name: 'assignee', isAttr: true, type: 'String' },
      { name: 'assigneeRole', isAttr: true, type: 'String' },
      { name: 'assigneeDeptId', isAttr: true, type: 'Integer' },
      { name: 'candidateUsers', isAttr: true, type: 'String' },
```

- [ ] **Step 2: 补断言**

`itsm-frontend/src/components/workflow/__tests__/itsm-moddle-descriptor.test.ts` 里：
```ts
    expect(properties).toEqual(expect.arrayContaining([
      'assignee', 'assigneeRole', 'candidateUsers', 'candidateGroups', 'taskPurpose',
```
改成：
```ts
    expect(properties).toEqual(expect.arrayContaining([
      'assignee', 'assigneeRole', 'assigneeDeptId', 'candidateUsers', 'candidateGroups', 'taskPurpose',
```

- [ ] **Step 3: 运行测试确认通过**

Run:
```bash
cd /home/administrator/project/itsm/itsm-frontend
npx jest src/components/workflow/__tests__/itsm-moddle-descriptor.test.ts
```
Expected: PASS。

- [ ] **Step 4: 给 `WorkflowNodeInspector.tsx` 加部门数据源**

在文件顶部 import 区块（紧接现有的 `import { RoleAPI } from '@/lib/api/role-api';` 之后）加：
```ts
import { departmentService, type Department } from '@/lib/services/department-service';
```

在 `const [roles, setRoles] = useState<{ id: number; name: string; code: string }[]>([]);` 之后加：
```ts
  const [departments, setDepartments] = useState<Department[]>([]);
  const [loadingDepartments, setLoadingDepartments] = useState(false);
```

在 `loadRoles()` 的 `useEffect`（约第 98-116 行）里，紧跟 `loadRoles` 函数定义之后、`loadUsers(); loadGroups(); loadRoles();` 那几行调用之前，加一个新的 `loadDepartments`：
```ts
    const loadDepartments = async () => {
      setLoadingDepartments(true);
      try {
        const tree = await departmentService.getDepartmentTree();
        if (!cancelled) setDepartments(tree);
      } catch (err) {
        console.error('加载部门列表失败:', err);
      } finally {
        if (!cancelled) setLoadingDepartments(false);
      }
    };
```
并把调用列表：
```ts
    loadUsers();
    loadGroups();
    loadRoles();
```
改成：
```ts
    loadUsers();
    loadGroups();
    loadRoles();
    loadDepartments();
```

- [ ] **Step 5: 加部门树展平工具函数**

在文件里 `toCsv` 函数（约第 49-54 行）后面加一个新的模块级函数：
```ts
/**
 * 部门树展平为带层级缩进的 Select options（集团→分公司→部门→科室 都是这棵树上不同深度的节点，
 * 缩进用 "—" 前缀帮用户在下拉框里分辨层级，不依赖后端返回任何层级/类型字段）。
 */
function flattenDepartmentOptions(
  nodes: Department[],
  depth = 0
): { label: string; value: number }[] {
  const result: { label: string; value: number }[] = [];
  for (const node of nodes) {
    result.push({
      label: `${depth > 0 ? '—'.repeat(depth) + ' ' : ''}${node.name}`,
      value: node.id,
    });
    if (node.children && node.children.length > 0) {
      result.push(...flattenDepartmentOptions(node.children, depth + 1));
    }
  }
  return result;
}
```

- [ ] **Step 6: 加 `currentAssigneeDeptId` 派生状态和 `departmentOptions`**

在：
```ts
  const currentAssignee = (bo.assignee as string) || '';
  const currentAssigneeRole = (bo.assigneeRole as string) || '';
```
后面加一行：
```ts
  const currentAssigneeDeptId = bo.assigneeDeptId ? Number(bo.assigneeDeptId) : undefined;
```

在 `assigneeRoleOptions` 定义（约第 315-318 行）后面加：
```ts

  // 部门选项（固定部门审批人用 department.id，对应后端 resolveFixedScopeAssignee 的
  // task.AssigneeDeptId → DeptManagerResolver 解析该固定部门的负责人，非申请人自己的部门）
  const departmentOptions = flattenDepartmentOptions(departments);
```

- [ ] **Step 7: 加 UI 控件，三者互斥**

先修正已有"按角色指派"字段下方过时的提示文案（它是上一轮为了给总经理用而写的，现在总经理改走本任务新加的固定部门审批人，这句提示要去掉"总经理"字样，只留真正还成立的"总监"场景）：

把：
```tsx
              <Text type="secondary" className="text-xs mt-1 block">
                指定该任务由某个角色下的用户处理（不依赖具体人，适合总经理/总监等跨部门审批角色）
              </Text>
            </div>

            {/* Candidate Users */}
```
改成：
```tsx
              <Text type="secondary" className="text-xs mt-1 block">
                指定该任务由某个角色下的用户处理（不依赖具体人，适合 IT总监等纯权限角色；跨部门/公司的组织架构负责人请用下方"固定部门审批人"）
              </Text>
            </div>

            {/* Assignee Dept — 固定部门审批人，跟前两者互斥。用于"总经理""分公司负责人"这类
                跟组织架构位置绑定、不看申请人自己部门的审批环节：把这里选成部门树的根节点就是
                总经理，选成某个分公司节点就是那个分公司的负责人。引擎复用"部门经理审批"同一条
                DeptManagerResolver，只是范围钉死在这里选的部门，不取申请人自己的。 */}
            <div className="mt-3">
              <Text strong className="text-sm flex items-center mb-2">
                <Shield className="w-3.5 h-3.5 mr-1" />
                固定部门审批人 (assigneeDeptId)
                <Tag color="orange" className="ml-2 text-xs">组织架构</Tag>
              </Text>
              <Select
                allowClear
                showSearch
                placeholder="选择部门（该部门负责人处理，无负责人则向上级部门找）"
                value={currentAssigneeDeptId}
                onChange={value =>
                  apply({ assigneeDeptId: value ?? '', assignee: '', assigneeRole: '' })
                }
                className="w-full"
                loading={loadingDepartments}
                filterOption={(input, option) =>
                  (option?.label ?? '').toString().toLowerCase().includes(input.toLowerCase())
                }
                options={departmentOptions}
                size="small"
              />
              <Text type="secondary" className="text-xs mt-1 block">
                指定该任务由某个固定部门的负责人处理（例如选公司根部门=总经理审批）；与"受理人""按角色指派"互斥
              </Text>
            </div>

            {/* Candidate Users */}
```

同时给前两个字段（受理人、按角色指派）的 `onChange` 补上清空 `assigneeDeptId`，保持三者互斥。把：
```tsx
                onChange={value => apply({ assignee: value || '', assigneeRole: '' })}
```
改成：
```tsx
                onChange={value => apply({ assignee: value || '', assigneeRole: '', assigneeDeptId: '' })}
```
把：
```tsx
                onChange={value => apply({ assigneeRole: value || '', assignee: '' })}
```
改成：
```tsx
                onChange={value => apply({ assigneeRole: value || '', assignee: '', assigneeDeptId: '' })}
```

- [ ] **Step 8: 类型检查 + lint**

Run:
```bash
cd /home/administrator/project/itsm/itsm-frontend
npm run type-check 2>&1 | tail -60
npx eslint src/components/workflow/designer/WorkflowNodeInspector.tsx src/components/workflow/itsm-moddle-descriptor.ts 2>&1 | tail -60
```
Expected: 两条命令都无错误输出。

- [ ] **Step 9: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-frontend/src/components/workflow/itsm-moddle-descriptor.ts \
        itsm-frontend/src/components/workflow/__tests__/itsm-moddle-descriptor.test.ts \
        itsm-frontend/src/components/workflow/designer/WorkflowNodeInspector.tsx
git commit -m "feat(bpmn-designer): 暴露固定部门审批人 (assigneeDeptId)

引擎里 resolveFixedScopeAssignee 早就支持按固定部门（而非申请人自己的部门）解析
审批人，但设计器 UI 一直没接。把部门树根节点设为固定审批部门就是总经理审批，
设为某个分公司节点就是分公司负责人审批——不再需要专门的角色来表达这类
跟组织架构位置绑定的审批人。"
```

---

## Task 4: 启用 `user_roles` 多对多边（一人多角色，仅 BPMN 候选资格）

**Files:**
- Modify: `itsm-backend/dto/user_dto.go`（`UpdateUserRequest` 加字段）
- Modify: `itsm-backend/service/user_service.go`（`UpdateUser` 处理新字段）
- Modify: `itsm-backend/service/bpmn_process_engine.go`（`resolveRoleCandidates` 参数改名 + 查询扩展）
- Test: `itsm-backend/service/bpmn_process_engine_test.go`

**Interfaces:**
- `dto.UpdateUserRequest` 新增字段 `AdditionalRoleIds []int \`json:"additionalRoleIds,omitempty"\``，供 Task 5 的前端调用消费。
- `service/bpmn_process_engine.go` 里 `resolveRoleCandidates(ctx context.Context, tenantID int, roleCode string) ([]string, error)`（原参数名 `role` 改成 `roleCode`，签名位置/类型不变，调用方 `e.resolveRoleCandidates(ctx, instance.TenantID, task.AssigneeRole)` 不需要改）。

- [ ] **Step 1: `dto/user_dto.go` 加字段**

`UpdateUserRequest`（第 23-31 行，Task 1 删完 `company_gm` 之后的当前状态）：
```go
// UpdateUserRequest 更新用户请求
type UpdateUserRequest struct {
	Username   string `json:"username,omitempty" binding:"omitempty,min=3,max=50"`
	Email      string `json:"email,omitempty" binding:"omitempty,email"`
	Name       string `json:"name,omitempty" binding:"omitempty,min=1,max=100"`
	Department string `json:"department,omitempty"`
	Phone      string `json:"phone,omitempty"`
	// 角色更新，仅管理员有权限更新
	Role string `json:"role,omitempty" binding:"omitempty,oneof=super_admin sysadmin it_director ops_director ops_manager ops_engineer dba network_eng sd_manager change_manager service_catalog_admin l1_support l2_support l3_expert security_admin audit_admin dept_manager end_user guest"`
}
```
改成：
```go
// UpdateUserRequest 更新用户请求
type UpdateUserRequest struct {
	Username   string `json:"username,omitempty" binding:"omitempty,min=3,max=50"`
	Email      string `json:"email,omitempty" binding:"omitempty,email"`
	Name       string `json:"name,omitempty" binding:"omitempty,min=1,max=100"`
	Department string `json:"department,omitempty"`
	Phone      string `json:"phone,omitempty"`
	// 角色更新，仅管理员有权限更新
	Role string `json:"role,omitempty" binding:"omitempty,oneof=super_admin sysadmin it_director ops_director ops_manager ops_engineer dba network_eng sd_manager change_manager service_catalog_admin l1_support l2_support l3_expert security_admin audit_admin dept_manager end_user guest"`
	// AdditionalRoleIds 是附加角色（多对多，走 User.roles 边），只影响 BPMN 按角色路由
	// 审批任务时的候选资格（resolveRoleCandidates），不影响 RBAC 权限判定——RBAC 权限
	// 判定只看上面单一的 Role 字段。传 nil 表示不修改；传 []int{} 表示清空所有附加角色。
	AdditionalRoleIds *[]int `json:"additionalRoleIds,omitempty"`
}
```
（用指针 `*[]int` 而不是 `[]int`，是为了区分"请求体没带这个字段"（nil，不动）和"显式传了空数组"（清空）——跟 `Role`/`Username` 这些用空字符串表示"不修改"的字段不同，角色列表的"空"和"不提供"是两个不同语义，必须能区分。）

- [ ] **Step 2: `service/user_service.go` 处理新字段**

在 `itsm-backend/service/user_service.go` 的 `UpdateUser` 函数里，紧接着现有的角色更新逻辑（第 264-273 行）：
```go
	// 角色更新（仅在提供时设置），管理员权限由RBAC控制
	if strings.TrimSpace(req.Role) != "" {
		role := strings.ToLower(strings.TrimSpace(req.Role))
		// 兼容前端传的"user"角色，自动转换为"end_user"
		if role == "user" {
			role = "end_user"
		}
		update = update.SetRole(role)

	}
```
后面加：
```go

	// 附加角色（仅影响 BPMN 按角色路由的候选资格，不影响 RBAC 权限判定，见 dto.UpdateUserRequest
	// 里 AdditionalRoleIds 的注释）。nil 表示不修改；非 nil 时用传入的列表整体替换现有附加角色——
	// 先清空再整体重设，语义等同于"提交的列表就是完整的附加角色集合"，避免增量 add/remove 的状态漂移。
	if req.AdditionalRoleIds != nil {
		update = update.ClearRoles()
		if len(*req.AdditionalRoleIds) > 0 {
			update = update.AddRoleIDs(*req.AdditionalRoleIds...)
		}
	}
```

- [ ] **Step 3: 写失败的测试（`resolveRoleCandidates` 应该能查到通过附加角色匹配的用户）**

在 `itsm-backend/service/bpmn_process_engine_test.go` 文件末尾追加：

```go
func TestResolveRoleCandidates_MatchesPrimaryAndAdditionalRole(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:resolve_role_candidates?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := zaptest.NewLogger(t).Sugar()

	tenant, err := client.Tenant.Create().
		SetName("Role Candidates Tenant").
		SetCode("role-candidates").
		SetDomain("role-candidates.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	itDirectorRole, err := client.Role.Create().
		SetName("IT总监").
		SetCode("it_director").
		SetDescription("IT部门总监").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// primaryUser: 主角色字段直接就是 it_director（老路径，应该继续命中）
	primaryUser, err := client.User.Create().
		SetUsername("primary_it_director").
		SetEmail("primary@role-candidates.test").
		SetName("Primary IT Director").
		SetPasswordHash("hash").
		SetRole("it_director").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// secondaryUser: 主角色是 dept_manager，但通过 user_roles 边额外挂了 it_director——
	// 这是本次新加的路径，应该也能被 resolveRoleCandidates("it_director") 命中。
	secondaryUser, err := client.User.Create().
		SetUsername("secondary_it_director").
		SetEmail("secondary@role-candidates.test").
		SetName("Secondary IT Director").
		SetPasswordHash("hash").
		SetRole("dept_manager").
		SetActive(true).
		SetTenantID(tenant.ID).
		AddRoleIDs(itDirectorRole.ID).
		Save(ctx)
	require.NoError(t, err)

	// unrelatedUser: 既不是主角色也没有附加角色，不应该出现在结果里。
	_, err = client.User.Create().
		SetUsername("unrelated_user").
		SetEmail("unrelated@role-candidates.test").
		SetName("Unrelated User").
		SetPasswordHash("hash").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	engine := NewCustomProcessEngine(client, logger).(*CustomProcessEngine)
	names, err := engine.resolveRoleCandidates(ctx, tenant.ID, "it_director")
	require.NoError(t, err)

	assert.Len(t, names, 2)
	assert.Contains(t, names, primaryUser.Username)
	assert.Contains(t, names, secondaryUser.Username)
}
```

检查文件顶部 import：如果 `context`、`itsm-backend/ent/enttest`、`github.com/mattn/go-sqlite3`（下划线导入）、`github.com/stretchr/testify/assert`、`github.com/stretchr/testify/require`、`go.uber.org/zap/zaptest` 这几个当前 `bpmn_process_engine_test.go` 顶部还没有，一并加上（现有文件目前只测 `evaluateCondition`，没有触库的测试，大概率缺这些 import）。

- [ ] **Step 4: 运行测试确认失败**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
go test ./service/... -run TestResolveRoleCandidates_MatchesPrimaryAndAdditionalRole -v
```
Expected: FAIL——`secondaryUser` 不会出现在 `names` 里（`assert.Len(t, names, 2)` 应该实际拿到 1）。

- [ ] **Step 5: 实现 `resolveRoleCandidates` 扩展查询**

`itsm-backend/service/bpmn_process_engine.go` 第 1051-1070 行当前内容：
```go
func (e *CustomProcessEngine) resolveRoleCandidates(ctx context.Context, tenantID int, role string) ([]string, error) {
	users, err := e.client.User.Query().
		Where(user.RoleEQ(role), user.TenantIDEQ(tenantID), user.Active(true)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询角色候选审批人失败: %w", err)
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		display := strings.TrimSpace(u.Username)
		if display == "" {
			display = strings.TrimSpace(u.Email)
		}
		if display == "" {
			display = strconv.Itoa(u.ID)
		}
		names = append(names, display)
	}
	return names, nil
}
```
改成：
```go
func (e *CustomProcessEngine) resolveRoleCandidates(ctx context.Context, tenantID int, roleCode string) ([]string, error) {
	// 候选人 = 主角色字段等于 roleCode 的用户，UNION 通过 user_roles 多对多边额外拥有
	// roleCode 这个角色的用户（一人多角色，仅影响这里的 BPMN 候选资格，不影响 RBAC 权限判定，
	// 见 dto.UpdateUserRequest.AdditionalRoleIds 的注释）。
	users, err := e.client.User.Query().
		Where(
			user.TenantIDEQ(tenantID),
			user.Active(true),
			user.Or(
				user.RoleEQ(roleCode),
				user.HasRolesWith(role.CodeEQ(roleCode), role.TenantIDEQ(tenantID)),
			),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询角色候选审批人失败: %w", err)
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		display := strings.TrimSpace(u.Username)
		if display == "" {
			display = strings.TrimSpace(u.Email)
		}
		if display == "" {
			display = strconv.Itoa(u.ID)
		}
		names = append(names, display)
	}
	return names, nil
}
```

**关键细节，容易踩坑**：这个文件顶部已经把 `itsm-backend/ent/role` 包导入成了 `role`（第 21 行 `"itsm-backend/ent/role"`），而这个函数原来的参数就叫 `role string`——如果不把参数改名成 `roleCode`，函数体内 `role.CodeEQ(...)` 会被 Go 解析成"对字符串类型的 `role` 变量取 `CodeEQ` 字段/方法"而编译失败（`role.CodeEQ undefined (type string has no field or method CodeEQ)`）。所以参数改名是必须的，不是风格选择。改名不影响调用方——调用点 `e.resolveRoleCandidates(ctx, instance.TenantID, task.AssigneeRole)`（第 828 行）是按位置传参，不需要跟着改。

- [ ] **Step 6: 运行测试确认通过**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
gofmt -w service/bpmn_process_engine.go service/user_service.go dto/user_dto.go
go build ./... 2>&1 | grep -v "permission denied"
go test ./service/... -run TestResolveRoleCandidates_MatchesPrimaryAndAdditionalRole -v
```
Expected: `go build` 无输出；测试 PASS。

- [ ] **Step 7: 跑受影响的完整包测试**

Run:
```bash
go test ./service/... ./dto/... ./controller/... 2>&1 | grep -Ev "^ok|permission denied|no test files"
```
Expected: 无 FAIL。

- [ ] **Step 8: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-backend/dto/user_dto.go itsm-backend/service/user_service.go itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_process_engine_test.go
git commit -m "feat(rbac): 启用 user_roles 多对多边，支持一人多角色的 BPMN 候选资格

User↔Role 的多对多边在 schema 里声明过但从未被业务代码使用。这次只用于扩展
resolveRoleCandidates 的候选人查询（按角色路由审批任务时，除了主角色 user.role，
也认通过附加角色关联到该角色的用户）；RBAC 权限判定完全不变，仍然只看主角色。"
```

---

## Task 5: 前端用户编辑表单加"附加角色"多选

**Files:**
- Modify: `itsm-frontend/src/lib/api/user-api.ts`
- Modify: `itsm-frontend/src/app/(main)/admin/users/page.tsx`

**Interfaces:**
- 消费 Task 4 新增的 `dto.UpdateUserRequest.AdditionalRoleIds`（JSON 字段名 `additionalRoleIds`）。
- 消费已有的 `RoleAPI.getRoles()`（`admin/users/page.tsx` 里 Task 上一轮已经在用，返回 `{roles: Role[]}`，`Role.id/name/code`）。

- [ ] **Step 1: `user-api.ts` 加类型字段**

在 `UpdateUserRequest`（当前状态，Task 4 之后）：
```ts
export interface UpdateUserRequest {
  username?: string;
  email?: string;
  name?: string;
  department?: string;
  phone?: string;
  role?: string;
}
```
改成：
```ts
export interface UpdateUserRequest {
  username?: string;
  email?: string;
  name?: string;
  department?: string;
  phone?: string;
  role?: string;
  // 附加角色（角色 ID 列表），仅影响 BPMN 按角色路由审批任务时的候选资格，不影响 RBAC 权限——
  // RBAC 权限只看上面的 role 字段。不传表示不修改；传空数组表示清空所有附加角色。
  additionalRoleIds?: number[];
}
```

同时在 `User` 接口（响应类型）里加一个只读展示字段，跟后端 `dto.UserDetailResponse` 对齐（后端目前没有在 `UserDetailResponse` 里加这个字段，本任务范围内**不需要**新增——附加角色是"仅写"字段，编辑表单打开时留空即可，不需要回显现有值。如果后续要回显，需要后端 `UserDetailResponse` 额外加 `additionalRoles` 字段并且 `dto/mappers.go` 的 `ToUserDetailResponse` 一并处理，这属于范围外的增量，本任务不做）。

- [ ] **Step 2: `admin/users/page.tsx` 编辑弹窗加"附加角色"字段**

在 `handleUpdateUser`（当前状态，Task 上一轮已加 `role: values.role`）：
```ts
      await UserApi.updateUser(selectedUser.id, {
        username: values.username,
        email: values.email,
        name: values.name,
        department: values.department,
        phone: values.phone,
        role: values.role,
      });
```
改成：
```ts
      await UserApi.updateUser(selectedUser.id, {
        username: values.username,
        email: values.email,
        name: values.name,
        department: values.department,
        phone: values.phone,
        role: values.role,
        additionalRoleIds: values.additionalRoleIds,
      });
```

**先处理数据源**：`roleOptions` 当前定义（上一轮加的）是 `{label, value}[]`，`value` 是 `role.code`（字符串），但 `additionalRoleIds` 后端要的是角色 **ID**（`[]int`），两者类型不一致，不能直接复用同一个 `roleOptions`，需要单独建一个按 ID 取值的选项列表。

在 `roleOptions` 状态定义旁边（`const [roleOptions, setRoleOptions] = useState<{ label: string; value: string }[]>([]);`）新增一个并列的状态：
```ts
  const [additionalRoleOptions, setAdditionalRoleOptions] = useState<{ label: string; value: number }[]>([]);
```
在 `loadRoles`（当前状态）：
```ts
  useEffect(() => {
    const loadRoles = async () => {
      try {
        const resp = await RoleAPI.getRoles({ page: 1, pageSize: 100 });
        setRoleOptions(
          (resp.roles || [])
            .filter(r => r.code)
            .map(r => ({ label: r.name, value: r.code as string }))
        );
      } catch (error) {
        console.error('加载角色列表失败:', error);
      }
    };
    loadRoles();
  }, []);
```
改成（同一次请求，顺便派生出按 ID 取值的第二份 options，不用多发一次请求）：
```ts
  useEffect(() => {
    const loadRoles = async () => {
      try {
        const resp = await RoleAPI.getRoles({ page: 1, pageSize: 100 });
        const roles = resp.roles || [];
        setRoleOptions(
          roles.filter(r => r.code).map(r => ({ label: r.name, value: r.code as string }))
        );
        setAdditionalRoleOptions(roles.map(r => ({ label: r.name, value: r.id })));
      } catch (error) {
        console.error('加载角色列表失败:', error);
      }
    };
    loadRoles();
  }, []);
```
**再加表单字段**：在编辑弹窗表单里，紧接着上一轮加的"角色"`Form.Item`（当前状态）：
```tsx
            <Col span={12}>
              <Form.Item name="role" label="角色">
                <Select
                  showSearch
                  placeholder="请选择角色"
                  options={roleOptions}
                  filterOption={(input, option) =>
                    (option?.label ?? '').toString().toLowerCase().includes(input.toLowerCase())
                  }
                />
              </Form.Item>
            </Col>
          </Row>
```
改成（把"附加角色"放在同一个 `Row` 外新起一行，跟"角色"字段区分开、并加说明文字避免误解成权限叠加，`options` 直接用上面新增的 `additionalRoleOptions`）：
```tsx
            <Col span={12}>
              <Form.Item name="role" label="角色">
                <Select
                  showSearch
                  placeholder="请选择角色"
                  options={roleOptions}
                  filterOption={(input, option) =>
                    (option?.label ?? '').toString().toLowerCase().includes(input.toLowerCase())
                  }
                />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item
            name="additionalRoleIds"
            label="附加角色"
            tooltip="仅影响该用户能否被按角色路由的审批任务选中，不会叠加权限——权限仍然只看上面的“角色”字段"
          >
            <Select
              mode="multiple"
              showSearch
              placeholder="可选，不选则维持现状"
              options={additionalRoleOptions}
              filterOption={(input, option) =>
                (option?.label ?? '').toString().toLowerCase().includes(input.toLowerCase())
              }
            />
          </Form.Item>
```

- [ ] **Step 3: 类型检查 + lint**

Run:
```bash
cd /home/administrator/project/itsm/itsm-frontend
npm run type-check 2>&1 | tail -60
npx eslint "src/app/(main)/admin/users/page.tsx" src/lib/api/user-api.ts 2>&1 | tail -60
```
Expected: 两条命令都无错误输出。

- [ ] **Step 4: Commit**

```bash
cd /home/administrator/project/itsm
git add itsm-frontend/src/lib/api/user-api.ts "itsm-frontend/src/app/(main)/admin/users/page.tsx"
git commit -m "feat(users): 用户编辑表单加附加角色多选（仅影响 BPMN 候选资格）

跟主角色字段分开展示、分开提交，避免被误解成权限叠加——RBAC 权限判定
仍然只看主角色；附加角色只影响 resolveRoleCandidates 的 BPMN 候选人查询。"
```

---

## Task 6: 端到端验证

**Files:** 无代码改动，纯验证。

- [ ] **Step 1: 重启 backend/frontend**

Run:
```bash
cd /home/administrator/project/itsm/itsm-backend
pkill -f "go run main.go" 2>/dev/null; sleep 1
nohup go run main.go > /tmp/itsm-backend.log 2>&1 & disown
cd /home/administrator/project/itsm/itsm-frontend
pkill -f "next dev -p 3010" 2>/dev/null; sleep 1
nohup npx next dev -p 3010 > /tmp/itsm-frontend.log 2>&1 & disown
```
等 `curl -s http://localhost:8090/api/v1/health` 返回 200、`curl -s http://localhost:3010` 返回 200/307/308 再继续。

- [ ] **Step 2: 用 `/admin/departments` 建一棵测试用的组织架构树**

创建（或确认已存在）：根部门"XX集团"（无 parent，manager 设成某个测试账号 A，代表总经理）→ 子部门"XX分公司"（parent 指向集团，manager 设成测试账号 B，代表分公司负责人）。

- [ ] **Step 3: 用 BPMN 设计器验证 `assigneeDeptId`**

打开 `/workflow/designer`，新建或编辑一个流程，给一个 UserTask 节点选"固定部门审批人"，指向上一步创建的集团根部门。保存后导出/重新打开确认 XML 里 `assigneeDeptId` 属性正确持久化（没有在导出时被丢弃）。

- [ ] **Step 4: 走一遍审批，确认候选人是账号 A 而不是申请人自己的部门经理**

用一个属于分公司（而非集团根部门）、自己部门另有直属经理的账号提交申请，确认这个 UserTask 的候选人/受理人解析成的是集团根部门的 manager（账号 A），而不是申请人自己部门的经理——这是 `assigneeDeptId` 和"部门经理审批"（申请人自己部门）语义区别的关键验证点。

- [ ] **Step 5: 验证一人多角色**

去 `/admin/users` 给一个测试账号（主角色随便，比如 `dept_manager`）在"附加角色"里加上 `it_director`。用这个账号登录，确认能在"我的待办"里看到一个 `assigneeRole=it_director` 的审批任务（需要先有一个这样的流程节点，可以复用之前"Copilot采购申请"场景里 IT-director 那个环节）。

- [ ] **Step 6: 确认 `company_gm` 彻底不可见**

- `/admin/users` 编辑弹窗的"角色"下拉框里不应该出现"总经理"。
- `curl -s http://localhost:8090/api/v1/roles | grep company_gm` 应该无匹配。

- [ ] **Step 7: 向人类报告验证结果**

汇总以上 6 步的结果（通过/不通过 + 截图或关键 API 响应），不需要额外提交（本任务无代码改动）。

---

## Self-Review 记录

- **Spec 覆盖**：spec 的"设计一/二/三"分别对应 Task 3+Task 6（设计二：assigneeDeptId）、Task 4+5（设计三：一人多角色）；"组织架构复用部门树"（设计一）本身不需要代码改动，已在 Task 6 Step 2 的验证步骤里覆盖（用现有部门管理 UI 建树）。"错误处理"对应 Task 2。"回滚 company_gm"对应 Task 1。spec 里"风险与未决问题"提到的"`assigneeDeptId`/`assigneeTeamId`等字段互斥范围目前只需要覆盖 assignee/assigneeRole/assigneeDeptId 三者"已经在 Task 3 Step 7 落实。
- **占位符扫描**：全文没有 TBD/TODO，所有代码块都是可直接使用的完整内容。
- **类型一致性**：`resolveRoleCandidates` 参数名 `roleCode`（Task 4）与调用点 `e.resolveRoleCandidates(ctx, instance.TenantID, task.AssigneeRole)`（未改动，按位置传参）一致；`AdditionalRoleIds *[]int`（Task 4 后端）与前端 `additionalRoleIds?: number[]`（Task 5）在 JSON 层面类型匹配（`*[]int` 序列化为 JSON 数组或省略，前端传数组即可，Go 会正确反序列化进指针）；`departmentService.getDepartmentTree()` 返回 `Department[]`（Task 3）与 `flattenDepartmentOptions(nodes: Department[], depth)` 参数类型一致。
