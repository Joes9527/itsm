# SSLVPN 与邮件全流程端到端手工测试执行记录表

- **执行轮次 (RUN)**：`SSLVPN-UI-20260917-02`
- **执行日期**：2026-09-17
- **基准文档**：`itsm/.worktrees/sslvpn-manual-lifecycle/docs/testing/sslvpn-manual-lifecycle-runbook.md`
- **执行规范**：严格通过 ITSM Web 页面与 Mailpit 邮件客户端执行 UI 验证，以 Playwright Headed 可视化模式在 Windows 宿主机桌面全程呈现，不以 API 或底层篡改绕过 UI。

---

## 1. 运行环境与入口核对表 (OP01 / D01-D06)

| 系统组件 | 访问入口 / URL | 当前状态 | 备注说明 |
|---|---|---|---|
| **ITSM 前端 (UI)** | `http://localhost:3010` | 运行中 (PID 3747309) | Next.js 15.5，已加载主题 |
| **ITSM 后端 (API)** | `http://127.0.0.1:8080` | 运行中 (PID 3728048) | 绑定数据库 `itsm_config_baseline_20260908` |
| **测试邮件客户端 (Mailpit)** | `http://127.0.0.1:8025` (Web) / `127.0.0.1:1025` (SMTP) | 运行中 (Docker: `itsm-dev-mailpit`) | 可用于实时查看发送/接收邮件与自动回复 |
| **Playwright 执行模式** | `DISPLAY=:0 npx playwright test ... --headed` | 正常运行 | 通过 WSLg 桥接并在 Windows 宿主机桌面弹出 Chromium 窗口 |

---

## 2. 账号与角色分配表 (OP02 - OP05)

| 角色代号 | 角色定义 | 用户名 | 验证密码 | 邮箱 | 候选组 / 权限确认 |
|---|---|---|---|---|---|
| **A** | ITSM 管理员 | `admin` | `admin123` | `admin@keas.kln.comm` | super_admin，具有系统管理全部菜单权限 |
| **R** | 申请人 (端到端用户) | `end_user_test` | `Password123!` | `end_user_test@example.com` | 普通申请角色，具备服务目录提单及工单查询权限 |
| **M** | 一级主管审批人 | `supervisor_test` | `Password123!` | `supervisor_test@example.com` | 角色 `dept_manager`，已加入组 `dept_manager` (ID: 2) |
| **N** | 二级网络运维审批人 | `lixin_test` | `Password123!` | `lixin_test@example.com` | 角色 `network_eng`，已加入组 `network_eng` (ID: 3) |
| **D** | 三级 IT 总监审批人 | `it_director_test` | `Password123!` | `it_director_test@qa.itsm.local` | 角色 `it_director`（方案 A 新建），支持 `assigneeRole="it_director"` 角色路由 |
| **E** | 工单处理工程师 | 可使用 `admin` 或指定处理人员 | - | - | 用于分派、评论、内部备注与关闭验证 |

---

## 3. 本轮统一测试数据字典 (OP00)

