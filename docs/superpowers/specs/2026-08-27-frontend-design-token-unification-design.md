# 前端设计 Token 架构统一 — 设计文档

- 日期:2026-08-27
- 状态:待评审
- 范围:`itsm-frontend`(仅前端,不涉及后端)

## 背景与问题

`itsm-frontend` 目前存在三套互相独立、彼此不同步的样式取值来源:

1. `src/lib/design-system/theme.tsx`(394 行):从 `colors.ts`/`spacing.ts` 两个纯数据模块生成 Ant Design `ConfigProvider` 主题 token(颜色、字号、间距、圆角、阴影、行高、字重),并导出 `generateCSSVariables()`/`applyCSSVariables()` 用于把这些 token 写成 `--color-*`/`--spacing-*`/`--border-radius-*`/`--box-shadow-*`/`--font-size-*` CSS 变量——但这两个函数目前没有在任何地方被真正调用,是死代码。
2. `tailwind.config.mjs`:维护着一份**手工复制**的调色板(`primary`/`success`/`warning`/`error`/`neutral` 等),色值与 `colors.ts` 里的 KLN 主题色相同但是独立抄写的;还有一组 `boxShadow.soft/medium/strong`,在 `spacing.ts` 的阴影刻度里根本不存在对应定义。
3. `globals.css`:直接硬编码约 30 条 antd override 规则用的字面量十六进制颜色(`#ffffff`、`#e5e5e5`、`#404040`、`#a3a3a3`、`#1890ff`、`#ff4d4f` 等),既不引用 CSS 变量,也不引用 Tailwind 配置。

三处互不感知,改一处不会同步到另外两处。这是下列存量问题的根源:

- **字号碎片化**:全仓库 118 处 Tailwind 任意值字号(`text-[Npx]`)语法,分布在 23 个文件。同一个"标签/meta 文字"角色被 10px、11px、13px 三种字号随手实现,没有共享刻度。集中出现在 `app/(main)/tickets/prototype/page.tsx`(36×`[11px]` + 15×`[10px]`)、`service-catalog/components/ServiceItemCard.tsx`(9×`[11px]`)、`components/ticket/TicketDetail.tsx`(8×`[11px]`)、`components/ticket/ServiceRequestPanel.tsx`(6×`[11px]`)、`login/page.tsx` 等处。
- **颜色碎片化**:全仓库 1,524 处裸六位十六进制颜色字面量。其中 `#1890ff`(157 处)、`#52c41a`(149 处)、`#ff4d4f`(99 处)、`#faad14`(92 处)——共计 497 处——是 **antd 组件库自带的默认色**被当作字面量写死,而不是被声明为系统自己的状态色 token;真正的品牌主色 `#F06820`/`#F27C38` 只出现 61 次,且集中在登录页和 `colors.ts` 自身。只有 5 个文件(`PageLayout.tsx:132`、`AuthForm.tsx:205`、`admin/users/page.tsx:399`、`admin/components/RecentActivity.tsx`)通过 antd 的 `useToken()` 正确读取主题 token,其余文件里 Tailwind 类名和 antd 用法是两套互不同步的颜色体系。
- **圆角/阴影不成体系**:`rounded-lg`(269 处)、`rounded-xl`(90 处)、`rounded-2xl`(53 处)、`rounded-md`(59 处)在视觉上同属"卡片"角色的容器上混用;阴影同样混用 Tailwind 默认刻度(`shadow-sm`/`shadow-md`/`shadow-lg`)、自定义的 `shadow-soft/medium/strong`,以及若干一次性的任意值阴影。

仓库内(`docs/`、`docs/superpowers/specs/`、`docs/superpowers/plans/`)目前没有任何设计系统/样式规范文档,这是一次从零建立规范的工作。

## 目标(Goals)

1. 消除"三套配置各管各的"问题:`colors.ts`/`spacing.ts` 成为唯一数据源,`tailwind.config.mjs`、`theme.tsx`、`globals.css` 全部从它派生。
2. 补上缺失的语义状态色 token(info/success/warning/danger),把当前散落各处的 antd 默认色字面量升级为可复用的具名 token。
3. 收敛字号碎片化:统一采用标准字号刻度,消除 `text-[Npx]` 任意值写法在业务代码中的滥用。
4. 建立防复发机制(lint 规则),让"以后又写回硬编码"这件事在提交前就被拦下,而不是重复地做同一次清理。
5. 存量清理达到可核验的完成标准(而不是模糊的"改善了一些")。

## 非目标(Non-Goals)

- **不重新设计视觉风格**:不改变任何品牌色、不引入新的色板或字号体系之外的美学决策。唯一的例外是 10px/11px/13px 收敛到 12px 造成的 ≤1-2px 视觉微调(见下文"字号刻度"一节),这被视为修复不一致而非重新设计。
- **不迁移到共享组件库**:`components/ui/` 目前只有 5 个文件在使用(全仓库 215 个文件直接使用原生 antd)。把这些调用点迁移到 `ui/` 包装组件是一个量级完全不同的独立项目,本设计明确不覆盖。
- **不清理全部 1,524 处硬编码颜色**:本设计只处理其中语义明确的 497 处(antd 默认色误用)。剩余约 1,027 处(渐变、阴影等装饰性一次性数值,没有清晰的语义映射)列入 backlog,机会性清理,不作为本设计的完成标准。

