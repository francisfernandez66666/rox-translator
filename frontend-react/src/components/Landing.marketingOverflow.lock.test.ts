// ============================================================================
// src/components/Landing.marketingOverflow.lock.test.ts — 官网营销骨架「窄档不撑宽文档」口径锁
// （★ 2026-10-16 用户报障续修：手机档首页整页横向滚动 + 641~760 档文件直出舞台把译文 chip 顶出容器右缘）
//
// 三处缺陷同族、根因不同，各钉各的：
//   ① 导航（.lc-mkt-nav）：一行装不下时 flex 默认**不换行**，于是多出来的宽把**整份文档**撑宽——
//      zh 的 min-content 是 473~497（brand 189 ＋ LangSelect 107 ＋ 登录 17 ＋ 试用 88/112 ＋间距），
//      iPhone SE 375/360/320 全在它下面；实测 360 视口 documentElement.scrollWidth=473、320 视口 473。
//      修法＝只在**锚点已隐藏的窄档**（≤980）允许换行（`margin-left:auto` 的右簇整行落到第二行并右对齐），
//      **>980 的交付单行 76px 形态一字不动**。
//   ①b 锚点组 981~1423「装不下」（★ 2026-10-17 用户裁定，方案A 被否后反转）：锚点组自然宽 709px（文本 493
//      ＋gap36×6），budget＝视口−内垫−brand189−右簇293−gap36×2 ⇒ 1423 起才放得下（1366 缺 57、1280 缺 143、
//      1024 缺 319）。第一版把锚点整段藏到 ≤1439，用户当场否掉（"页眉原来的按钮怎么都删了"）⇒ 反转成：
//      **锚点必须还在、且不被压扁**——1181~1439 用「收间距＋收字号」(gap 36→20/12/6、字号 17→16/15/14，
//      每档按 budget 现算留 ≥14px 余量) 把锚点压进一行；≤1180 连最紧档也放不下才隐藏；≥1440 交付原样。
//      CTA（语言/登录/试用）任何宽度一个不藏。
//   ② 文件直出舞台（.lc-fd-visual/.lc-fd-stage）：横排管线的**最小内容宽 680px**（chip 196×2 ＋ 引擎 96
//      ＋ 链路 min 48×2 ＋ gap 24×4），而舞台拿到的宽度由段落内垫决定（≤1200 档左右各 40）——
//      与视口不成正比。旧形态把"什么时候竖排"写死成 `@media (max-width:640px)`，于是 641~760 视口里
//      横排被硬塞：实测 700 视口容器只有 620，译文 chip 右缘 720 ⇒ 越出容器 60px、整页横滚 27px；
//      641 视口横滚 86px。修法＝容器自己声明 `container-type:inline-size`，竖排判据改吃 `@container`。
//
// 本文件钉住的（缺一条锁就是空的）：
//   N1 交付形态：基础规则仍是 `height:76px` 单行、**不带** flex-wrap（宽档不许被顺手改掉）。
//   N2 窄档换行：存在一条 max ≤ 980 的媒体块，其 `.lc-mkt-nav` 同时有 flex-wrap:wrap ＋ height:auto ＋ min-height。
//   N3 换行不许全球化：任何 max > 980 的块里都不许出现 `flex-wrap:wrap`（那会把 1024/1280 的导航变成两行）。
//   N4 右簇定位：`.lc-nav-cta` 必须保留 margin-left:auto（换行后靠它右对齐；丢了＝第二行缩在左边）。
//   N5 容器腿：`.lc-fd-visual` 必须声明 container-type:inline-size（否则 @container 不成立，退回视口解析）。
//   N6 ★射程自证＋等值推导：竖排门限必须**由同文件里的尺寸现算**——舞台横排最小内容宽
//      （chip 下限×2 ＋ 引擎 ＋ 链路下限×2 ＋ gap×4）＋ 20px 余量。改其中任一尺寸而不抬门限 ⇒ 当场红。
//   N7 竖排脚本体：@container 块里必须有 flex-direction:column（＋窄档 chip 宽度覆盖）。
//   N8 负向（根因）：不许再有"媒体块里把 .lc-fd-stage 改成 column"的写法（退回视口基准是本缺陷的根）。
//   N9 锚点隐藏档：display:none 落在 maxW∈[1100,1200] 的块里（保笔电宽度 1280/1366 可见），更高的块不许隐藏。
//   N10 锚点压缩档：≥1 处在 [1200,1440] 的块给 `.lc-nav-links` 收 gap/font（否则锚点被 flex 压扁）；
//      且字号 ≥13 地板、间距 ≥6 地板、maxW>1439 的块不许压缩（交付宽度原样）。
//   R1~R5 反证：摘 flex-wrap / 摘 container-type / 门限压回 640 / 摘隐藏兜底 / 摘压缩档，五条各被对应判据抓到。
//
// ⚠️ 判据只问 CSS 段本体：说明注释里刻意写着这些字面（AGENTS 要求写理由），所以扫描前先剥注释。
// 运行：npx vitest run src/components/Landing.marketingOverflow.lock.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const LANDING = path.join(HERE, 'Landing.tsx')

