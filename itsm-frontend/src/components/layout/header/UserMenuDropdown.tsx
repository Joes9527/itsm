'use client';

import React from 'react';
import { Dropdown, Avatar, Button } from 'antd';
import type { MenuProps } from 'antd';
import { User, Settings, LogOut } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { useAuthStore } from '@/lib/store/auth-store';
import { DESIGN } from '@/design-system/tokens';
import styles from './Header.module.css';

interface UserMenuDropdownProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onLogout: () => void;
}

export const UserMenuDropdown: React.FC<UserMenuDropdownProps> = ({
  open,
  onOpenChange,
  onLogout,
}) => {
  const router = useRouter();
  const { user } = useAuthStore();

  if (!user) {
    return (
      <Button
        type='primary'
        icon={<User size={16} />}
        onClick={() => router.push('/login')}
        style={{
          borderRadius: DESIGN.radius.md,
          height: 36,
          background: `linear-gradient(135deg, ${DESIGN.colors.accent} 0%, #B84A08 100%)`,
          boxShadow: DESIGN.shadows.glow(DESIGN.colors.accent),
        }}
      >
        登录
      </Button>
    );
  }

  const displayName = user?.name || user?.username || '';
  const userInitial = displayName.charAt(0).toUpperCase() || 'U';
  const handleMenuClick: MenuProps['onClick'] = ({ key }) => {
    if (key === 'logout') {
      onLogout();
    } else if (key === 'profile' || key === 'settings') {
      router.push('/profile');
    }
  };

  const menuItems: MenuProps['items'] = [
    {
      key: 'user-info',
      label: (
        <div style={{ padding: '8px 0', minWidth: 160 }}>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{displayName}</div>
          <div style={{ fontSize: 12, color: DESIGN.colors.textMuted }}>{user?.email}</div>
        </div>
      ),
      disabled: true,
    },
    { type: 'divider' },
    {
      key: 'profile',
      label: (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '8px 0' }}>
          <User size={16} />
          <span>个人中心</span>
        </div>
      ),
    },
    {
      key: 'settings',
      label: (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '8px 0' }}>
          <Settings size={16} />
          <span>设置</span>
        </div>
      ),
    },
    { type: 'divider' },
    {
      key: 'logout',
      label: (
        <div
          style={{ display: 'flex', alignItems: 'center', gap: 12, color: DESIGN.colors.danger }}
        >
          <LogOut size={16} />
          <span>退出登录</span>
        </div>
      ),
    },
  ];

  return (
    <Dropdown
      menu={{ items: menuItems, onClick: handleMenuClick }}
      placement='bottomRight'
      trigger={['click']}
      open={open}
      onOpenChange={onOpenChange}
      styles={{ root: { padding: 0 } }}
    >
      <button
        type='button'
        aria-label={`用户菜单：${displayName}`}
        title={displayName}
        className={styles.userMenuTrigger}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          padding: '2px 6px 2px 2px',
          borderRadius: DESIGN.radius.md,
          background: 'transparent',
          cursor: 'pointer',
          transition: 'all 0.2s',
          border: `1px solid ${open ? 'var(--color-primary)' : 'transparent'}`,
        }}
      >
        <Avatar
          size={27}
          style={{
            background: 'var(--color-primary)',
            fontSize: 14,
            fontWeight: 600,
          }}
        >
          {userInitial}
        </Avatar>
        <div className={styles.userMenuDetails} style={{ lineHeight: 1.3 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text-header)' }}>
            {displayName}
          </div>
        </div>
        <svg
          className={styles.userMenuChevron}
          width='14'
          height='14'
          viewBox='0 0 24 24'
          fill='none'
          stroke={DESIGN.colors.textMuted}
          strokeWidth='2'
          style={{
            transition: 'transform 0.2s',
            transform: open ? 'rotate(180deg)' : 'none',
          }}
        >
          <path d='M6 9l6 6 6-6' />
        </svg>
      </button>
    </Dropdown>
  );
};
