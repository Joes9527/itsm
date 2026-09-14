'use client';

import React from 'react';
import { Dropdown, Button } from 'antd';
import type { MenuProps } from 'antd';
import { Sparkles, Terminal, Activity, TrendingUp, Shield, ChevronDown, Check } from 'lucide-react';
import { useRouter, usePathname } from 'next/navigation';
import { useAuthStore } from '@/lib/store/auth-store';
import { usePersonaStore } from '@/lib/store/persona-store';
import { PERSONAS, PersonaType, getRolePersonaConfig } from '@/config/persona/persona-config';
import { DESIGN } from '@/design-system/tokens';
import styles from './Header.module.css';

const ICONS_MAP = {
  Sparkles: <Sparkles size={16} className='text-emerald-500' />,
  Terminal: <Terminal size={16} className='text-blue-500' />,
  Activity: <Activity size={16} className='text-amber-500' />,
  TrendingUp: <TrendingUp size={16} className='text-purple-500' />,
  Shield: <Shield size={16} className='text-red-500' />,
};

export const PersonaSwitcher: React.FC = () => {
  const router = useRouter();
  const pathname = usePathname();
  const { user } = useAuthStore();
  const { activePersona, setActivePersona } = usePersonaStore();

  const roleConfig = getRolePersonaConfig(user?.role);
  const currentPersona = PERSONAS[activePersona] || PERSONAS.portal;

  // 如果该角色只允许 1 个视图（如普通员工），则无需展示切换菜单
  if (roleConfig.allowedPersonas.length <= 1) {
    return (
      <div className={styles.personaStatic} title={currentPersona.name}>
        {ICONS_MAP[currentPersona.icon as keyof typeof ICONS_MAP]}
        <span>{currentPersona.name}</span>
      </div>
    );
  }

  const handleMenuClick: MenuProps['onClick'] = ({ key }) => {
    const targetPersona = key as PersonaType;
    if (targetPersona === activePersona) return;

    setActivePersona(targetPersona);
    const targetConfig = PERSONAS[targetPersona];
    router.push(targetConfig.homePath);
  };

  const menuItems: MenuProps['items'] = roleConfig.allowedPersonas.map(personaKey => {
    const item = PERSONAS[personaKey];
    const isSelected = personaKey === activePersona;

    return {
      key: item.type,
      label: (
        <div className='flex items-center justify-between gap-4 py-1.5 px-1 min-w-[200px]'>
          <div className='flex items-center gap-2.5'>
            {ICONS_MAP[item.icon as keyof typeof ICONS_MAP]}
            <div>
              <div className='text-sm font-semibold text-slate-800 dark:text-slate-100 flex items-center gap-2'>
                {item.name}
                {item.type === roleConfig.defaultPersona && (
                  <span className='text-[10px] bg-slate-100 dark:bg-slate-800 text-slate-500 px-1.5 py-0.5 rounded'>
                    默认
                  </span>
                )}
              </div>
              <div className='text-xs text-slate-400 font-normal'>{item.description}</div>
            </div>
          </div>
          {isSelected && <Check size={16} className='text-primary-600 font-bold' />}
        </div>
      ),
    };
  });

  return (
    <Dropdown menu={{ items: menuItems, onClick: handleMenuClick }} trigger={['click']}>
      <Button className={styles.personaButton} title={currentPersona.name}>
        <div className='flex items-center gap-1.5'>
          {ICONS_MAP[currentPersona.icon as keyof typeof ICONS_MAP]}
          <span className={styles.personaName}>{currentPersona.name}</span>
        </div>
        <ChevronDown size={14} className='text-slate-400' />
      </Button>
    </Dropdown>
  );
};
