/**
 * 给「纯图标按钮」补可访问名称。
 *
 * 背景：antd 的 AntdIcon 会给外层 span 设 role="img" + aria-label={icon.name}。带文字的按钮
 * 靠 aria-hidden 挡掉；纯图标按钮没有可访问名称时，图标一旦迁移，按钮的名字就会变成英文
 * 图标名（"more"、"delete"）——比 lucide 时代的「没有名字」更糟。所以必须先补名字再迁移。
 *
 * 这个脚本**不猜**：每个标签都必须由证据推出来（onClick 处理函数名 / 图标身份 / 包裹它的
 * Popconfirm、Dropdown、Tooltip），证据不足的一律列进「需人工判断」并且不动。默认干跑，
 * 加 --write 才落盘。
 *
 * 用法：
 *   node scripts/label-icon-buttons.mjs <files...>            # 干跑，打印每一条的推据
 *   node scripts/label-icon-buttons.mjs --write <files...>
 *   node scripts/label-icon-buttons.mjs --all
 */

import { readFileSync, writeFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import ts from 'typescript';

/** 图标 -> 标签。只收无歧义的：一个图标在一个按钮里就只可能是这一件事。 */
const BY_ICON = {
  Trash2: '删除',
  Delete: '删除',
  Pencil: '编辑',
  Edit: '编辑',
  Eye: '查看',
  MoreHorizontal: '更多操作',
  MoreVertical: '更多操作',
  RefreshCw: '刷新',
  RotateCcw: '刷新',
  X: '关闭',
  Search: '搜索',
  Plus: '新建',
};

/** onClick 处理函数名 -> 标签。比图标更具体，优先采信。 */
const BY_HANDLER = [
  [/removeStep|handleRemoveStep/, '删除此步骤'],
  [/handleDuplicate/, '复制'],
  [/handleDelete|onDelete|handleRemove|^remove$|handleBatchDelete/, '删除'],
  [/handleEdit|onEdit|handleModify/, '编辑'],
  [/handleRefresh|onRefresh|refresh|reload/i, '刷新'],
  [/handleView|onView|handleDetail|handleOpen/, '查看'],
  [/handleClose|onClose/, '关闭'],
];

function walk(dir, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (!/node_modules|\.next|__tests__|__mocks__/.test(entry.name)) walk(p, out);
    } else if (/\.tsx$/.test(entry.name)) out.push(p);
  }
  return out;
}

function lucideImports(sf) {
  const local = new Map();
  for (const stmt of sf.statements) {
    if (!ts.isImportDeclaration(stmt)) continue;
    if (!ts.isStringLiteral(stmt.moduleSpecifier)) continue;
    if (stmt.moduleSpecifier.text !== 'lucide-react') continue;
    const clause = stmt.importClause;
    if (!clause?.namedBindings || !ts.isNamedImports(clause.namedBindings)) continue;
    for (const el of clause.namedBindings.elements) {
      local.set(el.name.text, el.propertyName ? el.propertyName.text : el.name.text);
    }
  }
  return local;
}

const attrName = a => (ts.isJsxAttribute(a) && ts.isIdentifier(a.name) ? a.name.text : null);

function hasRealChildren(el) {
  if (!ts.isJsxElement(el)) return false;
  for (const child of el.children) {
    if (ts.isJsxText(child)) {
      if (child.text.trim()) return true;
    } else if (ts.isJsxExpression(child)) {
      if (child.expression) return true;
    } else return true;
  }
  return false;
}

const lineOf = (sf, node) => sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1;

/** 结尾的键盘提示，如 " (Ctrl+Y)" / " (Delete)" */
const KEY_HINT = /\s*\((?:Ctrl|Cmd|⌘|Alt|Shift|Del|Delete|Esc)[^)]*\)\s*$/;
/** 占位文案，不是动作名 */
const PLACEHOLDER = /(即将推出|敬请期待|暂未开放|开发中)$/;
const isAsciiOnly = s => /^[\x20-\x7E]+$/.test(s);

/**
 * 逐个人工核过的例外：证据推不出来，但读代码就能确定动作是什么。
 * 键是 file:line，故意写得具体——靠通用规则去猜这几个会把规则本身搞脏。
 * 行号一旦漂移，这条就不匹配、退回「需人工判断」，是安全方向。
 */
const OVERRIDES = {
  'src/app/(main)/knowledge/page.tsx:290': 'AI 搜索',
  'src/app/(main)/profile/page.tsx:394': '编辑资料',
  'src/components/knowledge/KnowledgeCollaboration.tsx:300': '版本历史',
  'src/components/layout/AppLayout.tsx:71': '打开菜单',
};

/**
 * 从证据推出标签；推不出返回 null。
 * 优先级：包裹它的 <Tooltip title="..."> 是**人写的**，证据最强，排第一。
 */
function deriveLabel({ icon, handler, wrappers, tooltipTitle }) {
  if (tooltipTitle) {
    // 键盘提示不属于可访问名称：屏幕阅读器会把 "重做 (Ctrl+Y)" 念成
    // 「重做 左括号 Ctrl 加 Y 右括号」。快捷键应该走 aria-keyshortcuts。
    const cleaned = tooltipTitle.replace(KEY_HINT, '').trim();
    // 占位文案（"…即将推出"）不是动作名；纯 ASCII 说明 tooltip 本身写的英文，
    // 那是另一个缺陷，不能顺手把英文抄进中文产品的 aria-label。
    if (cleaned && !PLACEHOLDER.test(cleaned) && !isAsciiOnly(cleaned)) {
      return {
        label: cleaned,
        why: `Tooltip title="${tooltipTitle}"${cleaned !== tooltipTitle ? '（已去掉键盘提示）' : ''}`,
      };
    }
  }
  for (const [re, label] of BY_HANDLER) {
    if (handler && re.test(handler)) return { label, why: `onClick=${handler}` };
  }
  if (icon && BY_ICON[icon]) return { label: BY_ICON[icon], why: `icon=${icon}` };
  if (wrappers.has('Dropdown')) return { label: '更多操作', why: '被 Dropdown 包裹' };
  return null;
}

