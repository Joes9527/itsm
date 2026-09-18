/**
 * 按钮图标门禁的单测。
 * 跑法: node --test scripts/__tests__/check-button-icons.test.mjs
 *
 * 重点是证明这个门禁**会失败**——一个永远通过的门禁等于没有门禁。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { findViolations } from '../check-button-icons.mjs';

const LUCIDE = "import { Trash2, RotateCcw, Eye } from 'lucide-react';\n";
const ANTD = "import { DeleteOutlined } from '@ant-design/icons';\n";

const rules = src => findViolations('t.tsx', src).map(v => v.rule);

test('lucide 图标出现在按钮里 -> 报错', () => {
  const src = LUCIDE + 'const a = <Button icon={<Trash2 />} onClick={f} />;';
  assert.deepEqual(rules(src), ['lucide-button-icon', 'no-accessible-name']);
});

test('antd 图标出现在按钮里 -> 不报错', () => {
  const src = ANTD + 'const a = <Button aria-label="删除" icon={<DeleteOutlined />} onClick={f} />;';
  assert.deepEqual(rules(src), []);
});

test('lucide 图标用在非按钮位置 -> 不报错（非按钮图标有意继续用 lucide）', () => {
  const src = LUCIDE + 'const cols = [{ render: () => <Trash2 /> }];';
  assert.deepEqual(rules(src), []);
});

test('带文字的按钮不要求可访问名称', () => {
  const src = LUCIDE + 'const a = <Button icon={<Trash2 />}>删除</Button>;';
  assert.deepEqual(rules(src), ['lucide-button-icon']);
});

test('纯图标按钮有 aria-label -> 名称规则不报', () => {
  const src = LUCIDE + 'const a = <Button aria-label="删除" icon={<Trash2 />} onClick={f} />;';
  assert.deepEqual(rules(src), ['lucide-button-icon']);
});

test('纯图标按钮有 title -> 名称规则不报', () => {
  const src = LUCIDE + 'const a = <Button title="删除" icon={<Trash2 />} onClick={f} />;';
  assert.deepEqual(rules(src), ['lucide-button-icon']);
});

test('纯图标按钮被 Tooltip 包裹 -> 名称规则不报', () => {
  const src = LUCIDE + 'const a = <Tooltip title="删除"><Button icon={<Trash2 />} onClick={f} /></Tooltip>;';
  assert.deepEqual(rules(src), ['lucide-button-icon']);
});

test('纯图标按钮只有注释子节点 -> 仍算纯图标，要报名称', () => {
  // 回归测试：TS 6 移除了 ts.isJsxEmptyExpression，早期实现会在这里崩或漏判
  const src = LUCIDE + 'const a = <Button icon={<Eye />}>{/* 占位 */}</Button>;';
  assert.deepEqual(rules(src), ['lucide-button-icon', 'no-accessible-name']);
});

test('别名导入也能识别', () => {
  const src = "import { Trash2 as Bin } from 'lucide-react';\nconst a = <Button icon={<Bin />} aria-label=\"删除\" />;";
  assert.deepEqual(rules(src), ['lucide-button-icon']);
});

test('RotateCcw 是批次 2 待定项 -> 规则 1 豁免，但规则 2 仍管', () => {
  const named = LUCIDE + 'const a = <Button aria-label="刷新" icon={<RotateCcw />} onClick={f} />;';
  assert.deepEqual(rules(named), [], '有名称时应当完全放行');

  const anon = LUCIDE + 'const a = <Button icon={<RotateCcw />} onClick={f} />;';
  assert.deepEqual(rules(anon), ['no-accessible-name'], '没有名称时仍应报');
});

test('没有 lucide 导入的文件直接跳过', () => {
  const src = "import { Button } from 'antd';\nconst a = <Button icon={<Whatever />} />;";
  assert.deepEqual(rules(src), []);
});

test('报告带行号，指向出问题的图标而不是文件头', () => {
  const src = LUCIDE + 'const pad = 1;\nconst pad2 = 2;\nconst a = <Button icon={<Trash2 />} aria-label="删除" />;';
  const [v] = findViolations('t.tsx', src);
  assert.equal(v.line, 4);
});