## 架构:单一数据源

### 现状 → 目标

```
现状:
  colors.ts/spacing.ts ──→ theme.tsx (antd)
  tailwind.config.mjs   (独立手抄一份,不引用 colors.ts/spacing.ts)
  globals.css           (独立硬编码第三份)

目标:
  colors.ts/spacing.ts ──┬──→ theme.tsx (antd 主题,不变)
                          ├──→ tailwind.config.mjs (改为 import 而非手抄)
                          └──→ generateCSSVariables() ──→ globals.css 引用的 CSS 变量
```

### 具体改动

- `tailwind.config.mjs` 删除手工维护的 `primary`/`success`/`warning`/`error`/`neutral` 调色板与 `boxShadow.soft/medium/strong`,改为直接 `import` `src/lib/design-system/colors.ts` 与 `spacing.ts`(两者均为无 DOM/React 依赖的纯数据模块,Node 构建期可直接执行)。`boxShadow.soft/medium/strong` 并入 `spacing.ts` 的阴影刻度,消除定义不一致。
- `theme.tsx` 基本保持不变,继续从同一数据源取值;额外导出新增的状态色 token 供业务代码按需引用。
- `globals.css` 中约 30 条硬编码十六进制的 antd override 规则,改为引用 `var(--color-*)` 等 CSS 变量。
- 把当前未被调用的 `applyCSSVariables()` 接入 `app/layout.tsx` 的根挂载流程,确保这些 CSS 变量在任何组件渲染前就已写入 `document.documentElement`(消除死代码,同时是 `globals.css` 引用变量能生效的前提)。

## 新增:语义状态色 Token

在 `colors.ts` 中新增 `statusColors` 导出:`info`/`success`/`warning`/`danger`。取值直接采用代码里目前事实上已经在用的 antd 默认色本身(`#1890ff`/`#52c41a`/`#faad14`/`#ff4d4f`)——**不引入新颜色**,只是把"到处被复制粘贴的字面量"提升为"声明一次、到处引用的具名 token"。视觉输出对这部分改动是**完全无变化**的。

## 字号刻度收敛

`text-[10px]`/`text-[11px]`/`text-[13px]` 承担的都是同一个"标签/meta 文字"角色,统一收敛到 Tailwind 内置的 `text-xs`(12px)。这是本设计中唯一不满足"字面量原样替换、视觉零变化"的部分:10px 处会变大 1-2px,13px 处会变小 1-2px。此决策已在设计评审阶段与用户明确确认,视为修复既有不一致,不视为改变视觉风格。

## 防复发:ESLint 规则

在现有 ESLint 配置中新增规则,拦截:

1. 新增的任意值字号语法(`text-[任意px]` 模式)。
2. 新增的裸六位十六进制颜色字面量(`design-system` token 文件自身除外)。

复用现有的 `npm run lint` / `npm run lint:check` 检查点,不引入新的 CI 流程或工具链。

## 存量清理范围

| 类别 | 数量 | 处理方式 |
|---|---|---|
| 任意值字号(23 个文件) | 118 处 | 替换为 `text-xs` |
| antd 默认色字面量(误用作状态色) | 497 处(`#1890ff`×157、`#52c41a`×149、`#ff4d4f`×99、`#faad14`×92) | 替换为新增的 `statusColors` token 引用(Tailwind 类名上下文用对应类,内联 `style` 上下文用 `useToken()`) |
| 其余硬编码十六进制(渐变/阴影等装饰性数值) | 约 1,027 处 | **不在本设计范围内**,记录为独立 backlog,机会性清理 |

由于改动点分布在数十个文件、涉及上百处调用点,实现阶段需要按目录/模块分批推进(具体批次划分留给实现计划)。

## 验证方式

本设计的改动理论上应保持视觉输出不变(字号那 1-2px 微调除外),验证思路是"比对而非测新功能":

1. `npm run type-check` 与 `npm run lint`(新增 lint 规则应为 0 违规)。
2. 运行现有 Jest 套件,确认没有测试断言了具体的 className 或颜色字面量而被误判失败。
3. 人工挑选代表性页面(登录页、工单详情、Dashboard、服务目录、管理后台)在改动前后分别截图比对,确认无非预期视觉变化——仓库目前没有自动化视觉回归工具,这一步依赖人工核对。

## Out of Scope / Backlog

- `components/ui/` 共享组件库的全面采用(215 个文件仍在直接使用原生 antd)。
- 剩余约 1,027 处装饰性硬编码颜色(渐变、一次性阴影等)的清理。
- 任何视觉风格/品牌方向的重新设计。
