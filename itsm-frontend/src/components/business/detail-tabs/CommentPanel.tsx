'use client';
import { useDetailRefreshEntry } from '@/components/business/detail-tabs/DetailRefreshContext';

import React, { useEffect, useState, useRef } from 'react';
import { Send, Edit, Trash2, MessageSquare, AtSign, User } from 'lucide-react';
import { Card, Typography, Button, Input, Avatar, Tag as AntTag, App } from 'antd';
import { UserSelect } from '@/components/common/UserSelect';
import type { CommentAdapter, CommentItem, TargetType } from './types';

import { useDetailIdentity, useDetailResource } from './useDetailResource';
import { DetailReadState } from './DetailReadState';

const { Text, Paragraph } = Typography;
const { TextArea } = Input;

export interface CommentPanelProps {
  targetType: TargetType;
  targetId: number | string;
  adapter: CommentAdapter;
  /**
   * 是否显示"仅内部可见"开关。默认 true。
   */
  showInternalToggle?: boolean;
  /**
   * 是否支持 @提及。默认 true。
   */
  showMentions?: boolean;
  /**
   * 当前用户 id，用于判断哪些评论可以编辑/删除
   */
  currentUserId?: number;
  /**
   * 自定义时间格式化，如未提供则使用 toLocaleString
   */
  formatDateTime?: (dateString: string) => string;
}

const defaultFormat = (s: string) => (s ? new Date(s).toLocaleString('zh-CN') : '');

