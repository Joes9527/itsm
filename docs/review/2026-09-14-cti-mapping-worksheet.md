# 旧 CTI 节点类型分流与映射工作表

> 权威配置：固定制品 `0788a9bb` 的 `itsm-backend/config/seed/default.json`。
> 旧 CTI 是**混合树**：业务系统→CMDB `business_system`（开单选 `ci_ids`，不进分类）；服务/动作→`ticket_categories`；组织/地点、基础设施另维。
> “建议目标”为自动相似度提示，**需人工确认**；目标库 ID 准入时生成。

## 摘要（按节点类型）

| 节点类型 | 数量 | 目标维度 |
| --- | ---: | --- |
| business_system | 49 | CMDB `business_system` CI |
| org_location | 17 | Phase 1 部门/地点 |
| ticket_category | 8 | `ticket_categories` |
| exclude | 4 | 排除 |
| infra_ci | 4 | CMDB CI（infra） |

## 逐节点映射工作表

| 旧路径 | 旧 ctiId | 节点类型 | 路由引用 | 目标 | 相似度 | 你的确认 |
| --- | --- | --- | --- | --- | --- | --- |
| BMS系统 | `3fb52530b83c4ba99871d12978ce510a` | business_system | 0 | 建 CMDB `business_system` CI：BMS系统 |  |  |
| CL-BI系统 | `d3193d15744647499ccba0c200b62314` | business_system | 0 | 建 CMDB `business_system` CI：CL-BI系统 |  |  |
| HR-BI | `0084f6701c1642b786d6b84bbaa054ef` | business_system | 0 | 建 CMDB `business_system` CI：HR-BI |  |  |
| K3.5数据变更 | `5be4d09ae4e344e38305f6d008405139` | ticket_category | 0 | 业务系统支持 / IL业务线支持 / IL主数据变更申请 / `APP-IL-DAT-002` | 0.5 |  |
| K3.5系统 | `6d09df4b7f574608b85e5e903ae52c6b` | business_system | 0 | 建 CMDB `business_system` CI：K3.5系统 |  |  |
| KAPP&KOMS系统 | `d21b8854d32a40b9905355fe739a60e8` | business_system | 1 | 建 CMDB `business_system` CI：KAPP&KOMS系统 |  |  |
| KAPP系统 | `43844d678b9541108a217353ea97e38b` | business_system | 0 | 建 CMDB `business_system` CI：KAPP系统 |  |  |
| KBMS结算系统 | `be5b5bb33d9c494ea45ba5ade7e39040` | business_system | 0 | 建 CMDB `business_system` CI：KBMS结算系统 |  |  |
| KBMS结算系统_ | `8a35110d259d40d39c2cf1a3cc923b48` | business_system | 0 | 建 CMDB `business_system` CI：KBMS结算系统_ |  |  |
| KCTS系统 | `d8edfca3745746119e7798b612ec5e0d` | business_system | 0 | 建 CMDB `business_system` CI：KCTS系统 |  |  |
| KEAS大数据平台 | `e4a2227811e04f74a3cf052d21ade9ca` | business_system | 0 | 建 CMDB `business_system` CI：KEAS大数据平台 |  |  |
| KOMS主客户实施 | `ea02ecaff4e9438296b5bdb804ddc0c5` | business_system | 0 | 建 CMDB `business_system` CI：KOMS主客户实施 |  |  |
| KTMS系统 | `44b85c79a0f04c59bf48986aea3b0399` | business_system | 0 | 建 CMDB `business_system` CI：KTMS系统 |  |  |
| KTMS系统_ | `aa2e0cd25f5b4ef3b6e1363e94a4601a` | business_system | 0 | 建 CMDB `business_system` CI：KTMS系统_ |  |  |
| KWMS系统 | `56f61337d0124ea3b02f4632e97814a8` | business_system | 0 | 建 CMDB `business_system` CI：KWMS系统 |  |  |
| KWMS系统_ | `f3a3a7cf233a414d8f4b8c2d4f6e3cfa` | business_system | 0 | 建 CMDB `business_system` CI：KWMS系统_ |  |  |
| Ksmart系统 | `cdb8ca38cfbb4df38f08b10fb8fe0c93` | business_system | 0 | 建 CMDB `business_system` CI：Ksmart系统 |  |  |
| Ksmart系统_ | `9a1c1940a851422fadc3707cb636eac3` | business_system | 0 | 建 CMDB `business_system` CI：Ksmart系统_ |  |  |
| LTL-BI系统 | `2e8509e420b14f24ba3d56e7953a3e22` | business_system | 3 | 建 CMDB `business_system` CI：LTL-BI系统 |  |  |
| OA申请 | `6217e2ebb22b4ef890d0af9af31c8f7a` | ticket_category | 0 | _（无候选，需补建/排除）_ |  |  |
| OA申请 / AD账户申请（Windows账户） | `8c81b8d98ed5470db217389d8436f66c` | ticket_category | 6 | _（无候选，需补建/排除）_ |  |  |
| OA申请 / O365邮箱导出申请 | `5df87242c8594dfa903290bbde570438` | ticket_category | 1 | 邮箱与Microsoft 365协作服务 / 共享资源与协作空间 / 共享邮箱申请 / `COL-SHR-001` | 0.5 |  |
| OA申请 / O365邮箱账户申请 | `f01241a4136d455680999c8759e94283` | ticket_category | 6 | 邮箱与Microsoft 365协作服务 / 共享资源与协作空间 / 共享邮箱申请 / `COL-SHR-001` | 0.5 |  |
| OA申请 / SSLVPN账号申请 | `05fae6bef2aa40ba90218daaf33ebd43` | ticket_category | 1 | 网络与远程访问服务 / VPN与远程连接 / VPN开通申请 / `NET-VPN-001` | 0.59 |  |
| OA申请 / 业务系统服务申请 | `253c64968ab046a0bf85799df965147c` | ticket_category | 0 | 业务系统支持 / `APP` | 0.57 |  |
| OA申请 / 业务系统账号申请 | `b95693094805482da65f0bcf51133806` | ticket_category | 0 | 账号与访问服务 / 特权与临时权限 / 特权账号申请 / `ACC-PRV-001` | 0.57 |  |
| OA系统 | `d75f90ea58824c76be4dd82efb09cee6` | business_system | 0 | 建 CMDB `business_system` CI：OA系统 |  |  |
| test | `9cd71e9b23db47c786a1aca485906bf1` | exclude | 0 | 排除 |  |  |
| test2 | `70d24d24c5054103b950ba10b9f0d1c2` | exclude | 0 | 排除 |  |  |
| 企业应用系统 | `0405630ab5ba4310a585ce0303456438` | business_system | 0 | 建 CMDB `business_system` CI：企业应用系统 |  |  |
| 企业应用系统 / Back Office | `2956bb98934c499b925a42cc05bea25c` | business_system | 0 | 建 CMDB `business_system` CI：Back Office |  |  |
| 企业应用系统 / Back Office / OA | `4a1f80f59da8482ebed44e34027dedbd` | business_system | 9 | 建 CMDB `business_system` CI：OA |  |  |
| 企业应用系统 / Back Office / 质量报案系统 | `6067201856934723869490de5282faf0` | business_system | 9 | 建 CMDB `business_system` CI：质量报案系统 |  |  |
| 企业应用系统 / IFF | `215a3d9095564db0b5333fa7e94ca56c` | business_system | 0 | 建 CMDB `business_system` CI：IFF |  |  |
| 企业应用系统 / IFF / DMS系统 | `3a52c34816a84fd784dead09965f47aa` | business_system | 9 | 建 CMDB `business_system` CI：DMS系统 |  |  |
| 企业应用系统 / IFF / K35系统 | `2c5c8fdd65ef4dbba6f2858e81036b35` | business_system | 18 | 建 CMDB `business_system` CI：K35系统 |  |  |
| 企业应用系统 / ITSM | `0941d803f2c84eab9ff58aa639620179` | business_system | 0 | 建 CMDB `business_system` CI：ITSM |  |  |
| 企业应用系统 / KAPP | `2ca22d902fd241859e8a820c2c823dfd` | business_system | 0 | 建 CMDB `business_system` CI：KAPP |  |  |
| 企业应用系统 / KAPP / CSC | `3d30541b8aa546b68af82970dd9416e7` | business_system | 91 | 建 CMDB `business_system` CI：CSC |  |  |
| 企业应用系统 / KAPP / KAPP | `0205eed96e784d249a7d49b724f0cb0b` | business_system | 36 | 建 CMDB `business_system` CI：KAPP |  |  |
| 企业应用系统 / KAPP / KBMS结算 | `d68b411584404600a23b07a7ab564cfc` | business_system | 36 | 建 CMDB `business_system` CI：KBMS结算 |  |  |
| 企业应用系统 / KAPP / KOMS | `8ee15341357241efb950ae84c65fcf7d` | business_system | 36 | 建 CMDB `business_system` CI：KOMS |  |  |
| 企业应用系统 / KAPP / KTMS | `f268adeb98c742aa92ebb5a823787906` | business_system | 31 | 建 CMDB `business_system` CI：KTMS |  |  |
| 企业应用系统 / KAPP / 运维支持 | `eec83381511a40aab0f24b9be5d2bf83` | business_system | 0 | 建 CMDB `business_system` CI：运维支持 |  |  |
| 企业应用系统 / KWMS北区 | `d35f56e902034623b8326a459abbaddd` | business_system | 0 | 建 CMDB `business_system` CI：KWMS北区 |  |  |
| 企业应用系统 / KWMS北区 / Ec-kassist | `1bd1a87f223547389308498cd96cc7be` | business_system | 13 | 建 CMDB `business_system` CI：Ec-kassist |  |  |
| 企业应用系统 / KWMS北区 / KWMS1.0 | `7b915bfba7984c4b8f3013aebfed4ca3` | business_system | 11 | 建 CMDB `business_system` CI：KWMS1.0 |  |  |
| 企业应用系统 / KWMS北区 / KWMS1.0 & KWMS2.0-新需求 | `44bf58792a02440b92788644f18785cd` | business_system | 11 | 建 CMDB `business_system` CI：KWMS1.0 & KWMS2.0-新需求 |  |  |
| 企业应用系统 / KWMS北区 / KWMS1.0-EDI | `23fad2dfeb0e4e3bb563fe8a12ed51ed` | business_system | 11 | 建 CMDB `business_system` CI：KWMS1.0-EDI |  |  |
| 企业应用系统 / KWMS北区 / KWMS365 | `b789f62e6ef447b9af2c07be25f7396c` | business_system | 8 | 建 CMDB `business_system` CI：KWMS365 |  |  |
| 企业应用系统 / KWMS北区 / 本地系统-KTLS | `0a2e992ba27542e8bc9d6d6f979f09af` | business_system | 12 | 建 CMDB `business_system` CI：本地系统-KTLS |  |  |
| 企业应用系统 / KWMS南区 | `442a2600f4014da18efa282ab6306f1c` | business_system | 0 | 建 CMDB `business_system` CI：KWMS南区 |  |  |
| 企业应用系统 / KWMS南区 / CWOS | `bc4881cea3c2491b845aa8c83765fc45` | business_system | 11 | 建 CMDB `business_system` CI：CWOS |  |  |
| 企业应用系统 / KWMS南区 / FICS  | `8b250a8452324112a79483740836d926` | business_system | 11 | 建 CMDB `business_system` CI：FICS  |  |  |
| 企业应用系统 / KWMS南区 / KWMS1.0 & KWMS2.0-新需求 | `1def98b2e738450ba80361a42310aa0d` | business_system | 13 | 建 CMDB `business_system` CI：KWMS1.0 & KWMS2.0-新需求 |  |  |
| 企业应用系统 / KWMS南区 / KWMS1.0 & Local-kassist | `59dadf21ef00448eb5466c94963494fa` | business_system | 13 | 建 CMDB `business_system` CI：KWMS1.0 & Local-kassist |  |  |
| 企业应用系统 / KWMS南区 / KWMS1.0-EDI | `63147a7fe0894204824156b93117c88f` | business_system | 13 | 建 CMDB `business_system` CI：KWMS1.0-EDI |  |  |
| 企业应用系统 / KWMS南区 / Kflex-U8 | `27e3c6c8780f4a11976c7b17df16dd42` | business_system | 9 | 建 CMDB `business_system` CI：Kflex-U8 |  |  |
| 企业应用系统 / 邮件系统 | `34b675b5b03546ac9244bce4652bd9a6` | business_system | 9 | 建 CMDB `business_system` CI：邮件系统 |  |  |
| 其他 | `577b276d6b6844f2bbe70de5db6b9f36` | exclude | 2 | 排除 |  |  |
| 其他 / 其他 | `2b3a6a0547ff478c84e57ab2b2567256` | exclude | 12 | 排除 |  |  |
| 基础架构 | `9f369d7e10394a078922dbc21c7a3745` | infra_ci | 0 | CMDB CI（infra） |  |  |
| 基础架构 / 数据库 | `b2b9ef80b06b4ae499a1b87a9941868f` | infra_ci | 11 | CMDB CI（infra） |  |  |
| 基础架构 / 服务器 | `4a0ece2db0004d258a35488480d696d0` | infra_ci | 11 | CMDB CI（infra） |  |  |
| 基础架构 / 网络 | `40c5ee732d1b425293ee2cbe1062b3f4` | infra_ci | 11 | CMDB CI（infra） |  |  |
| 本地系统-上海支持中心 | `d73f2c4f4fce441caacae6a8b0c8c9ba` | org_location | 0 | Phase 1 部门/地点 |  |  |
| 本地系统-上海支持中心 / 上海 | `dd352ba467cb4307a1344b2b06050d2c` | org_location | 17 | Phase 1 部门/地点 |  |  |
| 本地系统-上海支持中心 / 无锡 | `ecfaaa0b1ea543adb40900835ba542cf` | org_location | 5 | Phase 1 部门/地点 |  |  |
| 本地系统-上海支持中心 / 青岛 | `8f7915d3c25f4bdca2b0fb681743ee6f` | org_location | 5 | Phase 1 部门/地点 |  |  |
| 本地系统-北京支持中心 | `d4313d52592541dc99573129b1015b00` | org_location | 0 | Phase 1 部门/地点 |  |  |
| 本地系统-北京支持中心 / 北京分公司 | `80cb50e7920a45a387144e72c9241c39` | org_location | 9 | Phase 1 部门/地点 |  |  |
| 本地系统-北京支持中心 / 北京总部 | `9f8a985202a14da69ef9a06605938b35` | org_location | 7 | Phase 1 部门/地点 |  |  |
| 本地系统-北京支持中心 / 大连 | `81569821feff484db48539deafbade1f` | org_location | 7 | Phase 1 部门/地点 |  |  |
| 本地系统-北京支持中心 / 天津 | `81446755fdeb4d37bc0a9d8e9a2aa6a7` | org_location | 9 | Phase 1 部门/地点 |  |  |
| 本地系统-厦门支持中心 | `f175869ce48b49a6b68445fdbdd39113` | org_location | 0 | Phase 1 部门/地点 |  |  |
| 本地系统-厦门支持中心 / 应用系统 | `2b7e2aafc2c84edbadf720bc94c3b7c0` | org_location | 20 | Phase 1 部门/地点 |  |  |
| 本地系统-厦门支持中心 / 硬件网络 | `de7a7eaad0e94aed92ce546826464495` | org_location | 12 | Phase 1 部门/地点 |  |  |
| 本地系统-深圳支持中心 | `cb2ffb4fc4e3410e9305bdf13d7bc8e3` | org_location | 0 | Phase 1 部门/地点 |  |  |
| 本地系统-深圳支持中心 / 南宁 | `8758c8a5469d44eda9d43c40015fff19` | org_location | 7 | Phase 1 部门/地点 |  |  |
| 本地系统-深圳支持中心 / 成都 | `fd163c8ff2754d87b617a959d681c21a` | org_location | 2 | Phase 1 部门/地点 |  |  |
| 本地系统-深圳支持中心 / 深圳 | `dd0ab54e8d8b437cb4615a5b9629d5fc` | org_location | 15 | Phase 1 部门/地点 |  |  |
| 本地系统-深圳支持中心 / 重庆 | `6d4841d6737744da875813f2f2b003d3` | org_location | 2 | Phase 1 部门/地点 |  |  |

