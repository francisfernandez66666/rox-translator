// ============================================================================
// readability.test.ts — ★ UI 真值闸门（2026-09-22 全站还原批重写）
// ----------------------------------------------------------------------------
// 历史口径已废止：本文件原先钉的是 #35（字号整档 +1px）与 #67/#68（顶栏字阶提档、
// 次级灰/描边提亮）的「局部提亮」结果，并在 theme.css §十/§十一 维护一张 `html xxx`
// 页面级覆写表去压住组件库。2026-09-22 用户判定该口径整体偏离交付 UI（「偏蓝、显脏、
// 粗细间距都不对」），提亮层与覆写表已全部拆除，字号/字重/颜色一律回到
// UI-ANNOTATIONS + langcross-handoff 交付包真值。闸门随之从「只准更亮/更大」
// 改成「必须等于真值」——旧的单向锁会阻止还原，留着它反而会持续把配色往偏蓝方向推。
//
// 八条断言：
//  A) 令牌真值等值锁：tokens.css 的灰阶/描边/语义色与 theme.css 的 --npz-* 必须逐字等于
//     交付值（改一个字符即红，防止「顺手提亮一档」再次发生）；
//  B) 覆写层禁复活：theme.css / mobile.css 里不得再出现带 font-size 的 `html xxx` 覆写规则
//     （§十/§十一 的形态特征），负向锁只认代码形态并先剥注释，避免命中「不得复活」说明注释；
//  C) 关键几何与字阶等值：顶栏 38 高、品牌 14/700、工作台 Tab 13 胶囊（§2.2 真值）；
//  D) 提亮/蓝调遗留字面值清零：历次提亮与浅色主题遗留的 38 个十六进制值全站禁再出现；
//  E) 登录后界面不得写死次级灰（走 var(--lc-text-*)），营销门面页按自身画布口径豁免；
//  F) 描边字面值不得暗于 --lc-border-faint（令牌是 3.2:1 的最弱档，写死更暗即架空）；
//  G) 白色填充档：主按钮/主 CTA/反白件（徽标、用户气泡、FAB）必须纯白 #FFFFFF（--lc-fill-white），
//     不得用文字档 #E7E9EA 做整块填充——那正是「白色显脏/偏蓝」的根因（截图逐像素取证）；
//  H) 扩展插件面（../extension/popup.html + content.css）按同一套 §1.1 真值——它既不进 vite 产物
//     也不是后端直出 HTML，是第四类「两套闸门都扫不到」的渲染盲区。
//
// 读源文件而非渲染 DOM：jsdom 不加载外链 CSS，源码级断言才是稳定闸门；
// 运行时内联样式/换肤造成的回退由 e2e/pixel_uat.spec.ts 的 P2b（getComputedStyle 实测）互补。
// ============================================================================
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// ROOT 指向 frontend-react 包根：本文件用相对包根的硬路径读源文件，不做运行时拼接。
const ROOT = fileURLToPath(new URL('../../', import.meta.url))
const read = (p: string) => readFileSync(ROOT + p, 'utf-8')
// 源文件在模块顶层就读进来：路径一旦改名/删除，import 阶段就直接抛错，
// 让「闸门本身失效」也是红灯，而不是静默地什么都不断（本文件最怕假绿）。
const KIT_TOKENS = read('src/ui/langcross/css/tokens.css')
const KIT_COMPONENTS = read('src/ui/langcross/css/components.css')
const THEME_CSS = read('src/styles/theme.css')
const MOBILE_CSS = read('src/styles/mobile.css')
const APP_TSX = read('src/App.tsx')

// 剥注释：块注释（含 JSX 的 {/* */}）、行首 //、以及「空白 + //」行尾注释。
// 为什么必须剥：本仓大量「旧值 → 真值」的说明注释里带着 #878D95、12px 这类字样，
// 不剥掉负向锁会命中注释自己（历史事故：静态负向锁误吃「不得复活」说明文字）。
// 行尾注释要求 // 前有空白，因此 url(https://…) 这类协议头不会被误剥。
function stripComments(src: string) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .map((l) => (/^\s*(\/\/|\{\/)/.test(l) ? '' : l.replace(/\s\/\/.*$/, '')))
    .join('\n')
}

