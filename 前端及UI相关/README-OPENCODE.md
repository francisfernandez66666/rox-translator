# LangCross（能言）UI 交付包 · 给 opencode 的交接说明

> 你拿到的是一个**设计已完结**的项目的全部资产。你的任务是把这些设计实现为可运行的 React 应用并部署。设计系统、组件库、全套切图、可运行 HTML 参考都在本包内，**请严格按规范实现，不要发明新的视觉决定**。

## 0. 这是什么产品

**LangCross（能言）**：AI 驱动的企业级翻译工作台。核心能力：即时翻译（带术语校准）、翻译工单、对照编辑、知识库/术语库、企业管理后台（审批/组织/计费/成本对账等）。

角色分两端：
- **用户端**：个人/企业员工，用即时翻译、提工单、管知识库
- **管理后台**：企业管理员或平台运营，管审批、成员、租户、计费

## 1. 包结构（先读这个顺序）

```
langcross-handoff/
├── README-OPENCODE.md     ← 本文件（任务说明 + 硬规则）
├── react/                 ← React 组件库（直接可用，零运行时依赖，TS 直出）
│   ├── css/tokens.css     ← 设计令牌唯一来源（--lc-* 变量）
│   ├── css/components.css ← 组件样式（.lc-* 前缀）
│   ├── src/               ← 全部组件 TSX + icons（见 react/README.md 的 API 表）
│   └── demo.html          ← 组件静态总览（打开对照用）
├── html/                  ← 可运行 HTML 参考（打开即看，交互与动效的"标准答案"）
│   ├── index.html         ← 本包导航页（演示 + 全部切图索引）
│   ├── hero-stream.html   ← 官网首屏翻译动效（响应式参考实现）
│   ├── mobile-stream.html ← 纯移动端自适应 hero
│   ├── demo-mobile.html / demo-mobile-motion.html
│   └── demo-register-ai-motion.html ← AI 接管注册完整交互（见 §5）
└── shots/                 ← 全套设计切图（实现页面的视觉依据）
    ├── web/               ← 桌面 1440 × 24 张（认证 4 + 营销 2 + 用户端 6 + 后台 11 + 便签 1）
    ├── mobile/            ← 移动 390@2x × 11（用户端 7 + 后台 4）
    ├── tablet/            ← 平板 834@2x × 2（双栏重排示例）
    └── en/                ← 英文版 × 6（移动 4 + Web 2）
```

**建议实现顺序**：tokens + 组件 → 认证 → 用户端 → 后台 → 营销页 → i18n → 注册 AI 流程 → 动效。

## 2. 设计系统硬规则（违反=返工）

### 2.1 颜色（全站 X/Grok 单色）

- **底色纯黑 #000**；面板 #0E1014 / #16181C / #0A0B0D / #050607
- 文字：#E7E9EA（主）/ #9AA0AA / #71767B / #536471 / 禁用 #4A4F55
- **全站无蓝无绿**（任何位置不许出现绿色/蓝色，包括成功态、活跃态、图表）：
  - 正向/活跃/成功 = **白 #E7E9EA**（`--lc-success` 就是白）
  - 语义红 #E5484D / 功能红 #F85149 / 琥珀警示 #D29922 是仅有的三个非灰强调色
- 主按钮 = **白底黑字**；次按钮 = 描边无底；危险按钮 = 红底白字

### 2.2 描边与分隔线（已裁定，勿改）

- **所有描边框（卡片/输入框/按钮/弹窗）统一 1.2px**，色阶按对 #000 的对比度：
  - `--lc-border-card` #3A404C（卡片 2.5:1）/ `--lc-border-card-dim` #2A2F3A（骨架 1.9:1）
  - `--lc-border-faint` #464C58（常规分隔与轨道）/ `--lc-border-input` #5A6270（输入边）
  - `--lc-border-pill` #424956（胶囊/次按钮）/ `--lc-border-done` #6E7683（完成态）/ `--lc-border-strong` #8B939F（强强调）
