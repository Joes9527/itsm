'use client';

import React, { useState } from 'react';
import { useRouter } from 'next/navigation';
import {
  Tag,
  Button,
  Typography,
  Dropdown,
  App,
  Tooltip,
} from 'antd';
import type { MenuProps } from 'antd';
import {
  HardDrive,
  UserCog,
  ShieldCheck,
  Clock,
  ArrowRight,
  MoreHorizontal,
  Edit,
  Eye,
  Server,
  Database,
  Globe,
  KeyRound,
  FileCheck2,
  Sparkles,
  Zap,
} from 'lucide-react';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import type { ServiceItem } from '@/types/service-catalog';
import { useI18n } from '@/lib/i18n';

// 更加立体精细的 3D 图标容器与质感配置 (3D Embossed & Elevated Icon Visuals)
const getCategoryVisuals = (category: string, name: string) => {
  const lowerName = (name || '').toLowerCase();
  const lowerCat = (category || '').toLowerCase();

  if (lowerName.includes('数据库') || lowerName.includes('rds') || lowerName.includes('mysql') || lowerName.includes('redis')) {
    return {
      icon: Database,
      bgClass: 'bg-gradient-to-br from-emerald-400 to-emerald-600 text-white shadow-[0_4px_10px_rgba(16,185,129,0.3),inset_0_1px_1px_rgba(255,255,255,0.4)] border border-emerald-400/80',
      badgeClass: 'bg-emerald-50 text-emerald-700 border-emerald-200',
    };
  }
  if (lowerName.includes('服务器') || lowerName.includes('ecs') || lowerName.includes('vm') || lowerName.includes('主机')) {
    return {
      icon: Server,
      bgClass: 'bg-gradient-to-br from-blue-500 to-indigo-600 text-white shadow-[0_4px_10px_rgba(59,130,246,0.3),inset_0_1px_1px_rgba(255,255,255,0.4)] border border-blue-400/80',
      badgeClass: 'bg-blue-50 text-blue-700 border-blue-200',
    };
  }
  if (lowerName.includes('网络') || lowerName.includes('vpn') || lowerName.includes('ip') || lowerName.includes('域名')) {
    return {
      icon: Globe,
      bgClass: 'bg-gradient-to-br from-indigo-500 to-purple-600 text-white shadow-[0_4px_10px_rgba(99,102,241,0.3),inset_0_1px_1px_rgba(255,255,255,0.4)] border border-indigo-400/80',
      badgeClass: 'bg-indigo-50 text-indigo-700 border-indigo-200',
    };
  }
  if (lowerName.includes('权限') || lowerName.includes('账号') || lowerName.includes('密码') || lowerCat.includes('account') || lowerCat.includes('账号')) {
    return {
      icon: KeyRound,
      bgClass: 'bg-gradient-to-br from-amber-400 to-orange-500 text-white shadow-[0_4px_10px_rgba(245,158,11,0.3),inset_0_1px_1px_rgba(255,255,255,0.4)] border border-amber-300/80',
      badgeClass: 'bg-amber-50 text-amber-700 border-amber-200',
    };
  }
  if (lowerName.includes('安全') || lowerName.includes('证书') || lowerCat.includes('security') || lowerCat.includes('安全')) {
    return {
      icon: ShieldCheck,
      bgClass: 'bg-gradient-to-br from-rose-500 to-red-600 text-white shadow-[0_4px_10px_rgba(244,63,94,0.3),inset_0_1px_1px_rgba(255,255,255,0.4)] border border-rose-400/80',
      badgeClass: 'bg-rose-50 text-rose-700 border-rose-200',
    };
  }
  if (lowerCat.includes('cloud') || lowerCat.includes('云资源') || lowerCat.includes('it_service')) {
    return {
      icon: HardDrive,
      bgClass: 'bg-gradient-to-br from-sky-400 to-blue-600 text-white shadow-[0_4px_10px_rgba(14,165,233,0.3),inset_0_1px_1px_rgba(255,255,255,0.4)] border border-sky-300/80',
      badgeClass: 'bg-sky-50 text-sky-700 border-sky-200',
    };
  }
  return {
    icon: UserCog,
    bgClass: 'bg-gradient-to-br from-slate-500 to-slate-700 text-white shadow-[0_4px_10px_rgba(100,116,139,0.3),inset_0_1px_1px_rgba(255,255,255,0.4)] border border-slate-400/80',
    badgeClass: 'bg-slate-50 text-slate-700 border-slate-200',
  };
};

interface ServiceItemCardProps {
  catalog: ServiceItem & {
    priority?: string;
    shortDescription?: string;
    slaTime?: string;
    estimatedTime?: string;
    rating?: number;
  };
  showManageActions?: boolean;
  viewMode?: 'grid' | 'list';
}

