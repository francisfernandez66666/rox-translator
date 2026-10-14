// ============================================================================
// components/BrandTagline.lock.test.ts — ★ 页脚品宣尾注「Powered by LangCross AI」等值锁
// （2026-10-13 用户令②：「在所有有页脚的页面，页脚位置写一条：Powered by LangCross AI，
//   来加重品宣和 ai 赋能价值，重点提醒。」）
//
// 这条锁要防的三件事，恰好是用户这句话里的三个词：
//   ① 「所有有页脚的页面」⇒ 五张渲染面**一张都不许缺**（A 段逐面点名）。
//      本仓历史上「四面改了两面」的形态反复出现（页脚链接、手册入口 D-4 都是这样补的），
//      所以判据不是"扫到某处有"，而是**逐面各判一次**并数出覆盖面条数。
//   ② 「Powered by LangCross AI」是一句**固定字面量**⇒ B 段等值锁钉死整串，
//      禁止任何一面自己抄一份变体（大小写、连字符、"LangCross 能言"混写都算漂移）。
//      唯一事实源是 BrandTagline.tsx 的 BRAND_TAGLINE，后端直出面（Go 字符串）只能逐字对齐它。
//   ③ 用户没有要求本地化，而这一行的性质是**品牌标识**⇒ 它**必须不在** i18n 词典里（D 段负向锁）。
//      ⚠️ 负向锁按本仓纪律必须配正向对照（C 段）：只判"词典里没有这条键"的锁，
//      在扫描本身落空时（路径写错、键名改了、文件为空）会恒真——所以同一次跑里必须先证明
//      词典确实被读到（键数量 >3000 且随机取一把已知键命中），再看这一条不在里面。
//
// 反证（E 段，同一次跑里真跑，不靠注释声明）：把"某一面没有这句"和"这句写歪了"两种坏形态
//   喂给**同一个判据函数** taglineViolation()，必须被抓；再喂五份干净文本作对照，必须全绿。
//   只做磁盘扫描、不喂坏样本的锁，判据写反了会长期恒绿而没人知道（本仓多处真踩过）。
//
// ★ 2026-10-14 用户后令「这个页脚的句子能不能提亮并且做重点提示」⇒ 又添第四件事：
//   ④ 「提亮」是**视觉规格**，不是文案 ⇒ G 段把这一档钉成**跨语言逐维等值锁**。
//      两侧本来长在不同语言里（React 内联样式对象 / Go 内嵌 CSS），改一侧忘另一侧是最默认的结局，
//      所以 G 段比的不是"名字像不像"而是 **var() 解析后的实值等不等**（各自的令牌表各自查）。
//      ⚠️ 一律写成等值锁，禁止"只许更亮／更大"的单向锁（AGENTS §一·5 界面规格＝UI 交付真值）。
//
// 运行：npx vitest run src/components/BrandTagline.lock.test.ts
// ============================================================================
import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { BRAND_TAGLINE, TAGLINE_STYLE } from './BrandTagline'

// resolve() 而不是字符串相加：A 段要跨出包根去读后端直出面（../backend-go/…），
// 裸拼接不会折叠 `..`，会直接 ENOENT（闸门自己失效比红更难发现）——与 readability.test.ts 同一口径。
const ROOT = fileURLToPath(new URL('../../', import.meta.url)) // frontend-react 包根
const read = (p: string) => readFileSync(resolve(ROOT, p), 'utf-8')

/**
 * 剥掉注释，只留"真会被渲染/执行的那一份文本"。
 * ★ 这条不是洁癖，是本批反证真踩出来的：后端直出面我在说明注释里**引用了用户原话**
 *   （那句字面量整串就在注释里），结果把页面正文那句删掉之后 A 段仍然判绿——
 *   「注释冒充实现」是源码级负向锁最典型的假绿形态（AGENTS 记忆里那条「静态负向锁误伤说明注释」的反向版本：
 *   注释既能把负向腿误判红，也能把正向腿误判绿）。
 * 近似口径：行注释（`//` / `#`）整行丢、块注释（`/* … *\/`）与 HTML 注释（`<!-- … -->`）整段丢。
 * 对"`https://` 这种串里的双斜杠"会误伤，但本闸只判"某串在不在这几行里"，
 * 误伤方向是**把注释外内容也丢掉**→ 只会更难判绿（安全方向），不会造假绿。
 */
