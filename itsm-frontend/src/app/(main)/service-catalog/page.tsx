'use client';

import React, { useMemo, useState } from 'react';
import { Breadcrumb, Card, Col, Empty, Input, Row, Space, Spin, Tag, Typography } from 'antd';
import { BookOpen, Search } from 'lucide-react';
import { useServiceCatalogData } from './hooks/useServiceCatalogData';
import { ServiceItemCard } from './components/ServiceItemCard';

const { Title, Text } = Typography;

export default function ServiceCatalogPage() {
  const { catalogs, loading, error, searchText, setSearchText } = useServiceCatalogData();
  const [selectedCategory, setSelectedCategory] = useState<string | null>(null);

  const categories = useMemo(() => {
    const set = new Set<string>();
    catalogs.forEach(c => {
      if (c.category) set.add(String(c.category));
    });
    return Array.from(set);
  }, [catalogs]);

  const filteredCatalogs = useMemo(() => {
    if (!selectedCategory) return catalogs;
    return catalogs.filter(c => String(c.category) === selectedCategory);
  }, [catalogs, selectedCategory]);

  return (
    <div className="max-w-7xl mx-auto p-4 md:p-6">
      <div className="mb-6">
        <Breadcrumb items={[{ title: '首页', href: '/dashboard' }, { title: '服务目录' }]} className="mb-3" />
        <div className="flex items-center justify-between flex-wrap gap-4">
          <div>
            <Title level={3} className="!mb-1">
              <BookOpen className="mr-2 text-blue-500" />
              服务目录
            </Title>
            <Text type="secondary">浏览可申请的服务，选择需要的服务项发起申请</Text>
          </div>
          <Input
            prefix={<Search size={14} />}
            placeholder="搜索服务项..."
            value={searchText}
            onChange={e => setSearchText(e.target.value)}
            style={{ width: 280 }}
            allowClear
          />
        </div>
      </div>

      {categories.length > 0 && (
        <Space wrap className="mb-4">
          <Tag
            color={!selectedCategory ? 'blue' : 'default'}
            style={{ cursor: 'pointer' }}
            onClick={() => setSelectedCategory(null)}
          >
            全部 ({catalogs.length})
          </Tag>
          {categories.map(cat => (
            <Tag
              key={cat}
              color={selectedCategory === cat ? 'blue' : 'default'}
              style={{ cursor: 'pointer' }}
              onClick={() => setSelectedCategory(selectedCategory === cat ? null : cat)}
            >
              {cat} ({catalogs.filter(c => String(c.category) === cat).length})
            </Tag>
          ))}
        </Space>
      )}

      {loading ? (
        <div className="flex justify-center items-center h-64">
          <Spin size="large" tip="加载服务目录..." />
        </div>
      ) : error ? (
        <Empty description={error} />
      ) : filteredCatalogs.length === 0 ? (
        <Empty description={searchText || selectedCategory ? '没有匹配的服务项' : '暂无服务目录数据'} />
      ) : (
        <Row gutter={[16, 16]}>
          {filteredCatalogs.map(catalog => (
            <Col xs={24} sm={12} md={8} lg={6} key={catalog.id}>
              <ServiceItemCard catalog={catalog} />
            </Col>
          ))}
        </Row>
      )}
    </div>
  );
}