export const ServiceItemCard: React.FC<ServiceItemCardProps> = ({
  catalog,
  showManageActions = false,
  viewMode = 'grid',
}) => {
  const { t } = useI18n();
  const router = useRouter();
  const { message } = App.useApp();
  const [deleting, setDeleting] = useState(false);

  const categoryName = String(catalog.category || '通用服务');
  const visuals = getCategoryVisuals(categoryName, catalog.name);
  const IconComponent = visuals.icon;

  const estimatedResolution =
    catalog.availability?.resolutionTime ?? catalog.availability?.responseTime;

  const slaDisplay =
    catalog.slaTime ||
    catalog.estimatedTime ||
    (estimatedResolution ? `${estimatedResolution} 小时` : '24小时内');

  const handleCardClick = (e: React.MouseEvent) => {
    const target = e.target as HTMLElement;
    if (target.closest('button') || target.closest('.ant-dropdown')) {
      return;
    }
    router.push(`/service-catalog/detail/${catalog.id}`);
  };

  const handleDelete = async () => {
    setDeleting(true);
    try {
      await ServiceCatalogApi.deleteService(String(catalog.id));
      message.success(t('service.deleteSuccess') || '服务已删除');
      window.location.reload();
    } catch (error) {
      console.error('Failed to delete service:', error);
      message.error(t('service.deleteFailed') || '删除失败');
    } finally {
      setDeleting(false);
    }
  };

  const actionItems: MenuProps['items'] = [
    {
      key: 'detail',
      icon: <Eye size={14} />,
      label: t('common.view') || '查看详情',
      onClick: () => router.push(`/service-catalog/detail/${catalog.id}`),
    },
    {
      key: 'edit',
      icon: <Edit size={14} />,
      label: t('common.edit') || '编辑配置',
      onClick: () => router.push(`/service-catalog/edit/${catalog.id}`),
    },
    {
      type: 'divider',
    },
    {
      key: 'delete',
      icon: <span className="text-red-500 font-bold">×</span>,
      label: <span className="text-red-600">{t('common.delete') || '删除服务'}</span>,
      danger: true,
      onClick: handleDelete,
    },
  ];

  // ================= 紧凑列表视图 (List Mode) =================
  if (viewMode === 'list') {
    return (
      <div
        onClick={handleCardClick}
        className="group relative flex items-center justify-between p-4 bg-white rounded-xl border border-slate-200/80 hover:border-blue-400 hover:shadow-sm transition-all duration-150 cursor-pointer mb-2.5"
      >
        <div className="flex items-center gap-4 min-w-0 flex-1 mr-4">
          <div
            className={`w-11 h-11 rounded-lg border flex items-center justify-center shrink-0 transition-transform group-hover:scale-105 ${visuals.bgClass}`}
          >
            <IconComponent size={20} />
          </div>

          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 mb-1">
              <span className="font-semibold text-slate-800 text-sm truncate group-hover:text-blue-600 transition-colors">
                {catalog.name}
              </span>
              <span
                className={`text-[11px] px-2 py-0.5 rounded-md font-medium border shrink-0 ${visuals.badgeClass}`}
              >
                {categoryName}
              </span>
              {catalog.requiresApproval === false ? (
                <span className="inline-flex items-center gap-1 text-[11px] text-emerald-600 font-medium bg-emerald-50 border border-emerald-200/60 px-1.5 py-0.5 rounded shrink-0">
                  <Zap size={11} /> 免审批
                </span>
              ) : (
                <span className="inline-flex items-center gap-1 text-[11px] text-slate-500 font-medium bg-slate-50 border border-slate-200 px-1.5 py-0.5 rounded shrink-0">
                  <FileCheck2 size={11} /> 需审批
                </span>
              )}
            </div>

            <p className="text-xs text-slate-500 truncate m-0">
              {catalog.shortDescription || catalog.fullDescription || '提供标准规范的 IT 与资源交付支持'}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-5 shrink-0">
          <div className="hidden sm:flex flex-col items-end text-right">
            <span className="text-[11px] text-slate-400">SLA 承诺</span>
            <span className="text-xs font-mono font-medium text-slate-700 flex items-center gap-1">
              <Clock size={12} className="text-slate-400" />
              {slaDisplay}
            </span>
          </div>

          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={e => {
                e.stopPropagation();
                router.push(`/service-catalog/request/${catalog.id}`);
              }}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-orange-500 hover:bg-orange-600 active:bg-orange-700 text-white transition-colors duration-150 cursor-pointer group/btn"
            >
              <span>{t('serviceCatalog.applyService') || '申请服务'}</span>
              <ArrowRight size={13} className="transition-transform duration-150 group-hover/btn:translate-x-0.5" />
            </button>

            {showManageActions && (
              <Dropdown menu={{ items: actionItems }} trigger={['click']} placement="bottomRight">
                <Button
                  size="middle"
                  className="!h-8 !w-8 !p-0 !rounded-lg !border-slate-200 text-slate-500 hover:text-slate-800 flex items-center justify-center"
                  onClick={e => e.stopPropagation()}
                  loading={deleting}
                  icon={<MoreHorizontal size={15} />}
                />
              </Dropdown>
            )}
          </div>
        </div>
      </div>
    );
  }

  // ================= 现代化精细网格卡片 (Grid Mode) =================
  return (
    <div
      onClick={handleCardClick}
      className="group relative flex flex-col justify-between h-full bg-white rounded-xl border border-slate-200/90 hover:border-blue-400/80 hover:shadow-md transition-all duration-200 cursor-pointer overflow-hidden p-5"
    >
      {/* 顶部轻量点缀栏 */}
      <div className="flex items-start justify-between gap-3 mb-3.5">
        <div
          className={`w-11 h-11 rounded-xl border flex items-center justify-center shrink-0 shadow-xs transition-transform duration-200 group-hover:scale-105 ${visuals.bgClass}`}
        >
          <IconComponent size={22} />
        </div>

        <div className="flex items-center gap-1.5 shrink-0">
          {catalog.requiresApproval === false ? (
            <Tooltip title="该服务支持快速免审批开通">
              <span className="inline-flex items-center gap-1 text-[11px] text-emerald-700 font-medium bg-emerald-50 border border-emerald-200 px-2 py-0.5 rounded-full">
                <Zap size={11} className="text-emerald-500" />
                免审批
              </span>
            </Tooltip>
          ) : (
            <span className="inline-flex items-center gap-1 text-[11px] text-slate-500 font-medium bg-slate-50 border border-slate-200 px-2 py-0.5 rounded-full">
              <FileCheck2 size={11} className="text-slate-400" />
              需审批
            </span>
          )}

          {showManageActions && (
            <Dropdown menu={{ items: actionItems }} trigger={['click']} placement="bottomRight">
              <Button
                type="text"
                size="small"
                className="!h-6 !w-6 !p-0 text-slate-400 hover:text-slate-700 flex items-center justify-center rounded"
                onClick={e => e.stopPropagation()}
                loading={deleting}
                icon={<MoreHorizontal size={14} />}
              />
            </Dropdown>
          )}
        </div>
      </div>

      {/* 标题与描述 */}
      <div className="flex-1 flex flex-col mb-4">
        <div className="flex items-center gap-2 mb-1.5">
          <h4 className="font-semibold text-slate-900 text-base leading-snug tracking-tight m-0 line-clamp-1 group-hover:text-blue-600 transition-colors">
            {catalog.name}
          </h4>
        </div>

        <p className="text-xs text-slate-500 leading-relaxed m-0 line-clamp-2 min-h-[32px]">
          {catalog.shortDescription || catalog.fullDescription || '提供标准化 IT 服务支持与自动化履约保障'}
        </p>

        {/* 类别与规格标签 */}
        <div className="flex flex-wrap items-center gap-1.5 mt-3">
          <span
            className={`text-[11px] px-2 py-0.5 rounded-md font-medium border ${visuals.badgeClass}`}
          >
            {categoryName}
          </span>
          {catalog.requiresInfraFields && (
            <span className="text-[11px] px-2 py-0.5 rounded-md font-medium bg-violet-50 text-violet-700 border border-violet-200">
              云基础设施
            </span>
          )}
        </div>
      </div>

      {/* 底部元数据条与轻量操作区 */}
      <div className="pt-3 border-t border-slate-100/90 flex items-center justify-between">
        {/* 左侧：SLA 交付时长 */}
        <div className="flex items-center gap-1.5 text-xs text-slate-500">
          <Clock size={13} className="text-slate-400" />
          <span className="text-[11px] text-slate-400">SLA:</span>
          <span className="font-mono font-medium text-slate-700">{slaDisplay}</span>
        </div>

        {/* 右侧：经典纯平暖橙申请按钮 (Clean Flat Orange Button) */}
        <button
          type="button"
          onClick={e => {
            e.stopPropagation();
            router.push(`/service-catalog/request/${catalog.id}`);
          }}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-orange-500 hover:bg-orange-600 active:bg-orange-700 text-white transition-colors duration-150 cursor-pointer group/btn"
        >
          <span>{t('serviceCatalog.applyService') || '申请服务'}</span>
          <ArrowRight size={13} className="transition-transform duration-150 group-hover/btn:translate-x-0.5" />
        </button>
      </div>
    </div>
  );
};