// WCAG 相对亮度与对比度：口径与 e2e/pixel_uat.spec.ts 的 contrastOnCard 完全一致
// （同一 sRGB 线性化公式），两处一致才不会出现「单测绿、e2e 红」的调色盲区。
function contrast(fg: string, bg: string) {
  const lum = (hex: string) => {
    const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
      .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]
  }
  const a = lum(fg), b = lum(bg)
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}
// 取 `--令牌: #rrggbb` 的字面值（只认 6 位十六进制；写成 var() 或 #abc 视作未定义）。
const token = (css: string, name: string) => {
  const m = css.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`))
  return m ? m[1].toUpperCase() : ''
}
// 取某条规则声明块里的第一个 font-size（用于「选择器 → 档位」等值断言）。
function fontSizeOf(css: string, selector: string) {
  const body = stripComments(css)
  const re = /([^{}]+)\{([^{}]*)\}/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body))) {
    const sel = m[1].trim().replace(/\s+/g, ' ')
    if (sel !== selector) continue
    const v = m[2].match(/font-size:\s*([0-9.]+)px/)
    if (v) return Number(v[1])
  }
  return undefined
}

// ---- A) 令牌真值表：值取自 UI-ANNOTATIONS §1.1（面/文字/描边七档）与交付包 tokens.css ----
const TRUTH_TOKENS: [string, string][] = [
  ['--lc-bg', '#000000'],
  ['--lc-deep', '#050607'],
  ['--lc-inset', '#0A0B0D'],
  ['--lc-panel', '#0E1014'],
  ['--lc-raised', '#16181C'],
  ['--lc-text', '#E7E9EA'],
  ['--lc-text-2', '#9AA0AA'],
  ['--lc-text-3', '#71767B'],
  ['--lc-text-4', '#536471'],
  ['--lc-text-5', '#8A9099'],
  ['--lc-border-strong', '#8B939F'],
  ['--lc-border-done', '#6E7683'],
  ['--lc-border-input', '#5A6270'],
  ['--lc-border-faint', '#464C58'],
  ['--lc-border-pill', '#424956'],
  ['--lc-border-card', '#3A404C'],
  ['--lc-border-card-dim', '#2A2F3A'],
  ['--lc-danger', '#E5484D'],
  ['--lc-warn', '#D29922'],
  ['--lc-success', '#E7E9EA'], // 正向=白（第 11 轮废止绿），交付包口径
]
describe('A 令牌真值等值锁（UI-ANNOTATIONS §1.1 / 交付包 tokens.css）', () => {
  for (const [name, want] of TRUTH_TOKENS) {
    it(`${name} = ${want}`, () => {
      const got = token(KIT_TOKENS, name)
      expect(got, 'tokens.css 里该令牌缺失或不是 6 位字面量').toBe(want)
    })
  }
  // 页面级令牌（theme.css 自有一层，供未走组件库的历史页面引用）同样钉真值：
  // 这里的 --npz-text-2 / --npz-line 曾是 #35/#68 提亮的落点。
  it('theme.css --npz-text-2 / --npz-line 回到真值档', () => {
    expect(token(THEME_CSS, '--npz-text-2')).toBe('#9AA0AA')
    expect(token(THEME_CSS, '--npz-line')).toBe('#464C58')
  })
  // 语义绿/蓝永久废止：交付包把「正向」收敛为白，任何 #3FB950/#2f47f5 复活都会立刻红灯。
  it('组件库与页面层不复活语义绿/语义蓝', () => {
    const offenders: string[] = []
    for (const [file, src] of entriesCss()) {
      stripComments(src).split('\n').forEach((l, i) => {
        if (/#(3FB950|2f47f5|1f6feb|0969da|58a6ff)/i.test(l)) offenders.push(`${file}:${i + 1} ${l.trim().slice(0, 60)}`)
      })
    }
    expect(offenders, '语义绿/蓝已废止：\n' + offenders.join('\n')).toEqual([])
  })
})

// CSS 侧待扫文件（令牌定义处也在内，因为等值锁要覆盖整个样式层）
function entriesCss(): [string, string][] {
  return [
    ['src/ui/langcross/css/tokens.css', KIT_TOKENS],
    ['src/ui/langcross/css/components.css', KIT_COMPONENTS],
    ['src/styles/theme.css', THEME_CSS],
    ['src/styles/mobile.css', MOBILE_CSS],
  ]
}

// ---- B) 覆写层禁复活：只认「带 font-size 的 `html xxx` 规则」这一代码形态 ----
describe('B 页面级字号覆写层（旧 §十/§十一）不得复活', () => {
  for (const [file, src] of entriesCss()) {
    it(`${file} 无 html 前缀的 font-size 覆写规则`, () => {
      const body = stripComments(src)
      const hits: string[] = []
      const re = /([^{}]+)\{([^{}]*)\}/g
      let m: RegExpExecArray | null
      while ((m = re.exec(body))) {
        const sel = m[1].trim().replace(/\s+/g, ' ')
        if (!/(^|,)\s*html[\s.]/.test(sel)) continue
        for (const v of m[2].matchAll(/font-size:\s*[0-9.]+px/g)) hits.push(`${sel} { ${v[0]} }`)
      }
      expect(hits, `字阶必须由组件库/页面自身决定，不得用 html 覆写层抬高：\n${hits.join('\n')}`).toEqual([])
    })
  }
})

// ---- C) 关键几何与字阶（§2.2 用户工作台骨架：顶栏 38、品牌 14、导航 13px 胶囊）----
describe('C 工作台顶栏按 §2.2 真值', () => {
  it('.app-header 高 38px 且 --lc-workbench-topbar-h 同档', () => {
    const m = stripComments(THEME_CSS).match(/\.app-header\s*\{[^}]*height:\s*(\d+)px/)
    expect(m, '.app-header 的 height 声明丢失或形态变了').toBeTruthy()
    expect(Number(m![1]), '§2.2 顶栏高 38').toBe(38)
    const t = KIT_TOKENS.match(/--lc-workbench-topbar-h:\s*(\d+)px/)
    expect(t, 'tokens.css 缺 --lc-workbench-topbar-h 的 px 字面值').toBeTruthy()
    expect(Number(t![1]), '顶栏高度令牌须与 §2.2 同档').toBe(38)
  })
  it('.brand = 14px / Bold 700（§2.2「能言」14）', () => {
    expect(fontSizeOf(THEME_CSS, '.brand')).toBe(14)
    const w = stripComments(THEME_CSS).match(/\.brand\s*\{[^}]*font-weight:\s*(\d+)/)
    expect(Number(w![1]), '字重 Vocabulary 只有 400/500/600/700 四档，品牌位为 Bold 700').toBe(700)
  })
  it('App.tsx 内联 .app-tab = 13px 胶囊（§2.2 主导航 13px 胶囊）', () => {
    // .app-tab 的规则在 App.tsx 的 <style> 字符串里，不在 theme.css，只能按源码正则钉。
    // 「形态变了就先红」是刻意的：宁可红着等人同步锁，也不要正则静默失配变假绿。
    const m = APP_TSX.match(/\.app-tab\{[^}]*?border-radius:\s*(\d+)px[^}]*?font-size:\s*([0-9.]+)px/)
    expect(m, 'App.tsx 内联 .app-tab 规则形态变了，请同步本锁').toBeTruthy()
    expect(Number(m![2]), `Tab 字号 ${m![2]}px ≠ §2.2 真值 13px`).toBe(13)
    expect(Number(m![1]), '胶囊档必须是全圆角 999，8px 方角属旧覆写层残留').toBe(999)
  })
})

// ---- D) 提亮/浅色遗留字面值清零（历次批次自造的蓝调灰与浅色底，全站不得再出现）----
// 清单来源：09-18 提亮、#35 字号批、#67/#68 提亮与浅色主题遗留三批的实测值。
const BANNED_HEX = [
  '666E7C', 'B0B6C2', 'A6ADBA', '9AA2AF', '878D95', '7A828E', '575F6C', '565B63', 'E98286',
  '1F2228', '202329', '191D24', '17171C', '26282E', 'B6BBC3', '5F6B7A', '6A717A', '889', 'CDD',
  'fdecea', 'c5221f', 'fff6e0', 'b26a00', 'fff1f0', 'b45309', 'f0c674', 'c66900', 'ad6800',
  'e8f0fe', 'f5f6f8', 'e34d59', 'd45656', '546470', 'c0392b', '666C74', '5F656D', '141B2D', '525C70',
]
describe('D 提亮/蓝调遗留字面值清零', () => {
  const offenders: string[] = []
  for (const f of walkSrc('src')) {
    stripComments(read(f)).split('\n').forEach((l, i) => {
      for (const m of l.matchAll(/#([0-9a-fA-F]{3,8})\b/g)) {
        if (BANNED_HEX.includes(m[1].toUpperCase())) offenders.push(`${f}:${i + 1} #${m[1]} ${l.trim().slice(0, 60)}`)
      }
      if (/rgba\(\s*10\s*,\s*16\s*,\s*40/i.test(l)) offenders.push(`${f}:${i + 1} 蓝调遮罩 rgba(10,16,40) ${l.trim().slice(0, 40)}`)
    })
  }
  it('全站源码（含注释外的代码行）零命中', () => {
    expect(offenders, '这些值已被真值/令牌取代：\n' + offenders.join('\n')).toEqual([])
    // 断言扫描确实覆盖到了量级：文件数异常下降说明 walkSrc 失效（闸门空转）
    expect(walkSrc('src').length).toBeGreaterThan(120)
  })
})

