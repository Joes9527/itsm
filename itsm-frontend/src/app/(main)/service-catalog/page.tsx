'use client';

import React, { useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import { Button, Card, Empty, Input, Select, Space, Spin, Typography } from 'antd';
import {
  Bell,
  BookOpen,
  Clock3,
  Filter,
  LayoutGrid,
  Search,
  Table,
  Zap,
} from 'lucide-react';
import { useServiceCatalogData } from './hooks/useServiceCatalogData';
import { ServiceItemCard } from './components/ServiceItemCard';

const { Title, Text } = Typography;

type ViewMode = 'grid' | 'list';
type ApprovalFilter = 'all' | 'no_approval' | 'need_approval';
type SortBy = 'default' | 'name' | 'sla';

const getDeliveryHours = (resolutionTime?: number, responseTime?: number) =>
  resolutionTime ?? responseTime;

export default function ServiceCatalogPage() {
  const listSectionRef = useRef<HTMLDivElement>(null);
  const { catalogs, loading, error, searchText, setSearchText } = useServiceCatalogData();
  const [selectedCategory, setSelectedCategory] = useState<string | null>(null);
  const [viewMode, setViewMode] = useState<ViewMode>('grid');
  const [approvalFilter, setApprovalFilter] = useState<ApprovalFilter>('all');
  const [sortBy, setSortBy] = useState<SortBy>('default');
  const [showAdvancedFilters, setShowAdvancedFilters] = useState(false);

  const categories = useMemo(() => {
    const values = new Set<string>();
    catalogs.forEach(catalog => {
      if (catalog.category) {
        values.add(String(catalog.category));
      }
    });
    return Array.from(values);
  }, [catalogs]);

  const pageStats = useMemo(() => {
    const noApproval = catalogs.filter(catalog => catalog.requiresApproval === false).length;
    const quickDelivery = catalogs.filter(catalog => {
      const hours = getDeliveryHours(
        catalog.availability?.resolutionTime,
        catalog.availability?.responseTime
      );
      return typeof hours === 'number' && hours <= 8;
    }).length;

    return {
      total: catalogs.length,
      noApproval,
      needApproval: catalogs.length - noApproval,
      quickDelivery,
    };
  }, [catalogs]);

  const statsReady = !loading && !error;

  const filteredAndSortedCatalogs = useMemo(() => {
    let result = [...catalogs];

    if (selectedCategory) {
      result = result.filter(catalog => String(catalog.category) === selectedCategory);
    }

    if (approvalFilter === 'no_approval') {
      result = result.filter(catalog => catalog.requiresApproval === false);
    } else if (approvalFilter === 'need_approval') {
      result = result.filter(catalog => catalog.requiresApproval !== false);
    }

    if (sortBy === 'name') {
      result.sort((a, b) => (a.name || '').localeCompare(b.name || ''));
    } else if (sortBy === 'sla') {
      result.sort((a, b) => {
        const hoursA = getDeliveryHours(
          a.availability?.resolutionTime,
          a.availability?.responseTime
        );
        const hoursB = getDeliveryHours(
          b.availability?.resolutionTime,
          b.availability?.responseTime
        );
        return (hoursA ?? Number.MAX_SAFE_INTEGER) - (hoursB ?? Number.MAX_SAFE_INTEGER);
      });
    }

    return result;
  }, [approvalFilter, catalogs, selectedCategory, sortBy]);

  const resetFilters = () => {
    setSearchText('');
    setSelectedCategory(null);
    setApprovalFilter('all');
    setSortBy('default');
  };

  const jumpToCatalogList = () => {
    listSectionRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  return (
    <div className="min-h-screen bg-gray-50">
      <div className="bg-white border-b border-gray-200">
        <div className="w-full px-6 py-4">
          <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
            <div className="min-w-0">
              <div className="flex items-start gap-3">
                <div className="mt-1 flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-blue-50 text-blue-600">
                  <BookOpen size={20} />
                </div>
                <div className="min-w-0">
                  <Title level={2} style={{ marginBottom: 0 }}>
                    服务目录
                  </Title>
                  <Text type="secondary">
                    标准化自助申请入口，支持审批、SLA 与交付状态跟踪
                  </Text>
                </div>
              </div>
            </div>

            <Space wrap>
              <Button
                icon={<Filter size={16} />}
                onClick={() => setShowAdvancedFilters(current => !current)}
                aria-expanded={showAdvancedFilters}
              >
                高级筛选
              </Button>
              <Link href="/approvals/pending">
                <Button icon={<Bell size={16} />}>我的审批</Button>
              </Link>
              <Button type="primary" icon={<Zap size={16} />} onClick={jumpToCatalogList}>
                发起申请
              </Button>
            </Space>
          </div>

          <div className="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
            <Card size="small" className="rounded-lg shadow-sm">
              <div className="flex items-center justify-between gap-4">
                <div>
                  <Text type="secondary">已发布服务</Text>
                  <div className="text-2xl font-bold text-blue-600">
                    {statsReady ? pageStats.total : '--'}
                  </div>
                </div>
                <BookOpen className="text-blue-500" />
              </div>
            </Card>
            <Card size="small" className="rounded-lg shadow-sm">
              <div className="flex items-center justify-between gap-4">
                <div>
                  <Text type="secondary">免审批服务</Text>
                  <div className="text-2xl font-bold text-emerald-600">
                    {statsReady ? pageStats.noApproval : '--'}
                  </div>
                </div>
                <Zap className="text-emerald-500" />
              </div>
            </Card>
            <Card size="small" className="rounded-lg shadow-sm">
              <div className="flex items-center justify-between gap-4">
                <div>
                  <Text type="secondary">需审批服务</Text>
                  <div className="text-2xl font-bold text-orange-500">
                    {statsReady ? pageStats.needApproval : '--'}
                  </div>
                </div>
                <Bell className="text-orange-500" />
              </div>
            </Card>
            <Card size="small" className="rounded-lg shadow-sm">
              <div className="flex items-center justify-between gap-4">
                <div>
                  <Text type="secondary">快速交付</Text>
                  <div className="text-2xl font-bold text-violet-600">
                    {statsReady ? pageStats.quickDelivery : '--'}
                  </div>
                </div>
                <Clock3 className="text-violet-500" />
              </div>
            </Card>
          </div>
        </div>
      </div>

      {showAdvancedFilters && (
        <div className="bg-gray-50 border-b border-gray-200">
          <div className="w-full px-6 py-4">
            <Card size="small" className="rounded-lg shadow-sm">
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
                <div>
                  <Text className="mb-2 block text-sm font-medium text-slate-700">审批类型</Text>
                  <Select
                    value={approvalFilter}
                    onChange={value => setApprovalFilter(value)}
                    className="w-full"
                    options={[
                      { label: '全部审批类型', value: 'all' },
                      { label: '免审批服务', value: 'no_approval' },
                      { label: '需审批服务', value: 'need_approval' },
                    ]}
                  />
                </div>
                <div>
                  <Text className="mb-2 block text-sm font-medium text-slate-700">排序方式</Text>
                  <Select
                    value={sortBy}
                    onChange={value => setSortBy(value)}
                    className="w-full"
                    options={[
                      { label: '默认排序', value: 'default' },
                      { label: '按名称 A-Z', value: 'name' },
                      { label: '按交付 SLA', value: 'sla' },
                    ]}
                  />
                </div>
                <div className="flex items-end">
                  <Button onClick={resetFilters}>重置筛选条件</Button>
                </div>
              </div>
            </Card>
          </div>
        </div>
      )}

      <div ref={listSectionRef} className="w-full px-6 py-6">
        <Card className="rounded-xl shadow-sm">
          <div className="mb-4 flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
            <div className="w-full lg:max-w-xl">
              <Input
                size="large"
                prefix={<Search size={16} className="text-slate-400" />}
                placeholder="搜索服务名称、描述、关键字..."
                value={searchText}
                onChange={event => setSearchText(event.target.value)}
                allowClear
              />
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <Button
                type={viewMode === 'grid' ? 'primary' : 'default'}
                icon={<LayoutGrid size={16} />}
                onClick={() => setViewMode('grid')}
                aria-pressed={viewMode === 'grid'}
              >
                卡片视图
              </Button>
              <Button
                type={viewMode === 'list' ? 'primary' : 'default'}
                icon={<Table size={16} />}
                onClick={() => setViewMode('list')}
                aria-pressed={viewMode === 'list'}
              >
                列表视图
              </Button>
            </div>
          </div>

          <div className="mb-4 flex flex-wrap items-center gap-2">
            <Button
              type={!selectedCategory ? 'primary' : 'default'}
              onClick={() => setSelectedCategory(null)}
              aria-pressed={!selectedCategory}
            >
              全部
            </Button>
            {categories.map(category => {
              const count = catalogs.filter(catalog => String(catalog.category) === category).length;
              const active = selectedCategory === category;
              return (
                <Button
                  key={category}
                  type={active ? 'primary' : 'default'}
                  onClick={() => setSelectedCategory(active ? null : category)}
                  aria-pressed={active}
                >
                  {category} ({count})
                </Button>
              );
            })}
          </div>

          {loading ? (
            <div className="flex h-72 flex-col items-center justify-center">
              <Spin size="large" />
              <span className="mt-3 text-xs font-medium text-slate-400">
                加载服务目录清单中...
              </span>
            </div>
          ) : error ? (
            <div className="py-12 text-center">
              <Empty description={<span className="text-sm text-slate-500">{error}</span>} />
            </div>
          ) : filteredAndSortedCatalogs.length === 0 ? (
            <div className="py-12 text-center">
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={
                  <div className="space-y-1">
                    <p className="m-0 text-sm font-medium text-slate-700">
                      未找到符合条件的服务项
                    </p>
                    <p className="m-0 text-xs text-slate-400">
                      请尝试更换检索关键词或清空筛选条件
                    </p>
                  </div>
                }
              >
                {(searchText || selectedCategory || approvalFilter !== 'all' || sortBy !== 'default') && (
                  <Button size="small" onClick={resetFilters} className="mt-2">
                    重置筛选条件
                  </Button>
                )}
              </Empty>
            </div>
          ) : viewMode === 'grid' ? (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
              {filteredAndSortedCatalogs.map(catalog => (
                <div key={catalog.id} className="flex h-full flex-col">
                  <ServiceItemCard catalog={catalog} viewMode="grid" />
                </div>
              ))}
            </div>
          ) : (
            <div className="flex flex-col gap-2">
              {filteredAndSortedCatalogs.map(catalog => (
                <ServiceItemCard key={catalog.id} catalog={catalog} viewMode="list" />
              ))}
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
