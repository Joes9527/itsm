# 配置字典 → 新 ITSM 字段选项 对账表（seed 锚定）

- 状态：draft，供复核；不写目标库。
- 权威：固定制品 `0788a9bb` 的 `config/seed/default.json` 模板字段选项。
- 口径：**选项集对账**。已覆盖=旧值已被 seed 选项表达；差额=需决定新增选项/归并/排除；无落点组=未接纳。
- 仅对**语义关联组**精确对账；其余见 §3 未接纳。
- 旧字典：988 项 / 183 组；seed 含选项字段：18 个。

## 1. 权威目标字段（18 个含选项字段）

| 模板 | 字段 key | 字段名 | 选项 |
| --- | --- | --- | --- |
| 账号申请 | `account_type` | 账号类型 | AD账号(ad)；邮箱账号(email)；VPN账号(vpn)；业务系统账号(biz_system)；堡垒机账号(bastion) |
| 账号申请 | `target_system` | 相关系统 | KTMS - 运输管理(ktms)；KAPP - 物流App(kapp)；Ksmart - 货代系统(ksmart)；KWMS - 仓储管理(kwms)；KOMS - 订单管理(koms)；KBMS - 结算系统(kbms)；K365 - 结算平台(k365)；Ksmart Air - 空运(ksmart_air)；顺丰对接(sf_express) |
| 账号申请 | `operation` | 操作类型 | 新增/开通(create)；停用/回收(disable)；权限修改(modify)；密码重置(reset)；续期(renew) |
| 业务系统服务申请 | `target_system` | 相关系统 | KTMS - 运输管理(ktms)；KAPP - 物流App(kapp)；Ksmart - 货代系统(ksmart)；KWMS - 仓储管理(kwms)；KOMS - 订单管理(koms)；KBMS - 结算系统(kbms)；K365 - 结算平台(k365)；Ksmart Air - 空运(ksmart_air)；顺丰对接(sf_express) |
| 业务系统服务申请 | `operation` | 操作类型 | 数据修正(data_fix)；配置变更(config_change)；权限变更(perm_change)；服务方式修改(service_modify)；咨询(consult)；其他(other) |
| 故障报修 | `fault_category` | 故障分类 | 电脑无法开机(pc_no_power)；操作系统异常(os_error)；网络连接中断(network_down)；邮件收发异常(email_error)；打印机故障(printer_error)；软件异常(software_error)；外设故障(peripheral)；其他(other) |
| 故障报修 | `impact` | 影响范围 | 仅本人(self)；同组多人(team)；部门范围(dept)；大面积(wide) |
| 通用服务申请 | `service_type` | 服务类型 | 软件安装(software_install)；设备申请(device_request)；网络放行(network_allow)；会议室支持(meeting)；IT咨询(consult)；其他(other) |
| 通用服务申请 | `target_system` | 相关系统/资产 | KTMS - 运输管理(ktms)；KAPP - 物流App(kapp)；Ksmart - 货代系统(ksmart)；KWMS - 仓储管理(kwms)；KOMS - 订单管理(koms)；KBMS - 结算系统(kbms)；K365 - 结算平台(k365)；Ksmart Air - 空运(ksmart_air)；顺丰对接(sf_express) |
| 邮箱服务申请 | `operation` | 操作类型 | 邮箱开通(create)；邮箱停用(disable)；容量调整(quota)；共享邮箱申请(shared_mailbox)；协作空间权限(collab_perm) |
| 邮件故障报修 | `fault_type` | 故障类型 | 邮件发送失败(send_fail)；邮件接收异常(receive_error)；邮箱登录异常(login_error)；客户端同步异常(sync_error)；邮件误拦截(blocked)；其他(other) |
| 邮件故障报修 | `impact` | 影响范围 | 仅本人(self)；同组多人(team)；部门范围(dept) |
| 网络接入申请 | `access_type` | 接入类型 | VPN开通(vpn_create)；VPN停用(vpn_disable)；VPN权限变更(vpn_modify)；有线网络接入(wired)；无线网络接入(wifi)；端口开通(port)；网络放行(allowlist)；白名单申请(whitelist) |
| 网络接入申请 | `validity_period` | 有效期 | 临时(7天)(7d)；1个月(1m)；3个月(3m)；6个月(6m)；1年(1y)；长期(permanent) |
| 平台资源申请 | `resource_type` | 资源类型 | 服务器(server)；虚拟机(vm)；云资源(cloud)；存储空间(storage)；数据库(database)；中间件(middleware)；文件共享(fileshare)；其他(other) |
| 平台资源申请 | `expected_duration` | 预计使用期限 | 1个月(1m)；3个月(3m)；6个月(6m)；1年(1y)；长期(permanent) |
| 安全事件上报 | `incident_type` | 事件类型 | 安全事件(security_event)；终端感染/病毒(malware)；可疑行为(suspicious)；钓鱼邮件(phishing)；数据泄漏风险(dlp)；其他(other) |
| IT咨询请求 | `consult_type` | 咨询类型 | 业务流程咨询(process)；系统使用咨询(system_use)；操作指导(operation_guide)；服务引导/需求识别(guidance)；投诉与反馈(feedback)；其他(other) |

