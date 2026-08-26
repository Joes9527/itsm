'use client';

import React, { useEffect, useState } from 'react';
import { Empty } from 'antd';
import { Server, ExternalLink } from 'lucide-react';
import Link from 'next/link';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { CMDBApi } from '@/lib/api/cmdb-api';
import { CIType } from '@/types/cmdb';

interface CIContextCardProps {
  ticketId: number;
  source?: string;
}

const CI_TYPE_LABELS: Record<string, string> = {
  [CIType.SERVER]: '服务器',
  [CIType.APPLICATION]: '应用集群',
  [CIType.DATABASE]: '数据库',
  [CIType.MIDDLEWARE]: '中间件',
  [CIType.BUSINESS_SERVICE]: '业务服务',
  [CIType.TECHNICAL_SERVICE]: '技术服务',
  [CIType.NETWORK_DEVICE]: '网络设备',
  [CIType.STORAGE]: '存储',
  [CIType.OPERATING_SYSTEM]: '操作系统',
};

/**
 * 工单详情右侧工具箱：关联 CMDB 配置项（CI）卡片。
 * 仅服务目录来源的工单有 ciId（通过 ServiceRequest 关联），其余来源不渲染。
 * 样式对齐 prototype：CI 名称 + 类型 chip + 描述 + 拓扑图入口。
 */
export const CIContextCard: React.FC<CIContextCardProps> = ({ ticketId, source }) => {
  const [ciId, setCiId] = useState<number | null>(null);
  const [ci, setCi] = useState<{ name?: string; type?: string; description?: string } | null>(null);
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
            const [ciDetail, topo] = await Promise.all([
              CMDBApi.getCI(id),
              CMDBApi.getCITopology(id, 3),
            ]);
            if (cancelled) return;
            setCi({
              name: ciDetail?.name,
              type: ciDetail?.type ? String(ciDetail.type) : undefined,
              description: ciDetail?.description,
            });
            setTopology({ totalNodes: topo?.totalNodes ?? 0, totalEdges: topo?.totalEdges ?? 0 });
          } catch {
            if (!cancelled) {
              setCi(null);
              setTopology(null);
            }
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
      <div className="flex items-center justify-between border-b border-slate-100 pb-2">
        <span className="font-bold text-slate-800 flex items-center gap-1.5 text-xs">
          <Server size={14} className="text-slate-500" />
          关联配置项 (CI)
        </span>
        {ciId && (
          <Link
            href={`/cmdb/cis/${ciId}`}
            className="text-[11px] text-slate-600 hover:text-orange-600 hover:underline flex items-center gap-0.5"
          >
            拓扑图 <ExternalLink size={11} />
          </Link>
        )}
      </div>

      {ciId ? (
        <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1.5">
          <div className="flex items-center justify-between font-mono text-slate-800 font-bold text-xs">
            <span className="truncate">{ci?.name || `CI #${ciId}`}</span>
            {ci?.type && (
              <span className="text-[10px] text-slate-600 bg-slate-200 px-1.5 py-0.2 rounded font-normal shrink-0 ml-2">
                {CI_TYPE_LABELS[ci.type] || ci.type}
              </span>
            )}
          </div>
          <p className="text-[11px] text-slate-500 m-0">
            {ci?.description || `关联配置项 CI #${ciId}`}
            {topology ? `，拓扑共 ${topology.totalNodes} 个节点 / ${topology.totalEdges} 条关系` : ''}
          </p>
        </div>
      ) : (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="无关联 CI" />
      )}
    </div>
  );
};

export default CIContextCard;
