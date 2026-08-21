'use client';

import React from 'react';
import { Activity, AlertCircle, CheckCircle, RefreshCw, XCircle } from 'lucide-react';
import { useI18n } from '@/lib/i18n';

const getCurrentDate = () => {
  const now = new Date();
  return now
    .toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    })
    .replace(/\//g, '-');
};

const systemHealth = {
  overall: 'excellent', // excellent, good, warning, critical
  uptime: '99.98%',
  lastUpdate: getCurrentDate(),
  services: {
    database: 'healthy',
    api: 'healthy',
    cache: 'healthy',
    queue: 'healthy',
  },
};

const HEALTH_STYLES: Record<string, { tagClass: string; textClass: string; badgeClass: string }> = {
  excellent: {
    tagClass: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300',
    textClass: 'text-emerald-600',
    badgeClass: 'bg-emerald-600',
  },
  good: {
    tagClass: 'bg-blue-50 text-blue-700 dark:bg-blue-950/40 dark:text-blue-300',
    textClass: 'text-blue-600',
    badgeClass: 'bg-blue-600',
  },
  warning: {
    tagClass: 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300',
    textClass: 'text-amber-600',
    badgeClass: 'bg-amber-600',
  },
  critical: {
    tagClass: 'bg-red-50 text-red-700 dark:bg-red-950/40 dark:text-red-300',
    textClass: 'text-red-600',
    badgeClass: 'bg-red-600',
  },
  default: {
    tagClass: 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300',
    textClass: 'text-slate-500',
    badgeClass: 'bg-slate-500',
  },
};

export const SystemHealth: React.FC = () => {
  const { t } = useI18n();

  const getHealthLabel = (status: string) => {
    switch (status) {
      case 'excellent':
        return t('admin.excellent');
      case 'good':
        return t('admin.good');
      case 'warning':
        return t('admin.warning');
      case 'critical':
        return t('admin.critical');
      default:
        return t('admin.unknown');
    }
  };

  const getHealthIcon = (status: string) => {
    switch (status) {
      case 'excellent':
      case 'good':
        return CheckCircle;
      case 'warning':
        return AlertCircle;
      case 'critical':
        return XCircle;
      default:
        return Activity;
    }
  };

  const HealthIcon = getHealthIcon(systemHealth.overall);
  const healthStyle = HEALTH_STYLES[systemHealth.overall] ?? HEALTH_STYLES.default;

  const serviceList = Object.entries(systemHealth.services).map(([service, status]) => ({
    name:
      service === 'database'
        ? t('admin.database')
        : service === 'api'
          ? t('admin.apiService')
          : service === 'cache'
            ? t('admin.cache')
            : t('admin.messageQueue'),
    status,
    icon: getHealthIcon(status),
  }));

  return (
    <div className="h-full bg-white dark:bg-slate-900 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-sm p-5">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-slate-100">
          <Activity size={18} />
          <span>{t('admin.systemHealth')}</span>
          <span className="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-amber-100 text-amber-700 dark:bg-amber-950/50 dark:text-amber-300">
            示例数据
          </span>
        </div>
        <button className="p-1.5 rounded-lg text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors">
          <RefreshCw size={15} />
        </button>
      </div>

      <div className="flex items-center gap-4 mb-5">
        <div className={`w-12 h-12 rounded-full ${healthStyle.badgeClass} flex items-center justify-center text-white flex-shrink-0`}>
          <HealthIcon size={22} />
        </div>
        <div>
          <div className={`text-lg font-bold ${healthStyle.textClass}`}>{getHealthLabel(systemHealth.overall)}</div>
          <div className="text-xs text-slate-400">
            {t('admin.uptime')}: {systemHealth.uptime}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3">
        {serviceList.map((item) => {
          const ServiceIcon = item.icon;
          const style = HEALTH_STYLES[item.status] ?? HEALTH_STYLES.default;
          return (
            <div key={item.name} className="flex items-center gap-2 text-xs">
              <ServiceIcon size={14} className={style.textClass} />
              <span className="text-slate-700 dark:text-slate-300">{item.name}</span>
              <span className={`px-1.5 py-0.5 rounded text-[10px] font-semibold ${style.tagClass}`}>
                {getHealthLabel(item.status)}
              </span>
            </div>
          );
        })}
      </div>

      <div className="mt-4 pt-3 border-t border-slate-100 dark:border-slate-800">
        <span className="text-[11px] text-slate-400">
          {t('admin.lastUpdate')}: {systemHealth.lastUpdate}
        </span>
      </div>
    </div>
  );
};
