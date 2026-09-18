import type { TicketCategory } from '@/lib/api/ticket-category-api';

export interface ClassificationOption {
  value: number;
  label: string;
  children?: ClassificationOption[];
}

export function classificationOptions(nodes: TicketCategory[], depth = 0): ClassificationOption[] {
  return nodes.filter(node => node.isActive).map(node => ({
    value: node.id,
    label: node.name,
    ...(depth < 2 && node.children?.length
      ? { children: classificationOptions(node.children, depth + 1) }
      : {}),
  }));
}

export function classificationInput(path?: number[]) {
  if (!path?.length) return undefined;
  if (path.length > 3 || path.some(id => !Number.isInteger(id) || id <= 0)) {
    throw new Error('请选择有效的事件分类');
  }
  const [categoryId, typeId, itemId] = path;
  return { categoryId, ...(typeId ? { typeId } : {}), ...(itemId ? { itemId } : {}) };
}

export function classificationPath(categoryId: number | undefined, nodes: TicketCategory[]): number[] | undefined {
  if (!categoryId) return undefined;
  const index = new Map<number, TicketCategory>();
  const walk = (items: TicketCategory[]) => items.forEach(node => { index.set(node.id, node); walk(node.children || []); });
  walk(nodes);
  const path: number[] = [];
  let id: number | null | undefined = categoryId;
  while (id) {
    const node = index.get(id);
    if (!node || !node.isActive || path.includes(id) || path.length === 3) return undefined;
    path.unshift(id);
    id = node.parentId;
  }
  return path;
}

/**
 * 分类变更载荷：只在用户确实改过分类时携带 categoryId 与必填原因。
 * 后端对专业记录执行同一契约（分类变化必须有原因），因此这里同步阻断，
 * 避免出现"前端显示成功、后端拒绝"的错位。
 */
export function classificationUpdate(path: number[] | undefined, touched: boolean, reason?: string) {
  if (!touched) return {};
  return {
    categoryId: path?.length ? path[path.length - 1] : 0,
    classificationReason: (reason ?? '').trim(),
  };
}
