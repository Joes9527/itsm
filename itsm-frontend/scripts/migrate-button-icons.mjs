/**
 * 把按钮里的 lucide-react 图标迁到 @ant-design/icons。
 *
 * 背景见 scripts/button-icon-map.mjs 的注释：antd 的 resetIcon() 从不设 width/height，
 * 图标尺寸靠继承按钮字号，而 lucide 把 width/height 写成 SVG 呈现属性，永远赢。
 * 换库之后「24px 图标塞进 29px 小按钮」这类问题随继承自动消失。
 *
 * 两条安全规则（都不是可选项）：
 *
 * 1. 带文字的按钮 —— 图标加 aria-hidden="true"。antd 的 AntdIcon 会给外层 span 设
 *    role="img" + aria-label={icon.name}，不加 aria-hidden 的话按钮的可访问名称会变成
 *    英文图标名（"more"、"ellipsis"），比 lucide 的「没有名称」更糟。
 *
 * 2. 纯图标按钮 —— 必须先有 aria-label / title / 被 <Tooltip> 包裹，否则拒绝改动并报告。
 *    没有可访问名称的按钮加 aria-hidden 图标等于把它彻底对屏幕阅读器隐藏，不能默默做。
 *
 * 其余未知一律 fail-closed：表里没有的图标、条件表达式形态的 icon、命名冲突都报错停下，不猜。
 *
 * 用法：
 *   node scripts/migrate-button-icons.mjs <files...>            # 干跑，只报告
 *   node scripts/migrate-button-icons.mjs --write <files...>    # 落盘
 *   node scripts/migrate-button-icons.mjs --all                 # 全仓 src 下所有 tsx
 */

import { readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import ts from 'typescript';
import { LUCIDE_TO_ANTD } from './button-icon-map.mjs';

/**
 * 语义按上下文分叉的图标，本批不动，留给批次 2 定 glyph。
 * RotateCcw 在刷新场景该顺时针、在重试场景该保持逆时针，靠图标名分不出来，
 * 硬转会做错一半。出图对照见 scripts/glyph-sheet.mjs，产物在 /tmp/glyph-sheet/index.html。
 *
 * 注意别再照搬映射表里 RotateCcw -> ReloadOutlined 那条：迁移已经把 RefreshCw 换成了
 * SyncOutlined，现在 60 个按钮位在用它、ReloadOutlined 全仓 0 处。刷新到底用哪个
 * glyph 是被这个既成事实约束的，属于待人工拍板项，不是这里能默认的。
 */
const DEFERRED = new Set(['RotateCcw']);

/** 只影响布局的 class，迁移后由 antd token 接管，应当剥掉 */
const LAYOUT_CLASS = [
  /^[wh]-\d+$/,
  /^size-\d+$/,
  /^(min|max)-[wh]-\d+$/,
  /^m[trblxy]?-(\d+|auto|px)$/,
  /^space-[xy]-\d+$/,
  /^text-\[\d+(\.\d+)?(px|rem)\]$/,
  /^shrink-0$/,
  /^flex-shrink-0$/,
  /^grow$/,
  /^flex-grow$/,
  /^leading-\S+$/,
];

const isLayoutClass = cls => LAYOUT_CLASS.some(re => re.test(cls));

function walk(dir, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (!/node_modules|\.next|__tests__|__mocks__/.test(entry.name)) walk(p, out);
    } else if (/\.tsx$/.test(entry.name)) out.push(p);
  }
  return out;
}

