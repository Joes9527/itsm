# CTI Governance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `subagent-driven-development` when delegation is explicitly selected, or `executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将三级 CTI 的维护、目录默认分类、分类纠正及专业完成检查连成可验证闭环。

**Architecture:** `ticket_categories` 与 WorkItem 最深节点引用为单一权威。既有分类服务提供结构/引用校验，统一创建服务解析目录默认值，各专业服务维护自己的修改和完成事务。分派/SLA/BPMN 保持独立所有者。

**Tech Stack:** Go/Gin/Ent/PostgreSQL，Next.js/TypeScript/Ant Design，现有 Jest、Playwright 及 PG 集成测试设施。

**Status:** draft implementation plan，设计及审查补充已于 2026-09-17 获用户确认；本计划未执行、未授权共享环境写入。

## Global Constraints

- 权威：[已确认设计](../specs/2026-09-17-cti-governance-design.md)、[AGENTS](../../../AGENTS.md)、[CLAUDE](../../../CLAUDE.md)、[工程治理](../../agent-engineering-governance.md)、[工程约定](../../engineering-conventions.md)。运行前读[环境](../../development-environment.md)、[开发指南](../../DEVELOPMENT_GUIDE.md)、[命令参考](../../dev-commands-reference.md)、[E2E指南](../../e2e-testing-guide.md)。
- 基线是 `b8ac9639b` 的源码，设计分支已另有文档提交。执行者先 fetch；以最新 origin/main 建独立 worktree/分支，将本设计和计划的确切提交带入；出现拥有者路径/迁移账本变化须先更新计划，不覆盖本地工作。
- 最多三级；单个工单保存所选最深节点；不另加三个权威分类列。
- 普通报障允许未分类；目录发布/申请在受控启用后要求完整三级。旧在途单只提示，旧单重开不重新入组。
- 新 Requested Item 主动纠正保持完整三级。Incident 恢复/解决不受阻，正式关闭前检查。Generic 在第一个正常完成动作及直接关闭前检查；Problem/Change 最终关闭检查；Catalog Task 不新增人工要求。
- 分类变化不得隐式改派、重算 SLA、重启 BPMN。各专业权限、租户、执行范围、版本、事务回执及审计不削弱。
- 编码稳定；被引用节点/其祖先不可移动；停用保留既有合法完整路径；无子节点且无任何引用才可删除。
- 不执行旧 ticket 迁移、共享库清理、启动时迁移/seed、新数据库/schema/container创建、历史SLA重算或全域规则重构。
- 本计划的测试命令只连接显式指定的隔离目标。启动配置不作为测试数据库默认值；不要 source 当前 Mac/WSL 服务环境后运行集成测试。
- 每项采用红→绿→复审→提交，禁止将“尚未执行”的期望写成 PASS。阶段A通过不等于整个设计完成。

## 交付顺序与公共接口

A1 → A2 → A3 → A4 构成阶段A（基础与创建）；B1 → B2 → B3 → B4 构成阶段B（处理与完成）。A1必须先完成；B3可在A2契约稳定后单独实施，但最终集成依赖全部任务。不要多名Agent同时改迁移登记、统一创建或同一专业命令。

### 文件职责

| 位置 | 责任 |
| --- | --- |
| `itsm-backend/service/ticket_category_service.go`、`ticket_category_creation.go` | 复用现有分类维护/创建解析 |
| 新 `service/ticket_category_policy.go`、`ticket_category_references.go` | 纯三级/匹配语义与调用方事务内引用检查，避免继续扩大维护服务 |
| 新 `service/cti_governance.go` | 读取受控启用状态、不可变截止时间及专业动作质量判断；不执行专业状态转换 |
| `handlers/service_catalog/{service,creation,revision,preflight,repository_impl,entity}.go` | 默认分类持久化、版本、发布与创建读取 |
| `handlers/intake/resolver.go`、`handlers/common/workitemcreation/{command,plan}.go` | 目录先解析，选择权威CTI，再走同一分类校验 |
| `controller/ticket_category_controller.go`、既有 `router/` 注册 | 树路径、引用读取、维护错误DTO；复用既有权限 |
| 专业服务/handlers | 各专业动作事务调用共享检查，不能新增第二状态机 |
| 新 `itsm-frontend/src/components/business/CTISelector.tsx` | 统一值和路径选择；用户/坐席/管理端通过明确props使用 |
| `admin/ticket-categories` 路由下组件 | 树导航、详情、维护表单和引用列表 |

以下为本计划新增的最小内部契约，放在 `service/ticket_category_policy.go`；函数实现和持久化封装在A2完成，不是假设已有API：

```go
type CTINode struct {
    ID, ParentID, Level, TenantID int
    Name, Code string
    Active bool
}
type CTIMatchScope string
const (
    CTIExact CTIMatchScope = "exact"
    CTISubtree CTIMatchScope = "subtree"
)
// nodes 按根到所选节点排序；空路径在 requireFull=false 时合法。
func ValidateCTIPath(nodes []CTINode, tenantID int, requireFull, requireActive bool) error
// path 为候选工单完整根路径；未知scope返回错误。
func MatchCTI(path []CTINode, criterionID int, scope CTIMatchScope) (bool, error)
```

`TicketCategoryService`新增 `ResolveCTIPath(ctx context.Context, tx *ent.Tx, tenantID, selectedID int, requireFull, requireActive bool) ([]CTINode, error)`，selectedID=0表示无分类，返回结构化现有错误类型。数据库锁在拥有者事务内获取；纯函数不查询数据库。其他领域调用分类服务契约，不直接调用分类repository。

前端统一契约放入现有 `src/lib/api/ticket-category-api.ts`：

```ts
export interface CTIPathNode {
  id: number; parentId: number | null; level: 1 | 2 | 3;
  name: string; code: string; isActive: boolean;
}
export interface CTISelectorProps {
  value: number | null; // 最深节点ID，不存第二套路径
  onChange: (value: number | null) => void;
  requiredDepth: 0 | 3;
  disabled?: boolean;
}
```

## Task A1：固定基线、引用清单与启用契约

**Files:** 修改本计划的执行记录与设计§4证据；读取 `ent/schema/{ticketcategory,servicecatalog,systemconfig}.go`、`migration/`、`migrations/`、`service/system_config_service.go`、`service/ticket_assignment_rule_service.go`、`service/ticket_sla_service.go`、分类控制器及导入路径。不写共享数据库。

- [x] 获取并记录执行基线、分支、脏文件、最新canonical迁移注册；确认其他数据库恢复任务没有占用目标。（见「执行记录（A1）」基线/迁移账本/环境边界）

```bash
git fetch origin
git status --short
git log -1 --format='%H %s' origin/main
rg -n 'Register|047|schemaVersion' itsm-backend/migration
rg -n 'CategoryID|CategoryIds|category_id|category_ids|default_resolver|sla_tier' itsm-backend/service itsm-backend/handlers itsm-backend/ent/schema
```

