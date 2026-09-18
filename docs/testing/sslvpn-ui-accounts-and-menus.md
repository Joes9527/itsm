# SSLVPN UI 测试账号、菜单与环境清单

状态：2026-09-14 文档/源码核对；账号来自 SSLVPN 历史场景及测试 fixture，**未登录当前 WSL 验证，不表示已启用或密码有效**。配合[全流程主手册](sslvpn-manual-lifecycle-runbook.md)使用，业务测试仅通过 UI。

## 1. 测试账号与角色

| 角色 | SSLVPN 场景参考账号 | 必要身份与用途 | 当前 WSL 准备动作 |
|---|---|---|---|
| R 申请人 | `end_user_test` | 本租户 active 普通用户；申请目录、查看自己的工单；KAF workspace 普通 member | A 在用户管理搜索账号，核对租户、状态、实际邮箱；正常登录确认可申请和查看 |
| M 一级主管 | `supervisor_test` | 角色 code `dept_manager`，同时属于候选组 `dept_manager` | 在角色管理和组管理分别核对；与 R 使用不同身份，一级待办可领取 |
| N 二级网络审批人 | `lixin_test`（历史显示名“李昕/L2网络运维”） | 角色 code `network_eng`，同时属于候选组 `network_eng` | 一级通过后才出现二级待办；该显示名不是要求使用某位真实员工身份 |
| D 三级 IT 总监审批人 | `it_director_test` | 角色 code `it_director`，支持 `assigneeRole="it_director"` 角色路由 | 二级网络审批通过后出现终审待办，支持在待办审批中心领取并终审批准 |
| A ITSM 管理员 | 环境负责人指定的现有测试管理员，历史场景未提供本轮可靠登录名 | 用户/角色/组、目录字段、流程、审批链和连接器管理 | 登记真实账号安全引用，不默认使用 admin 或其他文档的默认密码 |
| E 工单处理工程师 | 环境负责人指定；如需新建可采用 `sslvpn_engineer_test`（建议名，未创建） | 本轮对象读取、分派、评论、附件、通知及正式生命周期动作 | 与审批身份分开留证；由角色页面按实际能力配置，不一律授管理员 |
| KAF 管理员 | 已获准的 Microsoft 登录邮箱 | workspace 和 KAF 管理；申请人不能借此身份测试 | 使用正常 Microsoft 登录，在可见个人资料/workspace 中确认身份；隐藏配置由 O 交接 |
| O 运行/恢复负责人 | 环境负责人指定 | WSL/ITSM/KAF/Worker 准备，外部权限基线及恢复 | 登记负责人、管理门户和交接记录；不作为申请人/审批人替身 |
| KAF 自动化主体 | `kaf_automation` 是所需独立技术身份；实际登录名由 O 确认 | 后台受限执行，属于目标租户 | 仅部署前置；不用于浏览器提单或审批，不在测试文档填写 token |

### 1.1 集团-分公司真实业务人员清单 (来自 eHR "公司架构" 根节点真实组织树)

| 业务角色 | 真实姓名 | 工号 | 归属部门 / 职位 | 账号/登录名 | 邮箱 (M365/实测) | 角色权限 / 审批权限 |
|---|---|---|---|---|---|---|
| **端到端提单人 (End User)** | 王雅蓉 (Luka) | `D42784` | 南宁嘉顺达物流有限公司 / IT帮助台专员 (直属主管: 赵颖) | `D42784` | `Julian@dawnpro.onmicrosoft.com` | `end_user`，通过 M365 向服务台发送邮件或自服务发起申请 |
| **IT帮助台主管 (Helpdesk)** | 赵颖 (Zoey Zhao) | `D33080` | 嘉里大通物流有限公司 / IT帮助台主管 | `D33080` | `Zoey.Y.Zhao@kln.com` | `sd_manager`，接收邮件工单、初核排班与协同转派 |
| **IT服务台主管 / 研发经理** | 彭军 (Julian Peng) | `D45124` | 嘉里物流联网有限公司 / 高级国际货代研发经理 | `D45124` | `julian.j.peng@kln.com` | 审批候选组 `dept_manager`，部门主管初审/审批协同 |
| **L2 网络工程师** | 王金海 (Jinhai Wang) | `D47105` | 嘉里物流联网有限公司 / 助理国际货代研发经理 | `D47105` | `Jinhai.Wang@kln.com` | 角色 `network_eng`，网络技术复审与网关访问权限配置 |
| **统一服务台支持邮箱** | Service Desk | - | 集团 IT 共享支持邮箱 | - | `ai-support@dawnpro.onmicrosoft.com` | Microsoft Graph API 轮询接入、自动建单与自动邮件回执 |

