// ============================================================================
// lib/navShim.test.ts — 公开面/后台**入口拆分**（★ 2026-10-17 首屏轻量化批 B）的机械锁
//
// 这一批做的事：把"只想看官网首页的未登录访客"从 `App.tsx` 那棵大树里分出来，
// 走不带 react-router 的 `PublicRoot`（分诊在 `main.tsx` 的 `pickEntry()`）。
// 体积账：入口 raw 470 KB → 目标 ≈320 KB（react-router 那一坨约 150 KB minified 是最大单项）。
//
// 为什么必须有这个文件（AGENTS §三「判据级断言代替不了守卫级」）：
//   拆分的**全部收益**只体现在"入口 chunk 的静态依赖闭包里有谁"——tsc 绿、vitest 绿、
//   单测里 `useNav()` 也绿（它本来就有 window 回落），**只有把闭包算一遍才看得见回归**。
//   一行 `import App from './App'`（或公开面某文件顺手 import 了 react-router）就能把
//   那 367 KB 悄悄搬回来，而界面上一个字都不会变。
//
// 四段：
//   A 入口分诊：main.tsx 必须 lazy 加载 PublicRoot/App，白名单与 App 的未登录分支同步。
//   B 闭包不许碰 router：从 main.tsx 出发的**静态 import 闭包**里，react-router 导入者必须为 0，
//     且闭包必须真含 PublicRoot、真不含 App（否则这条锁是空转的）。
//   C 桥接唯一入口：`lib/navRouter.tsx` 只许 App.tsx 引；`lib/nav.ts` 只许 window 回落。
//   D 两支等值：PublicRoot 的分诊与 App.tsx 的 `!user` 三支逐支等值（同一地址两棵树同一屏）。
//   各段带反证（喂变异后的文件表进同一个判据函数），判据与反证共用一份实现。
//
// ⚠️ 去注释用**逐字符状态机**，不许用 `/\*…\*/` 正则：本仓注释里写着 `panels/*.ts`、`from '…'`
//   这类字样，正则版会把从那个 `/*` 起到下一个 `*/` 之间的真代码一起吃掉，判据当场变 0 命中空转
//   （同 appPanelSplit.test.ts 首跑踩过的坑，实现与那份保持一致）。
// ============================================================================
import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const SRC = fileURLToPath(new URL('..', import.meta.url)) // src/

/** 剥注释（行注释 + 块注释 + 字符串感知，见文件头） */
function stripComments(s: string): string {
  let out = ''
  let state: 'code' | 'line' | 'block' | 'str' = 'code'
  let quote = ''
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    const n = s[i + 1]
    if (state === 'code') {
      if (c === '/' && n === '/') { state = 'line'; i++; continue }
      if (c === '/' && n === '*') { state = 'block'; i++; continue }
      if (c === '"' || c === "'" || c === '`') { state = 'str'; quote = c; out += c; continue }
      out += c; continue
    }
    if (state === 'line') { if (c === '\n') { state = 'code'; out += c } continue }
    if (state === 'block') { if (c === '*' && n === '/') { state = 'code'; i++ } continue }
    if (c === '\\') { out += c + (n ?? ''); i++; continue }
    if (c === quote) { state = 'code' }
    out += c
  }
  return out
}

/** 磁盘上的源码表（绝对路径 → 剥注释文本）：键就是"文件身份"，反证靠替换表里的值/增删键来做 */
function loadSources(): Map<string, string> {
  const out = new Map<string, string>()
  // ⚠️ SRC 以 `/` 结尾：拼路径时别再补一个（`src//main.tsx` 与 `src/main.tsx` 是**两个键**，
  //    查表的腿会全部静默变成"读不到"——本文件首跑真踩，10 条腿一起红在这上面）
  const join = (dir: string, name: string) => (dir.endsWith('/') ? dir + name : dir + '/' + name)
  const walk = (dir: string) => {
    for (const name of readdirSync(dir)) {
      const p = join(dir, name)
      if (statSync(p).isDirectory()) { walk(p); continue }
      if (!/\.(ts|tsx)$/.test(name)) continue
      if (/\.test\./.test(name)) continue
      out.set(p, stripComments(readFileSync(p, 'utf-8')))
    }
  }
  walk(SRC)
  return out
}
const FILES = loadSources()
const MAIN = SRC + 'main.tsx'
const APP = SRC + 'App.tsx'
const PUBLIC_ROOT = SRC + 'PublicRoot.tsx'
const NAV = SRC + 'lib/nav.ts'
const NAV_ROUTER = SRC + 'lib/navRouter.tsx'

