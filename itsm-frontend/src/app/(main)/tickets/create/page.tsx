'use client';

import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import {
  Card, Form, Input, Select, Button, Space, Typography, App, Tag,
  Row, Col, Spin, Alert, DatePicker, Tree, Empty, Divider, Collapse,
} from 'antd';
import type { TreeDataNode } from 'antd';
import {
  ArrowLeft, FileText, ChevronRight, Sparkles, FolderOpen,
} from 'lucide-react';
import { TicketApi } from '@/lib/api/ticket-api';
import { buildTicketFormFields } from './ticket-form-fields';
import { TicketCategoryApi, type TicketCategory } from '@/lib/api/ticket-category-api';
import { useI18n } from '@/lib/i18n';
import { httpClient } from '@/lib/api/http-client';
import {
  ticketTypePresets,
  ticketTypeCategories,
  getTicketTypesByCategory,
  type TicketTypePreset,
} from '@/lib/ticket-type-presets';

const { Title, Text } = Typography;
const { TextArea } = Input;

type Priority = 'low' | 'medium' | 'high' | 'urgent';
type TicketCreateType = 'incident' | 'service_request' | 'change' | 'problem';

// 图标映射
const iconMap: Record<string, React.ReactNode> = {
  Container: <FolderOpen className="w-5 h-5" />,
  Database: <FolderOpen className="w-5 h-5" />,
  Download: <FolderOpen className="w-5 h-5" />,
  Desktop: <FolderOpen className="w-5 h-5" />,
  User: <FolderOpen className="w-5 h-5" />,
  Code: <FolderOpen className="w-5 h-5" />,
  Global: <FolderOpen className="w-5 h-5" />,
  Safety: <FolderOpen className="w-5 h-5" />,
  Appstore: <FolderOpen className="w-5 h-5" />,
  Project: <FolderOpen className="w-5 h-5" />,
  Key: <FolderOpen className="w-5 h-5" />,
  FileText: <FileText className="w-5 h-5" />,
};

// 数据库模板类型
interface DbTemplate {
  id: number;
  name: string;
  description: string;
  category: string;
  priority: string;
  fields?: FieldDef[];
  isActive: boolean;
}

interface FieldDef {
  name: string;
  label: string;
  type: 'text' | 'textarea' | 'select' | 'number' | 'date' | 'file';
  required?: boolean;
  options?: { label: string; value: string }[];
}

// 分类树的节点类型
interface CategoryTreeNode extends TreeDataNode {
  code: string;
  level: number;
  categoryData: TicketCategory;
}

function buildCategoryTree(categories: TicketCategory[]): CategoryTreeNode[] {
  const map = new Map<number, CategoryTreeNode>();
  const roots: CategoryTreeNode[] = [];

  // 第一遍：创建节点
  for (const cat of categories) {
    const node: CategoryTreeNode = {
      key: `cat-${cat.id}`,
      title: cat.name,
      code: cat.code,
      level: cat.level,
      categoryData: cat,
      isLeaf: false, // 后面修正
      children: [],
    };
    map.set(cat.id, node);
  }

  // 第二遍：建立父子关系
  for (const cat of categories) {
    const node = map.get(cat.id)!;
    if (cat.parentId && map.has(cat.parentId)) {
      (map.get(cat.parentId)!.children as CategoryTreeNode[]).push(node);
    } else {
      roots.push(node);
    }
  }

  // 第三遍：标记叶子节点（没有 children 的节点）
  const markLeaves = (nodes: CategoryTreeNode[]) => {
    for (const n of nodes) {
      const children = n.children as CategoryTreeNode[] | undefined;
      if (children && children.length > 0) {
        markLeaves(children);
      } else {
        n.isLeaf = true;
      }
    }
  };
  markLeaves(roots);

  return roots;
}

function getCategoryCodeChain(node: CategoryTreeNode): string[] {
  // 返回从根到这个节点的所有 code（包括自己的 code）
  return [node.code];
}

