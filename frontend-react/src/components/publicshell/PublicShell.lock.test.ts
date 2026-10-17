// ============================================================================
// components/publicshell/PublicShell.lock.test.ts — 未登录门面移动壳的**接线与形态锁**
//
// 由来（2026-10-17 用户令）：「未登录的移动端也做一下适配，参考现在的登录态移动端设计」。
// 这一批把四支未登录门面（官网首页 / 定价 / 比价 / 登录注册）统一套上 PublicShell，
// 复用登录态前台外壳的「顶栏＋汉堡抽屉」形态。用户令是把**四支**当成一件事，所以本锁
// 按**路由全集**判（同 AGENTS §三「家族级锁要按全集判」，只测单页＝漏一页就整页返祖）。
//
// 四条判据（各问一个"改坏了会不会红"）：
//   L1 接线（守卫级）：App.tsx 的未登录分诊块里，**每一个** `return (` 都必须被 <PublicShell>
//      包住——数「页面返回数」与「<PublicShell 出现数」的对等，并逐页点名收口跨度
//      （只测"Shell 组件本身能渲染"是判据级，代替不了"每一条路由都接上了"这一守卫级，AGENTS §三）。
//   L2 形态（等值）：publicshell.css 必须在 ≤640 档把三支页头（.lc-mkt-nav/.lc-prc-nav/.lc-cmp-nav）
//      display:none（否则出现「壳顶栏＋页面自己顶栏」两条栏），且登录固定壳按令牌下移。
//   L3 单断点：PublicShell.tsx 必须且只能复用 useMobileShell()（不得再写第二份 matchMedia/断点）。
//   L4 靶子等值：e2e 判据用的类名（.pub-mob-topbar/.pub-topbar-actions/.pub-topbar-login/
//      .pub-drawer/.pub-drawer__cta/.pub-scrim）
//      必须同时存在于组件与 e2e/mobile_uat.spec.ts——一侧改名即红（同 BrandTagline 那条两处锁）。
//   L5 右簇归属（★ 2026-10-17 用户令「小屏幕把 language 选择器和登录按钮拿出来放右上角」）：
//      顶栏右簇必须有语言钮与登录入口，**抽屉里两者都不许再有**——「搬过去」不是「多放一份」，
//      只查一半（顶栏有没有）会让"抽屉里还留着旧的那份"一路绿（两处入口并存＝改一处漏一处的温床）。
//   L6 右簇同档（★ 2026-10-17 用户报「首页两个按钮组件样式都不统一」附截图）：
//      两枚入口必须写在**同一条规则**里（拆成两份＝只改一份的温床），且那条规则把几何档写全
//      （999 圆角／高 30／内垫 0 12／1.2px 描边／14px·500／行高 1）；别的规则不许单独覆写它们的几何。
// 反证 R1~R6：分别把六处改坏，必须各自判红（先证明响应，再证明修法正当）。
//
// ⚠️ 本锁只问**剥注释之后**的正文：注释里出现类名/说明是记录，不是实现（同 §一·5「注释不许冒充实现」）。
// 运行：npx vitest run src/components/publicshell/PublicShell.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const APP = path.join(HERE, '..', '..', 'App.tsx')
const SHELL = path.join(HERE, 'PublicShell.tsx')
const CSS = path.join(HERE, '..', '..', 'styles', 'publicshell.css')
const SPEC = path.join(HERE, '..', '..', '..', 'e2e', 'mobile_uat.spec.ts')

/** 剥注释（块注释 → 行注释）。宁可少剥也不许把正文剥掉：剥过头会让负向锁"看起来更干净"＝假绿。 */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/** 取未登录分诊块：`if (!user) {` 到下一个顶层守卫 `if (path.startsWith('/admin'))` 之间。 */
function unauthBlock(app: string): string {
  const s = app.indexOf('if (!user) {')
  const e = app.indexOf("if (path.startsWith('/admin'))")
  if (s < 0 || e < 0 || e <= s) return ''
  return stripComments(app.slice(s, e))
}