历史设计曾建议 `dept_manager_test`、`network_eng_test`，而后续计划、报告和 fixture 使用 `supervisor_test`、`lixin_test`。本表以后者作为查找候选；不能同时创建两套同义账号，也不能假定设计中名字已部署。旧 C4 临时 actors 和用户 ID 不能当成本轮登录资料。

**密码与邮箱：**不将 fixture 默认密码视为当前凭据。由负责人提供安全凭据引用；需要重置时仅在本轮获准测试账号范围内通过用户管理 UI 操作。fixture 的 `example.com` 邮箱只是测试数据，不具备真实邮件收发证明。邮件 M 用例必须登记可登录的真实测试邮箱，且发件地址映射到 R 的本租户有效用户。

最低能力参考：R 需要服务目录读取、服务请求创建/读取和本人 WorkItem 读取；M/N 需要流程读取/执行、工单/服务请求/目录读取及审批页面所需用户读取。历史部署参考键为 `service_catalog:read`、`service_request:create/read`、`workflow:read/write`、`ticket:read`、`user:read`。这些不是可以直接粘贴的新角色定义；当前权限列表、菜单权限和对象权限必须一起核对，尤其“有同名角色”不等于“在审批候选组内”。

## 2. 菜单与页面速查

当前侧栏从服务端动态菜单加载。下表的菜单名称参考本地菜单配置及页面，**运行环境的菜单层级/名称需登录核对**；本地 `menu-config.ts` 不能证明运行菜单已发布。先从菜单进入并记录点击路径；菜单缺失时记录导航缺口，允许在已登录浏览器地址栏打开已知页面独立测试页面能力，但不能因此把菜单用例补记通过。

所有路径追加到已确认的 ITSM 前端地址；不是 API 路径。

| 测试用途 | 菜单名称/页面定位参考 | 页面路径 | 使用角色与重点 |
|---|---|---|---|
| 登录 | 登录页 | `/login` | R/M/N/E/A 各自正常登录，确认当前账号 |
| 用户准备 | 系统管理 → 用户管理 | `/admin/users` | A 搜索三个历史账号，核对有效状态和实际邮箱 |
| 角色配置 | 系统管理 → 角色管理 | `/admin/roles` | A 核对角色及操作权限 |
| 候选组成员 | 系统管理 → 组管理 | `/admin/groups` | A 核对 dept_manager/network_eng 成员，保留其他现有成员 |
| 目录/自定义表单 | 系统管理 → 服务目录 | `/admin/service-catalogs` | A 配置五类字段、审批开关和可见绑定；accessPolicy 缺 UI 按主手册记录 |
| 目录浏览/申请 | 服务目录 | `/service-catalog` → `/service-catalog/request/{catalogId}` | R 搜索本轮 SSLVPN 目录并打开申请，ID 从页面取得 |
| 工单查找与详情 | 工单列表/统一工作项入口（以实际菜单为准） | `/tickets` → `/tickets/{workItemId}` | R/E/M/N 搜索本轮编号；查看业务扩展参数、审批链、评论、附件、通知和历史 |
| 服务请求列表 | 服务请求 → 服务请求列表 | `/service-requests` | R/E 点击原申请进入统一工单详情 |
| 人工审批 | 审批入口（以实际侧栏名称为准） | `/approvals` | M/N/D 定位初审、网络复审及总监终审，领取后批准/拒绝 |
| 审批链配置 | 系统管理 → 审批链 | `/admin/approval-chains` | A 核对配置；预解析审批链不能当作实际 BPMN 决策 |
| 流程管理 | 系统管理 → 工作流 | `/admin/workflows` | A 查看目录所用流程与管理能力 |
| 流程设计 | 工作流 → 流程设计器 | `/workflow/designer`；已有票据审批设计页 `/workflow/ticket-approval` | A 查看设计；这两个入口不是人工审批待办 |
| 流程版本 | 工作流 → 版本管理 | `/workflow/versions` | A 查看/激活已准备版本；不假设提供完整发布入口 |
| 实例进度 | 工作流 → 流程实例 | `/workflow/instances` | A 核对对应实例、节点和状态，授权范围内测试终止 |
| 邮件连接器 | 系统管理 → 连接器/插件市场 | `/admin/connectors` | A 在“市场”找到“邮件（Microsoft Graph）”，在“已配置”核对状态 |
| 工单发送通知 | 原工单详情 → 通知 → 发送通知 | `/tickets/{workItemId}` 内部页签 | E 选择本轮接收人和实际可用通知类型，R 在真实邮箱核对送达 |
| 邮件建单/回信 | 测试邮箱的已发送、收件箱、原邮件“回复” | 负责人提供的 Outlook/其他邮箱客户端入口 | R 执行 M01–M12；不能使用 fixture 邮箱代替 |
| KAF 发起/结果 | KAF 正常登录 → 目标 workspace → 对话 | 已确认 KAF 前端地址，通过 UI 选择 workspace | R 普通成员发起；历史 slug 为 `it-support`，实际名称/成员由负责人确认 |