// 剥注释（先块后行）：CSS 模板串里只有 /* */ 一种，保守剥法够用；剥过头会让负向锁"显得更干净"＝假绿方向。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

// 切 LANDING_CSS 模板串本体：从 `const LANDING_CSS = \`` 起，到 CSS 收尾的最后一个反引号止。
function cssBlock(src: string): string {
  const start = src.indexOf('const LANDING_CSS = `')
  const end = src.lastIndexOf('\n`')
  if (start < 0 || end <= start) return ''
  return stripComments(src.slice(start + 'const LANDING_CSS = `'.length, end))
}

// 按「@media (max-width:Npx){ … 顶格 }」/「@container (max-width:Npx){ … }」把 CSS 切成 section
// （本仓块就是「块首一行 + 顶格 } 收尾」；切不出媒体块时 sections 只剩 base，N2/N3/N8 会判红）。
// ⚠️ 两种块都要认：@container 是修法本体（竖排吃容器宽），@media 是负向判据的靶子（退回视口基准要红）。
type Section = { kind: 'base' | 'media' | 'container'; cond: string; maxW: number | null; body: string[] }
function sections(css: string): Section[] {
  const out: Section[] = [{ kind: 'base', cond: 'base', maxW: null, body: [] }]
  for (const raw of css.split('\n')) {
    const line = raw.trim()
    const m = /^@(media|container) \(max-width:(\d+)px\)\{$/.exec(line)
    if (m) { out.push({ kind: m[1] as 'media' | 'container', cond: line, maxW: Number(m[2]), body: [] }); continue }
    if (line === '}' && out[out.length - 1].kind !== 'base') {
      out.push({ kind: 'base', cond: 'base-after', maxW: null, body: [] }); continue
    }
    out[out.length - 1].body.push(line)
  }
  return out
}

// 段内某选择器的声明块行（本仓 CSS 一条规则一行）。
const ruleLine = (body: string[], sel: string): string => body.find((l) => l.startsWith(`${sel}{`)) ?? ''

// 取声明值：`prop:value` 到分号/右括号为止。
function decl(line: string, prop: string): string | null {
  const m = new RegExp(`(?:^|[;{])${prop.replace(/[-]/g, '\\-')}:([^;}]+)`).exec(line)
  return m ? m[1].trim() : null
}

const px = (v: string | null): number => (v ? parseFloat(v) : NaN)

// 舞台横排最小内容宽：chip 下限×2 ＋ 引擎 ＋ 链路 min×2 ＋ gap×4（全部从同文件现读，不写死第二份）。
function fdMinContentWidth(css: string): number {
  const secs = sections(css)
  const all = secs.flatMap((s) => s.body)
  const chip = all.find((l) => l.startsWith('.lc-fd-chip{')) ?? ''
  const clamp = /width:clamp\((\d+)px,/.exec(chip)
  const engLine = all.find((l) => l.startsWith('.lc-fd-eng{')) ?? ''
  const linkLine = all.find((l) => l.startsWith('.lc-fd-link{')) ?? ''
  const stageLine = all.find((l) => l.startsWith('.lc-fd-stage{')) ?? ''
  const chipMin = clamp ? Number(clamp[1]) : NaN
  const eng = px(decl(engLine, 'width'))
  const linkMin = px(decl(linkLine, 'min-width'))
  const gap = px(decl(stageLine, 'gap'))
  expect([chipMin, eng, linkMin, gap].every((n) => Number.isFinite(n)),
    `舞台不可换行时的最小内容宽算不出来（锚点漂了、N6 会静默空转）：chip=${chipMin} eng=${eng} link=${linkMin} gap=${gap}`).toBe(true)
  return chipMin * 2 + eng + linkMin * 2 + gap * 4
}

// 竖排门限：@container (max-width:Npx) 里的 N。
function containerThreshold(css: string): { n: number; block: string[] } {
  const secs = sections(css)
  const idx = secs.findIndex((s) => s.kind === 'container')
  if (idx < 0) return { n: NaN, block: [] }
  return { n: secs[idx].maxW ?? NaN, block: secs[idx].body }
}

describe('官网营销骨架：窄档不撑宽文档（导航换行 + 文件直出舞台竖排）', () => {
  const raw = readFileSync(LANDING, 'utf8')
  const css = cssBlock(raw)

  it('P1 射程自证：LANDING_CSS 段与各档媒体块必须切得到（切不到＝锚点漂了，下面几条会静默空转）', () => {
    expect(css.length, 'LANDING_CSS 段过短，判据已失明').toBeGreaterThan(8000)
    expect(css).toContain('.lc-mkt-nav{')
    expect(css).toContain('.lc-fd-visual{')
    const secs = sections(css)
    expect(secs.filter((s) => s.maxW === 980).length, '切不出 ≤980 媒体块（换行档的宿主）').toBe(1)
    expect(secs.filter((s) => s.maxW === 1200).length, '切不出 ≤1200 媒体块（宽度分档的宿主）').toBe(1)
    expect(secs.filter((s) => s.maxW === 1439).length, '切不出 ≤1439 媒体块（锚点压缩档①的宿主）').toBe(1)
    expect(secs.filter((s) => s.maxW === 1360).length, '切不出 ≤1360 媒体块（锚点压缩档②的宿主）').toBe(1)
    expect(secs.filter((s) => s.maxW === 1260).length, '切不出 ≤1260 媒体块（锚点压缩档③的宿主）').toBe(1)
    expect(secs.filter((s) => s.maxW === 1180).length, '切不出 ≤1180 媒体块（锚点隐藏档的宿主）').toBe(1)
  })

  it('N1 交付形态：基础规则的 .lc-mkt-nav 仍是单行 height:76px 且不带 flex-wrap（>980 档不许被改）', () => {
    const base = sections(css).find((s) => s.cond === 'base')!
    const line = ruleLine(base.body, '.lc-mkt-nav')
    expect(line, '基础 .lc-mkt-nav 规则不见了').not.toBe('')
    expect(decl(line, 'height'), '交付稿的单行导航高度被改了').toBe('76px')
    expect(decl(line, 'flex-wrap'), '宽档导航被改成可换行 ⇒ 1024/1280 会变两行，与交付形态不符').toBeNull()
  })

  it('N2 窄档换行：≤980 块里 .lc-mkt-nav 同时有 flex-wrap:wrap ＋ height:auto ＋ min-height（定高会把第二行裁掉）', () => {
    const sec = sections(css).find((s) => s.maxW === 980)!
    const line = ruleLine(sec.body, '.lc-mkt-nav')
    expect(line, '≤980 档没有给导航开放换行 ⇒ 手机档首页仍会被撑宽').not.toBe('')
    expect(decl(line, 'flex-wrap'), '缺 flex-wrap:wrap').toBe('wrap')
    expect(decl(line, 'height'), '缺 height:auto（定高会裁掉第二行）').toBe('auto')
    expect(decl(line, 'min-height'), '缺 min-height（单行档要与旧形态同高）').toBe('76px')
  })

  it('N3 换行不许全球化：任何 max > 980 的块里都不许出现 flex-wrap:wrap', () => {
    const bad = sections(css)
      .filter((s) => s.kind === 'media' && s.maxW !== null && s.maxW > 980)
      .filter((s) => ruleLine(s.body, '.lc-mkt-nav').includes('flex-wrap:wrap'))
      .map((s) => s.cond)
    expect(bad, `这些块里导航被改成换行 ⇒ 桌面/平板交付单行形态被改：${bad.join(', ')}`).toEqual([])
  })

  it('N4 右簇定位：.lc-nav-cta 保留 margin-left:auto（换行后靠它右对齐）', () => {
    const all = sections(css).flatMap((s) => s.body)
    const line = all.find((l) => l.startsWith('.lc-nav-cta{')) ?? ''
    expect(line, '.lc-nav-cta 基础规则不见了').not.toBe('')
    expect(decl(line, 'margin-left'), '右簇的 margin-left:auto 被摘 ⇒ 换行后第二行会缩在左边').toBe('auto')
  })

  it('N5 容器腿：.lc-fd-visual 声明 container-type:inline-size（否则 @container 不成立）', () => {
    const all = sections(css).flatMap((s) => s.body)
    const line = all.find((l) => l.startsWith('.lc-fd-visual{')) ?? ''
    expect(line, '.lc-fd-visual 规则不见了').not.toBe('')
    expect(decl(line, 'container-type'), '.lc-fd-visual 未声明容器，竖排判据会退回视口基准（本缺陷的根）').toBe('inline-size')
  })

  it('N6 ★门限由同文件尺寸现算：竖排门限 ≥ 舞台最小内容宽 ＋ 20px 余量', () => {
    const minW = fdMinContentWidth(css)
    const { n } = containerThreshold(css)
    expect(Number.isFinite(n), '找不到 @container (max-width:Npx) 竖排块').toBe(true)
    expect(n, `竖排门限 ${n}px 低于舞台最小内容宽 ${minW}px ＋ 20px 余量 ⇒ 641~${minW} 那一段仍会把 chip 顶出容器（实测 700 视口越界 60px）`)
      .toBeGreaterThanOrEqual(minW + 20)
  })

  it('N7 竖排脚本体：@container 块里必须有 column 重排（＋窄档 chip 宽度覆盖）', () => {
    const { block } = containerThreshold(css)
    const stage = ruleLine(block, '.lc-fd-stage')
    expect(stage, '@container 块里没有 .lc-fd-stage 重排').not.toBe('')
    expect(decl(stage, 'flex-direction'), '竖排块里舞台没改成 column').toBe('column')
    const chip = ruleLine(block, '.lc-fd-chip')
    expect(chip, '竖排块里缺 chip 宽度覆盖（竖排时 chip 仍吃横排的 clamp 下限）').toContain('width:min(230px,76vw)')
    expect(ruleLine(block, '.lc-fd-link'), '竖排块里链路没改成纵向短链').toContain('width:1.5px')
  })

  it('N8 负向（根因）：不许再有"媒体块里把 .lc-fd-stage 改成 column"的视口基准写法', () => {
    const bad = sections(css)
      .filter((s) => s.kind === 'media')
      .filter((s) => ruleLine(s.body, '.lc-fd-stage').includes('flex-direction:column'))
      .map((s) => s.cond)
    expect(bad, `竖排又被写回视口媒体块（与视口不成正比 ⇒ 641~760 必然溢出）：${bad.join(', ')}`).toEqual([])
  })

  it('N9 锚点隐藏档：display:none 落在 maxW∈[1100,1200] 的块里（保 1280/1366 可见），更高宽的块不许隐藏', () => {
    const hide = (sec: Section) => ruleLine(sec.body, '.lc-nav-links').includes('display:none')
    const hiding = sections(css).filter((s) => s.kind === 'media' && hide(s))
    expect(hiding.length, '锚点隐藏兜底不见了（≤1180 连压缩档都放不下，必须留一处隐藏，否则整页溢出）').toBeGreaterThan(0)
    const maxHide = Math.max(...hiding.map((s) => s.maxW ?? 0))
    expect(maxHide, `锚点隐藏阈值 ${maxHide}px 太高——锚点必须回到 1280/1366 这些笔电宽度（只允许 ≤1200 隐藏）`)
      .toBeLessThanOrEqual(1200)
    expect(maxHide, `锚点隐藏阈值 ${maxHide}px 太低——低于 1100 时压缩档也放不下，缺隐藏兜底会整页溢出`)
      .toBeGreaterThanOrEqual(1100)
    const over = sections(css).filter((s) => s.kind === 'media' && s.maxW !== null && s.maxW > maxHide && hide(s))
    expect(over.map((s) => s.cond), `比 ${maxHide}px 更宽的块里还藏着锚点（交付宽度不该隐藏）`).toEqual([])
  })

  it('N10 锚点压缩档：≥1 处在 [1200,1440] 收 gap/font；字号 ≥13、间距 ≥6、maxW>1439 不许压缩', () => {
    const compacts = sections(css)
      .filter((s) => s.kind === 'media')
      .map((s) => ({ sec: s, line: ruleLine(s.body, '.lc-nav-links') }))
      .filter(({ line }) => /gap:|font-size:/.test(line))
    expect(compacts.length, '没有任何压缩档 ⇒ 锚点在 1181~1439 会被 flex 压扁（本缺陷的原始形态：文字折两行）')
      .toBeGreaterThan(0)
    expect(compacts.filter(({ sec }) => (sec.maxW ?? 0) >= 1200 && (sec.maxW ?? 0) <= 1440).length,
      '压缩档不在 [1200,1440] 区间（要么没盖住笔电宽度、要么越界动了交付宽度）').toBeGreaterThan(0)
    for (const { sec, line } of compacts) {
      expect(sec.maxW ?? 0, `maxW=${sec.maxW} 的块压缩了锚点 ⇒ ≥1440 交付宽度必须原样 gap36/font17`)
        .toBeLessThanOrEqual(1439)
      const fs = px(decl(line, 'font-size'))
      if (fs) expect(fs, `${sec.cond} 字号 ${fs}px < 13px 地板（锚点会读不清）`).toBeGreaterThanOrEqual(13)
      const g = px(decl(line, 'gap'))
      if (g) expect(g, `${sec.cond} 间距 ${g}px < 6px 地板（锚点会贴成一坨）`).toBeGreaterThanOrEqual(6)
    }
  })

  it('R1 反证：摘掉 ≤980 的 flex-wrap:wrap 必须被 N2 抓到', () => {
    const sec = sections(css).find((s) => s.maxW === 980)!
    const line = ruleLine(sec.body, '.lc-mkt-nav')
    const mutated = css.replace(line, line.replace('flex-wrap:wrap;', ''))
    expect(mutated, '变异体没落地（R1 是空替换）').not.toBe(css)
    const msec = sections(mutated).find((s) => s.maxW === 980)!
    expect(decl(ruleLine(msec.body, '.lc-mkt-nav'), 'flex-wrap'), '摘掉换行却没被抓到').not.toBe('wrap')
  })

  it('R2 反证：摘掉 .lc-fd-visual 的 container-type 必须被 N5 抓到', () => {
    const mutated = css.replace('width:100%;container-type:inline-size}', 'width:100%}')
    expect(mutated, '变异体没落地（R2 是空替换）').not.toBe(css)
    const all = sections(mutated).flatMap((s) => s.body)
    const line = all.find((l) => l.startsWith('.lc-fd-visual{')) ?? ''
    expect(decl(line, 'container-type'), '摘掉容器声明却没被抓到').not.toBe('inline-size')
  })

  it('R3 反证：把竖排门限压回 640 必须被 N6 抓到（本缺陷的原始写法）', () => {
    const minW = fdMinContentWidth(css)
    const { n } = containerThreshold(css)
    const mutated = css.replace(`@container (max-width:${n}px){`, '@container (max-width:640px){')
    expect(mutated, '变异体没落地（R3 是空替换）').not.toBe(css)
    const m = containerThreshold(mutated)
    expect(m.n, `门限压到 ${m.n}px 却没被发现（舞台最小内容宽 ${minW}px）`).toBeLessThan(minW + 20)
  })

  it('R4 反证：把锚点隐藏兜底摘掉必须被 N9 抓到（更低档会整页溢出）', () => {
    const hide = (sec: Section) => ruleLine(sec.body, '.lc-nav-links').includes('display:none')
    const sec = sections(css).find((s) => s.kind === 'media' && s.maxW === 1180)!
    const mutated = css.replace(ruleLine(sec.body, '.lc-nav-links'), '')
    expect(mutated, '变异体没落地（R4 是空替换）').not.toBe(css)
    const msec = sections(mutated).filter((s) => s.kind === 'media' && hide(s))
    expect(msec.length, '摘掉隐藏兜底却没被发现（≤1180 会整页溢出）').toBe(0)
  })

  it('R5 反证：把三段压缩全摘掉（退回被压扁形态）必须被 N10 抓到', () => {
    let mutated = css
    for (const w of [1439, 1360, 1260]) {
      const sec = sections(mutated).find((s) => s.kind === 'media' && s.maxW === w)!
      mutated = mutated.replace(ruleLine(sec.body, '.lc-nav-links'), '')
    }
    expect(mutated, '变异体没落地（R5 是空替换）').not.toBe(css)
    const compacts = sections(mutated)
      .filter((s) => s.kind === 'media')
      .map((s) => ruleLine(s.body, '.lc-nav-links'))
      .filter((l) => /gap:|font-size:/.test(l))
    expect(compacts.length, '三段压缩都摘掉了却没被发现（1181~1439 会被压扁）').toBe(0)
  })
})
