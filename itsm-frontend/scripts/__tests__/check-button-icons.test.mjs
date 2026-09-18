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

test('RotateCcw 不再有任何豁免 —— 回归守卫', () => {
  // 曾经有个 PENDING 集合豁免它（批次 2 待定 glyph）。2026-09-18 那 7 处
  // 「重试 / 回滚」选型落地后删掉了。这条测试存在的意义就是别再有人把它加回来：
  // 一个长期豁免集会让「按钮图标不许来自 lucide」这条规则名存实亡。
  const named = LUCIDE + 'const a = <Button aria-label="重试" icon={<RotateCcw />} onClick={f} />;';
  assert.deepEqual(rules(named), ['lucide-button-icon'], '有名称也不能豁免规则 1');

  const anon = LUCIDE + 'const a = <Button icon={<RotateCcw />} onClick={f} />;';
  assert.deepEqual(rules(anon), ['lucide-button-icon', 'no-accessible-name']);
});

// ---- 条件／逻辑表达式形态（批次 5 关掉的盲区）----
// 2026-09-18 之前规则 1 只认 `icon={<X />}` 自闭合字面量，`icon={cond ? <A/> : <B/>}`
// 完全看不见——仓里曾有 11 处这样的 lucide 按钮图标，门禁却报「通过」。这几条就是那个盲区的守卫。

test('三元表达式里的 lucide 图标 -> 逐个报错', () => {
  const src =
    LUCIDE + 'const a = <Button aria-label="切换" icon={ok ? <Eye /> : <Trash2 />} onClick={f} />;';
  assert.deepEqual(rules(src), ['lucide-button-icon', 'lucide-button-icon']);
});

test('三元表达式里的 antd 图标 -> 不报错', () => {
  const src =
    ANTD +
    'const a = <Button aria-label="切换" icon={ok ? <DeleteOutlined /> : <EyeOutlined />} onClick={f} />;';
  assert.deepEqual(rules(src), []);
});

test('逻辑或的 lucide 兜底图标 -> 报错', () => {
  const src = LUCIDE + 'const a = <Button icon={custom || <Trash2 />} onClick={f} />;';
  assert.deepEqual(rules(src), ['lucide-button-icon', 'no-accessible-name']);
});

test('表达式形态的纯图标按钮，无名时只报一次名称问题，不按图标个数重复', () => {
  const src = LUCIDE + 'const a = <Button icon={ok ? <Eye /> : <Trash2 />} onClick={f} />;';
  const got = rules(src);
  assert.deepEqual(got, ['lucide-button-icon', 'lucide-button-icon', 'no-accessible-name']);
});

test('表达式形态的纯图标按钮，有 aria-label 就不报名称', () => {
  const src = LUCIDE + 'const a = <Button aria-label="切换" icon={ok ? <Eye /> : <Trash2 />} />;';
  assert.deepEqual(rules(src), ['lucide-button-icon', 'lucide-button-icon']);
});

test('表达式形态带文字 -> 不要求可访问名称', () => {
  const src = LUCIDE + 'const a = <Button icon={ok ? <Eye /> : <Trash2 />}>查看</Button>;';
  assert.deepEqual(rules(src), ['lucide-button-icon', 'lucide-button-icon']);
});

test('icon 是纯变量（值来自别处）-> 看不见，这是已记录的盲区', () => {
  // 不是「应该不报」，是「当前报不出来」。这条测试锁住现状，免得有人误以为
  // 门禁通过 == 全仓干净。见 check-button-icons.mjs 头部「已知盲区」。
  const src = LUCIDE + 'const a = <Button aria-label="查看" icon={action.icon} />;';
  assert.deepEqual(rules(src), []);
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
