import { httpClient } from '@/lib/api/http-client';

global.fetch = jest.fn();

describe('canonical cookie-only refresh', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('uses only POST /api/v1/auth/refresh with cookies and an empty DTO', async () => {
    (fetch as jest.Mock).mockResolvedValueOnce({
      ok: true,
      json: async () => ({ code: 0, message: 'success', data: { user: { id: 1 } } }),
    });

    const refreshed = await (
      httpClient as unknown as { refreshTokenInternal(): Promise<boolean> }
    ).refreshTokenInternal();

    expect(refreshed).toBe(true);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledWith(
      expect.stringMatching(/\/api\/v1\/auth\/refresh$/),
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
        body: '{}',
      })
    );
    const request = (fetch as jest.Mock).mock.calls[0][1] as RequestInit;
    expect(request.headers).not.toHaveProperty('Authorization');
  });

  it('fails closed when the canonical refresh endpoint rejects the session', async () => {
    (fetch as jest.Mock).mockResolvedValueOnce({ ok: false, status: 401 });

    const refreshed = await (
      httpClient as unknown as { refreshTokenInternal(): Promise<boolean> }
    ).refreshTokenInternal();

    expect(refreshed).toBe(false);
  });

  it('fails closed when refresh infrastructure is unavailable', async () => {
    (fetch as jest.Mock).mockRejectedValueOnce(new Error('network unavailable'));

    const refreshed = await (
      httpClient as unknown as { refreshTokenInternal(): Promise<boolean> }
    ).refreshTokenInternal();

    expect(refreshed).toBe(false);
  });
});
