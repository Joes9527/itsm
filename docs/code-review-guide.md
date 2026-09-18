# Code Review 流程规范

> 适用于 1-2 人小团队的轻量级 Code Review 流程

## 1. 流程概述

### 1.1 核心原则

| 原则 | 说明 |
|------|------|
| **小步提交** | 每次 PR 控制在 400 行以内，超过需拆分 |
| **快速反馈** | PR 必须在 24 小时内响应 |
| **对事不对人** | 评论聚焦代码，而非开发者 |
| **自动化优先** | 机器能检查的绝不人工检查 |

### 1.2 流程图

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  开发分支   │───▶│  提交 PR    │───▶│  CI 检查   │───▶│  代码审查  │
│  (feature) │    │             │    │ (自动通过) │    │  (人工)    │
└─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘
                                                              │
                           ┌───────────────────────────────────┘
                           ▼
                    ┌─────────────┐    ┌─────────────┐
                    │  需要修改   │───▶│  重新提交   │
                    │  (有评论)   │    │  (回到CI)   │
                    └─────────────┘    └─────────────┘
                           │
                           ▼
                    ┌─────────────┐
                    │   合并     │
                    │  (approved)│
                    └─────────────┘
```

## 2. PR 模板

### 2.1 模板文件位置

```
.github/
└── pull_request_template.md
```

### 2.2 模板内容

模板以 `.github/pull_request_template.md` 为准，**本规范不复制其内容**：一项规则只维护一个权威来源，复制出来的第二份必然漂移（这里曾长期写着 CI 并不运行的 `golangci-lint`）。

模板承载治理文档要求的字段——任务准入（[`agent-engineering-governance.md`](./agent-engineering-governance.md) §7）：单一目标与不在范围内的部分、影响范围、**是否写入共享数据库/执行迁移/改 RLS**、验证方式与完成条件、已知依赖与冲突分支；以及 PR 必述项（§5）：目标、影响范围、验证证据、风险与未验证项。

## 3. 检查清单

### 3.1 代码质量（必须检查）

| # | 检查项 | 严重度 |
|---|--------|--------|
| 1 | 无 `fmt.Println` / `console.log`（生产代码） | 🔴 P0 |
| 2 | 无 `panic()`（非灾难性场景） | 🔴 P0 |
| 3 | 错误已正确处理，无 `_ = err` 忽略 | 🔴 P0 |
| 4 | 无硬编码的敏感信息 | 🔴 P0 |
| 5 | 复杂逻辑有注释说明 | 🟡 P2 |
| 6 | 变量/函数命名清晰 | 🟡 P2 |

### 3.2 架构设计（重点检查）

| # | 检查项 | 严重度 |
|---|--------|--------|
| 1 | Controller 只做参数校验和响应转换 | 🟠 P1 |
| 2 | 业务逻辑在 Service 层 | 🟠 P1 |
| 3 | 无直接访问 DB 的查询（在 Repository 层） | 🟠 P1 |
| 4 | DTO 与 Ent 模型正确隔离 | 🟠 P1 |
| 5 | 多租户隔离正确（tenant_id 贯穿） | 🔴 P0 |

### 3.3 性能安全（必须检查）

| # | 检查项 | 严重度 |
|---|--------|--------|
| 1 | 无 N+1 查询问题 | 🟠 P1 |
| 2 | 大列表查询有分页 | 🟠 P1 |
| 3 | 敏感操作有日志记录 | 🟠 P1 |
| 4 | 权限校验在 API 层 | 🔴 P0 |
| 5 | 输入校验使用 binding/validation | 🟠 P1 |

### 3.4 测试覆盖（根据改动类型）

| 改动类型 | 最低要求 |
|----------|----------|
| 新功能 | 必须有单元测试 |
| Bug 修复 | 必须有回归测试 |
| 重构 | 保持现有测试通过 |
| 性能优化 | 添加性能基准测试 |

## 4. 审批规则

### 4.1 审批权限矩阵

| 改动类型 | 审查人 | 需要批准 |
|----------|--------|----------|
| 自己代码 | 另一位开发者 | ✅ 必须 |
| 紧急 Bug 修复 | 另一位开发者 | ✅ 必须 |
| 文档/配置 | 可跳过 | ❌ 可选 |
| 已有测试修改 | 另一位开发者 | ✅ 必须 |

### 4.2 审批条件

**通过条件（满足任一即可）：**
- 1 位审查者 approve + CI 全部通过

**必须修改条件（满足任一即需修改）：**
- 审查者提出 P0/P1 级别问题
- CI 检查失败
- 缺少必要的测试

### 4.3 审批时限

| 场景 | 响应时限 | 处理方式 |
|------|----------|----------|
| 普通 PR | 24 小时 | 未响应可催办 |
| 紧急修复 | 4 小时 | 立即通知 |
| 重构/大改动 | 48 小时 | 可拆分审查 |

## 5. 自动化集成

### 5.1 GitHub Actions 工作流

本规范**不复制 workflow 定义**。当前实际运行的 workflow 及其作用见[文档索引](./README.md)的「CI/CD 与发布」一节。

本节原有一份 `# .github/workflows/ci.yml` 示例，但**该文件不存在**，且其描述的检查与仓库实际运行的不一致：示例用 `golangci-lint` 与 `go vet`，实际 CI 跑的是 `gofumpt` 与 `staticcheck`。后端格式、静态检查与测试命令的权威说明在 [`DEVELOPMENT_GUIDE.md`](./DEVELOPMENT_GUIDE.md) §1。

