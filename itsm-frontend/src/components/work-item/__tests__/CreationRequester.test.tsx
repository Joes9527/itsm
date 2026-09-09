import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Form } from 'antd';
import { CreationRequester } from '../CreationRequester';
import { useAuthStore } from '@/lib/store/auth-store';
import { UserApi } from '@/lib/api/user-api';

jest.mock('@/lib/api/user-api', () => ({ UserApi: { getUsers: jest.fn() } }));
const getUsers = UserApi.getUsers as jest.Mock;
function Fixture({ submit }: { submit: jest.Mock }) {
  return (
    <Form onFinish={submit}>
      <CreationRequester resource='problem' />
      <button type='submit'>提交</button>
    </Form>
  );
}
const session = {
  id: 42,
  username: 'operator',
  name: 'Operator',
  email: '',
  tenantId: 7,
  actorTenantId: 7,
  role: 'end_user',
  permissions: [],
};
describe('requester selection from verified native actor tenant', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    useAuthStore.setState({
      user: session,
      currentTenant: {
        id: 7,
        code: 'customer',
        name: 'Customer',
        type: 'standard',
        status: 'active',
      } as never,
      isAuthenticated: true,
    });
    getUsers.mockResolvedValue({
      users: [
        { id: 50, name: 'Customer requester', tenantId: 7, active: true },
        { id: 51, name: 'Inactive requester', tenantId: 7, active: false },
        { id: 52, name: 'Foreign requester', tenantId: 8, active: true },
      ],
    });
  });
  it('allows same-tenant default without user:read or a directory fetch', async () => {
    const submit = jest.fn();
    render(<Fixture submit={submit} />);
    fireEvent.click(screen.getByText('提交'));
    await waitFor(() => expect(submit).toHaveBeenCalled());
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(getUsers).not.toHaveBeenCalled();
  });
  it('requires explicit active customer requester even though user.tenantId is already selected tenant', async () => {
    useAuthStore.setState({
      user: {
        ...session,
        actorTenantId: 2,
        role: 'msp_tech',
        mspRole: 'provider_agent',
        permissions: ['problem:create_on_behalf', 'user:read'],
      },
    });
    const submit = jest.fn();
    render(<Fixture submit={submit} />);
    fireEvent.click(screen.getByText('提交'));
    expect(await screen.findByText('请选择当前客户租户的申请人')).toBeInTheDocument();
    expect(submit).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(getUsers).toHaveBeenCalledWith(
        expect.objectContaining({ tenantId: 7, status: 'active' })
      )
    );
    fireEvent.mouseDown(screen.getByRole('combobox'));
    fireEvent.click(await screen.findByText('Customer requester'));
    expect(screen.queryByText('Inactive requester')).not.toBeInTheDocument();
    expect(screen.queryByText('Foreign requester')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('提交'));
    await waitFor(() => expect(submit).toHaveBeenCalledWith({ requesterId: 50 }));
    act(() =>
      useAuthStore.setState({
        currentTenant: {
          id: 8,
          code: 'next',
          name: 'Next',
          type: 'standard',
          status: 'active',
        } as never,
        user: {
          ...session,
          tenantId: 8,
          actorTenantId: 2,
          role: 'msp_tech',
          permissions: ['problem:create_on_behalf', 'user:read'],
        },
      })
    );
    submit.mockClear();
    fireEvent.click(screen.getByText('提交'));
    expect(await screen.findByText('请选择当前客户租户的申请人')).toBeInTheDocument();
    expect(submit).not.toHaveBeenCalled();
  });
});

it('does not offer delegation when a same-tenant user can read users but cannot create on behalf', async () => {
  useAuthStore.setState({
    user: { ...session, role: 'sysadmin', permissions: ['user:read', 'problem:write'] },
    currentTenant: { id: 7 } as never,
    isAuthenticated: true,
  });
  const submit = jest.fn();
  render(<Fixture submit={submit} />);
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  expect(screen.getByText(/当前申请将以你本人作为申请人/)).toBeInTheDocument();
  fireEvent.click(screen.getByText('提交'));
  await waitFor(() => expect(submit).toHaveBeenCalled());
  expect(submit.mock.calls[0][0].requesterId).toBeUndefined();
});
it('blocks customer-tenant creation without delegation permission instead of silently using the MSP actor', async () => {
  useAuthStore.setState({
    user: { ...session, actorTenantId: 2, permissions: ['user:read'] },
    currentTenant: { id: 7 } as never,
    isAuthenticated: true,
  });
  const submit = jest.fn();
  render(<Fixture submit={submit} />);
  fireEvent.click(screen.getByText('提交'));
  expect(await screen.findAllByText(/缺少代他人申请权限/)).not.toHaveLength(0);
  expect(submit).not.toHaveBeenCalled();
});
it.each(['admin', 'msp_tech'])(
  'does not infer problem delegation from role %s or another domain grant',
  role => {
    useAuthStore.setState({
      user: { ...session, role, permissions: ['user:read', 'incident:create_on_behalf'] },
      currentTenant: { id: 7 } as never,
      isAuthenticated: true,
    });
    render(<Fixture submit={jest.fn()} />);
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  }
);
it.each(['problem:create_on_behalf', 'problem:*', '*:create_on_behalf', '*:*'])(
  'offers selection for the matching permission %s',
  permission => {
    useAuthStore.setState({
      user: { ...session, permissions: ['user:read', permission] },
      currentTenant: { id: 7 } as never,
      isAuthenticated: true,
    });
    render(<Fixture submit={jest.fn()} />);
    expect(screen.getByRole('combobox')).toBeInTheDocument();
  }
);
it('clears a selected delegate when delegation permission is revoked', async () => {
  useAuthStore.setState({
    user: { ...session, permissions: ['user:read', 'problem:create_on_behalf'] },
    currentTenant: { id: 7 } as never,
    isAuthenticated: true,
  });
  getUsers.mockResolvedValue({
    users: [{ id: 50, name: 'Customer requester', tenantId: 7, active: true }],
  });
  const submit = jest.fn();
  render(<Fixture submit={submit} />);
  fireEvent.mouseDown(screen.getByRole('combobox'));
  fireEvent.click(await screen.findByText('Customer requester'));
  act(() => useAuthStore.setState({ user: { ...session, permissions: ['user:read'] } }));
  fireEvent.click(screen.getByText('提交'));
  await waitFor(() => expect(submit).toHaveBeenCalled());
  expect(submit.mock.calls[0][0].requesterId).toBeUndefined();
});
