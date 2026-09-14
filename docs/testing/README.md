# 测试文档

这里放当前仍有参考价值的测试方案和测试报告。历史测试报告已经移动到 `docs/archive/testing-reports/`。

## 当前入口

- [SSLVPN Coding Agent 执行说明](./sslvpn-coding-agent-execution-guide.md)及[本轮执行记录模板](./sslvpn-agent-run-record-template.md)：授权、角色交接、断点续跑和副作用记录。
- [SSLVPN UI 全流程测试手册](./sslvpn-manual-lifecycle-runbook.md)：按 OP00–OP22 逐功能执行，每项包含账号、菜单、控件操作、字段数据、保存和结果检查；邮件按 EM01–EM12，原用例作为断言索引，业务验收仅通过 UI。
- [邮件建单与自动回复 UI 手册](./email-ticket-ui-runbook.md)：真实邮箱发信、确认回信、原会话回复、附件、工程师通知与内部备注保密。
- [角色视角产品测试方案](./role-based-product-test-plan.md)
- [测试用例目录](./test-cases/README.md)

## 维护规则

- 可重复执行的测试方案放在本目录。
- 一次性测试报告保留日期，长期无效后移动到 `docs/archive/testing-reports/`。
- 自动化脚本放在 `docs/scripts/` 或 `tests/`，不要混在测试报告里。

- [SSLVPN UI 账号与菜单清单](sslvpn-ui-accounts-and-menus.md)：历史测试账号、角色/审批组、页面入口及当前环境确认步骤。