- **单向分隔线（border-top/bottom/left/right 单边）保持 1px**——画布上是独立的 1px 矩形，与描边框不同层级
- 图标描边 1.6–1.9（16×16 网格，并排图标按同行最重者对齐）
- 面板顶缘加 `inset 0 1px 0 rgba(255,255,255,.055)` 高光（`--lc-panel-highlight`）

### 2.3 字体与几何

- 中文 Noto Sans SC（注意 SemiBold **无空格**）、拉丁 Inter、等宽 JetBrains Mono
- 主按钮 radius 8（条状）/ 999（胶囊）；卡片 14；输入框 10；弹窗 14
- 内部页顶栏 56 + 1px 分隔线；键值行「标签定宽 72 + 值左对齐」（不要 SPACE_BETWEEN 右对齐）；表格操作列左对齐

### 2.4 动效（纯黑体系十条原则，详见 html/ 三个动效 demo）

1. 抽象进度必须带检查点名字（"术语检索/机器翻译/术语校准"，不能只有进度条）
2. 黑底别用几何形状做光——让内容自己反应（`filter: brightness` 只抬中间调）
3. 峰值是一个时刻不是一段，验收 = 能截出一张明显最亮的静帧
4. 峰值前先整块压暗（brightness .84）做落差；收放不对称（沉慢放快）
5. 页面换场：旧页退 120ms（-6px 淡出）/ 新页进 200ms（+8px 升入）
6. 进场节拍：导航即时在位，内容 60→290ms 逐级现身，只动 opacity/transform
7. 同组重复元素等速现身（70ms 步进），三次以内不逐次收紧
8. 动效尊重 `prefers-reduced-motion`；顺序动画用 JS await 编排，不用 animation-delay
9. 循环用 generation 计数器中止上一轮；reset 清干净所有临时 class
10. 打字机逐字文本必须用 `visibility:hidden` 同文本占位撑高（ghost 方案），否则窄屏换行时面板跳高

### 2.5 工程纪律（踩过的坑）

- **类名不许重名**：大容器状态类与小组件类同名会触发后代选择器灾难（曾致图标消失且零报错）
- 长文本容器必须显式 `width:100%/fill`，否则单行溢出被裁
- 量布局高度用 `offsetHeight`，`getBoundingClientRect` 会被 transform 骗 ±2px

## 3. 页面清单（实现范围）

### 桌面 1440（shots/web/，中文名即页面语义）

| 切图 | 页面 | 要点 |
|---|---|---|
| 01-login | 登录页 | 卡片居中，主按钮白底黑字 |
| 02/03-register-personal/org | 注册页·个人/企业 | 双 tab 切换字段组；企业侧含组织中文名/英文名、所属行业、品牌中英文名（选填）；协议勾选 |
| 04-forgot-password | 忘记密码 | |
| 05-pricing | 详细定价页 | 套餐卡 + FAQ |
| 06-marketing-home | 营销首页 | 纯黑单页：导航/Hero/方案 bento/价格/FAQ+CTA/页脚 |
| 07-translate | 用户端·即时翻译 | 原文→检查点进度→译文+术语校准行（对照 html/hero-stream.html 动效） |
| 08-tickets | 用户端·翻译工单 | 列表 + 筛选 chips |
| 09-editor | 用户端·对照编辑 | 双栏对照 |
| 10/11/12 | 知识库上传/改密弹窗/用户菜单 | Dialog 440 圆角 14 |
| 13–23 | 管理后台 11 屏 | 总览/反馈审批/个人中心/知识库/组织与成员/外部调用/计费套餐/租户管理/成本对账/系统运维/AI 助手；共享侧栏 238 + 顶栏布局 |
| 24-note-legal-wall | 便签·协议页登录墙 | 边缘场景参考 |

### 移动 390（shots/mobile/）与平板 834（shots/tablet/）

- 移动结构约定：状态栏 44 + 顶栏 52 + 内容栈（16 边距 gap 12）+ 底部浮动胶囊 TabBar 62 高 r36（活跃 tab 实心底黑字）；后台移动页无 TabBar、用 SVG 汉堡
- 平板 834 是响应式示例：即时翻译双栏重排 / 后台保留侧栏
- 响应式断点（react/src/AdminShell.tsx 已实现）：≤900 侧栏转抽屉；≤620 页头纵排/表格横滚/浮层 calc(100vw-32px)

