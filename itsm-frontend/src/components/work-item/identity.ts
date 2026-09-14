/** Identity comes only from the authorized WorkItem projection. */
export function workItemIdentity(dto: { workItemId?: number; number?: string; version?: number }) {
  if (
    !Number.isInteger(dto.workItemId) ||
    !dto.workItemId ||
    dto.workItemId <= 0 ||
    !dto.number?.trim() ||
    !Number.isInteger(dto.version) ||
    !dto.version ||
    dto.version <= 0
  ) {
    throw new Error('WorkItem 身份或版本无效，请刷新详情');
  }
  return { id: dto.workItemId, number: dto.number, version: dto.version };
}
