'use client';

import React, { useMemo } from 'react';
import { Tree, Empty } from 'antd';
import type { DataNode } from 'antd/es/tree';
import { Folder, FileText } from 'lucide-react';
import type { Department } from '@/lib/services/department-service';

// 虚拟根节点的 key：不对应任何真实部门记录，选中它等价于"清空选中"（回退到全量）。
export const ORG_TREE_ROOT_KEY = '__org_root__';

function buildTreeNodes(depts: Department[]): DataNode[] {
  return depts.map(dept => {
    const children = dept.children && dept.children.length > 0 ? buildTreeNodes(dept.children) : undefined;
    return {
      key: dept.id,
      title: dept.name,
      icon: children ? (
        <Folder size={14} className="text-amber-500" />
      ) : (
        <FileText size={14} className="text-gray-400" />
      ),
      children,
    };
  });
}

export interface DeptTreeSelectNode {
  value: number;
  title: string;
  children?: DeptTreeSelectNode[];
}

// 供 TreeSelect（如新建/编辑表单里选择所属部门）复用的节点结构，跟左侧展示树共享同一份
// 部门数据，避免在每个用到"选部门"的表单里各自维护一套 map 逻辑。
export function buildDeptTreeSelectData(depts: Department[]): DeptTreeSelectNode[] {
  return depts.map(dept => ({
    value: dept.id,
    title: dept.name,
    children: dept.children && dept.children.length > 0 ? buildDeptTreeSelectData(dept.children) : undefined,
  }));
}

export function findDepartmentById(depts: Department[], id: number): Department | null {
  for (const d of depts) {
    if (d.id === id) return d;
    if (d.children) {
      const found = findDepartmentById(d.children, id);
      if (found) return found;
    }
  }
  return null;
}

interface OrgDepartmentTreeProps {
  departments: Department[];
  selectedId: number | null; // null = 选中"组织架构"根节点（代表全量）
  onSelect: (dept: Department | null) => void;
  height?: number;
}

// 组织架构左侧树——部门管理页与用户管理页共用同一套树形展示逻辑，只是右侧联动的
// 数据不同（部门明细 vs 该部门下的用户）。真实导入的组织数据有 143 个互不隶属的
// 顶层部门，统一包在一个"组织架构"虚拟根节点下展示。
export default function OrgDepartmentTree({
  departments,
  selectedId,
  onSelect,
  height = 520,
}: OrgDepartmentTreeProps) {
  const orgTree: DataNode[] = useMemo(
    () => [
      {
        key: ORG_TREE_ROOT_KEY,
        title: '组织架构',
        icon: <Folder size={14} className="text-amber-500" />,
        children: buildTreeNodes(departments),
      },
    ],
    [departments]
  );

  if (departments.length === 0) {
    return <Empty description="暂无组织树" />;
  }

  return (
    <Tree
      showIcon
      showLine={{ showLeafIcon: false }}
      height={height}
      treeData={orgTree}
      defaultExpandedKeys={[ORG_TREE_ROOT_KEY]}
      selectedKeys={[selectedId ?? ORG_TREE_ROOT_KEY]}
      onSelect={keys => {
        const val = keys[0];
        if (val === undefined || val === ORG_TREE_ROOT_KEY) {
          onSelect(null);
          return;
        }
        onSelect(findDepartmentById(departments, val as number));
      }}
    />
  );
}
