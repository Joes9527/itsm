/**
 * 按钮图标门禁。
 *
 * 四条规则，都是 2026-09 那轮 lucide -> @ant-design/icons 迁移里用真金白银换来的：
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
 * 3. `icon={…}` 不是内联 JSX 的按钮——门禁看不见它是什么图标，**默认判违规**，
 *    除非该处有 `icon-gate: <理由>` 标注。见下面「盲区」一节。
 * 4. 同上，但形态是**整个 props 包**被 spread 进 `<Button>`（`<Button {...button}>`）。
 *    这是规则 3 之外的第二种看不见的形态：实测 WorkItemActionButton 这里漏了 6 个
 *    lucide 按钮图标——调用方写 `button={{ icon: <Pencil /> }}`，字面上完全不像按钮图标。
 *
 * ⚠️ 盲区的现状（2026-09-18 实测，别再照抄旧数字）：
 * 本门禁只检查**字面写在 `icon={...}` 里的**图标。`icon={action.icon}` 这种把值
 * 从 props / 对象字面量传进来的形态它看不见。**这个盲区不是理论问题**：实测
 * 全仓 8 个这种渲染点，其中真的漏着 lucide 按钮图标——incidents 页的批量分派/解决/
 * 关闭/删除 4 个、审批链的批量删除 1 个，都是调用方的对象字面量喂进来的，
 * 而且**线上全站扫描也扫不到**（BatchActionBar 在未选中行时 `return null`，
 * 页面级扫描根本不会渲染它）。那 5 个已迁 antd；8 个渲染点本身**全部**加了
 * `icon-gate:` 标注——值迁没迁和位点能不能被门禁看见是两回事。
 *
 * 规则 3 是 **fail-closed**：新出现一个看不见的图标位就会挂，逼作者二选一——
 * 把图标内联写进 `icon={...}`（门禁就能看），或显式标注并写明理由。
 * 规则 4 同理：包装组件必须标注，并在调用点自己核对图标来源——门禁看不到调用点。
 * `grep -rn "icon-gate:" src/` 就是当前的全部豁免清单，10 条（8 个可变图标位 + 2 个
 * spread 包装组件）。
 *
 * 注意规则 3 **不能**因为本文件没 import lucide 就跳过：要防的恰恰是
 * 「本文件不 import lucide、图标从别的文件当 prop 传进来」，AuthForm.tsx 正是如此。
 *
 * 也别忘了规则 3 只要求「标注过」，不保证标注处真的安全——标注是给人看的审查点，
 * 不是机器证明。`icon:` 对象字面量全仓 365 处（其中 268 处值是 lucide），
 * 绝大多数是 Dropdown 菜单项、统计卡片、Tab，**不是按钮图标，不在范围内**。
 *
 * 曾经的另一个盲区已经关掉：`icon={cond ? <A /> : <B />}`、`icon={x || <Plus />}`
 * 这类条件／逻辑表达式以前看不见（批次 1 的 codemod 归类为「复杂形态，跳过」，
 * 门禁沿用了同一判定），那 11 处于 2026-09-18 由 migrate-icon-expressions.mjs 迁完，
 * 本门禁同步改成走**整棵 icon 表达式子树**。别再退回只看自闭合字面量。
 *
 * ⚠️ **仍未关闭的盲区：children 位置的图标**（2026-09-18 在真浏览器里发现，未修）。
 * 本文件的图标判定只看 `icon` **属性**（`attrs.find(a => attrName(a) === 'icon')`），
 * 所以 `<Button><Search size={17} /></Button>` 这种把图标当 children 传的写法**完全看不见**。
 * 实测：应用页头有 7 个这样的 antd 按钮（search / bot / bell / moon / globe / ellipsis
 * + 个人切换按钮的 shield 与 chevron-down），**每个已登录路由都渲染**；全仓 AST 探针
 * 扫出 23 处、13 个文件（页头 6、installations 5、templates/TemplateList 2 等）。
 * **但先别急着当成缺陷**：这 7 个全都有 aria-label + title，且全都显式定尺寸
 * （14/16/17/18）——呈现属性就是想要的尺寸、实渲染也对得上，正好落在「显式定尺寸的
 * lucide 从来没错」那一侧。所以差的**不是观感也不是无障碍，是一致性与门禁覆盖**。
 * 要收的话，照规则 3/4 的做法做成 fail-closed（新出现即挂，逼作者内联或标注），
 * 别做成白名单——白名单会随代码漂移。
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

/**
 * 这个按钮上有没有 `icon-gate:` 标注。两种写法都算数：
 *
 * 1. 表达式位置的 `//` 注释——落在 opening 的前导 trivia 里，取
 *    opening.getFullStart() 到 icon 属性结尾这段文本就能看到。
 * 2. JSX 子节点位置的 `{/* … *\/}` 注释——**不在** trivia 里，TS 把它解析成
 *    紧邻的兄弟节点，所以还得回头扫同级的兄弟。遇到有实际内容的兄弟就停下，
 *    免得把上一个按钮的标注张冠李戴到下一个。
 */
