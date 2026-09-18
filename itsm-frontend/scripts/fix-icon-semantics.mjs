/**
 * 批次 2：修正语义倒置的按钮图标。
 *
 * 机械迁移只做等价替换，不修语义。所以迁移之后仍然有两类错：
 *
 *   1. RotateCcw（逆时针）被当成「刷新」用了 ~35 处 —— 迁移时按 DEFERRED 跳过。
 *   2. SyncOutlined 被当成「重置」用了 4 处（来源是 RefreshCw 当重置），
 *      CloseOutlined 被当成「重置」用了 3 处（来源是 X 当重置）。
 *
 * 2026-09-18 人工拍板：刷新 -> SyncOutlined（迁移已让 60 处按钮位在用它，零改动收口），
 * 重置 -> ClearOutlined（清空语义，与配对的「查询」成对）。
 *
 * 刻意**不动**的：RotateCcw 用于「重试」「版本回滚」「恢复默认」的位置——那些逆时针
 * 箭头是对的。判定靠按钮文案与 onClick 处理函数名，判不出来的拒绝改动并报告。
 *
 * 用法：
 *   node scripts/fix-icon-semantics.mjs            # 干跑，只报告分类
 *   node scripts/fix-icon-semantics.mjs --write    # 落盘
 */

import { readFileSync, writeFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import ts from 'typescript';

/** 刷新：迁移已让 SyncOutlined 成为既成事实，选它等于零改动收口 */
const REFRESH_ICON = 'SyncOutlined';
/** 重置：清空语义 */
const RESET_ICON = 'ClearOutlined';

/**
 * 这些文案说明按钮不是「刷新/重置」，一律不动。
 * 取消/关闭 用 CloseOutlined 本来就是对的；
 * 重试/回滚/恢复* 的逆时针箭头也是对的（2026-09-18 已人工确认这一批的保留范围）。
 */
const KEEP_TEXT = /重试|回滚|恢复|撤销|还原|取消|关闭/;
/** 刷新 */
const REFRESH_TEXT = /刷新|同步/;
/** 重置 */
const RESET_TEXT = /重置|清空|清除/;

/** i18n key 形态的「恢复默认」 */
const RESTORE_KEY = /resetDefault|restoreDefault|resetToDefault/i;

/** 只影响布局的 class，换 antd 后由 token 接管，应当剥掉 */
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

/**
 * 本批的契约是「只换 glyph」。所以图标上的 className 必须原样留下（剥掉纯布局类），
 * 其余没预期的属性一律拒绝改动——静默丢属性等于顺手改样式，那是另一件事。
 * aria-hidden 由替换模板统一补；尺寸类交给 antd 继承。
 */
function iconAttrs(node, sf) {
  const kept = [];
  for (const a of node.attributes.properties) {
    if (!ts.isJsxAttribute(a) || !ts.isIdentifier(a.name)) {
      return { ok: false, reason: '图标上有 JSX 展开属性' };
    }
    const name = a.name.text;
    if (name === 'aria-hidden' || name === 'size' || name === 'strokeWidth') continue;
    if (name === 'className') {
      // 动态 className（className={loading ? 'animate-spin' : ''}）原样保留
      if (!a.initializer || !ts.isStringLiteral(a.initializer)) {
        kept.push(a.getText(sf));
        continue;
      }
      const rest = a.initializer.text
        .split(/\s+/)
        .filter(Boolean)
        .filter(c => !isLayoutClass(c))
        .join(' ');
      if (rest) kept.push(`className=${JSON.stringify(rest)}`);
      continue;
    }
    return { ok: false, reason: `图标上有未预期的属性 ${name}` };
  }
  return { ok: true, kept };
}

/** onClick 处理函数名里的刷新线索 */
const REFRESH_HANDLER = /refresh|reload|loadData|load\b|fetch|reloadData/i;
/** onClick 处理函数名里的重置线索 */
const RESET_HANDLER = /reset|clear/i;
/** onClick 处理函数名里说明「不该动」的线索 */
const KEEP_HANDLER = /retry|rollback|revert|restore/i;

function walk(dir, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (!/node_modules|\.next|__tests__|__mocks__/.test(entry.name)) walk(p, out);
    } else if (/\.tsx$/.test(entry.name)) out.push(p);
  }
  return out;
}

const attrName = a => (ts.isJsxAttribute(a) && ts.isIdentifier(a.name) ? a.name.text : null);

function namedImports(sf, moduleName) {
  for (const stmt of sf.statements) {
    if (!ts.isImportDeclaration(stmt)) continue;
    if (!ts.isStringLiteral(stmt.moduleSpecifier)) continue;
    if (stmt.moduleSpecifier.text !== moduleName) continue;
    const b = stmt.importClause?.namedBindings;
    if (b && ts.isNamedImports(b)) return b;
  }
  return null;
}

