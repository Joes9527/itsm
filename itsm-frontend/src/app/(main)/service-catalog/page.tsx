'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';

export default function ServiceCatalogPage() {
  const router = useRouter();
  useEffect(() => {
    router.replace('/tickets/create');
  }, [router]);
  return null;
}