## 2. 关联组精确对账与建议去向

### 旧组「系统名称」（14 项） → 字段 `target_system`

| 旧值 | 旧 code/en | 判定 | 建议去向 |
| --- | --- | --- | --- |
| 其他 | sysname14 | 排除 | 通用占位值 |
| SQR系统 | sysname11 | 差额 | **新增选项**：target_system/`sqr` |
| KAMS | sysname07 | 差额 | **新增选项**：target_system/`kams` |
| Yonyou | sysname12 | 差额 | **新增选项**：target_system/`yonyou` |
| KAPP | sysname05 | 已覆盖 | 命中 seed 选项 |
| K3.5 | sysname08 | 差额 | **新增选项**：target_system/`k3_5` |
| HR休假系统 | sysname13 | 差额 | **新增选项**：target_system/`hr_leave`（若确为业务系统，否则排除） |
| K3/K5 | sysname09 | 差额 | **归并**：target_system/`k3_5`（同族） |
| KBMS | sysname06 | 已覆盖 | 命中 seed 选项 |
| FLUX | sysname04 | 差额 | **新增选项**：target_system/`flux` |
| KWMS2.0 | sysname02 | 差额 | **归并**：target_system/`kwms` |
| KWMS365 | sysname03 | 差额 | **归并**：target_system/`kwms`（或新增 `kwms365`） |
| BMS | sysname10 | 差额 | **新增选项**：target_system/`bms`；并须在 CMDB 业务系统清单有对应 CI |
| KWMS1.0 | sysname01 | 差额 | **归并**：target_system/`kwms`（同仓储系统版本） |

### 旧组「请求类型」（18 项） → 字段 `operation`, `service_type`, `account_type`, `access_type`, `consult_type`

| 旧值 | 旧 code/en | 判定 | 建议去向 |
| --- | --- | --- | --- |
| 数据修改 | shujuxiugai | 差额 | **归并**：业务系统服务申请/operation `data_fix` 数据修正 |
| 异常、中断或报错 | yichang | 差额 | **排除**：属事件语义，映射 recordClass=incident/故障报修模板，不是请求选项 |
| 信息咨询 | xxzx | 差额 | **归并**：业务系统服务申请/operation `consult` 或 IT咨询/consult_type |
| 密码问题 | password | 差额 | **归并**：账号申请/operation `reset` 密码重置 |
| 数据导出 | export | 差额 | **新增选项**：通用服务申请/service_type 或账号申请/operation `data_export` |
| 投诉 | tousu | 差额 | **归并**：IT咨询请求/consult_type `feedback` 投诉与反馈 |
| 优化设置 | youhua | 差额 | **排除**：非标准服务选项 |
| id增删改 | idadd | 差额 | **归并**：账号申请/operation `create`/`modify`（账号增改） |
| 下单 | xiadan | 差额 | **排除**：业务动作，非 IT 服务选项 |
| 咨询 | zixun | 已覆盖 | 命中 seed 选项 |
| 资产采购 | zsgc | 差额 | **新增选项**：通用服务申请/service_type `asset_purchase` |
| 权限修改 | quanxianxiugai | 已覆盖 | 命中 seed 选项 |
| 发布或变更请求 | fabu | 差额 | **排除**：应映射变更 recordClass/流程，不是请求选项 |
| 增删改查等服务请求 | zsgc | 差额 | **归并**：通用服务申请/service_type（通用）或业务系统服务申请/operation |
| 建议 | jianyi | 差额 | **归并**：IT咨询请求/consult_type `feedback`（或新增 `suggestion`） |
| 软件安装 | ruanjianaz | 已覆盖 | 命中 seed 选项 |
| 派单 | paidan | 差额 | **排除**：运维动作，非服务选项 |
| 其它 | requestqita | 排除 | 通用占位值 |

