import { render, screen, waitFor } from '@/lib/test-utils';
import MSPManagementPage from '../page';
import MSPService from '@/lib/services/msp-service';
import { UserApi } from '@/lib/api/user-api';

jest.mock('@/lib/services/msp-service', () => ({
  __esModule: true,
  default: {
    isMSPUser: jest.fn(),
    getAllocations: jest.fn(),
    getCustomers: jest.fn(),
    createAllocation: jest.fn(),
    deallocate: jest.fn(),
  },
}));

jest.mock('@/lib/api/user-api', () => ({
  UserApi: { getUsers: jest.fn() },
}));

const isMSPUser = MSPService.isMSPUser as jest.Mock;
const getAllocations = MSPService.getAllocations as jest.Mock;
const getCustomers = MSPService.getCustomers as jest.Mock;
const getUsers = UserApi.getUsers as jest.Mock;

describe('MSPManagementPage access loading', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    getAllocations.mockResolvedValue({ allocations: [], total: 0 });
    getCustomers.mockResolvedValue({ customers: [], total: 0 });
    getUsers.mockResolvedValue({ users: [], pagination: {} });
  });

  it('loads all management reads after authorized access resolves', async () => {
    isMSPUser.mockResolvedValue({ isMSP: true, isAdmin: false });

    render(<MSPManagementPage />);

    expect(await screen.findByText('MSP 分配管理')).toBeInTheDocument();
    await waitFor(() => {
      expect(getAllocations).toHaveBeenCalledTimes(1);
      expect(getCustomers).toHaveBeenCalledTimes(1);
      expect(getUsers).toHaveBeenCalledWith({ page: 1, pageSize: 200 });
    });
    expect(screen.queryByText('您没有权限访问此页面')).not.toBeInTheDocument();
  });

  it('shows denied access without loading management data', async () => {
    isMSPUser.mockResolvedValue({ isMSP: false, isAdmin: false });

    render(<MSPManagementPage />);

    expect(await screen.findByText('您没有权限访问此页面')).toBeInTheDocument();
    expect(getAllocations).not.toHaveBeenCalled();
    expect(getCustomers).not.toHaveBeenCalled();
    expect(getUsers).not.toHaveBeenCalled();
  });
});