- [ ] 以真实授权租户上下文只读盘点：三级数量、超深度、根错误、孤儿、环、跨租户父链、错误level、编码冲突、全部业务/配置引用及目录缺配。零行必须同时证明租户上下文和RLS身份，不能当成空库。只记录聚合/ID及错误类型，不导出个人数据。**未执行**：共享 Dev／验证库被在途恢复任务占用，且未获得只读目标指纹与凭据授权（见执行记录 A1 未执行项 1）。
- [x] 在计划记录引用矩阵（所有者、结构引用/遗留字符串、维护/查询入口、删除保护）；覆盖 TicketTemplate、目录、工单、分派、SLA、流程配置及当前代码发现的其他消费者。字符串引用不能因没外键被忽略；不明确的映射阻止受影响维护动作并报可操作错误。（见执行记录 A1 引用矩阵）
- [x] 固定启用记录使用既有 `system_configs`，每租户唯一保留键 `cti_governance_v1`；值含 `catalogEnforced`、`completionEnforced`、`effectiveFrom`（首次启用完成门禁时写入，后续不可改）。禁止通用配置接口随意改此保留键；按既有权限与审计走受控激活路径。读取失败/重复/非法值报错，不默认为关闭；从未配置则为未启用。（契约已固定；实现与验证在 B2）
- [x] 核对基础测试并记录实际命令结果；不因本地Mac CGO问题跳过PG验收，可在获准隔离Linux执行。实际用例名为 `TestMoveCategoryUpdatesParentSortOrderAndDescendantLevels` / `TestMoveCategoryRejectsMovingUnderDescendant`，PASS；PG 验收本轮 NOT RUN（无授权隔离目标）。

```bash
cd itsm-backend
go test ./service -run 'TestTicketCategory' -count=1
```

- [x] 提交审计记录：`docs: record CTI implementation baseline and reference ownership`。交付门禁：引用矩阵完整，所有阻塞有明确对象，未经授权无数据修复。

## Task A2：分类不变量、路径与受控迁移

**Files:** 修改 `ent/schema/{ticketcategory,servicecatalog}.go`、现有分类service/controller；新增 `service/ticket_category_policy.go`、`service/ticket_category_references.go`、同目录 `_test.go`，`tests/integration/cti_structure_postgres_test.go`。在现有canonical迁移登记增加一个 `cti_governance` 迁移及verify，序号在A1确认最新账本后取下一号，禁止预占048或修改旧校验和。Ent生成物只通过既有生成命令产生。

**Interfaces:** 产出上述 `CTINode`、`ValidateCTIPath`、`ResolveCTIPath`；维护失败映射现有验证/冲突/无权限错误，不泄露跨租户对象。

- [x] 新增具体红测（测试文件导入现有testify/assert或用标准testing）：

```go
func TestCTIRejectsIncompleteRequiredPath(t *testing.T) {
    path := []CTINode{{ID: 1, TenantID: 7, Level: 1, Active: true}}
    if ValidateCTIPath(path, 7, true, true) == nil {
        t.Fatal("required CTI accepted only level 1")
    }
}
func TestCTIAllowsUnclassifiedReport(t *testing.T) {
    if err := ValidateCTIPath(nil, 7, false, true); err != nil { t.Fatal(err) }
}
```

- [x] 运行 `go test ./service -run 'TestCTI' -count=1`，确认失败来自缺少约束/实现，而非不可用依赖。（红：`undefined: CTINode/ValidateCTIPath`；实现后 15 项全绿）
- [x] 实现根ParentID=0、Level从1连续递增、同租户、最多3、无环、完整/部分及全部祖先启用校验。解析最深节点向上最多3步，异常链返回失败，不能递归无限深。
- [x] 在创建/移动/导入/删除/停用入口调用同一维护规则；锁定完整被操作子树及引用检查所需行，移动被引用后代的祖先也拒绝。目录/工单创建与维护按固定锁顺序协调，不能只做preflight。
- [x] 迁移增加 `service_catalogs.default_ticket_category_id` 可空结构引用；检查实际PG与Ent的编码唯一性差异，按现有租户契约处理。已有异常只出预检失败，不自动修复。`system_configs` 为保留键建立局部唯一约束，重复行先阻塞；新增列/约束附RLS/租户与回退边界。
- [x] PG红绿用例命名为 `TestCTIStructurePostgres`：两个租户同名分类隔离；并发创建vs删除/停用不得产生无效引用；三级子树移动超深拒绝；字符串引用阻止删除；迁移失败原子回滚，不改旧账本。**已编写并通过 `go vet -tags integration_postgres` 编译；未执行（NOT RUN，无授权隔离目标）。**
- [x] 运行目标测试、迁移verify及 `git diff --check` 后提交 `feat: enforce CTI hierarchy and reference integrity`。迁移只在隔离目标应用。**

### 执行记录（A2）

- 新增 `service/ticket_category_policy.go`（`CTINode`/`CTIMatchScope`/`ValidateCTIPath`/`MatchCTI` 纯语义）与
  `service/ticket_category_references.go`（在调用方事务内扫描工单、目录默认分类、SLA、分派/自动化规则条件与动作、
  模板、流程绑定、遗留字符串引用）。
- `service/ticket_category_service.go` 重写维护路径为事务化实现：三级上限、同租户父链、连续 level、无环、
  编码租户内唯一且创建后不可变、被引用节点及其祖先不可移动、已发布目录引用时不可停用、
  无子节点且无引用才可删除、删除/移动/停用在提交事务内重新校验引用并锁定子树（PostgreSQL `FOR UPDATE`，
  SQLite 无锁子句仅用于单元 fixture）。
- 路径解析 `ResolveCTIPath(ctx, tx, tenantID, selectedID, requireFull, requireActive)`；`requireActive=false` 使
  停用前的合法完整路径仍可作为历史质量校验依据（设计 §5.4）。
- 迁移 `migration/cti_governance.go` = **048_cti_governance**（A1 已确认 047 为最新、048 在所有 ref 上空闲）：
  预检（租户内重复编码、层级越界、缺失/跨租户父级、level 与父级不一致、根非一级、超三级/环、保留键重复）
  + `code` 唯一范围全表→租户内 + `service_catalogs.default_ticket_category_id`（FK，`ON DELETE SET NULL`）
  + `system_configs` 保留键局部唯一索引；verify SQL 断言结构；dev-reset 明确回退并在跨租户重复编码时失败。
  登记于 `RegisteredMigrations`、`GetMigrationSQL`、受控目录（047 之后、retirement 之前），未改动任何历史 SQL/checksum。
- Ent：`ticketcategory.go` 的 `code` 由表级唯一改为 `(tenant_id, code)` 唯一索引；`servicecatalog.go` 增加
  `default_ticket_category_id` 边/字段。生成物由 `go generate ./ent` 产生；无语义变化的 `ent/processtask*`
  格式漂移已还原，不混入无关改动。
- 控制器：分类维护错误按 NotFound/Conflict/Param 映射；树支持 `includeInactive`；导入按 `parent_code`
  重建层级（父行后置时最多重试到最大层级+1 轮，不再把三级文件静默压平成一级）。
- 迁移计划测试的目录计数属“目录增长”预期，已同步为新值并补 `CTIGovernanceVersion` 的位置断言。

验证证据：

```text
go generate ./ent                            -> OK（仅目标实体生成物 + 已还原无关漂移）
go test ./migration/... -count=1             -> ok itsm-backend/migration
go test ./service -run 'TestCTI|TestCreateCategory|TestUpdateCategory|TestDeleteCategory|TestMoveCategory|TestDisableCategory|TestResolveCTIPath|TestGetCategory|TestSubtreeHeight|TestMaintenance' -count=1
                                             -> ok itsm-backend/service (0.539s)
go test ./... -count=1（全量单元/包测试）    -> 无 FAIL
go vet -tags integration_postgres ./tests/integration/  -> OK（PG 用例可编译）
go test -tags integration_postgres ./tests/integration -run TestCTIStructurePostgres -count=1
                                             -> FAIL（目标指纹校验：INTAKE_POSTGRES_TEST_DSN 未指向 127.0.0.1:36444/sslvpn_test）
                                                => 如实记为 NOT RUN，未以 skip 冒充通过
git diff --check                             -> 无输出
```

