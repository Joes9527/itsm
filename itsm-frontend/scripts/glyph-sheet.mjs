/**
 * 生成「刷新 / 重置」glyph 对照页，供人工选型。
 *
 * 只做一件事：把真实 @ant-design/icons 组件渲染成静态 SVG，按主题 token 的真实
 * 几何（--control-height-sm: 29px / --control-height: 34px、font-size 12/13px、
 * --border-radius-md: 6px、--color-primary: #F06820）摆进按钮里，让人眼在同一比例下
 * 比较。颜色、字号、间距都来自 src/styles/generated-theme-tokens.css，不手编。
 *
 * 输出 /tmp/glyph-sheet/index.html（刻意不写进仓库——这是一次性决策辅助，
 * 决策本身会落进代码，不需要把过程也留在版本库里）。
 *
 * 用法: node scripts/glyph-sheet.mjs
 */

import { renderToStaticMarkup } from 'react-dom/server';
import React from 'react';
import * as Icons from '@ant-design/icons';
import { writeFileSync, mkdirSync } from 'node:fs';

/** 先核对名字真的存在——icon-migration.js 就是对着不存在的图标集写的，不重蹈覆辙 */
function icon(name) {
  if (!(name in Icons)) throw new Error(`@ant-design/icons 里没有 ${name}，不要猜`);
  return renderToStaticMarkup(React.createElement(Icons[name], { 'aria-hidden': 'true' }));
}

/** 也渲染一份 lucide 的 RotateCcw 作为「现状」参照 */
async function lucideIcon(name) {
  const mod = await import('lucide-react');
  if (!(name in mod)) throw new Error(`lucide-react 里没有 ${name}`);
  // lucide 组件产出 <svg>，套上 antd 的 .anticon 外壳以便同壳比较
  const inner = renderToStaticMarkup(React.createElement(mod[name], { size: 16 }));
  return `<span class="anticon">${inner}</span>`;
}

const REFRESH = [
  { name: 'SyncOutlined', note: '迁移后已经在用 —— 60 处按钮位', tone: 'current' },
  { name: 'ReloadOutlined', note: '映射表原本给 RotateCcw 的目标，但目前全仓 0 处在用', tone: 'cand' },
  { name: 'RedoOutlined', note: '顺时针重做，偏「再执行一次」而不是「刷新」', tone: 'cand' },
  { name: 'RetweetOutlined', note: '双向循环箭头，偏「同步」语义', tone: 'cand' },
];

const RESET = [
  { name: 'SyncOutlined', note: '当前：4 个「重置」按钮正用着它 —— 这是语义错', tone: 'wrong' },
  { name: 'CloseOutlined', note: '当前：3 处原本用 X（关闭）做重置 —— 也是语义错', tone: 'wrong' },
  { name: 'ClearOutlined', note: '清空表单，与旁边「查询」成对', tone: 'cand' },
  { name: 'UndoOutlined', note: '逆时针撤销，偏「回到上一步」', tone: 'cand' },
  { name: 'RollbackOutlined', note: '回退箭头，偏「回滚到某版本」', tone: 'cand' },
  { name: 'RestOutlined', note: '沙漏/休息语义，不建议', tone: 'cand' },
];

const KEEP = [
  { name: 'RotateCcw', note: '逆时针 —— 保留给「重试」（3 处）与「版本回滚」（2 处）', lucide: true },
];

const btn = (iconHtml, { size = 'md', type = 'default', label = '' } = {}) =>
  `<span class="btn btn-${size} btn-${type}">${iconHtml}${label ? `<span class="lbl">${label}</span>` : ''}</span>`;

function row(item, html) {
  return `
    <div class="row row-${item.tone || 'cand'}">
      <div class="glyph">${html}</div>
      <div class="meta">
        <code>${item.name}</code>
        <span class="note">${item.note}</span>
      </div>
      <div class="samples">
        <div class="sample"><span class="cap">纯图标 · small 29px</span>${btn(html, { size: 'sm', type: 'primary' })}</div>
        <div class="sample"><span class="cap">带文字 · small</span>${btn(html, { size: 'sm', type: 'primary', label: '刷新' })}</div>
        <div class="sample"><span class="cap">带文字 · middle 34px</span>${btn(html, { size: 'md', type: 'default', label: '刷新' })}</div>
      </div>
    </div>`;
}

const refreshRows = REFRESH.map(c => row(c, icon(c.name))).join('');
const resetRows = RESET.map(c => row(c, icon(c.name))).join('');
const keepRows = (await Promise.all(KEEP.map(async c => row(c, await lucideIcon(c.name))))).join('');

