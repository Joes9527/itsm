# 旧审批工作流系统写锁运行手册

对应设计：`docs/superpowers/specs/2026-08-08-approval-bpmn-convergence-design.md`（组件④）
对应实施计划：`docs/superpowers/plans/2026-08-14-approval-bpmn-legacy-system-write-lock.md`

## 背景

旧的 `ApprovalWorkflow`（租户在 `/admin/approvals` 里自己配的审批工作流）正在被 BPMN 取代。这份手册管的是"下线"这一步：给旧系统的写入面加一个租户级的锁，锁上之后旧系统只剩只读历史查看，不能再新建/改/删配置。它不管迁移本身怎么跑（迁移工具是 `cmd/migrate_legacy_approvals`，独立于这次写锁功能），也不管物理删代码的时间点（明确不在这次范围内）。

## 流程设计

### 整体链路

```
运维执行 cmd/lock_legacy_approvals --tenant-id=X
        │
        ▼
写一行 SystemConfig(key="legacyApprovalWriteLocked", value="true", tenant_id=X)
        │
        ▼
ApprovalService.CreateWorkflow/UpdateWorkflow/DeleteWorkflow
每次调用先查这一行，命中且 value=="true" 就返回 ErrLegacyApprovalWriteLocked
        │
        ▼
ApprovalController 把这个 sentinel error 映射成 HTTP 403
（code=2003, message="旧审批工作流系统已下线，请使用 BPMN 流程设计器"）
        │
        ▼
/admin/approvals 前端页面在挂载时读同一个 key（通过既有的
SystemConfigAPI.getConfigByKey，不是新端点），锁定时禁用"新建工作流"、
把操作列换成"只读"标签
```

### 为什么锁的是这四个端点，不多不少

直接查了 `router/router.go` 和 `controller/approval_controller.go` 才定下来的范围，不是凭空假设：

- `ApprovalService.TriggerApproval`（旧系统创建新 `ApprovalRecord` 的唯一入口）在生产代码里已经**零调用点**——组件②把 `CreateTicket` 里那个重复触发点删掉之后，没有任何路径还会调用它。这意味着不管锁不锁，都不会再有新的审批记录被创建。
- 真正还能"以后需要迁移的数据"的写入点，只剩 `ApprovalWorkflow` 配置本身的增删改：`CreateWorkflow`/`UpdateWorkflow`/`PatchWorkflow`/`DeleteWorkflow`（`controller/approval_controller.go`）。锁的就是这四个。
- `SubmitApproval`（对已存在的 `ApprovalRecord` 提交审批决定）**不锁**——既然没有新记录产生，这个端点会随着存量 pending 记录被处理完自然枯竭，提前锁反而会让下线前已经在跑的审批卡住。
- `ListWorkflows`/`GetWorkflow`/`GetApprovalRecords`（历史只读查看）**不锁**。
- 路由层实际注册了两套几乎重复的路由组（`approvalWorkflows` 和 `approvals`，`router/router.go:591-611`），都指向同一个 `ApprovalController` 方法实例——锁加在 service 层而不是逐路由加，两套路由组自动都覆盖到，不用改路由注册代码。`PatchWorkflow` 内部直接调用 `UpdateWorkflow`，同样自动覆盖。

### 为什么不能靠通用的 SystemConfig 接口去解锁

`SystemConfig` 本身已经有一套通用 CRUD（`PUT /api/v1/system-configs/:id`、`/batch`），任何有 `config:update` 权限的租户管理员理论上都能顺手把这个 key 的值改掉，完全绕开"只有运维 CLI 能改"的设计意图。`SystemConfigService.UpdateSystemConfig`/`BatchUpdateSystemConfigs` 里加了一个受保护 key 黑名单（`protectedSystemConfigKeys`，`service/system_config_service.go`）挡住这条路——两个接口在写入前都会先检查目标 key 是否受保护，是的话直接拒绝，不会先改了再报错。

## 配置

