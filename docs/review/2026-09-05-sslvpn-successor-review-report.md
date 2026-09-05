# SSLVPN 接手期间增量审查

**结论：需要修正，已继续实施。** 审查以原暂停源码快照为基线，对比重新接手时的未提交工作区：21 个文件、约 3,052 行差异；HEAD 为 `695c75fe`。不是对整个分支或完整 A3b/A4 的批准。

## 已确认的有效工作

- 原 Ticket/Incident 五类失败修正方向正确：规则回执查询使用真实 `rule` 类型；fixture 满足实际 HTTP title/description 契约；跨租户 requester 由合法 actor 提交，真实进入 Intake 拒绝路径。
- 中间件依赖守卫仍约束生产代码，同时允许外部测试调用真实 HTTP 边界；精确数字断言适配保留实际持久化行为。
- Change 测试已迁移到真实 Intake，保留生命周期与审批断言；重复关联编号按去重后的数量校验，仍拒绝缺失/跨租户引用。
- Ticket repository 删除了生产 CreateParams；读取/更新测试采用直接 Ent 准备数据，没有恢复旧生产创建 API。
- SSLVPN 与 Catalog 字段 fixture 已适配确认版本、统一创建回执和持久化流程启动，但尚无本轮真实外部授权验收。

## 阻塞项

| 编号 | 发现 | 要求与状态 |
| --- | --- | --- |
| I1 | `handlers/change/change_regression_test.go` 的必填字段测试在无效请求返回 201 时仍执行 `t.Skip` | 删除跳过，按专业 owner 的明确必填规则拒绝无效创建，并验证无残留图；修复中 |
| I2 | 删除 `repository/ticket/repository_integration_test.go` 后，缺少实际 Intake 遭遇 PostgreSQL 唯一约束失败时的编号回滚/复用测试 | 在独立 PG schema 中制造真实插入冲突，验证回滚及下一次成功复用首号；修复中 |

I1 是交接时已要求关闭、但此次迁移仍保留的缺口；I2 是此次删除测试导致的覆盖损失。现有 allocator 手工事务回滚测试、SQLite hook 测试和 PG 成功并发测试不能替代 I2。

另有两项 Minor：接手 Agent 的 ledger 对 Change/repository 等文件“未修改、仍编译失败”的描述已过时，已追加当前事实；部分测试把 `repo.Get` 描述为受权 HTTP GET，需更正文案或补真实接口证据。

## 本轮实际验证

工作目录为 `itsm-backend`，各命令退出码独立取得：

| 命令 | 结果与限制 |
| --- | --- |
| `go test ./handlers/change ./repository/ticket -count=1` | 退出 0；两包通过，分别 1.672 秒和 0.666 秒。不能由此认为 I1 的跳过子例已执行拒绝断言 |
| `go test ./... -run '^$'` | 退出 0；所有包编译/链接通过，不执行业务测试 |

独立审查只读代码和记录，未重复运行上述测试。修复实现与后续复审分开执行；修复后的测试证据将在实际取得后追加。

## 仍然开放的后续门槛

当前复用和迁移有进展，但显式 workflow 覆盖权限、actor 审计测试替换、真实 bootstrap/Worker 装配、A1 其余字段处置、共享字段/schema 026+、前端、身份交换、KAF 和真实 Graph 验收仍未完成。完整后端任务审查范围继续保持 `94cc5787..最终HEAD`。

详细增量 diff、审查与修复记录保存在现有 worktree 的 `.superpowers/sdd/2026-09-05-sslvpn-end-to-end-implementation/resume-review/`；暂停快照保持原样。总进度见[验证报告](2026-09-05-sslvpn-end-to-end-verification-report.md)。
