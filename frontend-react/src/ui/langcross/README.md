# @langcross/ui

LangCross 纯黑 UI 的 React 组件库。零运行时依赖（React 之外），样式全部走 CSS 变量，与画布《LangCross UI 组件库》的「00 · 设计令牌」一一对应。

## 接入

```tsx
// 入口引入一次（顺序：先 token 后组件）——实际以仓库相对路径引入，见 src/main.tsx
import "./ui/langcross/css/tokens.css";
import "./ui/langcross/css/components.css";

import { ToastProvider } from "@/ui/langcross/src";

export default function App() {
  return <ToastProvider>{/* ... */}</ToastProvider>;
}
```

包以 TS 源码直出（`main`/`exports` 指向 `src`），消费方用自己的构建链（Vite / Next / webpack + ts-loader）转译即可，无需本包预构建。若要发私有 npm 前预构建，加一个 `vite lib mode` 或 `tsup` 配置即可。

## 组件一览（与画布分区对应）

| 画布分区 | 组件 | 要点 |
|---|---|---|
| 00 令牌 | `css/tokens.css` | `--lc-*` 全套变量；描边五档按对 #000 对比度定 |
| 01 按钮 | `Button` | primary / secondary / danger / disabled；`pill` 胶囊、`sm` 小尺寸 |
| 02 表单 | `Field` `Input` `Textarea` `Select` `Checkbox` `Switch` | 输入高 36 内嵌底；错误态描边 `#E5484D` + 12px 错误文案；选中态白底黑勾（X/Grok 单色） |
| 03 状态 | `StatusPill` `Badge` `KeyValue` | 键值行「标签定宽 72 + 值左对齐」，禁止右对齐 |
| 04 反馈 | `InlineBanner` `ToastProvider`/`useToast` `Skeleton` `SkeletonCard` `EmptyState` | Toast 停靠右上（72/24）最多 3 条；骨架呼吸 1.4s |
| 05 浮层 | `Dialog` `Drawer` `ContextMenu` | 对话框 440/圆角 14/遮罩 72%；危险对话框整框 `#402323`；菜单末项 danger 自动加 1px 分隔 |
| 06 后台 Shell（管理后台 11 屏提炼） | `AdminShell` `AdminTopBar` `Fab` `FooterBar` | 侧栏 238 `#0A0B0D`，活跃菜单 = raised 底 + 白字；顶栏右侧角色/租户/账号三胶囊；版权条 `#050607` |
| 07 数据件 | `StatCard`/`StatRow` `DataTable` `Link` `Tabs` `ChipGroup` | 统计卡 raised 底 + 顶缘高光；表格 = card 边容器 + 表头浮面 + 行 46 分隔；页签活跃白字 2px 下划线；chip 活跃白字 |
| 08 页面骨架 | `PageHeader` `Toolbar`/`ToolbarInput` `AuthCard` | 页头 20/600 + 灰描述 + 右侧动作；筛选行 gap 8；认证卡 400 宽 panel，外层配 `.lc-auth-bg` |
| 09 移动端（画布「移动端 UI · 390」页提炼） | `MobScreen` `StatusBar` `MobTopBar` `TabBar` `ListCard` `MStat`/`MStatGrid` `MSearch` `MSection` `MobButton` | 390 基准：状态栏 44 + 顶栏 52 + 内容栈（16 边距/gap 12）；TabBar 浮动胶囊 62 高 r36，活跃项实心白底黑字；列表卡 = 状态点 + 标题/meta/摘要 + 操作区 |

可视化预览：`demo.html` / `demo-mobile.html` / `demo-mobile-motion.html` 三份演示页随设计交接包（`前端及UI相关/langcross-handoff.zip`）分发，未入库；仓库内效果以落地页 `/`、定价页 `/pricing` 与后台 `/admin` 为准。

## 移动端动效约定（与全站纯黑动效十原则对应）

