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
// 运行：npx vitest run src/components/BrandTagline.lock.test.ts
// ============================================================================
import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { BRAND_TAGLINE } from './BrandTagline'

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
    // 后端那份手抄必须逐字一致（不许多空格／不许改成「能言」汉字名）
    expect(read('../backend-go/internal/api/public.go')).toContain(`· ${BRAND_TAGLINE}<br>`)
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
})