/** 解析一个 specifier 到源码表里的真实文件（只认相对路径与 `@/`） */
function resolveSpec(fromFile: string, spec: string, files: Map<string, string>): string | null {
  if (!spec.startsWith('.') && !spec.startsWith('@/')) return null
  const base = spec.startsWith('@/') ? SRC + spec.slice(2) : fileURLToPath(new URL(spec, 'file://' + fromFile))
  for (const cand of [base + '.ts', base + '.tsx', base + '/index.ts', base + '/index.tsx', base]) {
    if (files.has(cand)) return cand
  }
  return null
}

/** 静态 import 闭包（只跟 `from '…'`：**动态 import 是另一个 chunk**，对应"入口 chunk 里有什么"） */
function staticClosure(roots: string[], files: Map<string, string>): Set<string> {
  const seen = new Set<string>()
  const queue = [...roots]
  while (queue.length) {
    const f = queue.shift()!
    if (seen.has(f)) continue
    const src = files.get(f)
    if (src === undefined) continue
    seen.add(f)
    for (const m of src.matchAll(/from\s+'([^']+)'/g)) {
      const r = resolveSpec(f, m[1], files)
      if (r && !seen.has(r)) queue.push(r)
    }
  }
  return seen
}

/** 全量闭包（静态 `from` + 动态 `import('…')`）：对应"**访客真会下到**的那些 chunk"。
 *  动态边不能省：页面的懒加载分片（Landing/Login/定价/比价/挂件）只有它才走得到，
 *  而那几个文件正是最可能被顺手加一行 router 的地方（`PublicRoot` 里写一句
 *  `import { useLocation } from 'react-router-dom'` 是**最贵**的形态：它进入口分片的依赖图）。 */
function fullClosure(roots: string[], files: Map<string, string>): Set<string> {
  const seen = new Set<string>()
  const queue = [...roots]
  while (queue.length) {
    const f = queue.shift()!
    if (seen.has(f)) continue
    const src = files.get(f)
    if (src === undefined) continue
    seen.add(f)
    for (const m of src.matchAll(/from\s+'([^']+)'/g)) {
      const r = resolveSpec(f, m[1], files)
      if (r && !seen.has(r)) queue.push(r)
    }
    for (const m of src.matchAll(/import\(\s*'([^']+)'\s*\)/g)) {
      const r = resolveSpec(f, m[1], files)
      if (r && !seen.has(r)) queue.push(r)
    }
  }
  return seen
}

// ---------------------------------------------------------------- A 入口分诊
/** A 判据：main.tsx 必须 lazy 分诊、且白名单与 App 的未登录分支同步 */
function entryTriageFailures(files: Map<string, string>): string[] {
  const bad: string[] = []
  const main = files.get(MAIN)
  const app = files.get(APP)
  if (main === undefined || app === undefined) return ['读不到 main.tsx 或 App.tsx（锚漂了，本锁失明）']
  // ① 两个入口都必须是 lazy（静态 import App 就等于把 router 钉回入口包）
  if (!/lazy\(\(\) => import\('\.\/App'\)\)/.test(main)) bad.push('main.tsx 里 App 不是 React.lazy（静态 import 会把 router/后台面板钉回入口包）')
  if (!/lazy\(\(\) => import\('\.\/PublicRoot'\)\)/.test(main)) bad.push('main.tsx 里 PublicRoot 不是 React.lazy')
  // ② 分诊必须按「token × 路径」两支判定，且调用它在渲染里
  if (!/function pickEntry\(\)/.test(main)) bad.push('main.tsx 缺 pickEntry() 分诊（或改名了，请同步本锁）')
  if (!/!getAuthToken\(\) && PUBLIC_PATHS\.has\(window\.location\.pathname\)/.test(main)) {
    bad.push('分诊判据被改动（本锁认「无 token 且落在公开路径 ⇒ PublicRoot」）')
  }
  if ((main.match(/pickEntry\(\)/g) || []).length < 2) bad.push('pickEntry() 定义了却没在渲染里用（分诊空转）')
  // ③ 白名单 = App 未登录分支里出现的路径 ＋ 登录/注册两条。不同步＝新公开页退回"要下 router"的老形态
  const guest = guestRegion(app)
  if (!guest) return [...bad, '读不到 App.tsx 的未登录分诊区（锚点 `if (!user) {` / `// 后台路由守卫` 之一漂了）']
  const m = main.match(/const PUBLIC_PATHS = new Set\(\[([^\]]*)\]\)/)
  if (!m) return [...bad, '读不到 main.tsx 的 PUBLIC_PATHS']
  const listed = [...m[1].matchAll(/'([^']+)'/g)].map((x) => x[1])
  const handled = [...guest.matchAll(/path === '([^']*)'/g)].map((x) => x[1])
  if (handled.length === 0 || listed.length === 0) return [...bad, '白名单或 App 分支读空（判据空转）']
  const want = [...new Set([...handled, '/login', '/register'])].sort().join(',')
  if ([...listed].sort().join(',') !== want) {
    bad.push(`PUBLIC_PATHS 与 App 未登录分支不同步：main=[${listed}] 期望=[${want.split(',')}]`)
  }
  return bad
}

