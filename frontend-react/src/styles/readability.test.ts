// ============================================================================
// readability.test.ts — ★ #35 可读性闸门（2026-09-21 用户反馈：登录后前后台字体偏小、
// 次级文字对比度偏低 → 全站上调一档，并用本文件钉死，防止后续改动把字号/对比度改回去）
// ----------------------------------------------------------------------------
// 五条断言（① ~ ③ 为 #35 原口径，④ ⑤ 为 2026-09-22 #67/#68 追加）：
//  1) 组件库（src/ui/langcross/css/components.css，冻结区不可直接改）里每一处 ≤13.5px
//     的小字号，theme.css §十都必须有一条 `html <原选择器>` 的页面级覆写，且正好抬 1px；
//  2) 次级文字令牌（--lc-text-2/3/4）对页面底 #000 与卡面 #0E1014 的对比度都要 ≥6:1
//     （AA 底线 4.5:1 留一倍余量），且必须比 tokens.css 原值更亮（只准变好不准回退）；
//  3) 登录后的 tsx 里禁止再写死那四个灰色字面值，必须走语义令牌，
//     否则未来调对比度又要改几十个文件。
//  4) ★ #67：顶栏控件（品牌 / 账号钮 / 语种下拉 / 徽标）与工作台 Tab 的字号下限——
//     用户反馈「页眉、tab 都太小，X / Grok 的黑色 UI 字号很大」，防字阶被打回原形；
//  5) ★ #68：描边令牌（--lc-border-faint/card/input、--npz-line）对页面底 ≥4:1，
//     同样只准变亮不准变暗（纯黑 UI 上「看不见边」是本轮抱怨的直接成因）；
//  6) ★ #68 续：登录后 tsx 里写死的暗描边一律收口到描边令牌，防止令牌调档被字面值架空。
// 说明：本测试读源文件而非渲染后的 DOM——jsdom 不加载外链 CSS，源码级断言才是稳定闸门。
//   代价是它只认「写在 CSS 里的值」：运行时内联样式/换肤覆盖造成的回退，由
//   e2e/pixel_uat.spec.ts 的 P2b（getComputedStyle 实测字号与对比度）互补覆盖。
// 条目数与 describe 一一对应：1)~3) = #35 的三个 describe，4) = #67 字号锁，
//   5)~6) = #68 的两个 describe（描边令牌对比度 + 描边字面值禁写死）。
// ============================================================================
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// ROOT 指向 frontend-react 包根：本文件用相对包根的硬路径读源文件，不做运行时拼接。
const ROOT = fileURLToPath(new URL('../../', import.meta.url))
// 三份源文件在模块顶层就读进来：路径一旦改名/删除，import 阶段就直接抛错，
// 让「闸门本身失效」也是红灯，而不是静默地什么都不断（本文件最怕的就是假绿）。
const KIT_CSS = readFileSync(ROOT + 'src/ui/langcross/css/components.css', 'utf-8')
// KIT_TOKENS：冻结交付包的令牌真源，用来比对页面级覆盖有没有把对比度改回去。
const KIT_TOKENS = readFileSync(ROOT + 'src/ui/langcross/css/tokens.css', 'utf-8')
// THEME_CSS：本仓自己的页面级覆盖层，字号与对比度锁的断言对象。
const THEME_CSS = readFileSync(ROOT + 'src/styles/theme.css', 'utf-8')

// 去掉 /* */ 注释后按 { } 拆规则，返回「选择器 → font-size 列表」
// 为什么先剥注释：CSS 里大量「旧值 → 新值」的说明性注释带着 12px/13px 这类字样，
// 不剥掉会把它们当成真实声明扫出来，闸门立刻变成误报机。
function rules(css: string) {
  const body = css.replace(/\/\*[\s\S]*?\*\//g, '')
  const out: { sel: string; sizes: number[] }[] = []
  const re = /([^{}]+)\{([^{}]*)\}/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body))) {
    const sel = m[1].trim().replace(/\s+/g, ' ')
    // @ 开头（@media/@keyframes 等）与 from/to（关键帧步）都不算组件字阶：
    // 前者只是包裹层，真正的选择器在它内层的规则里，会单独被本循环命中。
    if (sel.startsWith('@') || sel.startsWith('from') || sel.startsWith('to')) continue
    const sizes = [...m[2].matchAll(/font-size:\s*([0-9.]+)px/g)].map((x) => Number(x[1]))
    if (sizes.length) out.push({ sel, sizes })
  }
  return out
}

