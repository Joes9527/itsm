import { hasBrowserSession } from '../browser-session';

describe('browser session gate', () => {
  it('treats an opaque access cookie as a session without decoding JWT claims', () => {
    expect(hasBrowserSession('opaque-session-value')).toBe(true);
    expect(hasBrowserSession('not.a.jwt')).toBe(true);
    expect(hasBrowserSession('')).toBe(false);
    expect(hasBrowserSession(undefined)).toBe(false);
  });
});
