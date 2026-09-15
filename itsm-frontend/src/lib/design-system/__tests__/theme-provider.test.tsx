import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { ThemeProvider, useTheme } from '../theme';

function Harness() {
  const { mode, isDark, setMode, toggleTheme } = useTheme();
  return (
    <>
      <output>
        {mode}/{isDark ? 'dark' : 'light'}
      </output>
      <button onClick={toggleTheme}>toggle</button>
      <button onClick={() => setMode('system')}>system</button>
      <button onClick={() => setMode('light')}>light</button>
    </>
  );
}
let systemDark = true;
const listeners = new Set<() => void>();
function mediaChange(dark: boolean) {
  act(() => {
    systemDark = dark;
    listeners.forEach(listener => listener());
  });
}
beforeEach(() => {
  localStorage.clear();
  systemDark = true;
  listeners.clear();
  (window.matchMedia as jest.Mock).mockImplementation(() => ({
    get matches() {
      return systemDark;
    },
    addEventListener: (_: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) => listeners.delete(listener),
  }));
});
afterEach(() => jest.restoreAllMocks());
it('defaults to light even on a dark operating system', () => {
  render(
    <ThemeProvider>
      <Harness />
    </ThemeProvider>
  );
  expect(screen.getByText('light/light')).toBeInTheDocument();
});
it('restores saved dark before writing and toggles to a persistent light selection', () => {
  localStorage.setItem('itsm-theme', 'dark');
  const writes: string[] = [];
  const original = Storage.prototype.setItem;
  jest.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    writes.push(value);
    original.call(this, key, value);
  });
  const view = render(
    <ThemeProvider>
      <Harness />
    </ThemeProvider>
  );
  expect(screen.getByText('dark/dark')).toBeInTheDocument();
  expect(writes.length).toBeGreaterThan(0);
  expect(writes.every(value => value === 'dark')).toBe(true);
  fireEvent.click(screen.getByText('toggle'));
  expect(screen.getByText('light/light')).toBeInTheDocument();
  view.unmount();
  render(
    <ThemeProvider>
      <Harness />
    </ThemeProvider>
  );
  expect(screen.getByText('light/light')).toBeInTheDocument();
});
it('uses media changes only in system mode and flips the resolved theme', () => {
  localStorage.setItem('itsm-theme', 'system');
  render(
    <ThemeProvider>
      <Harness />
    </ThemeProvider>
  );
  expect(screen.getByText('system/dark')).toBeInTheDocument();
  mediaChange(false);
  expect(screen.getByText('system/light')).toBeInTheDocument();
  fireEvent.click(screen.getByText('toggle'));
  expect(screen.getByText('dark/dark')).toBeInTheDocument();
  mediaChange(true);
  mediaChange(false);
  expect(screen.getByText('dark/dark')).toBeInTheDocument();
  fireEvent.click(screen.getByText('light'));
  mediaChange(true);
  expect(screen.getByText('light/light')).toBeInTheDocument();
  fireEvent.click(screen.getByText('system'));
  expect(screen.getByText('system/dark')).toBeInTheDocument();
});
it('recovers invalid storage and allows switching when storage is unavailable', () => {
  localStorage.setItem('itsm-theme', 'invalid');
  const view = render(
    <ThemeProvider>
      <Harness />
    </ThemeProvider>
  );
  expect(screen.getByText('light/light')).toBeInTheDocument();
  view.unmount();
  jest.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('denied');
  });
  jest.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('denied');
  });
  render(
    <ThemeProvider>
      <Harness />
    </ThemeProvider>
  );
  fireEvent.click(screen.getByText('toggle'));
  expect(screen.getByText('dark/dark')).toBeInTheDocument();
});