## 缺父引用（被路由引用但不在旧 CTI 树中）

- `02ec6fdb451047fe9b7d3ea5270468d4`（路由引用 11 条）
- `1d02d739f9b84378b4b893030d2c54e7`（路由引用 1 条）
- `22b18667ea904b2ab6f05d7243fd4c1a`（路由引用 12 条）
- `2b2c869fb2e44d11a30261b40cf8cad3`（路由引用 6 条）
- `4c4213f24a984c1f83076c543767fc6d`（路由引用 11 条）
- `4e20d9806662497d95e4923d2516b55e`（路由引用 10 条）
- `51b42b47eab6462880cbca6bcaeedd82`（路由引用 5 条）
- `5673eadc09a24170bf9627ae8df7e270`（路由引用 11 条）
- `9efc89c828cc42e1adec83e283cde29b`（路由引用 7 条）
- `a5405d694f5a43ca8d2e8ad358b826bc`（路由引用 12 条）
- `bff18c82361f4a589503e7d0c78ca9f5`（路由引用 7 条）
- `c1e06dbaf5d3421a9a32228322670684`（路由引用 2 条）
- `f0114020950648fc918b35f8d78e7bf2`（路由引用 12 条）
- `f26af604dcdb4999bcb9c6bb49d004c0`（路由引用 11 条）