### 旧组「邮箱申请类别」（4 项） → 字段 `operation`

| 旧值 | 旧 code/en | 判定 | 建议去向 |
| --- | --- | --- | --- |
| 公共 | emailType02 | 差额 | **新增选项**：邮箱服务申请/operation `public_mailbox` |
| 群组 | emailType04 | 差额 | **新增选项**：邮箱服务申请/operation `group_mailbox` |
| 个人 | emailType01 | 差额 | **归并**：邮箱服务申请/operation `create` 邮箱开通 |
| 共享 | emailType03 | 差额 | **归并**：邮箱服务申请/operation `shared_mailbox` 共享邮箱申请 |

### 旧组「影响范围」（8 项） → 字段 `impact`

| 旧值 | 旧 code/en | 判定 | 建议去向 |
| --- | --- | --- | --- |
| 严重 | YZ | 差额 | **排除**：旧为严重度；seed `impact` 是影响对象范围，语义不同，应归优先级/严重度 |
| 无 | wu | 差额 | **排除**：通用占位/语义不同 |
| 普通 | PT | 差额 | **排除**：同上，语义不同 |
| 无 | incidence01 | 差额 | **排除**：通用占位/语义不同 |
| 普通 | incidence03 | 差额 | **排除**：同上，语义不同 |
| 轻微 | incidence02 | 差额 | **排除**：同上，语义不同 |
| 轻微 | QW | 差额 | **排除**：同上，语义不同 |
| 严重 | incidence04 | 差额 | **排除**：旧为严重度；seed `impact` 是影响对象范围，语义不同，应归优先级/严重度 |

关联组小结：已覆盖 **5**，差额 **37**（建议见上表）。

## 3. 未接纳组（无对应目标字段，按现状登记）

共 178 组：

