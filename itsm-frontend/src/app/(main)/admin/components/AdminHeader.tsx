'use client';

import React, { useState, useEffect } from 'react';
import { useI18n } from '@/lib/i18n';

export const AdminHeader: React.FC = () => {
  const [currentTime, setCurrentTime] = useState(new Date());
  const { t } = useI18n();

  useEffect(() => {
    const timer = setInterval(() => {
      setCurrentTime(new Date());
    }, 1000);
    return () => clearInterval(timer);
  }, []);

  return (
    <div className="p-6 rounded-2xl bg-gradient-to-r from-primary-700 via-primary-600 to-purple-700 shadow-sm flex items-center justify-between flex-wrap gap-4">
      <div>
        <h1 className="text-2xl font-black text-white m-0 mb-2 tracking-tight">{t('admin.title')}</h1>
        <div className="text-base text-white/90">{t('admin.welcome')}</div>
        <div className="text-xs text-white/70 mt-1">{t('admin.description')}</div>
      </div>
      <div className="text-right">
        <div className="text-2xl text-white font-mono mb-1">{currentTime.toLocaleTimeString()}</div>
        <div className="text-xs text-white/70">
          {currentTime.toLocaleDateString('zh-CN', {
            year: 'numeric',
            month: 'long',
            day: 'numeric',
            weekday: 'long',
          })}
        </div>
      </div>
    </div>
  );
};
