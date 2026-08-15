import { useCallback, useEffect, useState } from 'react';
import { UserApi } from '@/lib/api/user-api';
import { useAuthStore } from '@/lib/store/auth-store';
import { ticketTemplateService } from '@/lib/services/ticket-template-service';
import type { TicketTemplate, User } from '../types';

interface TicketModalDataState {
  users: User[];
  templates: TicketTemplate[];
  loading: boolean;
  error: Error | null;
  reload: () => Promise<void>;
}

/**
 * Feature data hook for the ticket editor. It owns remote data loading while
 * keeping the form components controlled and free of transport concerns.
 */
export function useTicketModalData(enabled = true): TicketModalDataState {
  const hasPermission = useAuthStore(s => s.hasPermission);
  const [users, setUsers] = useState<User[]>([]);
  const [templates, setTemplates] = useState<TicketTemplate[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const reload = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // 无 user:read 权限时不加载用户列表（避免 403），仅加载模板
      const [userResponse, templateResponse] = await Promise.all([
        hasPermission('user:read')
          ? UserApi.getUsers({ page: 1, pageSize: 100 })
          : Promise.resolve({ users: [] }),
        ticketTemplateService.getTemplates({ page: 1, pageSize: 100, isActive: true }),
      ]);

      setUsers(
        (userResponse.users ?? []).map(user => ({
          id: user.id,
          name: user.name || user.username,
          role: user.department,
          avatar: (user.name || user.username || '?').slice(0, 1).toUpperCase(),
        }))
      );
      setTemplates(
        (templateResponse.templates ?? []).map(template => ({
          id: template.id,
          name: template.name,
          description: template.description,
          category: template.category,
        }))
      );
    } catch (cause) {
      setUsers([]);
      setTemplates([]);
      setError(cause instanceof Error ? cause : new Error('加载工单表单数据失败'));
    } finally {
      setLoading(false);
    }
  }, [hasPermission]);

  useEffect(() => {
    if (enabled) void reload();
  }, [enabled, reload]);

  return { users, templates, loading, error, reload };
}