PG 用例已执行（见下方 A2/B2 授权后补充证据）。未执行项：迁移在**共享/生产**目标库的应用、门禁启用。

## Task A3：目录默认分类与统一创建

**Files:** 修改 `handlers/service_catalog/{entity,service,repository_impl,creation,revision,preflight}.go`，`dto/service_dto.go`、`handlers/intake/resolver.go`、`handlers/common/workitemcreation/command.go`；测试 `handlers/service_catalog/creation_read_test.go`、`handlers/intake/catalog_option_contract_test.go`，新增 `tests/integration/cti_catalog_postgres_test.go`。

**Interfaces:** Catalog DTO 增加 `defaultTicketCategoryId: number|null`、派生 `defaultCTIPath`；`ResolvedCatalog`增加 `DefaultTicketCategoryID *int`。`publicCatalogDefinition`纳入默认ID及路径语义版本，沿用既有确认版本/冲突机制。

- [x] 在现有目录和intake fixture中新增红测：草稿可空；启用后发布空/部分/停用CTI拒绝；用户无CTI提交时生成最深节点；伪造不一致CTI拒绝；目录默认值改后旧确认版本拒绝且无工单/审计/Outbox半写入。（新增 `handlers/service_catalog/cti_default_publication_test.go`、`handlers/intake/cti_catalog_default_test.go`；先红后绿）
- [x] 运行 `go test ./handlers/service_catalog ./handlers/intake -run 'CTI|Catalog' -count=1`，记录功能失败。（红：`catalog publication configuration is incomplete`／默认分类未贯穿；实现后全绿）
- [x] 调整 `resolver.go` 当前“分类先、目录后”顺序：先校验目录权限/版本，再取默认分类，构造规范CTI输入，再调用现有classification resolver。独立报障仍接受无/部分CTI。目录存在默认值时，以目录为初始权威；客户端提供相同路径可兼容，不同路径返回冲突。未启用且旧目录无默认值时保留既有行为。

```text
catalog request → catalog owner resolves version/default
                → classification owner resolves full path
                → existing professional creator prepares WorkItem
ordinary report → classification owner resolves optional path
                → same professional creator / transaction / outbox
```

- [x] 字段贯穿创建/更新/复制/读取DTO、Ent映射、预检和版本；禁止前端仅提交默认ID而后端忽略。目录修改不更新旧工单。
- [x] 测试事务竞争：目录默认变更vs申请，祖先停用vs申请；旧幂等回执应返回原结果，不按新目录重新创建。AI/intake快照不泄露或复制第二份可写分类。
- [x] 隔离PG验证通过后提交 `feat: apply catalog CTI defaults through unified intake`。**PG 用例已编写并通过编译，未执行（NOT RUN）。**

### 执行记录（A3）

- `ResolvedCatalog.DefaultTicketCategoryID *int` + `CreateWorkItemCommand.CatalogDefaultCategoryID *int `json:"-"``：
  目录默认值由目录所有者解析后填入，**不接受客户端 JSON**（客户端自报会被忽略，测试断言该字段不出现在序列化结果中）。
- `handlers/intake/resolver.go` 顺序改为“目录先（权限/版本/表单）→ 取默认分类 → 分类所有者解析完整路径”；
  独立报障路径不变。新增 `requireCatalogDefaultCTI`：仅在 `catalogEnforced` 启用且目录无默认值时失败关闭，
  未启用时保留既有行为（部署新代码不阻断旧目录申请）。
- `service/ticket_category_creation.go` 重写为单一权威入口：
  - 目录默认值 → 必须解析出当前有效的完整三级路径（`requireFull=true, requireActive=true`），客户端提供不同最深节点直接冲突；
  - 内联/遗留名称路径 → 槽位必须连续，每个提供的节点必须出现在真实路径中且相对顺序一致
    （保留“多级节点用最深槽位表达”的历史契约），并统一走同一套租户/层级/启用校验；
  - 名称投影改取真实路径，工作流变量不再可能出现自报分类名。
- 目录侧：`ServiceCatalog.DefaultTicketCategoryID`（0=未配置）+ `DefaultCTIPath` 只读投影；DTO 增加
  `defaultTicketCategoryId`（null=未配置）与 `defaultCTIPath`；创建/更新请求可提交（更新传 0 即清除）；
  `publicCatalogDefinition` 纳入默认 ID 与派生路径（含名称/启用状态），因此改默认、改名、分类改名或停用节点
  都会改变 `CatalogVersion`，旧确认按既有 409 语义失效。
- 发布校验 `validatePublishedDefaultCTI`：门禁启用时缺省默认分类拒绝发布；已配置默认分类时必须是当前有效
  完整三级路径（与门禁是否启用无关）。草稿仍可空，但跨租户默认值在提交事务内拒绝（区分“不存在/跨租户”与
  真正的数据库故障，不再把基础设施错误伪装成租户问题）。
- 新增 `service/cti_governance.go`：读取 `system_configs` 保留键 `cti_governance_v1` 的结构化只读记录
  （`catalogEnforced`/`completionEnforced`/`effectiveFrom`）；未配置=未启用；重复行/非法 JSON/非法时间明确报错。
  受控写入与完成门禁判定由 B2 补齐。
- SQLite 支持性修正：`level` 列改为派生缓存语义，路径解析与创建/移动用真实父链计算层级，
  历史陈旧 level 不会把合法路径误判为非法（迁移预检单独报告这类脏数据）。

验证证据：

```text
go test ./service -run 'TestReadCTIGovernance' -count=1                     -> ok
go test ./handlers/service_catalog -run 'TestCatalog' -count=1              -> ok
go test ./handlers/intake -run 'TestCatalogDefault|TestCatalogEnforcement|TestOrdinaryReport' -count=1 -> ok
go test ./... -count=1（全量）                                              -> 无 FAIL
go vet -tags integration_postgres ./tests/integration/                      -> OK（含 TestCTICatalogPostgres）
```

未执行项：`TestCTICatalogPostgres` 实际执行（需授权隔离目标）；目录默认分类补配清单（B4）；门禁启用。

## Task A4：三级维护界面与共享选择器

**Files:** 修改 `src/app/(main)/admin/ticket-categories/page.tsx`、`categoryTreeUtils.ts`、`src/lib/api/ticket-category-api.ts`；新增同路由 `components/CategoryTreePanel.tsx`、`CategoryDetailsPanel.tsx`、`CategoryEditor.tsx`；新增 `src/components/business/CTISelector.tsx` 及相邻 `__tests__/CTISelector.test.tsx`；修改 `admin/service-catalogs/page.tsx`、`types/service-catalog.ts`、`lib/api/service-catalog-api.ts`；报障/坐席表单消费处通过实际引用检索逐一列入提交说明。

- [x] 组件红测：三级节点没有新增下级；选择二级在requiredDepth=0合法而requiredDepth=3报错；部门说明不暗示分派；异步失败不显示空结果；改名/编码只读/冲突保留输入。

```tsx
it('allows no classification for an ordinary report', () => {
  const onChange = jest.fn();
  render(<CTISelector value={null} onChange={onChange} requiredDepth={0} />);
  expect(screen.queryByText('请选择完整三级分类')).not.toBeInTheDocument();
});
```