### 英文版（shots/en/）

- i18n 建议：React Context + JSON 词典（zh/en），切语言不改布局；移动 4 屏 + Web 2 屏作为翻译对照基准
- 拉丁文案用 `--lc-font-latin`（Inter）

## 4. React 组件库使用

```tsx
import { Button, Input, Select, Checkbox, Switch, StatusPill, Badge, KeyValue,
         InlineBanner, ToastProvider, useToast, Dialog, Drawer, ContextMenu,
         Skeleton, EmptyState, AdminShell, PageHeader, DataTable, StatCard,
         Chip, Tabs, AuthCard, Mobile, Icon } from "./src";
```

- 样式：`import "./css/tokens.css"; import "./css/components.css";`（全局一次）
- 按钮变体：`--primary`（白底黑字）/ `--secondary`（描边）/ `--danger`（红底）/ `--block`（通栏）/ `--sm`
- 完整 API 表与每个组件的视觉规范见 **react/README.md**
- 组件是 TS 直出（.tsx 源码直接给，无构建产物），Vite/CRA/Next 均可直接引用

## 5. 注册页「AI 接管引导」流程规格（新功能，html/demo-register-ai-motion.html 是交互标准答案）

用户点「免费注册」进入传统注册页（完整复杂表单：个人 5 项 / 企业 9 项，双 tab + 管理员/员工角色）→ 表单卡**闪烁三次**（brightness 提亮，间隔渐短）→ 整页 120ms 退出，AI 助理面板接管 → 引导式问答：

1. 账号类型：个人 / 企业
2. 企业 → 企业身份：管理员（新建企业）/ 员工（加入企业）
3. 管理员 → 所属行业（4 选 1，行业词库）
4. 账号信息表单（字段按分支裁剪）：用户名**自动带入之前已填值**（标「已带入，不用再填」）、密码（掩码）、邮箱、邮箱验证码（OTP 6 格）、管理员加组织中文名+英文名、员工加组织编码
5. 管理员提交后 AI 追加品牌固定译名预配（chips：华创物流 / Huachuang Logistics）
6. 收尾：检查点三连（管理员=创建账号与组织/配置行业词库/发送邮件并抄送运营；员工=通知管理员审批）→ 注册完成摘要卡

支持「← 上一步」返回（进度行右侧，第一步隐藏，完成后禁用）。生产实现建议：问答状态机 + 服务端会话，AI 侧只做文案流，字段校验走前端表单。

## 6. 建议技术栈与工程化

- **Vite + React 18 + TypeScript + React Router**（或 Next.js App Router，若要 SSR/SEO 的营销页单独路由）
- i18n：`react-i18next` 或轻量 Context + 两份 JSON 词典（词条以切图文案为准）
- 状态：组件库不绑定任何状态库；页面数据用 React Query/Zustand 自行决定
- 图标：`react/src/icons.tsx` 已有全套 16×16 SVG（stroke 1.6–1.9），不要引第三方图标库混风格
- 部署：任意静态托管（Vercel/Netlify/自有 Nginx）；营销页与工作台可分域或同域分路由

## 7. 验收清单

- [ ] 全站无蓝无绿（grep hex：不允许 #3B82F6/#10B981/#3FB950 等出现）
- [ ] 描边框 1.2px、单向分隔线 1px、图标 1.6–1.9
- [ ] 活跃/成功态全部为白（TabBar 活跃、checkbox 选中、状态点）
- [ ] 三档断点：1440 桌面 / 390 移动 / 834 平板；≤900 侧栏转抽屉、≤620 表格横滚
- [ ] 中英双语切换后布局不破（对照 shots/en/）
- [ ] 动效遵循 §2.4（对照 html/ 三个 demo 的节拍）
- [ ] 注册 AI 流程可交互、可返回上一步、字段按分支裁剪正确
