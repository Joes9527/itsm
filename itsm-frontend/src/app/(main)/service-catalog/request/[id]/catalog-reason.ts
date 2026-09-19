import type { ServiceItem } from '@/types/service-catalog';

// 目录自定义字段中承担「申请理由」语义的字段名。
// 判定只看字段名：label 是展示文案，会随语言和措辞变化，不能作为契约依据。
// 该约定与后端 `CreateServiceRequestRequest.reason`（工单描述来源）互斥：
// 目录已经问过理由时，申请页不再重复询问，理由的唯一归属是目录字段。
const REASON_FIELD = /(^|_)reason$/;

export function catalogReasonField(fields: ServiceItem['fields']) {
  return (fields || []).find(field => REASON_FIELD.test(field.name));
}

export function catalogDeclaresReason(fields: ServiceItem['fields']): boolean {
  return catalogReasonField(fields) !== undefined;
}

// 只有目录把该字段声明为必填，理由的唯一归属才是它——申请页不再重复询问。
// 目录字段可选时保留硬编码的必填「申请理由」：这一角重复问一次，
// 好过两个理由都不填也能提交（工单描述为空），而服务端 reason 本身是选填的，
// 前端此前是唯一一道必填约束。
export function catalogOwnsRequiredReason(fields: ServiceItem['fields']): boolean {
  return Boolean(catalogReasonField(fields)?.required);
}
