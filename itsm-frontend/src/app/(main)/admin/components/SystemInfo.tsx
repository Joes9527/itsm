'use client';

import React from 'react';
import { Settings, FileText, ArrowUpRight } from 'lucide-react';
import { useI18n } from '@/lib/i18n';

const getCurrentYear = () => new Date().getFullYear();

export const SystemInfo: React.FC = () => {
  const { t } = useI18n();

  const supportItems = [
    { title: t('admin.configGuide'), status: '规划中' },
    { title: t('admin.apiDocs'), status: '规划中' },
    { title: t('admin.techSupport'), status: '规划中' },
    { title: t('admin.updateLog'), status: '规划中' },
  ];

  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
      {/* 系统信息 */}
      <div className="bg-white dark:bg-slate-900 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-sm p-5">
        <div className="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-slate-100 mb-4">
          <Settings size={18} />
          <span>{t('admin.systemInfo')}</span>
          <span className="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-amber-100 text-amber-700 dark:bg-amber-950/50 dark:text-amber-300">
            静态展示
          </span>
        </div>
        <div className="space-y-3">
          <div className="flex items-center justify-between text-xs">
            <span className="text-slate-500 dark:text-slate-400">{t('admin.systemVersion')}</span>
            <span className="font-semibold text-slate-800 dark:text-slate-200">AI-Native ITSM v1.0.0</span>
          </div>
          <div className="flex items-center justify-between text-xs">
            <span className="text-slate-500 dark:text-slate-400">{t('admin.databaseVersion')}</span>
            <span className="font-semibold text-slate-800 dark:text-slate-200">PostgreSQL 15.0</span>
          </div>
          <div className="flex items-center justify-between text-xs">
            <span className="text-slate-500 dark:text-slate-400">{t('admin.licenseStatus')}</span>
            <span className="px-1.5 py-0.5 rounded text-[11px] font-semibold bg-emerald-100 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300">
              {t('admin.licenseActivated')}
            </span>
          </div>
          <div className="flex items-center justify-between text-xs">
            <span className="text-slate-500 dark:text-slate-400">{t('admin.licenseExpiry')}</span>
            <span className="font-semibold text-slate-800 dark:text-slate-200">{getCurrentYear() + 1}-12-31</span>
          </div>
        </div>
      </div>

      {/* 帮助和支持 */}
      <div className="bg-white dark:bg-slate-900 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-sm p-5">
        <div className="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-slate-100 mb-4">
          <FileText size={18} />
          <span>{t('admin.helpSupport')}</span>
          <span className="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-blue-100 text-blue-700 dark:bg-blue-950/50 dark:text-blue-300">
            待接入
          </span>
        </div>
        <div className="divide-y divide-slate-100 dark:divide-slate-800">
          {supportItems.map((item) => (
            <div key={item.title} className="py-2.5 flex items-center justify-between">
              <span className="text-xs text-slate-700 dark:text-slate-300">{item.title}</span>
              <div className="flex items-center gap-2">
                <span className="px-1.5 py-0.5 rounded text-[11px] bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400">
                  {item.status}
                </span>
                <ArrowUpRight size={14} className="text-slate-400" />
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};