// theme.css 的页面级覆写表：「选择器（去掉 html 前缀）→ 该规则的 font-size」
// 只认 `html xxx` 形态：§十/§十一 的覆写约定就是这个前缀（抬到 html 层才压得住组件库），
// 换成别的写法（.lc-root 嵌套、!important 补丁）就不算「按约定覆写」，应当判红。
// 注意 sizes[0]：一条规则里写了多个 font-size 时只取第一个，所以覆写别在同一规则内堆叠档位。
const themeMap = new Map<string, number>()
for (const r of rules(THEME_CSS)) {
  if (!r.sel.startsWith('html ')) continue
  for (const s of r.sel.split(',')) themeMap.set(s.trim().replace(/^html\s+/, ''), r.sizes[0])
}
// .lc-root 基础字号是唯一的「非小字号」覆写（14→15），单独断言
// 只扫 10~13.5px 这一带：13.5px 以上已达 #35 上调后的档位，无需再管；
// 下界 10px 留给装饰性超小字号（当前库内最小档就是 10px，再往下出现即属误伤）。
// 一条规则里的多个 font-size 仍按整条判定（every），只要混了一个达标的档位就整条不入围。
const kitSmall = rules(KIT_CSS)
  .filter((r) => r.sizes.every((v) => v >= 10 && v <= 13.5))
  .flatMap((r) => r.sel.split(',').map((s) => ({ sel: s.trim().replace(/\s+/g, ' '), size: r.sizes[0] })))

describe('#35 字号上调闸门', () => {
  it('组件库每处 ≤13.5px 小字号都有 +1px 的页面级覆写', () => {
    // 实现口径是「至少抬 1px」（override < size + 1 才判缺），不是严格等于 +1：
    // 将来某处再往上调一档是允许的，本锁只拦「没抬」和「抬不够」。
    // 匹配键是去掉 html 前缀后的选择器原文（含大小写与伪类），所以覆写的选择器
    // 必须与组件库那一条逐字一致，改名即视为「缺覆写」。
    const missing: string[] = []
    for (const { sel, size } of kitSmall) {
      const override = themeMap.get(sel)
      if (override === undefined || override < size + 1) missing.push(`${sel}: 库内 ${size}px → 覆写 ${override ?? '无'}px`)
    }
    expect(missing, '缺覆写或抬升不足 1px：\n' + missing.join('\n')).toEqual([])
    // 断言非空集：解析一旦失效（选择器改名/正则退化）也要立刻红灯，而不是「零条通过」
    expect(kitSmall.length).toBeGreaterThan(40)
  })
  it('基础正文 .lc-root 由 14px 抬到 15px', () => {
    expect(themeMap.get('.lc-root')).toBe(15)
  })
})

