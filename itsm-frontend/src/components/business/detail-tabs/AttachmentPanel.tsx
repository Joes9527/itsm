'use client';
import { useDetailRefreshEntry } from '@/components/business/detail-tabs/DetailRefreshContext';

import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Upload, Button, App, Typography, Progress, Modal, Space, Empty, Spin, Alert } from 'antd';
import type { RcFile } from 'antd/es/upload/interface';
import { File as FileIcon, Image as ImageIcon, FileText, Music, Video, Archive } from 'lucide-react';
import {
  DeleteOutlined,
  DownloadOutlined,
  EyeOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import { useDetailIdentity, useDetailResource } from './useDetailResource';
import { DetailReadState } from './DetailReadState';
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

export const AttachmentPanel: React.FC<AttachmentPanelProps> = props => {
  const identity = useDetailIdentity(props.targetId);
  return <AttachmentPanelContent key={identity} {...props} />;
};

const AttachmentPanelContent: React.FC<AttachmentPanelProps> = ({
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
  const resource = useDetailResource(
    targetId,
    () => adapter.list(targetId),
    data => data.length,
    onCountChange,
    permissions.canRead
  );
  const items = resource.data || [];
  useDetailRefreshEntry(permissions.canRead ? { key: 'attachments', label: '附件', reload: resource.reload, isWriting: () => busy.current } : undefined);
  const access = useRef(permissions);
  access.current = permissions;
  const confirmation = useRef<ReturnType<typeof modal.confirm> | null>(null);

  const [uploadProgress, setUploadProgress] = useState<number>(0);
  const [uploading, setUploading] = useState(false);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [previewName, setPreviewName] = useState<string>('');
  const [previewOpen, setPreviewOpen] = useState(false);
  const [previewText, setPreviewText] = useState<string | null>(null);
  const [previewType, setPreviewType] = useState('');
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewRequest = useRef(0);
  const closePreview = useCallback(() => {
    previewRequest.current++;
    setPreviewOpen(false);
    setPreviewText(null);
    setPreviewUrl(null);
  }, []);
  useEffect(
    () => () => {
      if (previewUrl) URL.revokeObjectURL(previewUrl);
    },
    [previewUrl]
  );

  useEffect(() => {
    if (resource.denied) {
      closePreview();
      confirmation.current?.destroy();
      setUploading(false);
      setUploadProgress(0);
      busy.current = false;
    }
  }, [resource.denied, closePreview]);
  useEffect(
    () => () => {
      confirmation.current?.destroy();
    },
    []
  );
  const beforeUpload = (file: RcFile) => {
    if (!resource.ready || !access.current.canUpload || busy.current) return Upload.LIST_IGNORE;
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
    if (!resource.ready || !access.current.canUpload || busy.current) return;
    busy.current = true;
    const current = resource.capture();
    const file = options.file as File;
    setUploading(true);
    setUploadProgress(0);
    try {
      await adapter.upload(
        targetId,
        file,
        p => {
          if (current()) setUploadProgress(p);
        },
        () => {
          if (!current() || !access.current.canUpload) throw new Error('上传上下文已失效');
        }
      );
      if (!current()) return;
      options.onSuccess?.({});
      message.success(`${file.name} 上传成功`);
      await resource.reload({ afterWrite: true });
    } catch (e) {
      if (!current()) return;
      resource.deny(e);
      const err = e instanceof Error ? e : new Error('上传失败');
      options.onError?.(err);
      message.error(err.message);
    } finally {
      if (current()) busy.current = false;
      if (current()) setUploading(false);
      if (current()) setUploadProgress(0);
    }
  };

  const handleDelete = (item: AttachmentItem) => {
    if (!resource.ready || !access.current.canDelete || busy.current) return;
    const current = resource.capture();
    confirmation.current = modal.confirm({
      title: '确认删除',
      content: `确定要删除附件 "${item.fileName}" 吗？`,
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: async () => {
        if (!current() || busy.current || !access.current.canDelete) return;
        busy.current = true;
        try {
          await adapter.remove(targetId, item.id, () => {
            if (!current() || !access.current.canDelete) throw new Error('删除上下文已失效');
          });
          if (!current()) return;
          message.success('删除成功');
          await resource.reload({ afterWrite: true });
        } catch (e) {
          if (current()) {
            resource.deny(e);
            message.error(e instanceof Error ? e.message : '删除失败');
          }
        } finally {
          if (current()) busy.current = false;
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

  const isPreviewable = (mime: string) =>
    [
      'text/plain',
      'application/pdf',
      'image/png',
      'image/jpeg',
      'image/gif',
      'image/webp',
      'image/avif',
    ].includes(mime.split(';')[0].trim().toLowerCase());
  const handlePreview = async (item: AttachmentItem) => {
    if (!adapter.preview || !resource.ready || !access.current.canRead) return;
    const current = resource.capture();
    const request = ++previewRequest.current;
    const assertCurrent = () => {
      if (!current() || request !== previewRequest.current || !access.current.canRead)
        throw new Error('预览上下文已失效');
    };
    setPreviewOpen(true);
    setPreviewName(item.fileName);
    setPreviewError(null);
    setPreviewUrl(null);
    setPreviewText(null);
    try {
      const blob = await adapter.preview(targetId, item.id, assertCurrent);
      assertCurrent();
      const mime = blob.type.split(';')[0].trim().toLowerCase();
      if (!isPreviewable(mime)) throw new Error('该文件类型不支持安全预览，请下载查看');
      const text = mime === 'text/plain' ? await blob.text() : null;
      assertCurrent();
      setPreviewType(mime);
      if (text !== null) setPreviewText(text);
      else setPreviewUrl(URL.createObjectURL(blob));
    } catch (error) {
      if (!current() || request !== previewRequest.current) return;
      resource.deny(error);
      setPreviewError(error instanceof Error ? error.message : '预览失败');
    }
  };
  const feedback = (
    <DetailReadState error={resource.error} loading={resource.loading} reload={resource.reload} />
  );
  if (!resource.ready)
    return (
      <div className='p-3'>
        {feedback}
        {resource.loading && <Spin />}
      </div>
    );
  return (
    <div className='p-3 min-w-0'>
      {feedback}{' '}
      {permissions.canUpload && (
        <div className='mb-6'>
          <Upload
            disabled={uploading}
            showUploadList={false}
            beforeUpload={beforeUpload}
            customRequest={customRequest as never}
            accept={accept}
          >
            <Button icon={<UploadOutlined aria-hidden="true" />} loading={uploading}>
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
                {adapter.preview && isPreviewable(item.mimeType) && (
                  <Button size='small' icon={<EyeOutlined aria-hidden="true" />} onClick={() => handlePreview(item)}>
                    预览
                  </Button>
                )}
                <Button
                  size='small'
                  icon={<DownloadOutlined aria-hidden="true" />}
                  onClick={() => handleDownload(item)}
                >
                  下载
                </Button>
                {permissions.canDelete && (
                  <Button
                    size='small'
                    danger
                    icon={<DeleteOutlined aria-hidden="true" />}
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
        open={previewOpen}
        onCancel={closePreview}
        footer={null}
        width={900}
      >
        {previewError && <Alert type='error' showIcon title={previewError} />}
        {!previewError && previewText === null && !previewUrl && <Spin />}
        {previewText !== null && (
          <pre className='whitespace-pre-wrap break-all max-h-[70vh] overflow-auto'>
            {previewText}
          </pre>
        )}
        {previewUrl &&
          (previewType.startsWith('image/') ? (
            // Authenticated safe-MIME object URL, revoked on close.
            <img
              src={previewUrl}
              alt={previewName}
              className='max-w-full max-h-[70vh] object-contain'
            />
          ) : (
            <iframe
              src={previewUrl}
              style={{ width: '100%', height: '70vh', border: 0 }}
              title={previewName}
            />
          ))}
      </Modal>
    </div>
  );
};

export default AttachmentPanel;
