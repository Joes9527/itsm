'use client';

import type { ReactNode } from 'react';
import React from 'react';
import { cn } from '@/lib/utils';
import { layout, semanticSpacing } from '@/lib/design-system/spacing';

/**
 * 页面布局属性接口
 */
export interface PageLayoutProps {
  children: ReactNode;
  /** 页面标题 */
  title?: string;
  /** 页面描述 */
  description?: string;
  /** 页面头部内容 */
  header?: ReactNode;
  /** 页面侧边栏 */
  sidebar?: ReactNode;
  /** 页面底部内容 */
  footer?: ReactNode;
  /** 是否显示面包屑 */
  showBreadcrumb?: boolean;
  /** 面包屑数据 */
  breadcrumb?: Array<{ label: string; href?: string }>;
  /** 页面最大宽度 */
  maxWidth?: keyof typeof layout.page.maxWidth;
  /** 页面内边距 */
  padding?: keyof typeof semanticSpacing.padding;
  /** 页面背景色 */
  backgroundColor?: string;
  /** 自定义类名 */
  className?: string;
  /** 自定义样式 */
  style?: React.CSSProperties;
}

/**
 * 页面布局组件
 * 提供统一的页面布局结构
 */
export const PageLayout: React.FC<PageLayoutProps> = ({
  children,
  title,
  description,
  header,
  sidebar,
  footer,
  showBreadcrumb = false,
  breadcrumb = [],
  maxWidth = 'lg',
  padding = 'md',
  backgroundColor,
  className,
  style,
}) => {
  return (
    <div
      className={cn('min-h-screen flex flex-col', className)}
      style={{
        backgroundColor: backgroundColor || 'var(--color-bg-secondary)',
        ...style,
      }}
    >
      {/* 页面头部 */}
      {header && (
        <header
          className="sticky top-0 z-50"
          style={{
            backgroundColor: 'var(--color-bg-primary)',
            borderBottom: '1px solid var(--color-border)',
            boxShadow: 'none',
          }}
        >
          {header}
        </header>
      )}

      {/* 主要内容区域 */}
      <div className="flex flex-1">
        {/* 侧边栏 */}
        {sidebar && (
          <aside
            className="hidden lg:block"
            style={{
              width: layout.sidebar.width.lg,
              backgroundColor: 'var(--color-bg-primary)',
              borderRight: '1px solid var(--color-border)',
            }}
          >
            {sidebar}
          </aside>
        )}

        {/* 页面内容 */}
        <main className="flex-1 flex flex-col">
          {/* 页面标题区域 */}
          {(title || description || showBreadcrumb) && (
            <div
              className="px-4 py-6 lg:px-8"
              style={{
                backgroundColor: 'var(--color-bg-primary)',
                borderBottom: '1px solid var(--color-border)',
              }}
            >
              <div
                className="mx-auto"
                style={{
                  maxWidth: layout.page.maxWidth,
                  padding: `0 ${semanticSpacing.padding[padding]}`,
                }}
              >
                {/* 面包屑 */}
                {showBreadcrumb && breadcrumb.length > 0 && (
                  <nav className="mb-4">
                    <ol className="flex items-center space-x-2 text-[13px]">
                      {breadcrumb.map((item, index) => (
                        <li key={index} className="flex items-center">
                          {index > 0 && (
                            <span className="mx-2" style={{ color: 'var(--color-text-secondary)' }}>
                              /
                            </span>
                          )}
                          {item.href ? (
                            <a
                              href={item.href}
                              className="hover:underline"
                              style={{ color: 'var(--color-selected-text)' }}
                            >
                              {item.label}
                            </a>
                          ) : (
                            <span style={{ color: 'var(--color-text-secondary)' }}>{item.label}</span>
                          )}
                        </li>
                      ))}
                    </ol>
                  </nav>
                )}

                {/* 页面标题 */}
                {title && (
                  <h1 className="text-[24px] font-semibold mb-2" style={{ color: 'var(--color-text-primary)' }}>
                    {title}
                  </h1>
                )}

                {/* 页面描述 */}
                {description && (
                  <p className="text-[12px]" style={{ color: 'var(--color-text-secondary)' }}>
                    {description}
                  </p>
                )}
              </div>
            </div>
          )}

          {/* 页面内容 */}
          <div
            className="flex-1 px-4 py-6 lg:px-8"
            style={{
              padding: `${semanticSpacing.padding[padding]} ${semanticSpacing.padding[padding]}`,
            }}
          >
            <div
              className="mx-auto"
              style={{
                maxWidth: layout.page.maxWidth,
              }}
            >
              {children}
            </div>
          </div>
        </main>
      </div>

      {/* 页面底部 */}
      {footer && (
        <footer
          style={{
            backgroundColor: 'var(--color-bg-primary)',
            borderTop: '1px solid var(--color-border)',
            padding: semanticSpacing.padding[padding],
          }}
        >
          <div
            className="mx-auto"
            style={{
              maxWidth: layout.page.maxWidth,
            }}
          >
            {footer}
          </div>
        </footer>
      )}
    </div>
  );
};

