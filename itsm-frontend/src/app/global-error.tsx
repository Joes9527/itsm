'use client';

import React, { useEffect } from 'react';
import tokens from '@/design-system/theme-tokens.json';
import { expandThemeTokens } from '@/design-system/expand-theme-tokens.mjs';
import {
  applyThemePreference,
  getThemeBootstrapScript,
} from '@/lib/design-system/theme-preference';

// Root failure cannot assume the layout stylesheet or providers survived.
const failureThemeCss = [false, true]
  .map(isDark => {
    const declarations = Object.entries(expandThemeTokens(tokens, isDark))
      .map(([name, value]) => `${name}:${value}`)
      .join(';');
    return `${isDark ? ':root.dark' : ':root'}{${declarations}}`;
  })
  .join('');

/**
 * 根级错误边界 (global-error.tsx)
 *
 * 关键约束：不能使用 Ant Design 或任何依赖 root layout Provider 的组件，
 * 因为 root layout 可能已经挂掉。这里只使用原生 HTML + 内联样式。
 */
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // React-inserted scripts do not run when a client error replaces the root.
    applyThemePreference();
    console.error('[GlobalError]', error);
  }, [error]);

  const containerStyle: React.CSSProperties = {
    minHeight: '100vh',
    boxSizing: 'border-box',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    background: 'var(--color-bg-secondary)',
    padding: '24px',
    fontFamily: tokens.typography.fontFamily,
    fontSize: tokens.typography.fontSize.base,
    color: 'var(--color-text-primary)',
  };

  const cardStyle: React.CSSProperties = {
    maxWidth: '480px',
    width: '100%',
    background: 'var(--color-bg-primary)',
    borderRadius: tokens.sizes.cardRadius,
    padding: tokens.sizes.cardPadding,
    textAlign: 'center',
    border: '1px solid var(--color-border)',
    boxSizing: 'border-box',
  };

  const iconStyle: React.CSSProperties = {
    width: '64px',
    height: '64px',
    margin: '0 auto 24px',
    background: 'var(--color-bg-tertiary)',
    borderRadius: '50%',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontSize: 'var(--font-size-4xl)',
  };

  const titleStyle: React.CSSProperties = {
    fontSize: tokens.typography.pageTitle,
    fontWeight: tokens.typography.strong,
    color: 'var(--color-text-primary)',
    marginBottom: '12px',
  };

  const subtitleStyle: React.CSSProperties = {
    fontSize: 'var(--font-size-base)',
    color: 'var(--color-text-secondary)',
    lineHeight: 'var(--line-height-relaxed)',
    marginBottom: '32px',
  };

  const buttonContainerStyle: React.CSSProperties = {
    display: 'flex',
    gap: '12px',
    justifyContent: 'center',
    flexWrap: 'wrap',
  };

  const primaryButtonStyle: React.CSSProperties = {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '8px',
    background: tokens.brand.palette['500'],
    color: '#ffffff',
    border: 'none',
    borderRadius: tokens.sizes.buttonRadius,
    height: tokens.sizes.button,
    padding: '0 16px',
    boxSizing: 'border-box',
    fontSize: 'var(--font-size-sm)',
    fontFamily: 'inherit',
    fontWeight: 500,
    cursor: 'pointer',
    transition: 'background 0.2s',
  };

  const secondaryButtonStyle: React.CSSProperties = {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '8px',
    background: 'var(--color-bg-primary)',
    color: 'var(--color-text-primary)',
    border: '1px solid var(--color-border)',
    borderRadius: tokens.sizes.buttonRadius,
    height: tokens.sizes.button,
    padding: '0 16px',
    boxSizing: 'border-box',
    fontSize: 'var(--font-size-sm)',
    fontFamily: 'inherit',
    fontWeight: 500,
    cursor: 'pointer',
    transition: 'background 0.2s',
  };

  const errorIdStyle: React.CSSProperties = {
    marginTop: '24px',
    fontSize: 'var(--font-size-xs)',
    color: 'var(--color-text-secondary)',
  };

  return (
    <html lang='zh-CN' suppressHydrationWarning>
      <head>
        <style dangerouslySetInnerHTML={{ __html: failureThemeCss }} />
        <script dangerouslySetInnerHTML={{ __html: getThemeBootstrapScript() }} />
      </head>
      <body style={{ margin: 0 }}>
        <div style={containerStyle}>
          <div style={cardStyle}>
            <div style={iconStyle}>⚠️</div>
            <h1 style={titleStyle}>系统发生错误</h1>
            <p style={subtitleStyle}>
              抱歉，应用遇到了一个严重错误。请尝试重试，如果问题持续存在，请联系技术支持。
            </p>
            <div style={buttonContainerStyle}>
              <button style={primaryButtonStyle} onClick={() => reset()}>
                ↻ 重试
              </button>
              <button
                style={secondaryButtonStyle}
                onClick={() => {
                  window.location.href = '/dashboard';
                }}
              >
                ⌂ 返回仪表盘
              </button>
            </div>
            {error?.digest && <div style={errorIdStyle}>错误 ID: {error.digest}</div>}
          </div>
        </div>
      </body>
    </html>
  );
}
