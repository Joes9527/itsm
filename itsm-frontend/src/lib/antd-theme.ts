import type { ThemeConfig } from 'antd';
import { getAntdTheme } from '@/lib/design-system/theme';

/** Deterministic light-theme fixture for legacy test utilities.
 * Production pages inherit the resolved root AntdProvider theme.
 */
export const antdTheme: ThemeConfig = getAntdTheme(false) as ThemeConfig;

export default antdTheme;