| 项 | 值 |
|---|---|
| SystemConfig key | `legacyApprovalWriteLocked` |
| value 类型 | 字符串字面量 `"true"` / `"false"`（不是 JSON boolean，比较用的是精确字符串相等） |
| 作用域 | 租户级（`tenant_id` 字段），不是全局开关——不同租户可以独立锁定 |
| 找不到对应行时的行为 | 视为未锁定（安全默认值），不需要为每个租户预先创建一行 |
| category | `approval` |
| 谁能改 | 只有 `cmd/lock_legacy_approvals` 这个运维 CLI（直连数据库，不经过 HTTP）；通用 SystemConfig HTTP 接口对这个 key 的写入被显式拒绝 |

## 操作步骤

### 前提

- 目标租户已经用 `cmd/migrate_legacy_approvals -tenant-id=X -dry-run=false` 跑过批量迁移，且确认结果里 `failed=0`（或者失败的都是已知可以接受的情况，比如 `amount_based` 类型节点——这类节点设计上就不支持迁移，见组件①③）。
- 这一步是运维人工判断，工具不会自动帮你确认"是不是已经迁移完了"——写锁开关本身不检查这件事。

### 锁定一个租户

```bash
cd itsm-backend
go run ./cmd/lock_legacy_approvals -tenant-id=<租户ID>
```

默认就是锁定（`-unlock` 不传或者传 `-unlock=false`）。命令是幂等的：不管这个租户之前有没有这一行 `SystemConfig`，跑完之后都是"存在且 value=true"，可以放心重复执行。

### 解锁一个租户

```bash
cd itsm-backend
go run ./cmd/lock_legacy_approvals -tenant-id=<租户ID> -unlock=true
```

### 验证锁定生效

```bash
# 应该返回 403，code=2003
curl -X POST http://<host>/api/v1/approval-workflows \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"name":"test","isActive":true,"nodes":[{"level":1,"name":"审批","approverType":"user","approverIds":[1],"approvalMode":"any","rejectAction":"end"}]}'

# 应该仍然 200，正常返回数据——历史查看不受影响
curl http://<host>/api/v1/approval-workflows -H "Authorization: Bearer <token>"
```

前端侧：打开 `/admin/approvals`，"新建工作流"按钮应该是禁用状态，列表操作列应该显示"只读"标签而不是编辑/停用/删除按钮。

## 测试

### 自动化测试覆盖的内容

- `itsm-backend/service/approval_service_write_lock_test.go`：锁定租户的 Create/Update/Delete 全部返回 `ErrLegacyApprovalWriteLocked`，且确认没有产生任何数据变更；未锁定租户完全不受影响；锁定是租户级的，不会连带锁住别的租户。
- `itsm-backend/controller/approval_controller_write_lock_test.go`：四个写入端点在锁定状态下返回 HTTP 403 + 明确文案；`ListWorkflows`/`SubmitApproval` 不受锁定状态影响。
- `itsm-backend/service/system_config_service_test.go`：受保护 key 不能通过 `UpdateSystemConfig`/`BatchUpdateSystemConfigs` 改掉，普通 key 不受影响。
- `itsm-frontend/src/app/(main)/admin/approvals/__tests__/write-lock.test.tsx`：锁定时"新建工作流"禁用、操作列不渲染可写按钮；配置读取失败（404）时默认未锁定，不影响现有行为。

跑法（按测试文件跑，不依赖某个能精确覆盖所有函数名的 `-run` 正则——测试函数名不是统一的
"WriteLock" 前缀，用一个正则去凑容易漏测或者过度匹配到无关测试）：

```bash
cd itsm-backend
go test -v ./service/... -run 'TestApprovalService_(Create|Update|Delete)Workflow_.*|TestApprovalService_LockIsPerTenant'
go test -v ./service/... -run 'TestSystemConfigService_'
go test -v ./controller/... -run 'TestApprovalController_(Create|Delete)Workflow_LockedTenantReturns403|TestApprovalController_(ListWorkflows|SubmitApproval)_UnaffectedByLock'
cd ../itsm-frontend && npx jest --testPathPattern "admin/approvals/__tests__/write-lock.test.tsx" --coverage=false --forceExit
```

