import {
  buildCategoryTree,
  canAddChild,
  collectDescendantIds,
  filterByPath,
  flattenCategoryTree,
  type CategoryTreeInput,
} from '../categoryTreeUtils';

const flat: CategoryTreeInput[] = [
  { id: 1, name: '硬件', parentId: null, sortOrder: 1 },
  { id: 2, name: '软件', parentId: null, sortOrder: 2 },
  { id: 3, name: '服务器', parentId: 1, sortOrder: 1 },
  { id: 4, name: '网络设备', parentId: 1, sortOrder: 2 },
  { id: 5, name: '机架服务器', parentId: 3, sortOrder: 1 },
];

describe('buildCategoryTree', () => {
  it('builds an arbitrary-depth tree from a flat list', () => {
    const tree = buildCategoryTree(flat);
    expect(tree).toHaveLength(2);
    const hardware = tree.find(n => n.id === 1)!;
    expect(hardware.children).toHaveLength(2);
    const server = hardware.children!.find(n => n.id === 3)!;
    expect(server.children).toHaveLength(1);
    expect(server.children![0].id).toBe(5);
  });

  it('sorts siblings by sortOrder', () => {
    const tree = buildCategoryTree(flat);
    expect(tree.map(n => n.id)).toEqual([1, 2]);
    const hardware = tree.find(n => n.id === 1)!;
    expect(hardware.children!.map(n => n.id)).toEqual([3, 4]);
  });

  it('treats orphan parentId as root', () => {
    const orphans: CategoryTreeInput[] = [{ id: 9, name: '孤儿', parentId: 999, sortOrder: 1 }];
    const tree = buildCategoryTree(orphans);
    expect(tree).toHaveLength(1);
    expect(tree[0].id).toBe(9);
  });

  it('omits children key for leaf nodes', () => {
    const tree = buildCategoryTree(flat);
    const software = tree.find(n => n.id === 2)!;
    expect(software.children).toBeUndefined();
  });
});

describe('collectDescendantIds', () => {
  it('collects self and all descendants', () => {
    const ids = collectDescendantIds(flat, 1);
    expect(ids).toEqual(new Set([1, 3, 4, 5]));
  });

  it('returns only self for leaf node', () => {
    const ids = collectDescendantIds(flat, 5);
    expect(ids).toEqual(new Set([5]));
  });
});

describe('flattenCategoryTree', () => {
  it('derives root-to-node paths without inventing levels', () => {
    const rows = flattenCategoryTree(buildCategoryTree(flat));
    const rack = rows.find(row => row.id === 5)!;
    expect(rack.pathIds).toEqual([1, 3, 5]);
    expect(rack.pathLabel).toBe('硬件 / 服务器 / 机架服务器');
  });

  it('keeps every node (including inactive ones) addressable by id', () => {
    const rows = flattenCategoryTree(buildCategoryTree(flat));
    expect(rows.map(row => row.id).sort()).toEqual([1, 2, 3, 4, 5]);
  });
});

describe('canAddChild', () => {
  it('allows children on levels one and two only', () => {
    expect(canAddChild(1)).toBe(true);
    expect(canAddChild(2)).toBe(true);
    expect(canAddChild(3)).toBe(false);
  });

  it('treats a missing level as level one instead of allowing unlimited depth', () => {
    expect(canAddChild(undefined)).toBe(true);
    expect(canAddChild(4)).toBe(false);
  });
});

describe('filterByPath', () => {
  it('matches the full path so duplicate leaf names stay distinguishable', () => {
    const rows = flattenCategoryTree(buildCategoryTree(flat));
    const matched = filterByPath(rows, '硬件 / 服务器');
    expect(matched.map(row => row.id)).toEqual([3, 5]);
  });

  it('matches code and stays case-insensitive', () => {
    const rows = flattenCategoryTree(buildCategoryTree(flat));
    expect(filterByPath(rows, '机架')).toHaveLength(1);
    expect(filterByPath(rows, '')).toHaveLength(rows.length);
  });
});