- [x] 运行 `npx jest --runInBand --coverage=false CTISelector.test.tsx`；fixture 使用 jest.mock 惯例，未连接共享服务（9/9 通过）。
- [x] 构建左树右详情、完整路径搜索、明确C/T/I标题、窄屏切换。树只导航，详情与编辑使用原 API；保留状态/错误，不做分派或 SLA 业务复制。
- [x] 目录管理提供默认 CTI 选择（`CTISelector` 直接放入发布表单，发布状态必填）；目录申请不重复问用户 CTI（后端按目录默认解析）；普通报障允许“不确定”（requiredDepth=0 + 可清除）。引用页签在 B3 接口就绪前**不显示**，未放置任何假计数或占位数字。
- [x] 运行 `npm run type-check`（通过）与目标组件/页面测试；`tests/e2e/flows/cti-catalog.spec.ts` 已编写（建三级→发布目录→用户申请→校验默认分类最深节点随请求提交），**NOT RUN**（需把本分支部署到隔离环境，禁止切换共享 3010/8080）。
- [x] 提交 `feat: present CTI maintenance and catalog defaults`。记录阶段 A 验收，明确 B 未完成。

## Task B1：分类纠正的专业命令与审计

**Files:** 修改 `service/incident_service.go`、`service/ticket_service.go`、`handlers/problem/metadata.go`、`handlers/change/metadata.go`、`handlers/service_request/{service,handler}.go` 的专业入口（分类纠正实现放入新增 `handlers/service_request/classification.go`，由该服务拥有，不经generic更新），以及相应DTO/权限动作投影；新增 `tests/integration/cti_correction_postgres_test.go`。不能让专业类通过generic TicketService绕过已有拒绝规则。

- [ ] 写红测 `TestCTICorrectionPostgres`：新Requested Item完整→空/部分拒绝、完整→完整成功；Incident resolved未closed可修正CTI；closed/cancelled不新增修改通道；跨租户/无权/过期版本失败；原因空失败。
- [~] 在现有变更回执/专业命令中加入原因与路径验证，优先复用Incident已有专业修改入口（已核实resolved后可更新），不默认新增接口。各专业拥有者仍控制状态和权限。**进行中：Problem 已接入；Incident/Change/ServiceRequest 待接入。**
- [ ] 原子持久化最深节点、版本及既有审计，记录actor/source/原因/前后路径；同一次重试复用已有幂等语义。审计失败整笔回滚。
- [ ] 验证分类前后 `assignee_id`、SLA时间/周期、BPMN实例数、目录默认ID不变；并发纠正只有一个预期版本成功。历史在途单不新增完整性阻断。
- [ ] 前端只有后端授权动作允许时显示纠正入口，复用CTISelector并填写原因；显示停用的既有合法分类但不能重新选为新值。
- [ ] 运行受影响专业metadata测试及隔离PG测试后提交 `feat: govern WorkItem classification corrections`。

## Task B2：专业完成质量与不可变启用边界

**Files:** 新 `service/cti_governance.go`、`cti_governance_test.go`；复用 `service/system_config_service.go`，修改专业完成拥有者：`service/ticket_service.go`、`service/incident_commands.go` 的close命令、`handlers/problem/lifecycle.go`、`handlers/change/commands.go`；新增 `tests/integration/cti_completion_postgres_test.go`。调用位于专业命令事务内，不能只在HTTP控制器校验。

**Interfaces:** 新 `CTIGovernance` 类型含 `CatalogEnforced bool`、`CompletionEnforced bool`、`EffectiveFrom *time.Time`；新纯函数 `RequiresCTICompletion(policy CTIGovernance, createdAt time.Time, recordClass, action string) (bool,error)`，action采用已有专业命令语义，未知class/action失败。时间相等属于新单，使用服务端存储createdAt，取消/重复/误报沿专业终止命令处理。

- [x] 红测以下确定边界：截止前1ns不检查、相等检查；未启用不追溯；配置损坏（启用但缺截止时间）报错而不是按“关闭”处理；未知 class/action fail closed。（`service/cti_governance_test.go`，5 组用例全绿）

```go
func TestCTICutoffIncludesExactInstant(t *testing.T) {
    cut := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
    p := CTIGovernance{CompletionEnforced: true, EffectiveFrom: &cut}
    needed, err := RequiresCTICompletion(p, cut, "incident", "close")
    if err != nil || !needed { t.Fatalf("exact cutoff must apply: %v %v", needed, err) }
}
```

- [x] 运行 `go test ./service -run 'TestCTI(Cutoff|Completion)' -count=1` → ok（先红后绿）。
- [x] 将检查置于各专业状态写入的同一事务；Incident resolve不加硬门禁，close检查；Generic resolve和直接close覆盖；Problem/Change最终close检查；Requester交付规则不重写，CatalogTask不新增检查。
- [x] 启用配置受同一事务锁保护并审计：第一次启用设置截止，以后只能启停布尔值，不能改时间；通用SystemConfig创建/更新/批量/删除均拒绝保留键绕过。回退暂停不撤销已完成动作，恢复时返回暂停期在途未分类单数量下界。
- [x] 真实路径验证：不完整Incident能resolve，close被拒绝，补分类后close成功且SLA时间不变；分类停用前已合法引用的新在途单可close；专业原有证据不足即使CTI完整仍拒绝（测试中必须显式提供恢复证据才能 resolve）。
- [x] 覆盖手工与批量入口（`BatchCloseTickets` 复用 `CloseTicket`，gate 随之生效）；Generic resolve/close 走 `closeWithCompletionGate`（同事务读权威行→门禁→CAS 写入）；Problem/Change/Incident 在各自 `applyCommandTx` 内、专业证据校验之后写入门禁。提交 `feat: enforce domain-owned CTI completion quality`。

注：`docs` 中记录的「流程回调/自动关闭/工具入口」目前复用上述同一命令入口，未逐一新增用例；如需独立证据应在 B4 的验收清单中补齐。

## Task B3：规则精确/子树语义与真实关联查询

**Files:** 修改 `service/ticket_assignment_rule_service.go`、`service/ticket_sla_service.go`、对应规则DTO/schema及API管理界面；扩展A2的分类引用服务/controller，新增 `service/ticket_category_policy_test.go`、`tests/integration/cti_rule_scope_postgres_test.go`。新增scope持久化用后续canonical迁移，不改A2已落库SQL。

- [ ] 写纯函数红测：精确只匹配selectedID；subtree匹配完整路径任一祖先；未知scope拒绝。写PG回归覆盖旧规则的实际命中集合，不能先假设旧规则全部exact。

```go
func TestCTIScopeDistinguishesAncestor(t *testing.T) {
    path := []CTINode{{ID: 1}, {ID: 2}, {ID: 3}}
    exact, err := MatchCTI(path, 1, CTIExact)
    if err != nil || exact { t.Fatal("exact matched ancestor") }
    subtree, err := MatchCTI(path, 1, CTISubtree)
    if err != nil || !subtree { t.Fatal("subtree missed ancestor") }
}
```

- [ ] 运行 `go test ./service -run 'TestCTIScope' -count=1`；实现只处理分类条件，规则优先级由原所有者保留。
- [ ] 审核SLA分类匹配是否扫描正确候选（当前代码先取活跃候选再判断category，不能以注释证明正确）；修复本任务必要命中缺口并测试多候选/优先级。旧字符串分类条件保留其实际语义，歧义对象阻止迁移，不静默转ID/扩大集合。
- [ ] 分类详情引用接口返回授权可见目录/规则列表、分页和总数；所有者提供查询契约，聚合层不跨域调用repository。内部维护引用保护须覆盖所有真实引用，即使当前actor无权查看，返回通用“存在引用”而不泄露对象名称/计数。
- [ ] 规则编辑明确“仅当前/包含下级”，分类详情提供管理链接。引用查询失败明确报错；disabled/default_resolver等历史描述字段不伪装成已生效规则。
- [ ] 验证跨租户与RBAC、旧规则命中集一致、新范围命中、改分类无自动再执行；提交 `feat: expose CTI rule scope and authorized references`。

