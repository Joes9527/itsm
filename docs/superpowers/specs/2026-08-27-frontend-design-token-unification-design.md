# 前端设计 Token 架构统一 — 设计文档

- 日期:2026-08-27
- 状态:待评审
- 范围:`itsm-frontend`(仅前端,不涉及后端)
- 已对照 `AGENTS.md`「架构原则」复核:单一权威源头、旧路径同改动移除、不留无期限临时例外

## 背景与问题

`itsm-frontend` 目前存在多套互相独立、彼此不同步的样式取值来源:

1. `src/lib/design-system/theme.tsx`:从 `colors.ts`/`spacing.ts` 生成 Ant Design `ConfigProvider` 主题 token,并包含运行时 CSS 变量生成函数；这些函数目前没有接入应用启动流程。
2. `tailwind.config.mjs`:维护着一份**手工复制**的调色板(`primary`/`success`/`warning`/`error`/`neutral` 等),色值与 `colors.ts` 里的 KLN 主题色相同但是独立抄写的;还有一组 `boxShadow.soft/medium/strong`,在 `spacing.ts` 的阴影刻度里根本不存在对应定义。
3. `globals.css` 与 `styles/theme-variables.css`:除了 antd override 外,还各自维护颜色、背景、边框、阴影、间距和暗色主题变量；两者存在重复定义、命名差异和后置覆盖。

这些来源互不感知,改一处不会同步到另外几处。这是下列存量问题的根源:

- **字号碎片化**:全仓库 118 处 Tailwind 任意值字号(`text-[Npx]`)语法,分布在 23 个文件。同一个"标签/meta 文字"角色被 10px、11px、13px 三种字号随手实现,没有共享刻度。集中出现在 `app/(main)/tickets/prototype/page.tsx`(36×`[11px]` + 15×`[10px]`)、`service-catalog/components/ServiceItemCard.tsx`(9×`[11px]`)、`components/ticket/TicketDetail.tsx`(8×`[11px]`)、`components/ticket/ServiceRequestPanel.tsx`(6×`[11px]`)、`login/page.tsx` 等处。
- **颜色碎片化**:源码中存在大量裸六位十六进制颜色字面量。历史统计曾将其中一批 antd 默认色视为候选状态色，但统计范围包含哪些文件、哪些调用点真正属于状态语义，必须由实现阶段的基线脚本重新确认；Tailwind 类名和 antd 用法目前仍是两套互不同步的颜色体系。
- **圆角/阴影不成体系**:`rounded-lg`(269 处)、`rounded-xl`(90 处)、`rounded-2xl`(53 处)、`rounded-md`(59 处)在视觉上同属"卡片"角色的容器上混用;阴影同样混用 Tailwind 默认刻度(`shadow-sm`/`shadow-md`/`shadow-lg`)、自定义的 `shadow-soft/medium/strong`,以及若干一次性的任意值阴影。

仓库内(`docs/`、`docs/superpowers/specs/`、`docs/superpowers/plans/`)目前没有任何设计系统/样式规范文档,这是一次从零建立规范的工作。

## 目标(Goals)

1. 建立一个可被 TypeScript、Node 构建脚本、Tailwind v4 和 CSS 消费的权威 token 数据源；React/Ant Design API 与生成的 CSS/Tailwind 产物均从该数据源派生。
2. 建立语义状态色、品牌色、图表色和用户可配置颜色的边界，不按十六进制值进行无差别替换。
3. 收敛字号碎片化:统一采用标准字号刻度,消除 `text-[Npx]` 任意值写法在业务代码中的滥用。
4. 建立防复发机制(lint 规则),让"以后又写回硬编码"这件事在提交前就被拦下,而不是重复地做同一次清理。
5. 存量清理达到可核验的完成标准(而不是模糊的"改善了一些")。

## 非目标(Non-Goals)

