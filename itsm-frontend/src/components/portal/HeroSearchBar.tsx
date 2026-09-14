'use client';

import React from 'react';
import { Button } from 'antd';
import { Search } from 'lucide-react';
import { useRouter } from 'next/navigation';

export function HeroSearchBar() {
  const router = useRouter();

  return (
    <div className="relative w-full max-w-3xl mx-auto my-8">
      <div className="relative flex items-center bg-white dark:bg-slate-900 rounded-2xl shadow-lg border border-slate-200 dark:border-slate-800 p-2">
        <div className="pl-3 pr-2 text-slate-400">
          <Search size={22} aria-hidden="true" />
        </div>
        <input
          type="search"
          aria-label="搜索知识库"
          aria-describedby="portal-search-help"
          disabled
          placeholder="知识搜索暂未开放"
          className="w-full min-w-0 bg-transparent border-none text-slate-500 placeholder-slate-400 text-base px-2 py-1.5 cursor-not-allowed"
        />
      </div>
      <p id="portal-search-help" className="text-xs text-slate-500 mt-2">
        知识搜索暂未开放。您可以直接提交问题或申请服务。
      </p>
      <div className="flex flex-wrap justify-center gap-3 mt-3">
        <Button type="primary" onClick={() => router.push('/tickets/create?entry=help')}>
          提交问题 / 寻求帮助
        </Button>
        <Button onClick={() => router.push('/service-catalog')}>申请服务</Button>
      </div>
    </div>
  );
}