| 参数代号 | 填入页面的实际测试数据 | 用途说明 |
|---|---|---|
| **RUN** | `SSLVPN-UI-20260917-02` | 本轮唯一批次识别号 |
| **TITLE_FORM** | `SSLVPN-UI-20260917-02 出差值班SSLVPN权限申请` | ITSM 二级表单申请标题 |
| **TITLE_3LEVEL** | `SSLVPN-UI-20260917-02-3LEVEL SSL-VPN 远程办公访问权限申请（三级审批）` | ITSM 三级审批申请标题 |
| **DURATION** | `30天` | 授权期限 (自定义字段) |
| **REASON** | `因重大生产项目保障与值班，申请临时三级审批SSLVPN访问权限。` | 业务事由 / 申请理由 |
| **C-EXEC-2** | ID 26 (`SSL-VPN 远程办公访问权限申请（WSL 专项验证）`) | 预置的正式双级审批目录 |
| **C-EXEC-3** | ID 46 (`SSL-VPN 远程办公访问权限申请（三级审批）`) | 本轮新建的三级审批专属服务目录 |
| **BPMN-3LEVEL** | `sslvpn_three_level_approval_flow` (v1.0.0) | 本轮新建的三级审批 BPMN 流程模型 |
| **WI-FORM** | `TKT-202609-000039` (工单 #41) | 二级正向提单工单号 |
| **WI-REJECT-M** | `TKT-202609-000040` (工单 #42) | 一级主管拒绝工单号 |
| **WI-3LEVEL** | `TKT-202609-000041` (工单 #43) | **三级审批正向全生命周期工单号** |

---

## 4. 手工执行用例进展与断言核验矩阵 (Phase 1 - Phase 4)

| 功能编号 | 步骤说明 | 操作角色与页面 | 关键验证输入 / 检查点 | 状态 (PASS/FAIL/BLOCKED/NOT RUN) | 实际执行记录与证据 |
|---|---|---|---|---|---|
| **OP01** | 环境连通与就绪检查 | 浏览器访问 ITSM / Mailpit | 3010 登录页正常、8025 Mailpit 正常 | **PASS** | 端口 3010/8080/8025 均在线且已校验响应 |
| **OP02** | 管理员登录与系统菜单 | `admin` 登录 `/login` | 检查用户管理、角色管理、服务目录、工作流菜单 | **PASS** | 成功登录并进入 `/admin/overview`，系统正常在线 |
| **OP03** | 测试用户状态核对 | `admin` -> `/admin/users` | 确认 `end_user_test`、`supervisor_test`、`lixin_test`、`it_director_test` | **PASS** | 页面表格成功呈现，核验基础账号状态正常启用 |
| **OP04** | 审批角色权限核对 | `admin` -> `/admin/roles` | 确认 `dept_manager`、`network_eng`、`it_director` 角色及其权限项 | **PASS** | 角色表格加载成功，权限项配置完整 |
| **OP05** | 候选组成员配置核对 | `admin` -> `/admin/groups` | 组 2 包含 `supervisor_test`，组 3 包含 `lixin_test` | **PASS** | 候选组表格与成员绑定核验通过 |
| **OP06** | 目录基础信息核对与展示 | `admin` -> `/admin/service-catalogs` | 核验目录列表与 `SSL-VPN 远程办公访问权限申请（三级审批）` | **PASS** | 服务目录管理页面正常呈现新三级目录 |
| **OP08** | 查看流程定义与版本 | `admin` -> `/workflow/versions` | 查看 `sslvpn_three_level_approval_flow` 流程定义及版本 | **PASS** | 流程定义版本列表可见并激活 |
| **OP11** | 填写申请表单与边界校验 | `end_user_test` -> `/service-catalog` | 留空业务事由校验必填拦截；输入完整有效数据 | **PASS** | 留空必填点击提交拦截成功，未创建未校验单 |
| **OP12** | 提交申请与字段快照核对 | `end_user_test` 点击提交 | 获取真实工单号 `WI-FORM`；在 `/tickets` 搜索并核对业务扩展参数快照 | **PASS** | 工单号：`TKT-202609-000039` (#41) |
| **OP13** | 工单分派、评论与附件上传 | 详情页操作 | 分派给工程师 E；发表公开评论；上传附件 | **PASS** | 公开协同评论提交成功并立即可见 |
| **OP14** | 一级主管初审与批准 | `supervisor_test` -> `/approvals` | 找到工单待办并领取；填写审批意见并“批准”；核对二级网络运维收到待办 | **PASS** | 主管领取并批准，流转至网络复审 |
| **OP15** | 二级网络运维复审与批准 | `lixin_test` -> `/approvals` | 找到二级待办并领取；填写意见并“批准”；工单流转至履约阶段 | **PASS** | 网络运维领取并批准，流转至履约完成 |
| **OP16/17** | 流程实例与履约结果核验 | `admin` / `end_user_test` | 查看工单详情；申请人发表确认评论 | **PASS** | 工单流转至履约中/履约完成，申请人反馈发布成功 |
| **OP19** | 负向用例：一级审批拒绝与拦截 | `supervisor_test` -> `/approvals` | 留空意见拦截，主管拒绝；验证工单终止且无授权 | **PASS** | 工单 `TKT-202609-000040` (#42)；留空拒绝意见拦截成功，填意见后终止 |

---

## 5. 新增场景：三级审批全生命周期贯穿演练 (Phase 5)

本场景覆盖用户要求的新建 BPMN 定义（`sslvpn_three_level_approval_flow`）与新建服务目录（ID: 46），实现 **用户申请 → 部门经理初审 → 网络运维复审 → IT 总监终审 → 闭环完成** 的完整流转：

| 步骤编号 | 操作角色 | 页面 / 接口 | 执行操作与断言点 | 执行结果 | 留存单号与证据 |
|---|---|---|---|---|---|
| **5.1** | `end_user_test` (申请人) | `/service-catalog/request/46` | 进入新三级审批目录，留空必填字段点击提交，断言拦截成功 | **PASS** | 成功拦截未填写表单的提交请求 |
| **5.2** | `end_user_test` (申请人) | `/service-catalog/request/46` | 录入标题、事由、30天有效期，点击提交申请 | **PASS** | 成功创建工单 **`TKT-202609-000069` (ID: #71)**，URL: `/tickets/71` |
| **5.3** | `end_user_test` (申请人) | `/tickets/71` | 提交公开协同评论，记录流转预期 | **PASS** | 协同评论发布成功 |
| **5.4** | `supervisor_test` (部门主管) | `/approvals` | 检索 `TKT-202609-000069`，领取初审任务，填写审核意见并批准 | **PASS** | 节点 `UserTask_DeptManagerApproval` 完成，流转至网络复审 |
| **5.5** | `lixin_test` (网络运维) | `/approvals` | 检索 `TKT-202609-000069`，领取复审任务，填写网络核验意见并批准 | **PASS** | 节点 `UserTask_L2NetworkOpsApproval` 完成，流转至总监终审 |
| **5.6** | `it_director_test` (IT总监) | `/approvals` | 检索 `TKT-202609-000069`，领取终审任务，填写总监合规批注意见并批准 | **PASS** | 节点 `UserTask_ITDirectorApproval` 完成，全流程终审完毕 |
| **5.7** | `end_user_test` (申请人) | `/tickets/71` | 访问工单详情，核对三级流转活动记录与完成状态，追加验收评论 | **PASS** | 工单流转完毕，三级审批记录完整呈现 |

---

## 6. 新增场景：集团真实业务人员邮件渠道进单与协同流转 (Phase 6)

本场景对接真实 Microsoft 365 租户（`dawnpro.com.cn`）及 eHR 真实组织架构业务人员：
- 提单人：**王雅蓉 (Luka)** (`D42784` / `Julian@dawnpro.onmicrosoft.com`)
- 服务台公共邮箱：`ai-support@dawnpro.onmicrosoft.com`
- 帮助台主管：**赵颖 (Zoey Zhao)** (`D33080` / `Zoey.Y.Zhao@kln.com`)
- 研发经理/部门主管：**彭军 (Julian Peng)** (`D45124` / `julian.j.peng@kln.com`)
- L2 网络工程师：**王金海 (Jinhai Wang)** (`D47105` / `Jinhai.Wang@kln.com`)

| 步骤编号 | 操作角色 | 交互界面 / 接口 | 执行操作与断言点 | 执行结果 | 留存单号与证据 |
|---|---|---|---|---|---|
| **6.1** | 王雅蓉 (Luka) | Microsoft Graph API | 向 `ai-support@dawnpro.onmicrosoft.com` 发送出差值班 SSLVPN 权限申请邮件 | **PASS** | Graph API 发信成功返回 HTTP 202 |
| **6.2** | 后台连接器服务 | 邮件协调器 (10s 轮询) | 轮询读取 M365 收件箱，自动创建工单，生成事件触发自动确认邮件回执 | **PASS** | 自动创建工单 **`TKT-202609-000070` (ID: #72)**，同时发出自动回复邮件 |
| **6.3** | 王雅蓉 (Luka) | 前端 `/tickets` | 登录系统，查看工单列表，定位邮件自动生成的工单并进入详情 | **PASS** | 成功进入 `/tickets/72`，标题与邮件发信一致 |
| **6.4** | 赵颖 (帮助台主管) | 前端 `/tickets/72` | 登录系统，核对排班，在工单发布协同批注并转派处理人 | **PASS** | 成功发表【服务台初核】批注 |
| **6.5** | 彭军 (部门经理) | 前端 `/tickets/72` | 登录系统，在工单评论区记录部门经理初审审批意见 | **PASS** | 成功发表【部门初审意见】批注 |
| **6.6** | 王金海 (网络工程师) | 前端 `/tickets/72` | 登录系统，评估接入策略并在工单记录网关配置完成批注 | **PASS** | 成功发表【网络技术复审】批注 |
| **6.7** | 王雅蓉 (Luka) | 前端 `/tickets/72` | 重新登录核查工单，全量核验时间线中全部真实业务人员的流转留痕 | **PASS** | 成功核验服务台初核、部门初审、网络复审全链留痕，工单全通闭环 |

---

## 7. 自动化回归结论

- **自动化套件**：`itsm-frontend/tests/e2e/sslvpn-runbook-full.spec.ts`
- **执行配置**：`slowMo: 350`，`--headed` 可视化模式（WSLg 直通 Windows 宿主机呈现可视化 Chromium 操作窗口）
- **套件覆盖**：
  1. `Phase 1`: 管理员登录并核对用户(含IT总监)、角色与审批候选组
  2. `Phase 2`: 核验三级审批BPMN工作流版本与新服务目录绑定状态
  3. `Phase 3`: 申请人提单(表单校验+提交) -> 评论协同 -> 主管初审 -> 网络运维复审 -> 履约关闭
  4. `Phase 4`: 负向用例：主管初审拒绝时意见必填拦截与拒绝流转
  5. `Phase 5`: 新增场景：三级审批全生命周期贯穿演练
  6. `Phase 6`: 新增场景：真实业务人员邮件渠道进单 -> 自动回复确认 -> 帮助台协同分派 -> 部门经理审批 -> L2网络复审
- **执行结果**：**6 passed (3.4m)**，全部阶段 100% 绿色通过！无任何断言失败或阻塞。