/**
 * 内容区域布局属性接口
 */
export interface ContentLayoutProps {
  children: ReactNode;
  /** 内容标题 */
  title?: string;
  /** 内容描述 */
  description?: string;
  /** 内容头部操作 */
  actions?: ReactNode;
  /** 内容最大宽度 */
  maxWidth?: keyof typeof layout.content.maxWidth;
  /** 内容内边距 */
  padding?: keyof typeof semanticSpacing.padding;
  /** 是否显示边框 */
  bordered?: boolean;
  /** 自定义类名 */
  className?: string;
}

/**
 * 内容区域布局组件
 * 提供统一的内容区域布局
 */
export const ContentLayout: React.FC<ContentLayoutProps> = ({
  children,
  title,
  description,
  actions,
  maxWidth = 'md',
  padding = 'md',
  bordered = false,
  className,
}) => {
  return (
    <div
      className={cn('w-full', bordered && 'border rounded-[8px]', className)}
      style={{
        maxWidth: layout.content.maxWidth,
        padding: semanticSpacing.padding[padding],
        ...(bordered && {
          borderColor: 'var(--color-border)',
          backgroundColor: 'var(--color-bg-primary)',
        }),
      }}
    >
      {/* 内容头部 */}
      {(title || description || actions) && (
        <div className="mb-6">
          <div className="flex items-center justify-between mb-2">
            {title && (
              <h2 className="text-[15px] font-semibold" style={{ color: 'var(--color-text-primary)' }}>
                {title}
              </h2>
            )}
            {actions && <div className="ml-4">{actions}</div>}
          </div>
          {description && <p style={{ color: 'var(--color-text-secondary)' }}>{description}</p>}
        </div>
      )}

      {/* 内容主体 */}
      {children}
    </div>
  );
};

/**
 * 网格布局属性接口
 */
export interface GridLayoutProps {
  children: ReactNode;
  /** 网格列数 */
  columns?: 1 | 2 | 3 | 4 | 6 | 12;
  /** 网格间距 */
  gap?: keyof typeof semanticSpacing.component;
  /** 响应式列数 */
  responsive?: {
    xs?: 1 | 2 | 3 | 4;
    sm?: 1 | 2 | 3 | 4 | 6;
    md?: 1 | 2 | 3 | 4 | 6 | 8;
    lg?: 1 | 2 | 3 | 4 | 6 | 8 | 12;
    xl?: 1 | 2 | 3 | 4 | 6 | 8 | 12;
  };
  /** 自定义类名 */
  className?: string;
}

/**
 * 网格布局组件
 * 提供统一的网格布局系统
 */
export const GridLayout: React.FC<GridLayoutProps> = ({
  children,
  columns = 3,
  gap = 'md',
  responsive,
  className,
}) => {
  const getGridClasses = () => {
    const baseClasses = 'grid';

    // 基础列数
    const columnClasses = `grid-cols-${columns}`;

    // 响应式列数
    const responsiveClasses = responsive
      ? Object.entries(responsive)
          .map(([breakpoint, cols]) => `${breakpoint}:grid-cols-${cols}`)
          .join(' ')
      : '';

    return cn(baseClasses, columnClasses, responsiveClasses);
  };

  return (
    <div
      className={cn(getGridClasses(), className)}
      style={{ gap: semanticSpacing.component[gap] }}
    >
      {children}
    </div>
  );
};

/**
 * 弹性布局属性接口
 */
export interface FlexLayoutProps {
  children: ReactNode;
  /** 布局方向 */
  direction?: 'row' | 'column' | 'row-reverse' | 'column-reverse';
  /** 对齐方式 */
  align?: 'start' | 'end' | 'center' | 'baseline' | 'stretch';
  /** 分布方式 */
  justify?: 'start' | 'end' | 'center' | 'between' | 'around' | 'evenly';
  /** 是否换行 */
  wrap?: boolean;
  /** 间距 */
  gap?: keyof typeof semanticSpacing.component;
  /** 自定义类名 */
  className?: string;
}

/**
 * 弹性布局组件
 * 提供统一的弹性布局系统
 */
export const FlexLayout: React.FC<FlexLayoutProps> = ({
  children,
  direction = 'row',
  align = 'start',
  justify = 'start',
  wrap = false,
  gap = 'md',
  className,
}) => {
  const getFlexClasses = () => {
    const baseClasses = 'flex';
    const directionClasses = `flex-${direction}`;
    const alignClasses = `items-${align}`;
    const justifyClasses = `justify-${justify}`;
    const wrapClasses = wrap ? 'flex-wrap' : 'flex-nowrap';

    return cn(baseClasses, directionClasses, alignClasses, justifyClasses, wrapClasses);
  };

  return (
    <div
      className={cn(getFlexClasses(), className)}
      style={{ gap: semanticSpacing.component[gap] }}
    >
      {children}
    </div>
  );
};

export default PageLayout;
