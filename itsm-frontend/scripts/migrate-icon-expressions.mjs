/**
 * 批次 5：把 `icon={表达式}` 形态里的 lucide 图标迁到 @ant-design/icons。
 *
 * 为什么单独一个脚本：批次 1 的 migrate-button-icons.mjs 只认 `icon={<X />}` 这种
 * **自闭合字面量**，遇到 `icon={cond ? <A /> : <B />}` 直接归类「复杂形态，跳过」。
 * 门禁沿用了同一个判定，于是这 11 处成了一个双方都看不见的盲区——图标还是 lucide，
 * 还是占着按钮的 .ant-btn-icon 位置，还是带着「lucide 的 width/height 是 SVG 呈现属性、
 * 优先级高于继承，所以 antd 的尺寸语义永远不生效」这个整轮改动要消灭的问题。
 *
 * 与批次 1/2 一致的契约：
 *   1. 只换 glyph 与库，不动 onClick / 条件 / 布局。尺寸类属性（size/strokeWidth/color/
 *      absoluteStrokeWidth）剥掉交给 antd 继承；className 只剥布局类，其余原样保留
 *      （TemplateCard 的 style={{color:'#faad14'}} 这类也原样留着）。
 *   2. 图标一律加 aria-hidden="true"。antd 的 AntdIcon 会给外层 span 设 role="img" +
 *      aria-label={icon.name}，不加的话按钮的可访问名称会变成英文图标名（"clock-circle"）。
 *   3. **fail-closed**：表达式里只要有一个 lucide 名字不在 button-icon-map.mjs 里，
 *      整个文件拒绝落盘，不做部分迁移——半个表达式迁过去比不迁更难查。
 *   4. **纯图标按钮必须先有可访问名称**。加了 aria-hidden 之后它就是彻底无名的按钮，
 *      不能替用户默默决定。没有名字的直接拒绝并报出来，由人工补 aria-label 后再跑。
 *
 * 用法:
 *   node scripts/migrate-icon-expressions.mjs           # 预演，只报告
 *   node scripts/migrate-icon-expressions.mjs --write   # 落盘
 */

import { readFileSync, writeFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import { LUCIDE_TO_ANTD } from './button-icon-map.mjs';

const WRITE = process.argv.includes('--write');
const ROOT = path.resolve(fileURLToPath(new URL('.', import.meta.url)), '..');

/** 尺寸类属性交给 antd 继承，剥掉 */
const DROP_ATTRS = new Set(['size', 'strokeWidth', 'color', 'absoluteStrokeWidth']);

/** 纯布局 class：剥掉后不影响语义 */
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
      if (child.expression) return true;
    } else return true;
  }
  return false;
}

/**
 * 把一个 lucide 图标元素分析成替换文本。
 * @returns {{ok:true,text:string,antdName:string,exported:string} | {ok:false,reason:string}}
 */
function planIcon(node, sf, lucideLocal) {
  const tag = node.tagName.getText(sf);
  const exported = lucideLocal.get(tag);
  if (!exported) return { ok: false, reason: `NOT_LUCIDE: ${tag} 不是从 lucide-react 导入的` };

  const antdName = LUCIDE_TO_ANTD[exported];
  if (!antdName) {
    return { ok: false, reason: `UNMAPPED: ${exported} 不在 button-icon-map.mjs 里，请先核对并补表` };
  }

  if (!ts.isJsxSelfClosingElement(node)) {
    // 带子节点的图标（如 <Star>★</Star>）在本仓没有先例，不猜
    return { ok: false, reason: `UNSUPPORTED: ${tag} 不是自闭合元素，无法安全替换` };
  }

  const kept = [];
  for (const a of node.attributes.properties) {
    if (!ts.isJsxAttribute(a)) {
      return { ok: false, reason: `图标 ${tag} 上有 JSX 展开属性，无法安全处理` };
    }
    const name = attrName(a);
    if (name === null) return { ok: false, reason: `图标 ${tag} 有无法识别的属性` };
    if (DROP_ATTRS.has(name)) continue;

    if (name === 'className') {
      if (!a.initializer || !ts.isStringLiteral(a.initializer)) {
        kept.push(a.getText(sf)); // 动态 className 原样保留
        continue;
      }
      const rest = a.initializer.text.split(/\s+/).filter(Boolean).filter(c => !isLayoutClass(c)).join(' ');
      if (rest) kept.push(`className=${JSON.stringify(rest)}`);
      continue;
    }
    kept.push(a.getText(sf));
  }

  const text = `<${antdName} aria-hidden="true"${kept.map(k => ' ' + k).join('')} />`;
  return { ok: true, text, antdName, exported };
}

