// ============================================================================
// src/bootStorageGate.test.ts — 启动链静态锁（★ 2026-10-12「标题正常、整页黑」批次建立）
//
// 锁两件事，都是同一类故障的机械防线：**入口包评估中断 ⇒ 全站不挂载 ⇒ 纯黑屏零提示**。
//
//  A) 模块顶层不许有裸的存储读写。
//     浏览器把站点数据/Cookie 按策略拒绝时（Edge·Chrome「阻止所有 Cookie」档，隐私模式同样命中），
//     取 `localStorage`／`sessionStorage` 属性当场抛 SecurityError。这类调用一旦落在**模块顶层**
//     （不在函数体里、不在 try 里），异常就发生在入口 chunk 的评估阶段——
//     React 根本没来得及挂载，屏幕上只剩背景色与正确的标签标题，界面上一个字都没有。
//     判据形状＝「该行第一个非空白字符在第 0 列（＝模块顶层）＋ 直接摸存储」，
//     函数体里的读写（缩进行）与 `try { … } catch` 一行包裹都在射程外，因此这条锁**不限制**
//     正常业务代码用存储，只堵「顶层裸读」这一种形态。
//     现网实证的两处（src/i18n/index.ts 的 app_lang 顶层读、src/api/core.ts 的两条 sessionStorage
//     顶层读）已改为「取不到就按默认档起步」，本锁钉住它们不许回改。
//
//  B) index.html 必须有「脚本没起来就出人话」的启动哨兵。
//     哨兵是普通内联脚本（不是 type=module），所以它不受入口包失败影响；
//     #root 一直空着才显示，界面正常起来即自己收摊。没有它，下一次同类故障的表现仍然是黑屏。
//  C) 挂载**之后**再抛异常时哨兵已经收摊 ⇒ main.tsx 必须自带一层 ErrorBoundary。
//
// 反证六枚 + 对照腿在末尾：负向锁（零命中）必须配「往那一侧塞个假目标立刻报缺」，
// 否则「扫描器根本没扫到东西」和「确实干净」在屏幕上长得一模一样。
// ============================================================================
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// 包根用 URL 解析（同 src/styles/readability.test.ts 口径）：jsdom/node 两种环境下 cwd 都可能不是包根，
// 拿相对路径读源文件会让闸门「读不到文件」而静默失效。
const ROOT = fileURLToPath(new URL('../', import.meta.url))