export const CommentPanel: React.FC<CommentPanelProps> = props => {
  const identity = useDetailIdentity(props.targetId);
  return <CommentPanelContent key={identity} {...props} />;
};
const CommentPanelContent: React.FC<CommentPanelProps> = ({
  targetId,
  adapter,
  showInternalToggle = true,
  showMentions = true,
  currentUserId,
  formatDateTime = defaultFormat,
}) => {
  const { message, modal } = App.useApp();
  const confirmation = useRef<ReturnType<typeof modal.confirm> | null>(null);
  const busy = useRef(false);
  const resource = useDetailResource(
    targetId,
    () => adapter.list(targetId),
    data => data.total
  );
  const comments = resource.data?.comments || [];
  useDetailRefreshEntry(!resource.denied ? { key: 'comments', label: '评论', reload: resource.reload, isWriting: () => busy.current } : undefined);
  const [newComment, setNewComment] = useState('');
  const [isInternal, setIsInternal] = useState(false);
  const [mentionedUsers, setMentionedUsers] = useState<number[]>([]);
  const [editingCommentId, setEditingCommentId] = useState<number | null>(null);
  const [editingCommentContent, setEditingCommentContent] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const canEditByAdapter = typeof adapter.update === 'function';
  const canEditByUser = (c: CommentItem) => (currentUserId ? c.userId === currentUserId : false);

  useEffect(() => {
    if (!resource.denied) return;
    setNewComment('');
    setIsInternal(false);
    setMentionedUsers([]);
    setEditingCommentId(null);
    setEditingCommentContent('');
    setSubmitting(false);
    busy.current = false;
    confirmation.current?.destroy();
  }, [resource.denied]);
  useEffect(() => () => confirmation.current?.destroy(), []);
  const mutate = async (
    write: (assertCurrent: () => void) => Promise<unknown>,
    reset?: () => void
  ) => {
    if (!resource.ready || busy.current) return;
    const current = resource.capture();
    busy.current = true;
    setSubmitting(true);
    try {
      await write(() => {
        if (!current()) throw new Error('操作上下文已失效');
      });
      if (!current()) return;
      reset?.();
      message.success('评论操作成功');
      await resource.reload({ afterWrite: true });
    } catch (error) {
      if (!current()) return;
      resource.deny(error);
      message.error(error instanceof Error ? error.message : '评论操作失败');
    } finally {
      if (current()) {
        busy.current = false;
        setSubmitting(false);
      }
    }
  };
  const handleAddComment = () => {
    if (!newComment.trim()) return;
    return mutate(
      assertCurrent =>
        adapter.create(
          targetId,
          {
            content: newComment,
            isInternal: showInternalToggle ? isInternal : undefined,
            mentions: showMentions ? mentionedUsers : undefined,
          },
          assertCurrent
        ),
      () => {
        setNewComment('');
        setMentionedUsers([]);
        setIsInternal(false);
      }
    );
  };
  const handleEditComment = (commentId: number) => {
    const update = adapter.update;
    if (!editingCommentContent.trim() || !update) return;
    return mutate(
      assertCurrent =>
        update(targetId, commentId, { content: editingCommentContent }, assertCurrent),
      () => {
        setEditingCommentId(null);
        setEditingCommentContent('');
      }
    );
  };
  const handleDeleteComment = (commentId: number) =>
    mutate(assertCurrent => adapter.remove(targetId, commentId, assertCurrent));
  const startEditComment = (comment: CommentItem) => {
    setEditingCommentId(comment.id);
    setEditingCommentContent(comment.content);
  };

  const cancelEdit = () => {
    setEditingCommentId(null);
    setEditingCommentContent('');
  };

  if (!resource.ready) return <DetailReadState {...resource} />;
  return (
    <div className='p-6'>
      <DetailReadState {...resource} /> {/* 添加评论 */}
      <div className='mb-6'>
        <Card title='添加评论' className='shadow-sm'>
          <div className='space-y-4'>
            {showInternalToggle && (
              <div className='flex items-center space-x-4'>
                <div className='flex items-center space-x-2'>
                  <input
                    type='checkbox'
                    id={`internal-${targetId}`}
                    checked={isInternal}
                    onChange={e => setIsInternal(e.target.checked)}
                  />
                  <label
                    htmlFor={`internal-${targetId}`}
                    className="text-sm text-muted"
                  >
                    仅内部可见
                  </label>
                </div>
              </div>
            )}
            {showMentions && (
              <div>
                <div className='mb-2'>
                  <Text type='secondary' className='text-sm'>
                    <AtSign className='w-4 h-4 inline mr-1' />
                    @用户（可选）
                  </Text>
                </div>
                <UserSelect
                  value={mentionedUsers}
                  onChange={setMentionedUsers}
                  mode='multiple'
                  placeholder='选择要@的用户'
                  style={{ width: '100%' }}
                />
              </div>
            )}
            <TextArea
              value={newComment}
              onChange={e => setNewComment(e.target.value)}
              placeholder='输入您的评论...'
              rows={4}
            />
            <div className='flex justify-end'>
              <Button
                type='primary'
                icon={<Send size={14} />}
                onClick={handleAddComment}
                disabled={!newComment.trim()}
                loading={submitting}
              >
                发送评论
              </Button>
            </div>
          </div>
        </Card>
      </div>
      {/* 评论列表 */}
      <div className='space-y-4'>
        {comments.map(comment => (
          <Card key={comment.id} className='shadow-sm'>
            {editingCommentId === comment.id ? (
              <div className='space-y-3'>
                <TextArea
                  value={editingCommentContent}
                  onChange={e => setEditingCommentContent(e.target.value)}
                  rows={3}
                  placeholder='输入编辑后的评论...'
                />
                <div className='flex justify-end space-x-2'>
                  <Button onClick={cancelEdit}>取消</Button>
                  <Button
                    type='primary'
                    onClick={() => handleEditComment(comment.id)}
                    disabled={!editingCommentContent.trim()}
                    loading={submitting}
                  >
                    保存
                  </Button>
                </div>
              </div>
            ) : (
              <div className='flex items-start space-x-3'>
                <Avatar size='small' icon={<User size={14} />}>
                  {comment.user?.name?.[0] || comment.user?.username?.[0]}
                </Avatar>
                <div className='flex-1'>
                  <div className='flex items-center space-x-2 mb-2 flex-wrap'>
                    <Text strong>{comment.user?.name || comment.user?.username || '未知用户'}</Text>
                    {comment.isInternal && <AntTag color='orange'>仅内部可见</AntTag>}
                    {comment.mentions && comment.mentions.length > 0 && (
                      <AntTag color='blue' icon={<AtSign className='w-3 h-3' />}>
                        @{comment.mentions.length}人
                      </AntTag>
                    )}
                    <Text type='secondary' className='text-sm'>
                      {formatDateTime(comment.createdAt)}
                    </Text>
                    {comment.updatedAt && comment.updatedAt !== comment.createdAt && (
                      <Text type='secondary' className='text-xs'>
                        （已编辑）
                      </Text>
                    )}
                  </div>
                  <Paragraph className='mb-2 whitespace-pre-wrap'>{comment.content}</Paragraph>
                  <div className='flex items-center space-x-2'>
                    {canEditByAdapter && canEditByUser(comment) && (
                      <Button
                        type='link'
                        size='small'
                        icon={<Edit className='w-3 h-3' />}
                        onClick={() => startEditComment(comment)}
                      >
                        编辑
                      </Button>
                    )}
                    {canEditByUser(comment) && (
                      <Button
                        type='link'
                        size='small'
                        danger
                        icon={<Trash2 className='w-3 h-3' />}
                        onClick={() => {
                          const current = resource.capture();
                          confirmation.current = modal.confirm({
                            title: '确认删除',
                            content: '确定要删除这条评论吗？',
                            okText: '删除',
                            okType: 'danger',
                            cancelText: '取消',
                            onOk: () => {
                              if (current()) return handleDeleteComment(comment.id);
                            },
                          });
                        }}
                      >
                        删除
                      </Button>
                    )}
                  </div>
                </div>
              </div>
            )}
          </Card>
        ))}

        {comments.length === 0 && !resource.loading && (
          <div className="text-center py-8 text-muted">
            <MessageSquare className="w-16 h-16 mx-auto mb-4 text-muted" />
            <Text>暂无评论</Text>
          </div>
        )}
      </div>
    </div>
  );
};

export default CommentPanel;
