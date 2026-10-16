// ============================================================================
// components/FrontShell.layout.lock.test.ts — 前台外壳布局静态锁（★ 2026-09-24 〇-S/#7）
// 背景：用户拍板「套餐/余额/账号并入账号下拉；页脚回页脚位置」。本锁钉死改造后的结构口径：
//   ① 外壳改 height:100dvh + .app-main overflowY:auto（页脚常驻的前提，见 App.tsx 注释）；
//   ② ChatWindow 不再写死 calc(100vh - 39px)——页脚占位后该算法必然把页脚顶出首屏；
//   ③ 桌面档：左栏常驻 + 页脚挂外壳列尾；移动档：左栏化为汉堡抽屉、页脚收进抽屉折叠。
// ★ 2026-10-15 用户令（第五批）口径翻转（旧期望按新令改写，理由写在各条注释里，
//   不许只删断言——那会让下一个人照着旧口径改回去）：
//   · 〇-S 那条「顶栏不得有汉堡 / 不许有抽屉」→ 汉堡回归为**抽屉开关**（拉的是同一份左栏），
//     仍不许回潮成当年的 <Drawer> + menuOpen 那一套；
//   · 「移动两件套（标题栏 + 底部 TabBar）」→ 底部 TabBar 撤除，只剩标题栏 + 抽屉；
//   · 「桌面左栏与移动件按 isMobile 互斥渲染」→ 左栏改为**始终渲染**（桌面常驻 / 移动抽屉同一个节点），
//     只有页脚按 isMobile 分两处。
// ★ 2026-10-15 本批（用户令「把这次的 UI 做成固定组件和布局方案」）射程扩到四份源码：
//   结构搬进 src/components/frontshell/（App.tsx 只留外壳行布局与路由），样式搬进
//   src/styles/frontshell.css。故本锁一并钉住**两个守卫级事实**（判据级断言代替不了守卫级）：
//   · 样式必须有且只有一处、且入口包真的引了它（类名在、样式没引 ⇒ 界面照常渲染但整族无样式）；
//   · 懒加载件（AccountMenu/SiteFooter）必须以**组件**形式注入左栏，不许被静态 import
//     （静态 import 会把两份字节拉进前台入口包，体积闸门看不出「是谁拉的」）。
// 纯字符串静态断言（node 环境读源码），不渲染组件；行为面由 AccountMenu.selfnav.dom.test.tsx
// 与 e2e/mobile_uat.spec.ts 覆盖。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const ROOT = resolve(__dirname, '../..')
const read = (p: string) => readFileSync(resolve(ROOT, p), 'utf8')

// 负向锁必须先剥注释：本仓在这几份源码里留了「旧写法已退役」的说明注释
// （含 menuOpen、calc(100vh - 39px)、.lc-tabbar 等字样），不剥就会命中自己的历史记录
// （〇-M 注释批同款坑）。
// 剥法：/* */ 整段 + 行首或空白后的 // 到行尾；`https://` 这类冒号后紧跟的 // 不在射程（不误伤 URL）。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|\s)\/\/.*$/gm, '$1')
}

