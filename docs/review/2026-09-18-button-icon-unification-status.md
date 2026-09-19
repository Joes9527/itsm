# 按钮图标体系统一：交付状态与交接

- 状态：按钮内图标迁移已完成并部署；**门禁仍有一处未关闭的盲区**（见第 4 节）。
- 日期：2026-09-18（2026-09-19 补记合入 main 与覆盖率守卫豁免）。
- 证据基线：分支 `feat/button-icon-unification`，部署版本 `8f5b3940`（`8f5b39404a782f8cd6b81f845cfedf4caf137c01`），release `itsm-web-8f5b3940-yW7bNJYLZdpJqTQmMLE0Z`。这是历史验证锚点，不是最新 main 或运行版本声明。
- 合入 main：PR #83，merge commit `11604089`。**部署的 `8f5b3940` 早于这次合并**，线上跑的**不是** main 的 tip——所以不要拿 main 的内容反推 3010 的行为，也不要因为 main 前进了就以为线上跟着变了；判断线上状态只能查 release 目录与实际进程。该 PR 只改前端、触不到 `itsm-backend/**`，必需检查从未被创建，是以 `--admin` 合并的：这是仓库治理缺陷而非本分支的问题，已[单独立案](2026-09-19-docs-only-pr-branch-protection-trap.md)，此处不重复。
- 权威工程规则：[共享工程约定](../engineering-conventions.md) 的 Frontend 一节。本文记录背景、边界和未决项，不维护第二份规则。
- 门禁实现与契约：`itsm-frontend/scripts/check-button-icons.mjs` 头部注释（改门禁前必读）。

## 1. 交付范围与部署

按钮内图标从 `lucide-react` 迁到 `@ant-design/icons`，分 9 个提交、3 次发布完成（`35815cf2` → `51678954` → `8f5b3940`）。**非按钮图标（约 1500 处）故意留在 lucide，不属本轮范围**；卡片图标、Dropdown 菜单项、原生 `<button>` 内的图标同理保留，因为它们不经过 antd 的 `resetIcon()`，不存在尺寸冲突。迁移的技术根据写在工程约定里，此处不重复，也不需要重新论证。

## 2. 门禁契约

`npm run icons:check` + `icons:test`（25 例），已接入 frontend CI。四条规则：

1. 按钮里的图标不许来自 lucide-react。
2. 纯图标按钮必须有可访问名称。
3. `icon={…}` 非内联 JSX → **默认违规**，除非有 `icon-gate: <理由>` 标注。
4. `<Button {...spread}>` → 同上。

规则 3/4 不做数据流分析，是 fail-closed 设计。`grep -rn "icon-gate:" src/` 是全部豁免面，共 10 条（8 个可变图标位 + 2 个 spread 包装组件）。**标注记录的是审查义务，不是机器证明**——门禁看不到调用点，每个标注处都要在调用点自行核对图标来源。

## 3. 已验证与未验证的边界

已在本机 3010 上用真实 chromium 逐一查看本轮迁移的图标（此前 118 路由的全站扫描**结构上看不到**它们：`BatchActionBar` 未选中行时 `return null`，详情页是动态路由不在那份清单里）：

| 位置 | 结果 |
|---|---|
| `/cmdb` CSDMHub 10 个 action 按钮 | 渲染正确，尺寸随按钮字号 |
| `/incidents` 批量栏（选中行后）4 个 | 经 `icon={action.icon}` 管道，渲染正确 |
| `/incidents/3` 详情 5 个 | 经 `<Button {...button}>` spread，含禁用态 |
| `/admin/approval-chains` 批量删除 | 选中行后渲染 |
| `/problems/1` 详情 `EditOutlined` | 渲染正确 |

同时对上述页面断言两条不变量，均为零违规：非外壳 antd 按钮内无 lucide svg；带文字按钮内无未隐藏的 antd 图标（可访问名称陷阱）。

**未验证**：WebSocket 实时推送、任何后端路径、firefox/webkit（本机仅装 chromium，那两个是启动失败而非通过）。浏览器检查只覆盖本轮改动的图标，不是重跑全站扫描；未保存截图基线，因此**检测不了未来的视觉回归**。

## 4. 未关闭项：children 位置的图标

门禁的图标判定只读 `icon` **属性**，因此 `<Button><Search size={17} /></Button>` 这种把图标当 children 传的写法它**完全看不见**。

实测：应用页头有 7 个这样的 antd 按钮（search / bot / bell / moon / globe / ellipsis + 个人切换按钮的 shield 与 chevron-down），**每个已登录路由都渲染**；全仓 AST 探针扫出 23 处、13 个文件（页头 6、installations 5、`templates/TemplateList` 2 等）。

**这不构成缺陷，先别按缺陷修**：这 7 个全都有 `aria-label` + `title`，且全都显式定尺寸（14/16/17/18）——呈现属性就是想要的尺寸、实渲染也对得上，正好落在「显式定尺寸的 lucide 从来没错」那一侧。差的**不是观感也不是无障碍，是与迁移决定的一致性和门禁覆盖**。

要收的话，照规则 3/4 的做法做成 fail-closed（新出现即挂，逼作者内联或标注），**不要做成白名单**——白名单会随代码漂移。这是一项需要决策的范围选择，尚未实施。

## 5. 一条需要纠正的旧结论

上一版部署快照（`51678954`）记载：118 路由全站扫描、1110 个图标、ZERO violations，其中含「no lucide svg inside an application button」。页头在**每个**路由上都渲染，所以该断言按字面不成立——要么当时把 "application button" 限定为不含外壳，要么确实漏了。扫描脚本是一次性且已删除，无法回查，因此**该结论应重新限定范围，不宜继续原样引用**。本轮部署快照已就此加注，未沿用旧说法。

## 6. 已拍板、不要重开的事项

- 字形决策：刷新 = `SyncOutlined`、重置 = `ClearOutlined`、重试类 = `SyncOutlined`、回滚/恢复类 = `RollbackOutlined`。`TestRunner.tsx` 的 `CaretRightOutlined` 是手工「运行」语义，不要改。
- 尺寸机制有测试钉住：`src/components/ui/__tests__/button-icon-sizing.test.tsx`。谁要把按钮图标换回 lucide，这条先响。
- `src/components/templates/` 整个目录是死代码，`lib/templates/ui.tsx`、`AuthForm.tsx`、`AuthButton.tsx` 无调用点。
- `src/lib/templates/{ui,list-page}.tsx` 带 `test-coverage-guard: skip` 豁免（本次迁移新增，沿用 backend 已有的 2 处先例）。理由：该目录全仓无调用点（见上一条），本次是机械的图标迁移、没有可测的行为变化，给不可达代码补测试只会制造假覆盖率；**目录去留是另一件事，不在本次范围**。两条不要动：① **不要把这两个文件改回 lucide**——按钮图标门禁会挂；② 验证这个豁免时注意守卫是从**磁盘上的 `REPO_ROOT`** 读文件判断豁免的，在未含该提交的工作树里跑仍是失败，要验就在含豁免的工作树里跑。
- 页级英文文案（约 60 条）、`VirtualizedTicketList` 自带一份英文 `STATUS_CONFIG`/`PRIORITY_CONFIG`/`TYPE_CONFIG`（与 `src/constants/taxonomy.ts` 是同一概念的两个 owner）——已知未收，另议。
