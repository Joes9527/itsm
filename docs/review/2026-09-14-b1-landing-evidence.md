# B1 分类/资产落位执行证据

- 状态：**EXECUTED**（2026-09-14）。目标：`ga-itsm-20260914 / itsm_ga_ready`，租户 `tenant_id=1`。
- 计划依据：`docs/review/2026-09-14-cti-mapping-worksheet.md`（第 1 项已确认）。
- 生成器：`scripts/migrate_config_seed/generate_b1_sql.py`；数据：`scripts/migrate_config_seed/data/b1_ci_assets.json`。
- 生成 SQL sha256：`6f7e4d2198dea0cf965ad8d965be214651d82112f01ce48b800a64264cbf43c7`；写入前备份：`itsm_ga_ready-pre-b1.pgdump`（sha256 `fb7f2138c4ad22f4bf33b2e41c0bd585e44f8b5cf85be09108c8c26fd758da2e`）。

## 1. 结果计数

| 对象 | 期望 | 实际 |
| --- | ---: | ---: |
| CMDB CI（`business_system`） | 43 | **43** |
| CMDB CI（infra：database/server/network） | 3 | **3** |
| 新建 `ticket_categories` | 3 | **3** |
| `ticket_categories` 合计 | 185 | **185** |

## 2. 完整性校验

- `ci_type` 与 `ci_type_id` 指向的 `ci_types.name` 不一致 = **0**。
- 不同 `attributes.legacyCtiId` 数 = **46**（无重复落位）。
- 幂等复跑新增 = **0**。
- Phase 1 不变量（depts 7975 / users 7862 / roles 36）与账本（36）不变。
- 7 个容器节点未建 CI（层级写入叶子的 `attributes.legacyPath`）；`OA申请` 删除、`test/test2/其他/其他-其他` 排除、17 个 `org_location` 归 Phase 1，均未写入。
- **过程修正（已记录）**：首轮抽取把 markdown 表格竖线带入 43 个 CI 名称（尾部 `" |"`）；已修正数据文件并用条件 UPDATE 就地纠正（`name IS DISTINCT FROM` 幂等），复核 `name like '%|%'` = 0，并新增数据文件回归测试防止复发。

## 3. 新建分类（B1b）

| code | 名称 | 父 code | 目标 id |
| --- | --- | --- | ---: |
| `ACC-LCM-003` | 业务系统账号申请 | `ACC-LCM` | 733 |
| `APP-GEN-SVC-001` | 业务系统服务申请 | `APP-GEN` | 734 |
| `COL-MAIL-004` | 邮箱导出申请 | `COL-MAIL` | 732 |

## 4. 资产落位映射（旧 `legacyCtiId` → 目标 CI，可复用）

