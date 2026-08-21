import React from 'react';
import '@testing-library/jest-dom';

// Setup mocks before importing component
jest.mock('next/navigation', () => {
  const actual = jest.requireActual('next/navigation');
  return {
    ...actual,
    useParams: () => ({ id: '24' }),
    useRouter: () => ({ push: jest.fn() }),
  };
});

jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: (selector: (state: any) => any) =>
    selector({ user: { name: '测试用户', email: 'test@example.com' } }),
}));

const mockGet = jest.fn();
jest.mock('@/lib/api/http-client', () => ({
  httpClient: {
    get: (...args: unknown[]) => mockGet(...args),
  },
}));

jest.mock('@/lib/api/service-catalog-api', () => ({
  ServiceCatalogApi: { createServiceRequest: jest.fn() },
}));

// Mock DatePicker to avoid dayjs compatibility issues in tests
jest.mock('antd', () => {
  const actual = jest.requireActual('antd');
  return {
    ...actual,
    DatePicker: () => React.createElement('div', { 'data-testid': 'mock-date-picker' }),
  };
});

// Import component after mocks are set up
import { render, screen, waitFor } from '@testing-library/react';
import ServiceCatalogRequestPage from '../page';

describe('ServiceCatalogRequestPage', () => {
  afterEach(() => {
    mockGet.mockReset();
  });

  it('不渲染基础设施字段组：requiresInfraFields=false（如 Copilot 采购申请）', async () => {
    mockGet.mockResolvedValue({
      data: {
        id: 24,
        name: 'Copilot采购申请',
        requiresInfraFields: false,
        fields: [],
      },
    });

    render(<ServiceCatalogRequestPage />);

    await waitFor(() => expect(screen.getByText('申请标题')).toBeInTheDocument());

    expect(screen.queryByText('成本中心')).not.toBeInTheDocument();
    expect(screen.queryByText('数据分级')).not.toBeInTheDocument();
    expect(screen.queryByText('需要公网 IP')).not.toBeInTheDocument();
  });

  it('渲染基础设施字段组：requiresInfraFields=true（如云服务器申请）', async () => {
    mockGet.mockResolvedValue({
      data: {
        id: 5,
        name: '云服务器申请',
        requiresInfraFields: true,
        fields: [],
      },
    });

    render(<ServiceCatalogRequestPage />);

    await waitFor(() => expect(screen.getByText('成本中心')).toBeInTheDocument());

    expect(screen.getByText('数据分级')).toBeInTheDocument();
    expect(screen.getByText('需要公网 IP')).toBeInTheDocument();
  });
});
