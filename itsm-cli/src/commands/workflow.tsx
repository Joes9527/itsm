import React, { useState, useEffect } from 'react';
import { Box, Text, Newline } from 'ink';
import { apiClient } from '../lib/api-client.js';
import { Table } from '../components/table.js';
import { Spinner } from '../components/spinner.js';
import type { ProcessInstance, ApprovalTask } from '../lib/types.js';

export const WorkflowInstancesCommand: React.FC<{ page?: number }> = ({ page = 1 }) => {
  const [items, setItems] = useState<ProcessInstance[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  useEffect(() => {
    apiClient.listProcessInstances({ page, pageSize: 20 })
      .then(r => { setItems(r.items || []); setTotal(r.total); setLoading(false); })
      .catch(e => { setErr(e.message); setLoading(false); });
  }, []);
  if (loading) return <Spinner text="Loading process instances..." />;
  if (err) return <Text color="red">✗ {err}</Text>;
  return (
    <Box flexDirection="column">
      <Text bold color="cyan">⚙ Process instances ({total})</Text>
      <Newline />
      <Table
        data={items as unknown as Record<string, unknown>[]}
        columns={[
          { key: 'id', header: 'Instance ID', width: 36 },
          { key: 'processDefinitionKey', header: 'Process', width: 24 },
          { key: 'status', header: 'State', width: 10 },
          { key: 'startTime', header: 'Started', width: 20 },
        ]}
      />
    </Box>
  );
};

export const WorkflowTasksCommand: React.FC = () => {
  const [items, setItems] = useState<ApprovalTask[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  useEffect(() => {
    apiClient.listUserTasks()
      .then(r => { setItems(r.items || []); setTotal(r.total); setLoading(false); })
      .catch(e => { setErr(e.message); setLoading(false); });
  }, []);
  if (loading) return <Spinner text="Loading tasks..." />;
  if (err) return <Text color="red">✗ {err}</Text>;
  return (
    <Box flexDirection="column">
      <Text bold color="cyan">📝 My tasks ({total})</Text>
      <Newline />
      <Table
        data={items as unknown as Record<string, unknown>[]}
        columns={[
          { key: 'id', header: 'Task ID', width: 32 },
          { key: 'taskName', header: 'Name', width: 30 },
          { key: 'priority', header: 'Prio', width: 8 },
          { key: 'createdTime', header: 'Created', width: 20 },
          { key: 'dueDate', header: 'Due', width: 20 },
        ]}
      />
      <Newline />
      <Text dimColor>Use `itsm workflow complete &lt;task-id&gt;` to complete; `itsm workflow approve/reject` for approvals.</Text>
    </Box>
  );
};

export const WorkflowCompleteCommand: React.FC<{ id: string; outcome: string; comment?: string }> = ({ id, outcome, comment }) => {
  const [done, setDone] = useState(false);
  const [err, setErr] = useState('');
  useEffect(() => {
    apiClient.completeTask(id, outcome, comment)
      .then(() => setDone(true))
      .catch(e => { setErr(e.message); });
  }, []);
  if (err) return <Text color="red">✗ {err}</Text>;
  if (!done) return <Spinner text="Completing task..." />;
  return <Text color="green">✓ Task {id} completed</Text>;
};

export const ApprovalActionCommand: React.FC<{ id: string; action: 'approve' | 'reject'; comment?: string }> = ({ id, action, comment }) => {
  const [err, setErr] = useState('');
  const [done, setDone] = useState(false);
  useEffect(() => {
    const p = apiClient.submitTaskDecision(id, action, comment);
    p.then(() => setDone(true)).catch(e => setErr(e.message));
  }, []);
  if (err) return <Text color="red">✗ {err}</Text>;
  if (!done) return <Spinner text={`${action} ${id}...`} />;
  return <Text color="green">✓ {action} ok: {id}</Text>;
};
