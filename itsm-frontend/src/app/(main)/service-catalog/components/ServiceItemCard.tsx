'use client';

import React, { useState } from 'react';
import { useRouter } from 'next/navigation';
import { Button, Dropdown, App, Tooltip } from 'antd';
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
  Zap,
} from 'lucide-react';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import type { ServiceItem } from '@/types/service-catalog';
import { useI18n } from '@/lib/i18n';

// Preserve category icon selection; presentation follows the shared theme.
const getCategoryVisuals = (category: string, name: string) => {
  const lowerName = (name || '').toLowerCase();
  const lowerCat = (category || '').toLowerCase();

  if (
    lowerName.includes('数据库') ||
    lowerName.includes('rds') ||
    lowerName.includes('mysql') ||
    lowerName.includes('redis')
  ) {
    return {
      icon: Database,
    };
  }
  if (
    lowerName.includes('服务器') ||
    lowerName.includes('ecs') ||
    lowerName.includes('vm') ||
    lowerName.includes('主机')
  ) {
    return {
      icon: Server,
    };
  }
  if (
    lowerName.includes('网络') ||
    lowerName.includes('vpn') ||
    lowerName.includes('ip') ||
    lowerName.includes('域名')
  ) {
    return {
      icon: Globe,
    };
  }
  if (
    lowerName.includes('权限') ||
    lowerName.includes('账号') ||
    lowerName.includes('密码') ||
    lowerCat.includes('account') ||
    lowerCat.includes('账号')
  ) {
    return {
      icon: KeyRound,
    };
  }
  if (
    lowerName.includes('安全') ||
    lowerName.includes('证书') ||
    lowerCat.includes('security') ||
    lowerCat.includes('安全')
  ) {
    return {
      icon: ShieldCheck,
    };
  }
  if (
    lowerCat.includes('cloud') ||
    lowerCat.includes('云资源') ||
    lowerCat.includes('it_service')
  ) {
    return {
      icon: HardDrive,
    };
  }
  return {
    icon: UserCog,
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
      if (!catalog.catalogVersion) throw new Error('请刷新服务目录后重试');
      await ServiceCatalogApi.deleteService(String(catalog.id), catalog.catalogVersion);
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
        className="group relative flex flex-wrap gap-3 items-center justify-between p-[16px] bg-surface rounded-[8px] border border-border hover:border-[var(--color-primary)] hover:shadow-none transition-all duration-150 cursor-pointer mb-2.5"
      >
        <div className="flex items-center gap-4 min-w-0 flex-1 basis-[240px]">
          <div
            className={`w-11 h-11 rounded-[8px] border flex items-center justify-center shrink-0 transition-transform group-hover:scale-105 bg-raised text-muted border-border`}
          >
            <IconComponent size={20} />
          </div>

          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2 mb-1">
              <span className="font-semibold text-foreground text-[15px] break-words group-hover:text-[var(--color-primary)] transition-colors">
                {catalog.name}
              </span>
              <span
                className={`text-[11px] px-2 py-0.5 rounded-[6px] font-medium border shrink-0 bg-raised text-muted border-border`}
              >
                {categoryName}
              </span>
              {catalog.requiresApproval === false ? (
                <span className="inline-flex items-center gap-1 text-[11px] text-emerald-600 font-medium bg-emerald-50 border border-emerald-200/60 px-1.5 py-0.5 rounded shrink-0">
                  <Zap size={11} /> 免审批
                </span>
              ) : (
                <span className="inline-flex items-center gap-1 text-[11px] text-muted font-medium bg-raised border border-border px-1.5 py-0.5 rounded shrink-0">
                  <FileCheck2 size={11} /> 需审批
                </span>
              )}
            </div>

            <p className="text-[12px] text-muted truncate m-0">
              {catalog.shortDescription ||
                catalog.fullDescription ||
                '提供标准规范的 IT 与资源交付支持'}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-5 shrink-0">
          <div className="hidden sm:flex flex-col items-end text-right">
            <span className="text-[11px] text-muted">SLA 承诺</span>
            <span className="text-[12px] font-mono font-medium text-foreground flex items-center gap-1">
              <Clock size={12} className="text-muted" />
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
              className="inline-flex items-center gap-1.5 px-3 h-[29px] rounded-[6px] text-[12px] font-medium bg-[var(--color-primary)] hover:bg-[var(--color-primary-hover)] active:bg-[var(--color-primary-hover)] text-white transition-colors duration-150 cursor-pointer group/btn"
            >
              <span>{t('serviceCatalog.applyService') || '申请服务'}</span>
              <ArrowRight
                size={13}
                className="transition-transform duration-150 group-hover/btn:translate-x-0.5"
              />
            </button>

            {showManageActions && (
              <Dropdown menu={{ items: actionItems }} trigger={['click']} placement="bottomRight">
                <Button
                  size="middle"
                  className="!h-8 !w-8 !p-0 !rounded-[8px] !border-border text-muted hover:text-foreground flex items-center justify-center"
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
      className="group relative flex flex-col justify-between h-full bg-surface rounded-[8px] border border-border hover:border-[var(--color-primary)] hover:shadow-none transition-all duration-200 cursor-pointer overflow-hidden p-[16px]"
    >
      {/* 顶部轻量点缀栏 */}
      <div className="flex items-start justify-between gap-3 mb-3.5">
        <div
          className={`w-11 h-11 rounded-[8px] border flex items-center justify-center shrink-0 shadow-none transition-transform duration-200 group-hover:scale-105 bg-raised text-muted border-border`}
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
            <span className="inline-flex items-center gap-1 text-[11px] text-muted font-medium bg-raised border border-border px-2 py-0.5 rounded-full">
              <FileCheck2 size={11} className="text-muted" />
              需审批
            </span>
          )}

          {showManageActions && (
            <Dropdown menu={{ items: actionItems }} trigger={['click']} placement="bottomRight">
              <Button
                type="text"
                size="small"
                className="!h-6 !w-6 !p-0 text-muted hover:text-foreground flex items-center justify-center rounded"
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
        <div className="flex flex-wrap items-center gap-2 mb-1.5">
          <h4 className="font-semibold text-foreground text-[15px] leading-snug tracking-tight m-0 break-words group-hover:text-[var(--color-primary)] transition-colors">
            {catalog.name}
          </h4>
        </div>

        <p className="text-[12px] text-muted leading-relaxed m-0 line-clamp-2 min-h-[32px]">
          {catalog.shortDescription ||
            catalog.fullDescription ||
            '提供标准化 IT 服务支持与自动化履约保障'}
        </p>

        {/* 类别与规格标签 */}
        <div className="flex flex-wrap items-center gap-1.5 mt-3">
          <span
            className={`text-[11px] px-2 py-0.5 rounded-[6px] font-medium border bg-raised text-muted border-border`}
          >
            {categoryName}
          </span>
          {catalog.requiresInfraFields && (
            <span className="text-[11px] px-2 py-0.5 rounded-[6px] font-medium bg-violet-50 text-violet-700 border border-violet-200">
              云基础设施
            </span>
          )}
        </div>
      </div>

      {/* 底部元数据条与轻量操作区 */}
      <div className="pt-3 flex-wrap gap-2 border-t border-border flex items-center justify-between">
        {/* 左侧：SLA 交付时长 */}
        <div className="flex items-center gap-1.5 text-[12px] text-muted">
          <Clock size={13} className="text-muted" />
          <span className="text-[11px] text-muted">SLA:</span>
          <span className="font-mono font-medium text-foreground">
            {slaDisplay}
          </span>
        </div>

        {/* 右侧：经典纯平暖橙申请按钮 (Clean Flat Orange Button) */}
        <button
          type="button"
          onClick={e => {
            e.stopPropagation();
            router.push(`/service-catalog/request/${catalog.id}`);
          }}
          className="inline-flex items-center gap-1.5 px-3 h-[29px] rounded-[6px] text-[12px] font-medium bg-[var(--color-primary)] hover:bg-[var(--color-primary-hover)] active:bg-[var(--color-primary-hover)] text-white transition-colors duration-150 cursor-pointer group/btn"
        >
          <span>{t('serviceCatalog.applyService') || '申请服务'}</span>
          <ArrowRight
            size={13}
            className="transition-transform duration-150 group-hover/btn:translate-x-0.5"
          />
        </button>
      </div>
    </div>
  );
};
