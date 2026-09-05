# SSLVPN 接手期间增量审查

**结论：两项阻塞问题修复后，针对性复审已通过；继续实施剩余任务。** 审查以原暂停源码快照为基线，对比重新接手时的未提交工作区：21 个文件、约 3,052 行差异；HEAD 为 `695c75fe`。不是对整个分支或完整 A3b/A4 的批准。

恢复后的后端源码已保存为中间检查点 `229d8091`（84 个文件，旧创建路径退役与测试迁移），提交前最新的全部包编译检查退出 0。该提交包含暂停前的工作、接手增量和上述修复；仍需完成剩余门槛后接受完整 `94cc5787..最终HEAD` 审查。

## 已确认的有效工作

- 原 Ticket/Incident 五类失败修正方向正确：规则回执查询使用真实 `rule` 类型；fixture 满足实际 HTTP title/description 契约；跨租户 requester 由合法 actor 提交，真实进入 Intake 拒绝路径。
- 中间件依赖守卫仍约束生产代码，同时允许外部测试调用真实 HTTP 边界；精确数字断言适配保留实际持久化行为。
- Change 测试已迁移到真实 Intake，保留生命周期与审批断言；重复关联编号按去重后的数量校验，仍拒绝缺失/跨租户引用。
- Ticket repository 删除了生产 CreateParams；读取/更新测试采用直接 Ent 准备数据，没有恢复旧生产创建 API。
- SSLVPN 与 Catalog 字段 fixture 已适配确认版本、统一创建回执和持久化流程启动，但尚无本轮真实外部授权验收。

## 原阻塞项及修复结果

| 编号 | 发现 | 要求与状态 |
| --- | --- | --- |
| I1 | `handlers/change/change_regression_test.go` 的必填字段测试在无效请求返回 201 时仍执行 `t.Skip` | 已删除跳过，专业 Prepare 在模板展开后校验；十个空/空白用例拒绝且无残留图，复审通过 |
| I2 | 删除 `repository/ticket/repository_integration_test.go` 后，缺少实际 Intake 遭遇 PostgreSQL 唯一约束失败时的编号回滚/复用测试 | 已恢复真实 Intake PG 用例，SQLSTATE 23505 后回滚完整图并复用首号，复审通过 |

I1 是交接时已要求关闭、但此次迁移仍保留的缺口；I2 是此次删除测试导致的覆盖损失。现有 allocator 手工事务回滚测试、SQLite hook 测试和 PG 成功并发测试不能替代 I2。

另有两项 Minor：接手 Agent 的 ledger 对 Change/repository 等文件“未修改、仍编译失败”的描述已过时，已追加当前事实；部分测试把 `repo.Get` 描述为受权 HTTP GET，需更正文案或补真实接口证据。

## 本轮实际验证

工作目录为 `itsm-backend`，各命令退出码独立取得：

| 命令 | 结果与限制 |
| --- | --- |
| `go test ./handlers/change ./repository/ticket -count=1` | 退出 0；两包通过，分别 1.672 秒和 0.666 秒。不能由此认为 I1 的跳过子例已执行拒绝断言 |
| `go test ./... -run '^$'` | 退出 0；所有包编译/链接通过，不执行业务测试 |

独立审查只读代码和记录，未重复运行上述测试。修复实现与独立复审分开执行。修复后的完整 Change/Intake 包、十个必填字段用例、`^TestIntake` 集成用例及真实 PG 约束失败/回滚/精确编号复用测试均退出 0；PG 独立 schema 清理已验证为零残留。复审确认 I1/I2 和详情读取证据文案问题已解决，无新增阻塞问题。

更广测试发现的七个 HTTP fixture 401 已修复：四个 Standard Change 实例化和三个 Dynamic Fields 集成用例均接入真实租户/用户/Intake/幂等键与统一回执；Standard Change 与普通 integration 两个完整包通过。独立审查要求补强模板字段的精确名称、标签、值断言，该补充也已测试并复审通过。该轮仅修改两份测试文件，不修改生产逻辑。新增 PG 测试限定本机专属 DSN 的可移植性问题作为 Minor 转入 A7，当前不会扩大数据库执行范围。

## 后续身份与权限检查点

提交 `1802a000` 使用现有权限服务，在回执查询及重放前校验所有显式流程覆盖的 `workflow:write`，包括 BPMN 运行变量；正常目录配置不要求申请人具备流程管理权限。真实 Intake → Outbox → 流程引擎测试恢复了 actor/requester 分离、规范业务身份、缺失身份拒绝及启动重放覆盖；Incident 自定义优先级矩阵变化不会重算已创建请求。相关旧 BPMN 创建 fake 测试也已迁移，实际专业持久化失败验证完整回滚及同来源重试。

八个完整后端包通过；所选真实集成测试 29 个顶层用例、84 条通过事件，无跳过。独立审查无 Critical/Important。完整 controller 包仍有两处旧 Incident→Problem 测试失败：缺少幂等键且仍断言旧 fake/详情响应。MSP 用例还暴露原操作者租户与目标客户租户被强制校验为相同的冲突，将依照既有 MSP 关系、专业权限和双层审计约束修复，不能改用客户本地用户冒充 MSP 验证。另一个已有角色门控测试缺少 tenant 上下文，在 `tenantID.(int)` 处被 Gin 恢复；不计为成功删除流程绑定的证据。

## 仍然开放的后续门槛

后续提交 `1802a000` 已完成显式 workflow 覆盖权限、真实创建到持久化启动的 actor 审计测试、Incident 配置矩阵及重放验证，并通过独立审查。真实 bootstrap/Worker 装配、MSP 创建身份与转换入口、A1 其余字段处置、共享字段/schema 026+、前端、身份交换、KAF 和真实 Graph 验收仍未完成。完整后端任务审查范围继续保持 `94cc5787..最终HEAD`。

详细增量 diff、审查与修复记录保存在现有 worktree 的 `.superpowers/sdd/2026-09-05-sslvpn-end-to-end-implementation/resume-review/`；暂停快照保持原样。总进度见[验证报告](2026-09-05-sslvpn-end-to-end-verification-report.md)。