/** App.tsx 的未登录分诊区（从 `if (!user) {` 到**后台路由守卫**那句 `if`）。
 *  ⚠️ 结束锚点必须是**代码**（`if (path.startsWith('/admin')) {`）而不是那句注释：
 *  本文件把注释剥掉了，用 `// 后台路由守卫` 当锚点会恒找不到（首跑真踩）。 */
function guestRegion(app: string): string | null {
  const at = app.indexOf('if (!user) {')
  const end = app.indexOf("if (path.startsWith('/admin')) {", at)
  if (at < 0 || end < at) return null
  return app.slice(at, end)
}

// ------------------------------------------------- B 入口闭包里不许有 router
/** B 判据（两条账分别算）：
 *  ① **入口 chunk 的静态闭包**（main.tsx 的静态依赖）里不许有 App、不许有 router 导入者
 *     —— 这一条直接对应"访客为入口包付的那 470 KB 里有没有 router"。
 *  ② **公开面全量闭包**（PublicRoot 走静态+动态边，含懒加载页面分片）里 router 导入者必须为 0
 *     —— 页面分片同样砸在访客头上，且"顺手加一行 router"最常落在这里。
 *  射程自证：公开面全量闭包必须真含那五个懒加载页面（否则第②条对它们完全无感）。 */
function closureFailures(files: Map<string, string>): string[] {
  const bad: string[] = []
  const entry = staticClosure([MAIN], files)
  const pub = fullClosure([PUBLIC_ROOT], files)
  if (entry.size < 5 || pub.size < 5) return ['闭包算不出（判据空转）']
  if (entry.has(APP)) bad.push('入口静态闭包里出现了 App（入口又把 router 与后台面板背回来了）')
  const pages = ['components/Login.tsx', 'components/Landing.tsx', 'components/PricingPage.tsx',
    'components/PriceComparePage.tsx', 'components/AiAssist.tsx']
  const missing = pages.filter((p) => !pub.has(SRC + p))
  if (missing.length) bad.push(`公开面全量闭包里少了页面分片：${missing.join('、')}（②这条的射程不完整）`)
  const routerUsers = [...new Set([...entry, ...pub])]
    .filter((f) => /from\s+'react-router(-dom)?'/.test(files.get(f) || ''))
  if (routerUsers.length) {
    bad.push(`入口/公开面闭包里有 ${routerUsers.length} 个文件引 react-router：` +
      routerUsers.map((f) => f.slice(SRC.length)).join('、'))
  }
  return bad
}