/** 按钮的可读文案：children 文本（含 {t('...')} 里的中文）拼接 */
function textOf(el, sf) {
  if (!ts.isJsxElement(el)) return '';
  let out = '';
  const scan = n => {
    if (ts.isJsxText(n)) out += n.text;
    else if (ts.isStringLiteral(n)) out += ' ' + n.text;
    ts.forEachChild(n, scan);
  };
  for (const c of el.children) scan(c);
  return out.trim();
}

/** 从证据判定意图 */
function classify({ text, ariaLabel, title, handler, icon }) {
  const hay = [text, ariaLabel, title].filter(Boolean).join(' ');
  const handlerText = handler || '';

  // 「恢复默认」是还原语义（把我改过的设置还原回去），不是清空。
  // 文案走 i18n 时拿到的是 key（notifications.resetDefault），中文匹配不到，单独认一下。
  if (RESTORE_KEY.test(hay)) return { action: 'keep', why: `恢复默认（${hay}）` };

  // 「不该动」优先：重试/回滚/恢复默认 的逆时针箭头是正确的
  if (KEEP_TEXT.test(hay) || KEEP_HANDLER.test(handlerText)) {
    return { action: 'keep', why: KEEP_TEXT.test(hay) ? `文案「${hay}」` : `处理函数 ${handlerText}` };
  }

  // 只处理三种待修图标，其余一律不碰
  const fixable = icon === 'RotateCcw' || icon === 'SyncOutlined' || icon === 'CloseOutlined';
  if (!fixable) return { action: 'skip', why: `图标 ${icon} 不在本次范围` };

  // RotateCcw 在现有代码里既被当「刷新」也被当「重置」用过，两种都要认。
  // （先判重置：文案「重置」不该被 REFRESH_TEXT 的「同步」之类误吞。）
  if (icon === 'RotateCcw') {
    if (RESET_TEXT.test(hay) || RESET_HANDLER.test(handlerText)) {
      return { action: 'reset', to: RESET_ICON, why: RESET_TEXT.test(hay) ? `文案「${hay}」` : `处理函数 ${handlerText}` };
    }
    if (REFRESH_TEXT.test(hay) || REFRESH_HANDLER.test(handlerText)) {
      return { action: 'refresh', to: REFRESH_ICON, why: REFRESH_TEXT.test(hay) ? `文案「${hay}」` : `处理函数 ${handlerText}` };
    }
    return { action: 'unknown', why: `图标 RotateCcw，但文案「${hay}」与处理函数「${handlerText}」都推不出意图` };
  }

  // SyncOutlined / CloseOutlined 当「重置」用
  if (RESET_TEXT.test(hay) || RESET_HANDLER.test(handlerText)) {
    return { action: 'reset', to: RESET_ICON, why: RESET_TEXT.test(hay) ? `文案「${hay}」` : `处理函数 ${handlerText}` };
  }

  return { action: 'skip', why: `图标 ${icon} 但不像重置（文案「${hay}」），不动` };
}