/** 收集 lucide 的本地导入名 -> 原始导出名，处理 `import { X as Y }` */
function lucideImports(src, sf) {
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

/** JSX 元素的属性名 */
const attrName = a => (ts.isJsxAttribute(a) && ts.isIdentifier(a.name) ? a.name.text : null);

/** 元素是否自闭合（纯图标按钮的常见形态） */
const isSelfClosing = el => ts.isJsxSelfClosingElement(el);

/** 拿到 JsxElement 的 children（自闭合元素没有 children） */
const childrenOf = el => (ts.isJsxElement(el) ? el.children : []);

/** 子节点里有没有实际内容（非空白文本 / 非注释表达式） */
function hasRealChildren(el) {
  for (const child of childrenOf(el)) {
    if (ts.isJsxText(child)) {
      if (child.text.trim()) return true;
    } else if (ts.isJsxExpression(child)) {
      // {/* 注释 */} 的 expression 是 undefined（TS 6 已移除 isJsxEmptyExpression，
      // 不要再加那个判断——它不存在）。{undefined} 会算作内容，属保守方向。
      if (child.expression) return true;
    } else {
      return true;
    }
  }
  return false;
}

/** 把一串 class 里的布局类剥掉，返回剩余（可能为空） */
function stripLayoutClasses(value) {
  const kept = value.split(/\s+/).filter(Boolean).filter(cls => !isLayoutClass(cls));
  return kept.join(' ');
}

/**
 * 分析一个图标元素，产出替换计划。
 * @returns {{ok: true, replacement: string} | {ok: false, reason: string}}
 */
function planIcon(node, sf, lucideLocal) {
  const tag = node.tagName.getText(sf);
  if (!ts.isJsxSelfClosingElement(node)) {
    return { ok: false, reason: `图标 ${tag} 不是自闭合元素，无法安全替换` };
  }
  const exported = lucideLocal.get(tag);
  // 不是 lucide 图标 ≠ 出错：可能是已经迁过的 antd 图标，也可能是本地自定义组件
  // （如 login 页的 MicrosoftIcon）。这一类只跳过，不当作需要人工处理。
  if (!exported) return { ok: false, reason: `NOT_LUCIDE: ${tag} 不是从 lucide-react 导入的，跳过` };

  if (DEFERRED.has(exported)) {
    return { ok: false, reason: `DEFERRED: ${exported} 的语义按上下文分叉，留给批次 2` };
  }
  const antdName = LUCIDE_TO_ANTD[exported];
  if (!antdName) {
    return { ok: false, reason: `UNMAPPED: ${exported} 不在 button-icon-map.mjs 里，请先核对并补表` };
  }

  const attrs = node.attributes.properties.filter(ts.isJsxAttribute);
  const kept = [];
  for (const a of attrs) {
    const name = attrName(a);
    if (name === null) return { ok: false, reason: `图标 ${tag} 上有 JSX 展开属性，无法安全处理` };

    // 尺寸类属性：交给 antd 继承，剥掉
    if (name === 'size' || name === 'strokeWidth' || name === 'color' || name === 'absoluteStrokeWidth') continue;

    if (name === 'className') {
      if (!a.initializer || !ts.isStringLiteral(a.initializer)) {
        // 动态 className（如 className={loading ? 'animate-spin' : ''}）原样保留
        kept.push(a.getText(sf));
        continue;
      }
      const remaining = stripLayoutClasses(a.initializer.text);
      if (!remaining) continue; // 纯布局 class，整条属性删掉
      kept.push(`className=${JSON.stringify(remaining)}`);
      continue;
    }

    kept.push(a.getText(sf));
  }

  return { ok: true, tag, exported, antdName, kept };
}

/** 处理单个文件，返回 {edits: [...], report: [...]} */
function processFile(file, { write }) {
  const src = readFileSync(file, 'utf8');
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const lucideLocal = lucideImports(src, sf);
  const report = [];
  const edits = []; // {start, end, text}
  const neededAntd = new Set();
  const removedLucide = new Set();

  if (!lucideLocal.size) return { edits, report, src, sf, neededAntd, removedLucide };

  // 建立父子关系，用来判断是否被 <Tooltip> 包裹
  const parent = new Map();
  const link = node => {
    ts.forEachChild(node, child => {
      parent.set(child, node);
      link(child);
    });
  };
  link(sf);

  const wrappedInTooltip = node => {
    let cur = node;
    while (cur && parent.get(cur)) {
      const p = parent.get(cur);
      if ((ts.isJsxElement(p) || ts.isJsxSelfClosingElement(p)) && p.openingElement?.tagName?.getText(sf) === 'Tooltip') {
        return true;
      }
      if (ts.isJsxElement(p) && p.openingElement.tagName.getText(sf) === 'Tooltip') return true;
      cur = p;
    }
    return false;
  };

  const visit = node => {
    const isButtonEl =
      (ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node)) &&
      (ts.isJsxElement(node) ? node.openingElement : node).tagName.getText(sf) === 'Button';

    if (isButtonEl) {
      const opening = ts.isJsxElement(node) ? node.openingElement : node;
      const attrs = opening.attributes.properties.filter(ts.isJsxAttribute);
      const names = new Set(attrs.map(attrName).filter(Boolean));

      // 找出 icon={<X/>} 形态
      const iconAttr = attrs.find(a => attrName(a) === 'icon');
      if (iconAttr) {
        if (!iconAttr.initializer || !ts.isJsxExpression(iconAttr.initializer) || !iconAttr.initializer.expression) {
          report.push({ file, line: lineOf(sf, iconAttr), level: 'skip', msg: 'icon 属性不是内联 JSX，跳过' });
        } else if (
          !ts.isJsxSelfClosingElement(iconAttr.initializer.expression) &&
          !ts.isJsxElement(iconAttr.initializer.expression)
        ) {
          report.push({
            file,
            line: lineOf(sf, iconAttr),
            level: 'skip',
            msg: 'icon 是条件表达式等复杂形态，跳过',
          });
        } else {
          const el = iconAttr.initializer.expression;
          const plan = planIcon(el, sf, lucideLocal);

          if (!plan.ok) {
            report.push({
              file,
              line: lineOf(sf, el),
              level: plan.reason.startsWith('DEFERRED')
                ? 'deferred'
                : plan.reason.startsWith('NOT_LUCIDE')
                  ? 'skip'
                  : 'error',
              msg: plan.reason,
            });
          } else {
            const iconOnly = !hasRealChildren(node);
            const hasLabel =
              names.has('aria-label') || names.has('title') || names.has('aria-labelledby') || wrappedInTooltip(node);

            if (iconOnly && !hasLabel) {
              report.push({
                file,
                line: lineOf(sf, el),
                level: 'error',
                msg: `NO_ACCESSIBLE_NAME: 纯图标按钮用 ${plan.exported} 但没有任何可访问名称，需先补 aria-label/title/Tooltip`,
              });
            } else {
              const replaceTag = ts.isJsxSelfClosingElement(el) ? '<' + plan.antdName + ' aria-hidden="true"' : '';
              const closeTag = ts.isJsxSelfClosingElement(el) ? ' />' : '';
              const pieces = [replaceTag, ...plan.kept.map(k => ' ' + k), closeTag].filter(Boolean);
              edits.push({
                start: el.getStart(sf),
                end: el.getEnd(),
                text: pieces.join(''),
              });
              neededAntd.add(plan.antdName);
              removedLucide.add(plan.tag);
              report.push({
                file,
                line: lineOf(sf, el),
                level: 'migrate',
                msg: `${plan.exported} -> ${plan.antdName}${iconOnly ? ' (纯图标)' : ''}`,
              });
            }
          }
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);

  // 只有当文件里再无其它引用时，才能把名字从 lucide 导入里摘掉。
  // 同一个图标常既用在按钮里、又用在表格列等非按钮位置（如 Trash2），
  // 只按「迁移过的名字」删导入会把那些用法一起弄坏。
  // 排除区间 = 已替换的图标元素 + 所有 import 声明（导入子句本身不算「引用」）。
  const excluded = [
    ...edits.map(e => ({ start: e.start, end: e.end })),
    ...sf.statements.filter(ts.isImportDeclaration).map(s => ({ start: s.getStart(sf), end: s.getEnd() })),
  ];
  const covered = pos => excluded.some(r => pos >= r.start && pos < r.end);
  const stillUsed = new Set();
  const scan = node => {
    // JSX 标签名/属性名在 TS 的 AST 里就是普通 Identifier，没有独立的 JsxIdentifier 类型
    if (ts.isIdentifier(node) && removedLucide.has(node.text) && !covered(node.getStart(sf))) {
      stillUsed.add(node.text);
    }
    ts.forEachChild(node, scan);
  };
  scan(sf);
  for (const name of stillUsed) {
    removedLucide.delete(name);
    report.push({
      file,
      line: 0,
      level: 'note',
      msg: `${name} 在非按钮位置仍有引用，保留在 lucide 导入里`,
    });
  }

  return { edits, report, src, sf, neededAntd, removedLucide };
}

const lineOf = (sf, node) => sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1;

/** 应用 edits（从后往前，避免位移）并重写 import */
function applyEdits({ edits, src, sf, neededAntd, removedLucide }) {
  if (!edits.length) return src;

  let out = src;
  const importEdits = [];

  let lucideStmt = null;
  let antdStmt = null;
  for (const stmt of sf.statements) {
    if (!ts.isImportDeclaration(stmt) || !ts.isStringLiteral(stmt.moduleSpecifier)) continue;
    if (stmt.moduleSpecifier.text === 'lucide-react' && !lucideStmt) lucideStmt = stmt;
    if (stmt.moduleSpecifier.text === '@ant-design/icons' && !antdStmt) antdStmt = stmt;
  }

  const antdNames = [...neededAntd].sort((a, b) => a.localeCompare(b));
  // 单名走单行，多行展开只用于 2 个以上（与仓库既有风格一致）
  const formatAntdImport = names =>
    names.length === 1
      ? `import { ${names[0]} } from '@ant-design/icons';`
      : 'import {\n  ' + names.join(',\n  ') + ",\n} from '@ant-design/icons';";

  // 1. lucide 导入：剔除已迁移的名字。空了先记下「整条可删」，但**先不落 edit**——
  //    待会儿 antd 导入可能要顶替它的位置，同一区间只能有一条 edit。
  let lucideRemoval = null;
  if (lucideStmt) {
    const bindings = lucideStmt.importClause?.namedBindings;
    if (bindings && ts.isNamedImports(bindings)) {
      const keep = bindings.elements.filter(el => !removedLucide.has(el.name.text));
      if (keep.length === 0) {
        lucideRemoval = { start: lucideStmt.getStart(sf), end: lucideStmt.getEnd(), text: '' };
      } else if (keep.length !== bindings.elements.length) {
        importEdits.push({
          start: lucideStmt.getStart(sf),
          end: lucideStmt.getEnd(),
          text: 'import { ' + keep.map(el => el.getText(sf)).join(', ') + " } from 'lucide-react';",
        });
      }
    }
  }

  // 2. antd 导入：并进现成的那条；否则新建。
  //    新建时若 lucide 整条可删就顶替它的位置，否则插在它**后面**——
  //    绝不能替换一条还要留着的 lucide 导入，那会把残留图标一起删掉。
  if (antdNames.length) {
    const bindings = antdStmt?.importClause?.namedBindings;
    if (antdStmt && bindings && ts.isNamedImports(bindings)) {
      const existing = new Set(bindings.elements.map(el => el.name.text));
      const add = antdNames.filter(n => !existing.has(n));
      if (add.length) {
        const merged = [...existing, ...add].sort((a, b) => a.localeCompare(b));
        importEdits.push({
          start: antdStmt.getStart(sf),
          end: antdStmt.getEnd(),
          text: formatAntdImport(merged),
        });
      }
      if (lucideRemoval) importEdits.push(lucideRemoval);
    } else if (lucideRemoval) {
      importEdits.push({ ...lucideRemoval, text: formatAntdImport(antdNames) });
    } else if (lucideStmt) {
      importEdits.push({ start: lucideStmt.getEnd(), end: lucideStmt.getEnd(), text: '\n' + formatAntdImport(antdNames) });
    } else {
      importEdits.push({ start: 0, end: 0, text: formatAntdImport(antdNames) + '\n' });
    }
  } else if (lucideRemoval) {
    importEdits.push(lucideRemoval);
  }

  // 图标替换 + import 改动一起按位置从后往前应用
  const all = [...edits, ...importEdits].sort((a, b) => b.start - a.start);
  for (const edit of all) {
    out = out.slice(0, edit.start) + edit.text + out.slice(edit.end);
  }
  return out;
}

// ---- 主流程 ----
const argv = process.argv.slice(2);
const write = argv.includes('--write');
const all = argv.includes('--all');
const files = all
  ? walk('src')
  : argv.filter(a => !a.startsWith('--'));

if (!files.length) {
  console.error('用法: node scripts/migrate-button-icons.mjs [--write] [--all | <files...>]');
  process.exit(1);
}

const tally = { migrate: 0, deferred: 0, error: 0, skip: 0, note: 0, filesChanged: 0 };
const problems = [];

for (const file of files) {
  const result = processFile(file, { write });
  for (const r of result.report) {
    tally[r.level]++;
    if (r.level === 'error' || r.level === 'deferred') problems.push(r);
  }
  if (result.edits.length) {
    tally.filesChanged++;
    if (write) {
      writeFileSync(file, applyEdits(result));
    }
    console.log(`${write ? '写入' : '待改'} ${file}  (${result.edits.length} 处)`);
    for (const r of result.report.filter(r => r.level === 'migrate' || r.level === 'note')) {
      console.log(`    ${r.line ? 'L' + r.line + '  ' : ''}${r.msg}`);
    }
  }
}

console.log('\n' + '='.repeat(60));
console.log(`迁移 ${tally.migrate} 处 | 推迟 ${tally.deferred} | 需人工处理 ${tally.error} | 跳过 ${tally.skip}`);
console.log(`${tally.filesChanged} 个文件${write ? '已写入' : '待写入（加 --write 落盘）'}`);

if (problems.length) {
  console.log('\n需要人工处理的位点：');
  const byKind = {};
  for (const p of problems) {
    const kind = p.msg.split(':')[0];
    (byKind[kind] ||= []).push(p);
  }
  for (const [kind, list] of Object.entries(byKind)) {
    console.log(`\n[${kind}]  ${list.length} 处`);
    for (const p of list) console.log(`  ${p.file}:${p.line}`);
  }
}