// WCAG 相对亮度与对比度（与《部署指南》无关，纯前端可读性校验）
// 自己实现而不引第三方库：口径要与 e2e/pixel_uat.spec.ts 里的 contrastOnCard 完全一致
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
// 取某个 `--令牌: #rrggbb` 的字面值。
// ★ 顺序敏感：String.prototype.match 只返回**第一个**命中，所以本函数读的是文件里
//   最早出现的那一处声明。同一令牌在 theme.css 里写两档（例如后面又「补救」一次），
//   本闸门看到的仍是旧值，而浏览器实际用的是后写的——这类不一致只有 CSS 审查能发现，
//   闸门不会红。故约定：每个令牌在 theme.css §十 只出现一次，改档就地改，别再追加一条。
//   （与上面 themeMap 的 Map.set 相反：同选择器重复出现时是「后者覆盖前者」。）
// 只认 6 位十六进制：写成 #abc 简写会匹配不到，等价于「未覆写」而判红——刻意为之，
//   简写撑不起下面的对比度实算。
// 键名与冒号之间不允许夹别的标识符，所以 --lc-border-card 不会误吃 --lc-border-card-dim 的值；
//   但 `var(--lc-border-card)` 这种引用同样不匹配（后面是 `)`），只认字面量定义那一行。
// 找不到时返回空串，由调用处的 `.length === 7` 先红在「必须覆写」，而不是让 contrast()
//   拿着 '' 去 parseInt 得出 NaN 比较（NaN 比较恒 false，会变成查不出原因的假绿）。
const token = (css: string, name: string) => {
  const m = css.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`))
  return m ? m[1] : ''
}

describe('#35 次级文字对比度闸门', () => {
  // 三个令牌逐个开 it：某一条红时报告直接点名是哪个灰阶被改暗，省得在合并断言里翻。
  // 底色的两个取值都要算：页面底 #000（纯黑换肤后的整屏底）与卡面 #0E1014（次级文字实际
  // 大量落在卡上），只钉其一会留出「卡面上偏暗」的盲区。
  for (const name of ['--lc-text-2', '--lc-text-3', '--lc-text-4']) {
    const now = token(THEME_CSS, name)
    const before = token(KIT_TOKENS, name)
    it(`${name} 在页面底与卡面上都 ≥6:1 且不得比原值更暗`, () => {
      expect(now.length, 'theme.css §十 必须覆写 ' + name).toBe(7)
      expect(contrast(now, '#000000')).toBeGreaterThanOrEqual(6)
      expect(contrast(now, '#0E1014')).toBeGreaterThanOrEqual(6)
      // 这条「只准变好」的锁不给 before 兜底（对比 #68 那段有 if）：
      // 组件库这三个令牌必有原值，取不到就应红，正好暴露「原令牌被改名/删除」。
      expect(contrast(now, '#000000'), '不得回退到 tokens.css 的更暗值')
        .toBeGreaterThanOrEqual(contrast(before, '#000000'))
    })
  }
})

// 未登录的营销页保持原设计口径，不在本闸门范围内（#35 次级灰 / #68 描边两条锁共用）
// 豁免清单逐项的理由：
//   langcross —— 组件库冻结区，只减不增，不允许为了让闸门变绿去改它；
//   .test.     —— 测试自身源码里会出现这些字面值（对照样例/断言常量），扫到只是自指；
//   Landing/PricingPage/LeadForm/SiteFooter/HeroDemo/Login —— #35 只管「登录后」界面，
//                  营销门面走另一套浅色设计口径；
//   theme.css / mobile.css —— 语义令牌的定义处与移动端补丁，本来就该写字面值。
const EXEMPT = /langcross|\.test\.|\/(Landing|PricingPage|LeadForm|SiteFooter|HeroDemo|Login)\.tsx|styles\/(theme|mobile)\.css/

describe('#35 灰色字面值禁再写死', () => {
  // 走「全量扫源码 + 违规清单」而不逐文件开 it：命中项要一次看全（改一半留一半仍算红），
  // 且新增文件天然纳入扫描，无需维护清单。
  const offenders: string[] = []
  for (const f of walkTs('src')) {
    if (EXEMPT.test(f)) continue
    readFileSync(ROOT + f, 'utf-8').split('\n').forEach((line, i) => {
      if (/color:\s*['"]?#(9AA0AA|878D95|7A828E|8A9099)/i.test(line)) offenders.push(`${f}:${i + 1}`)
    })
  }
  it('登录后界面不得再出现写死的次级灰（改走 var(--lc-text-*)）', () => {
    expect(offenders, '请改为语义令牌：\n' + offenders.join('\n')).toEqual([])
  })
})

// 枚举 src 下的 ts/tsx（测试里不跑 shell，也不进组件库冻结区）
function walkTs(dir: string): string[] {
  const acc: string[] = []
  for (const e of readdirSync(ROOT + dir)) {
    const rel = `${dir}/${e}`
    if (statSync(ROOT + rel).isDirectory()) { if (!/langcross/.test(rel)) acc.push(...walkTs(rel)); continue }
    if (/\.(tsx|ts)$/.test(rel)) acc.push(rel)
  }
  return acc
}

// ============================================================================
// ★ #67 + #68（2026-09-22 用户反馈「页眉、tab 都太小看不清楚；对比度整体再提升」）
// 两条锁：
//  1) 顶栏控件字号不得低于本档（theme.css §十一 的 html 覆写 + App.tsx 内联 .app-tab），
//     防止后续换肤/组件库升级把导航字阶悄悄打回 12~14px；
//  2) 描边令牌对页面底对比度 ≥4:1 且不得比 tokens.css 原值更暗（边框属图形元素，AA 3:1，
//     这里按 4:1 留余量，纯黑底上「看不见边」是本轮抱怨的直接成因）。
// ============================================================================
const APP_TSX = readFileSync(ROOT + 'src/App.tsx', 'utf-8')

describe('#67 顶栏与工作台 Tab 字号锁', () => {
  // 下限值即 theme.css §十一 / App.tsx 内联样式的当前档位，不是「随手取整」：
  // 调档要同时改 CSS 与此处，故意留两道手工同步成本，防止单方面把字阶打回 12~14px。
  // .lc-badge（余额/套餐徽标）是四档里最低的一档 12px，本锁只保证它不再往下掉。
  const floor: [string, number][] = [
    ['.brand', 21],         // 品牌字标
    ['.am-trigger', 15],    // 账号菜单触发钮
    ['.lang-sel-btn', 14],  // 12 语种下拉
    ['.lc-badge', 12],      // 余额/套餐徽标
  ]
  for (const [sel, min] of floor) {
    it(`theme.css §十一 覆写 ${sel} 字号 ≥${min}px`, () => {
      const size = themeMap.get(sel)
      expect(size, `theme.css 缺 html ${sel} 的 font-size 覆写`).toBeTypeOf('number')
      expect(size!, `${sel} 实际 ${size}px 低于 ${min}px`).toBeGreaterThanOrEqual(min)
    })
  }
  it('App.tsx 内联 .app-tab 工作台 Tab ≥16px 且钮高 ≥38px', () => {
    // .app-tab 的规则写在 App.tsx 的 <style> 字符串里（不在 theme.css，themeMap 查不到它），
    // 所以这条只能按源码正则钉。正则本身要求 height 声明在 font-size 之前，
    // 内联串里换顺序就会失配——失配同样是红，不是绿。
    // 「形态变了就 truthy 断言先红」是刻意的：宁可红着等人来同步锁，也不要正则静默失配变假绿。
    // 38px 是钮高下限（当前档 40px），与 e2e P2b 的「实高 ≤46px」配对：
    // 下限防钮被压扁到看不清，上限防放大后文案折行——两条一起才是完整的控件几何锁。
    const m = APP_TSX.match(/\.app-tab\{[^}]*?height:\s*(\d+)px[^}]*?font-size:\s*([0-9.]+)px/)
    expect(m, 'App.tsx 内联 .app-tab 规则形态变了，请同步本锁').toBeTruthy()
    expect(Number(m![2]), `Tab 字号 ${m![2]}px 偏小`).toBeGreaterThanOrEqual(16)
    expect(Number(m![1]), `Tab 高度 ${m![1]}px 偏矮`).toBeGreaterThanOrEqual(38)
  })
})

describe('#68 描边对比度锁', () => {
  // 只算页面底 #000，不像 #35 那样再算卡面：描边基本只出现在整屏底色上的分区/卡片外框，
  // 且卡面 #0E1014 比 #000 更亮，同一前景在它上面比值只会更高，#000 才是严苛侧。
  // 4:1 的口径：图形元素 WCAG 底线是 3:1，这里按 4:1 留一档余量（本轮抱怨的是「看不见边」）。
  for (const name of ['--lc-border-faint', '--lc-border-card', '--lc-border-input', '--npz-line']) {
    const now = token(THEME_CSS, name)
    const before = token(KIT_TOKENS, name)
    it(`${name} 对页面底 ≥4:1 且不得比原值更暗`, () => {
      expect(now.length, `theme.css §十 必须覆写 ${name}`).toBe(7)
      expect(contrast(now, '#000000')).toBeGreaterThanOrEqual(4)
      // 与 #35 那段不同，这里给 before 加了 if 兜底：--npz-line 是页面级令牌，
      // tokens.css 里并无同名原值（before 为空串），此时只能保留 4:1 的绝对锁；
      // 另三个 --lc-border-* 有历史值，「只准变亮」那条相对锁照常生效。
      if (before.length) {
        expect(contrast(now, '#000000'), `${name} 不得回退到 tokens.css 的更暗值`)
          .toBeGreaterThanOrEqual(contrast(before, '#000000'))
      }
    })
  }
})

// ★ #68 第二条锁：登录后的描边要么走 --lc-border-* / --npz-line 令牌，要么本身在纯黑底
// ≥4:1（如白底钮的 #E7E9EA 描边）。本轮把 App.tsx #4A505C、ChatWindow/TicketsPage/EditorPage
// 的 #464C58、AiAssist 的 #2A2F3A~#575F6C 全部收口到令牌；写死暗值会让「调一处全站跟随」失效。
describe('#68 描边禁再写死暗值', () => {
  // 底线动态取：以 --lc-border-faint 当前对比度为准，而不是再写死一个数。
  // 用 Math.min(4, floor) 是因为 faint 本身已被上面那条锁保证 ≥4:1，取小值等于
  // 「不比最弱令牌更弱」；万一将来 faint 被调暗，这里跟着一起降，
  // 不会出现「令牌违规没人管、tsx 里合法值反倒全红」的连锁假红。-0.001 抵浮点误差。
  const floor = contrast(token(THEME_CSS, '--lc-border-faint'), '#000000')
  const offenders: string[] = []
  for (const f of walkTs('src')) {
    if (EXEMPT.test(f)) continue
    readFileSync(ROOT + f, 'utf-8').split('\n').forEach((line, i) => {
      // [:=] 两种写法都要扫：[:=] 前者是 CSS 字符串里的 border: / borderBottom:，
      // 后者是 React 内联对象 style={{ borderBottom: '1px solid #464C58' }}——
      // 本轮收口时两类都出现过暗值，只扫 CSS 会漏掉内联这一大口。
      for (const m of line.matchAll(/border[a-z-]*\s*[:=]\s*['"]?([^'"{};]*)#([0-9a-fA-F]{6})/gi)) {
        if (m[1].includes('var(')) continue   // var(--token, #兜底) 的兜底值不算写死
        if (contrast('#' + m[2], '#000000') >= Math.min(4, floor) - 0.001) continue
        offenders.push(`${f}:${i + 1}  ${m[0].trim()}`)
      }
    })
  }
  it('描边字面值不得暗于 --lc-border-faint（改走 var(--lc-border-*)）', () => {
    expect(offenders, '请改为描边令牌：\n' + offenders.join('\n')).toEqual([])
  })
})
