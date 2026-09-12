/** Shared by the pre-paint bootstrap and the existing React theme provider. */
export type ThemeMode = 'light' | 'dark' | 'system';
export const THEME_STORAGE_KEY = 'itsm-theme';

export function parseThemeMode(value: unknown): ThemeMode {
  return value === 'dark' || value === 'system' ? value : 'light';
}

export function resolveIsDark(mode: ThemeMode, systemDark: boolean): boolean {
  return mode === 'dark' || (mode === 'system' && systemDark);
}

export function applyThemePreference(
  parse: typeof parseThemeMode = parseThemeMode,
  resolve: typeof resolveIsDark = resolveIsDark,
  key: string = THEME_STORAGE_KEY
) {
  let stored: unknown;
  try {
    stored = window.localStorage.getItem(key);
  } catch {
    /* Session-only preference. */
  }
  const isDark = resolve(
    parse(stored),
    window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false
  );
  const root = document.documentElement;
  root.classList.toggle('dark', isDark);
  root.classList.toggle('light', !isDark);
  root.style.colorScheme = isDark ? 'dark' : 'light';
}

/** Only static functions and a fixed key are serialized; no stored value becomes executable code. */
export function getThemeBootstrapScript(): string {
  return `(${applyThemePreference.toString()})(${parseThemeMode.toString()},${resolveIsDark.toString()},${JSON.stringify(THEME_STORAGE_KEY)});`;
}
