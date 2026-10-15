// ============================================================================
// components/frontshell/index.ts — 前台外壳布局方案（对外统一入口）
// ----------------------------------------------------------------------------
// 这一族件是「前台外壳」的固定组件与布局方案，供后续迭代直接复用，不要再在 App.tsx
// 里现攒一份（历史上「桌面一套 / 移动一套」两棵子树正是漂移的温床）。
//
// 布局方案（结构 → 组件 → 样式三个出处一一对应）：
//
//   FrontShell（src/App.tsx，只留外壳行布局与路由）
//   └─ <div style="height:100dvh; display:flex; flex-direction:row">
//        ├─ FrontSideNav ───────── 左栏：桌面常驻 / 移动档汉堡抽屉（同一节点两形态）
//        │    · 品牌区（logo + 租户身份）→ 语言行 → 工作台 Tab 导航 → 撑杆
//        │    · 底部信息带（自助入口 + 套餐积分 + 账号菜单）
//        │    · 移动档尾部：页脚折叠块（<details>）
//        └─ <div class="fs-main" style="flex:1; min-width:0"> 右主列
//             ├─ FrontMobileTopBar ── 移动档顶栏（汉堡 + 品牌簇）；桌面档整条退役
//             ├─ 余额耗尽横幅（depleted 时）
//             ├─ <div class="app-main"> 路由出口（自带走内滚）
//             └─ SiteFooter（桌面档常驻列尾 / 移动档已挪进左栏抽屉）
//
// 断点只有一档：≤640（useMobileShell）。641–900 仍走桌面档——左栏用 .fs-sidebar
// 把位置钉回 static，不吃 components.css 那段 ≤900 的「侧栏转抽屉」规则。
//
// 样式出处：src/styles/frontshell.css（唯一一份，入口包引入）+ 各件自有内联样式。
// 业务数据一律经 props 由外壳注入，本族件不直接调接口（可测性 + 订阅面收敛）。
// ============================================================================

export { default as FrontSideNav } from './FrontSideNav'
export { default as FrontMobileTopBar } from './FrontMobileTopBar'
export { useMobileShell, MOBILE_SHELL_QUERY } from './useMobileShell'
export type { FrontSideNavProps, FrontAccountMenuProps, FrontFooterProps, WorkbenchTab } from './FrontSideNav'
export type { FrontMobileTopBarProps } from './FrontMobileTopBar'
export type { MobileShell } from './useMobileShell'