## Task B4：全链路验收、文档与受控启用准备

**Files:** 新 `itsm-frontend/tests/e2e/flows/cti-governance.spec.ts`；维护本计划执行表、设计状态、`AGENTS.md`/`CLAUDE.md`、`docs/DEVELOPMENT_GUIDE.md`、`docs/README.md`。共享写入操作清单在本计划执行记录下维护，包含每个target fingerprint，不新建第二份环境权威。

- [ ] 针对两租户隔离fixture执行完整路径；流程只用安全测试履约，不调用真实AD/邮件/权限开通。每步记录账户/菜单/数据/动作/预期/证据。
- [ ] 必验矩阵：普通用户未分类提交；目录预分类；工程师修正；Incident恢复与关闭；Generic完成；Problem/Change保留专业证据；旧单兼容；RequestedItem分类退化拒绝；分类移动/停用/删除；精确/子树；回退/恢复。
- [ ] 新e2e应使用实际UI控件，例如：

```ts
await page.getByRole('button', {name: '关闭工单', exact: true}).click();
await expect(page.getByText('请补齐三级工单分类', {exact: true})).toBeVisible();
// 同时通过现有隔离fixture的只读数据库断言核对状态/审计无半写入。
```

- [ ] 执行受影响Go测试、生成检查、前端type-check与目标Jest/Playwright。浏览器验收失败不得以API成功代替；列出未验证功能。
- [ ] 交付可审阅启用清单：备份/恢复证据、实例/库/schema/租户、schema迁移及verify、已发布目录补配ID列表、唯一写入者、配置截止时间、分阶段开关、暂停方式和复验。没有目标写入授权只交付清单，不执行。
- [ ] 按证据更新设计为implemented时必须区分代码交付与目标启用状态；入口文档不写全局“所有环境已升级”。运行 `git diff --check`、请求独立代码review并处理重要问题，提交 `test: verify CTI governance journeys and rollout guide`，提交PR由用户按既有交付授权决定推送/合并。

## 自检覆盖与执行记录

| 设计内容 | 落地任务 |
| --- | --- |
| CTI/目录/专业类型边界，旧文档取代 | A1、A4、B4 |
| 数据结构、路径、三级约束、引用维护 | A2 |
| 目录默认值、发布、统一创建 | A3、A4 |
| 纠正/终态/RequestedItem完整性 | B1 |
| 专业完成时点、新旧、暂停恢复 | B2 |
| 分派/SLA范围、真实引用 | B3 |
| 权限、事务竞争、UI验收、部署边界 | A2–B4 |

执行记录初始状态：A1–A4、B1–B4均未开始。本轮仅验证计划引用/格式及设计覆盖，未运行生产代码测试、迁移或业务验收。


### 测试命令与目标保护

Go命令在 `itsm-backend` 执行，npm命令在 `itsm-frontend` 执行。新增PG测试沿用 `tests/integration/catalog_reader_postgres_test.go` 的连接、角色及fixture机制，先阅读其环境变量/skip条件再执行；没有隔离目标时必须报告未运行，不能把skip当通过。仅在确认目标允许测试写入后执行：

```bash
go test ./tests/integration -run 'TestCTI(Structure|Correction|Completion|Scope|Catalog|Rule)' -count=1 -v
npm run type-check
npx playwright test tests/e2e/flows/cti-catalog.spec.ts tests/e2e/flows/cti-governance.spec.ts
```

PG测试stdout必须证明测试实际运行、RLS身份和隔离target fingerprint匹配；日志不得含密码/连接串。任何红测应先区分基线失败与本任务新增失败。迁移命令使用既有dev-commands-reference与canonical runner，不提供绕过账本的裸SQL执行捷径。

## 执行记录（A1：基线、引用清单与启用契约）

状态：A1 已完成（代码/文档范围）。**未执行**任何共享库写入、迁移应用或门禁启用。

### 基线

| 项 | 值 |
| --- | --- |
| 实施 worktree | `.worktrees/cti-governance` |
| 实施分支 | `codex/feat/cti-governance`（自 `origin/main` 创建） |
| 代码基线 | `origin/main` = `b8ac9639b4197c9c93ee2e72747e46bf59958664` |
| 设计/计划提交 | `8ff8aea2`（分支 `origin/codex/docs/cti-governance-design`，相对基线 ahead 3 / behind 0，已快进合入实施分支） |
| 实施前脏文件 | 仓库根 checkout 的 `itsm-frontend/test-results/junit.xml`（其他任务产物，未触碰） |

`git status --short`（实施 worktree）：干净。

### 迁移账本

- `itsm-backend/migration/migrations.go` 的 `RegisteredMigrations` 最大版本为 `047_bpmn_assignment_source`；
  045/046 分别为 `045_notification_email_target`、`046_auth_token_state`。
- 全仓引用与全 ref 校对：`git log --all -- 'itsm-backend/migrations/048*'` 无记录，任何 `origin/*` 或本地分支的
  `itsm-backend/migration|migrations` 下都不存在 `048`。**下一个可用序号是 048，但本轮未预占、未写入**。
- 未修改任何历史 SQL 或既有 checksum。

### 环境与授权边界（A1 核对结果）

- 维护启动权威 `/home/administrator/apps/itsm-kaf/stack status`：`itsm` running（PID 256991，source `fc8de9d3`）、
  `itsm-web` running（3010）、`kaf*`/`itsm-worker-*` stopped（stale 记录）。即共享 Dev 上仍有其他任务的恢复/迁移工作。
- 仓库根 checkout 处于 `codex/migration-legacy-config-data`；另有 `.worktrees/{database-reconciliation,
  dev-restoration-20260916,dev-restoration-api-20260916,config-migration-review,workitem-config-migration,…}` 处于在途状态。
  **结论：共享 Dev / 迁移验证库被他人在途任务占用，本轮不做共享库写入、迁移或修复。**
- 文档化的隔离 PG 目标（`catalog_reader_postgres_test.go`/`migrationEntryTarget`）要求 `127.0.0.1:36444/sslvpn_test`；
  本机该端口无监听者，且本机只有 PostgreSQL 客户端工具（无 `initdb`/`postgres` 服务端），
  授权边界又禁止新建数据库容器。**因此本轮 PG 集成测试为未执行（NOT RUN），不以 skip 记通过。**
- 未使用私有启动配置/连接串作为测试默认值；未读取或输出凭据。

### 引用矩阵（源码核对，所有者与保护点）