function hasIconGateMark(sf, node, opening, iconAttr) {
  if (sf.text.slice(opening.getFullStart(), iconAttr.getEnd()).includes('icon-gate:')) return true;

  const container = node.parent;
  if (!container || !ts.isJsxElement(container)) return false;
  const kids = container.children;
  for (let j = kids.indexOf(node) - 1; j >= 0; j--) {
    const s = kids[j];
    if (ts.isJsxText(s)) {
      if (s.text.trim()) return false; // 中间夹了真内容，标注不属于这个按钮
      continue;
    }
    if (ts.isJsxExpression(s) && !s.expression) {
      if (s.getText(sf).includes('icon-gate:')) return true;
      continue;
    }
    return false;
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
  // 注意这里**不能**因为本文件没 import lucide 就整体跳过：规则 3（不可判定的图标位）
  // 要防的恰恰是「本文件没 import lucide、图标是从别的文件当 prop 传进来的」，
  // AuthForm.tsx 就是这种——它自己不 import lucide，却把调用方的图标送进 <Button>。

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

      // 规则 3：门禁看不见的图标位，默认失败。
      // `icon={action.icon}` 这种形态里没有 JSX，门禁无从判断它是 antd 还是 lucide；
      // 2026-09-18 实测这个盲区里真的漏着 lucide 按钮图标（incidents 的批量分派/解决/
      // 关闭/删除 4 个，审批链批量删除 1 个），全部由调用方的对象字面量喂进来。
      // 不做数据流分析，改成 fail-closed：要么把图标内联写进 icon={...}（门禁就能看），
      // 要么在此处显式声明这是有意的可变图标位。
      if (iconAttr && expr && !jsxNodesIn(expr).length && !hasIconGateMark(sf, node, opening, iconAttr)) {
        out.push({
          line: lineOf(iconAttr),
          rule: 'unverifiable-icon-slot',
          msg: `按钮的 icon 不是内联 JSX（${expr.getText(sf).slice(0, 40)}），门禁看不见它是什么图标。`
            + `请把图标内联写进 icon={...}；确实需要可变图标位的话，在此处加 "// icon-gate: <理由>" 显式声明。`,
        });
      }

      // 规则 4：整个 props 包被 spread 进 <Button> 的包装组件，门禁看不见里面的 icon。
      // WorkItemActionButton 就是这种：`<Button {...button}>`，调用方写
      // `button={{ icon: <Pencil /> }}`——字面上完全不像按钮图标。实测这里漏了
      // 6 个 lucide 按钮图标（IncidentDetail 5 个、ProblemDetail 1 个），已迁 antd。
      for (const s of opening.attributes.properties.filter(ts.isJsxSpreadAttribute)) {
        if (!hasIconGateMark(sf, node, opening, s)) {
          out.push({
            line: lineOf(s),
            rule: 'unverifiable-icon-slot',
            msg: `按钮的 props 整包 spread 进来（{...${s.expression.getText(sf).slice(0, 40)}}），`
              + `门禁看不见里面的 icon。请在此处加 "// icon-gate: <理由>" 声明这是有意的包装，并在调用点自行核对图标来源。`,
          });
        }
      }

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