// ----------------------------------------------------------- C 桥接唯一入口
/** C 判据：navRouter 只许 App.tsx 引；nav.ts 必须零 react-router 且回落用的是 location.assign/replace */
function bridgeFailures(files: Map<string, string>): string[] {
  const bad: string[] = []
  const importers: string[] = []
  for (const [f, src] of files) {
    if (f === NAV_ROUTER) continue
    if (/from\s+'(?:\.\.?\/)*lib\/navRouter'|from\s+'@\/lib\/navRouter'/.test(src)) importers.push(f)
  }
  const rel = importers.map((f) => f.slice(SRC.length)).sort()
  if (rel.join(',') !== 'App.tsx') {
    bad.push(`lib/navRouter.tsx 的引用方必须恰为 App.tsx，实测=[${rel}]（公开面引它＝router 回到入口包）`)
  }
  const nav = files.get(NAV)
  if (nav === undefined) return [...bad, '读不到 lib/nav.ts']
  if (/from\s+'react-router(-dom)?'/.test(nav)) bad.push('lib/nav.ts 引了 react-router（它就是为"不带 router 的公开面"存在的）')
  if (!/window\.location\.(assign|replace)\(/.test(nav)) bad.push('lib/nav.ts 的 window 回落没有真跳转实现（假跳转：URL 变了而界面不换）')
  return bad
}

// ------------------------------------------------------------- D 两支等值
const LOGIN_FACE = SRC + 'components/LoginFace.tsx'
const AUTH_STORE = SRC + 'stores/auth.tsx'
/** 两棵树必须逐支等值的锚点（每一条都要在 App 的未登录区与 PublicRoot 里各出现一次）。
 *  ★ 2026-10-21 IAM P1-7：登录面本身已从两棵树里抽出去，收成 `components/LoginFace` 一处调用——
 *    所以「登录壳 / 挂件 / admin 形态 / 登录后弹回」这四条不再是"两处各出现一次"，而是
 *    "两棵树都只调 LoginFace、且 LoginFace 里有这四条"。改判据的理由：
 *    两处各写一份时，下一批把两处同时改成另一个不同的值照样"相等地错"；
 *    收敛成唯一调用点之后，射程应该问"有没有第二份登录面"，而不是"两份写得像不像"。 */
const GUEST_MARKERS: Array<[string, RegExp]> = [
  ['定价页分支', /path === '\/pricing'/],
  ['比价页分支', /path === '\/compare'/],
  ['首页分支', /path === '\/'/],
  // ★ 2026-10-23：判据签名从 `landingEnabledForGuest()` 变成 `landingEnabledForGuest(branding.tenantId)`
  //   （加了租户维度：租户定制页对访客不展示平台落地页）。正则只锚到左括号，参数写什么不锁——
  //   锁的是「两棵树都真的问过这个判据」，不是参数拼写。
  ['门面开关判定', /!landingEnabledForGuest\(/],
  ['营销壳（不带 auth 档）', /<PublicShell>/],
]
/** 登录面唯一调用点内部必须有的口径（两棵树共用，所以只查一次） */
const FACE_MARKERS: Array<[string, RegExp]> = [
  ['登录壳（带 auth 档）', /<PublicShell auth>/],
  ['挂件常驻', /<AiAssist \/>/],
  ['admin 登录形态', /startsWith\('\/admin'\) \? 'admin' : 'home'/],
  ['登录成功后处理', /applyLogin\(u, onLogin, path, navigate\)/],
]
/** D 判据：未登录分诊锚点两处都在；登录面恰一处实现且两棵树都只调它；
 *  且"门面关闭"那一支两侧语义等值（换地址 + 不压历史栈）。 */
function parityFailures(files: Map<string, string>): string[] {
  const bad: string[] = []
  const app = files.get(APP)
  const pub = files.get(PUBLIC_ROOT)
  const face = files.get(LOGIN_FACE)
  const auth = files.get(AUTH_STORE)
  if (app === undefined || pub === undefined) return ['读不到 App.tsx 或 PublicRoot.tsx']
  if (face === undefined) return ['读不到 components/LoginFace.tsx（登录面的唯一调用点没了）']
  const guest = guestRegion(app)
  if (!guest) return ['读不到 App.tsx 的未登录分诊区（锚点漂了）']
  for (const [name, re] of GUEST_MARKERS) {
    if (!re.test(guest)) bad.push(`App.tsx 的未登录分诊区缺「${name}」（判据或实现其一漂了）`)
    if (!re.test(pub)) bad.push(`PublicRoot.tsx 缺「${name}」（与 App 的两支不等值）`)
  }
  for (const [name, re] of FACE_MARKERS) {
    if (!re.test(face)) bad.push(`LoginFace.tsx 缺「${name}」（登录面的口径漂了）`)
  }
  // 唯一调用点：两棵树各恰一次，且**不许**再直接渲染 `<Login`（第二份登录面＝改一面留一面）
  const calls = (src: string) => (src.match(/<LoginFace\b/g) || []).length
  if (calls(guest) !== 1) bad.push(`App.tsx 未登录区必须恰有一处 <LoginFace>，实测 ${calls(guest)}`)
  if (calls(pub) !== 1) bad.push(`PublicRoot.tsx 必须恰有一处 <LoginFace>，实测 ${calls(pub)}`)
  if (/<Login\b/.test(guest) || /<Login\b/.test(pub)) bad.push('两棵树里又直接渲染了 <Login>（登录面出现第二份实现）')
  // 弹回首页那一腿的判定落在 stores/auth.tsx 的 applyLogin 里（Login 内部拦一次 + 这里补第二道）
  if (auth === undefined || !/pathname\.startsWith\('\/admin'\) && roleLevel\(u\.role\) < 2\) navigate\('\/'\)/.test(auth)) {
    bad.push('stores/auth.tsx 的 applyLogin 缺「admin 档非租管弹回首页」那一腿')
  }
  if (!/<Navigate to="\/login" replace \/>/.test(guest)) bad.push('App.tsx 门面关闭分支的写法变了（本锁认 <Navigate to="/login" replace />）')
  if (!/navigate\('\/login', \{ replace: true \}\)/.test(pub)) bad.push('PublicRoot 门面关闭分支必须换地址且不压历史栈（replace: true）')
  // ★ 2026-10-23 租户维度：两棵树都必须把 `branding.tenantId` 喂给判据。
  //   只锁「问过判据」不够——判据自己有两条腿（租户定制页／部署级标记），
  //   传成 0 就等于把租户那条腿**静默摘掉**：源码、tsc、单测全绿，而客户品牌域名上
  //   访客又开始看到平台营销落地页。所以参数本身也是判据的一部分。
  if (!/landingEnabledForGuest\(branding\.tenantId\)/.test(guest)) {
    bad.push('App.tsx 的未登录分诊没把 branding.tenantId 传给门面判据（租户定制页那条腿会静默失效）')
  }
  if (!/landingEnabledForGuest\(branding\.tenantId\)/.test(pub)) {
    bad.push('PublicRoot.tsx 没把 branding.tenantId 传给门面判据（租户定制页那条腿会静默失效）')
  }
  // 登录后换树：公开面树没有工作台，不换就永远停在登录页（Login 自身不感知登录态）
  if (!/if \(user\) navigate\('\/', \{ replace: true \}\)/.test(pub)) bad.push('PublicRoot 缺"登录成功即换树到 App"那一腿')
  return bad
}

describe('A 入口分诊（main.tsx）', () => {
  it('两个入口都 lazy，分诊按 token × 公开路径，白名单与 App 分支同步', () => {
    const bad = entryTriageFailures(FILES)
    expect(bad, bad.join('\n')).toEqual([])
  })
  it('反证：静态 import App／把 /admin 塞进白名单 —— 各自必须判红', () => {
    const m = new Map(FILES)
    m.set(MAIN, (m.get(MAIN) || '').replace(/lazy\(\(\) => import\('\.\/App'\)\)/, "'./App'").replace(/^/, "import A2 from './App'\n"))
    expect(entryTriageFailures(m).length, '静态 import App 没被判红').toBeGreaterThan(0)

    const m2 = new Map(FILES)
    m2.set(MAIN, (m2.get(MAIN) || '').replace("'/compare',", "'/compare', '/admin',"))
    expect(m2.get(MAIN), '反证失效：变异没落地').not.toBe(FILES.get(MAIN))
    expect(entryTriageFailures(m2).join('\n'), '白名单不同步没被判红').toContain('PUBLIC_PATHS')
  })
})

describe('B 入口静态闭包不许碰 react-router', () => {
  it('入口静态闭包不含 App、公开面全量闭包含五个页面分片且 router 导入者为 0', () => {
    const bad = closureFailures(FILES)
    expect(bad, bad.join('\n')).toEqual([])
  })
  it('射程自证：两个闭包都不是空集（规模过小＝判据空转）', () => {
    expect(staticClosure([MAIN], FILES).size).toBeGreaterThanOrEqual(5)
    expect(fullClosure([PUBLIC_ROOT], FILES).size).toBeGreaterThanOrEqual(15)
  })
  it('反证：PublicRoot 引一行 router／main 静态引 App —— 各自必须判红', () => {
    const m = new Map(FILES)
    m.set(PUBLIC_ROOT, (m.get(PUBLIC_ROOT) || '') + "\nimport { useLocation } from 'react-router-dom'\n")
    expect(closureFailures(m).join('\n'), 'PublicRoot 引 router 没被判红').toContain('react-router')

    const m2 = new Map(FILES)
    m2.set(MAIN, (m2.get(MAIN) || '') + "\nimport A3 from './App'\n")
    expect(closureFailures(m2).join('\n'), 'main 静态引 App 没被判红').toContain('出现了 App')

    // 第三条：把某个页面分片（懒加载那一层）加上 router —— ②那条必须抓得到（这是最容易被漏的一层）
    const m3 = new Map(FILES)
    m3.set(SRC + 'components/Landing.tsx', (m3.get(SRC + 'components/Landing.tsx') || '') + "\nimport { useNavigate } from 'react-router-dom'\n")
    expect(closureFailures(m3).join('\n'), '懒加载页面引 router 没被判红').toContain('react-router')
  })
})

describe('C 桥接唯一入口（navRouter / nav）', () => {
  it('navRouter 只许 App.tsx 引；nav.ts 零 router 且回落真跳转', () => {
    const bad = bridgeFailures(FILES)
    expect(bad, bad.join('\n')).toEqual([])
  })
  it('反证：公开面引 navRouter／nav.ts 引 router —— 各自必须判红', () => {
    const m = new Map(FILES)
    m.set(PUBLIC_ROOT, (m.get(PUBLIC_ROOT) || '') + "\nimport { RouterNavBridge } from '@/lib/navRouter'\n")
    expect(bridgeFailures(m).join('\n'), '公开面引 navRouter 没被判红').toContain('必须恰为 App.tsx')

    const m2 = new Map(FILES)
    m2.set(NAV, (m2.get(NAV) || '') + "\nimport { useNavigate } from 'react-router-dom'\n")
    expect(bridgeFailures(m2).join('\n'), 'nav.ts 引 router 没被判红').toContain('react-router')
  })
})

describe('D 公开面与 App 的未登录三支等值', () => {
  it('九个锚点两处都在 + 门面关闭分支语义等值 + 登录后换树那一腿在位', () => {
    const bad = parityFailures(FILES)
    expect(bad, bad.join('\n')).toEqual([])
  })
  it('反证：摘掉 PublicRoot 的门面开关判定／换树腿 —— 各自必须判红', () => {
    const m = new Map(FILES)
    m.set(PUBLIC_ROOT, (m.get(PUBLIC_ROOT) || '').replace('!landingEnabledForGuest(branding.tenantId)', 'false'))
    expect(parityFailures(m).join('\n'), '门面开关判定被摘没被判红').toContain('门面开关判定')

    // ★ 2026-10-23：判据还在、但参数被悄悄改成常量 0 —— 同样必须判红。
    //   这一条针对的是「锁住了调用、没锁住参数」那类退化：租户定制页会静默恢复成平台落地页。
    const mArg = new Map(FILES)
    mArg.set(APP, (mArg.get(APP) || '').replace('landingEnabledForGuest(branding.tenantId)', 'landingEnabledForGuest(0)'))
    expect(mArg.get(APP), '反证失效：参数变异没落地').not.toBe(FILES.get(APP))
    expect(parityFailures(mArg).join('\n'), '参数被改成 0 没被判红').toContain('branding.tenantId')

    const m2 = new Map(FILES)
    m2.set(PUBLIC_ROOT, (m2.get(PUBLIC_ROOT) || '').replace(/if \(user\) navigate\('\/', \{ replace: true \}\)/, 'void user'))
    expect(m2.get(PUBLIC_ROOT), '反证失效：变异没落地').not.toBe(FILES.get(PUBLIC_ROOT))
    expect(parityFailures(m2).join('\n'), '登录后换树腿被摘没被判红').toContain('换树')
  })
})
