'use client';

import React, { useEffect, useState } from 'react';
import { Empty } from 'antd';
import { Server, ExternalLink } from 'lucide-react';
import Link from 'next/link';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { CMDBApi } from '@/lib/api/cmdb-api';

interface CIContextCardProps {
  ticketId: number;
  source?: string;
}

/**
 * 工单详情右侧工具箱：关联 CMDB 配置项（CI）卡片。
 * 仅服务目录来源的工单有 ciId（通过 ServiceRequest 关联），
 * 其余来源不渲染；数据复用 ServiceCatalogApi + CMDBApi.getCITopology。
 */
export const CIContextCard: React.FC<CIContextCardProps> = ({ ticketId, source }) => {
  const [ciId, setCiId] = useState<number | null>(null);
  const [topology, setTopology] = useState<{ totalNodes: number; totalEdges: number } | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (source !== 'service_catalog') {
      setLoading(false);
      return;
    }

    let cancelled = false;
    setLoading(true);

    (async () => {
      try {
        const request = await ServiceCatalogApi.getServiceRequestByTicketId(ticketId);
        if (cancelled) return;
        const id = request?.ciId ?? null;
        setCiId(id);
        if (id) {
          try {
            const topo = await CMDBApi.getCITopology(id, 3);
            if (!cancelled) {
              setTopology({ totalNodes: topo?.totalNodes ?? 0, totalEdges: topo?.totalEdges ?? 0 });
            }
          } catch {
            if (!cancelled) setTopology(null);
          }
        }
      } catch {
        if (!cancelled) setCiId(null);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [ticketId, source]);

  if (source !== 'service_catalog') return null;
  if (loading) return null;

  return (
    <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-3 text-xs">
      <span className="font-bold text-slate-800 block border-b border-slate-100 pb-2 text-xs flex items-center gap-1.5">
        <Server size={14} className="text-slate-500" />
        关联 CMDB 配置项
      </span>

      {ciId ? (
        <div className="space-y-2">
          <Link
            href={`/cmdb/cis/${ciId}`}
            className="inline-flex items-center gap-1 text-slate-700 hover:text-orange-600 font-medium transition-colors"
          >
            CI #{ciId}
            <ExternalLink size={12} />
          </Link>
          <div className="flex items-center justify-between text-[11px] text-slate-500">
            <span>拓扑节点: {topology ? topology.totalNodes : '--'}</span>
            <span>拓扑关系: {topology ? topology.totalEdges : '--'}</span>
          </div>
        </div>
      ) : (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="无关联 CI" />
      )}
    </div>
  );
};

export default CIContextCard;
