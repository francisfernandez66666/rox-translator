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

  it('哨兵五件读数都在：站点数据／脚本加载失败／未捕获异常／模块加载被拒／下载未完成，外加重载按钮', () => {
    expect(code).toContain('storageBlocked')
    expect(code).toContain("tagName === 'SCRIPT'")
    expect(code).toContain('uncaught')
    // ★ 2026-10-17 第四件：路由 chunk 的 import 失败只走 rejection 通道，
    //   缺这一腿时屏幕上只剩「没有抓到具体报错」那句零信息量文案（现网实证）。
    expect(code).toContain('unhandledrejection')
    expect(code).toContain('rejected')
    // ★ 第五件：请求卡住（既不报错也不抛异常）时唯一的读数来源＝Resource Timing 没有条目
    expect(code).toContain('pendingScripts')
    expect(code).toContain('getEntriesByName')
    expect(code).toMatch(/重新加载/)
    expect(code).toContain('location.reload()')
  })

  // 反证 8：摘掉「下载未完成」腿 ⇒ 第五件读数判不到；且兜底那句必须同步收口
  //   （只加读数不改兜底条件 ⇒ 界面上同时出现「下载没完成」和「没抓到具体报错」两句自相矛盾的话）。
  it('反证 8：摘掉下载未完成腿后第五件读数判不到（并钉住兜底条件同步收口）', () => {
    const stripped = code.replace(/function pendingScripts\(\)[\s\S]*?\n        \}\n/, '')
    expect(stripped, '反证失效：变异没落地（正则没命中＝没反证）').not.toBe(code)
    // 只判**调用形态**：说明注释里本来就会写到这个词（JS 注释不归 stripHtmlComments 管，
    // 拿裸词判＝被自己的注释喂饱，本仓踩过）
    expect(stripped).not.toContain('function pendingScripts')
    expect(stripped).not.toContain('getEntriesByName')
    // 对照腿：没摘的正文里两件都在
    expect(code).toContain('function pendingScripts()')
    expect(code).toContain('getEntriesByName')
    // 兜底收口：两句不许同框（摘掉 !pending.length 也必须判到）
    expect(code).toMatch(/!deadScripts\.length && !pending\.length/)
    const noPendingGuard = code.replace('&& !pending.length', '')
    expect(noPendingGuard, '反证失效：兜底条件变异没落地').not.toBe(code)
    expect(noPendingGuard).not.toMatch(/!deadScripts\.length && !pending\.length/)
  })

  // 反证 7：摘掉 unhandledrejection 那一腿（回到旧形态）⇒ 第四件读数必须判不到。
  //   ⚠️ 剥的是**调用形态**不是那串词：说明注释里本来就会写到 unhandledrejection
  //   （「按字面量判据会被自己的注释喂饱」是本仓反复踩过的形态，同 §一·9 那条）。
  //   两截各摘一次：只听事件不收口、只收口不监听，都必须是红。
  const REJ_LISTEN = "window.addEventListener('unhandledrejection'"
  it('反证 7：摘掉 unhandledrejection 监听后第四件读数判不到（回到旧形态即红）', () => {
    const stripped = code.replace(/window\.addEventListener\('unhandledrejection'[\s\S]*?\n        \}\)\n/, '')
    expect(stripped, '反证失效：变异没落地（正则没命中＝没反证）').not.toBe(code)
    expect(stripped).not.toContain(REJ_LISTEN)
    // 对照腿：没摘的正文里两件都在（否则上面是空转）
    expect(code).toContain(REJ_LISTEN)
    expect(code).toMatch(/界面模块没能加载/)
    // 兜底那句必须已被 rejected 收口——只加读数不接收口条件，等于"有读数但照样说没抓到"
    //   ⚠️ 判据只问相邻两片（!uncaught && !rejected）：这一行后面还挂着第五腿（!pending.length），
    //   把整串写死＝下一腿加进来就假红（那正是本文件最该避免的"判据自己过时"）。
    expect(code).toMatch(/!uncaught && !rejected\)/)
    const noGuard = code.replace('&& !rejected', '')
    expect(noGuard, '反证失效：收口条件变异没落地').not.toBe(code)
    expect(noGuard).not.toMatch(/!uncaught && !rejected\)/)
  })

  // ★ 2026-10-17 新锁（本次真踩）：index.html 里**不许出现形如构建产物的 assets/ 路径**——
  //   连注释里也不行。理由是 deploy/swap_web_and_binary.sh 的备料段会把 index.html 里
  //   所有 `assets/….js|css` 当成"首页引用的 asset"逐个查落盘，注释里写一个不存在的名字
  //   会让它在**换源前**判 BAD、整批停在备料段（本次首跑实测：`BAD 首页引用的 assets/Landing-xxxx.js 未落盘`，
  //   而那一串只是我写在哨兵注释里的举例）。改法只有"注释别写出那个形状"，
  //   因为"注释喂饱下游判据"是本仓反复出现的形态（AGENTS §一·9 那条）。
  const assetPathLines = (src: string) =>
    src.split('\n').map((l, i) => [i + 1, l] as const).filter(([, l]) => /assets\//.test(l)).map(([n]) => n)

  it('index.html 内零 assets/ 路径字面量（写出会被换源脚本当引用去查落盘）', () => {
    const hits = assetPathLines(code)
    expect(hits, '这些行含 assets/ 路径，换源备料段会逐条查落盘：\n' + hits.map((n) => `${n}: ${code.split('\n')[n - 1].trim()}`).join('\n')).toEqual([])
    // 对照腿：同一判据函数喂一行带假路径的源码必须报出来（否则上面那条是空转）
    expect(assetPathLines('<a href="/assets/index-zzz.js">x</a>')).toEqual([1])
    expect(assetPathLines('// 举个例：assets/Landing-xxx.js')).toEqual([1])
    expect(assetPathLines('<a href="/logo.svg">x</a>')).toEqual([])
  })

  it('只在 #root 一直空着时才显示（正常挂载即收摊，不许打扰健康访问）', () => {
    expect(code).toContain('childElementCount')
    expect(code).toMatch(/rootEmpty\(\)[\s\S]{0,120}clearInterval/)
  })

  // ★★ 2026-10-17 现网第二次实证（访客交回的 ② 读数指向**刚换上去的新入口**）：真身不是某个坏文件，
  //   而是这一跳确实慢（本机跨境实测：163 KB 的 br 入口 12.8 s 才完，另两次 60 s 都没下完；
  //   同一台打源站只要几十毫秒）——于是**哨兵自己**成了那块黑屏：
  //     ① 6s 固定截止把「还在下载」误报成「下载没完成」（用户被引导去点重新加载＝从零再下一遍）；
  //     ② 面板一旦显示就再也不会撤（旧形态只在「#root 空着」时轮询收摊，`show()` 之后 #root
  //        才填上的那一档没人管）⇒ 界面其实已经挂载、用户脸上还盖着「页面没能加载起来」。
  //   三条新锁与它们的反证共用同一把尺子（下面的两个判据函数）。
  function adaptiveFailures(src: string): string[] {
    const bad: string[] = []
    if (!/navigator\.connection/.test(src)) bad.push('没有现读网络档（慢链路判据缺输入）')
    if (!/DEADLINE_MS = SLOW_LINK \? 30000 : 8000/.test(src)) bad.push('截止没有按网络档分档')
    if (!/CEILING_MS\s*=\s*60000/.test(src)) bad.push('缺上限（慢链路上会无限等下去）')
    if (!/function resCount\(\)[\s\S]{0,200}getEntriesByType\('resource'\)/.test(src)) bad.push('缺「链路还在动」这一读数')
    if (!/DEADLINE_MS = Math\.min\(Date\.now\(\) - startAt \+ 8000, CEILING_MS\)/.test(src)) {
      bad.push('到期后没有再等一拍（会把"还在下载"误报成"没下完"）')
    }
    return bad
  }
  function slowPathFailures(src: string): string[] {
    const bad: string[] = []
    if (!/show\('slow'\)/.test(src)) bad.push('慢链路没有单独的展示档（会和确定失效混为一谈）')
    if (!/data-kind', 'slow'/.test(src)) bad.push('横幅没有形态标记（撤销与判据都认不出它）')
    if (!/clearInterval\(timer\);\s*dismissBootNotice\(\)/.test(src)) bad.push('挂载后没有撤销哨兵产物')
    const i = src.indexOf("kind === 'slow'")
    const j = src.indexOf('box = document.createElement')
    if (i < 0 || j < i) bad.push('慢链路分支读不到（判据射程失效）')
    else if (/inset:0/.test(src.slice(i, j))) bad.push('慢链路横幅用了全屏铺满（会盖住已经挂载好的界面）')
    return bad
  }

  it('哨兵三期口径：网络档自适应＋上限＋链路还在动就再等一拍', () => {
    const bad = adaptiveFailures(code)
    expect(bad, bad.join('\n')).toEqual([])
  })

  it('慢链路只出底部横幅（不挡界面）且挂载后必须撤销', () => {
    const bad = slowPathFailures(code)
    expect(bad, bad.join('\n')).toEqual([])
  })

  // 反证 9：三条腿各摘一条（写回固定截止／慢链路改回全屏面板／撤掉撤销调用），必须各自判红
  it('反证 9：固定截止／慢链路走全屏／不撤销 —— 三处各自改坏都要判红', () => {
    const fixed = code.replace('var DEADLINE_MS = SLOW_LINK ? 30000 : 8000', 'var DEADLINE_MS = 6000')
    expect(fixed, '反证失效：变异没落地').not.toBe(code)
    expect(adaptiveFailures(fixed).length, '固定 6s 截止没被判红').toBeGreaterThan(0)
    expect(adaptiveFailures(code)).toEqual([])

    const blocked = code.replace("show('slow')", "show('panel')")
    expect(blocked, '反证失效：变异没落地').not.toBe(code)
    expect(slowPathFailures(blocked).length, '慢链路改回全屏面板没被判红').toBeGreaterThan(0)

    const noDismiss = code.replace('clearInterval(timer); dismissBootNotice(); dismissBootSkeleton(); return',
      'clearInterval(timer); return')
    expect(noDismiss, '反证失效：变异没落地').not.toBe(code)
    expect(slowPathFailures(noDismiss).length, '挂载后不撤销没被判红').toBeGreaterThan(0)
    expect(slowPathFailures(code)).toEqual([])
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

// D) 首屏骨架（★ 2026-10-17 轻量化批 C）：跨境入口 br 163 KB 在 6~20 KB/s 的链路上要 10~33 s
//    才到，此前这段时间屏幕只有一个空 #root＝纯黑（哨兵横幅 8 s 后才出）。骨架是**纯 HTML+CSS**，
//    四条口径（兄弟节点／无文案／无资源／两条路径都要撤）逐条钉在这里；五枚反向腿保证不是空转。
describe('启动链锁 D：首屏骨架在位且会撤（轻量化批 C）', () => {
  const html = readFileSync(ROOT + 'index.html', 'utf-8')
  const code = html.replace(/<!--[\s\S]*?-->/g, '')
  // 结构摘取：骨架节点块与 <style> 块（判据都在这两块**之内**问，避免被别的段落喂饱）
  const nodeOf = (src: string) => (src.match(/<div id="__boot_skeleton"[\s\S]{0,200}?<\/div>/) || [''])[0]
  const styleOf = (src: string) => (src.match(/<style>[\s\S]*?<\/style>/) || [''])[0]
  function skeletonFailures(src: string): string[] {
    const bad: string[] = []
    const node = nodeOf(src)
    const css = styleOf(src)
    if (!node) bad.push('读不到骨架节点（判据射程失效）')
    if (!css || !/#__boot_skeleton/.test(css)) bad.push('读不到骨架样式（判据射程失效）')
    // ① 兄弟节点：必须落在 <div id="root"> 之前，且结构上不是它的子节点（自带闭合 </div>）
    const rootAt = src.indexOf('<div id="root">')
    const nodeAt = src.indexOf('<div id="__boot_skeleton"')
    if (rootAt < 0 || nodeAt < 0 || nodeAt > rootAt) bad.push('骨架不是 #root 的兄弟节点（塞进去会让哨兵以为界面已挂载）')
    // ② 无文案：节点里不许有多字节文案；样式里不许用 content: 变相写字
    if (/[\u3000-\u9fff\uff00-\uffef]/.test(node)) bad.push('骨架节点里带了文案（换语种/租户会闪错话）')
    if (/(^|[\s;{])content\s*:/.test(css)) bad.push('样式里用 content: 写字（同样是写死的文案）')
    // ③ 无资源引用：不放 logo/svg/图片（logo 在本仓有唯一事实源，第三份拷贝＝漂移温床）
    if (/url\(|src=|\.svg|\.png|\.jpg|\.gif/.test(node + css)) bad.push('骨架引用了外部资源（logo 只许有一个出处）')
    // ④ 两条路径都要撤
    if (!/function dismissBootSkeleton\(\)/.test(src)) bad.push('缺撤销函数')
    if (!/clearInterval\(timer\);\s*dismissBootNotice\(\);\s*dismissBootSkeleton\(\)/.test(src)) {
      bad.push('界面挂载时没有撤骨架')
    }
    if (!/data-kind', 'panel'\)[\s\S]{0,240}dismissBootSkeleton\(\)/.test(src)) bad.push('确定失效转全屏面板时没有撤骨架')
    const slowAt = src.indexOf("kind === 'slow'")
    const slowEnd = src.indexOf('return', slowAt)
    if (slowAt < 0 || slowEnd < slowAt) bad.push('慢链路分支读不到（判据射程失效）')
    else if (/dismissBootSkeleton\(/.test(src.slice(slowAt, slowEnd))) bad.push('慢链路横幅那一档不该撤骨架（那一档本就是"还在加载"）')
    // reduced-motion：转圈动画必须能被系统设置关掉
    if (!/@media \(prefers-reduced-motion: reduce\)[\s\S]{0,120}animation: none/.test(css)) bad.push('缺 prefers-reduced-motion 腿')
    return bad
  }

  it('射程自检＋四条口径全在位', () => {
    const bad = skeletonFailures(code)
    expect(bad, bad.join('\n')).toEqual([])
  })

  it('反证 10：摘掉骨架／摘掉挂载撤销／把骨架塞进 #root —— 三处各自改坏都要判红', () => {
    const noNode = code.replace(nodeOf(code), '')
    expect(noNode, '反证失效：变异没落地').not.toBe(code)
    expect(skeletonFailures(noNode).length, '摘掉骨架没被判红').toBeGreaterThan(0)

    const noDismiss = code.replace(/clearInterval\(timer\); dismissBootNotice\(\); dismissBootSkeleton\(\); return/,
      'clearInterval(timer); return')
    expect(noDismiss, '反证失效：变异没落地').not.toBe(code)
    expect(skeletonFailures(noDismiss).length, '挂载不撤骨架没被判红').toBeGreaterThan(0)

    const inside = code.replace(nodeOf(code), '').replace('<div id="root"></div>', '<div id="root">' + nodeOf(code) + '</div>')
    expect(inside, '反证失效：变异没落地').not.toBe(code)
    expect(skeletonFailures(inside).length, '骨架塞进 #root 没被判红').toBeGreaterThan(0)

    // 第四条：用 CSS content 写字（文案换个地方写回来）——判据必须仍然抓得到
    // ⚠️ 锚点随批 B 变了：骨架容器那条规则现在是 `#__boot_skeleton,\n.lc-boot-cover {`（两个选择器共用一份声明），
    //   写死 `'#__boot_skeleton {'` 会命中不到 ⇒ 变异变 no-op、反证自己判红（本批真踩）
    const cssText = code.replace('#__boot_skeleton,', '#__boot_skeleton::after { content: "加载中" } #__boot_skeleton,')
    expect(cssText, '反证失效：变异没落地').not.toBe(code)
    expect(skeletonFailures(cssText).length, 'CSS 写字没被判红').toBeGreaterThan(0)
  })
})

// C) 挂载**之后**的崩溃：哨兵只治「#root 一直空着」，挂载成功后再抛异常时 React 会卸载整棵树，
//    那时哨兵已经收摊 ⇒ 仍然是黑屏。所以 main.tsx 必须自己有一层 ErrorBoundary 出话。
describe('启动链锁 C：main.tsx 的渲染期兜底在位', () => {
  const main = readFileSync(ROOT + 'src/main.tsx', 'utf-8')
  // ★ 2026-10-17 批 B 起判据换成「boundary 之内是**分诊出来的入口**」：App/PublicRoot 都是
  //   `React.lazy`（见 lib/navShim.test.ts），渲染处不再是字面量 `<App />` 而是 `pickEntry()`。
  //   boundary 的射程没变：它仍包住整棵树（含入口分片的 Suspense），渲染期异常照样出话。
  const wrapped = /<BootErrorBoundary>[\s\S]{0,600}pickEntry\(\)[\s\S]{0,200}<\/BootErrorBoundary>/

  it('分诊出来的入口被 BootErrorBoundary 包着（渲染期异常出话，不出黑屏）', () => {
    expect(main).toMatch(/class BootErrorBoundary extends React\.Component/)
    expect(main).toMatch(/static getDerivedStateFromError/)
    expect(main).toMatch(wrapped)
  })

  // ★ 入口分片的 Suspense fallback **必须非空**（且与首屏骨架同貌）：哨兵判「界面起没起来」的
  //   依据是 `#root` 有没有子节点，fallback 给 null 时懒加载分片下载期间 `#root` 恒空
  //   ⇒ 哨兵在截止时刻会把"正在正常下载"误判成「页面没能加载起来」盖全屏（见 BootLoading.tsx 文件头）。
  it('入口 Suspense 的 fallback 是 BootLoading（不能是 null）', () => {
    expect(main).toMatch(/<React\.Suspense fallback=\{<BootLoading \/>\}>/)
    expect(main).not.toMatch(/<React\.Suspense fallback=\{null\}>/)
  })

  // 反证 6：把包裹层摘掉（回到旧形态）判据必须失效
  it('反证 6：拆掉包裹层后「入口在 boundary 内」这条判不到', () => {
    const naked = main.replace('<BootErrorBoundary>', '').replace('</BootErrorBoundary>', '')
    expect(naked).not.toMatch(wrapped)
    // 第二条腿的反证：把 fallback 换成 null（"省一个组件"式改动）必须当场判红
    const nullFallback = main.replace('<React.Suspense fallback={<BootLoading />}>', '<React.Suspense fallback={null}>')
    expect(nullFallback).not.toBe(main)
    expect(nullFallback).not.toMatch(/<React\.Suspense fallback=\{<BootLoading \/>\}>/)
    expect(nullFallback).toMatch(/<React\.Suspense fallback=\{null\}>/)
  })

  it('兜底文案是人话且不含堆栈／内部路径（只把详情留在控制台）', () => {
    // ★ 2026-10-19 G20：兜底文案改走词典（旧形态硬编码中文，非中文访客启动失败看中文）。
    //   判据从「含旧中文字面量」翻转为「三个 i18n 键在位 + 旧硬编码文案清零」——
    //   负向腿同时在注释上生效：不许把旧文案留在注释里糊过判据。
    expect(main).toContain("t('common.pageError')")
    expect(main).toContain("t('common.bootErrorBody')")
    expect(main).toContain("t('common.reloadPage')")
    expect(main).not.toContain('页面出错了')
    expect(main).not.toContain('重新加载通常就能恢复')
    expect(main).toContain('console.error')
    expect(main).not.toContain('error.stack')
    expect(main).not.toContain('/src/')
  })
})
