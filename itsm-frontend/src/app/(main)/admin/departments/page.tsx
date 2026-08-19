'use client';

import React, { useState, useEffect, useCallback, useMemo } from 'react';
import {
  Card,
  Table,
  Button,
  Space,
  Typography,
  Modal,
  Form,
  Input,
  Select,
  Tree,
  TreeSelect,
  message,
  Popconfirm,
  Tag,
  Row,
  Col,
  Statistic,
  Empty,
} from 'antd';
import type { DataNode } from 'antd/es/tree';
import {
  Plus,
  Edit,
  Trash2,
  Users,
  RefreshCw,
  Search,
  Folder,
  FileText,
} from 'lucide-react';
import type { ColumnsType } from 'antd/es/table';
import type { Department, CreateDepartmentRequest } from '@/lib/services/department-service';
import { departmentService } from '@/lib/services/department-service';
import { UserApi } from '@/lib/api/user-api';

const { Title, Text } = Typography;
const { TextArea } = Input;

export default function DepartmentManagement() {
  const [departments, setDepartments] = useState<Department[]>([]);
  const [treeData, setTreeData] = useState<Department[]>([]);
  const [selectedNode, setSelectedNode] = useState<Department | null>(null);
  const [loading, setLoading] = useState(false);
  const [fetching, setFetching] = useState(false);
  const [showModal, setShowModal] = useState(false);
  const [selectedDepartment, setSelectedDepartment] = useState<Department | null>(null);
  const [form] = Form.useForm();
  const [users, setUsers] = useState<{ label: string; value: number }[]>([]);
  const [searchTerm, setSearchTerm] = useState('');

  // 构建符合 TreeSelect / Left Tree 结构的节点
  const buildTreeData = (depts: Department[]): Department[] => {
    return depts.map(dept => {
      const children = dept.children && dept.children.length > 0 ? buildTreeData(dept.children) : undefined;
      return {
        ...dept,
        key: dept.id,
        value: dept.id,
        title: dept.name,
        icon: children ? (
          <Folder size={14} className="text-amber-500" />
        ) : (
          <FileText size={14} className="text-gray-400" />
        ),
        children,
      };
    });
  };

  // 左侧展示树的虚拟根节点——真实导入的组织数据里有 143 个平级顶层部门（历史遗留的演示
  // 部门与本次导入的分公司混在一起，互不隶属），全部挂在这个"组织架构"根节点下面展示，
  // 而不是散在没有共同入口的顶层列表里（呼应截图里的树形结构）。这只是前端展示层面的
  // 包装节点，不对应任何真实部门记录，选中它等价于清空选中（右侧表格回退到"全量部门"）。
  const ORG_ROOT_KEY = '__org_root__';

  // 加载部门数据
  const loadDepartments = useCallback(async () => {
    setFetching(true);
    try {
      const data = await departmentService.getDepartmentTree();
      setDepartments(data);
      const built = buildTreeData(data);
      setTreeData(built);
      // 默认选中第一个顶层节点
      if (built.length > 0 && !selectedNode) {
        setSelectedNode(built[0]);
      }
    } catch (error) {
      console.error('Failed to load departments:', error);
      message.error('加载部门数据失败');
    } finally {
      setFetching(false);
    }
  }, [selectedNode]);

  // 加载用户列表（用于选择部门经理）
  const loadUsers = useCallback(async () => {
    try {
      const response = await UserApi.getUsers({ page: 1, pageSize: 100 });
      setUsers(
        response.users.map(user => ({
          label: user.name || user.username,
          value: user.id,
        }))
      );
    } catch (error) {
      console.error('Failed to load users:', error);
    }
  }, []);

  useEffect(() => {
    loadDepartments();
    loadUsers();
  }, [loadDepartments, loadUsers]);

  // 左侧展示树：所有顶层部门（含历史演示部门与本次导入的分公司）包在一个虚拟根节点下
  const orgTree: DataNode[] = useMemo(
    () => [
      {
        key: ORG_ROOT_KEY,
        title: '组织架构',
        icon: <Folder size={14} className="text-amber-500" />,
        children: treeData as unknown as DataNode[],
      },
    ],
    [treeData]
  );

  const findDepartmentById = (depts: Department[], id: number): Department | null => {
    for (const d of depts) {
      if (d.id === id) return d;
      if (d.children) {
        const found = findDepartmentById(d.children, id);
        if (found) return found;
      }
    }
    return null;
  };

  // 右侧表格只展示当前选中的节点及其直接/间接子部门
  const getSubTreeList = (node: Department | null): Department[] => {
    if (!node) return [];
    const list: Department[] = [node];
    if (node.children) {
      node.children.forEach(child => {
        list.push(...getSubTreeList(child));
      });
    }
    return list;
  };

  // 如果有搜索词，全局匹配；无搜索词时，展示左侧选中节点的子部门清单
  const activeDisplayList = searchTerm.trim()
    ? flattenAll(departments).filter(dept => {
        const keyword = searchTerm.trim().toLowerCase();
        return (
          dept.name.toLowerCase().includes(keyword) ||
          dept.code.toLowerCase().includes(keyword) ||
          (dept.description || '').toLowerCase().includes(keyword)
        );
      })
    : (selectedNode ? getSubTreeList(selectedNode) : flattenAll(departments));

  function flattenAll(depts: Department[]): Department[] {
    const res: Department[] = [];
    depts.forEach(d => {
      res.push(d);
      if (d.children) res.push(...flattenAll(d.children));
    });
    return res;
  }

  // 处理保存
  const handleSave = async () => {
    try {
      const values = await form.validateFields();
      setLoading(true);

      if (selectedDepartment) {
        await departmentService.updateDepartment(selectedDepartment.id, values);
        message.success('部门更新成功');
      } else {
        await departmentService.createDepartment(values as CreateDepartmentRequest);
        message.success('部门创建成功');
      }

      setShowModal(false);
      form.resetFields();
      setSelectedDepartment(null);
      loadDepartments();
    } catch (error) {
      console.error('Failed to save department:', error);
      message.error('保存部门失败');
    } finally {
      setLoading(false);
    }
  };

  // 处理删除
  const handleDelete = async (id: number) => {
    try {
      await departmentService.deleteDepartment(id);
      message.success('部门删除成功');
      loadDepartments();
    } catch (error) {
      console.error('Failed to delete department:', error);
      message.error('删除部门失败');
    }
  };

  // 处理编辑
  const handleEdit = (record: Department) => {
    setSelectedDepartment(record);
    form.setFieldsValue({
      name: record.name,
      code: record.code,
      description: record.description,
      managerId: record.managerId,
      parentId: record.parentId,
    });
    setShowModal(true);
  };

  // 表格列定义
  const columns: ColumnsType<Department> = [
    {
      title: '部门名称',
      dataIndex: 'name',
      key: 'name',
      render: (text: string, record: Department) => (
        <Space>
          <Users className="w-4 h-4 text-blue-500" />
          <span className="font-medium">{text}</span>
        </Space>
      ),
    },
    {
      title: '部门编码',
      dataIndex: 'code',
      key: 'code',
      ellipsis: true,
      render: (text: string) => <Tag color="blue">{text}</Tag>,
    },
    {
      title: '类型',
      dataIndex: 'orgType',
      key: 'orgType',
      width: 110,
      render: (type?: string) => (
        <Tag color={type === 'warehouse' ? 'orange' : 'green'}>
          {type === 'warehouse' ? '仓库/物流' : '行政部门'}
        </Tag>
      ),
    },
    {
      title: '地域',
      dataIndex: 'areaName',
      key: 'areaName',
      width: 90,
      render: (area?: string) => <span>{area || '中国'}</span>,
    },
    {
      title: '部门经理',
      dataIndex: 'managerId',
      key: 'manager',
      render: (managerId: number) => {
        const user = users.find(u => u.value === managerId);
        return <span>{user?.label || '-'}</span>;
      },
    },
    {
      title: '操作',
      key: 'actions',
      width: 120,
      render: (_: unknown, record: Department) => (
        <Space size="small">
          <Button
            type="text"
            icon={<Edit size={16} />}
            onClick={() => handleEdit(record)}
          />
          <Popconfirm
            title="确认删除"
            description={`确定要删除部门"${record.name}"吗？`}
            onConfirm={() => handleDelete(record.id)}
            okText="确认"
            cancelText="取消"
          >
            <Button type="text" danger icon={<Trash2 size={16} />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div style={{ padding: 24 }}>
      <div className="mb-6">
        <Title level={2} className="!mb-2">
          <Users className="mr-2" />
          组织架构与部门管理
        </Title>
        <Text type="secondary">采用左侧组织树 + 右侧部门明细的联动架构进行管理</Text>
      </div>

      {/* 顶部工具栏 */}
      <Card className="mb-4">
        <Space wrap style={{ width: '100%', justifyContent: 'space-between' }}>
          <Space>
            <Input
              allowClear
              placeholder="搜索全量部门名称、编码或描述"
              prefix={<Search size={16} />}
              value={searchTerm}
              onChange={event => setSearchTerm(event.target.value)}
              style={{ width: 320 }}
            />
            <Button
              type="primary"
              icon={<Plus size={16} />}
              onClick={() => {
                setSelectedDepartment(null);
                form.resetFields();
                if (selectedNode) {
                  form.setFieldValue('parentId', selectedNode.id);
                }
                setShowModal(true);
              }}
            >
              新建部门
            </Button>
            <Button
              icon={<RefreshCw size={16} />}
              onClick={() => loadDepartments()}
              loading={fetching}
            >
              刷新
            </Button>
          </Space>
        </Space>
      </Card>

      {/* 主界面：左树右表布局 */}
      <Row gutter={16}>
        {/* 左侧组织树 */}
        <Col xs={24} md={8} lg={7} xl={6}>
          <Card title="组织机构树" className="min-h-[600px] enterprise-card">
            {treeData.length > 0 ? (
              <Tree
                showIcon
                showLine={{ showLeafIcon: false }}
                height={520}
                treeData={orgTree}
                defaultExpandedKeys={[ORG_ROOT_KEY]}
                selectedKeys={[selectedNode ? selectedNode.id : ORG_ROOT_KEY]}
                onSelect={keys => {
                  const val = keys[0];
                  if (val === undefined || val === ORG_ROOT_KEY) {
                    setSelectedNode(null);
                    return;
                  }
                  setSelectedNode(findDepartmentById(departments, val as number));
                }}
              />
            ) : (
              <Empty description="暂无组织树" />
            )}
            <div className="mt-4 p-3 bg-gray-50 rounded border text-xs text-gray-500">
              <p className="font-semibold mb-1">提示：</p>
              <p>点击左侧树中的节点展开/收起，或直接点选分公司、大区、部门节点，右侧表格自动联动过滤该节点下辖的子部门与仓库；点选"组织架构"根节点查看全量部门。用顶部搜索框可跨全量部门按名称/编码/描述搜索。</p>
            </div>
          </Card>
        </Col>

        {/* 右侧数据明细表格 */}
        <Col xs={24} md={16} lg={17} xl={18}>
          <Card
            title={
              <Space>
                <span>{selectedNode ? selectedNode.name : '全量部门'}</span>
                {selectedNode?.orgType && (
                  <Tag color={selectedNode.orgType === 'warehouse' ? 'orange' : 'green'}>
                    {selectedNode.orgType === 'warehouse' ? '仓库/物流' : '行政部门'}
                  </Tag>
                )}
                {selectedNode?.areaName && <Tag color="blue">{selectedNode.areaName}</Tag>}
              </Space>
            }
            className="min-h-[600px] enterprise-card"
          >
            <Table
              columns={columns}
              dataSource={activeDisplayList}
              rowKey="id"
              loading={fetching}
              pagination={{ pageSize: 15, showSizeChanger: true }}
              scroll={{ x: 760 }}
              locale={{
                emptyText: <Empty description="当前节点下暂无子部门" />,
              }}
              className="enterprise-table"
            />
          </Card>
        </Col>
      </Row>

      {/* 新建/编辑模态框 */}
      <Modal
        title={
          <span>
            <Edit className="w-4 h-4 mr-2" />
            {selectedDepartment ? '编辑部门' : '新建部门'}
          </span>
        }
        open={showModal}
        onOk={handleSave}
        onCancel={() => {
          setShowModal(false);
          setSelectedDepartment(null);
          form.resetFields();
        }}
        width={600}
        confirmLoading={loading}
        okText="保存"
        cancelText="取消"
      >
        <Form form={form} layout="vertical" className="mt-4">
          <Form.Item
            label="部门名称"
            name="name"
            rules={[{ required: true, message: '请输入部门名称' }]}
          >
            <Input placeholder="请输入部门名称" />
          </Form.Item>
          <Form.Item
            label="部门编码"
            name="code"
            rules={[{ required: true, message: '请输入部门编码' }]}
          >
            <Input placeholder="请输入部门编码（如：DEPT001）" />
          </Form.Item>
          <Form.Item
            label="上级部门"
            name="parentId"
          >
            <TreeSelect
              placeholder="选择上级部门（可选）"
              treeData={treeData.filter(dept => dept.id !== selectedDepartment?.id)}
              treeNodeFilterProp="title"
              allowClear
              style={{ width: '100%' }}
            />
          </Form.Item>
          <Form.Item
            label="部门经理"
            name="managerId"
          >
            <Select
              placeholder="选择部门经理"
              options={users}
              allowClear
              style={{ width: '100%' }}
            />
          </Form.Item>
          <Form.Item
            label="描述"
            name="description"
          >
            <TextArea rows={3} placeholder="请输入部门描述" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