- **不重新设计视觉风格**:不改变任何品牌色、不引入新的色板或字号体系之外的美学决策。唯一的例外是 10px/11px/13px 收敛到 12px 造成的 ≤1-2px 视觉微调(见下文"字号刻度"一节),这被视为修复不一致而非重新设计。
- **不迁移到共享组件库**:`components/ui/` 目前只有 5 个文件在使用(全仓库 215 个文件直接使用原生 antd)。把这些调用点迁移到 `ui/` 包装组件是一个量级完全不同的独立项目,本设计明确不覆盖。
- **不清理所有硬编码颜色**:本设计只处理基线脚本识别并经人工确认的语义状态、品牌、图表和用户颜色调用点。无法安全映射的渐变、阴影、一次性装饰值列入 backlog,不作为本设计的完成标准。

## 架构:单一数据源

### 现状 → 目标

```
现状:
  colors.ts/spacing.ts ──→ theme.tsx (antd,唯一走对了的一条线)
  tailwind.config.mjs   (独立手抄一份色板,不引用 colors.ts/spacing.ts)
  globals.css 手写 :root ─┬─ 与 theme-variables.css 手写 :root 重复定义同名变量
  theme-variables.css 手写 :root ┘  (后加载的覆盖前面的,靠隐式层叠决定谁生效)

目标:
  colors.ts/spacing.ts ──┬──→ theme.tsx (antd 主题,不变)
                          └──→ 构建期 token 生成脚本
                                 ├──→ theme-variables.css(改为生成产物,不再手写)
                                 │      ├──→ globals.css 的 antd override 规则引用其中的 CSS 变量
                                 │      └──→ Tailwind v4 通过 `@theme`/变量入口消费同一份变量
                                 └──→ tailwind.config.mjs 中仍需保留的兼容项(如有)从生成产物取值,不再手抄
```

### 具体改动

- `tailwind.config.mjs` 不再被假定为 Tailwind v4 的唯一配置入口。增加构建期 token 产物，使 Tailwind v4 通过 CSS `@theme`/变量入口消费 token；不得依赖 Node 直接 import TypeScript。旧配置中的兼容项默认删除,只有在实现阶段验证仍有真实消费者时才保留,并在保留处写明消费者是谁——不得作为"以防万一"的旧路径长期并存(参见 AGENTS.md「架构原则」:新路径替换旧路径时,同一改动内移除旧路径,除非明确要求向后兼容)。
- `theme.tsx` 基本保持不变,继续从同一数据源取值;额外导出新增的状态色 token 供业务代码按需引用。
- `styles/theme-variables.css` 改为构建脚本的生成产物(唯一的手写 CSS 变量文件),不再手工维护;`globals.css` 中与之重复的手写 `:root` 变量声明**删除**,只保留真正属于"antd 组件覆盖"而非"变量定义"的规则,并把这些规则里的硬编码十六进制改为引用生成的变量。合并后两个文件里对同一变量(如 `--color-success`、`--color-info`)只允许存在一份定义、一个值——当前两处对不上的字面量(`#22c55e` vs 状态色 token 待定值等)在合并时必须被显式裁决,不能靠"哪个文件后加载哪个赢"这种隐式层叠规则决定。
- 主题 class 在 hydration 前通过无副作用的 bootstrap 脚本设置；React 组件只负责后续同步。不得在服务端 `app/layout.tsx` 直接调用访问 `document` 的函数。

## 新增:语义状态色 Token

状态 token 不按字面量值命名，也不假设所有历史调用点都是同一语义。权威 token 至少分为:

- `brand.primary`: 品牌主色及其色阶；
- `status.info/success/warning/error`: 交互和业务状态色，`error` 与 Ant Design API 对齐；
- `chart.series`: 图表序列调色板；
- `userDefined`: 用户输入或后端配置的颜色，保留值并做格式校验。

迁移必须由调用点语义决定。用户可选颜色、branding 配置、图表序列和测试 fixture 不得仅因为值等于历史颜色就替换为状态 token。