const html = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>按钮图标 glyph 对照 — 刷新 / 重置</title>
<style>
  :root {
    --color-primary: #F06820;
    --color-text-primary: #354153;
    --color-text-secondary: #667181;
    --color-bg-primary: #FFFFFF;
    --color-bg-secondary: #F5F6F8;
    --control-height: 34px;
    --control-height-sm: 29px;
    --border-radius-md: 6px;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; padding: 32px;
    font: 13px/1.6 -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
    color: var(--color-text-primary); background: var(--color-bg-secondary);
  }
  .wrap { max-width: 1080px; margin: 0 auto; }
  h1 { font-size: 20px; margin: 0 0 8px; }
  h2 { font-size: 15px; margin: 32px 0 4px; }
  .lede { color: var(--color-text-secondary); margin: 0 0 8px; }
  .lede b { color: var(--color-text-primary); }
  .card { background: var(--color-bg-primary); border-radius: 8px; padding: 8px 16px; margin-top: 12px; }
  .row { display: grid; grid-template-columns: 56px 240px 1fr; align-items: center;
         gap: 16px; padding: 14px 0; border-bottom: 1px solid #EEF0F3; }
  .row:last-child { border-bottom: 0; }
  .glyph { display: flex; align-items: center; justify-content: center;
           font-size: 28px; color: var(--color-text-primary); }
  .meta code { display: block; font-size: 13px; font-weight: 600; }
  .meta .note { color: var(--color-text-secondary); font-size: 12px; }
  .samples { display: flex; gap: 20px; align-items: flex-end; flex-wrap: wrap; }
  .sample { display: flex; flex-direction: column; gap: 6px; }
  .cap { font-size: 11px; color: var(--color-text-secondary); }
  .row-current { background: #F0F7F0; }
  .row-wrong { background: #FFF4F0; }
  .btn { display: inline-flex; align-items: center; justify-content: center;
         gap: 8px; border-radius: var(--border-radius-md); padding: 0 12px;
         font-size: 13px; border: 1px solid #D9D9D9; background: #fff;
         color: var(--color-text-primary); white-space: nowrap; }
  .btn-sm { height: var(--control-height-sm); font-size: 12px; }
  .btn-md { height: var(--control-height); }
  .btn-primary { background: var(--color-primary); border-color: var(--color-primary); color: #fff; }
  .btn .anticon { display: inline-flex; align-items: center; line-height: 0; }
  .btn .anticon svg { display: inline-block; }
  .btn-sm .anticon { font-size: 12px; }
  .btn-md .anticon { font-size: 13px; }
  .glyph .anticon { display: inline-flex; }
  .tag { display: inline-block; font-size: 11px; padding: 1px 7px; border-radius: 999px;
         margin-left: 6px; vertical-align: 1px; }
  .tag-cur { background: #E4F2E4; color: #2C6B2C; }
  .tag-wrong { background: #FDE4DA; color: #A63A16; }
</style>
</head>
<body>
<div class="wrap">
  <h1>按钮图标 glyph 对照 — 刷新 / 重置</h1>
  <p class="lede">
    机械迁移已经跑完，但它<strong>只做等价替换，不修语义</strong>。所以有两件事现在需要你定。
    下面每个 glyph 都是真实的 <code>@ant-design/icons</code> 组件渲染出来的，按钮的高度、字号、
    圆角、主色全部取自 <code>generated-theme-tokens.css</code>，和线上 1:1。
  </p>

  <h2>一、刷新：其实已经定了 <span class="tag tag-cur">零改动</span></h2>
  <p class="lede">
    迁移把 <code>RefreshCw</code> 换成了 <code>SyncOutlined</code>，现在
    <b>60 个按钮位已经在用它</b>；而 <code>ReloadOutlined</code> 全仓 <b>0 处在用</b>。
    剩下 35 处 <code>RotateCcw</code>（逆时针）是迁移时故意留下的——所以现在同一件事
    在不同页面上长得不一样。选 <code>SyncOutlined</code> 等于零改动收口；选别的则要连
    那 60 处一起改。
  </p>
  <div class="card">${refreshRows}</div>

  <h2>二、重置：两处现存的语义错，需要选一个正确 glyph</h2>
  <p class="lede">
    前两行是<strong>当前状态</strong>：4 个「重置」按钮用着 <code>SyncOutlined</code>（顺时针循环），
    3 处沿用了原来的 <code>X</code>（关闭）。两者都不是「重置」。
  </p>
  <div class="card">${resetRows}</div>

  <h2>三、保持不动</h2>
  <p class="lede">
    这几个位置的逆时针箭头是对的，迁移时已按 DEFERRED 跳过，不参与本次选型。
  </p>
  <div class="card">${keepRows}</div>
</div>
</body>
</html>`;

mkdirSync('/tmp/glyph-sheet', { recursive: true });
writeFileSync('/tmp/glyph-sheet/index.html', html);
console.log('已生成 /tmp/glyph-sheet/index.html');