| 旧组 | 项数 |
| --- | ---: |
| <ungrouped> | 38 |
| K35权限 | 37 |
| 请求管理 | 29 |
| 公共字典 | 28 |
| 行业 | 22 |
| 请求动作按钮 | 21 |
| 事件动作按钮 | 20 |
| 变更动作按钮 | 18 |
| 问题动作按钮 | 15 |
| 语言包词条管理(Manage All) | 15 |
| 事件管理 | 13 |
| 缺陷模块 | 12 |
| 需求权限 | 11 |
| 项目模块 | 11 |
| 根本原因 | 11 |
| 任务模块类型 | 10 |
| 值班标识 | 10 |
| 排班值班标识 | 10 |
| 预警事件 | 10 |
| 项目状态 | 10 |
| 工单状态类型 | 9 |
| 省份 | 9 |
| 需求模块 | 9 |
| 工单状态 | 9 |
| 任务状态 | 9 |
| 责任人所属室 | 8 |
| 驳回原因 | 8 |
| 子事件类型 | 8 |
| 原因类型 | 8 |
| 变更来源 | 8 |
| 项目来源 | 7 |
| 用例类型 | 7 |
| 缺陷定位_系统 | 7 |
| 事件来源 | 7 |
| 事件工单状态 | 7 |
| 缺陷定位_浏览器 | 7 |
| 知识状态 | 7 |
| 缺陷状态 | 7 |
| 工作项 | 7 |
| 变更状态 | 7 |
| 事件类型 | 7 |
| 环境 | 6 |
| 用例适用阶段 | 6 |
| 缺陷类型 | 6 |
| 需求评审状态 | 6 |
| 所属区域 | 6 |
| 事件处理类型 | 6 |
| 主要责任方 | 6 |
| 服务台动作按钮 | 6 |
| 需求优先级 | 6 |
| 知识动作按钮 | 6 |
| 所属年份 | 6 |
| 报告级别 | 6 |
| 事件等级 | 6 |
| 缺陷解决方案 | 5 |
| 需求来源 | 5 |
| 完成代码 | 5 |
| 问题来源 | 5 |
| 挂起代码 | 5 |
| 假期 | 5 |
| 知识管理 | 5 |
| 配置状态 | 5 |
| 事件子任务状态 | 5 |
| 进度条 | 5 |
| 工时分类 | 5 |
| 其他模块 | 5 |
| 排班部门 | 5 |
| 请求子任务状态 | 5 |
| 上网地点 | 5 |
| 区域 | 4 |
| 项目 | 4 |
| 用户状态 | 4 |
| 变更子任务状态 | 4 |
| 任务类型 | 4 |
| 用例执行结果 | 4 |
| 缺陷驳回原因 | 4 |
| 是否 | 4 |
| 事件影响度 | 4 |
| 新增或取消已有账号 | 4 |
| 事件上报方式 | 4 |
| 缺陷挂起原因 | 4 |
| 挂起原因 | 4 |
| 数据权限 | 4 |
| 缺陷来源 | 4 |
| 用例模块 | 4 |
| KBMS角色 | 4 |
| 紧急程度 | 4 |
| 安装状态 | 4 |
| 任务模块 | 4 |
| 邮件模版 | 4 |
| 短信模版 | 4 |
| 紧急度 | 4 |
| 技术需求优先级 | 3 |
| 关闭原因 | 3 |
| 通知模版接收人设置 | 3 |
| 任务退回挂起原因 | 3 |
| 问题原因分类 | 3 |
| 所用电脑 | 3 |
| 严重程度 | 3 |
| 群组用途类型 | 3 |
| 内容类型 | 3 |
| 请求性质 | 3 |
| 用户故事优先级 | 3 |
| 未解决原因 | 3 |
| 用例优先级 | 3 |
| 缺陷优先级 | 3 |
| 任务挂起原因 | 3 |
| k3.5常规/非常规 | 3 |
| 风险管理计划状态 | 3 |
| 通知模版类型 | 3 |
| 有用 | 3 |
| k3.5业务类型 | 3 |
| 操作类型 | 3 |
| 申请邮箱版本 | 3 |
| 知识库类型 | 3 |
| 需求紧急程度 | 3 |
| 区域标识 | 3 |
| 团队类型 | 3 |
| 通知方式 | 3 |
| 任务优先级 | 3 |
| 项目优先级 | 3 |
| 使用者身份 | 3 |
| 没用 | 3 |
| 在线状态 | 3 |
| 结果 | 3 |
| 任务情况 | 3 |
| 需求状态 | 3 |
| 工单是否超时 | 2 |
| 缺陷测试驳回原因 | 2 |
| 知识能效级别 | 2 |
| 报告状态 | 2 |
| 岗位 | 2 |
| 性别 | 2 |
| 网关(网络室分配) | 2 |
| DNS域名操作 | 2 |
| 公告类型 | 2 |
| 任务退回原因 | 2 |
| 反向解析 | 2 |
| 响应及处理规则开始时间 | 2 |
| 机构类型 | 2 |
| K3.5是否 | 2 |
| 报告类型 | 2 |
| 职务 | 2 |
| 响应及处理规则 | 2 |
| 审批步骤类型 | 2 |
| 使用者 | 2 |
| 用户类型 | 2 |
| 迭代状态 | 2 |
| 响应及处理规则截至时间 | 2 |
| 变更类型 | 2 |
| 事件状态 | 2 |
| 项目名称 | 2 |
| 业务类型 | 2 |
| 所属产品 | 2 |
| 需求不通过原因 | 2 |
| 知识自助 | 2 |
| 处理完成 | 1 |
| 分析中 | 1 |
| 服务请求 | 1 |
| 问题流程 | 1 |
| 已提交 | 1 |
| 事件流程 | 1 |
| 指派工程师类别 | 1 |
| 安装单完成代码 | 1 |
| 问题状态 | 1 |
| 待签收 | 1 |
| 待评价 | 1 |
| 迭代模块 | 1 |
| 产品模块 | 1 |
| 变更流程 | 1 |
| 处理中 | 1 |
| 安装流程 | 1 |
| 已回顾 | 1 |
| 服务台 | 1 |
| 产品类型 | 1 |
| 通用 | 1 |
| 已受理 | 1 |
| 需求类型 | 1 |
| 项目类型 | 1 |