function processFile(file) {
  const src = readFileSync(file, 'utf8');
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);

  const lucide = namedImports(sf, 'lucide-react');
  const antd = namedImports(sf, '@ant-design/icons');
  const lucideNames = new Map();
  if (lucide) {
    for (const el of lucide.elements) {
      lucideNames.set(el.name.text, el.propertyName ? el.propertyName.text : el.name.text);
    }
  }

  const rows = [];
  const visits = [];
  const lineOf = n => sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1;

  const visit = node => {
    const isButton =
      (ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node)) &&
      (ts.isJsxElement(node) ? node.openingElement : node).tagName.getText(sf) === 'Button';

    if (isButton) {
      const opening = ts.isJsxElement(node) ? node.openingElement : node;
      const attrs = opening.attributes.properties.filter(ts.isJsxAttribute);
      const iconAttr = attrs.find(a => attrName(a) === 'icon');
      const expr = iconAttr?.initializer;
      const iconEl =
        expr && ts.isJsxExpression(expr) && expr.expression && ts.isJsxSelfClosingElement(expr.expression)
          ? expr.expression
          : null;

      if (iconEl) {
        const tag = iconEl.tagName.getText(sf);
        const exported = lucideNames.get(tag) || tag; // antd 图标直接用标签名
        const get = n => attrs.find(a => attrName(a) === n);
        // 可访问名称常写成模板字符串：aria-label={`回滚到版本 v${record.version}`}。
        // 取字面量头部即可——判定靠中文动作词，变量尾部不会含这些词。
        // 注意 NoSubstitutionTemplateLiteral 不是 StringLiteral（kind 是 FirstTemplateToken），
        // 两个判断都要留，否则「纯模板字符串」这种没有变量的写法会漏成 null。
        const lit = a => {
          let init = a?.initializer;
          if (!init) return null;
          // 花括号形态要再剥一层：aria-label={...} 的 initializer 是 JsxExpression 容器，
          // 不是里面的字面量。只在花括号形态下才有这个问题——裸字符串的 initializer 就是字面量。
          if (ts.isJsxExpression(init)) init = init.expression;
          if (!init) return null;
          if (ts.isStringLiteral(init) || ts.isNoSubstitutionTemplateLiteral(init)) return init.text;
          if (ts.isTemplateExpression(init)) return init.head.text;
          return null;
        };

        // 只认「裸函数名」与「对象.方法」两种 onClick 形态，绝不拿内联函数体的文本去匹配。
        // 实测教训：TicketDetail 的三个「取消」按钮体里都有 xxxForm.resetFields()，
        // 用整段文本匹配会把它们误判成重置按钮，把 CloseOutlined 改成 ClearOutlined。
        const handlerAttr = get('onClick');
        const handlerExpr =
          handlerAttr?.initializer && ts.isJsxExpression(handlerAttr.initializer)
            ? handlerAttr.initializer.expression
            : null;
        const handler = !handlerExpr
          ? null
          : ts.isIdentifier(handlerExpr)
            ? handlerExpr.text
            : ts.isPropertyAccessExpression(handlerExpr)
              ? handlerExpr.name.text
              : null;

        let verdict = classify({
          text: textOf(node, sf),
          ariaLabel: lit(get('aria-label')),
          title: lit(get('title')),
          handler,
          icon: exported,
        });

        // 要落盘的位点再看一眼图标自身的属性：有没法安全保留的东西就退回「拒绝改动」。
        let kept = [];
        if (verdict.action === 'refresh' || verdict.action === 'reset') {
          const attrs = iconAttrs(iconEl, sf);
          if (attrs.ok) kept = attrs.kept;
          else verdict = { action: 'unknown', why: `${exported} -> ${verdict.to} 被拒：${attrs.reason}` };
        }

        if (verdict.action !== 'skip') {
          rows.push({ file, line: lineOf(iconEl), icon: exported, tag, ...verdict });
          if (verdict.action === 'refresh' || verdict.action === 'reset') {
            visits.push({ iconEl, tag, to: verdict.to, from: exported, kept });
          }
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);

  return { src, sf, rows, visits, lucide, antd };
}

// ---- 主流程 ----
const write = process.argv.includes('--write');
const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');

const allRows = [];
const plan = [];

for (const file of walk(path.join(root, 'src'))) {
  const { src, sf, rows, visits, lucide, antd } = processFile(file);
  allRows.push(...rows);
  if (visits.length) plan.push({ file, src, sf, visits, lucide, antd });
}

const rel = f => path.relative(root, f);
const by = a => allRows.filter(r => r.action === a);

for (const r of by('refresh')) console.log(`刷新  ${rel(r.file)}:${r.line}  ${r.icon} -> ${r.to}   [${r.why}]`);
for (const r of by('reset')) console.log(`重置  ${rel(r.file)}:${r.line}  ${r.icon} -> ${r.to}   [${r.why}]`);
console.log(`\n保持不动 ${by('keep').length} 处：`);
for (const r of by('keep')) console.log(`  ${rel(r.file)}:${r.line}  ${r.icon}   [${r.why}]`);

if (by('unknown').length) {
  console.log(`\n推不出意图、拒绝改动 ${by('unknown').length} 处：`);
  for (const r of by('unknown')) console.log(`  ${rel(r.file)}:${r.line}  ${r.why}`);
}

console.log(`\n合计：刷新 ${by('refresh').length} | 重置 ${by('reset').length} | 不动 ${by('keep').length} | 待判 ${by('unknown').length}`);

if (!write) {
  console.log('\n（干跑，加 --write 落盘）');
  process.exit(0);
}

// ---- 落盘 ----
let changedFiles = 0;
for (const { file, src, sf, visits, lucide, antd } of plan) {
  const edits = visits.map(v => ({
    start: v.iconEl.getStart(sf),
    end: v.iconEl.getEnd(),
    text: `<${v.to} aria-hidden="true"${v.kept.map(k => ' ' + k).join('')} />`,
  }));

  const removedLucide = new Set(visits.filter(v => v.from === 'RotateCcw').map(v => v.tag));
  const addedAntd = new Set(visits.map(v => v.to));

  // 只有全文再无引用时才把名字从 lucide 导入里摘掉
  const excluded = [
    ...edits.map(e => ({ start: e.start, end: e.end })),
    ...sf.statements.filter(ts.isImportDeclaration).map(s => ({ start: s.getStart(sf), end: s.getEnd() })),
  ];
  const covered = pos => excluded.some(r => pos >= r.start && pos < r.end);
  const still = new Set();
  const scan = n => {
    if (ts.isIdentifier(n) && removedLucide.has(n.text) && !covered(n.getStart(sf))) still.add(n.text);
    ts.forEachChild(n, scan);
  };
  scan(sf);
  for (const n of still) removedLucide.delete(n);

  let out = src;
  const importEdits = [];

  // 整条 lucide 导入没人用了，必须连 `import ... from 'lucide-react'` 一起删。
  // 只把命名子句清空会留下 `import  from 'lucide-react';` —— 语法错误（实测踩过 7 个文件）。
  const lucideDecl = sf.statements.find(
    s =>
      ts.isImportDeclaration(s) &&
      ts.isStringLiteral(s.moduleSpecifier) &&
      s.moduleSpecifier.text === 'lucide-react'
  );
  // 连行尾换行一起吃掉，否则删完会留下一行空行。
  // 调用方若要用别的内容顶替这个位置，必须自己把 '\n' 补回去。
  const removeLucideDecl = text => {
    const end = lucideDecl.getEnd();
    return {
      start: lucideDecl.getStart(sf),
      end: src[end] === '\n' ? end + 1 : end,
      text,
    };
  };
  let lucideRemoval = null;
  if (lucide && lucideDecl) {
    const keep = lucide.elements.filter(el => !removedLucide.has(el.name.text));
    if (keep.length === 0) {
      lucideRemoval = removeLucideDecl('');
    } else if (keep.length !== lucide.elements.length) {
      importEdits.push({
        start: lucide.getStart(sf),
        end: lucide.getEnd(),
        text: '{ ' + keep.map(el => el.getText(sf)).join(', ') + ' }',
      });
    }
  }

  if (antd) {
    // antd 导入里改名/新增
    const existing = antd.elements.map(el => el.name.text);
    const next = new Set(existing);
    for (const v of visits) {
      // 只有当旧名字在这个文件里再没有别的用处时才移除
      next.delete(v.from);
      next.add(v.to);
    }
    const wanted = [...next].sort((a, b) => a.localeCompare(b));
    if (wanted.length !== existing.length || wanted.some((n, i) => n !== existing[i])) {
      // 但如果旧的 antd 名字在文件里还有其它引用，必须留着
      const stillUsed = new Set();
      for (const v of visits) {
        const scan2 = n => {
          if (ts.isIdentifier(n) && n.text === v.from && !covered(n.getStart(sf))) stillUsed.add(v.from);
          ts.forEachChild(n, scan2);
        };
        scan2(sf);
      }
      // 只把**本来就来自 antd 的**名字放回去。v.from 在 lucide 场景下是 lucide 的名字
      // （RotateCcw），它不在 antd 的导出里——无条件放回会把 `RotateCcw` 塞进
      // `from '@ant-design/icons'`，同时 lucide 那边还留着同名导入，直接重复声明（实测踩过）。
      for (const n of stillUsed) if (existing.includes(n)) wanted.push(n);
      const uniq = [...new Set(wanted)].sort((a, b) => a.localeCompare(b));
      importEdits.push({
        start: antd.getStart(sf),
        end: antd.getEnd(),
        text: '{\n  ' + uniq.join(',\n  ') + ',\n}',
      });
    }
    // 放在合并分支的**外面**：即使 antd 导入恰好不用改，lucide 那半边的删除也得落地
    if (lucideRemoval) importEdits.push(lucideRemoval);
  } else if (addedAntd.size) {
    const names = [...addedAntd].sort((a, b) => a.localeCompare(b));
    const line = names.length === 1
      ? `import { ${names[0]} } from '@ant-design/icons';`
      : 'import {\n  ' + names.join(',\n  ') + ",\n} from '@ant-design/icons';";
    if (lucideRemoval) {
      // 顶替整条 lucide 导入的位置。不能改成「在第一条 import 之后插入」——
      // 当 lucide 就是第一条 import 时，插入点正好落在删除区间的边界上，两个 edit 会打架。
      // 这里补回 '\n'：removeLucideDecl 把行尾换行一起吃掉了。
      importEdits.push(removeLucideDecl(line + '\n'));
    } else {
      const first = sf.statements.find(ts.isImportDeclaration);
      const at = first ? first.getEnd() : 0;
      importEdits.push({ start: at, end: at, text: '\n' + line });
    }
  } else if (lucideRemoval) {
    importEdits.push(lucideRemoval);
  }

  for (const e of [...edits, ...importEdits].sort((a, b) => b.start - a.start)) {
    out = out.slice(0, e.start) + e.text + out.slice(e.end);
  }
  writeFileSync(file, out);
  changedFiles++;
}

console.log(`\n已写入 ${changedFiles} 个文件。`);
