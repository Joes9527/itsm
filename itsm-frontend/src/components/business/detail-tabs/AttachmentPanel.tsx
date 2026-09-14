'use client';

import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Upload, Button, App, Typography, Progress, Modal, Space, Empty, Spin, Alert } from 'antd';
import type { RcFile } from 'antd/es/upload/interface';
import {
  File as FileIcon,
  Image as ImageIcon,
  FileText,
  Music,
  Video,
  Archive,
  Download,
  Eye,
  Trash2,
  Upload as UploadIcon,
} from 'lucide-react';
import type { AttachmentAdapter, AttachmentItem, TargetType } from './types';

const { Text } = Typography;

export interface AttachmentPanelProps {
  targetType: TargetType;
  targetId: number | string;
  adapter: AttachmentAdapter;
  permissions: { canRead: boolean; canUpload: boolean; canDelete: boolean };
  onCountChange?: (count: number | undefined) => void;
  maxSize?: number; // bytes，默认 50MB
  accept?: string;
  currentUserId?: number;
  formatDateTime?: (dateString: string) => string;
}

const defaultFormat = (s: string) => (s ? new Date(s).toLocaleString('zh-CN') : '');

const formatFileSize = (bytes: number): string => {
  if (!bytes || bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return Math.round((bytes / Math.pow(k, i)) * 100) / 100 + ' ' + sizes[i];
};

const getFileIcon = (mimeType: string) => {
  if (!mimeType) return <FileIcon size={20} />;
  if (mimeType.startsWith('image/')) return <ImageIcon size={20} />;
  if (mimeType.startsWith('video/')) return <Video size={20} />;
  if (mimeType.startsWith('audio/')) return <Music size={20} />;
  if (mimeType.includes('pdf') || mimeType.includes('word') || mimeType.includes('excel'))
    return <FileText size={20} />;
  if (mimeType.includes('zip') || mimeType.includes('rar')) return <Archive size={20} />;
  return <FileIcon size={20} />;
};

export const AttachmentPanel: React.FC<AttachmentPanelProps> = ({
  targetId,
  adapter,
  maxSize = 50 * 1024 * 1024,
  accept,
  permissions,
  onCountChange,
  formatDateTime = defaultFormat,
}) => {
  const { message, modal } = App.useApp();
  const busy = useRef(false);
  const [items, setItems] = useState<AttachmentItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [uploadProgress, setUploadProgress] = useState<number>(0);
  const [uploading, setUploading] = useState(false);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [previewName, setPreviewName] = useState<string>('');

  const fetch = useCallback(async () => {
    if (!permissions.canRead) {
      setItems([]);
      onCountChange?.(undefined);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const res = await adapter.list(targetId);
      setItems(res || []);
      onCountChange?.(res.length);
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载附件失败');
    } finally {
      setLoading(false);
    }
  }, [adapter, targetId, permissions.canRead, onCountChange]);

  useEffect(() => {
    void fetch();
  }, [fetch]);

  const beforeUpload = (file: RcFile) => {
    if (!permissions.canUpload || busy.current) return Upload.LIST_IGNORE;
    if (file.size > maxSize) {
      message.error(`文件大小超过 ${formatFileSize(maxSize)} 限制`);
      return Upload.LIST_IGNORE;
    }
    return true;
  };

  const customRequest = async (options: {
    file: File | RcFile | Blob;
    onSuccess?: (response: unknown) => void;
    onError?: (err: Error) => void;
  }) => {
    if (!permissions.canUpload || busy.current) return;
    busy.current = true;
    const file = options.file as File;
    setUploading(true);
    setUploadProgress(0);
    try {
      await adapter.upload(targetId, file, p => setUploadProgress(p));
      options.onSuccess?.({});
      message.success(`${file.name} 上传成功`);
      await fetch();
    } catch (e) {
      const err = e instanceof Error ? e : new Error('上传失败');
      options.onError?.(err);
      message.error(err.message);
    } finally {
      busy.current = false;
      setUploading(false);
      setUploadProgress(0);
    }
  };

  const handleDelete = (item: AttachmentItem) => {
    if (!permissions.canDelete || busy.current) return;
    modal.confirm({
      title: '确认删除',
      content: `确定要删除附件 "${item.fileName}" 吗？`,
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: async () => {
        if (busy.current || !permissions.canDelete) return;
        busy.current = true;
        try {
          await adapter.remove(targetId, item.id);
          message.success('删除成功');
          await fetch();
        } catch (e) {
          message.error(e instanceof Error ? e.message : '删除失败');
        } finally {
          busy.current = false;
        }
      },
    });
  };

  const handleDownload = (item: AttachmentItem) => {
    const url = adapter.getDownloadUrl(targetId, item.id);
    // 通过 <a download> 触发浏览器下载
    const a = document.createElement('a');
    a.href = url;
    a.download = item.fileName;
    a.target = '_blank';
    a.rel = 'noreferrer';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  };

  const handlePreview = (item: AttachmentItem) => {
    if (!adapter.getPreviewUrl) {
      message.warning('该附件不支持预览');
      return;
    }
    setPreviewUrl(adapter.getPreviewUrl(targetId, item.id));
    setPreviewName(item.fileName);
  };

  const isPreviewable = (mime: string) =>
    mime.startsWith('image/') || mime.includes('pdf') || mime.startsWith('text/');

  if (!permissions.canRead) return <Alert title='无权读取附件' type='error' showIcon />;

  if (loading && items.length === 0) {
    return (
      <div className='p-6 text-center'>
        <Spin />
      </div>
    );
  }

  return (
    <div className='p-6'>
      {error && (
        <Alert
          message={error}
          type='error'
          showIcon
          closable
          className='mb-4'
          onClose={() => setError(null)}
          action={
            <Button size='small' type='link' onClick={() => void fetch()}>
              重试
            </Button>
          }
        />
      )}

      {permissions.canUpload && (
        <div className='mb-6'>
          <Upload
            disabled={uploading}
            showUploadList={false}
            beforeUpload={beforeUpload}
            customRequest={customRequest as never}
            accept={accept}
          >
            <Button icon={<UploadIcon size={14} />} loading={uploading}>
              上传附件
            </Button>
          </Upload>
          {uploading && (
            <div className='mt-3'>
              <Progress percent={Math.round(uploadProgress)} size='small' />
            </div>
          )}
          <Text type='secondary' className='ml-3 text-xs'>
            单文件最大 {formatFileSize(maxSize)}
          </Text>
        </div>
      )}

      {items.length === 0 ? (
        <Empty description='暂无附件' />
      ) : (
        <div className='grid grid-cols-1 sm:grid-cols-2 gap-3'>
          {items.map(item => (
            <div
              key={item.id}
              className='min-w-0 rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 p-3 space-y-2'
            >
              <div className='flex items-center gap-2 min-w-0'>
                {getFileIcon(item.mimeType)}
                <span className='break-all text-xs font-medium'>{item.fileName}</span>
              </div>
              <Text type='secondary' className='text-xs block'>
                {formatFileSize(item.fileSize)} ·{' '}
                {item.uploader?.name || item.uploader?.username || '未知'} ·{' '}
                {formatDateTime(item.createdAt)}
              </Text>
              <Space wrap size='small'>
                {adapter.getPreviewUrl && isPreviewable(item.mimeType) && (
                  <Button size='small' icon={<Eye size={14} />} onClick={() => handlePreview(item)}>
                    预览
                  </Button>
                )}
                <Button
                  size='small'
                  icon={<Download size={14} />}
                  onClick={() => handleDownload(item)}
                >
                  下载
                </Button>
                {permissions.canDelete && (
                  <Button
                    size='small'
                    danger
                    icon={<Trash2 size={14} />}
                    onClick={() => handleDelete(item)}
                  >
                    删除
                  </Button>
                )}
              </Space>
            </div>
          ))}
        </div>
      )}

      <Modal
        title={previewName}
        open={!!previewUrl}
        onCancel={() => setPreviewUrl(null)}
        footer={null}
        width={900}
      >
        {previewUrl && (
          <iframe
            src={previewUrl}
            style={{ width: '100%', height: '70vh', border: 0 }}
            title={previewName}
          />
        )}
      </Modal>
    </div>
  );
};

export default AttachmentPanel;