| 目标 CI id | ci_type | 名称 | 旧 ctiId | 旧路径 |
| ---: | --- | --- | --- | --- |
| 47 | business_system | BMS系统 | `3fb52530b83c4ba99871d12978ce510a` | BMS系统 |
| 48 | business_system | CL-BI系统 | `d3193d15744647499ccba0c200b62314` | CL-BI系统 |
| 49 | business_system | CSC | `3d30541b8aa546b68af82970dd9416e7` | 企业应用系统 / KAPP / CSC |
| 50 | business_system | CWOS | `bc4881cea3c2491b845aa8c83765fc45` | 企业应用系统 / KWMS南区 / CWOS |
| 51 | business_system | DMS系统 | `3a52c34816a84fd784dead09965f47aa` | 企业应用系统 / IFF / DMS系统 |
| 52 | business_system | Ec-kassist | `1bd1a87f223547389308498cd96cc7be` | 企业应用系统 / KWMS北区 / Ec-kassist |
| 53 | business_system | FICS | `8b250a8452324112a79483740836d926` | 企业应用系统 / KWMS南区 / FICS |
| 54 | business_system | HR-BI | `0084f6701c1642b786d6b84bbaa054ef` | HR-BI |
| 55 | business_system | ITSM | `0941d803f2c84eab9ff58aa639620179` | 企业应用系统 / ITSM |
| 57 | business_system | K35系统 | `2c5c8fdd65ef4dbba6f2858e81036b35` | 企业应用系统 / IFF / K35系统 |
| 56 | business_system | K3.5系统 | `6d09df4b7f574608b85e5e903ae52c6b` | K3.5系统 |
| 58 | business_system | KAPP | `0205eed96e784d249a7d49b724f0cb0b` | 企业应用系统 / KAPP / KAPP |
| 59 | business_system | KAPP&KOMS系统 | `d21b8854d32a40b9905355fe739a60e8` | KAPP&KOMS系统 |
| 60 | business_system | KAPP系统 | `43844d678b9541108a217353ea97e38b` | KAPP系统 |
| 61 | business_system | KBMS结算 | `d68b411584404600a23b07a7ab564cfc` | 企业应用系统 / KAPP / KBMS结算 |
| 62 | business_system | KBMS结算系统 | `be5b5bb33d9c494ea45ba5ade7e39040` | KBMS结算系统 |
| 63 | business_system | KBMS结算系统_ | `8a35110d259d40d39c2cf1a3cc923b48` | KBMS结算系统_ |
| 64 | business_system | KCTS系统 | `d8edfca3745746119e7798b612ec5e0d` | KCTS系统 |
| 65 | business_system | KEAS大数据平台 | `e4a2227811e04f74a3cf052d21ade9ca` | KEAS大数据平台 |
| 80 | business_system | Kflex-U8 | `27e3c6c8780f4a11976c7b17df16dd42` | 企业应用系统 / KWMS南区 / Kflex-U8 |
| 66 | business_system | KOMS | `8ee15341357241efb950ae84c65fcf7d` | 企业应用系统 / KAPP / KOMS |
| 67 | business_system | KOMS主客户实施 | `ea02ecaff4e9438296b5bdb804ddc0c5` | KOMS主客户实施 |
| 81 | business_system | Ksmart系统 | `cdb8ca38cfbb4df38f08b10fb8fe0c93` | Ksmart系统 |
| 82 | business_system | Ksmart系统_ | `9a1c1940a851422fadc3707cb636eac3` | Ksmart系统_ |
| 68 | business_system | KTMS | `f268adeb98c742aa92ebb5a823787906` | 企业应用系统 / KAPP / KTMS |
| 69 | business_system | KTMS系统 | `44b85c79a0f04c59bf48986aea3b0399` | KTMS系统 |
| 70 | business_system | KTMS系统_ | `aa2e0cd25f5b4ef3b6e1363e94a4601a` | KTMS系统_ |
| 74 | business_system | KWMS1.0 | `7b915bfba7984c4b8f3013aebfed4ca3` | 企业应用系统 / KWMS北区 / KWMS1.0 |
| 76 | business_system | KWMS1.0-EDI | `63147a7fe0894204824156b93117c88f` | 企业应用系统 / KWMS南区 / KWMS1.0-EDI |
| 75 | business_system | KWMS1.0-EDI | `23fad2dfeb0e4e3bb563fe8a12ed51ed` | 企业应用系统 / KWMS北区 / KWMS1.0-EDI |
| 72 | business_system | KWMS1.0 & KWMS2.0-新需求 | `1def98b2e738450ba80361a42310aa0d` | 企业应用系统 / KWMS南区 / KWMS1.0 & KWMS2.0-新需求 |
| 71 | business_system | KWMS1.0 & KWMS2.0-新需求 | `44bf58792a02440b92788644f18785cd` | 企业应用系统 / KWMS北区 / KWMS1.0 & KWMS2.0-新需求 |
| 73 | business_system | KWMS1.0 & Local-kassist | `59dadf21ef00448eb5466c94963494fa` | 企业应用系统 / KWMS南区 / KWMS1.0 & Local-kassist |
| 77 | business_system | KWMS365 | `b789f62e6ef447b9af2c07be25f7396c` | 企业应用系统 / KWMS北区 / KWMS365 |
| 78 | business_system | KWMS系统 | `56f61337d0124ea3b02f4632e97814a8` | KWMS系统 |
| 79 | business_system | KWMS系统_ | `f3a3a7cf233a414d8f4b8c2d4f6e3cfa` | KWMS系统_ |
| 83 | business_system | LTL-BI系统 | `2e8509e420b14f24ba3d56e7953a3e22` | LTL-BI系统 |
| 84 | business_system | OA | `4a1f80f59da8482ebed44e34027dedbd` | 企业应用系统 / Back Office / OA |
| 85 | business_system | OA系统 | `d75f90ea58824c76be4dd82efb09cee6` | OA系统 |
| 86 | business_system | 本地系统-KTLS | `0a2e992ba27542e8bc9d6d6f979f09af` | 企业应用系统 / KWMS北区 / 本地系统-KTLS |
| 87 | business_system | 质量报案系统 | `6067201856934723869490de5282faf0` | 企业应用系统 / Back Office / 质量报案系统 |
| 88 | business_system | 运维支持 | `eec83381511a40aab0f24b9be5d2bf83` | 企业应用系统 / KAPP / 运维支持 |
| 89 | business_system | 邮件系统 | `34b675b5b03546ac9244bce4652bd9a6` | 企业应用系统 / 邮件系统 |
| 90 | database | 数据库 | `b2b9ef80b06b4ae499a1b87a9941868f` | 基础架构 / 数据库 |
| 91 | network | 网络 | `40c5ee732d1b425293ee2cbe1062b3f4` | 基础架构 / 网络 |
| 92 | server | 服务器 | `4a0ece2db0004d258a35488480d696d0` | 基础架构 / 服务器 |