### 5.2 覆盖率门禁

**当前 CI 没有任何测试覆盖率百分比门禁。** 本节原先给出的"service 60% / controller 40% / 新增代码 70% 最低覆盖率"与"下降超过 10% 阻止合并"，是对一套**已被删除的**机制的漂移描述，而不是纯属虚构——所以照抄本节旧文的数字同样是错的，两边都要以当前实际为准。

被删除的机制（2026-07-15 之前）：

| 工作流 | 规则 | 级别 |
|:---|:---|:---|
| `coverage-diff.yml` | 新增/修改行的增量覆盖率 ≥ 60% | `::error::` 阻塞 |
| `ga-gate.yml` G1 | 整体覆盖率 ≥ 1%（v1.0 floor） | `::error::` 阻塞 |
| `ga-gate.yml` G1 | 整体覆盖率 ≥ 70% | 仅 `::warning::` |

`294397a7`（ci: consolidate GitHub Actions workflows）删除了 `coverage-diff.yml`，并把阈值判断从 `backend-ci.yml` 与 `ga-gate.yml` 一并移除。此后 `ga-gate.yml` 不再提及覆盖率；`backend-ci.yml` 只跑测试并上传 `coverage.out`，不设门槛。

实际存在的两道相关检查，管的是不同的事：

- `test-coverage-guard.yml`：**改了受管源码就必须有对应测试文件**，是文件映射检查，不是百分比门槛。
- `acl-gate.yml`：触及 router 文件的 PR 的 ACL 覆盖门禁（要求 100%），与测试覆盖率无关。

覆盖率阶段目标以 [`contributing.md`](./contributing.md) 为准：v1.0 GA 阶段为 ≥1% 防退化 floor（实测 2%），70% 仅作 `::warning::`；v1.1 目标 40%+，v2.0 目标 70%+。

### 5.3 PR Size 检查

```yaml
# 在 CI 中添加
- name: Check PR size
  run: |
    # 计算改动的行数（排除空行和注释）
    CHANGES=$(git diff --stat --stat-width=200 | tail -1)
    echo "Changes: $CHANGES"
    # PR 超过 400 行需要拆分
```

## 6. 常见问题处理

### 6.1 审查者不在场

**解决方案：**
1. 紧急修复可先合并后审查——**但有例外**：涉及 WorkItem、RBAC、tenant/MSP、BPMN、数据库迁移、连接器、AI 工具调用或安全边界的变更，必须由独立审查者或维护者复核，没有"先合并后审"的快捷方式（[`agent-engineering-governance.md`](./agent-engineering-governance.md) §7）
2. 使用 GitHub 的 "Require review from Code Owners" 功能
3. 每日站会同步代码审查状态

### 6.2 代码风格分歧

**解决方案：**
1. 优先遵循现有代码风格
2. 有争议时参考官方 style guide
3. 记录决策到团队规范文档

### 6.3 大改动拆分

**拆分原则：**
1. 按功能模块拆分
2. 按依赖关系排序（先底层后上层）
3. 每个 PR 可独立测试和合并

**拆分示例：**
```
❌ 一次大 PR: "重写用户模块"
  - 修改 User Entity
  - 修改 User Service
  - 修改 User Controller
  - 新增测试

✅ 拆分为:
  PR #1: "User Entity 添加字段" (50行)
  PR #2: "User Service 业务逻辑重构" (150行)
  PR #3: "User Controller API 调整" (80行)
  PR #4: "User 模块测试补全" (120行)
```

## 7. 实施检查表

前四项是"建立流程"的动作，本仓库已完成，但**工具口径与当初设想不同**，已按实际更正：

- [x] PR 模板已存在：`.github/pull_request_template.md`；其字段覆盖面见 §2.2
- [x] GitHub Actions CI 已配置，见[文档索引](./README.md)的「CI/CD 与发布」；后端用 `gofumpt` + `staticcheck`，**不使用 golangci-lint**
- [x] 前端 ESLint + type-check 已在 `frontend-ci.yml` 中运行
- [ ] 覆盖率**百分比**门禁未配置，当前阶段口径见 §5.2

以下为团队习惯项：

- [ ] 团队成员熟悉检查清单
- [ ] 建立每日/每周代码审查习惯

## 8. 附录

### 8.1 Go 静态检查配置

CI **不使用 golangci-lint**，因此没有生效的 golangci-lint 配置。仓库根的 `.golangci-lint.yml` 是既有的未接入文件：它自身的注释要求以 `--config .golangci-lint.yml` 显式指定才生效，而没有任何 workflow 这样做。

生效的 Go 检查是 `gofumpt` 与 `staticcheck`，两者都没有独立配置文件，版本在 workflow 中钉死，见 [`DEVELOPMENT_GUIDE.md`](./DEVELOPMENT_GUIDE.md) §1。

### 8.2 ESLint 配置

详见前端目录 `eslint.config.mjs`

### 8.3 相关文档

- [团队技术提升指导](../team-tech-improvement-guide.md)
- [Go 代码规范](https://go.dev/wiki/CodeReviewComments)
- [Google Go Style Guide](https://google.github.io/styleguide/go/)
