'use client';

import React from 'react';
import { Tag } from 'antd';
import { Sparkles } from 'lucide-react';

interface AIConfidenceBadgeProps {
  confidence?: number; // 0 ~ 100 or 0 ~ 1
  label?: string;
  size?: 'small' | 'middle';
}

export const AIConfidenceBadge: React.FC<AIConfidenceBadgeProps> = ({
  confidence = 90,
  label = 'AI 建议',
  size = 'small',
}) => {
  const normalizedScore = confidence <= 1 ? Math.round(confidence * 100) : Math.round(confidence);

  let colorClass = 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:border-emerald-800';
  if (normalizedScore < 70) {
    colorClass = 'bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:border-amber-800';
  }

  return (
    <span
      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium border ${colorClass}`}
    >
      <Sparkles size={12} className="animate-pulse" />
      <span>{label}</span>
      <span className="font-semibold">{normalizedScore}%</span>
    </span>
  );
};