// 未登录的营销门面页按自身画布口径（UI-ANNOTATIONS §3.1-01~06 与 hero-stream 实测值），
// 不纳入「登录后必须走语义令牌」这条锁；组件库冻结区是令牌定义处，同理豁免。
const EXEMPT = /langcross|\.test\.|\/(Landing|PricingPage|LeadForm|HeroDemo|Login|SiteFooter)\.tsx/

// 枚举 src 下的 ts/tsx（含组件库，供扫描用；走源码而不跑 shell）
function walkSrc(dir: string, acc: string[] = []): string[] {
  for (const e of readdirSync(ROOT + dir)) {
    const rel = `${dir}/${e}`
    if (statSync(ROOT + rel).isDirectory()) { walkSrc(rel, acc); continue }
    if (/\.(tsx|ts|css)$/.test(rel) && !/\.test\.|\/locales\//.test(rel)) acc.push(rel)
  }
  return acc
}

// ---- E) 登录后界面的次级灰必须走语义令牌 ----
describe('E 登录后界面禁写死次级灰', () => {
  const offenders: string[] = []
  for (const f of walkSrc('src')) {
    if (EXEMPT.test(f) || f.endsWith('.css')) continue
    stripComments(read(f)).split('\n').forEach((l, i) => {
      if (/color:\s*['"]?#(9AA0AA|71767B|8A9099)/i.test(l)) offenders.push(`${f}:${i + 1} ${l.trim().slice(0, 60)}`)
    })
  }
  it('次级灰一律 var(--lc-text-*)，改一处全站跟随', () => {
    expect(offenders, '请改为语义令牌：\n' + offenders.join('\n')).toEqual([])
  })
})

// ---- F) 描边字面值不得暗于最弱令牌 ----
describe('F 描边字面值不得架空令牌', () => {
  // 底线动态取：以 --lc-border-faint 当前对比度为准（还原后 464C58 对 #000 约 3.2:1），
  // 而不是再写死一个数。将来令牌调档，这条锁跟着走，不会出现「令牌违规没人管、
  // tsx 里合法值反倒全红」的连锁假红。-0.001 抵浮点误差。
  const floor = contrast(token(KIT_TOKENS, '--lc-border-faint'), '#000000')
  const offenders: string[] = []
  for (const f of walkSrc('src')) {
    if (EXEMPT.test(f) || f.endsWith('.css')) continue
    stripComments(read(f)).split('\n').forEach((l, i) => {
      // [:=] 两种写法都扫：前者是 CSS 字符串里的 border:，后者是 React 内联对象
      // style={{ borderBottom: '1px solid …' }}，只扫一类会漏掉另一大口。
      for (const m of l.matchAll(/border[a-z-]*\s*[:=]\s*['"]?([^'"{};]*)#([0-9a-fA-F]{6})/gi)) {
        if (m[1].includes('var(')) continue   // var(--token, #兜底) 的兜底值不算写死
        if (contrast('#' + m[2], '#000000') >= Math.min(3.2, floor) - 0.001) continue
        offenders.push(`${f}:${i + 1}  ${m[0].trim().slice(0, 70)}`)
      }
    })
  }
  it('描边字面值不得暗于 --lc-border-faint', () => {
    expect(offenders, '请改为描边令牌：\n' + offenders.join('\n')).toEqual([])
  })
})

// ---- G) 白色填充档：实心白件必须纯白（2026-09-22 像素取证批新增）----
// 取证链：① 交付包 react/css/components.css `.lc-btn--primary{background:#FFFFFF}`；
// ② 零偏移截图 05-pricing / 06-marketing-home 逐像素直方图 —— 卡内「免费注册」按钮、
//    「注册即送 / 即购即刻到账」徽标、底部 CTA 按钮的填充全部实测 #FFFFFF，
//    而 #E7E9EA 在这两张图里只出现在文字行；③ UI-ANNOTATIONS §1.1 把 #E7E9EA 定义为
//    「主文字 / 正向活跃态」，从未授权它做整块填充。
// 本锁防的正是用户判定「白色显脏、偏蓝」的根因：把主按钮写成 var(--lc-text-1)。
// 反过来说，进度条 / 光标 / Tab 活跃胶囊这类「正向活跃态」仍属 --lc-success 档，不在本锁射程。

// 选择器文本规范化：压平空白，并剥掉 CSS-in-JS 模板串残留的反引号/引号
// （AiAssist 的样式写在 `css\`…\` 里，首个规则的选择器会带上开头的反引号）。
const normSel = (s: string) => s.replace(/[`"{}]/g, ' ').replace(/\s+/g, ' ').trim()

// 取某选择器（全等匹配，规范化空白后）所有声明块里的 background 值。
// 同名选择器会在媒体查询里再次出现，因此收集全部、任一命中纯白即通过。
function bgsOf(src: string, selector: string): string[] {
  const body = stripComments(src)
  const out: string[] = []
  const re = /([^{}]+)\{([^{}]*)\}/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body))) {
    if (normSel(m[1]) !== selector) continue
    const v = m[2].match(/background(?:-color)?\s*:\s*([^;]+)/)
    if (v) out.push(v[1].trim())
  }
  return out
}
// 纯白判据：#FFFFFF 字面量，或 var(--lc-fill-white)（含带兜底值的写法）。
const isPureWhite = (v: string) => /^(#FFFFFF|var\(--lc-fill-white(,[^)]*)?\))$/i.test(v.replace(/\s+/g, ''))

// 逐点等值表：每一点都由截图取证支撑，新增白底实心件时同步往这里加一行。
const WHITE_FILL_SITES: [string, string][] = [
  ['src/ui/langcross/css/components.css', '.lc-btn--primary'],
  ['src/components/Landing.tsx', '.lc-mkt .lc-mkt-btn--pri'],
  ['src/components/Landing.tsx', '.lc-plan--pro'],
  ['src/components/Landing.tsx', '.lc-cta'],
  ['src/components/PricingPage.tsx', '.lc-prc-badge'],
  ['src/components/AiAssist.tsx', '.na-fab'],
  ['src/components/AiAssist.tsx', '.na-send'],
  ['src/components/AiAssist.tsx', '.na-row.me .na-bubble'],
  ['src/styles/theme.css', '.avatar-ai'],
  ['src/styles/theme.css', '.progress-lang'],
  ['src/styles/theme.css', '.icon-docx'],
]
// 负向锁的选择器网：只圈「按钮 / 主投 / CTA / 发送 / FAB / 徽标 / 气泡」这一类实心白件，
// 不碰进度条与活跃胶囊（它们合法地留在 #E7E9EA 档）。
const WHITE_FILL_SEL = /(^|[.\s,:])[a-z0-9_-]*(btn|cta|send|fab|badge|bubble|--pri|--primary)/i

describe('G 白色填充档（主按钮/主 CTA/反白件 = 纯白 #FFFFFF）', () => {
  it('--lc-fill-white 令牌存在且等于 #FFFFFF', () => {
    expect(token(KIT_TOKENS, '--lc-fill-white'), 'tokens.css 缺纯白填充档令牌').toBe('#FFFFFF')
  })
  for (const [file, sel] of WHITE_FILL_SITES) {
    it(`${file} → ${sel} 底为纯白`, () => {
      const got = bgsOf(read(file), sel)
      // 匹配不到 background 同样红灯：选择器改名/规则挪走都属"形态变了"，不许静默放行
      expect(got.length, `${sel} 未匹配到含 background 的声明块，请同步本锁`).toBeGreaterThan(0)
      expect(got.some(isPureWhite), `${sel} 实际底值 ${JSON.stringify(got)} 不是纯白`).toBe(true)
    })
  }
  it('ErrorBoundary 重试按钮（React 内联样式，不走 CSS 块解析）底为纯白', () => {
    const src = read('src/components/ErrorBoundary.tsx')
    const got = [...src.matchAll(/background:\s*'([^']+)'/g)].map((m) => m[1])
    expect(got.length, '内联 background 形态变了，请同步本锁').toBeGreaterThan(0)
    expect(got.some(isPureWhite), `重试按钮底值 ${JSON.stringify(got)} 不是纯白`).toBe(true)
  })
  it('按钮/CTA/徽标/气泡类选择器不得再用文字档灰做整块填充', () => {
    const offenders: string[] = []
    for (const f of walkSrc('src')) {
      const body = stripComments(read(f))
      const re = /([^{}]+)\{([^{}]*)\}/g
      let m: RegExpExecArray | null
      while ((m = re.exec(body))) {
        const sel = normSel(m[1])
        if (!WHITE_FILL_SEL.test(sel)) continue
        // 只锁「这一件自己的面」：末级是裸元素（badge 里的 i 圆点、按钮里的 span）时跳过，
        // 那些子元素取的是 --lc-success 活跃档，属合法灰，不是整块填充。
        if (/(\s|^)(i|em|b|strong|span|svg|path|circle|rect)\s*$/.test(sel)) continue
        for (const v of m[2].matchAll(/background(?:-color)?\s*:\s*([^;]+)/g)) {
          if (/(#E7E9EA|var\(\s*--lc-(text|text-1|success)\s*\))/i.test(v[1])) {
            offenders.push(`${f}  ${sel} { ${v[0].trim()} }`)
          }
        }
      }
    }
    expect(offenders, '实心白件一律取 #FFFFFF / var(--lc-fill-white)：\n' + offenders.join('\n')).toEqual([])
  })
})

// ---- H) 扩展插件面（浏览器扩展 popup + 划词注入样式）按同一套真值 ----
// 为什么放这里：`extension/` 是独立打包物，既不进 vite 构建产物（D/G 的 walkSrc('src') 扫不到），
// 也不是后端直出 HTML（backend-go public_ui_test.go 扫不到）——它是第 4 类渲染盲区。
// 2026-09-22 全量排查时发现它整套还是 indigo #1a237e 主色 + 白底气泡 + 绿色成功态，
// 与交付的 X/Grok 单色纯黑（§1.1 全站无蓝无绿）完全相反，故在此补锁。
const EXT_POPUP_HTML = read('../extension/popup.html')
const EXT_CONTENT_CSS = read('../extension/content.css')
// 旧扩展主题的自造色：靛蓝主色族 + Google 灰族 + 语义绿 + 自造琥珀高亮
const EXT_BANNED_HEX = ['1A237E', '3949AB', '2E7D32', 'DADCE0', '202124', 'FFD54F', '555', '999', 'DDD', 'FFF']

describe('H 扩展插件面（popup + 划词注入样式）单色真值', () => {
  for (const [name, src] of [['extension/popup.html', EXT_POPUP_HTML], ['extension/content.css', EXT_CONTENT_CSS]] as const) {
    const code = stripComments(src)
    it(`${name} 旧靛蓝/浅底/绿色清零`, () => {
      const hits = [...code.matchAll(/#([0-9a-fA-F]{3,8})\b/g)].map((m) => m[1].toUpperCase())
        .filter((h) => EXT_BANNED_HEX.includes(h))
      expect(hits, `${name} 出现旧扩展主题色：\n${hits.join(', ')}`).toEqual([])
    })
    it(`${name} 取 §1.1 令牌真值`, () => {
      expect(code).toContain('#0E1014')
      expect(code).toContain('#FFFFFF')
      expect(code).toContain('1.2px')
    })
  }
  it('popup 主按钮＝纯白底黑字，成功态不标绿', () => {
    const code = stripComments(EXT_POPUP_HTML)
    expect(code, 'popup 主按钮未按交付真值走白底黑字').toContain('background: var(--lc-white); color: #000000')
    expect(code, '页面底必须纯黑（§1.1 --lc-bg）').toContain('background: var(--lc-bg)')
  })
  it('划词浮动按钮＝实心白件、结果气泡＝深色面板', () => {
    const code = stripComments(EXT_CONTENT_CSS)
    expect(code, 'FAB 底不是纯白档').toContain('background: var(--lc-white)')
    expect(code, '气泡底不是 §1.1 面板档').toContain('background: var(--lc-panel)')
  })
})
