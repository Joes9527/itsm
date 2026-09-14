# 旧 ITSM vs 新 ITSM 迁移库 差异报告

## 摘要

```json
{
  "classification": {
    "matched": 0,
    "only_legacy": [
      "ad账户申请windows账户",
      "backoffice",
      "bms系统",
      "clbi系统",
      "csc",
      "cwos",
      "dms系统",
      "eckassist",
      "fics",
      "hrbi",
      "... (+63 more)"
    ],
    "only_new": [
      "ad与基础账号",
      "ad账号停用",
      "ad账号新建",
      "dlp策略相关申请",
      "iff&backoffice业务处理中断",
      "iff&backoffice业务数据查询申请",
      "iff&backoffice业务线功能咨询",
      "iff&backoffice业务线支持",
      "iff&backoffice业务线角色权限申请",
      "iff&backoffice主数据变更申请",
      "... (+172 more)"
    ],
    "coverage": 0.0
  },
  "dictionary": {
    "matched": 6,
    "only_legacy": [
      "01结束",
      "02跟进中",
      "03停滞",
      "aectryadmin",
      "aenormal",
      "aestationadmin",
      "aesuper",
      "aeview",
      "aictryadmin",
      "ainormal",
      "... (+785 more)"
    ],
    "only_new": [
      "1个月",
      "1年",
      "30天临时",
      "3个月",
      "6个月",
      "90天临时",
      "a",
      "ad账号",
      "b",
      "c",
      "... (+81 more)"
    ],
    "coverage": 0.0075
  },
  "sla": {
    "legacy_levels": 15,
    "legacy_matrix": 86,
    "new_slas": 14,
    "direct_name_match": 0
  },
  "process": {
    "matched": 0,
    "only_legacy": [
      "childprotest",
      "copyofchildprotest",
      "copyof请求工单000",
      "copyof请求工单流程",
      "fg",
      "事件002",
      "事件工单",
      "事件流程001",
      "包含子流程",
      "参数子流程",
      "... (+15 more)"
    ],
    "only_new": [
      "changenormalflowcn",
      "cloudprivateopsflow",
      "cloudpublicopsflow",
      "cloudsecurityscanflow",
      "copilot采购申请审批流程",
      "e2e测试新服务请求流程",
      "incidentemergencyflowcn",
      "incidentemergencyflowv11",
      "problemmanagementflowcn",
      "releaseapprovalflowcn",
      "... (+19 more)"
    ],
    "coverage": 0.0
  },
  "routing": {
    "legacy_authorized": 720,
    "new_assignment_rules": 0
  },
  "modules": {
    "matched": 0,
    "only_legacy": [
      "事件管理",
      "服务台",
      "知识管理",
      "请求管理"
    ],
    "only_new": [
      "ddl执行",
      "gitlab代码仓库申请",
      "k8s扩缩容",
      "其他工单",
      "域名申请",
      "应用申请",
      "数据导出",
      "数据库账号申请",
      "虚拟机申请",
      "账号申请",
      "... (+2 more)"
    ],
    "coverage": 0.0,
    "legacy_modules": 4,
    "new_ticket_types": 12
  }
}
```

> 摘要中长清单仅保留前 10 条样本；完整清单见同名 `.json` 文件。

## 1. 分类口径（CTI vs ticket_categories）

旧 CTI 73 个名称 vs 新 182 个名称；名称覆盖率 0.0%，仅旧 73，仅新 182。旧树最大层级 3，孤儿节点 0。

| 指标 | 值 |
| 旧节点数 | 82 |
| 新分类数 | 183 |
| 名称匹配 | 0 |
| 覆盖率 | 0.0% |
| 仅旧 | 73 |
| 仅新 | 182 |

## 2. 配置字典（configDictionaries vs field_definitions/form_fields）

旧字典 988 项 / 183 组；新字段选项 label 97 个。label 覆盖率 0.8%（匹配 6，仅旧 795）。

| 字典组 | 旧选项数 |
| <ungrouped> | 38 |
| K35权限 | 37 |
| 请求管理 | 29 |
| 公共字典 | 28 |
| 行业 | 22 |
| 请求动作按钮 | 21 |
| 事件动作按钮 | 20 |
| 变更动作按钮 | 18 |
| 请求类型 | 18 |
| 问题动作按钮 | 15 |
| 语言包词条管理(Manage All) | 15 |
| 系统名称 | 14 |
| 事件管理 | 13 |
| 缺陷模块 | 12 |
| 需求权限 | 11 |

## 3. 优先级 / SLA 与优先级矩阵

旧优先级 15 条 / 矩阵 86 条；新 SLA 14 条。按名称直接匹配 0/15，命名体系不同（旧 P0–P3 / 新 urgent/high/medium/low），需映射表。

| 优先级 | 响应(分) | 处理(分) | 颜色 | 新版 SLA 匹配 |
| P0 | 60 | 480 | #f6383a | — |
| P1 | 60 | 960 | #ffeb3b | — |
| P1 | 20 | 480 | #ffc107 | — |
| P1 | 30 | 480 | #ffc107 | — |
| P3 | 60 | 1440 | #4caf50 | — |
| P2 | 120 | 1440 | #0894ec | — |
| P2 | 60 | 480 | #3f51b5 | — |
| P2 | 30 | 720 | #4cd964 | — |
| P3 | 60 | 1920 | #5eb95e | — |
| P2 | 60 | 1440 | #2196f3 | — |
| P0 | 30 | 240 | #f6383a | — |
| P3 | 120 | 1920 | #4caf50 | — |
| P1 | 60 | 960 | #ffc107 | — |
| P0 | 10 | 120 | #f6383a | — |
| P0 | 60 | 480 | #f6383a | — |

## 4. 流程（process-definition/model vs process_* 表)

旧流程定义 145 + 模型 24；新定义 68、部署 41、绑定 28、实例 39。名称覆盖率 0.0%。

| 指标 | 旧 | 新 |
| 定义数 | 145 | 68 |
| 模型/部署 | 24 | 41 |
| 绑定 | — | 28 |
| 实例 | — | 39 |
| 名称匹配 | 0 |  |

## 5. CTI 路由（ruleCtiauthorized vs ticket_assignment_rules）

旧 CTI 授权 720 条；新派单规则 0 条。旧规则为 (ctiId → authorizedType/authorizedId) 授权模型，新系统为声明式 assignment_rules，需逐条映射。

| 指标 | 值 |
| 旧授权规则 | 720 |
| 新派单规则 | 0 |

## 6. ITIL 模块（configModule vs ticket_types）

旧 ITIL 模块 4（事件管理, 服务台, 知识管理, 请求管理）；新 ticket_types 12（ddl执行, gitlab代码仓库申请, k8s扩缩容, 其他工单, 域名申请, 应用申请, 数据导出, 数据库账号申请, 虚拟机申请, 账号申请, 防火墙规则申请, 项目申请），名称匹配 0。旧侧为 ITIL 流程模块（事件/请求/问题/变更/知识/服务台），新侧为具体服务类型编码，语义层不同，不能按名称直接对齐。

| 旧模块 | 旧 moduleCode |
| 事件管理 | IN |
| 请求管理 | SR |
| 知识管理 | KN |
| 服务台 | SERVER |
