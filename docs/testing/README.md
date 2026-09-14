# 测试文档

这里放当前仍有参考价值的测试方案和测试报告。历史测试报告已经移动到 `docs/archive/testing-reports/`。

## 当前入口

- [SSLVPN 手工全流程测试手册](./sslvpn-manual-lifecycle-runbook.md)：先完成 WSL/ITSM/KAF/Worker 部署核对，再按角色测试自定义字段、BPMN 与 Ticket 生命周期；包含异常分支和执行记录模板。
- [角色视角产品测试方案](./role-based-product-test-plan.md)
- [测试用例目录](./test-cases/README.md)

## 维护规则

- 可重复执行的测试方案放在本目录。
- 一次性测试报告保留日期，长期无效后移动到 `docs/archive/testing-reports/`。
- 自动化脚本放在 `docs/scripts/` 或 `tests/`，不要混在测试报告里。
