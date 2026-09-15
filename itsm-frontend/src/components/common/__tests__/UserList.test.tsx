import { render, screen } from '@/lib/test-utils';
import UserList from '../UserList';
import { httpClient } from '@/lib/api/http-client';

jest.mock('@/lib/api/http-client', () => ({
  httpClient: { get: jest.fn() },
}));

const get = httpClient.get as jest.Mock;

describe('UserList', () => {
  it('renders users from the canonical paged API response in the real table', async () => {
    get.mockResolvedValue({
      users: [{
        id: 7,
        username: 'fixture.agent',
        name: 'Fixture Agent',
        email: 'agent@example.test',
        department: 'IT',
        phone: '1000',
        active: true,
        tenantId: 1,
        role: 'agent',
        createdAt: '2026-09-09T00:00:00.000Z',
        updatedAt: '2026-09-09T00:00:00.000Z',
      }],
      pagination: { page: 1, pageSize: 10, total: 1, totalPages: 1 },
    });

    render(<UserList />);

    expect(await screen.findByText('fixture.agent')).toBeInTheDocument();
    expect(screen.getByText('Fixture Agent')).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith('/api/v1/users', {});
  });
});
