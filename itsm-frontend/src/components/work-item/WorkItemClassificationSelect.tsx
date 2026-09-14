 'use client';
import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Cascader, Space } from 'antd';
import { useAuthStore } from '@/lib/store/auth-store';
import { TicketCategoryApi, type TicketCategory } from '@/lib/api/ticket-category-api';
import { classificationOptions, classificationPath } from './classification';

interface Props {
  value?: number[];
  onChange?: (value: number[]) => void;
  initialCategoryId?: number;
  id?: string;
}
export function WorkItemClassificationSelect({ value, onChange, initialCategoryId, id }: Props) {
  const tenantId = useAuthStore(state => state.currentTenant?.id);
  const previousTenant = useRef(tenantId);
  const changeRef = useRef(onChange);
  changeRef.current = onChange;
  const [categories, setCategories] = useState<TicketCategory[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let cancelled = false;
    if (previousTenant.current !== tenantId) {
      previousTenant.current = tenantId;
      changeRef.current?.([]);
    }
    setCategories([]);
    setError('');
    if (!tenantId) { setLoading(false); return; }
    setLoading(true);
    TicketCategoryApi.getCategoryTree()
      .then(rows => { if (!cancelled) setCategories(rows); })
      .catch(() => { if (!cancelled) setError('分类加载失败，请重试或检查分类读取权限'); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [tenantId, revision]);
  const selected = value ?? classificationPath(initialCategoryId, categories);
  return <Space orientation="vertical" style={{ width: '100%' }}>
    <Cascader id={id} value={selected} onChange={path => onChange?.(path as number[])}
      options={classificationOptions(categories)} placeholder="选择分类" changeOnSelect allowClear showSearch
      style={{ width: '100%' }} loading={loading} disabled={loading || !!error || !tenantId}
      notFoundContent="暂无可用分类" />
    {error && <Alert type="error" title={error} action={<Button onClick={() => setRevision(v => v + 1)}>重试</Button>} />}
    {!loading && !error && initialCategoryId && value === undefined && !selected
      ? <Alert type="warning" title="原分类已停用或不可见；未重新选择时保留原分类" /> : null}
  </Space>;
}
