import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { HeroSearchBar } from '../HeroSearchBar';
import { KnowledgeBaseApi } from '@/lib/api/knowledge-base-api';

const mockPush = jest.fn();
jest.mock('next/navigation', () => ({ useRouter: () => ({ push: mockPush }) }));
jest.mock('@/lib/api/knowledge-base-api', () => ({ KnowledgeBaseApi: { getArticles: jest.fn(), search: jest.fn() } }));
jest.mock('@/lib/store/auth-store', () => ({ useAuthStore: () => ({ user: { id: 1, tenantId: 1, permissions: ['knowledge:read'] }, currentTenant: { id: 1, status: 'active' }, isAuthenticated: true, hasPermission: () => true }) }));
beforeEach(() => { jest.clearAllMocks(); });

test('keeps direct help and service requests reachable without search', () => {
  render(<HeroSearchBar />);
  fireEvent.click(screen.getByRole('button', { name: '提交问题 / 寻求帮助' }));
  expect(mockPush).toHaveBeenCalledWith('/tickets/create?entry=help');
  fireEvent.click(screen.getByRole('button', { name: '申请服务' }));
  expect(mockPush).toHaveBeenCalledWith('/service-catalog');
});
test('clearly marks knowledge search unavailable without calling a provider', () => {
  render(<HeroSearchBar />);
  const input = screen.getByRole('searchbox', { name: '搜索知识库' });
  expect(input).toBeDisabled();
  expect(input).toHaveAccessibleDescription('知识搜索暂未开放。您可以直接提交问题或申请服务。');
  expect(KnowledgeBaseApi.getArticles).not.toHaveBeenCalled();
  expect(KnowledgeBaseApi.search).not.toHaveBeenCalled();
  expect(screen.queryByText(/92%|已为您记录自愈成功|AI 智能自愈与推荐建议/)).not.toBeInTheDocument();
});
