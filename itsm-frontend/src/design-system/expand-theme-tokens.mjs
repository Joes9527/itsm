/**
 * Expand the authoritative theme schema for both CSS generation and runtime consumers.
 * This module is pure JavaScript so Node scripts and the browser use exactly one mapping.
 * @param {typeof import('./theme-tokens.json')} tokens
 * @param {boolean} isDark
 * @returns {Record<string, string>}
 */
export function expandThemeTokens(tokens, isDark) {
  const prefix = (name, values) =>
    Object.fromEntries(Object.entries(values).map(([key, value]) => [`--${name}-${key}`, value]));
  return {
    ...tokens.common,
    ...prefix('color-primary', tokens.brand.palette),
    ...prefix('font-size', tokens.typography.fontSize),
    '--font-family-base': tokens.typography.fontFamily,
    '--font-size-page-title': tokens.typography.pageTitle,
    '--font-size-card-title': tokens.typography.cardTitle,
    '--font-size-statistic': tokens.typography.statistic,
    '--font-size-helper': tokens.typography.helper,
    '--header-height': `${tokens.sizes.header}px`,
    '--sidebar-width': `${tokens.sizes.sidebar}px`,
    '--sidebar-collapsed-width': `${tokens.sizes.sidebarCollapsed}px`,
    '--control-height': `${tokens.sizes.button}px`,
    '--control-height-sm': `${tokens.sizes.buttonSmall}px`,
    '--card-padding': `${tokens.sizes.cardPadding}px`,
    ...Object.fromEntries(
      Object.entries(tokens.aliases).map(([key, value]) => [key, `var(${value})`])
    ),
    ...tokens.themes[isDark ? 'dark' : 'light'],
  };
}