## 字号刻度收敛

`text-[10px]`/`text-[11px]`/`text-[13px]` 承担的都是同一个"标签/meta 文字"角色,统一收敛到 Tailwind 内置的 `text-xs`(12px)。这是本设计中唯一不满足"字面量原样替换、视觉零变化"的部分:10px 处会变大 1-2px,13px 处会变小 1-2px。此决策已在设计评审阶段与用户明确确认,视为修复既有不一致,不视为改变视觉风格。

## 防复发:ESLint 规则

在现有 ESLint 配置和独立的 diff 检查脚本中增加防复发机制:

1. 对业务 TS/TSX 中新增的任意值字号语法(`text-[任意px]` 模式)进行检查。
2. 对业务 TS/TSX、CSS 和可执行配置中的新增裸六位十六进制颜色字面量进行检查。
3. 明确允许列表：token 源文件、生成文件、用户颜色输入、图表 palette、测试 fixture 和迁移期间的逐行例外。迁移期例外必须带注释注明原因和清理时间点(关联 backlog 条目),不得是无说明、无期限的裸豁免——按 AGENTS.md 原则,临时例外不能变成长期与新规则并存的隐性旧路径。

存量清理完成后,ESLint 负责全量语法约束，diff 检查负责识别新增行；`npm run lint:check` 作为只读验收命令，`npm run lint` 不作为零违规证明。

## 存量清理范围

| 类别 | 数量 | 处理方式 |
|---|---|---|
| 任意值字号(23 个文件) | 118 处 | 替换为 `text-xs` |
| 候选状态色字面量 | 初始基线需由脚本按源码范围重新生成 | 逐个按调用点语义迁移到 `status`、`brand`、`chart` 或 `userDefined`；不得按颜色值全局替换 |
| 其余硬编码十六进制(渐变/阴影等装饰性数值) | 基线脚本确认后计数 | **不在本设计范围内**,记录为独立 backlog,机会性清理 |

由于改动点分布在数十个文件、涉及上百处调用点,实现阶段必须按 token 基础层、CSS/Tailwind 消费层、主题启动、语义颜色、字号、门禁和视觉验证分批推进，且每批独立通过窄范围验证。实现计划的**第一个任务**必须是编写基线审计脚本、产出"候选状态色字面量"的真实清单(文件、行号、当前值、初步语义归类),把上表里的 TBD 替换成具体数字——不允许这个数字无限期悬空。

## 验证方式

本设计的改动理论上应保持视觉输出不变(字号那 1-2px 微调除外),验证思路是"比对而非测新功能":

1. 运行 `npm run type-check` 与 `npm run lint:check`，并运行新增的 token 检查脚本；禁止项和未列入允许列表的新增值必须为 0。
2. 运行 token 单元测试，覆盖源 token、生成 CSS、Tailwind v4 入口、light/dark 变量和主题 bootstrap；运行现有 Jest 套件。
3. 在 390px 和桌面宽度下，对登录页、工单详情、Dashboard、服务目录和管理后台分别验证 light/dark 模式，至少覆盖主题切换、下拉框、状态标签、统计图表和字号迁移后的布局。仓库目前没有自动化视觉回归工具,具体做法是对这五个页面 × 两种宽度 × light/dark 在改动前后分别截图,人工比对确认无非预期变化(字号那 1-2px 微调除外)。
4. 记录可复现的基线命令、排除目录、允许列表和迁移后的剩余计数；不再使用未定义范围的“全仓库数量”。

## Out of Scope / Backlog

- `components/ui/` 共享组件库的全面采用(215 个文件仍在直接使用原生 antd)。
- 未能安全映射的装饰性硬编码颜色(渐变、一次性阴影等)的清理。
- 任何视觉风格/品牌方向的重新设计。
- 运行时由业务数据动态创建任意 Tailwind class；动态颜色必须使用 CSS 变量、Ant Design token 或受控 palette。
