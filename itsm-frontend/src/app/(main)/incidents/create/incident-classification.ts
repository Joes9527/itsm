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