### 端到端验证（真实数据，不是 mock）

2026-08-14 在开发环境跑通过一遍，步骤记录如下，供以后复现或验证回归用。需要 `itsm-postgres-dev`（dev Postgres）容器在跑，后端指向同一个数据库（不要用生产库做这个验证）：

1. 用真实的 `/api/v1/approval-workflows` POST 接口（不是直接插库）建一条工作流，节点用 `approverType:"user"` + `approverIds:[<某个真实用户ID>]`（固定审批人这种形状，是最容易在旧版本迁移工具上出问题的一类）。
2. `cmd/migrate_legacy_approvals -tenant-id=<X> -dry-run=true` 确认能正常生成 BPMN，`migrated=1, failed=0`。
3. `-dry-run=false` 真的跑一遍，然后直接查库确认：
   - `process_bindings` 表里 `business_type='ticket'`、`business_sub_type=<工单类型>`（不是别的形状——这是组件③修的那个"绑定永远不可达"bug 的验证点）。
   - `process_definitions` 表里的 `bpmn_xml`（base64 编码，需要 `python3 -c "import base64; print(base64.b64decode(...).decode())"` 解码）包含 `itsm:candidateUsers="<对应ID>"` 和正确的 `itsm:approvalMode`。
4. `cmd/lock_legacy_approvals -tenant-id=<X>` 锁定，用真实 API 调用确认 CREATE/UPDATE/DELETE 都是 403、READ 仍然 200。
5. 尝试用通用 `PUT /api/v1/system-configs/:id` 接口把这个 key 改回 `false`，确认被拒绝（"配置项受保护"），且数据库里的值没变。
6. `-unlock=true` 解锁，确认写操作恢复正常。
7. 清理验证过程中产生的测试数据（`process_bindings`/`process_definitions`/`system_configs` 里对应的行），让开发库回到验证前的状态——不要把测试产生的垃圾数据留在共享的 dev 库里。

## 失败与回滚

- 误锁了一个还没迁移完的租户：直接用 `-unlock=true` 解锁，写操作立即恢复；这个开关的读写都是幂等的，不会因为"锁了又解"产生副作用。
- 锁定/解锁本身失败（CLI 报错退出）：检查数据库连通性和 `-tenant-id` 是否正确；CLI 目前不校验租户是否真的存在，传错租户 ID 会静默创建一条指向不存在租户的配置行，不会报错——这是已知的、这次没有修的小缺口，操作前自己确认租户 ID。
- 不要通过直接改数据库 `system_configs` 表来锁定/解锁——用 CLI，保持行为跟 `ApprovalService` 的读取逻辑（`value=="true"` 精确字符串比较）完全一致，避免手改出现大小写或空格之类的低级错误。

## 已知限制（不在这次范围内，明确记录）

- 生成的 BPMN 审批链没有拒绝感知的网关——一次拒绝不会真的中断审批链，见组件③评审记录的 C2，需要单独的 BPMN 网关设计工作。
- 前端 `http-client.ts` 曾经有一个 bug，会把后端返回的具体错误消息吞掉（包括这次写锁功能的 403 提示），已经在独立 PR 里修复（不跟这次写锁功能的 PR 混在一起，因为那个文件是整个前端共用的基础设施，需要单独走 review）。
- 租户开通/克隆流程（`pkg/seeder/tenant_provisioner.go`）目前还是会无视 source 租户的锁定状态，把旧的 `ApprovalWorkflow` 继续克隆给新开通的租户——这不违反这次的范围（写锁只需要盖住四个 CRUD 端点），但要记进物理删除旧代码那个阶段的清单里，到时候一起处理。
- 物理删除 `approval_controller.go`/`approval_service.go`/`legacy_approval_migration_service.go` 等旧代码不在这次范围内，需要先确认所有租户都锁定/迁移完成、旧路径确实没有流量之后再做，见设计文档"非目标"章节。