function stripComments(src: string): string {
  return src
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .filter((l) => !/^\s*(\/\/|#)/.test(l))
    .join('\n')
}

/** Go 直出面只认「页脚那一个 div 的正文」，不给整文件判（整文件判会被注释/别处文案蒙混） */
function goFooterRegion(src: string): string {
  const m = stripComments(src).match(/<div class="footer">([\s\S]*?)<\/div>/)
  return m ? m[1] : ''
}

/**
 * 一张渲染面的页脚里，品宣尾注的**期望形态**是什么。
 * 两种实现都算合规，因为「前端走组件、后端直出走字符串」是既有的分层事实：
 *   · React 面：引用共享组件（`<BrandTagline`）**且**该组件那份文件里就是钉死的字面量；
 *   · Go 面：**页脚那个 div 的正文**里逐字出现同一句（拿不到组件，只能抄，所以靠本锁把它钉成等值）。
 * 反过来，React 面**不许**直接写字符串而不走组件——那样"唯一事实源"就名存实亡，
 * 下一次改文案会出现改了组件、漏了某一面的漂移（本函数对这种情况判红）。
 * ⚠️ 所有判据吃的都是 stripComments 之后的文本：注释里写着这句**不算**这句在页面上。
 */
function taglineViolation(raw: string, kind: 'react' | 'go'): string | null {
  if (raw.trim() === '') return '该面源文件读到空内容（扫描落空＝锁失明，不是这里干净了）'
  const src = stripComments(raw)
  if (kind === 'react') {
    if (src.trim() === '') return '剥掉注释后正文为空（这一面只剩说明，没有实现）'
    if (!src.includes('<BrandTagline')) return '页脚未引用 <BrandTagline> 组件'
    if (!/from\s+['"][^'"]*BrandTagline['"]/.test(src)) return '引用了组件但没有 import（多半是改名式破坏）'
    // React 面禁止在页脚里另抄一份字面量：组件调用本身不含字面量，
    // 正文里出现这句就说明有人绕过了事实源手写一份。
    if (src.includes(BRAND_TAGLINE)) return '不该在本文件里重写一遍字面量（唯一事实源是 BrandTagline.tsx）'
    return null
  }
  const region = goFooterRegion(raw)
  if (region === '') return '后端直出面里取不到 <div class="footer"> 那段（页脚写法变了＝锁失明）'
  return region.includes(BRAND_TAGLINE) ? null : '后端直出页脚正文缺这句固定文案（注释里有也不算）'
}

// 五张渲染面（用户令「所有有页脚的页面」的完整分解）：
// ① SiteFooter＝全站外壳页脚（工作台/后台/登录/注册/工单等**所有经外壳的页面**共用）；
// ② Landing＝官网首页自带页脚；③ PricingPage＝/pricing；④ PriceComparePage＝/compare；
// ⑤ 后端直出 /docs/*（法务页与帮助中心；不在 dist 里，改动必须换 translator-server 二进制）。
const SURFACES: { name: string; file: string; kind: 'react' | 'go' }[] = [
  { name: '全站页脚 SiteFooter', file: 'src/components/SiteFooter.tsx', kind: 'react' },
  { name: '官网首页页脚', file: 'src/components/Landing.tsx', kind: 'react' },
  { name: '报价页页脚', file: 'src/components/PricingPage.tsx', kind: 'react' },
  { name: '对照页页脚', file: 'src/components/PriceComparePage.tsx', kind: 'react' },
  { name: '后端直出 /docs/* 页脚', file: '../backend-go/internal/api/public.go', kind: 'go' },
]

// 反证夹具里那行 import 单独抽出来：E③ 要喂「组件在、又另抄一份」的形态，
// 少了这行会先被「没有 import」那条抓住，就测不到想测的那一档了。
const IMPORT_LINE = `import { BrandTagline } from './BrandTagline'\n`

// ---------------------------------------------------------------------------
// G 段（★ 2026-10-14 尾注「提亮＋重点提示」）用的那套样式等值比对件。
// 全部按**解析后的值**比对，两侧各自的令牌表来自各自那一份 CSS：
//   · React 组件 → src/ui/langcross/css/tokens.css（--lc-fill-white / --lc-invert-fg）
//   · 后端直出面 → public.go 自己内嵌的那份 :root（只有 --lc-white，**没有** --lc-fill-white）
// 这一层"两张令牌表"是刻意的：两侧名字本来就不同，只有换成值才能判"同一档"，
// 也只有换成值才能抓住「把前端令牌名照抄进直出面」这一族静默失效（见 G4 ④）。
// ---------------------------------------------------------------------------

/** 尾注这一档被点名的八个维度（少任何一维都算"某一侧把这条声明摘了"） */
const TAGLINE_DIMENSIONS = [
  'background',
  'border-radius',
  'color',
  'display',
  'font-weight',
  'padding',
  'unicode-bidi',
  'white-space',
]

/** camelCase（React 内联样式）→ kebab-case（CSS），并统一小写：两侧键形状在这里归一 */
const kebab = (k: string) => k.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase()).toLowerCase()

/** 从任意一段 CSS 里读令牌表（同一键首次声明为准，别名值保留 var() 形态交给 resolveVars 递归） */
function tokenTable(css: string): Record<string, string> {
  const t: Record<string, string> = {}
  for (const m of css.matchAll(/(--[\w-]+)\s*:\s*([^;{}]+)/g)) {
    if (!(m[1] in t)) t[m[1]] = m[2].trim()
  }
  return t
}

/** 把值里的 var(--x) 换成令牌表里的实值（跟着别名链最多 5 跳）；**解不开就原样留着** */
function resolveVars(val: string, tokens: Record<string, string>, depth = 0): string {
  if (depth > 5) return val.trim()
  const out = val.replace(/var\(\s*(--[\w-]+)\s*(?:,\s*[^)]*)?\)/g, (whole, name: string) =>
    tokens[name] === undefined ? whole : tokens[name],
  )
  return out === val ? val.trim() : resolveVars(out, tokens, depth + 1)
}