const inferTicketType = (template: DbTemplate | null, preset: TicketTypePreset | null): TicketCreateType => {
  if (preset) {
    const value = `${preset.id} ${preset.code} ${preset.name}`;
    if (/change|变更|ddl|firewall|domain/i.test(value)) return 'change';
    if (/problem|问题/i.test(value)) return 'problem';
  }
  if (template) {
    if (/故障|报修|异常|incident/i.test(template.name)) return 'incident';
  }
  return 'service_request';
};

// 归一化优先级：历史模板数据可能存了 SLA 优先级代码（P0-P4），
// 统一映射为标准优先级（low/medium/high/critical/urgent），避免后端 oneof 校验失败。
const normalizePriority = (p: string): string => {
  switch (p) {
    case 'P0':
      return 'urgent';
    case 'P1':
      return 'high';
    case 'P2':
      return 'medium';
    case 'P3':
    case 'P4':
      return 'low';
    default:
      return p;
  }
};

export default function CreateTicketPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { message } = App.useApp();
  const { t } = useI18n();
  const [form] = Form.useForm();

  // URL 参数：从服务目录点进来的分类和名称
  const urlCategory = searchParams.get('category') || '';
  const urlItem = searchParams.get('item') || '';

  // --- 状态 ---
  const [loading, setLoading] = useState(false);
  const [categories, setCategories] = useState<TicketCategory[]>([]);
  const [categoriesLoading, setCategoriesLoading] = useState(true);
  const [dbTemplates, setDbTemplates] = useState<DbTemplate[]>([]);
  const [templatesLoading, setTemplatesLoading] = useState(true);

  // 选中的分类节点（Tree 组件）
  const [selectedCategoryKeys, setSelectedCategoryKeys] = useState<React.Key[]>([]);
  const [selectedCategoryCode, setSelectedCategoryCode] = useState<string | null>(null);

  // 选中的模板
  const [selectedTemplate, setSelectedTemplate] = useState<DbTemplate | null>(null);
  // 选中的静态预设（Cloud Ops 遗留）
  const [selectedPreset, setSelectedPreset] = useState<TicketTypePreset | null>(null);

  // AI
  const [aiSuggestions, setAiSuggestions] = useState<{
    category?: string; priority?: string; confidence?: number; reasoning?: string;
  } | null>(null);
  const [aiLoading, setAiLoading] = useState(false);

  // --- 数据加载 ---
  useEffect(() => {
    // 加载分类树
    TicketCategoryApi.getCategories({ pageSize: 200 })
      .then(res => {
        const cats = res.categories || res.items || [];
        setCategories(cats);
      })
      .catch(err => console.warn('Failed to load categories:', err))
      .finally(() => setCategoriesLoading(false));

    // 加载数据库模板
    TicketApi.getTemplates({ page: 1, pageSize: 100 })
      .then(res => {
        if (res?.templates && Array.isArray(res.templates)) {
          const mapped: DbTemplate[] = res.templates.map((item: any) => ({
            id: item.id,
            name: item.name,
            description: item.description || '',
            category: item.category || '',
            priority: item.priority || 'medium',
            fields: (item.fields || []).map((f: any) => ({
              name: f.name,
              label: f.label,
              type: f.type || 'text',
              required: !!f.required,
              options: f.options || undefined,
            })),
            isActive: item.isActive || item.is_active || true,
          }));
          setDbTemplates(mapped);
        }
      })
      .catch(err => console.warn('Failed to load templates:', err))
      .finally(() => setTemplatesLoading(false));
  }, []);

  // URL 参数：自动选中树节点 + 预填标题
  useEffect(() => {
    if (!urlCategory || categories.length === 0) return;
    const cat = categories.find(c => c.code === urlCategory);
    if (cat) {
      setSelectedCategoryKeys([`cat-${cat.id}`]);
      setSelectedCategoryCode(cat.code);
      form.setFieldValue('category', cat.code);
      if (urlItem) form.setFieldValue('title', urlItem);
    }
  }, [urlCategory, categories]); // eslint-disable-line react-hooks/exhaustive-deps

  // --- 分类树 ---
  const categoryTree = useMemo(() => buildCategoryTree(categories), [categories]);

  // L1 domains for category dropdown
  const domainOptions = useMemo(() => {
    const domainNames: Record<string, string> = {
      ACC: '账号与访问服务', EUC: '终端与办公支持', COL: '邮箱与M365协作',
      NET: '网络与远程访问', INF: '平台与基础设施', APP: '业务系统支持',
      SEC: '安全与合规支持', ADV: '咨询与服务引导',
    };
    return categories
      .filter(c => c.level === 1)
      .map(c => ({ label: `${c.name} (${c.code})`, value: c.code }));
  }, [categories]);

  // 根据选中的分类节点（ID）精确匹配模板的 categoryIds
  const filteredTemplates = useMemo(() => {
    if (selectedCategoryKeys.length === 0) return dbTemplates;
    const selectedId = Number(String(selectedCategoryKeys[0]).replace('cat-', ''));
    if (!selectedId) return dbTemplates;
    return dbTemplates.filter(t => {
      const ids = (t as any).categoryIds || (t as any).category_ids;
      if (ids && Array.isArray(ids) && ids.length > 0) {
        return ids.includes(selectedId);
      }
      // Fallback: string prefix match for templates without categoryIds
      const tCat = t.category?.toUpperCase();
      const selCat = selectedCategoryCode?.toUpperCase();
      if (!tCat || !selCat) return false;
      return tCat === selCat || selCat.startsWith(tCat) || tCat.startsWith(selCat);
    });
  }, [dbTemplates, selectedCategoryKeys, selectedCategoryCode]);

  // 兼容的 Cloud Ops 预设（当没有匹配的数据库模板时使用）
  const cloudOpsPresets = useMemo(() => ticketTypePresets, []);

  // --- 分类树选中处理 ---
  const handleCategorySelect = (keys: React.Key[]) => {
    if (keys.length === 0) {
      setSelectedCategoryKeys([]);
      setSelectedCategoryCode(null);
      return;
    }
    const key = keys[0];
    setSelectedCategoryKeys([key]);

    // 查找对应分类节点的 code
    const findNode = (nodes: CategoryTreeNode[]): CategoryTreeNode | null => {
      for (const n of nodes) {
        if (n.key === key) return n;
        if (n.children) {
          const found = findNode(n.children as CategoryTreeNode[]);
          if (found) return found;
        }
      }
      return null;
    };
    const node = findNode(categoryTree);
    if (node) {
      setSelectedCategoryCode(node.code);
      form.setFieldValue('category', node.code);
    }
  };

  // --- 模板/预设选择 ---
  const handleSelectTemplate = (tmpl: DbTemplate) => {
    setSelectedTemplate(tmpl);
    setSelectedPreset(null);
    form.setFieldValue('priority', tmpl.priority);
  };

  const handleSelectPreset = (preset: TicketTypePreset) => {
    setSelectedPreset(preset);
    setSelectedTemplate(null);
    form.setFieldValue('priority', preset.priority);
  };

  const handleClearSelection = () => {
    setSelectedTemplate(null);
    setSelectedPreset(null);
  };

  // 激活的选中项
  const activeSelection = selectedTemplate || selectedPreset;
  const activeFields = selectedTemplate
    ? (selectedTemplate.fields || []).map(f => ({
        name: f.name,
        label: f.label,
        type: f.type,
        required: f.required,
        options: f.options,
      }))
    : selectedPreset?.fields || [];

  // --- 提交 ---
  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      setLoading(true);

      let description = values.description || '';

      // 附加自定义字段摘要
      if (activeFields.length > 0) {
        const fieldDetails = activeFields
          .map(field => {
            const value = values[field.name];
            if (value) {
              const optLabel = field.options?.find(o => o.value === value)?.label || value;
              return `${field.label}: ${optLabel}`;
            }
            return null;
          })
          .filter(Boolean)
          .join('\n');

        if (fieldDetails) {
          const name = selectedTemplate?.name || selectedPreset?.name || '';
          description = `[${name}]\n${fieldDetails}\n\n---\n${description}`;
        }
      }

      if (description.length < 10 && activeFields.length === 0) {
        message.warning('描述至少需要10个字符');
        setLoading(false);
        return;
      }

      const title = values.title || (activeSelection ? `${activeSelection.name}请求` : '新建工单');
      const priority = normalizePriority(values.priority || (activeSelection ? activeSelection.priority : 'medium'));

      const customFieldValues: Array<{ name: string; value: unknown }> = [];
      if (activeFields.length > 0) {
        activeFields.forEach(field => {
          const value = values[field.name];
          if (value !== undefined && value !== null && value !== '') {
            customFieldValues.push({ name: field.name, value });
          }
        });
      }

      const templateId = selectedTemplate?.id;

      const created = await TicketApi.createTicket({
        title,
        description,
        priority,
        type: inferTicketType(selectedTemplate, selectedPreset),
        category: values.category || selectedCategoryCode || (activeSelection ? (selectedTemplate?.category || selectedPreset?.category) : undefined),
        templateId,
        formFields: activeSelection
          ? buildTicketFormFields(activeFields, customFieldValues, templateId)
          : undefined,
        workflowDefinitionKey: selectedPreset?.workflowTemplateId,
      });

      message.success('工单创建成功');
      router.push(`/tickets/${created.id}`);
    } catch (e: unknown) {
      console.error('Create ticket error:', e);
      const errorObj = e as { message?: string; error?: { message?: string } };
      message.error(errorObj?.message || errorObj?.error?.message || '创建工单失败');
    } finally {
      setLoading(false);
    }
  };

  // --- AI 分类 ---
  const handleAITriage = async () => {
    try {
      const values = await form.validateFields();
      const title = values.title || (activeSelection ? `${activeSelection.name}请求` : '');
      if (!title && !values.description) {
        message.warning('请先填写标题或描述');
        return;
      }
      setAiLoading(true);
      const response = await httpClient.post<any>('/api/v1/ai/triage', {
        title, description: values.description || '',
        category: values.category, priority: values.priority,
      });
      if (response?.suggestions) {
        setAiSuggestions(response.suggestions);
        if (response.suggestions.category) form.setFieldValue('category', response.suggestions.category);
        if (response.suggestions.priority) form.setFieldValue('priority', response.suggestions.priority);
        message.success('AI 分类建议已应用');
      }
    } catch {
      message.warning('AI 分类服务暂时不可用');
    } finally {
      setAiLoading(false);
    }
  };

  // --- 渲染 ---
  const isLoading = categoriesLoading || templatesLoading;

  return (
    <div className="max-w-7xl mx-auto p-4 md:p-6" role="main" aria-label="创建工单页面">
      <Space orientation="vertical" size={16} style={{ width: '100%' }}>
        {/* 页面头部 */}
        <Card>
          <Space align="center" style={{ width: '100%' }}>
            <Button icon={<ArrowLeft className="w-4 h-4" />} onClick={() => router.back()}>返回</Button>
            <div style={{ flex: 1 }}>
              <Title level={4} style={{ marginBottom: 4 }}>新建工单</Title>
              <Text type="secondary">选择服务分类 → 选择模板 → 填写信息 → 提交</Text>
            </div>
          </Space>
        </Card>

        {isLoading ? (
          <Card><div className="text-center py-12"><Spin size="large" tip="加载服务目录..." /></div></Card>
        ) : (
          <Row gutter={[16, 16]}>
            {/* 左侧：服务分类树 */}
            <Col xs={24} md={8} lg={7}>
              <Card
                title={<Space><FolderOpen className="w-4 h-4" /><span>服务分类</span></Space>}
                styles={{ body: { padding: '8px', maxHeight: 560, overflowY: 'auto' } }}
              >
                {categoryTree.length > 0 ? (
                  <Tree
                    treeData={categoryTree as TreeDataNode[]}
                    selectedKeys={selectedCategoryKeys}
                    onSelect={handleCategorySelect}
                    showLine={false}
                    blockNode
                    defaultExpandAll={false}
                    switcherIcon={<ChevronRight className="w-3 h-3" />}
                  />
                ) : (
                  <Empty description="暂无分类数据" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                )}
              </Card>
            </Col>

            {/* 右侧：模板选择 + 表单 */}
            <Col xs={24} md={16} lg={17}>
              {/* 模板列表（当选中分类时显示） */}
              {selectedCategoryCode && !activeSelection && (
                <Card
                  title={<Space><FolderOpen className="w-4 h-4" /><span>可用模板</span></Space>}
                  styles={{ body: { padding: '12px' } }}
                  style={{ marginBottom: 16 }}
                >
                  {filteredTemplates.length > 0 ? (
                    <Space orientation="vertical" style={{ width: '100%' }} size={8}>
                      {filteredTemplates.map(tmpl => (
                        <Card
                          key={tmpl.id}
                          size="small"
                          hoverable
                          onClick={() => handleSelectTemplate(tmpl)}
                          style={{ cursor: 'pointer', borderColor: '#d9d9d9' }}
                        >
                          <div className="flex items-center gap-3">
                            <FileText className="w-5 h-5 text-blue-500" />
                            <div style={{ flex: 1 }}>
                              <div className="font-medium">{tmpl.name}</div>
                              <Text type="secondary" className="text-xs">{tmpl.description}</Text>
                            </div>
                            <Tag>{tmpl.priority}</Tag>
                          </div>
                        </Card>
                      ))}
                    </Space>
                  ) : (
                    <Empty description="该分类下暂无模板，请选择其他分类" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                  )}
                </Card>
              )}

              {/* 无分类选中时显示所有模板 */}
              {!selectedCategoryCode && !activeSelection && (
                <Card
                  title={<Space><FolderOpen className="w-4 h-4" /><span>全部模板</span><Tag>{dbTemplates.length}</Tag></Space>}
                  styles={{ body: { padding: '12px' } }}
                  style={{ marginBottom: 16 }}
                >
                  {dbTemplates.length > 0 ? (
                    <Row gutter={[8, 8]}>
                      {dbTemplates.map(tmpl => (
                        <Col xs={24} sm={12} key={tmpl.id}>
                          <Card
                            size="small"
                            hoverable
                            onClick={() => handleSelectTemplate(tmpl)}
                            style={{ cursor: 'pointer' }}
                          >
                            <div className="flex items-center gap-2">
                              <FileText className="w-4 h-4 text-blue-500" />
                              <div style={{ flex: 1 }}>
                                <div className="font-medium text-sm">{tmpl.name}</div>
                                <Text type="secondary" className="text-xs">{tmpl.description}</Text>
                              </div>
                              <Tag color="blue">{tmpl.category}</Tag>
                            </div>
                          </Card>
                        </Col>
                      ))}
                    </Row>
                  ) : (
                    <Empty description="暂无可用模板" />
                  )}
                </Card>
              )}

              {/* 已选中的激活项 */}
              {activeSelection && (
                <Card
                  style={{
                    marginBottom: 16,
                    borderColor: '#F06820',
                    background: '#F0682008',
                  }}
                >
                  <Space orientation="vertical" style={{ width: '100%' }}>
                    <Space>
                      <FileText className="w-5 h-5 text-blue-500" />
                      <div style={{ flex: 1 }}>
                        <div className="font-medium">{activeSelection.name}</div>
                        <Text type="secondary">{'description' in activeSelection ? (activeSelection as DbTemplate).description : (activeSelection as TicketTypePreset).description}</Text>
                      </div>
                      <Tag color="blue">{'category' in activeSelection ? (activeSelection as DbTemplate).category : (activeSelection as TicketTypePreset).category}</Tag>
                      <Button type="link" size="small" onClick={handleClearSelection}>更换</Button>
                    </Space>
                  </Space>
                </Card>
              )}

              {/* 表单 */}
              <Form form={form} layout="vertical" requiredMark="optional">
                <Card title="工单信息" style={{ marginBottom: 16 }}>
                  <Alert
                    type="info" showIcon className="mb-4"
                    message={activeSelection
                      ? '标题/描述留空将根据所选模板自动生成。'
                      : '请先从左侧选择服务分类，或直接选择模板开始。'}
                  />

                  <Form.Item
                    name="title" label="标题"
                    rules={activeSelection ? [{ min: 2, message: '标题至少2个字符' }] : [{ required: true, message: '请输入标题' }, { min: 2 }]}
                  >
                    <Input placeholder={activeSelection ? `例如：${activeSelection.name}请求` : '简要描述您的请求'} />
                  </Form.Item>

                  <Form.Item
                    name="description" label="详细描述"
                    rules={activeSelection ? [{ min: 10, message: '至少10个字符' }] : [{ required: true, message: '请输入描述' }, { min: 10 }]}
                    extra="建议写清现象、影响范围和期望结果。"
                  >
                    <TextArea rows={4} placeholder="请详细描述问题/需求与影响范围..." />
                  </Form.Item>

                  <Row gutter={[16, 16]}>
                    <Col xs={24} sm={12}>
                      <Form.Item name="priority" label="优先级" initialValue="medium" rules={[{ required: true }]}>
                        <Select<Priority>
                          options={[
                            { label: '低', value: 'low' },
                            { label: '中', value: 'medium' },
                            { label: '高', value: 'high' },
                            { label: '紧急', value: 'urgent' },
                          ]}
                        />
                      </Form.Item>
                    </Col>
                    <Col xs={24} sm={12}>
                      {selectedCategoryCode ? (
                        <div style={{ paddingTop: 4 }}>
                          <Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 4 }}>服务分类</Text>
                          <Space>
                            <Tag color="blue">{selectedCategoryCode}</Tag>
                            <Button type="link" size="small" onClick={() => {
                              setSelectedCategoryKeys([]);
                              setSelectedCategoryCode(null);
                              form.setFieldValue('category', undefined);
                            }}>清除</Button>
                          </Space>
                        </div>
                      ) : (
                        <Form.Item name="category" label="服务分类（可选）">
                          <Select
                            allowClear showSearch
                            options={domainOptions}
                            placeholder="未选分类树？在此快速选择"
                            optionFilterProp="label"
                          />
                        </Form.Item>
                      )}
                      {/* 隐藏字段，确保分类值提交 */}
                      <Form.Item name="category" hidden>
                        <Input />
                      </Form.Item>
                    </Col>
                  </Row>
                </Card>

                {/* 自定义字段 */}
                {activeFields.length > 0 && (
                  <Card title={`${activeSelection?.name || ''} - 补充信息`} style={{ marginBottom: 16 }}>
                    <Row gutter={[16, 0]}>
                      {activeFields.map(field => (
                        <Col span={24} key={field.name}>
                          <Form.Item
                            name={field.name}
                            label={field.label}
                            rules={field.required ? [{ required: true, message: `请填写${field.label}` }] : []}
                          >
                            {field.type === 'textarea' ? (
                              <TextArea rows={3} placeholder={`请输入${field.label}`} />
                            ) : field.type === 'select' ? (
                              <Select placeholder={`请选择${field.label}`} options={field.options} />
                            ) : field.type === 'number' ? (
                              <Input type="number" placeholder={`请输入${field.label}`} />
                            ) : field.type === 'date' ? (
                              <DatePicker style={{ width: '100%' }} placeholder={`请选择${field.label}`} />
                            ) : field.type === 'file' ? (
                              <Input placeholder={`${field.label}（提交后可在工单详情中上传）`} disabled />
                            ) : (
                              <Input placeholder={`请输入${field.label}`} />
                            )}
                          </Form.Item>
                        </Col>
                      ))}
                    </Row>
                  </Card>
                )}

                {/* 操作按钮 */}
                <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
                  <Button type="primary" onClick={handleSubmit} loading={loading} size="large" block>
                    创建工单
                  </Button>
                  <Button onClick={() => router.push('/tickets')} size="large" block>取消</Button>
                </Space>

                {/* AI 智能分类 */}
                <Card size="small" className="mt-4"
                  title={<span className="flex items-center gap-2"><Sparkles className="w-4 h-4 text-yellow-500" />AI 智能分类</span>}
                >
                  <Spin spinning={aiLoading}>
                    {aiSuggestions ? (
                      <div className="space-y-2">
                        <div className="flex flex-wrap gap-2">
                          {aiSuggestions.category && <Tag color="blue">分类: {aiSuggestions.category}</Tag>}
                          {aiSuggestions.priority && <Tag color="orange">优先级: {aiSuggestions.priority}</Tag>}
                        </div>
                        {aiSuggestions.reasoning && <Text type="secondary" className="text-sm">{aiSuggestions.reasoning}</Text>}
                        {aiSuggestions.confidence && <Text type="secondary" className="text-xs">置信度: {Math.round(aiSuggestions.confidence * 100)}%</Text>}
                      </div>
                    ) : <Text type="secondary">点击获取 AI 智能分类建议</Text>}
                  </Spin>
                  <Button type="default" icon={<Sparkles className="w-4 h-4" />} onClick={handleAITriage} loading={aiLoading} className="mt-2" block>
                    获取 AI 建议
                  </Button>
                </Card>

                {/* Cloud Ops 预设（折叠，不干扰主流程） */}
                <Collapse
                  className="mt-4"
                  items={[{
                    key: 'cloud-ops',
                    label: <span className="text-gray-400 text-sm">运维操作类型（Cloud Ops / DevOps 场景）</span>,
                    children: (
                      <Space orientation="vertical" style={{ width: '100%' }} size={8}>
                        <Text type="secondary" className="text-xs">以下为云平台运维场景的预设类型，不作为 Helpdesk 主要入口。</Text>
                        <Row gutter={[8, 8]}>
                          {cloudOpsPresets.map(preset => (
                            <Col xs={24} sm={12} key={preset.id}>
                              <Card
                                size="small"
                                hoverable
                                onClick={() => handleSelectPreset(preset)}
                                style={{
                                  cursor: 'pointer',
                                  borderColor: selectedPreset?.id === preset.id ? preset.color : '#d9d9d9',
                                  backgroundColor: selectedPreset?.id === preset.id ? `${preset.color}10` : '#fff',
                                }}
                              >
                                <div className="flex items-center gap-2">
                                  <div style={{ color: preset.color }}>{iconMap[preset.icon] || <FileText className="w-4 h-4" />}</div>
                                  <div style={{ flex: 1 }}>
                                    <div className="font-medium text-sm">{preset.name}</div>
                                    <Text type="secondary" className="text-xs">{preset.description}</Text>
                                  </div>
                                </div>
                              </Card>
                            </Col>
                          ))}
                        </Row>
                      </Space>
                    ),
                  }]}
                />
              </Form>
            </Col>
          </Row>
        )}
      </Space>
    </div>
  );
}
