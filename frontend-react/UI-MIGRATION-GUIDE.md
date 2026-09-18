# UI 迁移契约 —— TDesign → LangCross 纯黑组件库（子代理必读）

> 目标：把仍在用 `tdesign-react` 的页面全部迁到 `@/ui/langcross` 纯黑组件库，视觉对齐
> 画布 57:1「线上 UI 全量」23 屏。**只改呈现层，不改业务逻辑、API 调用、路由行为、i18n 键。**
> 文案一律保留现有 i18n key 与实际系统口径（用户明确要求：文案按系统现状，视觉按 UI 设计）。

## 0. 硬规则（违反=返工）
1. **零 tdesign**：完成后文件内不得残留 `tdesign` 字样（import、类名 `t-*`、CSS 覆盖都算）。
2. **无蓝无绿**：全站禁用蓝色与绿色。正向/成功=白 `var(--lc-success)`(#E7E9EA)；错误=`var(--lc-danger)` #E5484D；警告=`var(--lc-warn)` #D29922。
3. **纯黑底**：页面底 `var(--lc-bg)` #000；卡片 `var(--lc-panel)` #0E1014 或 `var(--lc-raised)` #16181C；内嵌 `var(--lc-inset)` #0A0B0D。
4. **描边 1.2px**：卡片/输入框/弹层边框一律 `1.2px solid var(--lc-border-card)`（#464C58）；分隔线（单向 border-top/bottom）用 `1px solid var(--lc-border-faint)`。
5. **主按钮白底黑字**：一屏只一个 primary；次按钮 secondary 描边无底；危险动作 danger 红底白字。
6. **类名不许重名**：页面级 CSS 类必须带本页前缀（如 `.kb-`、`.models-`），禁止定义 `.tick/.mark/.head/.row` 这类裸名——历史上双名导致后代选择器污染、图标整条消失且零报错。
7. **不动的东西**：`src/ui/langcross/**`、`src/i18n/**` 的键、`@/api` 调用签名、业务分支逻辑。样式缺件时用页面级 CSS 自建，不改组件库。
8. **无 emoji**：不得新增任何 emoji（含注释）。
9. `prefers-reduced-motion` 尊重；动效只用 `transform/opacity`。

## 1. 组件速查（从 `@/ui/langcross/src` 导入）
```ts
import { Button, Field, Input, Textarea, Select, Checkbox, Switch,
         StatusPill, Badge, KeyValue, InlineBanner, Skeleton, SkeletonCard,
         EmptyState, Dialog, Drawer, ContextMenu, DataTable, Link,
         PageHeader, Toolbar, ToolbarInput, Tabs, StatCard, StatRow,
         AdminShell, AdminTopBar, Fab, useToast,
         Icon, CheckIcon, CloseIcon, SearchIcon, AlertIcon, GearIcon,
         BellIcon, KeyIcon, DocIcon, GlobeIcon, BookIcon, UploadIcon,
         DownloadIcon, PlusIcon, TrashIcon, PencilIcon, EyeIcon, EyeOffIcon,
         RefreshIcon, ChevronRightIcon, ChevronLeftIcon, ArrowRightIcon,
         ArrowUpIcon, UsersIcon, UserIcon, BuildingIcon, ChatIcon,
         ClipboardIcon, CardIcon, CrownIcon, LayersIcon, TagIcon,
         TerminalIcon, ShieldIcon, LockIcon, MailIcon, MenuIcon,
         MoreIcon, ThemeIcon, ... } from '@/ui/langcross/src'
```
逐字 API 以 `src/ui/langcross/src/*.tsx` 为准，动手前**必读**：
`Button.tsx / Input.tsx / Checkbox.tsx / StatusPill.tsx / Dialog.tsx / DataTable.tsx /
PageHeader.tsx / Tabs.tsx / StatCard.tsx / AdminShell.tsx / Skeleton.tsx / icons.tsx`
以及样式 `css/tokens.css`、`css/components.css`、`css/motion.css`。

### 映射表
| TDesign | 替代 |
|---|---|
| `Button` | `Button`（variant: primary/secondary/danger；size；`pill` 仅营销页用） |
| `Input/Textarea` | `Input`（props 兼容原生；`error` 进错误态）/ `Textarea` |
| `Select` | 原生 `<select className="lc-select">` 或 `Select`（胶囊样式） |
| `Checkbox/Switch` | `Checkbox label` / `Switch`（36×20 轨道白球） |
| `Tag` | `StatusPill tone`（状态语义）或 `Badge`（计数/版本，mono 可选） |
| `Dialog / DialogPlugin` | `Dialog`（受控：open/title/confirmText/onConfirm/onCancel/danger） |
| `Table` | `DataTable<T>`（columns: `{key,title,width?,align?,mono?,dim?,render?}`、rows、rowKey、emptyText）+ `<Link tone="danger">` 操作列 |
| `Tabs` | `Tabs items={[{key,label}]} activeKey onChange` |
| `Menu`（侧栏） | `AdminShell nav={NavItem[]}`（见 AdminShell.tsx） |
| `PageHeader 区块` | `PageHeader title desc actions` + `Toolbar`/`ToolbarInput` |
| `Card 统计` | `StatCard value label tone extra` + `StatRow` 网格 |
| `Loading` | `Skeleton`/`SkeletonCard`（呼吸骨架） |
| `Empty` | `EmptyState` |
| `Popup/Dropdown` | `ContextMenu`（菜单项）或页面级 CSS 绝对定位浮层（inset 底 + card 边 + r10） |
| `Popconfirm` | `Dialog danger`（确认弹层） |
| `MessagePlugin.*` | 组件内 `useToast().toast({title,tone})`；**非组件/lib 层用 `import { toastError, toastSuccess, toastWarn } from '@/lib/toastBus'`**（总线已接好，勿再建桥） |
| `DateRangePicker` | 两个 `Input type="date"`（`.lc-input`）并排 |
| `Cascader` | 两级 `Select`（先父后子）或按交互简化为单层 `Select` |
| `RadioGroup/Radio` | `Select` 或 `Checkbox` 组（视觉规范无 radio） |
| `Slider` | `Input type="range"` 配页面 CSS（accent-color: var(--lc-text)） |

## 2. 页面结构样板
- **后台页**（admin/*）：`AdminShell` 已在 AdminDashboard 挂——页内只做：`<PageHeader/> → <Toolbar/> → <StatRow>(可选) → <DataTable/> → 弹层`。间距：页 padding 40/24（AdminShell 默认），区块间 gap 16-20。
- **用户页**：顶栏 56px + 1px 分隔线；内容 max-width 1200 居中；键值行「标签定宽 72 + 值左对齐」，**不要** SPACE_BETWEEN 右对齐；列表操作列左对齐。
- **弹层规范**：遮罩 rgba(0,0,0,.72)；Dialog 440 宽 r14 panel 底 card 边 24 内边距；Drawer 480；右下 Toast 组件库已接管。
- **骨架屏**：加载态用 `SkeletonCard`/`Skeleton`，别留白屏。

## 3. 页面级 CSS 写法
```tsx
const CSS_XXX = `
.xxx-page{background:var(--lc-bg);color:var(--lc-text);font-family:var(--lc-font)}
.xxx-card{background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:14px;padding:20px}
`
// 组件根部：<style>{CSS_XXX}</style>
```
- 优先复用组件库已有的 `.lc-*` 类与令牌；页面 CSS 只做布局/间距/特有控件。
- 浮层/卡片顶缘可加 `box-shadow:var(--lc-panel-highlight)`（inset 受光高光）。
- 响应式：≤900 侧栏转抽屉（AdminShell 内置）、≤620 双栏转单列、表格容器横向滚动。

## 4. 范例（先读再动手）
- `src/components/PricingPage.tsx` —— 公开页整页纯黑写法（CSS 常量 + lc 组件混排）
- `src/components/Login.tsx` —— 认证表单（AuthCard/Field/Input/Checkbox/EyeIcon）
- `src/components/AiAssist.tsx` —— 浮层 + useToast
- `src/components/admin/ApiKeysP.tsx`、`src/components/admin/AdminDashboard.tsx` —— 后台页半迁移现状（把剩余 tdesign 清完）

## 5. 验收（每完成一批）
1. `npx tsc --noEmit` 零错误（用 `node_modules/.bin/tsc`，node 用 `/Users/zhangzifei/.workbuddy/binaries/node/versions/22.22.2-3/bin/node` 前缀 npx）。
2. 本批文件 `grep -c tdesign <file>` 全为 0。
3. 不跑 vitest（编排层统一跑），但不要破坏现有测试引用的 DOM 结构/类名（测试搜 `data-*`、文本、`lc-*`）。
4. 汇报：改了哪些文件、每个文件替换了哪些 TDesign 组件、有没有交互 simplification（如 Cascader→两级 Select）需要编排层知晓。