/** L1 判据：未登录块里「页面返回数」与「<PublicShell 数」必须相等，且逐页点名收口跨度。 */
function wiringFailures(app: string): string[] {
  const block = unauthBlock(app)
  const bad: string[] = []
  if (!block) return ['取不到未登录分诊块（App.tsx 的 !user 分支锚点漂了 ⇒ 本锁没有射程，不是"通过"）']
  const pageReturns = (block.match(/return \(/g) || []).length
  const shells = (block.match(/<PublicShell/g) || []).length
  // 射程自证：分诊块本该有 4 支（首页/定价/比价/登录兜底），取到 0 说明切片失败
  if (pageReturns < 4) bad.push(`未登录分诊块里只数到 ${pageReturns} 个页面 return（预期 ≥4，锚点可能漂了）`)
  if (shells !== pageReturns) bad.push(`页面返回 ${pageReturns} 支、<PublicShell 只出现 ${shells} 次 ⇒ 有门面漏包外壳`)
  // 逐页点名：每个页面根之前 600 字符内必须出现过 <PublicShell（这正是四支的收口跨度）
  for (const p of ['<PricingPage />', '<PriceComparePage />', '<Landing />', '<Login']) {
    const i = block.indexOf(p)
    if (i < 0) { bad.push(`未登录分诊块里找不到页面 ${p}`); continue }
    const before = block.slice(Math.max(0, i - 600), i)
    if (!before.includes('<PublicShell')) bad.push(`${p} 未被 <PublicShell> 包裹（未登录门面漏了移动壳）`)
  }
  return bad
}

/** 取 `@media` 块的**全部**实例（同一 header 在文件里可以出现多次）。 */
function atRuleBlocks(css: string, header: string): string[] {
  const out: string[] = []
  let i = css.indexOf(header)
  while (i >= 0) {
    const open = css.indexOf('{', i)
    if (open < 0) break
    let depth = 0
    let j = open
    for (; j < css.length; j++) {
      if (css[j] === '{') depth++
      else if (css[j] === '}') { depth--; if (depth === 0) break }
    }
    out.push(css.slice(open + 1, j))
    i = css.indexOf(header, j)
  }
  return out
}

/** L2 判据：≤640 档必须收起三支页头（等值），并按令牌给登录固定壳让位。 */
function cssFailures(css: string): string[] {
  const bad: string[] = []
  const blocks = atRuleBlocks(stripComments(css), '@media (max-width: 640px)')
  if (blocks.length === 0) return ['publicshell.css 里找不到 @media (max-width: 640px) 块（锚点漂了 ⇒ 本锁没有射程）']
  const hide = blocks.find((b) => b.includes('.lc-mkt-nav'))
  if (!hide) { bad.push('≤640 档没有收起 .lc-mkt-nav（壳顶栏与页面自带头部会并存成两条栏）') }
  else {
    for (const sel of ['.pub-shell .lc-mkt-nav', '.pub-shell .lc-prc-nav', '.pub-shell .lc-cmp-nav']) {
      if (!hide.includes(sel)) bad.push(`≤640 档未收起的页头：${sel}`)
    }
    // 三支必须是 display:none（而不是只改 padding —— 那样页头仍在屏上占位）
    if (!/\{\s*display:\s*none;\s*\}/.test(hide)) bad.push('页头隐藏规则不是 display:none（可能只改内垫＝仍在屏上占位）')
  }
  // 登录固定壳让位顶栏：必须吃令牌，不许写死像素（写死＝与 --lc-mob-topbar-h 漂移）
  const auth = blocks.find((b) => b.includes('.lc-auth-bg'))
  if (!auth) bad.push('≤640 档没有给登录固定壳(.lc-auth-bg)让位顶栏的规则')
  else if (!auth.includes('var(--lc-mob-topbar-h)')) bad.push('登录固定壳让位下限不是 var(--lc-mob-topbar-h)（写死像素会与令牌漂移）')
  return bad
}

/** L3 判据：唯一断点来源＝useMobileShell()，组件自身不得再写 matchMedia/断点。 */
function singleBreakpointFailures(shell: string): string[] {
  const bad: string[] = []
  const code = stripComments(shell)
  if (!/from '@\/components\/frontshell\/useMobileShell'/.test(code)) bad.push('PublicShell.tsx 未复用 useMobileShell（断点应只有一处来源）')
  if (/matchMedia|innerWidth|max-width/.test(code)) bad.push('PublicShell.tsx 自己又写了一份断点判定（两处各判一次档必然打架）')
  return bad
}

/**
 * L5 判据：语言钮与登录入口只许待在顶栏右簇，抽屉里一件都不许留。
 * 切法按函数名锚点分段（顶栏 vs 抽屉），不按"下一个顶层声明"猜——同 doubleConfirmGate 那条教训。
 */
function topbarFailures(shell: string): string[] {
  const code = stripComments(shell)
  const bad: string[] = []
  const ti = code.indexOf('function PublicMobileTopBar')
  const di = code.indexOf('function PublicDrawer')
  if (ti < 0 || di < 0 || di <= ti) {
    return ['取不到顶栏/抽屉的函数切面（锚点漂了 ⇒ 本锁没有射程，不是"通过"）']
  }
  const topbar = code.slice(ti, di)
  const drawer = code.slice(di)
  if (!topbar.includes('<LangSelect')) bad.push('顶栏右簇缺语言钮（用户令：语言选择器放右上角）')
  if (!/pub-topbar-login/.test(topbar)) bad.push('顶栏右簇缺登录钮（用户令：登录按钮放右上角）')
  if (/fs-langrow|<LangSelect/.test(drawer)) bad.push('抽屉里仍挂着语言钮（应已搬到顶栏右簇，不许两处并存）')
  if (/href="\/login"/.test(drawer)) bad.push('抽屉里仍挂着登录入口（应已搬到顶栏右簇，不许两处并存）')
  return bad
}

/** L4 判据：e2e 靶子类名必须两侧同时存在。 */
const TARGETS = ['.pub-mob-topbar', '.pub-topbar-actions', '.pub-topbar-login',
  '.pub-drawer', '.pub-drawer__cta', '.pub-scrim']
function anchorFailures(shell: string, spec: string): string[] {
  const bad: string[] = []
  for (const t of TARGETS) {
    const cls = t.slice(1)
    if (!shell.includes(cls)) bad.push(`组件里找不到靶子类名 ${t}（e2e 判据会读空＝恒绿或恒红）`)
    if (!spec.includes(cls)) bad.push(`e2e/mobile_uat.spec.ts 未用靶子类名 ${t}（判据没落到这颗件上）`)
  }
  return bad
}

/** 把样式表拆成 `{选择器列表, 规则体}` 序列（按大括号配对切；@media 里的内层规则也会各自成条）。 */
function cssRules(css: string): Array<{ sel: string; body: string; i: number }> {
  const out: Array<{ sel: string; body: string; i: number }> = []
  const re = /([^{}]+)\{([^{}]*)\}/g
  let m: RegExpExecArray | null
  while ((m = re.exec(css)) !== null) {
    out.push({ sel: m[1].split(',').map((s) => s.trim()).filter(Boolean).join(','), body: m[2], i: m.index })
  }
  return out
}

/**
 * L6 判据：顶栏右簇两枚入口（语言钮／登录钮）必须**同档**——且是写在**同一条规则**里的那种同档。
 *
 * 由来（2026-10-17 用户报「首页两个按钮组件样式都不统一」并附截图）：语言钮是库件 .lang-sel-btn
 * （交付档 8px 圆角／内垫 10／行高 1），登录钮是本壳新增件（999／内垫 12／行高继承 1.6），
 * 并排＝圆角矩形＋胶囊两种形态。这类"两枚并排件各写一份"的漂移，只锁"两处取值相等"是不够的
 * （下一批仍会各改一份、且当两处同时被改成另一个不同的值时就永远相等地错）——所以判据问的是
 * **同一条规则同时列出这两个选择器**，再要求那条规则把几何档写全。
 * 另配两条负向：别的规则不许单独覆写这两枚的几何（拆出去＝两把尺子）。
 */
function topbarPairFailures(css: string): string[] {
  const rules = cssRules(stripComments(css))
  const bad: string[] = []
  const GEO = /(?:^|;)\s*(height|padding|border-radius|font-size|font-weight|line-height)\s*:/
  const both = rules.filter((r) =>
    r.sel.includes('.pub-topbar-actions .lang-sel-btn') && r.sel.includes('.pub-topbar-login'))
  if (both.length === 0) {
    return ['语言钮与登录钮不在同一条规则里（拆成两份＝下一个人只改一份的温床；用户已报过一次样式不统一）']
  }
  if (both.length > 1) bad.push(`右簇同档规则重复定义了 ${both.length} 次（后一条会压前一条＝等于又拆成两份）`)
  const body = both[0].body
  for (const [key, want] of [['border-radius', '999px'], ['height', '30px'], ['padding', '0 12px'],
    ['font-size', '14px'], ['font-weight', '500'], ['line-height', '1']] as const) {
    if (!new RegExp(`(?:^|;)\\s*${key}\\s*:\\s*${want.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*(?:;|$)`).test(body)) {
      bad.push(`右簇同档规则里 ${key} 不是 ${want}（两枚并排就不一致了）`)
    }
  }
  if (!/(?:^|;)\s*border\s*:\s*1\.2px/.test(body)) bad.push('右簇同档规则里描边不是 1.2px（〇-P 交付档）')
  // 负向：别的规则不许单独覆写这两枚中任一枚的几何（拆出去＝两处尺子）
  for (const r of rules) {
    if (r.i === both[0].i) continue
    const touches = (r.sel.includes('.pub-topbar-login') || r.sel.includes('.lang-sel-btn')) &&
      r.sel.includes('.pub-topbar-actions')
    if (touches && GEO.test(r.body)) {
      bad.push(`另有规则单独覆写右簇入口几何（${r.sel}）⇒ 两处尺子，改一处漏一处`)
    }
  }
  return bad
}

describe('未登录门面移动壳（PublicShell）锁', () => {
  const appRaw = readFileSync(APP, 'utf8')
  const shellRaw = readFileSync(SHELL, 'utf8')
  const cssRaw = readFileSync(CSS, 'utf8')
  const specRaw = readFileSync(SPEC, 'utf8')

  it('L1 接线：未登录四支门面每一支都被 <PublicShell> 包住', () => {
    const bad = wiringFailures(appRaw)
    expect(bad, bad.join('\n')).toEqual([])
  })

  it('L2 形态：≤640 收起三支页头（display:none）且登录壳按令牌让位', () => {
    const bad = cssFailures(cssRaw)
    expect(bad, bad.join('\n')).toEqual([])
  })

  it('L3 单断点：只复用 useMobileShell()，组件自身零 matchMedia', () => {
    const bad = singleBreakpointFailures(shellRaw)
    expect(bad, bad.join('\n')).toEqual([])
  })

  it('L4 靶子等值：e2e 类名在组件与 spec 两侧同时存在', () => {
    const bad = anchorFailures(shellRaw, specRaw)
    expect(bad, bad.join('\n')).toEqual([])
  })

  it('L5 右簇归属：语言钮与登录钮在顶栏、抽屉里一件不留', () => {
    const bad = topbarFailures(shellRaw)
    expect(bad, bad.join('\n')).toEqual([])
  })

  it('L6 右簇同档：语言钮与登录钮写在同一条规则里且几何档写全', () => {
    const bad = topbarPairFailures(cssRaw)
    expect(bad, bad.join('\n')).toEqual([])
  })

  it('R1 反证：摘掉一支 <PublicShell> 必须被 L1 判红', () => {
    const mutated = appRaw.replace(/(\s*)<PublicShell>(\s*)<RouteBoundary>/, '$1<RouteBoundary>')
    expect(mutated, '反证失效：变异没落地（替换没命中＝没反证）').not.toBe(appRaw)
    expect(wiringFailures(mutated).length, 'R1 失效：漏包外壳没被判红').toBeGreaterThan(0)
    expect(wiringFailures(appRaw)).toEqual([])
  })

  it('R2 反证：摘掉一条页头隐藏（.lc-cmp-nav）必须被 L2 判红', () => {
    const mutated = cssRaw.replace(/\.pub-shell \.lc-cmp-nav\s*\{[^}]*\}/, '')
    expect(mutated, '反证失效：变异没落地（替换没命中＝没反证）').not.toBe(cssRaw)
    expect(cssFailures(mutated).length, 'R2 失效：比价页头没被收起却没判红').toBeGreaterThan(0)
    expect(cssFailures(cssRaw)).toEqual([])
  })

  it('R3 反证：e2e 侧摘掉抽屉 CTA 靶子必须被 L4 判红', () => {
    const mutated = specRaw.replace(/pub-drawer__cta/g, 'pub-drawer-cta')
    expect(mutated, '反证失效：变异没落地（替换没命中＝没反证）').not.toBe(specRaw)
    expect(anchorFailures(shellRaw, mutated).length, 'R3 失效：e2e 靶子消失却没判红').toBeGreaterThan(0)
    expect(anchorFailures(shellRaw, specRaw)).toEqual([])
  })

  it('R4 反证：把语言钮挪回抽屉必须被 L5 判红', () => {
    const mutated = shellRaw
      .replace('        <LangSelect align="right" />\n', '')
      .replace('        <nav className="fs-nav">', '        <div className="fs-langrow"><LangSelect align="left" /></div>\n        <nav className="fs-nav">')
    expect(mutated, '反证失效：变异没落地（替换没命中＝没反证）').not.toBe(shellRaw)
    expect(topbarFailures(mutated).length, 'R4 失效：语言钮被挪回抽屉却没判红').toBeGreaterThan(0)
    expect(topbarFailures(shellRaw)).toEqual([])
  })

  it('R5 反证：把两枚入口拆成两条规则必须被 L6 判红', () => {
    // 拆法＝把登录钮单独提出来、给回"原来的"圆角矩形档（正是用户截图里那个形态）
    const mutated = cssRaw.replace(
      '.pub-topbar-actions .lang-sel-btn,\n.pub-topbar-login { display: inline-flex;',
      '.pub-topbar-actions .lang-sel-btn { display: inline-flex;')
      .replace('  text-decoration: none; white-space: nowrap; }',
        '  text-decoration: none; white-space: nowrap; }\n.pub-topbar-login { height: 30px; padding: 0 10px; border-radius: 8px; }')
    expect(mutated, '反证失效：变异没落地（替换没命中＝没反证）').not.toBe(cssRaw)
    expect(topbarPairFailures(mutated).length, 'R5 失效：两枚入口被拆成两条规则却没判红').toBeGreaterThan(0)
    expect(topbarPairFailures(cssRaw)).toEqual([])
  })

  it('R6 反证：同一条规则里把圆角改回 8px 必须被 L6 判红（等值档不许退化）', () => {
    const mutated = cssRaw.replace('border-radius: 999px; background: none;', 'border-radius: 8px; background: none;')
    expect(mutated, '反证失效：变异没落地（替换没命中＝没反证）').not.toBe(cssRaw)
    expect(topbarPairFailures(mutated).length, 'R6 失效：圆角退化回 8px 却没判红').toBeGreaterThan(0)
    expect(topbarPairFailures(cssRaw)).toEqual([])
  })
})