// walkSrc 枚举 src 下的 ts/tsx：排除测试自身与十份语种词典（词典是纯数据，不碰浏览器 API）。
function walkSrc(dir: string, acc: string[] = []): string[] {
  for (const e of readdirSync(ROOT + dir)) {
    const rel = `${dir}/${e}`
    if (statSync(ROOT + rel).isDirectory()) {
      walkSrc(rel, acc)
      continue
    }
    if (/\.(tsx|ts)$/.test(rel) && !/\.test\.|\/locales\//.test(rel)) acc.push(rel)
  }
  return acc
}

// 顶层裸存储访问判据：行首无缩进（模块顶层）＋ 摸 storage + 不是 try 一行包裹。
// 注释行单独放行（本文件与改动点的说明注释里必然出现这些词，负向锁误伤注释是长期红灯的来源）。
const STORAGE_TOUCH = /(localStorage|sessionStorage)\s*[.[\]]/
function topLevelStorageHits(src: string): number[] {
  const hits: number[] = []
  src.split('\n').forEach((l, i) => {
    if (/^\s/.test(l)) return // 缩进行＝在函数体／try 块里，属射程外
    const t = l.trim()
    if (t.startsWith('//') || t.startsWith('*') || t.startsWith('/*')) return // 注释行
    if (!STORAGE_TOUCH.test(t)) return
    if (t.includes('try {')) return // 一行式 try 包裹＝本批的修法形态
    hits.push(i + 1)
  })
  return hits
}

describe('启动链锁 A：模块顶层零裸存储访问', () => {
  const files = walkSrc('src')

  it('射程自检：src 下枚举到的源文件数不离谱（枚举失效＝闸门空转）', () => {
    expect(files.length).toBeGreaterThan(120)
  })

  it('全站源码零「顶层裸读存储」（浏览器拒站点数据时抛 SecurityError 会黑屏）', () => {
    const offenders: string[] = []
    for (const f of files) {
      const hs = topLevelStorageHits(readFileSync(ROOT + f, 'utf-8'))
      for (const ln of hs) offenders.push(`${f}:${ln}`)
    }
    expect(offenders, '这些行把存储访问写在了模块顶层，请挪进 try 或函数内：\n' + offenders.join('\n')).toEqual([])
  })

  it('本批修好的两处保持修法形态：i18n 的 app_lang 读、api/core 的两条会话态读都带 try', () => {
    const i18n = readFileSync(ROOT + 'src/i18n/index.ts', 'utf-8')
    expect(i18n).toMatch(/try\s*\{\s*storedRaw\s*=\s*localStorage\.getItem\('app_lang'\)\s*\}\s*catch/)
    const core = readFileSync(ROOT + 'src/api/core.ts', 'utf-8')
    // 两条读现在在同一个 try 块里：先要求「顶层直接赋值」那两种旧形态都不存在，再要求 try 块里有这两次读
    expect(core).not.toMatch(/^let authToken = sessionStorage\.getItem/m)
    expect(core).not.toMatch(/^let activeTenantId = Number\(sessionStorage/m)
    expect(core).toMatch(/try\s*\{[\s\S]{0,200}sessionStorage\.getItem\('auth_token'\)[\s\S]{0,200}sessionStorage\.getItem\(TENANT_KEY\)/)
  })

  // ---- 反证四枚：判据必须真的抓得住那一族形态 ----
  it('反证 1：顶层 const = localStorage.getItem 必须报缺', () => {
    expect(topLevelStorageHits("const stored = localStorage.getItem('app_lang')")).toEqual([1])
  })
  it('反证 2：顶层 let = sessionStorage.getItem 必须报缺', () => {
    expect(topLevelStorageHits("let authToken = sessionStorage.getItem('auth_token') || ''")).toEqual([1])
  })
  it('反证 3：顶层裸写/裸清同样算越界', () => {
    expect(topLevelStorageHits("localStorage.setItem('app_lang', 'zh')")).toEqual([1])
    expect(topLevelStorageHits('sessionStorage.clear()')).toEqual([1])
  })
  it('反证 4：行号要一起报出来（多行源码不能只回「有」）', () => {
    const src = ["const a = 1", "let b = localStorage.getItem('x')", 'function f() {', "  localStorage.setItem('y', '1')", '}'].join('\n')
    expect(topLevelStorageHits(src)).toEqual([2]) // 缩进那次是函数体内＝放行
  })
  it('对照腿：try 一行包裹与注释行都不报（否则锁会误伤修法与本说明）', () => {
    expect(topLevelStorageHits("try { storedRaw = localStorage.getItem('app_lang') } catch { /* ignore */ }")).toEqual([])
    expect(topLevelStorageHits("// 这里提到 localStorage.getItem 只是说明文字")).toEqual([])
    expect(topLevelStorageHits("  const v = localStorage.getItem('in-function')")).toEqual([])
  })
})

describe('启动链锁 B：index.html 的启动哨兵在位', () => {
  const html = readFileSync(ROOT + 'index.html', 'utf-8')
  // ★ 判据一律走**去注释后的正文**（2026-10-12 首跑自伤真踩）：哨兵那段说明注释里就写着
  //   「兜底文案必须放 <body>」，按字面 indexOf 找 `<body` 会命中注释里的那一个，
  //   于是「noscript 在 head」判成假、哨兵位置锁与摘除反证一起红——判据自己撒的第一类谎。
  const stripHtmlComments = (src: string) => src.replace(/<!--[\s\S]*?-->/g, '')
  const code = stripHtmlComments(html)

  // noscript 的落点判据：HTML5 的「in-head noscript」插入模式只认 link/style/meta，
  // 里面写 <div> 会让 vite build 的 parse5 **当场解析报错**（2026-10-12 首跑真踩：
  // 开发热更一切正常，一进 `npx vite build` 就报 startTagInHeadNoScript，根本出不了包）。
  // 故兜底文案必须落在 <body>。返回 true＝形态不对。
  function noscriptPlacementBad(src: string): boolean {
    const body = src.indexOf('<body')
    const ns = src.indexOf('<noscript>')
    if (ns < 0 || body < 0) return true
    return ns < body
  }

  it('有 <noscript> 兜底文案（禁 JS 的访客不该只看到黑屏）', () => {
    expect(code).toMatch(/<noscript>[\s\S]{0,400}JavaScript/)
  })

  it('noscript 落在 <body> 内（放 head 会让 vite build 解析失败）', () => {
    expect(noscriptPlacementBad(code)).toBe(false)
  })

  // 反证：三种形态各自必须被判不对（否则上面那条是空转）
  it('反证：head 版 noscript／缺 noscript 都能被抓到，body 版放行', () => {
    expect(noscriptPlacementBad('<html><head><noscript><div>x</div></noscript></head><body></body></html>')).toBe(true)
    expect(noscriptPlacementBad('<html><head></head><body><noscript><p>x</p></noscript></body></html>')).toBe(false)
    expect(noscriptPlacementBad('<html><head></head><body></body></html>')).toBe(true)
  })

  it('哨兵是普通内联脚本且放在入口 module 之前（type=module 的脚本自己失败时救不了自己）', () => {
    const sent = code.indexOf('DEADLINE_MS')
    const mod = code.indexOf('<script type="module"')
    expect(sent).toBeGreaterThan(-1)
    expect(mod).toBeGreaterThan(-1)
    expect(sent).toBeLessThan(mod)
    // 哨兵那段所在的 <script> 开标签不许带 type="module"
    const openTag = code.slice(code.lastIndexOf('<script', sent), sent)
    expect(openTag).not.toContain('type="module"')
  })

  it('哨兵三件读数都在：站点数据探测／脚本加载失败／未捕获异常，外加一个重载按钮', () => {
    expect(code).toContain('storageBlocked')
    expect(code).toContain("tagName === 'SCRIPT'")
    expect(code).toContain('uncaught')
    expect(code).toMatch(/重新加载/)
    expect(code).toContain('location.reload()')
  })

  it('只在 #root 一直空着时才显示（正常挂载即收摊，不许打扰健康访问）', () => {
    expect(code).toContain('childElementCount')
    expect(code).toMatch(/rootEmpty\(\)[\s\S]{0,120}clearInterval/)
  })

  // 反证 5：摘掉哨兵那一段 `<script>` 后，三段哨兵判据必须全部失效
  //         （以前这里是「按注释起点切到 </head>」——注释一剥，那种切法既切不干净又会连带把
  //          body 里的 noscript 判没，属于「反证自己不可信」。改成按结构剥那一个脚本块。）
  it('反证 5：摘掉哨兵脚本块后，哨兵判据一条都不成立', () => {
    const stripped = code.replace(/<script>[\s\S]*?DEADLINE_MS[\s\S]*?<\/script>/, '')
    expect(stripped).not.toContain('DEADLINE_MS')
    expect(stripped).not.toContain('storageBlocked')
    expect(stripped).not.toContain('childElementCount')
    // 纯 `<script>`（不带任何属性）全文只有哨兵那一个；module 那个带属性、不该被这段判据算进去
    expect((code.match(/<script>/g) || []).length).toBe(1)
    expect((stripped.match(/<script>/g) || []).length).toBe(0)
    // 对照腿：没摘的正文里这三条必须在（否则上面那三条「not.toContain」是空转）
    expect(code).toContain('DEADLINE_MS')
    expect(code).toContain('storageBlocked')
    expect(code).toContain('childElementCount')
  })
})

// C) 挂载**之后**的崩溃：哨兵只治「#root 一直空着」，挂载成功后再抛异常时 React 会卸载整棵树，
//    那时哨兵已经收摊 ⇒ 仍然是黑屏。所以 main.tsx 必须自己有一层 ErrorBoundary 出话。
describe('启动链锁 C：main.tsx 的渲染期兜底在位', () => {
  const main = readFileSync(ROOT + 'src/main.tsx', 'utf-8')

  it('App 被 BootErrorBoundary 包着（渲染期异常出话，不出黑屏）', () => {
    expect(main).toMatch(/class BootErrorBoundary extends React\.Component/)
    expect(main).toMatch(/static getDerivedStateFromError/)
    expect(main).toMatch(/<BootErrorBoundary>[\s\S]{0,200}<App\s*\/>[\s\S]{0,200}<\/BootErrorBoundary>/)
  })

  // 反证 6：把包裹层摘掉（回到旧形态）判据必须失效
  it('反证 6：拆掉包裹层后「<App/> 在 boundary 内」这条判不到', () => {
    const naked = main.replace('<BootErrorBoundary>', '').replace('</BootErrorBoundary>', '')
    expect(naked).not.toMatch(/<BootErrorBoundary>[\s\S]{0,200}<App\s*\/>[\s\S]{0,200}<\/BootErrorBoundary>/)
  })

  it('兜底文案是人话且不含堆栈／内部路径（只把详情留在控制台）', () => {
    expect(main).toContain('页面出错了')
    expect(main).toContain('console.error')
    expect(main).not.toContain('error.stack')
    expect(main).not.toContain('/src/')
  })
})
