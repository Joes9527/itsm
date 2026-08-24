'use client';

import React, { useMemo, useState } from 'react';
import { Breadcrumb, Col, Empty, Input, Row, Select, Spin, Button, Segmented, Tooltip } from 'antd';
import {
  BookOpen,
  Search,
  LayoutGrid,
  List,
  Sparkles,
  Layers,
  ShieldCheck,
  Server,
  KeyRound,
  Filter,
  CheckCircle2,
} from 'lucide-react';
import { useServiceCatalogData } from './hooks/useServiceCatalogData';
import { ServiceItemCard } from './components/ServiceItemCard';

export default function ServiceCatalogPage() {
  const { catalogs, loading, error, searchText, setSearchText } = useServiceCatalogData();
  const [selectedCategory, setSelectedCategory] = useState<string | null>(null);
  const [viewMode, setViewMode] = useState<'grid' | 'list'>('grid');
  const [approvalFilter, setApprovalFilter] = useState<'all' | 'no_approval' | 'need_approval'>('all');
  const [sortBy, setSortBy] = useState<'default' | 'name' | 'sla'>('default');

  // 动态提取分类列表
  const categories = useMemo(() => {
    const set = new Set<string>();
    catalogs.forEach(c => {
      if (c.category) set.add(String(c.category));
    });
    return Array.from(set);
  }, [catalogs]);

  // 过滤与排序
  const filteredAndSortedCatalogs = useMemo(() => {
    let result = [...catalogs];

    // 1. 分类筛选
    if (selectedCategory) {
      result = result.filter(c => String(c.category) === selectedCategory);
    }

    // 2. 审批属性筛选
    if (approvalFilter === 'no_approval') {
      result = result.filter(c => c.requiresApproval === false);
    } else if (approvalFilter === 'need_approval') {
      result = result.filter(c => c.requiresApproval !== false);
    }

    // 3. 排序
    if (sortBy === 'name') {
      result.sort((a, b) => (a.name || '').localeCompare(b.name || ''));
    } else if (sortBy === 'sla') {
      result.sort((a, b) => {
        const timeA = a.availability?.resolutionTime ?? a.availability?.responseTime ?? 999;
        const timeB = b.availability?.resolutionTime ?? b.availability?.responseTime ?? 999;
        return timeA - timeB;
      });
    }

    return result;
  }, [catalogs, selectedCategory, approvalFilter, sortBy]);

  // 分类专属小图标
  const getCategoryIcon = (category: string) => {
    const cat = category.toLowerCase();
    if (cat.includes('云') || cat.includes('cloud') || cat.includes('it_service')) {
      return <Server size={13} className="text-blue-500" />;
    }
    if (cat.includes('权限') || cat.includes('account') || cat.includes('账号')) {
      return <KeyRound size={13} className="text-amber-500" />;
    }
    if (cat.includes('安全') || cat.includes('security')) {
      return <ShieldCheck size={13} className="text-rose-500" />;
    }
    return <Layers size={13} className="text-slate-400" />;
  };

  return (
    <div className="min-h-[calc(100vh-64px)] bg-slate-50/50 p-4 md:p-8">
      <div className="max-w-7xl mx-auto space-y-6">
        {/* 面包屑与顶部导航 */}
        <div className="flex flex-col gap-1">
          <Breadcrumb
            items={[{ title: '首页', href: '/dashboard' }, { title: '服务目录' }]}
            className="text-xs text-slate-500 mb-1"
          />

          <div className="flex flex-col md:flex-row md:items-center md:justify-between gap-4">
            <div>
              <div className="flex items-center gap-2.5">
                <div className="w-8 h-8 rounded-lg bg-blue-600 flex items-center justify-center text-white shadow-sm">
                  <BookOpen size={18} />
                </div>
                <h1 className="text-xl md:text-2xl font-bold tracking-tight text-slate-900 m-0">
                  服务目录与自助申请
                </h1>
              </div>
              <p className="text-xs md:text-sm text-slate-500 mt-1 mb-0">
                标准化交付企业 IT 云资源、数据库、安全及办公权限服务，全程 SLA 跟踪与审计保障
              </p>
            </div>

            {/* 统计胶囊 */}
            <div className="flex items-center gap-2 self-start md:self-auto bg-white border border-slate-200/80 px-3.5 py-1.5 rounded-full shadow-xs">
              <span className="flex items-center gap-1.5 text-xs text-slate-600 font-medium">
                <Sparkles size={13} className="text-amber-500" />
                已发布服务
                <span className="font-mono font-bold text-blue-600 ml-0.5">{catalogs.length}</span> 项
              </span>
            </div>
          </div>
        </div>

        {/* 增强型 Command Bar (搜索 + 分类 Filter 标签 + 快捷操作控制台) */}
        <div className="bg-white rounded-2xl border border-slate-200/80 p-4 md:p-5 shadow-xs space-y-4">
          {/* 上半区：大号快速检索栏 + 视图切换 */}
          <div className="flex flex-col sm:flex-row items-center justify-between gap-3">
            <div className="relative w-full sm:max-w-md">
              <Input
                size="large"
                prefix={<Search size={16} className="text-slate-400 mr-1.5" />}
                placeholder="搜索服务名称、描述或关键字 (如: ECS, MySQL, VPN)..."
                value={searchText}
                onChange={e => setSearchText(e.target.value)}
                allowClear
                className="!rounded-xl !bg-slate-50/70 hover:!bg-white focus:!bg-white !border-slate-200 text-sm transition-all shadow-none"
              />
            </div>

            <div className="flex items-center gap-2.5 w-full sm:w-auto justify-between sm:justify-end">
              {/* 审批类型快捷过滤 */}
              <Select
                value={approvalFilter}
                onChange={setApprovalFilter}
                className="w-32"
                size="middle"
                options={[
                  { label: '全部审批类型', value: 'all' },
                  { label: '⚡ 免审批服务', value: 'no_approval' },
                  { label: '📋 需审批服务', value: 'need_approval' },
                ]}
              />

              {/* 排序 */}
              <Select
                value={sortBy}
                onChange={setSortBy}
                className="w-28"
                size="middle"
                options={[
                  { label: '默认排序', value: 'default' },
                  { label: '按名称 A-Z', value: 'name' },
                  { label: '按交付 SLA', value: 'sla' },
                ]}
              />

              {/* 视图切换 (Grid / List) */}
              <Segmented
                value={viewMode}
                onChange={v => setViewMode(v as 'grid' | 'list')}
                options={[
                  {
                    value: 'grid',
                    icon: (
                      <Tooltip title="网格卡片视图">
                        <LayoutGrid size={15} />
                      </Tooltip>
                    ),
                  },
                  {
                    value: 'list',
                    icon: (
                      <Tooltip title="紧凑列表视图">
                        <List size={15} />
                      </Tooltip>
                    ),
                  },
                ]}
                className="!bg-slate-100 p-0.5 rounded-lg shrink-0"
              />
            </div>
          </div>

          {/* 下半区：分类标签 Pills */}
          <div className="pt-3 border-t border-slate-100 flex items-center gap-2 overflow-x-auto no-scrollbar pb-1">
            <span className="text-xs font-medium text-slate-400 shrink-0 mr-1 flex items-center gap-1">
              <Filter size={12} />
              分类:
            </span>

            <button
              type="button"
              onClick={() => setSelectedCategory(null)}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium transition-all shrink-0 cursor-pointer border ${
                !selectedCategory
                  ? 'bg-blue-50 text-blue-700 border-blue-200/80 shadow-2xs font-semibold'
                  : 'bg-transparent text-slate-600 border-transparent hover:bg-slate-100/80'
              }`}
            >
              全部
              <span
                className={`text-[11px] font-mono px-1.5 py-0.2 rounded-full ${
                  !selectedCategory ? 'bg-blue-200/60 text-blue-800' : 'bg-slate-200/70 text-slate-600'
                }`}
              >
                {catalogs.length}
              </span>
            </button>

            {categories.map(cat => {
              const count = catalogs.filter(c => String(c.category) === cat).length;
              const isSelected = selectedCategory === cat;
              return (
                <button
                  key={cat}
                  type="button"
                  onClick={() => setSelectedCategory(isSelected ? null : cat)}
                  className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium transition-all shrink-0 cursor-pointer border ${
                    isSelected
                      ? 'bg-blue-50 text-blue-700 border-blue-200/80 shadow-2xs font-semibold'
                      : 'bg-transparent text-slate-600 border-transparent hover:bg-slate-100/80'
                  }`}
                >
                  {getCategoryIcon(cat)}
                  {cat}
                  <span
                    className={`text-[11px] font-mono px-1.5 py-0.2 rounded-full ${
                      isSelected ? 'bg-blue-200/60 text-blue-800' : 'bg-slate-200/70 text-slate-600'
                    }`}
                  >
                    {count}
                  </span>
                </button>
              );
            })}
          </div>
        </div>

        {/* 主内容区 */}
        {loading ? (
          <div className="flex flex-col justify-center items-center h-72 bg-white rounded-2xl border border-slate-200/80">
            <Spin size="large" />
            <span className="text-xs text-slate-400 mt-3 font-medium">加载服务目录清单中...</span>
          </div>
        ) : error ? (
          <div className="bg-white rounded-2xl border border-slate-200/80 p-12 text-center">
            <Empty description={<span className="text-slate-500 text-sm">{error}</span>} />
          </div>
        ) : filteredAndSortedCatalogs.length === 0 ? (
          <div className="bg-white rounded-2xl border border-slate-200/80 p-12 text-center">
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                <div className="space-y-1">
                  <p className="text-sm font-medium text-slate-700 m-0">未找到符合条件的服务项</p>
                  <p className="text-xs text-slate-400 m-0">请尝试更换检索关键词或清空分类筛选条件</p>
                </div>
              }
            >
              {(searchText || selectedCategory || approvalFilter !== 'all') && (
                <Button
                  size="small"
                  onClick={() => {
                    setSearchText('');
                    setSelectedCategory(null);
                    setApprovalFilter('all');
                    setSortBy('default');
                  }}
                  className="mt-2 text-xs"
                >
                  重置筛选条件
                </Button>
              )}
            </Empty>
          </div>
        ) : viewMode === 'grid' ? (
          <Row gutter={[16, 16]}>
            {filteredAndSortedCatalogs.map(catalog => (
              <Col xs={24} sm={12} md={8} lg={6} key={catalog.id} className="flex flex-col">
                <ServiceItemCard catalog={catalog} viewMode="grid" />
              </Col>
            ))}
          </Row>
        ) : (
          <div className="flex flex-col gap-2.5">
            {filteredAndSortedCatalogs.map(catalog => (
              <ServiceItemCard key={catalog.id} catalog={catalog} viewMode="list" />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

