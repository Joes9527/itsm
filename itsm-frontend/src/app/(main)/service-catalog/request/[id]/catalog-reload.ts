import type { ServiceItem } from '@/types/service-catalog';

export interface IncompatibleCatalogAnswer {
  path: string[];
  label: string;
  value: unknown;
}

const serviceFields = {
  contactName: '联系人', contactEmail: '联系邮箱', quantity: '数量', expectedAt: '期望交付时间',
};
const infrastructureFields = {
  costCenter: '成本中心', dataClassification: '数据分级', needsPublicIp: '需要公网 IP',
  sourceIpWhitelist: '来源 IP 白名单', complianceAck: '合规确认', expireAt: '申请有效期',
};
const hasAnswer = (value: unknown) =>
  value !== undefined && value !== null && value !== '' &&
  (!Array.isArray(value) || value.length > 0);

// Compare the existing Form answers; this does not own a second editable draft.
export function incompatibleCatalogAnswers(
  previous: ServiceItem,
  next: ServiceItem,
  answers: Record<string, any>
): IncompatibleCatalogAnswer[] {
  const incompatible: IncompatibleCatalogAnswer[] = [];
  const add = (path: string[], label: string, value: unknown) => {
    if (hasAnswer(value)) incompatible.push({ path, label, value });
  };
  for (const field of previous.fields || []) {
    const value = answers.customFields?.[field.name];
    const replacement = next.fields?.find(candidate => candidate.name === field.name);
    if (!replacement || replacement.type !== field.type ||
      (replacement.type === 'select' && !replacement.options?.some(option => option.value === value))) {
      add(['customFields', field.name], `${field.label} (${field.name})`, value);
    }
  }
  if (previous.targetClass === 'service_request_item') {
    if (next.targetClass !== 'service_request_item') {
      for (const [name, label] of Object.entries(serviceFields)) add([name], label, answers[name]);
    }
    if (previous.requiresInfraFields &&
      (next.targetClass !== 'service_request_item' || !next.requiresInfraFields)) {
      for (const [name, label] of Object.entries(infrastructureFields)) add([name], label, answers[name]);
    }
  } else if (previous.targetClass !== next.targetClass) {
    const section = previous.targetClass === 'change_request' ? 'change' : previous.targetClass;
    if (section) {
      for (const [name, value] of Object.entries(answers[section] || {})) {
        add([section, name], `${section}.${name}`, value);
      }
    }
    if (next.targetClass === 'service_request_item') add(['ciIds'], '关联配置项 ID', answers.ciIds);
  }
  return incompatible;
}