| 消费者 | 引用形态 | 所有者 | 维护/查询入口 | 现有删除/移动保护 |
| --- | --- | --- | --- | --- |
| `tickets.category_id` | 真实 FK → `ticket_categories(id)`，`ON DELETE SET NULL` | WorkItem/Ticket 域 | `workitemcreation.NewPlan`（写最深节点）、`service/ticket_category_service.go` | 仅服务层 `DeleteCategory` 先计数工单；FK 是 SET NULL，DB 不阻止删除，存在先查后写竞态 |
| `SLADefinition.category_ids` | `JSON []int`（无 FK） | SLA 服务 | `service/ticket_sla_service.go` | 无保护；`getSLADefinition` 只取“第一个活跃定义”再判断 category，命中集合不完整 |
| `TicketAssignmentRule.conditions` | JSON 条件 `field=category_id`（equals/in/not_in → 精确匹配叶节点 ID） | 分派规则服务 | `service/ticket_assignment_rule_service.go`、`ticket_rule_conditions.go` | 无保护 |
| `TicketAutomationRule.conditions` / `actions` | JSON：条件同上；动作可设 `category_id` | 自动化规则服务 | `service/ticket_automation_rule_service.go`、`ticket_automation_creation.go` | 无保护 |
| `TicketTemplate.category_ids` | `JSON []int`（无 FK） | 工单模板 | `service/ticket_template_service.go`、前端 `TemplateList` | 无保护 |
| `ProcessBinding.category_id` + `category` | `int` + 遗留字符串 | BPMN 创建配置 | `service/bpmn_creation_configuration.go` | 无保护 |
| `IncidentEscalationRule.category_match` | 遗留字符串匹配 | Incident 域 | — | 字符串，无法映射为 ID；歧义对象须阻止受影响维护动作 |
| `ServiceCatalog.category` | 展示分组字符串 | 服务目录 | `handlers/service_catalog/*` | 展示分组，不自动映射 CTI（设计 §2）；本轮新增独立结构化 `default_ticket_category_id` |
| `KnowledgeArticle.category` | 展示/检索字符串 | 知识库 | `service/knowledge_integration_service.go` | 遗留字符串，不属 CTI 引用 |
| 前端 | `TicketCategorySelector`、`WorkItemClassificationSelect`、`admin/ticket-categories`、目录管理页 | 前端 | A4 | — |

字符串/JSON 引用不能因“没有外键”被忽略：A2 的引用检查覆盖上述结构性引用（JSON 中的 ID 列表按租户扫描），
歧义或不可映射的遗留字符串阻止受影响的维护动作并返回可操作错误。

### 启用契约（固定记录）

- 位置：既有 `system_configs`；保留键 `cti_governance_v1`，每租户唯一。
- 值（JSON）：`catalogEnforced`、`completionEnforced`、`effectiveFrom`（首次启用完成门禁时写入，此后不可改）。
- 读取失败 / 重复行 / 非法值 → 报错（**不默认为关闭**）；从未配置 = 未启用。
- 通用系统配置写入/导入接口须拒绝该保留键；受控激活走独立路径并审计。由 B2 实现并验证。

### 基线测试

```text
cd itsm-backend && go test ./service -run 'TestMoveCategoryUpdatesParentSortOrderAndDescendantLevels|TestMoveCategoryRejectsMovingUnderDescendant' -count=1
--- PASS: TestMoveCategoryUpdatesParentSortOrderAndDescendantLevels (0.03s)
ok  	itsm-backend/service
```

`go test ./service -run 'TestTicketCategory'` 无匹配测试（既有用例名不含该前缀），已改用实际用例名记录。

### A1 未执行项（需授权或目标）

1. 真实授权租户上下文的只读分类盘点（三级数量、超深度、根错误、孤儿、环、跨租户父链、错误 level、编码冲突、
   全部业务/配置引用、已发布目录缺配）。需要明确的目标指纹与只读凭据；本轮未使用他人私有启动配置。
2. 任何共享 Dev / 迁移验证库的写入、迁移应用与门禁启用。
3. PG 集成测试（无授权隔离目标）。

### 执行记录（A4）

- 新增共享选择器 `src/components/business/CTISelector.tsx`（value = **所选最深节点 ID**，`requiredDepth: 0 | 3`），
  错误/加载/空态三态分离：异步失败渲染可重试的错误而非空结果；`requiredDepth=3` 且路径不足三级时给出
  「请选择完整三级分类」并回调 `onValidityChange(false)`；`requiredDepth=0` 允许未分类与部分分类。
- `WorkItemClassificationSelect` 改为**适配层**：分类选择/校验统一委托 `CTISelector`，
  仅把「最深节点」与既有调用方的路径表示（`number[]`）互转，事件/问题/工单详情页 props 契约不变
  （既有 8 项 classification-edit 测试全部通过，证明无回归）。
- 维护界面重构为**左树右详情**：`CategoryTreePanel`（完整路径搜索、状态标签、一级/二级可「新增下级」，
  **三级不提供**）、`CategoryDetailsPanel`（C/T/I 明确标题、完整路径、编码只读、状态、所属部门并明确
  “不代表自动分派对象”）、`CategoryEditor`（新建下级锁定父级、**父级选择禁用三级节点**、
  编码编辑态只读、保存失败保留输入且弹窗不关闭）。窄屏树/详情互斥切换（`xs` 单栏 + `md` 并排）。
- 维护界面改用 `getCategoryTree({ includeInactive: true })`：停用节点必须可见可维护；
  `ticket-category-api` 增加 `CTIPathNode`、`pathIds`、`CTI_MAX_LEVEL/CTI_COMPLETE_LEVEL`，调用方不再各写死“3”。
- 目录契约贯通：`ServiceItem.defaultTicketCategoryId/defaultCTIPath`、创建/更新 payload（更新传 0 = 清除）、
  管理端表单使用同一 `CTISelector` 且「发布」状态下必填（客户端提示，后端仍是权威）。

验证证据（`itsm-frontend`）：

```text
npm run type-check                                                -> 通过（tsc --noEmit，附 theme:check）
npx jest --runInBand --coverage=false CTISelector.test.tsx        -> 9/9
npx jest CategoryMaintenance.test.tsx                             -> 7/7
npx jest CategoryMaintenancePage.test.tsx                         -> 4/4
npx jest categoryTreeUtils.test.ts ticket-category-api.test.ts    -> 通过（含新增路径/停用/常量用例）
npx jest classification-edit.test.tsx                             -> 8/8（共享选择器替换后的回归证据）
npx jest service-catalog-api/useServiceCatalog/ticket-category-service/incident-classification/catalog-reload -> 62/62
npx jest src/app/(main)/tickets/create src/components/work-item     -> 71/71
npx eslint <changed files>                                         -> 无输出
```

环境说明（不影响结论，但需知悉）：

- `npm ci` 在本仓库状态下失败：`package.json` 与 `package-lock.json` 已不同步（pre-existing，缺 `workerpack`/`webpack` 等条目）。
  未修改 lock/package.json。验证使用**与之逐字节相同的 lockfile** 的既有 worktree 依赖树
  （`package.json`/`package-lock.json` 的 git hash 与本 worktree 完全一致），通过 `node_modules` 符号链接复用；
  该目录已在 `.gitignore` 中，未进入提交。
- `itsm-frontend/test-results/junit.xml` 会在 Jest 运行时被覆盖（仓库中已被跟踪且未被忽略），
  已 `git checkout` 还原，未纳入提交。

未执行项：Playwright `tests/e2e/flows/cti-catalog.spec.ts` 与浏览器验收（需隔离部署）；B1–B4 全部未开始。

### 执行记录（B2，部分完成）

已完成：**纯策略 + 确定边界测试**（`service/cti_governance.go` / `cti_governance_test.go`）

- `RequiresCTICompletion(policy, createdAt, recordClass, action) (bool, error)`：
  - 受控动作矩阵显式声明 `gate`/`not-gate`：generic resolve+close 门禁；incident **close 门禁、resolve 不门禁**；
    problem/change_request/service_request_item 的 close 门禁；cancel/reopen/pending/delivered 明确不门禁；
    `catalog_task` 不新增门禁；**矩阵外的 class/action 返回错误**（fail closed）。
  - 边界：`createdAt >= EffectiveFrom` 适用（**相等适用**，截止前 1ns 不适用）；未启用不追溯；
    `completionEnforced=true` 但缺 `effectiveFrom` 视为损坏配置并报错，绝不静默按“关闭”处理。
- 验证：`go test ./service -run 'TestCTI(Cutoff|Completion|Governance)' -count=1` → ok。

