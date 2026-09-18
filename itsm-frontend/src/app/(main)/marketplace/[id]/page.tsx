'use client';

import React, { useEffect, useMemo, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import { App, Button } from 'antd';
import {
  AlertCircle,
  ArrowLeft,
  Calendar,
  CheckCircle2,
  Code,
  Download,
  Globe,
  Shield,
  Star,
  Tag,
  Terminal,
  User,
} from 'lucide-react';
import { Badge } from '@/components/ui/Badge';
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from '@/components/ui/Card';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/Tabs';
import type {
  MarketplaceItem,
  TenantInstallation,
} from '@/lib/services/marketplace-service';
import marketplaceService from '@/lib/services/marketplace-service';

const typeNames = {
  connector: '连接器',
  skill: '技能',
  plugin: '插件',
};

const formatDate = (value?: string) => (value ? new Date(value).toLocaleDateString() : '暂无');

const normalizeMarkdown = (text?: string) =>
  (text || '暂无详细介绍').split('\n').filter(line => line.trim().length > 0);

const MarketplaceDetailPage = () => {
  const params = useParams();
  const router = useRouter();
  const itemId = Number(params.id);
  const { message, modal } = App.useApp();

  const [item, setItem] = useState<MarketplaceItem | null>(null);
  const [installation, setInstallation] = useState<TenantInstallation | null>(null);
  const [loading, setLoading] = useState(true);
  const [installing, setInstalling] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const versions = useMemo(() => item?.edges?.versions || [], [item]);
  const isInstalled = Boolean(installation);

  // NaN 守卫：验证 itemId 有效性
  useEffect(() => {
    if (!Number.isInteger(itemId) || itemId <= 0) {
      setLoadError('无效的项目 ID');
      setLoading(false);
    }
  }, [itemId]);

  useEffect(() => {
    // 如果 ID 无效，不发起请求
    if (!Number.isInteger(itemId) || itemId <= 0) return;

    let cancelled = false;
    const load = async () => {
      setLoading(true);
      setLoadError(null);
      try {
        const [itemRes, installationRes] = await Promise.all([
          marketplaceService.getItem(itemId),
          marketplaceService.getInstallation(itemId),
        ]);
        if (cancelled) return;
        setItem(itemRes);
        setInstallation(installationRes);
      } catch (error: any) {
        if (cancelled) return;
        console.error('Failed to fetch marketplace item detail:', error);
        setLoadError(error?.message || '加载应用详情失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    if (Number.isFinite(itemId)) {
      load();
    }
    return () => {
      cancelled = true;
    };
  }, [itemId]);

  const handleInstall = async () => {
    setInstalling(true);
    try {
      const installed = await marketplaceService.installItem(itemId);
      setInstallation(installed);
      router.push('/installations');
    } catch (error) {
      console.error('Failed to install item:', error);
      message.error(error instanceof Error ? error.message : '安装失败');
    } finally {
      setInstalling(false);
    }
  };

  const handleUninstall = async () => {
    if (!item) return;
    const confirmed = await modal.confirm({
      title: '确认卸载',
      content: `确定要卸载「${item.title}」吗？卸载后相关功能将无法使用。`,
      okText: '确认',
      cancelText: '取消',
    });
    if (!confirmed) return;
    try {
      await marketplaceService.uninstallItem(itemId);
      setInstallation(null);
      message.success('卸载成功');
    } catch (error) {
      console.error('Failed to uninstall item:', error);
      message.error(error instanceof Error ? error.message : '卸载失败');
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600" />
      </div>
    );
  }

  if (loadError || !item) {
    return (
      <div className="container mx-auto px-4 py-6">
        <div className="rounded-[8px] border border-border bg-surface p-[24px] text-center shadow-none">
          <AlertCircle className="h-12 w-12 text-red-400 mb-4 mx-auto" />
          <h3 className="mb-2 text-[15px] font-semibold text-foreground">应用不存在</h3>
          <p className="mb-4 text-[12px] text-muted">{loadError || '您访问的应用可能已被下架或删除'}</p>
          <Link href="/marketplace">
            <Button type="primary">返回应用市场</Button>
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="container mx-auto px-4 py-6">
      <div className="mb-6 flex items-center gap-2 text-[13px]">
        <Link href="/marketplace" className="flex items-center gap-1 text-muted hover:text-foreground">
          <ArrowLeft className="h-4 w-4" />
          返回市场
        </Link>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div className="lg:col-span-2 space-y-6">
          <Card className="rounded-[8px] border border-border bg-surface text-[13px] text-foreground shadow-none">
            <CardHeader className="p-[16px]">
              <div className="flex flex-col md:flex-row md:items-start gap-4">
                <div className="flex h-16 w-16 items-center justify-center overflow-hidden rounded-[6px] bg-raised">
                  {item.iconUrl ? (
                    <img src={item.iconUrl} alt={item.title} className="w-full h-full object-cover" />
                  ) : (
                    <span className="text-[13px]">{typeNames[item.type]}</span>
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex flex-wrap items-center gap-3 mb-1">
                    <h1 className="text-[24px] font-semibold">{item.title}</h1>
                    <Badge variant="secondary">{typeNames[item.type]}</Badge>
                    {item.isOfficial && <Badge className="bg-blue-100 text-blue-800 hover:bg-blue-200">官方</Badge>}
                    {item.isFree ? (
                      <Badge className="bg-green-100 text-green-800 hover:bg-green-200">免费</Badge>
                    ) : (
                      <Badge className="bg-orange-100 text-orange-800 hover:bg-orange-200">¥{item.price || 0}</Badge>
                    )}
                  </div>
                  <p className="text-muted">{item.description || '暂无描述'}</p>
                  <div className="mt-3 flex flex-wrap items-center gap-4 text-[13px] text-muted">
                    <span className="flex items-center gap-1">
                      <Star className="h-4 w-4 fill-yellow-400 text-yellow-400" />
                      {(item.rating || 0).toFixed(1)}
                    </span>
                    <span className="flex items-center gap-1">
                      <Download className="h-4 w-4" />
                      {item.installCount || 0} 次安装
                    </span>
                    <span className="flex items-center gap-1">
                      <User className="h-4 w-4" />
                      作者：{item.authorName || item.provider}
                    </span>
                    <span className="flex items-center gap-1">
                      <Calendar className="h-4 w-4" />
                      更新于 {formatDate(item.updatedAt)}
                    </span>
                  </div>
                </div>
                <div className="md:text-right">
                  {isInstalled ? (
                    <div className="space-y-2">
                      <div className="flex items-center md:justify-end gap-1 text-green-600">
                        <CheckCircle2 className="h-5 w-5" />
                        <span>已安装</span>
                      </div>
                      <div className="flex gap-2">
                        <Button type="primary" onClick={() => router.push('/installations')}>管理</Button>
                        <Button type="primary" danger onClick={handleUninstall}>卸载</Button>
                      </div>
                    </div>
                  ) : (
                    <Button type="primary" size="large" onClick={handleInstall} disabled={installing}>
                      {installing ? '安装中...' : '立即安装'}
                    </Button>
                  )}
                  <p className="mt-2 text-[12px] text-muted">版本 {item.latestVersion || '1.0.0'}</p>
                </div>
              </div>
            </CardHeader>
          </Card>

          <Card className="rounded-[8px] border border-border bg-surface text-[13px] text-foreground shadow-none">
            <Tabs defaultValue="description">
              <CardHeader className="p-[16px] pb-[8px]">
                <TabsList>
                  <TabsTrigger value="description">详情介绍</TabsTrigger>
                  <TabsTrigger value="capabilities">功能特性</TabsTrigger>
                  <TabsTrigger value="permissions">权限说明</TabsTrigger>
                  <TabsTrigger value="versions">版本历史</TabsTrigger>
                </TabsList>
              </CardHeader>
              <CardContent className="px-[16px] pb-[16px]">
                <TabsContent value="description" className="mt-0 space-y-4">
                  <div className="prose prose-sm max-w-none">
                    {normalizeMarkdown(item.longDescription || item.description).map((line, index) => (
                      <p key={index}>{line}</p>
                    ))}
                  </div>
                  {(item.tags || []).length > 0 && (
                    <div>
                      <h3 className="mb-3 text-[15px] font-semibold">相关标签</h3>
                      <div className="flex flex-wrap gap-2">
                        {(item.tags || []).map(tag => (
                          <Badge key={tag} variant="outline">
                            <Tag className="h-3 w-3 mr-1" />
                            {tag}
                          </Badge>
                        ))}
                      </div>
                    </div>
                  )}
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    <InfoLink icon={<Globe className="h-5 w-5 text-muted mt-0.5" />} label="官方网站" value={item.homepage} />
                    <InfoLink icon={<Code className="h-5 w-5 text-muted mt-0.5" />} label="代码仓库" value={item.repository} />
                    <InfoText icon={<Shield className="h-5 w-5 text-muted mt-0.5" />} label="开源协议" value={item.license || '未声明'} />
                    <InfoText icon={<Terminal className="h-5 w-5 text-muted mt-0.5" />} label="最低系统版本" value={item.minSystemVersion || '未声明'} />
                  </div>
                </TabsContent>

                <TabsContent value="capabilities" className="mt-0">
                  <h3 className="mb-3 text-[15px] font-semibold">支持的功能特性</h3>
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                    {(item.capabilities || []).map(capability => (
                      <div key={capability} className="flex items-center gap-2 rounded-[6px] bg-raised p-2">
                        <CheckCircle2 className="h-5 w-5 text-green-500 flex-shrink-0" />
                        <span>{capability}</span>
                      </div>
                    ))}
                  </div>
                </TabsContent>

                <TabsContent value="permissions" className="mt-0">
                  <h3 className="mb-3 text-[15px] font-semibold">需要的系统权限</h3>
                  <div className="space-y-3">
                    {(item.requiredPermissions || []).map(permission => (
                      <div key={permission} className="flex items-start gap-2 rounded-[6px] bg-raised p-2">
                        <Shield className="h-5 w-5 text-blue-500 flex-shrink-0 mt-0.5" />
                        <span>{permission}</span>
                      </div>
                    ))}
                    {(item.requiredPermissions || []).length === 0 && <p className="text-[13px] text-muted">暂无额外权限说明</p>}
                  </div>
                </TabsContent>

                <TabsContent value="versions" className="mt-0">
                  <h3 className="mb-3 text-[15px] font-semibold">版本历史</h3>
                  <div className="space-y-4">
                    {versions.length > 0 ? versions.map(version => (
                      <div key={version.version} className="border-l-2 border-border pb-4 pl-4">
                        <div className="flex items-center justify-between mb-1">
                          <div className="font-medium">v{version.version}</div>
                          <div className="text-[13px] text-muted">{formatDate(version.releasedAt)}</div>
                        </div>
                        <p className="whitespace-pre-line text-[13px] text-muted">{version.changelog || '暂无更新说明'}</p>
                      </div>
                    )) : <p className="text-[13px] text-muted">暂无版本历史</p>}
                  </div>
                </TabsContent>
              </CardContent>
            </Tabs>
          </Card>
        </div>

        <div className="space-y-6">
          <Card className="rounded-[8px] border border-border bg-surface text-[13px] text-foreground shadow-none">
            <CardHeader className="p-[16px] pb-[8px]">
              <CardTitle className="text-[15px] font-semibold">安装信息</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 px-[16px] pb-[16px]">
              <InfoRow label="最新版本" value={item.latestVersion || '1.0.0'} />
              <InfoRow label="发布日期" value={formatDate(versions[0]?.releasedAt || item.updatedAt)} />
              <InfoRow label="最低系统要求" value={item.minSystemVersion || '未声明'} />
              <InfoRow label="安装量" value={String(item.installCount || 0)} />
              <div>
                <div className="mb-1 text-[13px] font-medium">分类</div>
                <Badge variant="secondary">{item.category || '未分类'}</Badge>
              </div>
            </CardContent>
            <CardFooter className="border-t border-border p-[16px]">
              {isInstalled ? (
                <Button type="primary" className="w-full" onClick={() => router.push('/installations')}>管理已安装应用</Button>
              ) : (
                <Button type="primary" className="w-full" size="large" onClick={handleInstall} disabled={installing}>
                  {installing ? '安装中...' : '立即安装'}
                </Button>
              )}
            </CardFooter>
          </Card>

          <Card className="rounded-[8px] border border-border bg-surface text-[13px] text-foreground shadow-none">
            <CardHeader className="p-[16px] pb-[8px]">
              <CardTitle className="text-[15px] font-semibold">提供商信息</CardTitle>
            </CardHeader>
            <CardContent className="px-[16px] pb-[16px]">
              <div className="flex items-center gap-3 mb-4">
                <div className="w-10 h-10 rounded-full bg-blue-100 flex items-center justify-center text-blue-600 font-medium">
                  {(item.provider || item.authorName || item.title).charAt(0)}
                </div>
                <div>
                  <div className="font-medium">{item.provider || item.authorName || '未知提供商'}</div>
                  {item.isOfficial && <Badge className="mt-1 bg-blue-100 text-blue-800 hover:bg-blue-200">官方认证</Badge>}
                </div>
              </div>
              <p className="text-[13px] text-muted">
                {item.isOfficial ? '由ITSM官方团队开发和维护，确保兼容性和安全性。' : '由社区开发者贡献，经过官方审核验证。'}
              </p>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
};

const InfoRow = ({ label, value }: { label: string; value: string }) => (
  <div>
    <div className="mb-1 text-[13px] font-medium">{label}</div>
    <div className="text-muted">{value}</div>
  </div>
);

const InfoText = ({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) => (
  <div className="flex items-start gap-2">
    {icon}
    <div>
      <div className="text-[13px] font-medium">{label}</div>
      <div className="break-all text-[13px] text-muted">{value}</div>
    </div>
  </div>
);

const InfoLink = ({ icon, label, value }: { icon: React.ReactNode; label: string; value?: string }) => (
  <div className="flex items-start gap-2">
    {icon}
    <div>
      <div className="text-[13px] font-medium">{label}</div>
      {value ? (
        <a href={value} target="_blank" rel="noopener noreferrer" className="break-all text-[13px] text-blue-600 hover:underline">
          {value}
        </a>
      ) : (
        <div className="text-[13px] text-muted">未提供</div>
      )}
    </div>
  </div>
);

export default MarketplaceDetailPage;
