/**
 * 工单分类树工具函数
 */

export interface CategoryTreeInput {
  id: number;
  name: string;
  parentId: number | null;
  sortOrder: number;
  children?: CategoryTreeInput[];
}

/** 将扁平分类列表组装为任意层级的树 */
export function buildCategoryTree<T extends CategoryTreeInput>(list: T[]): T[] {
  const map = new Map<number, T>();
  list.forEach(item => map.set(item.id, { ...item, children: [] }));
  const roots: T[] = [];
  map.forEach(item => {
    if (item.parentId && map.has(item.parentId)) {
      (map.get(item.parentId)!.children as T[]).push(item);
    } else {
      roots.push(item);
    }
  });
  const sortRec = (nodes: T[]) => {
    nodes.sort((a, b) => a.sortOrder - b.sortOrder || a.name.localeCompare(b.name));
    nodes.forEach(n => {
      if (n.children && n.children.length > 0) sortRec(n.children as T[]);
      else delete n.children;
    });
  };
  sortRec(roots);
  return roots;
}

/** 收集某分类的全部后代 id（含自身），用于父级选择时禁用，防止形成环 */
export function collectDescendantIds(
  list: { id: number; parentId: number | null }[],
  rootId: number
): Set<number> {
  const childrenMap = new Map<number, number[]>();
  list.forEach(item => {
    if (item.parentId) {
      const arr = childrenMap.get(item.parentId) || [];
      arr.push(item.id);
      childrenMap.set(item.parentId, arr);
    }
  });
  const result = new Set<number>([rootId]);
  const stack = [rootId];
  while (stack.length > 0) {
    const current = stack.pop()!;
    (childrenMap.get(current) || []).forEach(childId => {
      if (!result.has(childId)) {
        result.add(childId);
        stack.push(childId);
      }
    });
  }
  return result;
}

/** 分类树的最大层级（Category → Type → Item）。 */
export const CTI_MAX_LEVEL = 3;
/** 兼容既有命名。 */
export const CATEGORY_MAX_LEVEL = CTI_MAX_LEVEL;

/**
 * 把分类树摊平成带完整路径的一维列表，供维护界面的路径搜索与选择使用。
 * 路径以分类树为准，不依赖后端是否回填 path 字段。
 */
export function flattenCategoryTree<T extends CategoryTreeInput>(
  nodes: T[] | undefined,
  parents: T[] = []
): (T & { pathIds: number[]; pathLabel: string })[] {
  const result: (T & { pathIds: number[]; pathLabel: string })[] = [];
  (nodes ?? []).forEach(node => {
    const chain = [...parents, node];
    const pathIds = chain.map(item => item.id);
    result.push({ ...node, pathIds, pathLabel: chain.map(item => item.name).join(' / ') });
    if (node.children && node.children.length > 0) {
      result.push(...flattenCategoryTree(node.children as T[], chain));
    }
  });
  return result;
}

/** 节点是否仍可新增下级（第三级不提供）。 */
export function canAddChild(level: number | undefined, maxLevel = CATEGORY_MAX_LEVEL): boolean {
  return (level ?? 1) < maxLevel;
}

/** 按完整路径做大小写不敏感过滤，命中节点返回自身而非根。 */
export function filterByPath<T extends { pathLabel?: string; name: string; code?: string }>(
  items: T[],
  keyword: string
): T[] {
  const needle = keyword.trim().toLowerCase();
  if (!needle) return items;
  return items.filter(item => {
    const haystack = `${item.pathLabel ?? item.name} ${item.name} ${item.code ?? ''}`.toLowerCase();
    return haystack.includes(needle);
  });
}