/** 收集表达式子树里所有 JSX 元素节点（含表达式自身） */
function jsxNodesIn(expr) {
  const found = [];
  const walk = n => {
    if (ts.isJsxSelfClosingElement(n) || ts.isJsxElement(n)) found.push(n);
    ts.forEachChild(n, walk);
  };
  walk(expr);
  return found;
}

const walkDir = (dir, acc = []) => {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (!/node_modules|\.next|__tests__|__mocks__/.test(e.name)) walkDir(p, acc);
    } else if (/\.tsx$/.test(e.name)) acc.push(p);
  }
  return acc;
};

const results = [];
let changedFiles = 0;

for (const file of walkDir(path.join(ROOT, 'src'))) {
  const rel = path.relative(ROOT, file);
  const src = readFileSync(file, 'utf8');
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const lucideLocal = lucideImports(sf);
  if (!lucideLocal.size) continue;

  const edits = [];
  const visits = [];
  const removedLucide = new Set();
  const addedAntd = new Set();
  const blocked = [];
  const lineOf = n => sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1;

  // 父子关系，用于判断是否被 <Tooltip> 包裹
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

      if (expr) {
        // 只看表达式子树；按钮的子节点是另一回事
        const targets = jsxNodesIn(expr).filter(
          n => (ts.isJsxSelfClosingElement(n) || ts.isJsxElement(n)) && lucideLocal.has(n.tagName.getText(sf))
        );

        if (targets.length) {
          const iconOnly = !hasRealChildren(node);
          const hasLabel =
            names.has('aria-label') || names.has('title') || names.has('aria-labelledby') || wrappedInTooltip(node);

          if (iconOnly && !hasLabel) {
            blocked.push({
              line: lineOf(expr),
              msg: `NO_ACCESSIBLE_NAME: 纯图标按钮用了 lucide 图标却没有可访问名称，加了 aria-hidden 会变成彻底无名的按钮，需先补 aria-label/title/Tooltip`,
            });
          } else {
            for (const t of targets) {
              const plan = planIcon(t, sf, lucideLocal);
              if (!plan.ok) {
                blocked.push({ line: lineOf(t), msg: plan.reason });
                continue;
              }
              edits.push({ start: t.getStart(sf), end: t.getEnd(), text: plan.text });
              visits.push({ from: t.tagName.getText(sf), to: plan.antdName });
              removedLucide.add(t.tagName.getText(sf));
              addedAntd.add(plan.antdName);
            }
          }
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);

  if (!edits.length && !blocked.length) continue;

  if (blocked.length) {
    results.push({ file: rel, blocked, migrated: 0 });
    continue; // fail-closed：整个文件不动
  }

  // ---- 导入维护（与 fix-icon-semantics.mjs 同一套，踩过的坑都在那里的注释里）----
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

  const importEdits = [];
  const antd = sf.statements.find(
    s =>
      ts.isImportDeclaration(s) &&
      ts.isStringLiteral(s.moduleSpecifier) &&
      s.moduleSpecifier.text === '@ant-design/icons'
  );
  const lucideDecl = sf.statements.find(
    s =>
      ts.isImportDeclaration(s) &&
      ts.isStringLiteral(s.moduleSpecifier) &&
      s.moduleSpecifier.text === 'lucide-react'
  );
  const lucide = lucideDecl?.importClause?.namedBindings;

  // 连行尾换行一起吃，否则删完留一个空行；顶替这个位置的调用方要自己补回 '\n'
  const removeLucideDecl = text => {
    const end = lucideDecl.getEnd();
    return { start: lucideDecl.getStart(sf), end: src[end] === '\n' ? end + 1 : end, text };
  };

  let lucideRemoval = null;
  if (lucide && ts.isNamedImports(lucide)) {
    const keep = lucide.elements.filter(el => !removedLucide.has(el.name.text));
    if (keep.length === 0) lucideRemoval = removeLucideDecl('');
    else if (keep.length !== lucide.elements.length) {
      importEdits.push({
        start: lucide.getStart(sf),
        end: lucide.getEnd(),
        text: '{ ' + keep.map(el => el.getText(sf)).join(', ') + ' }',
      });
    }
  }

  if (antd && ts.isNamedImports(antd.importClause?.namedBindings)) {
    const bindings = antd.importClause.namedBindings;
    const existing = bindings.elements.map(el => el.name.text);
    const next = new Set(existing);
    for (const v of visits) {
      next.delete(v.from);
      next.add(v.to);
    }
    const wanted = [...next].sort((a, b) => a.localeCompare(b));
    if (wanted.length !== existing.length || wanted.some((n, i) => n !== existing[i])) {
      // 旧名字若在文件里还有别的引用，必须留着。只把**本来就来自 antd 的**放回去——
      // v.from 是 lucide 的名字，无条件放回会把它塞进 from '@ant-design/icons'（实测踩过）。
      const stillUsed = new Set();
      for (const v of visits) {
        const scan2 = n => {
          if (ts.isIdentifier(n) && n.text === v.from && !covered(n.getStart(sf))) stillUsed.add(v.from);
          ts.forEachChild(n, scan2);
        };
        scan2(sf);
      }
      for (const n of stillUsed) if (existing.includes(n)) wanted.push(n);
      const uniq = [...new Set(wanted)].sort((a, b) => a.localeCompare(b));
      importEdits.push({
        start: bindings.getStart(sf),
        end: bindings.getEnd(),
        text: '{\n  ' + uniq.join(',\n  ') + ',\n}',
      });
    }
    // 放在合并分支外面：即使 antd 导入不用改，lucide 那半边的删除也得落地
    if (lucideRemoval) importEdits.push(lucideRemoval);
  } else if (addedAntd.size) {
    const names = [...addedAntd].sort((a, b) => a.localeCompare(b));
    const line =
      names.length === 1
        ? `import { ${names[0]} } from '@ant-design/icons';`
        : 'import {\n  ' + names.join(',\n  ') + ",\n} from '@ant-design/icons';";
    if (lucideRemoval) {
      // 顶替整条 lucide 导入的位置（不能改成「在第一条 import 之后插入」：当 lucide 就是
      // 第一条时插入点落在删除区间边界上，两个 edit 会打架）。补回被吃掉的 '\n'。
      importEdits.push(removeLucideDecl(line + '\n'));
    } else {
      const first = sf.statements.find(ts.isImportDeclaration);
      const at = first ? first.getEnd() : 0;
      importEdits.push({ start: at, end: at, text: '\n' + line });
    }
  } else if (lucideRemoval) {
    importEdits.push(lucideRemoval);
  }

  results.push({ file: rel, migrated: edits.length, sites: visits.map(v => `${v.from} -> ${v.to}`), blocked: [] });

  if (WRITE) {
    let out = src;
    for (const e of [...edits, ...importEdits].sort((a, b) => b.start - a.start)) {
      out = out.slice(0, e.start) + e.text + out.slice(e.end);
    }
    writeFileSync(file, out);
  }
  changedFiles++;
}

// ---- 报告 ----
const blockedFiles = results.filter(r => r.blocked.length);
console.log(`\n${WRITE ? '已写入' : '预演（未落盘）'}：${changedFiles} 个文件，以下为明细\n`);
for (const r of results) {
  if (r.blocked.length) {
    console.error(`✗ ${r.file} —— 拒绝改动`);
    for (const b of r.blocked) console.error(`    :${b.line}  ${b.msg}`);
  } else {
    console.log(`✓ ${r.file}  (${r.migrated})  ${[...new Set(r.sites)].join('  ')}`);
  }
}
if (blockedFiles.length) {
  console.error(`\n有 ${blockedFiles.length} 个文件被拒绝，未落盘。`);
  process.exit(1);
}
console.log(`\n共 ${changedFiles} 个文件、${results.reduce((a, r) => a + r.migrated, 0)} 处图标。`);