**未完成（不得视为已交付）**：门禁尚未接入任何专业完成事务。
剩余工作与插入点：
1. `service/ticket_service.go` `ResolveTicket/CloseTicket/BatchCloseTickets`（generic resolve/close，含批量入口）——
   当前实现为 `repo.Update` 单行更新、无事务，需要先建立事务边界再写入门禁与审计。
2. `service/incident_commands.go` close 命令、`handlers/problem/lifecycle.go` `applyCommandTx`、
   `handlers/change/commands.go` `applyCommandTx`：这些路径已有事务，是最安全的接入点；
   需要先确认专业扩展到 WorkItem 的关联字段，再从 WorkItem 读取**最深节点**校验完整性（专业表不复制 CTI）。
3. 受控激活：首次启用写入 `effectiveFrom` 后不可修改（只能启停布尔值），
   并在通用 SystemConfig 写入/导入 API 拒绝 `cti_governance_v1` 保留键。
4. 真实路径验证：不完整 Incident 可 resolve、close 被拒绝、补分类后 close 成功且 SLA 时间不变；
   覆盖手工/批量/流程回调/自动关闭/工具入口，确认没有可达关闭路径绕过检查。
5. 新增 `tests/integration/cti_completion_postgres_test.go`（需授权隔离目标，NOT RUN）。

### 执行记录（B2，完成）

实现（全部与状态写入同事务，绝不只在 HTTP 控制器校验）：

- `service.EnforceWorkItemCompletionCTI`：专业命令共同适配层。业务拒绝返回 `common.NewValidationError`
  （调用方按专业语义返回 400 类错误），策略/基础设施故障原样返回使整笔事务失败，**不降级为允许**。
- 接入点：
  - `handlers/problem/lifecycle.go` `applyCommandTx`（专业证据校验之后、状态写入之前）；
  - `handlers/change/commands.go` `applyCommandTx`（同上，close 前）；
  - `service/incident_commands.go` `applyIncidentCommandTx`（同上）；
  - `service/ticket_service.go` 新增 `closeWithCompletionGate`：同一事务内读取权威行（服务端 createdAt +
    最深节点）→ 门禁 → `UpdateTx` CAS 写入 → 提交；`ResolveTicket`/`CloseTicket` 都改走该路径，
    `BatchCloseTickets` 因此自动覆盖。
- 完整性语义：**只要求「路径存在且为三级」，不要求当前启用**——停用只禁止新选择，分类停用前已合法引用的
  在途单必须仍可完成，否则停用一个节点会永久卡死历史工单（有测试固定该行为）。
- 受控启用 `SystemConfigService.SetCTIGovernance`：第一次启用写入 `effectiveFrom`（服务端时间）且此后不可修改；
  暂停只改布尔值并保留截止时间；幂等重放不重复写审计；恢复时返回暂停期在途未分类单数量下界；
  同事务写 `AuditLog`（actor/source/前后值指纹），审计失败整笔回滚。路由
  `PUT /api/v1/system-configs/governance/cti`（沿用 `system_config:update`），刻意使用静态路径避免与 `/:id` 冲突。
- 保留键保护：`CreateSystemConfig`/`UpdateSystemConfig`/`BatchUpdateSystemConfigs`/`DeleteSystemConfig`
  一律拒绝 `cti_governance_v1`（批量请求整笔拒绝，不留下部分写入）。
- 动作矩阵完整性：`TestCTICompletionMatrixCoversOwningCommandActions` 逐个核对各专业命令的动作集合，
  新增动作若未归类会在此暴露（运行时仍 fail closed）。

验证证据（`itsm-backend`）：

```text
go test ./service -run 'TestCTI|TestRequireWorkItem|TestSetCTIGovernance|TestReserved' -count=1   -> ok
go test ./service -run 'TestIncidentCompletionCTIGate' -count=1                                    -> ok（真实专业命令路径）
go test ./service -run 'TestTicketCompletionCTIGate' -count=1                                      -> ok（generic resolve+close）
go test ./handlers/problem ./handlers/change -count=1                                              -> ok
go test ./... -count=1                                                                             -> 无 FAIL
go vet ./...（仅 3 处与本任务无关的既有告警：service/bpmn/ticket_handler.go 等，未在本分支改动）
```

未执行项：`tests/integration/cti_completion_postgres_test.go`（需授权隔离目标，NOT RUN）；
流程回调/自动关闭/工具入口的独立用例；前端启用开关（B4）。

### 授权隔离目标后的补充执行证据（2026-09-17）

隔离目标（自建、一次性、仅本机）：

```text
container codex-cti-governance-pg-20260917
labels      com.itsm.test.owner=cti-governance, com.itsm.test.disposable=true
image       postgres:17（本地既有镜像）  data=tmpfs（停止即丢弃）
endpoint    127.0.0.1:36444 -> 5432（与文档化 INTAKE_POSTGRES_TEST_DSN 指纹一致）
database    sslvpn_test
```

```text
INTAKE_POSTGRES_TEST_DSN=postgres://cti_test:<本地随机口令>@127.0.0.1:36444/sslvpn_test?sslmode=disable
ITSM_TEST_DB=<同一 DSN>（部分用例契约使用该变量）

go test -tags integration_postgres ./tests/integration -run 'TestCTI' -count=1 -v
  -> PASS TestCTICatalogPostgres（2 子项）/ PASS TestCTICompletionPostgres（4 子项）/ PASS TestCTIStructurePostgres（5 子项）
  -> 每个用例独立 schema 并在结束时 DROP，remaining=0（已用 pg_namespace 复查）

go test -tags integration_postgres ./tests/integration -run 'CTI|Migration|Catalog' -count=1 -p 2 -v
  -> 22 个顶层用例 PASS、38 个子用例 PASS，1 个 FAIL：TestWorkItemControlledRetirementLaterMigrationsAndExactExecution
  -> 该 FAIL 发生在应用 **039_candidate_execution_scope** 时（`candidate execution scope requires explicitly selected public schema`），
     即 048 尚未执行；其夹具 `migrationEntryFixture` 把 search_path 指向新建 schema，而 039/040/044 硬性要求 current_schema()=public，
     该族按文档需要专属 V2 目标（36542/workitem_v2_task2_test + WORKITEM_V2_POSTGRES_TEST_DSN），
     在 legacy 36444 目标上结构性不可通过 => 与本任务改动无关（本任务从未修改 039）。
```

**真实目标发现的缺陷（已修复）：**

1. **产品缺陷（严重）**：`048` 预检规则 5a 的深度 CTE 以节点的 `level` 作为初始 depth 并判定 `depth >= 3`，
   导致**任何合法的三级分类都会被判为“超三级”并让迁移直接失败**——即 048 在生产库上根本无法应用。
   已改为按“自身算作第 1 层”计数并判定 `depth > 3`，并同步新增明确的错误文案（`deeper than three levels`）。
2. **产品缺陷**：规则 5b 的环检测把“闭合那一跳”的行用 WHERE 过滤掉了，实际只能捕获自环；
   现改为在递归中保留该行并标记 `cycle`，可真正检出 A→B→A 这类父链环。
3. **PG 夹具缺陷（A2 编写时未执行过）**：`three_level_limit_is_enforced` 把一级节点当作转移父级，
   实际最深只有 3 层、不该拒绝；已改为“移到二级父级下（4 层）必须拒绝 + 移到一级父级下（3 层）必须成功”双侧断言。
4. **PG 夹具缺陷**：`migration_preflight_failure_rolls_back_without_touching_ledger` 在已有跨租户同名编码的数据上
   重建旧的全表唯一索引，导致夹具自身先失败；已在还原旧形态前先消除跨租户重复。

