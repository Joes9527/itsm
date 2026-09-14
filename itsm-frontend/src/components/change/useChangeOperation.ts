'use client';

import { useRef } from 'react';
import type { ChangeMutationIdentity } from '@/lib/api/change-api';

// Preserve the observed version with the payload after uncertain transport failure.
// A caller clears only after receiving an actual receipt or durable acceptance.
export function useChangeOperation() {
  const attempt = useRef<{ signature: string; identity: ChangeMutationIdentity } | null>(null);
  return {
    identity(target: string, payload: unknown, version: number): ChangeMutationIdentity {
      const signature = JSON.stringify([target, payload]);
      if (!attempt.current || attempt.current.signature !== signature) {
        if (!Number.isInteger(version) || version <= 0) throw new Error('请先加载当前变更版本');
        attempt.current = {
          signature,
          identity: { expectedVersion: version, operationId: crypto.randomUUID() },
        };
      }
      return attempt.current.identity;
    },
    clear() {
      attempt.current = null;
    },
  };
}