/** 声明串（`a:b;c:d`）→ 归一化的「维度 → 解析后的值」表 */
function cssDecls(block: string, tokens: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const part of block.split(';')) {
    const m = part.trim().match(/^([-\w]+)\s*:\s*([\s\S]+)$/)
    if (!m) continue
    out[kebab(m[1])] = resolveVars(m[2], tokens)
  }
  return out
}

/** 这一段里引用了但那张令牌表没有的名字（＝var() 当场解不开，面色退回 transparent 那一族静默失效） */
function undeclaredVarsIn(block: string, tokens: Record<string, string>): string[] {
  const bad: string[] = []
  for (const m of block.matchAll(/var\(\s*(--[\w-]+)\s*(?:,\s*[^)]*)?\)/g)) {
    if (tokens[m[1]] === undefined && !bad.includes(m[1])) bad.push(m[1])
  }
  return bad
}

/** 组件那份样式对象 → CSS 声明串（不正则扫源码，直接吃导出的对象：删掉属性＝这里少一维，必红） */
function reactTaglineCss(): string {
  return Object.entries(TAGLINE_STYLE as Record<string, string | number>)
    .map(([k, v]) => `${kebab(k)}:${v};`)
    .join('')
}

const reactTokens = () => tokenTable(read('src/ui/langcross/css/tokens.css'))

/** 直出面那一份 :root 声明块（本页**唯一**的令牌来源；取不到就交回空表，由调用侧的在位性判据点名） */
function goTokens(): Record<string, string> {
  const raw = stripComments(read('../backend-go/internal/api/public.go'))
  const m = raw.match(/:root\s*\{([\s\S]*?)\}/)
  return tokenTable(m ? m[1] : '')
}

