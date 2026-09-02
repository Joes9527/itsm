/**
 * ITSM 多角色工作台与视图映射规范配置
 * 遵循 docs/superpowers/specs/2026-08-20-multi-persona-ui-and-ai-integration-design.md
 */

export type PersonaType = 'portal' | 'workspace' | 'manager' | 'executive' | 'admin';

export interface PersonaDefinition {
  type: PersonaType;
  name: string;
  description: string;
  homePath: string;
  layoutType: 'portal' | 'console';
  icon: string;
}

export const PERSONAS: Record<PersonaType, PersonaDefinition> = {
  portal: {
    type: 'portal',
    name: '员工自服务门户',
    description: '快速提单、服务目录申请与进度追踪',
    homePath: '/portal',
    layoutType: 'portal',
    icon: 'Sparkles',
  },
  workspace: {
    type: 'workspace',
    name: '工程师协同工作台',
    description: '三栏分屏工单处理、SLA 监控与排障辅助',
    homePath: '/workspace/tickets',
    layoutType: 'console',
    icon: 'Terminal',
  },
  manager: {
    type: 'manager',
    name: '服务台运营中心',
    description: '实时进单监控墙、团队负载调度与滞留催办',
    homePath: '/manager/live-board',
    layoutType: 'console',
    icon: 'Activity',
  },
  executive: {
    type: 'executive',
    name: '战略决策驾驶舱',
    description: 'MTTR 趋势分析、服务指标与 IT 资源洞察',
    homePath: '/executive/dashboard',
    layoutType: 'console',
    icon: 'TrendingUp',
  },
  admin: {
    type: 'admin',
    name: '系统管理后台',
    description: 'BPMN 流程、组织权限与系统底层配置',
    homePath: '/admin/overview',
    layoutType: 'console',
    icon: 'Shield',
  },
};

export interface RoleRouteConfig {
  roleCode: string;
  roleName: string;
  defaultPersona: PersonaType;
  allowedPersonas: PersonaType[];
}

/**
 * 20 个系统预置角色与允许访问的工作台视图映射
 */
export const ROLE_ROUTE_MAPPING: Record<string, RoleRouteConfig> = {
  // 1. 终端用户与部门审批人
  end_user: {
    roleCode: 'end_user',
    roleName: '普通用户',
    defaultPersona: 'portal',
    allowedPersonas: ['portal'],
  },
  user: {
    roleCode: 'user',
    roleName: '普通用户',
    defaultPersona: 'portal',
    allowedPersonas: ['portal'],
  },
  guest: {
    roleCode: 'guest',
    roleName: '访客',
    defaultPersona: 'portal',
    allowedPersonas: ['portal'],
  },
  dept_manager: {
    roleCode: 'dept_manager',
    roleName: '部门经理',
    defaultPersona: 'portal',
    allowedPersonas: ['portal'],
  },
  team_lead: {
    roleCode: 'team_lead',
    roleName: '团队主管',
    defaultPersona: 'portal',
    allowedPersonas: ['portal'],
  },

  // 2. 技术支持与运维一线二线
  l1_support: {
    roleCode: 'l1_support',
    roleName: '一线工程师',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },
  l2_support: {
    roleCode: 'l2_support',
    roleName: '二线工程师',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },
  l3_expert: {
    roleCode: 'l3_expert',
    roleName: '三线专家',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },
  agent: {
    roleCode: 'agent',
    roleName: '服务台客服',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },
  technician: {
    roleCode: 'technician',
    roleName: '技术专员',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },
  ops_engineer: {
    roleCode: 'ops_engineer',
    roleName: '运维工程师',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },
  dba: {
    roleCode: 'dba',
    roleName: 'DBA工程师',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },
  network_eng: {
    roleCode: 'network_eng',
    roleName: '网络工程师',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },
  developer: {
    roleCode: 'developer',
    roleName: '开发工程师',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },
  qa_engineer: {
    roleCode: 'qa_engineer',
    roleName: '测试工程师',
    defaultPersona: 'workspace',
    allowedPersonas: ['portal', 'workspace'],
  },

  // 3. 服务台主管与运营经理
  sd_manager: {
    roleCode: 'sd_manager',
    roleName: '服务台主管',
    defaultPersona: 'manager',
    allowedPersonas: ['portal', 'workspace', 'manager'],
  },
  ops_manager: {
    roleCode: 'ops_manager',
    roleName: '运维经理',
    defaultPersona: 'manager',
    allowedPersonas: ['portal', 'workspace', 'manager'],
  },
  rd_manager: {
    roleCode: 'rd_manager',
    roleName: '研发经理',
    defaultPersona: 'manager',
    allowedPersonas: ['portal', 'workspace', 'manager'],
  },
  manager: {
    roleCode: 'manager',
    roleName: '主管',
    defaultPersona: 'manager',
    allowedPersonas: ['portal', 'workspace', 'manager'],
  },

  // 4. 管理层与 IT 总监
  it_director: {
    roleCode: 'it_director',
    roleName: 'IT总监',
    defaultPersona: 'executive',
    allowedPersonas: ['portal', 'workspace', 'manager', 'executive', 'admin'],
  },
  ops_director: {
    roleCode: 'ops_director',
    roleName: '运维总监',
    defaultPersona: 'executive',
    allowedPersonas: ['portal', 'workspace', 'manager', 'executive', 'admin'],
  },

  // 5. 系统管理员与审计管理员
  sysadmin: {
    roleCode: 'sysadmin',
    roleName: '系统管理员',
    defaultPersona: 'admin',
    allowedPersonas: ['portal', 'workspace', 'manager', 'executive', 'admin'],
  },
  admin: {
    roleCode: 'admin',
    roleName: '管理员',
    defaultPersona: 'admin',
    allowedPersonas: ['portal', 'workspace', 'manager', 'executive', 'admin'],
  },
  super_admin: {
    roleCode: 'super_admin',
    roleName: '超级管理员',
    defaultPersona: 'admin',
    allowedPersonas: ['portal', 'workspace', 'manager', 'executive', 'admin'],
  },
  security_admin: {
    roleCode: 'security_admin',
    roleName: '安全管理员',
    defaultPersona: 'admin',
    allowedPersonas: ['portal', 'workspace', 'manager', 'executive', 'admin'],
  },
  audit_admin: {
    roleCode: 'audit_admin',
    roleName: '审计管理员',
    defaultPersona: 'admin',
    allowedPersonas: ['portal', 'workspace', 'manager', 'executive', 'admin'],
  },
};

/**
 * 根据用户角色获取其默认工作台配置
 */
export function getRolePersonaConfig(roleCode?: string): RoleRouteConfig {
  if (!roleCode || !ROLE_ROUTE_MAPPING[roleCode]) {
    return ROLE_ROUTE_MAPPING.end_user;
  }
  return ROLE_ROUTE_MAPPING[roleCode];
}

/**
 * Resolve the single canonical landing page for a role.
 */
export function getDefaultHomePath(roleCode?: string): string {
  const roleConfig = getRolePersonaConfig(roleCode);
  return PERSONAS[roleConfig.defaultPersona].homePath;
}

/**
 * 判断当前路径属于哪种工作台视图类型
 */
export function getPersonaByPath(pathname: string): PersonaType {
  if (pathname.startsWith('/portal')) return 'portal';
  if (pathname.startsWith('/workspace')) return 'workspace';
  if (pathname.startsWith('/manager')) return 'manager';
  if (pathname.startsWith('/executive')) return 'executive';
  if (pathname.startsWith('/admin')) return 'admin';
  return 'portal';
}
