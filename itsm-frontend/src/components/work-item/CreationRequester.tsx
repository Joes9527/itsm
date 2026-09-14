'use client';
import { useEffect, useState } from 'react';
import { Alert, Form, Select } from 'antd';
import { useAuthStore } from '@/lib/store/auth-store';
import { matchesPermission } from '@/lib/permissions/utils';
import { UserApi, type User } from '@/lib/api/user-api';

type CreationResource = 'ticket' | 'incident' | 'problem' | 'change' | 'service_request';

export function CreationRequester({ resource }: { resource: CreationResource }) {
  const tenantId = useAuthStore(state => state.currentTenant?.id);
  const user = useAuthStore(state => state.user);
  const [users, setUsers] = useState<User[]>([]);
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const form = Form.useFormInstance();
  const required = !!user && user.actorTenantId !== tenantId;
  const permissions = new Set(user?.permissions || []);
  const canDelegate =
    user?.role === 'super_admin' || matchesPermission(permissions, resource, 'create_on_behalf');
  const canReadUsers =
    user?.role === 'super_admin' || matchesPermission(permissions, 'user', 'read');
  const canSelect = canDelegate && canReadUsers;
  const blockedReason = !canDelegate
    ? `缺少代他人申请权限（${resource}:create_on_behalf）`
    : '缺少申请人目录读取权限（user:read）';
  useEffect(() => {
    form.setFieldValue('requesterId', undefined);
  }, [tenantId, user?.id, resource, canSelect, form]);
  useEffect(() => {
    let cancelled = false;
    setUsers([]);
    setError('');
    if (!canSelect || !tenantId) return;
    const timer = setTimeout(() => {
      UserApi.getUsers({ tenantId, status: 'active', search, page: 1, pageSize: 100 })
        .then(result => {
          if (!cancelled)
            setUsers(
              result.users.filter(candidate => candidate.active && candidate.tenantId === tenantId)
            );
        })
        .catch(() => {
          if (!cancelled) setError('无法读取当前租户申请人，请检查权限后重试');
        });
    }, 200);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [tenantId, canSelect, search]);
  if (!canSelect)
    return required ? (
      <>
        <Alert type='error' title={blockedReason} />
        <Form.Item
          name='requesterId'
          hidden
          rules={[{ validator: () => Promise.reject(new Error(blockedReason)) }]}
        >
          <Select />
        </Form.Item>
      </>
    ) : (
      <p>当前申请将以你本人作为申请人；{blockedReason}，不能选择其他申请人。</p>
    );
  return (
    <>
      {error && <Alert type='error' title={error} />}
      <Form.Item
        name='requesterId'
        label='申请人'
        extra={
          required ? '请选择当前客户租户的有效申请人' : '留空由服务器使用当前用户；代他人申请需授权'
        }
        rules={[{ required, message: '请选择当前客户租户的申请人' }]}
      >
        <Select
          allowClear
          showSearch
          filterOption={false}
          onSearch={setSearch}
          placeholder='选择当前租户申请人'
          options={users.map(candidate => ({
            value: candidate.id,
            label: candidate.name || candidate.username,
          }))}
        />
      </Form.Item>
    </>
  );
}