/** 直出面尾注那条规则的声明正文；取不到回 ''（G5 负责把"取不到"报成红，而不是在这里静默合规） */
function goTaglineRule(): string {
  const raw = stripComments(read('../backend-go/internal/api/public.go'))
  const m = raw.match(/\.footer \.brand-tagline\s*\{([^}]*)\}/)
  return m ? m[1] : ''
}

/** 两张「维度 → 值」表做逐维等值，返回不一致的清单（空＝等值） */
function styleDrift(a: Record<string, string>, b: Record<string, string>): string[] {
  const keys = [...new Set([...Object.keys(a), ...Object.keys(b)])].sort()
  const out: string[] = []
  for (const k of keys) {
    const av = a[k]
    const bv = b[k]
    if (av === undefined) out.push(`${k}: 组件侧缺这条声明（直出面=${bv}）`)
    else if (bv === undefined) out.push(`${k}: 直出面缺这条声明（组件侧=${av}）`)
    else if (av.toLowerCase() !== bv.toLowerCase()) out.push(`${k}: 组件侧=${av} 直出面=${bv}`)
  }
  return out
}

/** 现行真实形态的等值差（G1/G4⑤ 的靶子） */
function taglineStyleDrift(): string[] {
  const rule = goTaglineRule()
  if (rule === '') return ['直出面取不到 .footer .brand-tagline 那条规则（样式腿不在＝这一整段只剩半条锁）']
  return styleDrift(cssDecls(reactTaglineCss(), reactTokens()), cssDecls(rule, goTokens()))
}

/**
 * 反证夹具：改/删某一侧的某一维，然后重跑**同一个**等值判据。
 * ⚠️ 找不到那一维就返回「夹具失效」而不是原样返回——
 *   空替换（new==old）的反证恒绿是本仓真踩过的形态：那时"红"来自夹具本身，抓不到被改坏的那一维。
 */
function patchDecl(block: string, prop: string, value: string | null): string | null {
  const parts = block
    .split(';')
    .map((p) => p.trim())
    .filter(Boolean)
  let hit = 0
  const out = parts
    .map((p) => {
      const m = p.match(/^([-\w]+)\s*:\s*([\s\S]+)$/)
      if (!m || kebab(m[1]) !== kebab(prop)) return p
      hit++
      return value === null ? '' : `${prop}:${value}`
    })
    .filter(Boolean)
  if (hit === 0) return null
  return out.join(';') + ';'
}

function withGoOverride(prop: string, value: string): string[] {
  const patched = patchDecl(goTaglineRule(), prop, value)
  if (patched === null) return ['反证夹具失效：那一侧的声明块里找不到目标维度（形态变了，请同步本段）']
  return styleDrift(cssDecls(reactTaglineCss(), reactTokens()), cssDecls(patched, goTokens()))
}

function withReactOverride(prop: string, value: string): string[] {
  const patched = patchDecl(reactTaglineCss(), prop, value)
  if (patched === null) return ['反证夹具失效：那一侧的声明块里找不到目标维度（形态变了，请同步本段）']
  return styleDrift(cssDecls(patched, reactTokens()), cssDecls(goTaglineRule(), goTokens()))
}

function withGoDrop(prop: string): string[] {
  const patched = patchDecl(goTaglineRule(), prop, null)
  if (patched === null) return ['反证夹具失效：那一侧的声明块里找不到目标维度（形态变了，请同步本段）']
  return styleDrift(cssDecls(reactTaglineCss(), reactTokens()), cssDecls(patched, goTokens()))
}

