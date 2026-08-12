'use client';

import React, { useEffect, useState, useMemo } from 'react';
import { useRouter } from 'next/navigation';
import {
  Card, Input, Tag, Spin, Empty, Typography, Row, Col, Button, Space,
  Breadcrumb, Tree, Tooltip,
} from 'antd';
import type { TreeDataNode } from 'antd';
import {
  Search, FolderOpen, FileText, ArrowRight, BookOpen,
} from 'lucide-react';
import { TicketCategoryApi, type TicketCategory } from '@/lib/api/ticket-category-api';

const { Title, Text, Paragraph } = Typography;

// 域的颜色配置
const domainColors: Record<string, string> = {
  ACC: '#F06820', EUC: '#52c41a', COL: '#722ed1', NET: '#13c2c2',
  INF: '#fa8c16', APP: '#eb2f96', SEC: '#f5222d', ADV: '#8c8c8c',
};

const domainIcons: Record<string, string> = {
  ACC: '👤', EUC: '🖥️', COL: '📧', NET: '🌐',
  INF: '🖧', APP: '📦', SEC: '🛡️', ADV: '💬',
};

interface TreeNode extends TreeDataNode {
  code: string;
  level: number;
  cat: TicketCategory;
  children?: TreeNode[];
}

function buildTree(categories: TicketCategory[]): TreeNode[] {
  const map = new Map<number, TreeNode>();
  const roots: TreeNode[] = [];
  for (const c of categories) {
    map.set(c.id, {
      key: `cat-${c.id}`, title: c.name, code: c.code, level: c.level, cat: c, children: [],
    });
  }
  for (const c of categories) {
    const node = map.get(c.id)!;
    if (c.parentId && map.has(c.parentId)) {
      (map.get(c.parentId)!.children as TreeNode[]).push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
}

export default function ServiceCatalogPage() {
  const router = useRouter();
  const [categories, setCategories] = useState<TicketCategory[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchText, setSearchText] = useState('');
  const [selectedDomain, setSelectedDomain] = useState<string | null>(null);

  useEffect(() => {
    TicketCategoryApi.getCategories({ pageSize: 200 })
      .then(res => {
        const cats = res.categories || res.items || [];
        setCategories(cats);
      })
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  const tree = useMemo(() => buildTree(categories), [categories]);

  // L1 (domains)
  const domains = useMemo(() => categories.filter(c => c.level === 1), [categories]);

  // Filtered service items (L3) by selected domain and search
  const filteredItems = useMemo(() => {
    let items = categories.filter(c => c.level === 3);
    if (selectedDomain) {
      // Find all L3 items under the selected domain
      const domainNode = tree.find(n => n.code === selectedDomain);
      if (domainNode) {
        const collectL3 = (nodes: TreeNode[]): string[] => {
          const codes: string[] = [];
          for (const n of nodes) {
            if (n.level === 3) codes.push(n.code);
            if (n.children) codes.push(...collectL3(n.children as TreeNode[]));
          }
          return codes;
        };
        const domainL3Codes = new Set(collectL3([domainNode]));
        items = items.filter(c => domainL3Codes.has(c.code));
      }
    }
    if (searchText) {
      const q = searchText.toLowerCase();
      items = items.filter(c =>
        c.name.toLowerCase().includes(q) ||
        c.code.toLowerCase().includes(q) ||
        c.description?.toLowerCase().includes(q)
      );
    }
    return items;
  }, [categories, selectedDomain, searchText, tree]);

  // Group L3 items by L2
  const groupedItems = useMemo(() => {
    const map = new Map<number, { l2: TicketCategory; items: TicketCategory[] }>();
    for (const item of filteredItems) {
      const l2 = categories.find(c => c.id === item.parentId);
      if (l2) {
        if (!map.has(l2.id)) map.set(l2.id, { l2, items: [] });
        map.get(l2.id)!.items.push(item);
      }
    }
    return Array.from(map.values()).sort((a, b) => a.l2.sortOrder - b.l2.sortOrder);
  }, [filteredItems, categories]);

  const handleCreateRequest = (code: string, name: string) => {
    router.push(`/tickets/create?category=${encodeURIComponent(code)}&item=${encodeURIComponent(name)}`);
  };

  if (loading) {
    return <div className="flex justify-center items-center h-64"><Spin size="large" tip="加载服务目录..." /></div>;
  }

  return (
    <div className="max-w-7xl mx-auto p-4 md:p-6">
      {/* Header */}
      <div className="mb-6">
        <Breadcrumb items={[{ title: '首页', href: '/' }, { title: '服务目录' }]} className="mb-3" />
        <div className="flex items-center justify-between flex-wrap gap-4">
          <div>
            <Title level={3} className="!mb-1">
              <BookOpen className="mr-2 text-blue-500" />
              服务目录
            </Title>
            <Text type="secondary">Browse service catalog to find the right service item and submit a request</Text>
          </div>
          <Input
            prefix={<Search />}
            placeholder="搜索服务项..."
            value={searchText}
            onChange={e => setSearchText(e.target.value)}
            style={{ width: 280 }}
            allowClear
          />
        </div>
      </div>

      {categories.length === 0 ? (
        <Empty description="暂无服务目录数据" />
      ) : (
        <Row gutter={[16, 16]}>
          {/* Left: Domain navigation */}
          <Col xs={24} md={6} lg={5}>
            <Card size="small" title="服务分类" styles={{ body: { padding: 8 } }}>
              <Space orientation="vertical" style={{ width: '100%' }} size={4}>
                <Tag
                  color={!selectedDomain ? 'blue' : 'default'}
                  style={{ cursor: 'pointer', width: '100%', textAlign: 'center', margin: 0 }}
                  onClick={() => setSelectedDomain(null)}
                >
                  全部 ({categories.filter(c => c.level === 3).length})
                </Tag>
                {domains.map(d => {
                  const count = categories.filter(c =>
                    c.level === 3 && c.code.startsWith(d.code)
                  ).length;
                  return (
                    <Tag
                      key={d.code}
                      color={selectedDomain === d.code ? 'blue' : 'default'}
                      style={{
                        cursor: 'pointer', width: '100%', textAlign: 'center', margin: 0,
                        borderColor: selectedDomain === d.code ? domainColors[d.code] : undefined,
                      }}
                      onClick={() => setSelectedDomain(selectedDomain === d.code ? null : d.code)}
                    >
                      {domainIcons[d.code] || '📋'} {d.name} ({count})
                    </Tag>
                  );
                })}
              </Space>
            </Card>

            {/* Quick stats */}
            <Card size="small" className="mt-3" styles={{ body: { padding: 12 } }}>
              <Row gutter={[8, 8]}>
                <Col span={12}>
                  <div className="text-center">
                    <div className="text-2xl font-bold text-blue-600">{categories.filter(c => c.level === 1).length}</div>
                    <Text type="secondary" className="text-xs">Domains</Text>
                  </div>
                </Col>
                <Col span={12}>
                  <div className="text-center">
                    <div className="text-2xl font-bold text-green-600">{categories.filter(c => c.level === 2).length}</div>
                    <Text type="secondary" className="text-xs">Categories</Text>
                  </div>
                </Col>
                <Col span={24}>
                  <div className="text-center mt-2">
                    <div className="text-2xl font-bold text-purple-600">{categories.filter(c => c.level === 3).length}</div>
                    <Text type="secondary" className="text-xs">Service Items</Text>
                  </div>
                </Col>
              </Row>
            </Card>
          </Col>

          {/* Right: Service items */}
          <Col xs={24} md={18} lg={19}>
            {/* Result count */}
            <div className="mb-3">
              <Tag color="blue">{filteredItems.length} items</Tag>
              {selectedDomain && (
                <Tag closable onClose={() => setSelectedDomain(null)}>
                  {domains.find(d => d.code === selectedDomain)?.name}
                </Tag>
              )}
              {searchText && <Tag closable onClose={() => setSearchText('')}>Search: {searchText}</Tag>}
            </div>

            {groupedItems.length === 0 ? (
              <Empty description={searchText ? 'No matching services found' : 'Select a domain to browse services'} />
            ) : (
              <Space orientation="vertical" size={16} style={{ width: '100%' }}>
                {groupedItems.map(group => (
                  <Card
                    key={group.l2.id}
                    title={
                      <Space>
                        <FolderOpen className="text-blue-500" />
                        <span>{group.l2.name}</span>
                        <Tag>{group.items.length}</Tag>
                      </Space>
                    }
                    size="small"
                    styles={{ body: { padding: 8 } }}
                  >
                    <Row gutter={[8, 8]}>
                      {group.items.map(item => {
                        const domainCode = item.code.split('-')[0];
                        const color = domainColors[domainCode] || '#8c8c8c';
                        return (
                          <Col xs={24} sm={12} md={8} lg={6} key={item.id}>
                            <Card
                              size="small"
                              hoverable
                              className="h-full"
                              style={{ borderLeft: `3px solid ${color}` }}
                              onClick={() => handleCreateRequest(item.code, item.name)}
                            >
                              <div className="flex flex-col h-full">
                                <div className="flex items-center gap-2 mb-1">
                                  <span className="text-lg">{domainIcons[domainCode] || '📋'}</span>
                                  <Text strong className="text-sm flex-1">{item.name}</Text>
                                </div>
                                <Text type="secondary" className="text-xs mb-2 flex-1" ellipsis>
                                  {item.description || 'No description'}
                                </Text>
                                <div className="flex items-center justify-between mt-auto">
                                  <Tag color={color} className="text-xs" style={{ margin: 0 }}>{item.code}</Tag>
                                  <Button type="link" size="small" className="!p-0">
                                    Request <ArrowRight className="w-3 h-3 ml-0.5" />
                                  </Button>
                                </div>
                              </div>
                            </Card>
                          </Col>
                        );
                      })}
                    </Row>
                  </Card>
                ))}
              </Space>
            )}
          </Col>
        </Row>
      )}
    </div>
  );
}
