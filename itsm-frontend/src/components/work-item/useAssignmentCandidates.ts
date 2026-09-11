'use client';
import { useEffect, useState } from 'react';
import { UserApi } from '@/lib/api/user-api';

/** Reads every page of the existing authorized directory without changing its tenant scope. */
export function useAssignmentCandidates(enabled: boolean) {
  const [candidates, setCandidates] = useState<Array<{ id: number; label: string }>>([]);
  const [error, setError] = useState<string>();
  useEffect(() => {
    let active = true;
    setCandidates([]);
    setError(undefined);
    if (!enabled) return;
    const load = async () => {
      const collected = new Map<number, { id: number; label: string }>();
      let page = 1,
        totalPages = 1;
      do {
        const result = await UserApi.getUsers({ page, pageSize: 100 });
        if (!active) return;
        if (!Number.isInteger(result.pagination?.totalPages) || result.pagination.totalPages < 0)
          throw new Error('Invalid directory pagination');
        totalPages = result.pagination.totalPages;
        for (const user of result.users)
          if (user.active)
            collected.set(user.id, { id: user.id, label: user.name || user.username });
        page++;
      } while (page <= totalPages);
      if (active) setCandidates([...collected.values()]);
    };
    void load().catch(() => {
      if (active) {
        setCandidates([]);
        setError('无法读取完整授权人员目录，请确认权限后刷新页面');
      }
    });
    return () => {
      active = false;
    };
  }, [enabled]);
  return { candidates, error };
}
