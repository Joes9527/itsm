/**
 * 按钮图标门禁。
 *
 * 两条规则，都是 2026-09 那轮 lucide -> @ant-design/icons 迁移里用真金白银换来的：
 *
 * 1. 按钮里的图标不许再来自 lucide-react。
 *    不是审美问题：antd 的 resetIcon() 只给 .ant-btn-icon > svg 设 display/color/
 *    line-height/vertical-align，从不设 width/height/font-size，尺寸靠继承按钮字号；
 *    而 lucide 把 width/height 写成 SVG **呈现属性**（默认 24），优先级高于继承。
 *    两者混用的话，lucide 那些 size={N} / w-N h-N 会一直赢，antd 的尺寸语义永远不生效。
 *
 * 2. 纯图标按钮必须有可访问名称（aria-label / title / aria-labelledby / 被 Tooltip 包裹）。
 *    antd 的 AntdIcon 会给外层 span 设 role="img" + aria-label={icon.name}，带文字的按钮
 *    靠 aria-hidden 挡掉；纯图标按钮没名字的话，按钮的可访问名称会变成英文图标名
 *    （"more"、"delete"），比 lucide 时代的「没有名字」更糟。
 *
 * 为什么不用 ESLint：no-restricted-imports 无法按 JSX 上下文区分（非按钮图标**应该**
 * 继续用 lucide）；no-restricted-syntax 的选择器解析不了「这个图标是不是来自 lucide」。
 * 为什么不用根目录的 check-engineering-contracts.js：那是全文件正则，对 JSX 不适用。
 *
 * ⚠️ 已知盲区（2026-09-18 实测，别把它当成「全仓都干净了」）：
 * 本门禁只检查**字面写在 `icon={...}` 里的**图标。`icon={action.icon}` 这种把值
 * 从 props / 对象字面量传进来的形态它看不见，而仓里确实有这种位点：
 * `<Button icon={action.icon}>` 共 6 个渲染点（lib/templates/ui.tsx:135/144、
 * BatchActionBar.tsx:103/127、CSDMHub.tsx:104、ApprovalChainTable.tsx:221），
 * 图标值来自同文件或调用方的对象字面量。**其中有多少是 lucide 尚未测准**——
 * 仓里 `icon:` 对象字面量共 264 处 / 56 文件，但绝大多数是统计卡片、Tab、
 * 空状态图标而非按钮图标，要精确区分得做数据流分析，本门禁不做。
 * 所以「门禁通过」的正确读法是「**在字面形态里**没有违规」。
 *
 * 曾经的另一个盲区已经关掉：`icon={cond ? <A /> : <B />}`、`icon={x || <Plus />}`
 * 这类条件／逻辑表达式以前看不见（批次 1 的 codemod 归类为「复杂形态，跳过」，
 * 门禁沿用了同一判定），那 11 处于 2026-09-18 由 migrate-icon-expressions.mjs 迁完，
 * 本门禁同步改成走**整棵 icon 表达式子树**。别再退回只看自闭合字面量。
 *
 * 用法:
 *   node scripts/check-button-icons.mjs          # 检查 src/，有违规退出码 1
 *   node scripts/check-button-icons.mjs --json   # 机器可读
 */

import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

// 门禁没有豁免集。曾经有过一个（批次 2 待定的 RotateCcw），2026-09-18 那 7 处
// 「重试 / 回滚」选型落地后已删除——留一个长期豁免集等于给门禁开个永久窟窿，
// 而这 7 个按钮恰恰还带着整个改动要消除的尺寸问题。别再把它加回来。

const attrName = a => (ts.isJsxAttribute(a) && ts.isIdentifier(a.name) ? a.name.text : null);

/** 收集 lucide 的本地导入名 -> 导出名（处理 `import { X as Y }`） */
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

/** 按钮有没有实际文字内容（决定它是不是「纯图标按钮」） */
function hasRealChildren(el) {
  if (!ts.isJsxElement(el)) return false;
  for (const child of el.children) {
    if (ts.isJsxText(child)) {
      if (child.text.trim()) return true;
    } else if (ts.isJsxExpression(child)) {
      // {/* 注释 */} 的 expression 是 undefined。别用 isJsxEmptyExpression——TS 6 已删。
      if (child.expression) return true;
    } else return true;
  }
  return false;
}