function processFile(file) {
  const src = readFileSync(file, 'utf8');
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const lucide = lucideImports(sf);
  if (!lucide.size) return { edits: [], rows: [] };

  const parent = new Map();
  const link = n => {
    ts.forEachChild(n, c => {
      parent.set(c, n);
      link(c);
    });
  };
  link(sf);

  /** 向上走，收集包裹它的组件名，并取最近的 <Tooltip title="..."> 的 title 字面量 */
  const contextOf = node => {
    const found = new Set();
    let tooltipTitle = null;
    let cur = node;
    while (cur && parent.get(cur)) {
      const p = parent.get(cur);
      const opening = ts.isJsxElement(p) ? p.openingElement : ts.isJsxSelfClosingElement(p) ? p : null;
      const tag = opening ? opening.tagName.getText(sf) : null;
      if (tag) found.add(tag);
      if (tag === 'Tooltip' && !tooltipTitle) {
        const t = opening.attributes.properties
          .filter(ts.isJsxAttribute)
          .find(a => attrName(a) === 'title');
        // 只认字符串字面量：title={<span>…</span>} 或 title={t('…')} 取不出稳定文本，留给人工
        if (t?.initializer && ts.isStringLiteral(t.initializer)) tooltipTitle = t.initializer.text;
      }
      cur = p;
    }
    return { found, tooltipTitle };
  };

  const edits = [];
  const rows = [];

  const visit = node => {
    const isButton =
      (ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node)) &&
      (ts.isJsxElement(node) ? node.openingElement : node).tagName.getText(sf) === 'Button';

    if (isButton) {
      const opening = ts.isJsxElement(node) ? node.openingElement : node;
      const attrs = opening.attributes.properties.filter(ts.isJsxAttribute);
      const names = new Set(attrs.map(attrName).filter(Boolean));
      const hasLabel =
        names.has('aria-label') || names.has('title') || names.has('aria-labelledby');

      const iconAttr = attrs.find(a => attrName(a) === 'icon');
      const expr = iconAttr?.initializer;
      const iconEl =
        expr && ts.isJsxExpression(expr) && expr.expression && ts.isJsxSelfClosingElement(expr.expression)
          ? expr.expression
          : null;
      const iconTag = iconEl ? iconEl.tagName.getText(sf) : null;
      const icon = iconTag ? lucide.get(iconTag) : null;

      // 只处理「纯图标 + 有 lucide 图标 + 没名字」
      if (icon && !hasLabel && !hasRealChildren(node)) {
        const { found: wrappers, tooltipTitle } = contextOf(node);
        const handlerAttr = attrs.find(a => attrName(a) === 'onClick');
        let handler = null;
        if (handlerAttr?.initializer && ts.isJsxExpression(handlerAttr.initializer) && handlerAttr.initializer.expression) {
          handler = handlerAttr.initializer.expression.getText(sf);
        }
        // 人工核过的例外优先于一切启发式
        const overrideLabel = OVERRIDES[`${file}:${lineOf(sf, opening)}`];
        const got = overrideLabel
          ? { label: overrideLabel, why: '人工核对' }
          : deriveLabel({ icon, handler, wrappers, tooltipTitle });
        if (got) {
          edits.push({ pos: opening.tagName.getEnd(), text: ` aria-label="${got.label}"` });
          rows.push({ file, line: lineOf(sf, opening), icon, label: got.label, why: got.why, ok: true });
        } else {
          rows.push({
            file,
            line: lineOf(sf, opening),
            icon,
            label: null,
            why: `icon=${icon} onClick=${handler || '(无)'} 包裹=${[...wrappers].join('/') || '(无)'}`,
            ok: false,
          });
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);

  return { edits, rows, src };
}

// ---- 主流程 ----
const argv = process.argv.slice(2);
const write = argv.includes('--write');
const all = argv.includes('--all');
const files = all ? walk('src') : argv.filter(a => !a.startsWith('--'));

if (!files.length) {
  console.error('用法: node scripts/label-icon-buttons.mjs [--write] [--all | <files...>]');
  process.exit(1);
}

const allRows = [];
let changed = 0;

for (const file of files) {
  const { edits, rows, src } = processFile(file);
  allRows.push(...rows);
  if (edits.length && write) {
    let out = src;
    for (const e of [...edits].sort((a, b) => b.pos - a.pos)) {
      out = out.slice(0, e.pos) + e.text + out.slice(e.pos);
    }
    writeFileSync(file, out);
    changed++;
  } else if (edits.length) {
    changed++;
  }
}

const ok = allRows.filter(r => r.ok);
const unresolved = allRows.filter(r => !r.ok);

console.log(`${write ? '已写入' : '待写入'}：${ok.length} 处，涉及 ${changed} 个文件\n`);
for (const r of ok) {
  console.log(`  ${r.file}:${r.line}  ${r.icon} -> "${r.label}"   [${r.why}]`);
}

if (unresolved.length) {
  console.log(`\n需人工判断 ${unresolved.length} 处（本轮不动）：`);
  for (const r of unresolved) console.log(`  ${r.file}:${r.line}  ${r.why}`);
}

if (!write && ok.length) console.log('\n（干跑，加 --write 落盘）');
