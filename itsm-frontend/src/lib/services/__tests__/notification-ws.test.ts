jest.mock('@/lib/env', () => ({
  logger: { info: jest.fn(), error: jest.fn(), warn: jest.fn(), debug: jest.fn() },
}));

jest.mock('@/lib/api/http-client', () => ({ httpClient: { post: jest.fn() } }));

import { NotificationWSService } from '../notification-ws';
import { httpClient } from '@/lib/api/http-client';

const mockPost = httpClient.post as jest.Mock;

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  static OPEN = 1;
  static CONNECTING = 0;
  readyState = FakeWebSocket.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  sent: string[] = [];

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this);
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = 3;
  }

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.({} as Event);
  }

  message(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) } as MessageEvent);
  }
}

describe('NotificationWSService cookie-only ticket authentication', () => {
  const realWebSocket = global.WebSocket;

  beforeEach(() => {
    FakeWebSocket.instances = [];
    mockPost.mockReset().mockResolvedValue({ ticket: 'short-lived-ticket' });
    global.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  });

  afterEach(() => {
    global.WebSocket = realWebSocket;
  });

  it('exchanges the HttpOnly-cookie session for a one-time WebSocket ticket', async () => {
    process.env.NEXT_PUBLIC_WS_URL = 'ws://backend.test/api/v1/ws/notifications';
    const service = new NotificationWSService();
    const connecting = service.connect();
    await Promise.resolve();
    FakeWebSocket.instances[0].open();
    await connecting;

    expect(mockPost).toHaveBeenCalledWith('/api/v1/ws/ticket', {});
    expect(FakeWebSocket.instances[0].url).toBe(
      'ws://backend.test/api/v1/ws/notifications?ticket=short-lived-ticket'
    );
    expect(FakeWebSocket.instances[0].url).not.toContain('token=');
    expect(FakeWebSocket.instances[0].url).not.toContain('user_id=');
  });

  it('routes notifications after ticket-authenticated connection', async () => {
    const service = new NotificationWSService();
    const callback = jest.fn();
    service.onNotification(callback);
    const connecting = service.connect();
    await Promise.resolve();
    FakeWebSocket.instances[0].open();
    await connecting;

    const notification = { id: 7, content: 'ready' };
    FakeWebSocket.instances[0].message({ type: 'notification', data: notification });
    expect(callback).toHaveBeenCalledWith(notification);
  });

  it('fails closed when a ticket cannot be issued', async () => {
    mockPost.mockRejectedValueOnce(new Error('unauthorized'));
    const service = new NotificationWSService();
    await expect(service.connect()).rejects.toThrow('unauthorized');
    expect(FakeWebSocket.instances).toHaveLength(0);
  });
});
