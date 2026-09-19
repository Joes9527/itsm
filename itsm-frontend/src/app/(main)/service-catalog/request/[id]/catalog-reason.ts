import type { ServiceItem } from '@/types/service-catalog';

// 目录自定义字段中承担「申请理由」语义的字段名。
// 判定只看字段名：label 是展示文案，会随语言和措辞变化，不能作为契约依据。
// 该约定与后端 `CreateServiceRequestRequest.reason`（工单描述来源）互斥：
// 目录已经问过理由时，申请页不再重复询问，理由的唯一归属是目录字段。
const REASON_FIELD = /(^|_)reason$/;

export function catalogDeclaresReason(fields: ServiceItem['fields']): boolean {
  return (fields || []).some(field => REASON_FIELD.test(field.name));
}