1. **进场节拍**：状态栏/顶栏即时在位，内容 60→120→180→230→290ms 逐级现身（只动 opacity/transform，stagger 用 animation-delay）。
2. **进度必须带名字**：翻译流程 = 术语检索 → 机器翻译 → 术语校准三个检查点，等速 900ms。
3. **峰值是一个时刻**：术语校准行三连点亮（260ms 步进等速）；峰值前整块压暗 brightness .84，收放不对称（沉 .5s / 放 .28s）。
4. **TabBar 换场**：旧页 120ms 快退（-6px），新页 200ms 缓进（+8px）；活跃 tab 换色不搬家，图标按压 scale(.88)。
5. **抽屉**：开 260ms / 收 180ms，遮罩 220ms；JS await 编排 + generation 计数器 + reset 清态。
6. **打字机**：visibility:hidden 占位撑高，真身绝对定位（窄屏不跳高）。
7. 全部尊重 `prefers-reduced-motion`（直接到终态）。

## 响应式约定

- `tokens.css` 记录断点口径：**≤900px** 后台侧栏转抽屉（`AdminShell` 自带 ☰ 汉堡 + 遮罩，`.lc-sidebar--open` 拉回）、指标卡重排；**≤620px** 页头纵排、工具栏输入整行、表格横向滚动、对话框/抽屉占满减 32。
- 平板（834）为演示终态：用户端即时翻译双栏重排、后台侧栏保留 —— 见画布「移动端 UI · 390」页第三排。

## 使用示例

```tsx
import { Button, Field, Input, useToast, StatusPill, KeyValue, Dialog } from "@langcross/ui";
import { useState } from "react";

export function Demo() {
  const { toast } = useToast();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const dup = name === "销售线索库_v2";

  return (
    <div className="lc-root">
      <Field label="术语库名称" error={dup ? "名称已被占用，换一个试试" : undefined}>
        <Input
          value={name}
          error={dup}
          placeholder="请输入术语库名称"
          onChange={(e) => setName(e.target.value)}
        />
      </Field>

      <StatusPill tone="success">已生效</StatusPill>
      <KeyValue label="术语库" value="汽车行业 · 128 条" />
      <KeyValue label="更新时间" value="2026-09-16 20:40" mono />

      <Button onClick={() => toast({ title: "译文已导出", desc: "langcross-zh-en-0916.txt · 12 KB" })}>
        导出译文
      </Button>

      <Dialog
        open={open}
        danger
        title="删除术语库？"
        confirmText="确认删除"
        onCancel={() => setOpen(false)}
        onConfirm={() => setOpen(false)}
      >
        「汽车行业术语库」将被永久删除，128 条行业词表无法恢复。
      </Dialog>
    </div>
  );
}
```

## 硬规则（实现时不可破）

1. **一屏一个主按钮**（primary 白底黑字）；其余动作一律 secondary，危险动作 danger。
2. **描边按对比度定档（第 11 轮已整体上提）**：强外框 `#8B939F`（6.0:1）→ 完成态 `#6E7683`（4.7:1）→ 输入边 `#5A6270`（3.9:1）→ 分隔/轨道 `#464C58`（3.2:1）→ 胶囊/次按钮 `#424956`（3.0:1）→ 卡片边 `#3A404C`（2.5:1）。不要凭「看着行」调边框；用户反馈「偏浅偏细」= 对比度不足，先提档再说。
3. **类名不与令牌重名**：状态类与组件类分属两层，一旦重名，一条后代选择器就能把图形整条抹掉且控制台零报错。
4. **动效尊重 `prefers-reduced-motion`**（components.css 已处理 Toast / 抽屉 / 骨架）。
5. **X/Grok 单色（全站无蓝无绿，2026-09-17 用户裁定）**：正向/活跃 = 白 `#E7E9EA`（`--lc-success`，原 #3FB950 绿已废）；语义红 `#E5484D` / 危险红 `#F85149` / 琥珀 `#D29922`。**绿色永久禁用，任何组件不许引入。**
