'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { App, Input } from 'antd';
import { Send, Edit, Trash2, AtSign, Lock, MessageSquare } from 'lucide-react';
import { UserSelect } from '@/components/common/UserSelect';
import { ticketCommentAdapter } from '@/components/business/detail-tabs';
import type { CommentItem } from '@/components/business/detail-tabs';

const { TextArea } = Input;

interface TicketCommentStreamProps {
  ticketId: number;
  currentUserId?: number;
  ticketAssigneeId?: number;
  formatDateTime?: (s: string) => string;
}

const defaultFormat = (s?: string) => (s ? new Date(s).toLocaleString('zh-CN') : '');

/**
 * 工单工作台评论流：视觉对齐 prototype 的消息气泡样式，
 * 数据仍走 ticketCommentAdapter（与评论 Tab 原实现同一数据源）。
 */
export const TicketCommentStream: React.FC<TicketCommentStreamProps> = ({
  ticketId,
  currentUserId,
  ticketAssigneeId,
  formatDateTime = defaultFormat,
}) => {
  const { message } = App.useApp();
  const [comments, setComments] = useState<CommentItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [replyText, setReplyText] = useState('');
  const [isInternalComment, setIsInternalComment] = useState(false);
  const [mentionedUsers, setMentionedUsers] = useState<number[]>([]);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editingContent, setEditingContent] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const fetchComments = useCallback(async () => {
    setLoading(true);
    try {
      const data = await ticketCommentAdapter.list(ticketId);
      setComments(data?.comments || []);
    } catch {
      setComments([]);
    } finally {
      setLoading(false);
    }
  }, [ticketId]);

  useEffect(() => {
    void fetchComments();
  }, [fetchComments]);

  const handleSend = async () => {
    if (!replyText.trim()) return;
    setSubmitting(true);
    try {
      await ticketCommentAdapter.create(ticketId, {
        content: replyText,
        isInternal: isInternalComment,
        mentions: mentionedUsers,
      });
      setReplyText('');
      setIsInternalComment(false);
      setMentionedUsers([]);
      await fetchComments();
      message.success('评论已发布');
    } catch (e) {
      message.error(e instanceof Error ? e.message : '发布评论失败');
    } finally {
      setSubmitting(false);
    }
  };

  const handleEdit = async (commentId: number) => {
    if (!editingContent.trim() || !ticketCommentAdapter.update) return;
    setSubmitting(true);
    try {
      await ticketCommentAdapter.update(ticketId, commentId, { content: editingContent });
      setEditingId(null);
      setEditingContent('');
      await fetchComments();
      message.success('评论已更新');
    } catch (e) {
      message.error(e instanceof Error ? e.message : '更新评论失败');
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (commentId: number) => {
    try {
      await ticketCommentAdapter.remove(ticketId, commentId);
      await fetchComments();
      message.success('评论已删除');
    } catch (e) {
      message.error(e instanceof Error ? e.message : '删除评论失败');
    }
  };

  const renderBadges = (comment: CommentItem) => (
    <>
      {comment.isInternal && (
        <span className="text-[10px] bg-amber-100 text-amber-800 px-1.5 py-0.2 rounded font-medium flex items-center gap-0.5">
          <Lock size={10} /> 仅内部可见
        </span>
      )}
      {comment.mentions && comment.mentions.length > 0 && (
        <span className="text-[10px] bg-blue-100 text-blue-700 px-1.5 py-0.2 rounded font-medium flex items-center gap-0.5">
          <AtSign size={10} /> @提及 {comment.mentions.length}人
        </span>
      )}
      {ticketAssigneeId && comment.userId === ticketAssigneeId && (
        <span className="text-[10px] bg-orange-100 text-orange-700 px-1.5 py-0.2 rounded font-medium">
          处理人
        </span>
      )}
    </>
  );

  if (loading) {
    return <div className="p-6 text-center text-xs text-slate-400">评论加载中...</div>;
  }

  return (
    <div className="space-y-4 pt-2">
      {/* 消息时间线 */}
      {comments.length > 0 ? (
        <div className="space-y-3.5">
          {comments.map(comment => {
            const name = comment.user?.name || comment.user?.username || '未知用户';
            const isOwn = currentUserId ? comment.userId === currentUserId : true;
            return (
              <div key={comment.id} className="flex items-start gap-3 text-xs">
                <div
                  className={`w-8 h-8 rounded-full font-bold flex items-center justify-center shrink-0 ${
                    comment.isInternal
                      ? 'bg-amber-100 text-amber-800'
                      : ticketAssigneeId && comment.userId === ticketAssigneeId
                        ? 'bg-orange-100 text-orange-700'
                        : 'bg-slate-100 text-slate-700'
                  }`}
                >
                  {name[0]}
                </div>
                <div
                  className={`flex-1 rounded-xl p-3.5 border space-y-1.5 ${
                    comment.isInternal
                      ? 'bg-amber-50/50 border-amber-200/70'
                      : ticketAssigneeId && comment.userId === ticketAssigneeId
                        ? 'bg-orange-50/30 border-orange-100'
                        : 'bg-slate-50 border-slate-100'
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-1.5 flex-wrap">
                      <span className="font-semibold text-slate-900 text-xs">{name}</span>
                      {renderBadges(comment)}
                    </div>
                    <span className="text-slate-400 font-mono text-[11px] shrink-0 ml-2">
                      {formatDateTime(comment.createdAt)}
                    </span>
                  </div>

                  {editingId === comment.id ? (
                    <div className="space-y-2">
                      <TextArea
                        value={editingContent}
                        onChange={e => setEditingContent(e.target.value)}
                        rows={3}
                        className="!rounded-xl !text-xs"
                      />
                      <div className="flex justify-end gap-2">
                        <button
                          type="button"
                          onClick={() => {
                            setEditingId(null);
                            setEditingContent('');
                          }}
                          className="px-2.5 py-1 rounded-md text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200"
                        >
                          取消
                        </button>
                        <button
                          type="button"
                          onClick={() => handleEdit(comment.id)}
                          disabled={!editingContent.trim() || submitting}
                          className="px-2.5 py-1 rounded-md text-xs font-medium bg-orange-500 hover:bg-orange-600 text-white disabled:opacity-50"
                        >
                          保存
                        </button>
                      </div>
                    </div>
                  ) : (
                    <p className="text-slate-700 m-0 leading-relaxed text-xs whitespace-pre-wrap">
                      {comment.content}
                    </p>
                  )}

                  {editingId !== comment.id && isOwn && (
                    <div className="flex items-center gap-1">
                      {typeof ticketCommentAdapter.update === 'function' && (
                        <button
                          type="button"
                          onClick={() => {
                            setEditingId(comment.id);
                            setEditingContent(comment.content);
                          }}
                          className="text-[11px] text-slate-400 hover:text-orange-600 inline-flex items-center gap-0.5"
                        >
                          <Edit size={11} /> 编辑
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => handleDelete(comment.id)}
                        className="text-[11px] text-slate-400 hover:text-red-600 inline-flex items-center gap-0.5"
                      >
                        <Trash2 size={11} /> 删除
                      </button>
                    </div>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      ) : (
        <div className="text-center py-6 text-slate-400">
          <MessageSquare className="w-8 h-8 mx-auto mb-2 text-slate-300" />
          <span className="text-xs">暂无评论</span>
        </div>
      )}

      {/* 添加评论区域 */}
      <div className="pt-3 mt-4 border-t border-slate-100 space-y-3">
        <div className="flex items-center space-x-2">
          <input
            type="checkbox"
            id={`workbench-internal-${ticketId}`}
            checked={isInternalComment}
            onChange={e => setIsInternalComment(e.target.checked)}
            className="rounded text-orange-600 focus:ring-orange-500 cursor-pointer"
          />
          <label
            htmlFor={`workbench-internal-${ticketId}`}
            className="text-xs text-slate-600 font-medium cursor-pointer"
          >
            仅内部可见
          </label>
        </div>

        <div>
          <div className="mb-1.5">
            <span className="text-xs text-slate-500 font-medium flex items-center gap-1">
              <AtSign size={13} className="text-slate-400" />
              @用户（可选）
            </span>
          </div>
          <UserSelect
            value={mentionedUsers}
            onChange={setMentionedUsers}
            mode="multiple"
            placeholder="选择要@的用户"
            style={{ width: '100%' }}
          />
        </div>

        <TextArea
          rows={4}
          placeholder="输入您的评论或内部评估记录..."
          value={replyText}
          onChange={e => setReplyText(e.target.value)}
          className="!rounded-xl !border-slate-200 !text-xs !p-3 shadow-none focus:!border-orange-500"
        />

        <div className="flex justify-end pt-1">
          <button
            type="button"
            onClick={handleSend}
            disabled={!replyText.trim() || submitting}
            className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-orange-500 hover:bg-orange-600 active:bg-orange-700 text-white transition-colors duration-150 cursor-pointer shadow-xs disabled:opacity-50"
          >
            <Send size={13} />
            <span>{submitting ? '发送中...' : '发送评论'}</span>
          </button>
        </div>
      </div>
    </div>
  );
};

export default TicketCommentStream;
