import { authPageRedirect } from './middleware-policy';

describe('edge authentication redirect policy', () => {
  it('never redirects /login based only on an opaque invalid cookie', () => {
    expect(authPageRedirect('/login', true)).toBeNull();
  });

  it('redirects an unauthenticated protected route to login', () => {
    expect(authPageRedirect('/tickets', false)).toBe('/login?redirect=%2Ftickets');
  });
});