由此，A2 的三级/环与迁移原子性、A3 的目录默认分类租户隔离、B2 的门禁事务内判定与并发启用，
均获得**真实 PostgreSQL 证据**（此前为 NOT RUN）。

### 执行记录（B1，部分完成）

已交付：**共享纠正契约 + Problem 拥有者接入**

- `service/cti_correction.go`（新）：
  - `RequireCTICorrectionReason(reason, changed)`：分类**确实变化**时原因必填，未变化时不强迫填原因；
  - `ValidateCTICorrectionTargetTx` + `CTICorrectionTargetPolicy{AllowClear, RequireComplete}`：
    目标必须存在、同租户、启用且父链连续；**是否要求完整三级由调用方声明**——
    目录申请项（Requested Item）`RequireComplete=true`，事件/问题/变更等专业纠正为 `false`，
    因为完整度是 B2“完成质量门禁”的完成时要求，而不是纠正时的门槛；否则在租户完成分类补配前
    工程师无法修正任何记录（原实现一度强制三级，已按此理由纠正，并由现有 HTTP 契约测试证明二级目标仍合法）。
    跨租户与不存在共用同一错误，不泄露其它租户对象是否存在；
  - `CTICorrectionEvidence.Metadata(reason)`：把「原因 + 前后完整路径快照（ID/名称/编码/层级/启用状态）」
    写进**拥有者既有的操作回执**，而不是新增第二行审计——
    `audit_logs` 上有 `(tenant_id,user_id,operation_id)` 的操作回执唯一索引，同一操作插第二行会直接违反约束
    （这一点是在真实 SQLite 用例上撞到 23505 后修正的）。
- `handlers/problem/metadata.go`：`p.CategoryID` 分支改为「原因校验 + 目标路径校验 + 前后路径证据写回执」，
  全部在既有专业事务内；`dto.UpdateProblemRequest` 新增 `classificationReason`。
- 测试：
  - 新 `service/cti_correction_test.go`（原因规则、策略矩阵、停用/跨租户/不存在目标、证据形状、非法输入）；
  - `handlers/problem/classification_contract_test.go` 扩展为三重契约：变化必须有原因（缺原因 → 拒绝且
    分类/版本不变、无回执）、回执必须含 `classificationReason/classificationBefore/classificationAfter`、
    既有“非活跃/跨租户/不存在不落库”断言保持。

验证证据：

```text
go test ./service -run 'TestRequireCTICorrectionReason|TestValidateCTICorrectionTarget|TestCTICorrectionEvidence' -count=1 -> ok
go test ./handlers/problem -count=1 -> ok
go test ./... -count=1             -> 无 FAIL
```

**B1 剩余工作（未交付）**

1. Incident：`service/incident_service.go` `UpdateIncident` 目前非事务（`s.client` 直连），
   要让「版本 CAS + 分类写入 + 回执/审计」原子化需先建立事务边界；DTO 需新增 `classificationReason`；
   现有前端 `categoryId: 0` 清空行为需按新契约改造（或显式传原因）。
2. ServiceRequest：按计划新增 `handlers/service_request/classification.go`，使用 `RequireComplete=true`，
   配套 DTO/权限动作投影与路由；Requested Item 的引用完整性仍走目录所有者。
3. Change：`handlers/change/metadata.go` 当前没有分类修改入口，若产品需要纠正能力需新增专业命令。
4. 前端：仅在“后端授权动作允许”时显示纠正入口，复用 `CTISelector` 并要求填写原因；
   停用的既有合法分类只展示、不可重新选为新值。
5. `tests/integration/cti_correction_postgres_test.go`：完整→空/部分拒绝、完整→完整成功、
   resolved 未 closed 可纠正、closed/cancelled 无新通道、跨租户/无权/过期版本失败、原因空失败，
   并断言 `assignee_id`/SLA 时间/周期/BPMN 实例数/目录默认 ID 不变与并发纠正只有一个预期版本成功。

### 执行记录（B1 增量二：Incident + 前端）

**后端（Incident 拥有者）**

- `service/incident_service.go` `updateIncident`：`req.CategoryID` 分支改为
  「原因规则（仅变化时必填）+ 目标路径校验（`AllowClear: true`，保留既有清空语义）+ 前后路径证据」；
  **无需新建事务边界**——该路径本来就运行在 `UpdateIncident`/`UpdateIncidentTx` 的事务中。
- 证据落点：分类变化时把既有 `incident_events` 时间线事件升级为 `classification_change`，
  `Description` 记录原因，`Metadata` 写入 `classificationReason/classificationBefore/classificationAfter`
  （与 Problem 使用同一 `CTICorrectionEvidence.Metadata` 形状）；写入失败与状态/字段写入一起回滚。
  请求级 actor/来源由既有审计中间件留痕，不再重复写第二行 `audit_logs`
  （该表有 `(tenant_id,user_id,operation_id)` 操作回执唯一索引）。
- 既有专业分类入口 `IncidentService.UpdateClassification` 增加必填 `reason` 参数，
  HTTP 端点 `/incidents/:id/classification`（及 `/incidents/classification`）绑定 `reason`（required, max=500），
  BPMN 服务任务改为从流程变量 `classification_reason` 取值、缺失时使用明确的系统来源原因。
- 测试：`TestIncidentUpdateClassificationIDContract` 扩展为「缺原因拒绝且零副作用 + 事件证据结构化断言
  （事件类型/原因/前后路径 ID 与名称）+ 既有停用/跨租户/不存在/回滚断言保持」；
  `incident_service_test.go` 跨租户用例同步传原因。

**前端**（4 个入口：事件编辑页、问题编辑页、事件详情内联分类、事件管理弹窗）

- `classificationUpdate(path, touched, reason)` 现在同时返回 `categoryId` 与 `classificationReason`（trim 后）。
- 每个入口在提交前做同一校验：分类被触碰但原因为空 → 阻断并提示「调整分类时必须填写原因」，
  避免「界面显示成功、后端 400」的错位；仅在编辑态渲染「分类调整原因」输入（新建态不渲染）。
- 测试：`classification-edit.test.tsx` 的两条变更用例补充原因并断言载荷，
  新增 incident/problem 两条「无原因被阻断且不发请求」用例；`incident-classification.test.ts`
  `classificationUpdate` 形状断言更新。

验证证据：

```text
itsm-backend:  go test ./... -count=1                                  -> 无 FAIL
itsm-backend:  go test ./service -run TestIncidentUpdateClassificationIDContract -count=1 -> ok
itsm-frontend: npm run type-check                                      -> 通过
itsm-frontend: npx jest classification-edit.test.tsx incident-classification.test.ts -> 13/13
itsm-frontend: npx jest "src/app/(main)/incidents" "src/app/(main)/problems" "src/components/incident" -> 8/8
itsm-frontend: npx jest src/components/work-item/__tests__             -> 71/71
itsm-frontend: npx eslint <changed files>                              -> 无输出
```

顺带发现（未处理，留待 B4 清理清单）：`src/lib/api/incident-api.ts` 的
`getIncidentClassification/createIncidentClassification/updateIncidentClassification` 三个 helper
**没有任何调用方**，且其路径与现有路由不一致（GET 不存在的 `/incidents/:id/classification`、
PUT 使用 `/incidents/classification/:id`）。它们在本改动前就已失效，属于独立清理项，不纳入本次提交以免混入无关改动。

**B1 剩余**：Change 与 ServiceRequest 拥有者接入；`tests/integration/cti_correction_postgres_test.go`。
