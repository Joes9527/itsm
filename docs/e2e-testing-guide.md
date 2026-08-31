# E2E 测试实施总结

## 阶段三完成：E2E 测试补充

### 新增测试文件

| 文件 | 测试用例数 | 说明 |
|------|------------|------|
| `tests/e2e/business-flows/approval-workflow.spec.ts` | 16 | 审批工作流完整生命周期测试 |
| `tests/e2e/business-flows/sla-monitoring.spec.ts` | 19 | SLA 监控完整测试 |
| `tests/e2e/utils/page-objects/ApprovalPage.ts` | - | 审批页面对象 |

### 测试覆盖范围

#### 审批工作流测试
- ✅ 工作流列表/创建/搜索/过滤页面
- ✅ 审批记录管理（待审批/已通过/已拒绝/历史 Tab）
- ✅ 工单审批流程（审批按钮可见性、审批记录关联）
- ✅ 权限测试（普通用户 vs 管理员）
- ✅ API 接口测试（列表、创建、审批提交）
- ✅ API 权限验证

#### SLA 监控测试
- ✅ SLA 仪表盘（页面加载、统计卡片）
- ✅ SLA 定义管理（列表/创建/搜索）
- ✅ SLA 监控（页面加载、告警列表、状态过滤）
- ✅ 工作流 SLA
- ✅ SLA API 接口（列表、监控数据、告警）
- ✅ SLA 与工单关联（工单详情 SLA 信息、工单创建时选择 SLA）
- ✅ SLA 报表（页面加载、时间范围选择）
- ✅ API 集成测试和权限验证

### Page Objects

| 类名 | 用途 |
|------|------|
| `ApprovalPage` | 审批工作流页面交互封装 |

### CI 集成

截至当前，`.github/workflows/` 下没有任何 workflow 运行本文档描述的 E2E/Playwright 测试
（`ci.yml` 不存在；现有 workflow 是 `backend-ci.yml`、`frontend-ci.yml`、`api-contract-check.yml`、
`test-coverage-guard.yml`、`ga-gate.yml`、`acl-gate.yml`、`security.yml`、`release.yml` 等，
其中 `ga-gate.yml` 只做组装后核心栈的健康检查和 API 烟测，不等同于本文档的业务流程 E2E）。
以下命令需要在本地或专门的 E2E 环境手动运行，尚未接入 CI 门禁。

### 运行命令

```bash
# 运行所有 E2E 测试
npm run test:e2e

# 运行审批流程 E2E 测试
npx playwright test tests/e2e/business-flows/approval-workflow.spec.ts

# 运行 SLA 监控 E2E 测试
npx playwright test tests/e2e/business-flows/sla-monitoring.spec.ts

# 运行冒烟测试
npm run test:smoke

# 运行业务流测试
npm run test:e2e:business
```

### 测试统计

| 指标 | 阶段三前 | 阶段三后 |
|------|----------|----------|
| 审批相关 E2E 测试 | 4 | 16+ |
| SLA 相关 E2E 测试 | 5 | 19+ |
| Page Objects | 6 | 7 |
| E2E 测试总数 | 50+ | 70+ |

### 后续建议

1. **持续补充**：针对其他核心业务（如知识库、服务目录）补充 E2E 测试
2. **测试数据准备**：实现测试夹具（fixtures）自动准备测试数据
3. **测试报告**：集成 Allure 或 HTML 报告生成
4. **测试并行化**：配置 Playwright 并行执行加速 CI

---

## Unified Intake PostgreSQL E2E（2026-09-01）

Unified Intake 的权威集成证据位于 `itsm-backend/handlers/intake/e2e_test.go`。它不是 SQLite 替代测试：必须连接一次性 PostgreSQL 数据库，通过真实 Gin HTTP 路由验证 Access JWT、KAF assertion exchange、精确重放、伪造 assertion 拒绝，以及 workflow Outbox 的 `dead → pending → published` 恢复。

```bash
cd itsm-backend
INTAKE_POSTGRES_TEST_DSN='<fresh disposable PostgreSQL DSN>' \
  go test -tags integration_postgres -v ./handlers/intake -run E2E -count=1

RLS_TEST_DSN='<separate disposable PostgreSQL owner DSN>' \
  go test -tags integration_rls -v ./database/rls/... -count=1
```

两个 suite 应使用不同的一次性数据库。RLS suite 会启用并强制策略；若复用同一数据库，普通 Intake E2E 没有 RLS session tenant 时会按设计 fail-closed，不能把该 503 误判为创建逻辑回归。RLS 输出出现任何 skip 都不构成发布证据。

Intake E2E 对每条命令断言恰好一个 receipt、WorkItem、专业扩展、resolution snapshot、workflow-start Outbox 和 process instance。测试结束后删除一次性数据库；禁止在共享 Dev/生产数据库运行会创建或清理探针数据的 suite。

完整的已执行命令、计数和非绿色基线见 [Unified Intake 实施报告](./reports/2026-08-31-unified-intake-implementation-report.md)。

### 尚未覆盖的真实浏览器路径

- BPMN 设计器对不支持元素的 warning 目前只有 React 单测，没有真实点击/保存 E2E。
- 自定义字段管理 UI 仍不能配置 `multiselect`/`boolean`；`file` 在工单创建页可渲染，但共享管理编辑器没有对应选项。
- Service Request 自定义流程部署和 tenant_id=0 平台操作目前由 Go 自动化覆盖，没有浏览器级验收。
- Unified Intake 还没有独立 UI/Session/附件 staging；现有 Incident 和 Service Catalog 页面通过专业 API 薄适配器进入 Intake。

历史场景与当时结果保存在 [BPMN 整改遗留项 E2E 测试计划](./archive/testing-reports/BPMN%20整改遗留项%20E2E%20测试计划.md)；其中命令和环境值仅作归档，当前执行以本指南和实施报告为准。
