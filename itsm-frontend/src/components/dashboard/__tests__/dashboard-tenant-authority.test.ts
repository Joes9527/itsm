import { normalizeDashboardTicket, normalizeDashboardUser } from '../DashboardOverview';

describe('dashboard tenant authority', () => {
  it('rejects a ticket without a backend tenant identity', () => {
    expect(normalizeDashboardTicket({ id: 1, title: 'missing tenant' } as never)).toBeNull();
  });

  it('rejects a user without a backend tenant identity', () => {
    expect(normalizeDashboardUser({ id: 2, username: 'missing-tenant' } as never)).toBeNull();
  });

  it('rejects users and ticket actors without a backend role', () => {
    expect(
      normalizeDashboardUser({ id: 2, tenantId: 9, username: 'missing-role' } as never)
    ).toBeNull();
    expect(
      normalizeDashboardTicket({
        id: 1,
        tenantId: 9,
        requester: { id: 3, username: 'requester' },
      } as never)
    ).toBeNull();
    expect(
      normalizeDashboardTicket({
        id: 1,
        tenantId: 9,
        assignee: { id: 4, username: 'assignee' },
      } as never)
    ).toBeNull();
  });

  it('preserves a positive backend tenant identity', () => {
    expect(normalizeDashboardTicket({ id: 1, tenantId: 9 } as never)?.tenantId).toBe(9);
    expect(normalizeDashboardUser({ id: 2, tenantId: 9, role: 'agent' } as never)?.tenantId).toBe(
      9
    );
  });
});
