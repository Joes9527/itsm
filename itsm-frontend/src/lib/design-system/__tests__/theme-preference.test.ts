import { parseThemeMode, resolveIsDark, getThemeBootstrapScript } from '../theme-preference';

it.each([
  ['light', 'light'],
  ['dark', 'dark'],
  ['system', 'system'],
  ['invalid', 'light'],
  [null, 'light'],
  [undefined, 'light'],
  [42, 'light'],
])('normalizes %s to %s', (value, expected) => {
  expect(parseThemeMode(value)).toBe(expected);
});
it.each([
  ['system', true, true],
  ['system', false, false],
  ['light', true, false],
  ['dark', false, true],
] as const)('resolves %s with OS dark=%s', (mode, systemDark, expected) => {
  expect(resolveIsDark(mode, systemDark)).toBe(expected);
});
it.each([
  ['dark', false, true],
  ['system', true, true],
  ['light', true, false],
  ['invalid', true, false],
])('bootstrap restores %s without changing saved preference', (saved, systemDark, expected) => {
  localStorage.setItem('itsm-theme', saved as string);
  (window.matchMedia as jest.Mock).mockReturnValue({ matches: systemDark });
  new Function(getThemeBootstrapScript())();
  expect(document.documentElement.classList.contains('dark')).toBe(expected);
  expect(document.documentElement.style.colorScheme).toBe(expected ? 'dark' : 'light');
  expect(localStorage.getItem('itsm-theme')).toBe(saved);
});
it('bootstrap tolerates denied storage', () => {
  jest.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('denied');
  });
  expect(() => new Function(getThemeBootstrapScript())()).not.toThrow();
  expect(document.documentElement.style.colorScheme).toBe('light');
  jest.restoreAllMocks();
});
