import React from 'react';
import { render, fireEvent, screen } from '@testing-library/react';
const { renderToStaticMarkup } =
  jest.requireActual<typeof import('react-dom/server')>('react-dom/server.node');
import GlobalError from '../global-error';

function bootFailureDocument() {
  const markup = renderToStaticMarkup(
    <GlobalError
      error={Object.assign(new Error('root failed'), { digest: 'root-123' })}
      reset={() => {}}
    />
  );
  const parsed = new DOMParser().parseFromString(markup, 'text/html');
  const bootstrap = parsed.querySelector('script');
  expect(bootstrap).not.toBeNull();
  // Execute the actual standalone document bootstrap, without mounting any providers.
  new Function(bootstrap!.textContent ?? '')();
  expect(parsed.body.textContent).toContain('root-123');
}

beforeEach(() => {
  localStorage.clear();
  document.documentElement.className = '';
  (window.matchMedia as jest.Mock).mockReturnValue({ matches: true });
});
afterEach(() => jest.restoreAllMocks());

it.each([
  ['dark', false, 'dark'],
  ['system', true, 'dark'],
  ['system', false, 'light'],
  ['light', true, 'light'],
  ['invalid', true, 'light'],
  [null, true, 'light'],
])(
  'root failure restores %s with system dark=%s without overwriting preference',
  (saved, systemDark, expected) => {
    if (saved !== null) localStorage.setItem('itsm-theme', saved);
    (window.matchMedia as jest.Mock).mockReturnValue({ matches: systemDark });
    bootFailureDocument();
    expect(document.documentElement.classList.contains(expected)).toBe(true);
    expect(document.documentElement.style.colorScheme).toBe(expected);
    expect(localStorage.getItem('itsm-theme')).toBe(saved);
  }
);

it('root failure remains usable with denied storage', () => {
  jest.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('denied');
  });
  bootFailureDocument();
  expect(document.documentElement.style.colorScheme).toBe('light');
});

it('client-mounted root failure restores preference and keeps recovery usable', () => {
  localStorage.setItem('itsm-theme', 'dark');
  jest.spyOn(console, 'error').mockImplementation(() => {});
  const reset = jest.fn();
  render(<GlobalError error={new Error('client root failed')} reset={reset} />);
  expect(document.documentElement.classList.contains('dark')).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: /重试/ }));
  expect(reset).toHaveBeenCalledTimes(1);
});
