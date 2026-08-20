'use client';

import React, { useState, useEffect } from 'react';
import { Clock, AlertTriangle } from 'lucide-react';

interface SLACountdownTimerProps {
  targetTime?: string; // ISO string
  priority?: string;
}

export const SLACountdownTimer: React.FC<SLACountdownTimerProps> = ({
  targetTime = new Date(Date.now() + 45 * 60 * 1000).toISOString(),
  priority = 'high',
}) => {
  const [timeLeft, setTimeLeft] = useState<{ minutes: number; seconds: number; isBreached: boolean }>({
    minutes: 45,
    seconds: 0,
    isBreached: false,
  });

  useEffect(() => {
    const updateCountdown = () => {
      const now = Date.now();
      const target = new Date(targetTime).getTime();
      const diff = target - now;

      if (diff <= 0) {
        setTimeLeft({ minutes: 0, seconds: 0, isBreached: true });
        return;
      }

      const minutes = Math.floor(diff / 60000);
      const seconds = Math.floor((diff % 60000) / 1000);
      setTimeLeft({ minutes, seconds, isBreached: false });
    };

    updateCountdown();
    const interval = setInterval(updateCountdown, 1000);
    return () => clearInterval(interval);
  }, [targetTime]);

  if (timeLeft.isBreached) {
    return (
      <div className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-red-100 dark:bg-red-950/60 border border-red-300 dark:border-red-800 text-red-700 dark:text-red-300 text-xs font-bold animate-pulse">
        <AlertTriangle size={14} />
        <span>SLA 已违规</span>
      </div>
    );
  }

  const isUrgent = timeLeft.minutes < 30;

  return (
    <div
      className={`flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-bold border transition-colors ${
        isUrgent
          ? 'bg-amber-50 dark:bg-amber-950/40 border-amber-300 dark:border-amber-700 text-amber-700 dark:text-amber-300 animate-pulse'
          : 'bg-emerald-50 dark:bg-emerald-950/40 border-emerald-300 dark:border-emerald-700 text-emerald-700 dark:text-emerald-300'
      }`}
    >
      <Clock size={14} />
      <span>
        SLA 响应倒计时：{String(timeLeft.minutes).padStart(2, '0')}:{String(timeLeft.seconds).padStart(2, '0')}
      </span>
    </div>
  );
};