describe('前台外壳布局锁（#7 汉堡退役 → 2026-10-15 抽屉回归 + 固定组件化）', () => {
  const app = stripComments(read('src/App.tsx'))
  const nav = stripComments(read('src/components/frontshell/FrontSideNav.tsx'))
  const top = stripComments(read('src/components/frontshell/FrontMobileTopBar.tsx'))
  const shellHook = stripComments(read('src/components/frontshell/useMobileShell.ts'))
  const css = stripComments(read('src/styles/frontshell.css'))
  const main = stripComments(read('src/main.tsx'))

  it('汉堡只作左栏抽屉开关，不得回潮成 <Drawer> / menuOpen 旧形态', () => {
    // ★ 2026-10-15 第五批口径翻转：用户令「除会话外全部塞进汉堡」⇒ 顶栏汉堡钮必须回来。
    //   但它开的是**同一份左栏**（--drawer 档），不是当年的独立抽屉组件。
    expect(top).toContain('<Icon n="menu"')
    for (const [name, src] of [['App.tsx', app], ['FrontMobileTopBar.tsx', top], ['FrontSideNav.tsx', nav]] as const) {
      expect(src, `${name} 不许回潮 <Drawer>`).not.toMatch(/<Drawer\b/)
      expect(src, `${name} 不许回潮 menuOpen`).not.toMatch(/menuOpen/)
    }
  })

  it('左栏与移动顶栏是固定组件：App.tsx 只留外壳行布局，不再持两档形态', () => {
    // 正向：外壳按组件消费这两件（并注入懒加载件与业务数据）
    expect(app).toContain('<FrontSideNav')
    expect(app).toContain('<FrontMobileTopBar')
    expect(app).toContain('useMobileShell()')
    // 守卫级负向：左栏的 <aside> 与移动顶栏的 .lc-mob-topbar 不许再散落在外壳里
    expect(app, '左栏结构已搬进 FrontSideNav.tsx').not.toMatch(/<aside\b/)
    expect(app, '移动顶栏已搬进 FrontMobileTopBar.tsx').not.toContain('lc-mob-topbar')
    // 组件化后外壳里不该再有抽屉态/档位相关派生值（都收在 useMobileShell）
    expect(app).not.toContain('setDrawerOpen')
  })

  it('SiteFooter 分两处：桌面档常驻 .app-main 之后，移动档收进抽屉并折叠', () => {
    // 桌面档：列尾常驻（这个实例出现在 .app-main 之后）
    expect(app).toContain('{!isMobile && <SiteFooter />}')
    expect(app.indexOf('className="app-main"')).toBeLessThan(app.indexOf('{!isMobile && <SiteFooter />}'))
    // 移动档：抽屉内的折叠块，且落在左栏 <aside>…</aside> 之内
    expect(nav).toContain('<SiteFooter bare />')
    const asideIdx = nav.indexOf('<aside')
    // 取 JSX 用法而不是同名 CSS 规则（样式已迁出本文件，此处仍按 JSX 形态取，防将来又把样式写回来）
    const foldIdx = nav.indexOf('className="fs-foot-fold"')
    expect(asideIdx, '左栏 <aside> 不在').toBeGreaterThan(-1)
    expect(foldIdx, '移动档折叠页脚 fs-foot-fold 不在抽屉里').toBeGreaterThan(asideIdx)
    expect(foldIdx, '折叠页脚必须落在 </aside> 之前').toBeLessThan(nav.indexOf('</aside>'))
  })

  it('外壳锁 100dvh、.app-main 接管滚动', () => {
    expect(app).toContain("height: '100dvh'")
    expect(app).toContain("overflowY: 'auto'")
  })

  it('ChatWindow 不再按「视口-顶栏」自算高度', () => {
    const cw = stripComments(read('src/components/ChatWindow.tsx'))
    expect(cw).not.toContain('calc(100vh - 39px)')
    expect(cw).toContain('flex: 1')
  })

  // ★ 2026-10-10 批3（§七 #14）：S 档（≤640）移动壳接入——源码侧互斥锁。
  // ★ 2026-10-15 用户令（第五批）「参考元宝重做移动端」：移动壳＝极简标题栏（汉堡 + 品牌簇）＋
  //   汉堡抽屉（左栏 --drawer 档）；底部胶囊 TabBar 撤除，假状态栏 .lc-statusbar 与第二信息带
  //   .lc-mob-band 早在上一批退役。左栏不再与移动件互斥——它是**同一个节点**，桌面常驻、移动抽屉。
  it('移动壳＝标题栏 + 汉堡抽屉（无底部 TabBar）', () => {
    expect(top).toContain('lc-mob-topbar')
    // ★ 用户令：底部 TabBar 撤除（旧表达式 `mobActive === ...` 的高亮也随之删）
    expect(app, '底部 TabBar 已按用户令撤除').not.toContain('lc-tabbar')
    expect(css, '底部 TabBar 样式不该在这族里复活').not.toContain('lc-tabbar')
    expect(app, 'mobActive 派生值随之删').not.toContain('mobActive')
    // 桌面顶栏已退役（负向锁：谁把 app-header 装回来当场红）
    expect(app).not.toMatch(/<header className="app-header"/)
    // 假状态栏与第二信息带已退役（负向锁：谁装回来当场红）
    expect(app).not.toContain('lc-statusbar')
    expect(app).not.toContain('lc-mob-band')
    // 左栏**不再**按 isMobile 互斥：同一份左栏既当桌面常驻栏、又当移动抽屉
    expect(app).not.toMatch(/!isMobile && \(\s*<aside/)
    // 抽屉两档与遮罩都在左栏件里
    expect(nav).toContain('fs-sidebar--drawer')
    expect(nav).toContain('fs-sidebar--open')
    expect(nav).toContain('lc-shell-scrim')
    // 移动档仍有 isMobile 门禁：顶栏（外壳）、遮罩与折叠页脚（左栏件）
    expect(app).toMatch(/\{isMobile && <FrontMobileTopBar/)
    expect(nav).toMatch(/\{isMobile && \(/)
    // 判档必须走 matchMedia(≤640)，不许换成别的启发式（换档位先同步 §1.7 断点表）
    expect(shellHook).toContain("'(max-width: 640px)'")
    expect(shellHook).toContain('matchMedia')
  })

  // ★ 2026-10-15 元宝式左栏结构锁：同一份左栏，桌面常驻 / 移动抽屉都靠它。
  // ★ 2026-10-15 用户令（第二批）：通知铃铛从顶栏退役——通知改以「消息(N)」菜单项收进
  //   AccountMenu 下拉（组件在 AccountMenu.tsx，不在左栏源码里），故本锁把 `<Bell` 从正向
  //   清单换成负向清单（谁把独立铃铛装回左栏当场红），正向补 `fs-footrow`。
  // ★ 2026-10-15 用户令（第五批）：页脚（折叠块）也收进这一栏，正向补 `fs-foot-fold`。
  it('元宝式左栏：品牌/语言/功能菜单/自助入口/身份余额控件/折叠页脚都在左栏之内', () => {
    const start = nav.indexOf('<aside')
    expect(start, '左栏 <aside> 不在').toBeGreaterThan(-1)
    const end = nav.indexOf('</aside>')
    expect(end, '左栏 </aside> 不在').toBeGreaterThan(start)
    // 取「左栏开口 → 闭口」之间的片段，断言一切控件都落在左栏之内
    const sideBlock = nav.slice(start, end)
    for (const token of ['lc-sidebar__logo', 'fs-langrow', 'lc-side-item', 'lc-sidebar__foot',
                         'className="fs-tenant"', '<LangSelect', '<AccountMenu', 'fs-footrow', 'pkg-line-tag',
                         'fs-foot-fold']) {
      expect(sideBlock, `左栏缺少 ${token}`).toContain(token)
    }
    // 负向锁：独立通知铃铛已收进账号下拉，左栏里不许再出现它
    expect(sideBlock, '独立铃铛已收进账号菜单下拉，左栏不许再挂 <Bell').not.toContain('<Bell')
    // 负向锁（★ 2026-10-15 用户令）：新建翻译钮与对话历史列表已撤，谁装回来当场红
    expect(sideBlock, '「新建翻译」钮已撤').not.toContain('fs-new')
    expect(sideBlock, '「对话历史」列表已撤').not.toContain('fs-history')
    // 右主列必须包住路由出口（flex 1 吃掉剩余宽度）
    expect(app.indexOf('className="fs-main"')).toBeLessThan(app.indexOf('className="app-main"'))
  })

  // ★ 2026-10-15 守卫级补腿（本批抽组件的真正风险面）：
  //   ① 样式搬到 styles/frontshell.css 后，**忘了在 main.tsx 引入**是最坏的静默形态——
  //      类名都在、JSX 都对、组件测试全绿（jsdom 不管样式），界面照常渲染但整族无样式，
  //      只有真机/e2e 才看得见。同 AGENTS §一·5 那条「名字照抄、值没落地」。
  //   ② AccountMenu/SiteFooter 若被左栏**静态 import**，两件会被并进入口包——体积闸门会红，
  //      但红的读数（入口体积变大）指不到「是谁拉的」，所以在这里钉死注入形态。
  it('样式出处唯一且入口包真引了它（否则整族静默无样式）', () => {
    expect(css, '导出样式必须留在这族唯一的 CSS 文件里').toContain('.fs-nav')
    expect(css).toContain('.lc-sidebar.fs-sidebar--drawer')
    expect(css).toContain('.fs-foot-fold')
    // 外壳里不许再内联这族规则（谁搬回 App.tsx 的内联 <style> 当场红）
    expect(app, 'fs-* 样式已迁出外壳').not.toContain('.fs-nav{')
    expect(app, 'fs-* 样式已迁出外壳').not.toContain('.fs-sidebar--drawer{')
    // 入口包引了它，且晚于 components.css（同权重覆写靠源序第二道保险）
    expect(main, 'main.tsx 缺少 frontshell.css 引入 ⇒ 整族无样式').toContain("'./styles/frontshell.css'")
    expect(main.indexOf("'./ui/langcross/css/components.css'")).toBeLessThan(main.indexOf("'./styles/frontshell.css'"))
  })

  it('懒加载件以组件注入左栏，不许被静态 import（守住入口包体积）', () => {
    expect(nav, 'AccountMenu 必须是注入件').not.toMatch(/^import\s+AccountMenu\b/m)
    expect(nav, 'SiteFooter 必须是注入件').not.toMatch(/^import\s+SiteFooter\b/m)
    expect(nav, '不许直接 import AccountMenu 模块').not.toMatch(/from\s+['"][^'"]*AccountMenu['"]/)
    expect(nav, '不许直接 import SiteFooter 模块').not.toMatch(/from\s+['"][^'"]*SiteFooter['"]/)
    // 外壳侧仍是 React.lazy（懒加载两件不许退回静态 import）
    expect(app).toMatch(/lazy\(\(\) => import\(['"]\.\/components\/AccountMenu['"]\)\)/)
    expect(app).toMatch(/lazy\(\(\) => import\(['"]\.\/components\/SiteFooter['"]\)\)/)
  })

  // ★ 2026-10-16 本批「侧栏 tab 页布局重做」的机械锁。三个事实，缺一个都会静默退化：
  //   ① 四个自助页的页框**只由路由层一处提供**（FrontPage/.fs-page）——此前每个页面各写
  //      各的外垫（/my 贴边零外垫、MyBilling 内联 maxWidth:900），同一个卡片在四个页面上
  //      左边界各不相同；谁再在页面里开第二个框（内联 maxWidth/padding）当场红。
  //   ② .ss-grid 必须 align-items:start——默认 stretch 会把「快速入口」这种矮卡拉到
  //      同行最高那张（实测 101 → 315px，卡内空一大片），这是用户点名的缺陷本体。
  //   ③ .fs-page 的样式出处唯一（frontshell.css），别在页面里再内联一份。
  it('自助页页框只由路由层一处提供（FrontPage/.fs-page），且卡片不再被拉伸', () => {
    // ① 四条自助路由都套在 <FrontPage> 里
    for (const p of ['/billing', '/packages', '/my']) {
      const idx = app.indexOf(`path="${p}"`)
      expect(idx, `路由 ${p} 不在`).toBeGreaterThan(-1)
      const line = app.slice(idx, app.indexOf('\n', idx))
      expect(line, `${p} 必须包在 <FrontPage> 之内（页框唯一出处）`).toContain('<FrontPage>')
    }
    const inv = app.slice(app.indexOf('path="/invites"'))
    expect(inv.slice(0, inv.indexOf('\n')), '/invites 必须包在 <FrontPage> 之内').toContain('<FrontPage>')
    // ② .ss-grid 不再拉伸子项（用户点名的 /my 留白缺陷）
    expect(app, '.ss-grid 必须 align-items:start（否则矮卡被拉高留白）').toMatch(/\.ss-grid\{[^}]*align-items:start/)
    // auto-fit（空轨道塌掉，卡片摊满整行）＋窄屏安全下限（硬下限在 <380px 视口会横向溢出）
    expect(app, '.ss-grid 轨道下限要能收到 100%').toMatch(/grid-template-columns:repeat\(auto-fit,minmax\(min\(3[0-9]{2}px,100%\),1fr\)\)/)
    // ③ 页面自己不许再开框：MyBilling 的页级内联容器已撤，卡间距走 .fs-page 的 gap
    const mb = stripComments(read('src/components/MyBilling.tsx'))
    expect(mb, 'MyBilling 不许再自带页级容器（会双外垫）').not.toMatch(/maxWidth:\s*900/)
    expect(mb, '卡间距已交 .fs-page 的 gap，.mb-card 不许再带 margin-bottom').not.toMatch(/\.mb-card\{[^}]*margin-bottom/)
    // 样式出处唯一：.fs-page 只在 frontshell.css 里定义
    expect(css).toContain('.fs-page')
    expect(app, '.fs-page 样式只许在 frontshell.css 一处').not.toContain('.fs-page{')
    expect(mb).not.toContain('.fs-page{')
  })
})

// ★ F-48（〇-U 批 I-5 2026-09-26）：窄屏余额徽标的**静态机制锁**。
// 旧 mobile.css 写 `.app-header .pkg-line-tag { display: none }`——手机上把「还剩多少积分」
// 整块抹掉（要查余额得翻进「套餐/账单」二级页）。运行时真值由 e2e/mobile_uat.spec.ts
// 「余额徽标窄屏仍可见」承担（那条要起全站服务）；本锁的价值是**不依赖服务也能红**：
// 谁把 display:none 加回来（或加在别的选择器上），npm test 立刻拦，不用等发版闸门跑 e2e。
// 等值锁口径（AGENTS §一·5 〇-L）：既要求新四项在位，也负向清掉旧写法，二者缺一即红。
describe('窄屏余额徽标锁（★ F-48：余额不得在手机上隐身）', () => {
  const mobile = stripComments(read('src/styles/mobile.css'))
  // 只看 .pkg-line-tag 自己的声明块（注释已剥掉，故文件里那句「旧写法 display:none 已退役」
  // 的说明注释不会自伤命中——〇-M 注释批同款坑）。
  const blocks = [...mobile.matchAll(/\.pkg-line-tag[^{]*\{([^}]*)\}/g)].map((m) => m[1].replace(/\s+/g, ''))

  it('规则仍在且块内不得出现 display:none（旧「窄屏隐藏」负向清零）', () => {
    expect(blocks.length, 'mobile.css 里必须仍有 .pkg-line-tag 规则（被整段删掉＝徽标失去截断保护）').toBeGreaterThan(0)
    for (const b of blocks) {
      expect(b, '★ F-48 复发：余额徽标又被 display:none 藏了').not.toContain('display:none')
    }
  })

  it('徽标单行省略号五件套在位（就在 .pkg-line-tag 块内，不是别的规则蹭到）', () => {
    // 五项缺一都有实际后果：min-width:0 少了 flex 项不收缩（撑破 390px，被 e2e 溢出锁红）、
    // overflow+ellipsis 少了不截断、nowrap 少了换行撑高页眉
    for (const decl of ['max-width:100%', 'min-width:0', 'overflow:hidden', 'text-overflow:ellipsis', 'white-space:nowrap']) {
      expect(blocks.some((b) => b.includes(decl)), `徽标窄屏规则缺少 ${decl}`).toBe(true)
    }
  })
})