本地菜单配置中“服务目录”的 path 指向 `/tickets/create`，与专用目录页 `/service-catalog` 不同。测试时记录实际落点；如果不能到达目标目录，登记导航问题，再独立打开专用目录页验证。不要据此改后端权限或使用旧文档的 `/services`、`/tasks/todo`。

## 3. WSL 入口与测试对象

| 项目 | 已有文档参考 | 本轮必须确认 |
|---|---|---|
| WSL 宿主 | Windows `192.168.31.66` 内的 WSL | 浏览器所在机器、实际可访问域名/端口及转发方式 |
| ITSM 前端 | 历史 Dev 端口 `3001`，登录 `/login` | 填完整可访问 URL；`localhost` 指浏览器所在机器，不能直接当 WSL 地址 |
| KAF 前端 | 历史 Dev 端口 `5173` | 填正常登录入口及实际 workspace；Microsoft 回调采用部署方已配置地址 |
| SSLVPN 目录 | 历史名称 `SSL-VPN 远程办公访问权限申请` | 当前专用目录名称、页面 ID、已发布状态、字段版本；不硬编码历史目录编号 |
| 流程 | `sslvpn_approval_flow` | 本轮激活版本、目录绑定和两级候选组 |
| Graph 授权对象 | 部署负责人保管的固定 Dev 用户/组 | 通过已获准管理门户核对身份与基线；不从登录名/邮箱猜 Object ID |
| 测试支持邮箱 | 当前没有已确认的实际地址 | 填真实收件邮箱、R 发件邮箱、允许收件人范围和观察期限 |

完整部署依据见[WSL 部署说明第 0 节](../deployment/sslvpn-wsl-deployment-and-manual-verification.md#wsl-dev-entry)和[开发环境](../development-environment.md)。本清单不启动部署、不创建账号、不发送邮件。

## 4. 开测前按顺序核对

1. A 在用户、角色和组页面核对 R/M/N；把有效登录名、邮箱与安全凭据来源填写到[本轮记录](sslvpn-agent-run-record-template.md)。缺账号才在已授权范围内通过 UI 创建，不执行 fixture/seed 来覆盖共享用户。
2. R/M/N/E 使用独立浏览器配置文件分别登录，截图账号和实际菜单。按本表逐个验证所需入口；缺菜单和页面拒绝分别记录。
3. R 打开 SSLVPN 目录，核对实际字段；M/N 先确认审批页面可访问，新申请提交后再核对准确节点及角色顺序。
4. R 在 KAF 正常登录，核对 workspace；邮件路线在真实客户端核对发件/收件邮箱。用户名正确不代表跨系统映射或收发能力已具备。
5. 保存账号→角色→组→菜单→本轮用例的对应记录，再进入主手册。未确认账号/映射的依赖场景 BLOCKED；其他独立 UI 用例可以继续。

## 5. 核对来源

- [历史执行计划](../superpowers/plans/2026-08-24-sslvpn-approval-e2e-verification.md)与[历史 UI 报告](../archive/testing-reports/2026-08-24-sslvpn-approval-e2e-verification-report.md)：具体账号和历史目录名称；不继承其中旧状态/字段数或 PASS。
- [测试 fixture](../../itsm-backend/tests/fixtures/sslvpn_fixtures.go)：账号命名及角色；不是当前数据库清单，不执行它作为准备步骤。
- [侧栏实现](../../itsm-frontend/src/components/layout/sidebar/Sidebar.tsx)、[菜单配置参考](../../itsm-frontend/src/components/layout/sidebar/menu-config.ts)：动态菜单来源及路径差异。