/** 表达式子树里所有 JSX 元素节点（含表达式自身） */
function jsxNodesIn(expr) {
  const found = [];
  const walk = n => {
    if (ts.isJsxSelfClosingElement(n) || ts.isJsxElement(n)) found.push(n);
    ts.forEachChild(n, walk);
  };
  walk(expr);
  return found;
}

/**
 * 核心检查，纯函数，便于单测。
 * @returns {Array<{line:number, rule:string, msg:string}>}
 */
export function findViolations(fileName, src) {
  const sf = ts.createSourceFile(fileName, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const lucide = lucideImports(sf);
  if (!lucide.size) return [];

  const parent = new Map();
  const link = n => {
    ts.forEachChild(n, c => {
      parent.set(c, n);
      link(c);
    });
  };
  link(sf);

  const wrappedInTooltip = node => {
    let cur = node;
    while (cur && parent.get(cur)) {
      const p = parent.get(cur);
      const opening = ts.isJsxElement(p) ? p.openingElement : ts.isJsxSelfClosingElement(p) ? p : null;
      if (opening && opening.tagName.getText(sf) === 'Tooltip') return true;
      cur = p;
    }
    return false;
  };

  const out = [];
  const lineOf = n => sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1;

  const visit = node => {
    const isButton =
      (ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node)) &&
      (ts.isJsxElement(node) ? node.openingElement : node).tagName.getText(sf) === 'Button';

    if (isButton) {
      const opening = ts.isJsxElement(node) ? node.openingElement : node;
      const attrs = opening.attributes.properties.filter(ts.isJsxAttribute);
      const names = new Set(attrs.map(attrName).filter(Boolean));

      const iconAttr = attrs.find(a => attrName(a) === 'icon');
      const init = iconAttr?.initializer;
      const expr = init && ts.isJsxExpression(init) ? init.expression : null;

      // 走**整棵** icon 表达式子树。`icon={cond ? <A /> : <B />}`、`icon={x || <Plus />}`
      // 里的图标一样是按钮图标、一样带着尺寸问题，只看自闭合字面量会漏掉它们。
      const lucideIcons = expr
        ? jsxNodesIn(expr).filter(n => lucide.has(n.tagName.getText(sf)))
        : [];

      for (const iconEl of lucideIcons) {
        out.push({
          line: lineOf(iconEl),
          rule: 'lucide-button-icon',
          msg: `按钮图标 ${iconEl.tagName.getText(sf)} 来自 lucide-react；按钮图标必须用 @ant-design/icons`,
        });
      }

      // 纯图标按钮的可访问名称：只要还是 lucide 图标，迁移后就会失去名字，所以现在就报。
      // 一个按钮只报一次，不按图标个数重复报。
      if (lucideIcons.length && !hasRealChildren(node)) {
        const hasLabel =
          names.has('aria-label') || names.has('title') || names.has('aria-labelledby') || wrappedInTooltip(node);
        if (!hasLabel) {
          const which = [...new Set(lucideIcons.map(i => i.tagName.getText(sf)))].join('/');
          out.push({
            line: lineOf(lucideIcons[0]),
            rule: 'no-accessible-name',
            msg: `纯图标按钮（${which}）没有可访问名称，需补 aria-label / title / Tooltip`,
          });
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return out;
}

// ---- CLI ----
const isMain = process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url);

if (isMain) {
  const json = process.argv.includes('--json');
  const root = path.resolve(fileURLToPath(new URL('.', import.meta.url)), '..');

  const walk = (dir, acc = []) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) {
        if (!/node_modules|\.next|__tests__|__mocks__/.test(e.name)) walk(p, acc);
      } else if (/\.tsx$/.test(e.name)) acc.push(p);
    }
    return acc;
  };

  const findings = [];
  for (const file of walk(path.join(root, 'src'))) {
    const rel = path.relative(root, file);
    for (const v of findViolations(rel, readFileSync(file, 'utf8'))) findings.push({ file: rel, ...v });
  }

  if (json) {
    console.log(JSON.stringify(findings, null, 2));
  } else if (findings.length === 0) {
    console.log('按钮图标门禁通过：没有 lucide 按钮图标，没有无名纯图标按钮。');
  } else {
    const byRule = {};
    for (const f of findings) (byRule[f.rule] ||= []).push(f);
    for (const [rule, list] of Object.entries(byRule)) {
      console.error(`\n[${rule}] ${list.length} 处`);
      for (const f of list) console.error(`  ${f.file}:${f.line}  ${f.msg}`);
    }
    console.error(`\n共 ${findings.length} 处违规。`);
  }
  process.exit(findings.length ? 1 : 0);
}