## 权威 seed 分类全量（182，分类类节点映射参照）

- L1 账号与访问服务  (`ACC`)
- L1 终端与办公支持  (`EUC`)
- L1 邮箱与Microsoft 365协作服务  (`COL`)
- L1 网络与远程访问服务  (`NET`)
- L1 平台与基础设施服务  (`INF`)
- L1 业务系统支持  (`APP`)
- L1 安全与合规支持  (`SEC`)
- L1 咨询与服务引导  (`ADV`)
- L2 账号与访问服务 / AD与基础账号  (`ACC-AD`)
- L2 账号与访问服务 / 认证与登录支持  (`ACC-AUT`)
- L2 账号与访问服务 / 特权与临时权限  (`ACC-PRV`)
- L2 账号与访问服务 / 账号生命周期管理  (`ACC-LCM`)
- L2 咨询与服务引导 / 业务流程咨询  (`ADV-PRC`)
- L2 咨询与服务引导 / 系统使用咨询  (`ADV-SYS`)
- L2 咨询与服务引导 / 操作指导与知识支持  (`ADV-KB`)
- L2 咨询与服务引导 / 服务引导与需求识别  (`ADV-GUI`)
- L2 业务系统支持 / IL业务线支持  (`APP-IL`)
- L2 业务系统支持 / IFF & Back-office业务线支持  (`APP-IFF`)
- L2 业务系统支持 / Transportation业务线支持  (`APP-TRN`)
- L2 业务系统支持 / 跨业务线通用支持  (`APP-GEN`)
- L2 邮箱与Microsoft 365协作服务 / 邮箱账号与基础配置  (`COL-MAIL`)
- L2 邮箱与Microsoft 365协作服务 / 邮件收发与访问支持  (`COL-ACC`)
- L2 邮箱与Microsoft 365协作服务 / Teams与会议协作支持  (`COL-MTG`)
- L2 邮箱与Microsoft 365协作服务 / 共享资源与协作空间  (`COL-SHR`)
- L2 邮箱与Microsoft 365协作服务 / Microsoft 365 License与订阅支持  (`COL-LIC`)
- L2 邮箱与Microsoft 365协作服务 / 邮件安全与协作安全例外  (`COL-SEC`)
- L2 终端与办公支持 / 终端交付与标准环境  (`EUC-STD`)
- L2 终端与办公支持 / 终端硬件与基础故障支持  (`EUC-HDW`)
- L2 终端与办公支持 / 外设与打印支持  (`EUC-PRT`)
- L2 终端与办公支持 / 桌面系统与办公环境支持  (`EUC-ENV`)
- L2 终端与办公支持 / 设备资产生命周期协同  (`EUC-AST`)
- L2 平台与基础设施服务 / 存储与备份服务  (`INF-STR`)
- L2 平台与基础设施服务 / 服务器与计算资源  (`INF-SRV`)
- L2 平台与基础设施服务 / 虚拟机与云资源  (`INF-VM`)
- L2 平台与基础设施服务 / 平台访问与基础配置  (`INF-ACC`)
- L2 平台与基础设施服务 / 中间件与数据库平台  (`INF-MDW`)
- L2 平台与基础设施服务 / 环境与部署支持  (`INF-ENV`)
- L2 网络与远程访问服务 / 有线与无线网络接入  (`NET-ACC`)
- L2 网络与远程访问服务 / VPN与远程连接  (`NET-VPN`)
- L2 网络与远程访问服务 / 网络访问控制与放行  (`NET-CTL`)
- L2 网络与远程访问服务 / 网络故障与连通性异常  (`NET-INC`)
- L2 安全与合规支持 / 安全事件与应急响应  (`SEC-EMG`)
- L2 安全与合规支持 / 安全评估与扫描  (`SEC-SCN`)
- L2 安全与合规支持 / 身份与访问安全控制  (`SEC-IAM`)
- L2 安全与合规支持 / 终端与数据安全控制  (`SEC-DLP`)
- L2 安全与合规支持 / 合规与审计支持  (`SEC-CMP`)
- L3 账号与访问服务 / AD与基础账号 / AD账号新建  (`ACC-AD-001`)
- L3 账号与访问服务 / AD与基础账号 / AD账号停用  (`ACC-AD-002`)
- L3 账号与访问服务 / 认证与登录支持 / 密码重置/解锁  (`ACC-AUT-001`)
- L3 账号与访问服务 / 认证与登录支持 / MFA绑定/重置  (`ACC-AUT-002`)
- L3 账号与访问服务 / 认证与登录支持 / 企业账号登录异常  (`ACC-AUT-003`)
- L3 账号与访问服务 / 账号生命周期管理 / 入职账号开通  (`ACC-LCM-001`)
- L3 账号与访问服务 / 账号生命周期管理 / 离职账号回收  (`ACC-LCM-002`)
- L3 账号与访问服务 / 特权与临时权限 / 特权账号申请  (`ACC-PRV-001`)
- L3 账号与访问服务 / 特权与临时权限 / 临时权限开通  (`ACC-PRV-002`)
- L3 咨询与服务引导 / 服务引导与需求识别 / 不清楚提哪类服务  (`ADV-GUI-001`)
- L3 咨询与服务引导 / 服务引导与需求识别 / 投诉与服务反馈受理  (`ADV-GUI-002`)
- L3 咨询与服务引导 / 操作指导与知识支持 / 标准操作指导  (`ADV-KB-001`)
- L3 咨询与服务引导 / 操作指导与知识支持 / SOP查询  (`ADV-KB-002`)
- L3 咨询与服务引导 / 业务流程咨询 / 流程怎么走咨询  (`ADV-PRC-001`)
- L3 咨询与服务引导 / 业务流程咨询 / 归口部门咨询  (`ADV-PRC-002`)
- L3 咨询与服务引导 / 系统使用咨询 / 某场景该用哪个系统咨询  (`ADV-SYS-001`)
- L3 咨询与服务引导 / 系统使用咨询 / 系统入口与使用路径咨询  (`ADV-SYS-002`)
- L3 业务系统支持 / 跨业务线通用支持 / 业务系统标准操作指导  (`APP-GEN-FNC-001`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office业务线功能咨询  (`APP-IFF-FNC-001`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office业务线角色权限申请  (`APP-IFF-PRM-001`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office主数据新增申请  (`APP-IFF-DAT-001`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office主数据变更申请  (`APP-IFF-DAT-002`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office业务数据查询申请  (`APP-IFF-DAT-003`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office数据修正申请  (`APP-IFF-DAT-004`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office接口异常受理  (`APP-IFF-IFC-001`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office数据传输失败  (`APP-IFF-IFC-002`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office系统无法登录  (`APP-IFF-INC-001`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office关键功能异常  (`APP-IFF-INC-002`)
- L3 业务系统支持 / IFF & Back-office业务线支持 / IFF & Back-office业务处理中断  (`APP-IFF-INC-003`)
- L3 业务系统支持 / IL业务线支持 / IL业务线功能咨询  (`APP-IL-FNC-001`)
- L3 业务系统支持 / IL业务线支持 / IL业务线角色权限申请  (`APP-IL-PRM-001`)
- L3 业务系统支持 / IL业务线支持 / IL主数据新增申请  (`APP-IL-DAT-001`)
- L3 业务系统支持 / IL业务线支持 / IL主数据变更申请  (`APP-IL-DAT-002`)
- L3 业务系统支持 / IL业务线支持 / IL业务数据查询申请  (`APP-IL-DAT-003`)
- L3 业务系统支持 / IL业务线支持 / IL数据修正申请  (`APP-IL-DAT-004`)
- L3 业务系统支持 / IL业务线支持 / IL接口异常受理  (`APP-IL-IFC-001`)
- L3 业务系统支持 / IL业务线支持 / IL数据传输失败  (`APP-IL-IFC-002`)
- L3 业务系统支持 / IL业务线支持 / IL系统无法登录  (`APP-IL-INC-001`)
- L3 业务系统支持 / IL业务线支持 / IL关键功能异常  (`APP-IL-INC-002`)
- L3 业务系统支持 / IL业务线支持 / IL业务处理中断  (`APP-IL-INC-003`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation业务线功能咨询  (`APP-TRN-FNC-001`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation业务线角色权限申请  (`APP-TRN-PRM-001`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation主数据新增申请  (`APP-TRN-DAT-001`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation主数据变更申请  (`APP-TRN-DAT-002`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation业务数据查询申请  (`APP-TRN-DAT-003`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation数据修正申请  (`APP-TRN-DAT-004`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation接口异常受理  (`APP-TRN-IFC-001`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation数据传输失败  (`APP-TRN-IFC-002`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation系统无法登录  (`APP-TRN-INC-001`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation关键功能异常  (`APP-TRN-INC-002`)
- L3 业务系统支持 / Transportation业务线支持 / Transportation业务处理中断  (`APP-TRN-INC-003`)
- L3 邮箱与Microsoft 365协作服务 / 邮件收发与访问支持 / 邮件发送失败  (`COL-ACC-001`)
- L3 邮箱与Microsoft 365协作服务 / 邮件收发与访问支持 / 邮件接收异常  (`COL-ACC-002`)
- L3 邮箱与Microsoft 365协作服务 / 邮件收发与访问支持 / 邮箱登录异常  (`COL-ACC-003`)
- L3 邮箱与Microsoft 365协作服务 / 邮件收发与访问支持 / 邮件客户端同步异常  (`COL-ACC-004`)
- L3 邮箱与Microsoft 365协作服务 / Microsoft 365 License与订阅支持 / 标准License分配申请  (`COL-LIC-001`)
- L3 邮箱与Microsoft 365协作服务 / Microsoft 365 License与订阅支持 / License变更/回收申请  (`COL-LIC-002`)
- L3 邮箱与Microsoft 365协作服务 / Microsoft 365 License与订阅支持 / License能力与差异咨询  (`COL-LIC-003`)
- L3 邮箱与Microsoft 365协作服务 / 邮箱账号与基础配置 / 邮箱开通  (`COL-MAIL-001`)
- L3 邮箱与Microsoft 365协作服务 / 邮箱账号与基础配置 / 邮箱停用  (`COL-MAIL-002`)
- L3 邮箱与Microsoft 365协作服务 / 邮箱账号与基础配置 / 邮箱容量调整  (`COL-MAIL-003`)
- L3 邮箱与Microsoft 365协作服务 / Teams与会议协作支持 / Teams访问与基础使用支持  (`COL-MTG-001`)
- L3 邮箱与Microsoft 365协作服务 / Teams与会议协作支持 / Teams会议与账号支持  (`COL-MTG-002`)
- L3 邮箱与Microsoft 365协作服务 / Teams与会议协作支持 / 会前调试支持  (`COL-MTG-003`)
- L3 邮箱与Microsoft 365协作服务 / Teams与会议协作支持 / 会议中技术故障支持  (`COL-MTG-004`)
- L3 邮箱与Microsoft 365协作服务 / 邮件安全与协作安全例外 / 钓鱼邮件举报  (`COL-SEC-001`)
- L3 邮箱与Microsoft 365协作服务 / 邮件安全与协作安全例外 / 邮件误拦截处理  (`COL-SEC-002`)
- L3 邮箱与Microsoft 365协作服务 / 共享资源与协作空间 / 共享邮箱申请  (`COL-SHR-001`)
- L3 邮箱与Microsoft 365协作服务 / 共享资源与协作空间 / 共享日历申请  (`COL-SHR-002`)
- L3 邮箱与Microsoft 365协作服务 / 共享资源与协作空间 / 协作空间权限申请  (`COL-SHR-003`)
- L3 终端与办公支持 / 设备资产生命周期协同 / 设备交付协同  (`EUC-AST-001`)
- L3 终端与办公支持 / 设备资产生命周期协同 / 设备回收协同  (`EUC-AST-002`)
- L3 终端与办公支持 / 设备资产生命周期协同 / 设备调拨协同  (`EUC-AST-003`)
- L3 终端与办公支持 / 桌面系统与办公环境支持 / 操作系统异常  (`EUC-ENV-001`)
- L3 终端与办公支持 / 桌面系统与办公环境支持 / 驱动问题  (`EUC-ENV-002`)
- L3 终端与办公支持 / 桌面系统与办公环境支持 / 本地办公软件异常  (`EUC-ENV-003`)
- L3 终端与办公支持 / 桌面系统与办公环境支持 / 域加入/退域  (`EUC-ENV-004`)
- L3 终端与办公支持 / 桌面系统与办公环境支持 / 补丁更新/回退  (`EUC-ENV-005`)
- L3 终端与办公支持 / 终端硬件与基础故障支持 / 电脑无法开机  (`EUC-HDW-001`)
- L3 终端与办公支持 / 终端硬件与基础故障支持 / 内存升级/更换  (`EUC-HDW-002`)
- L3 终端与办公支持 / 终端硬件与基础故障支持 / 硬盘更换/扩容  (`EUC-HDW-003`)
- L3 终端与办公支持 / 外设与打印支持 / 打印机安装  (`EUC-PRT-001`)
- L3 终端与办公支持 / 外设与打印支持 / 打印异常处理  (`EUC-PRT-002`)
- L3 终端与办公支持 / 外设与打印支持 / 扫描枪支持  (`EUC-PRT-003`)
- L3 终端与办公支持 / 外设与打印支持 / 显示器/扩展坞支持  (`EUC-PRT-004`)
- L3 终端与办公支持 / 终端交付与标准环境 / 新电脑初始化  (`EUC-STD-001`)
- L3 终端与办公支持 / 终端交付与标准环境 / 标准镜像部署  (`EUC-STD-002`)
- L3 终端与办公支持 / 终端交付与标准环境 / 标准软件安装  (`EUC-STD-003`)
- L3 平台与基础设施服务 / 平台访问与基础配置 / 平台级访问开通  (`INF-ACC-001`)
- L3 平台与基础设施服务 / 平台访问与基础配置 / 基础平台访问异常  (`INF-ACC-002`)
- L3 平台与基础设施服务 / 平台访问与基础配置 / 平台配置变更申请  (`INF-ACC-003`)
- L3 平台与基础设施服务 / 平台访问与基础配置 / 基础平台账号支持  (`INF-ACC-004`)
- L3 平台与基础设施服务 / 环境与部署支持 / 测试/生产环境准备申请  (`INF-ENV-001`)
- L3 平台与基础设施服务 / 环境与部署支持 / 发布部署支持申请  (`INF-ENV-002`)
- L3 平台与基础设施服务 / 环境与部署支持 / 环境配置异常支持  (`INF-ENV-003`)
- L3 平台与基础设施服务 / 中间件与数据库平台 / 数据库访问支持  (`INF-MDW-001`)
- L3 平台与基础设施服务 / 中间件与数据库平台 / 中间件服务开通申请  (`INF-MDW-002`)
- L3 平台与基础设施服务 / 中间件与数据库平台 / 中间件/数据库异常  (`INF-MDW-003`)
- L3 平台与基础设施服务 / 服务器与计算资源 / 服务器资源申请  (`INF-SRV-001`)
- L3 平台与基础设施服务 / 服务器与计算资源 / 资源扩容申请  (`INF-SRV-003`)
- L3 平台与基础设施服务 / 服务器与计算资源 / 服务器重启申请  (`INF-SRV-004`)
- L3 平台与基础设施服务 / 存储与备份服务 / 文件共享申请  (`INF-FIL-001`)
- L3 平台与基础设施服务 / 存储与备份服务 / 共享目录权限支持  (`INF-FIL-002`)
- L3 平台与基础设施服务 / 存储与备份服务 / 文件服务访问异常  (`INF-FIL-003`)
- L3 平台与基础设施服务 / 存储与备份服务 / 平台级存储空间申请  (`INF-FIL-004`)
- L3 平台与基础设施服务 / 存储与备份服务 / 备份恢复申请  (`INF-DAT-001`)
- L3 平台与基础设施服务 / 存储与备份服务 / 平台级数据恢复支持  (`INF-DAT-002`)
- L3 平台与基础设施服务 / 存储与备份服务 / 存储挂载/扩容申请  (`INF-DAT-003`)
- L3 平台与基础设施服务 / 存储与备份服务 / 基础平台日志/信息查询  (`INF-DAT-004`)
- L3 平台与基础设施服务 / 虚拟机与云资源 / 虚拟机申请  (`INF-VM-001`)
- L3 平台与基础设施服务 / 虚拟机与云资源 / 虚拟机资源调整申请  (`INF-VM-002`)
- L3 平台与基础设施服务 / 虚拟机与云资源 / 云资源开通申请  (`INF-VM-003`)
- L3 网络与远程访问服务 / 有线与无线网络接入 / 有线网络接入申请  (`NET-ACC-001`)
- L3 网络与远程访问服务 / 有线与无线网络接入 / 无线网络接入申请  (`NET-ACC-002`)
- L3 网络与远程访问服务 / 有线与无线网络接入 / 网络端口开通  (`NET-ACC-003`)
- L3 网络与远程访问服务 / 网络访问控制与放行 / 网络放行申请  (`NET-CTL-001`)
- L3 网络与远程访问服务 / 网络访问控制与放行 / 白名单申请  (`NET-CTL-002`)
- L3 网络与远程访问服务 / 网络访问控制与放行 / 防火墙策略变更申请  (`NET-CTL-003`)
- L3 网络与远程访问服务 / 网络故障与连通性异常 / 网络连接中断故障  (`NET-INC-001`)
- L3 网络与远程访问服务 / 网络故障与连通性异常 / 局部网络访问异常  (`NET-INC-002`)
- L3 网络与远程访问服务 / 网络故障与连通性异常 / 大面积网络故障  (`NET-INC-003`)
- L3 网络与远程访问服务 / VPN与远程连接 / VPN开通申请  (`NET-VPN-001`)
- L3 网络与远程访问服务 / VPN与远程连接 / VPN账号停用  (`NET-VPN-002`)
- L3 网络与远程访问服务 / VPN与远程连接 / VPN权限变更  (`NET-VPN-003`)
- L3 网络与远程访问服务 / VPN与远程连接 / VPN连接异常  (`NET-VPN-004`)
- L3 安全与合规支持 / 合规与审计支持 / 审计资料支持申请  (`SEC-CMP-001`)
- L3 安全与合规支持 / 合规与审计支持 / 合规要求与整改路径咨询  (`SEC-CMP-002`)
- L3 安全与合规支持 / 合规与审计支持 / 权限审计协同申请  (`SEC-CMP-003`)
- L3 安全与合规支持 / 终端与数据安全控制 / DLP策略相关申请  (`SEC-DLP-001`)
- L3 安全与合规支持 / 终端与数据安全控制 / 数据外发限制咨询  (`SEC-DLP-002`)
- L3 安全与合规支持 / 安全事件与应急响应 / 安全事件上报  (`SEC-EMG-001`)
- L3 安全与合规支持 / 安全事件与应急响应 / 终端感染上报  (`SEC-EMG-002`)
- L3 安全与合规支持 / 身份与访问安全控制 / 高风险访问例外申请  (`SEC-IAM-001`)
- L3 安全与合规支持 / 身份与访问安全控制 / 访问安全策略咨询  (`SEC-IAM-002`)
- L3 安全与合规支持 / 安全评估与扫描 / 安全扫描申请  (`SEC-SCN-001`)
- L3 安全与合规支持 / 安全评估与扫描 / 漏洞复扫申请  (`SEC-SCN-002`)