describe('页脚品宣尾注（2026-10-13 用户令②）', () => {
  it('A 五张渲染面逐面点名：每一面都在页脚位出这句（缺任一面即红）', () => {
    const bad: string[] = []
    const scanned: string[] = []
    for (const s of SURFACES) {
      const src = read(s.file)
      scanned.push(s.name)
      const why = taglineViolation(src, s.kind)
      if (why) bad.push(`${s.name}（${s.file}）：${why}`)
    }
    // 覆盖面条数本身要钉住：将来有人往清单里删一条（比如嫌后端面麻烦），
    // 单看「bad 为空」会绿——这条正锁把"扫到几张面"也变成判据。
    expect(scanned.length, '渲染面清单被删短了').toBe(5)
    expect(bad, `品宣尾注覆盖不合格：\n${bad.join('\n')}`).toEqual([])
  })

  it('B 等值锁：唯一事实源就是用户点名的那串字面量，五面逐字同源', () => {
    expect(BRAND_TAGLINE).toBe('Powered by LangCross AI')
    // 组件确实渲染这个常量（写成别的字符串＝常量漂了但界面没漂，或反之）
    const comp = read('src/components/BrandTagline.tsx')
    expect(comp).toContain(`export const BRAND_TAGLINE = '${BRAND_TAGLINE}'`)
    expect(comp).toContain('{BRAND_TAGLINE}')
    // 后端那份手抄必须逐字一致（不许多空格／不许改成「能言」汉字名）。
    // ★ 2026-10-14 用户后令「提亮＋重点提示」后，这句外面多了一层胶囊 span（样式见 G 段），
    //   靶子跟着现行形态走；G 段那枚"夹具空替换"防线会兜住"形态又变了而靶子没同步"。
    expect(read('../backend-go/internal/api/public.go')).toContain(
      `· <span class="brand-tagline">${BRAND_TAGLINE}</span><br>`,
    )
  })

  it('C 正向对照（D 段负向锁的前提）：i18n 词典**确实被读到**了', () => {
    // 全量词典里键的行数（"key':" 形态）——读到 0 行说明路径或写法变了，
    // 那 D 段的「词典里没有这句」就会恒真，属于锁失明。
    const de = read('src/i18n/locales/de.ts')
    const keyLines = de.split('\n').filter((l) => /^\s*['"][a-zA-Z][\w.]*['"]\s*:/.test(l)).length
    expect(keyLines, `德语词典只扫到 ${keyLines} 个键，判据射程可疑`).toBeGreaterThan(3000)
    expect(de).toContain('app.langBtn') // 随机点一把已知键，证明扫描真的走到了词典正文
  })

  it('D 负向锁：这句是品牌标识，**整个 i18n 目录里都不许出现**（十二语种一律同一串，禁止被本地化）', () => {
    // 递归扫 src/i18n 全部 .ts（中英在 dicts.*.ts + panels/*.ts，其余十语种在 locales/*.ts）——
    // 只扫 locales 会让中英面板成为下一条 PR 的落点，正是 082x 品牌名事故里「局部正确、整体打架」那一族。
    const files: string[] = []
    const walk = (dir: string) => {
      for (const e of readdirSync(dir, { withFileTypes: true })) {
        const p = resolve(dir, e.name)
        if (e.isDirectory()) walk(p)
        else if (e.name.endsWith('.ts')) files.push(p)
      }
    }
    walk(resolve(ROOT, 'src/i18n'))
    // 覆盖面条数正锁：扫到 0 个文件＝路径写错＝锁失明（与 A 段同一手法）
    expect(files.length, `src/i18n 只扫到 ${files.length} 份词典源，判据射程可疑`).toBeGreaterThanOrEqual(20)
    for (const f of files) {
      const src = readFileSync(f, 'utf-8')
      expect(src.includes(BRAND_TAGLINE), `${f} 里不该出现这句（应走 BrandTagline 固定字面量）`).toBe(false)
    }
  })

  it('E 反证：七种坏形态必须被抓，干净文本必须全绿（证明 A 段不是摆设）', () => {
    const reactClean = IMPORT_LINE + '<footer><BrandTagline /></footer>'
    // ① 某一面整条删掉（本仓最常见的漂移：改了四面漏了第五面）
    expect(taglineViolation('<footer>© 2026</footer>', 'react')).toContain('未引用')
    // ② 只留组件调用、import 被改名式破坏删掉
    expect(taglineViolation('<footer><BrandTagline /></footer>', 'react')).toContain('没有 import')
    // ③ 绕过事实源在本文件里手写一遍（将来改文案就会两边漂移）
    //    注意喂的是「组件在、又另抄一份」的形态：只缺组件那种由 ① 那条抓，
    //    这里要单独证明**这一条判据本身**有射程（本仓纪律：负向腿必须配一枚真打到它的反证）。
    expect(taglineViolation(IMPORT_LINE + `<footer><BrandTagline /><span>${BRAND_TAGLINE}</span></footer>`, 'react'))
      .toContain('唯一事实源')
    // ④ 后端面正文少了这句
    expect(taglineViolation('<div class="footer">© 2026 能言 LangCross</div>', 'go')).toContain('缺这句')
    // ⑤ 读到空文件＝锁失明，必须报红而不是"没扫到违规"
    expect(taglineViolation('', 'go')).toContain('锁失明')
    // ⑥ ★ 注释冒充实现（**本批实跑反证真踩到的一条**）：
    //    第一版锁只问"整份文件里有没有这句"，而我给后端页脚写的说明注释里引用了用户原话，
    //    于是把正文那句删掉之后 A 段照样判绿、只有 B 段红。判据必须吃剥掉注释后的正文。
    expect(
      taglineViolation(`<!-- 用户令：页脚写一条 ${BRAND_TAGLINE} -->\n<div class="footer">© 2026 能言 LangCross</div>`, 'go'),
    ).toContain('注释里有也不算')
    // ⑦ 同族的 React 侧：注释提了这句、正文没引组件 ⇒ 仍须判红（不许"看着像有"）
    expect(taglineViolation(`/* 页脚加 ${BRAND_TAGLINE} */\n<footer>© 2026</footer>`, 'react')).toContain('未引用')
    // ⑧ 页脚那段 div 的写法被改掉（类名换了/改成组件）⇒ 报"取不到＝锁失明"，不许静默合规
    expect(taglineViolation('<div class="site-footer">' + BRAND_TAGLINE + '</div>', 'go')).toContain('锁失明')
    // 对照组：合规形态必须全绿，否则上面八条只是恒红
    expect(taglineViolation(reactClean, 'react')).toBeNull()
    expect(taglineViolation(`<div class="footer">· ${BRAND_TAGLINE}</div>`, 'go')).toBeNull()
  })

  it('F 组件自带 RTL 隔离 + 不接租户品牌名：阿语界面里这句不许被双向算法重排、也不随白标改名', () => {
    const comp = read('src/components/BrandTagline.tsx')
    expect(comp).toContain('dir="ltr"')
    expect(comp).toContain("unicodeBidi: 'isolate'")
    // 白标纪律（口径②）：这一行是平台方品宣位，不接租户品牌名。
    // ⚠️ 判据刻意问「有没有把租户品牌那个 hook 引进来」（useBranding 是唯一的取品牌名入口），
    //    **不是** grep 源码里有没有 `brandName` 字样——本文件的说明注释里正写着"与 brandName 无关"，
    //    按字样判会把注释当违规（AGENTS/记忆里那条「静态负向锁误伤说明注释」的老坑）。
    expect(comp.includes('useBranding'), '品宣尾注不许取租户品牌名').toBe(false)
    expect(BRAND_TAGLINE.includes('{'), '尾注不许是模板插值形态（一旦有占位符就等于给了改名入口）').toBe(false)
  })

  // ==========================================================================
  // G 段（★ 2026-10-14 用户后令「这个页脚的句子能不能提亮并且做重点提示」）
  // —— 视觉档的跨语言等值锁。
  //
  // 为什么要单独一段：这一族的文案有 A/B 两段管着，而**样式**原先散在四个调用点各写一份
  // `fontWeight: 600`（第五面即后端直出，压根没有）。用户这次要改的正是样式，于是「改四面留一面」
  // 的风险从假设变成了必走路径。G 段把这一档收成两半：
  //   · 正向：组件那份 TAGLINE_STYLE 与后端那条 `.footer .brand-tagline{…}` **逐维等值**，
  //     且等值比较吃的是 **var() 解析后的值**（不是源码里的令牌名）——
  //     两侧本来就叫不同名字（前端 `--lc-fill-white`／直出面 `--lc-white`），
  //     按名字比会红、按名字**不**比则会把「名字照抄、值没落地」那类静默失效放过去
  //     （直出面 :root 里根本没有 --lc-fill-white：写成那个名字时 var() 解不开 ⇒ 面色退回
  //     transparent、字色退回继承灰，界面照常渲染、零报错）。
  //   · 负向：四面调用**不许再带 style**（样式只许有组件那一个出处）。
  //
  // ⚠️ 这是**等值锁**，不是「只许更亮／更大」的单向锁（AGENTS §一·5「界面规格＝UI 交付真值」）：
  //    真值来自 UI-ANNOTATIONS §2.3 的尾注档，四个维度全部取交付既有令牌、零新增色值。
  //    要改这一档先改那份文档真值，再同步本段与 readability.test.ts G 段。
  // ==========================================================================

  it('G1 视觉档跨语言逐维等值：组件那份与后端那条规则，解析后每一维都相同', () => {
    const drift = taglineStyleDrift()
    expect(drift, `React 组件样式与后端直出面样式不一致：\n${drift.join('\n')}`).toEqual([])
    // 射程正锁：两边都至少解析出这八个维度。少一维＝某一侧被摘掉声明，
    // 而"缺声明"在只做 Object.keys 并集的写法里可能被"两侧都缺"蒙过（同 A 段数覆盖面条数的手法）。
    const react = cssDecls(reactTaglineCss(), reactTokens())
    expect(Object.keys(react).sort()).toEqual(TAGLINE_DIMENSIONS.slice().sort())
    expect(undeclaredVarsIn(reactTaglineCss(), reactTokens()).length, `组件样式里有令牌在 tokens.css 解析不到：${undeclaredVarsIn(reactTaglineCss(), reactTokens()).join(', ')}`).toBe(0)
  })

  it('G2 这一档的交付真值逐维点名（白底反相胶囊徽标；禁单向"更亮/更大"锁）', () => {
    const react = cssDecls(reactTaglineCss(), reactTokens())
    // 面色＝实心白填充档（#FFFFFF，**不是**文字档 #E7E9EA——白色两档不许混用那条）
    expect(react['background'].toUpperCase()).toBe('#FFFFFF')
    expect(react['color']).toBe('#000000') // 白底必须配反相黑字
    expect(react['font-weight']).toBe('600')
    expect(react['border-radius']).toBe('999px')
    expect(react['padding']).toBe('1px 8px')
    expect(react['display']).toBe('inline-block')
    expect(react['white-space']).toBe('nowrap')
    expect(react['unicode-bidi']).toBe('isolate')
    // ★ 字号／行高**刻意不设**：五张面的页脚字号档本就不同（全站 15px／报价·对照 14px），
    //   统一抬档属未授权的规格改动。这一条是负向腿，必须有正向对照（下面那句"其他维度在位"）才不是空转。
    expect(react['font-size'], '尾注不许自带字号档').toBeUndefined()
    expect(react['line-height'], '尾注不许自带行高档').toBeUndefined()
  })

  it('G3 负向锁：四面调用一律不带 style（样式只许组件那一个出处）', () => {
    const offenders: string[] = []
    for (const s of SURFACES.filter((x) => x.kind === 'react')) {
      const src = stripComments(read(s.file))
      // 夹具射程正锁：这一面必须真的用到组件，否则下面的循环是空转、"没违规"是假的
      const calls = [...src.matchAll(/<BrandTagline\b[^>]*>/g)]
      expect(calls.length, `${s.name} 里一个 <BrandTagline 调用都没扫到＝判据射程为空（锁失明）`).toBeGreaterThan(0)
      for (const c of calls) {
        if (/\bstyle\s*=/.test(c[0])) offenders.push(`${s.file}：${c[0].trim()}`)
      }
    }
    expect(offenders, `页脚尾注的样式只许写在 BrandTagline.tsx 的 TAGLINE_STYLE 里：\n${offenders.join('\n')}`).toEqual([])
  })

  it('G4 反证：只改一侧、两侧同改成新值、摘掉声明、令牌名照抄但值没落地四种形态都要被抓', () => {
    // ① 只改后端那一侧（用户/后人"顺手"把直出面字重抬成 700）⇒ 必须红，且点名那一维
    const goOnly = withGoOverride('font-weight', '700')
    expect(goOnly.some((d) => d.includes('font-weight')), `只改直出面字重却没红：${JSON.stringify(goOnly)}`).toBe(true)
    // ② 只改组件那一侧（改前端忘了后端）⇒ 同样必须红
    const reactOnly = withReactOverride('background', 'var(--lc-text)')
    expect(reactOnly.some((d) => d.includes('background')), `只改组件面色却没红：${JSON.stringify(reactOnly)}`).toBe(true)
    // ③ 摘掉一侧的一整个维度（改成"由页脚继承"）⇒ 必须报"某一侧缺这一维"，不许静默少比一维
    const dropped = withGoDrop('border-radius')
    expect(dropped.some((d) => d.includes('border-radius') && d.includes('直出面缺')), `摘掉直出面圆角却没红：${JSON.stringify(dropped)}`).toBe(true)
    // ④ ★ 本段存在的理由：**令牌名照抄、值没落地**。
    //    把直出面写成前端那个名字（--lc-fill-white），而那一页的 :root 只有 --lc-white ⇒
    //    var() 解不开、面色退回 transparent、字色退回继承灰，界面照常、零报错、两侧"名字"还完全一致。
    //    按名字比的锁在这里恒绿；本锁按解析后的值比 ⇒ 一侧解出 #FFFFFF、另一侧解不出 ⇒ 必须红。
    const nameOnlyCopied = withGoOverride('background', 'var(--lc-fill-white)')
    expect(nameOnlyCopied.some((d) => d.includes('background')), `直出面抄了前端令牌名（该页 :root 没有它）却没红：${JSON.stringify(nameOnlyCopied)}`).toBe(true)
    // ⑤ 夹具自证：上面四枚反证走的都是同一个判据函数，所以必须先证明**当前真实形态喂进去是绿的**
    //    （否则"红"可能来自夹具本身坏了，而不是来自被改坏的那一维——本仓「反证恒红＝无射程」那一族）。
    expect(taglineStyleDrift(), `真实形态在本判据下就不绿，反证全失去意义（先看 G1 为什么红）`).toEqual([])
  })

  it('G5 后端那条规则的在位性与唯一性（直出面样式没了＝本段只剩半条锁）', () => {
    const raw = stripComments(read('../backend-go/internal/api/public.go'))
    const rules = [...raw.matchAll(/\.footer \.brand-tagline\{([^}]*)\}/g)]
    // 恰好一条：0 条＝规则被删/类名改了（B 段的 markup 与这里的类名是同一靶子，注释里已点名）；
    // >1 条＝有人又抄了一份，那"唯一出处"就又不成立了。
    expect(rules.length, `直出面里 .footer .brand-tagline 规则应恰好 1 条，实得 ${rules.length} 条`).toBe(1)
    const goBlock = rules[0][1]
    const go = cssDecls(goBlock, goTokens())
    // 直出面那一侧的**维度覆盖数**也钉住：G1 只比"两边相同的维度等不等"，
    // 两侧同时缺某一维时它是绿的；这一句保证八个维度在直出面侧一个都不能少。
    expect(Object.keys(go).sort(), '直出面尾注规则的维度集合变了').toEqual(TAGLINE_DIMENSIONS.slice().sort())
    expect(undeclaredVarsIn(goBlock, goTokens()).length, `直出面引用了本页 :root 没声明的令牌：${undeclaredVarsIn(goBlock, goTokens()).join(', ')}`).toBe(0)
    // 组件那份必须被页脚 markup 真正挂上：类名在、span 没套类名＝规则成了死码而界面仍是灰字
    expect(raw, '直出面页脚那句 span 必须带 brand-tagline 类，否则那条 CSS 规则是死码').toContain('<span class="brand-tagline">')
  })
})
