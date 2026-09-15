'use client';
import { useDetailRefresh } from './DetailRefreshContext';
import { Alert, Button } from 'antd';

export function DetailReadState({
  error,
  loading,
  reload,
}: {
  error?: string;
  loading: boolean;
  reload: () => Promise<unknown>;
}) {
  const coordinated = useDetailRefresh() !== undefined;
  return (
    <div className='mb-3 space-y-2'>
      {error && <Alert title={error} type='error' showIcon />}
      {(!!error || !coordinated) && <Button
        aria-label={error ? '重试' : '刷新'}
        size='small'
        loading={loading}
        onClick={() => void reload()}
      >
        {error ? '重试' : '刷新'}
      </Button>}
    </div>
  );
}
