// ============================================================================
// lib/turnstileLoad.lock.test.ts — D-02「装载窗口不许静默放弃」源码锁（2026-10-27 批）
//
// 现网/本机实证形态：`/login` 上 Cloudflare 那一跳 **+13.998s** 才 200 到达、
//   `window.turnstile` **+14.987s** 才出现，而改造前的装载器是「150ms×80 拍＝12s 到点
//   `clearInterval` 走人」——onReady 永不触发 ⇒ 容器永远空壳 ⇒ 访客点「发送验证码」
//   只撞一句「请先完成人机验证」，而那一句指着一个永远不会自己出现的框（零出路）。
//
// ★ 2026-10-27 多提供方批：等待策略已从 lib/turnstile.ts 抽到 lib/captchaLoad.ts
//   （极验与 CF 共用同一把尺子），所以 **L1~L5 的靶子文件跟着搬家**。
//   搬家后必须重新证明射程（AGENTS §三④ 那条）：每条腿仍吃真源，
//   且每条都配「故意破坏 → 同一个判据函数判红」的反证；空替换一律先证伪。
// 本锁钉的是这条链的**形状**，十二条腿一一对应一个失败模式：
//   L1 截止不许是单常数：必须"默认档＋慢链档＋硬上限"三档并按 `navigator.connection` 选；
//   L2 「到期时链路还在动」必须再等一拍（判据是 Resource Timing 条目数的**变化**）；
//   L3 放弃必须**出声**且**先停表后出声**（顺序锁按同一函数体内首末比，AGENTS §三 那条）；
//   L4 「重试」必须真的重新起表（摘旧脚本＋重注入＋重开轮询），不是只改一句文案；
//   L5 脚本晚到要追得回来（`load`），当场被拒要当场出声（`error`）——「慢」与「坏」分开报；
//   L6 挂件尺寸必须由渲染调用带下去（不传＝一律 normal 300px，见同批的宽度锁）；
//   L7 每个消费方都要有出路：`unavailable` 分支 + 一个真按得下去的 `retryTurnstile()`；
//   L8/L9 **两家脚本源各只许出现在 lib 一处**（按 src/ 全集判，不按点位判）；
//   L10 等待尺子只有一把：提供方文件不许再声明那几个时间常数、也不许自带轮询；
//   L11 分发口必须把 size 真传进 renderTurnstile（消费方选了档、分发口不传＝照样 300px 硬宽）；
//   L12 消费方与各门面不许各自调 SDK（渲染只走 lib/captcha 那一个口）。
// 每条腿都配「故意破坏 → 必须由同一个判据函数判红」的反证；空替换（new==old）一律先证伪。
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'fs'
import { resolve, join } from 'path'

const SRC_ROOT = resolve(__dirname, '..')
const loadSrc = readFileSync(resolve(__dirname, 'captchaLoad.ts'), 'utf8')
const libSrc = readFileSync(resolve(__dirname, 'turnstile.ts'), 'utf8')
const gtSrc = readFileSync(resolve(__dirname, 'geetest.ts'), 'utf8')
const dispSrc = readFileSync(resolve(__dirname, 'captcha.ts'), 'utf8')
// name 存**绝对路径**：反证夹具要靠它和全集聚合里的同一条目对齐，
// 存相对路径会让"我以为改了、其实没改"——那种反证是假反证（本仓老坑）。
const consumers = ['components/LeadForm.tsx', 'components/Login.tsx', 'components/AiRegisterFlow.tsx']
  .map((p) => ({ name: join(SRC_ROOT, p), code: readFileSync(join(SRC_ROOT, p), 'utf8') }))
// carriers：会拿到 register-config 并向下游传 provider 的面（多一处＝少一处"各家自判提供方"）
const carriers = [...consumers, {
  name: join(SRC_ROOT, 'components/ProfileSetupGate.tsx'),
  code: readFileSync(join(SRC_ROOT, 'components/ProfileSetupGate.tsx'), 'utf8'),
}]

// stripComments 剥 TS/JS 注释与 CSS 块注释：判据问的是"代码里有没有这段修法"，
// 而本仓的老坑正是「说明注释里写着那个词 ⇒ 负向恒真/正向恒绿」。
function stripComments(s: string): string {
  return s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

// bodyOf 按**花括号配平**切出某个函数体的原文。
// ⚠️ 不能用"下一个顶层声明"切法：本仓代码风格下它会切过头（AGENTS §一·5 那条真踩记录同族），
//    切过头＝把下一条函数的腿算进来，反证会红错地方、正则会假绿。
function bodyOf(code: string, decl: string): string {
  const at = code.indexOf(decl)
  if (at < 0) return ''
  // 先跳过参数表：多行参数里也可能有 `{ size?: T }` 这种**对象类型**，
  // 直接找"decl 之后的第一个左花括号"会切进参数表（L6 首跑就是这么假红的）
  let cur = at, pd = 0
  while (cur < code.length) {
    if (code[cur] === '(') pd++
    else if (code[cur] === ')') { pd--; if (pd === 0) break }
    cur++
  }
  const open = code.indexOf('{', cur + 1)
  if (open < 0) return ''
  let depth = 0
  for (let i = open; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') {
      depth--
      if (depth === 0) return code.slice(open, i + 1)
    }
  }
  return ''
}

// mutateIn 只改**指定函数体内**那一段，再拼回原文。
// 直接 src.replace 的风险：同一串字面量在别的函数里也有（本锁就要处理
// stopPoll→publishState 这一对在 tick 与 error 腿里各出现一次），改了另一处＝反证打空。
function mutateIn(src: string, decl: string, from: string, to: string, where: string): string {
  // ⚠️ 这里**不能**先剥注释：body 之后要 `src.replace(body, nb)` 拼回原文，
  // 而"剥了注释的那一段"不是原文的子串（tick 体内就有中文注释行），拼不回＝变异静默失效、
  // 反证假绿（本批首跑真踩：三条反证"改了却没判红"，红的其实是夹具）。
  const body = bodyOf(src, decl)
  expect(body, `夹具错：${decl} 的函数体取不到（锚漂了）`).not.toBe('')
  const nb = body.replace(from, to)
  expect(nb, `反证夹具无效：${where} 没改动任何字节`).not.toBe(body)
  return src.replace(body, nb)
}

// L1：三档截止＋按链路选档；旧的单常数 80 拍形态不许复活
function adaptiveDeadlineOk(lib: string): boolean {
  const c = stripComments(lib)
  return /BASE_DEADLINE_MS\s*=/.test(c) && /SLOW_DEADLINE_MS\s*=/.test(c) && /HARD_LIMIT_MS\s*=/.test(c)
    && /connection/.test(c) && /effectiveType/.test(c)
    && !/const WAIT_TICKS\s*=/.test(c)
}

// L2：延长那一拍的判据必须是"条目数变了"，不是"再睡一会儿"
function extendBeatOk(lib: string): boolean {
  const body = bodyOf(stripComments(lib), 'function tick')
  return body !== '' && /scriptEntryCount\s*\(/.test(body) && /deadlineAt\s*=\s*now\s*\+\s*EXTEND_MS/.test(body)
}

// L3：放弃 = 先停表、后出声（顺序反了＝既没人再探也没人知道）
function giveUpAudibleOk(lib: string): boolean {
  const body = bodyOf(stripComments(lib), 'function tick')
  const stop = body.indexOf('stopPoll()')
  const announce = body.indexOf("publishState('unavailable')")
  return stop >= 0 && announce >= 0 && stop < announce
}

// L4：重试必须重新起表（摘旧脚本 + 重注入 + 重开轮询），不是只把红字再显示一次
function retryArmedOk(lib: string): boolean {
  const body = bodyOf(stripComments(lib), 'function retryChannel')
  return body !== '' && body.includes('ensureScript(true)') && body.includes('startPoll()')
}

// L5：晚到要追回来（load）、当场被拒要当场出声（error）——「慢」与「坏」分开报的落地位置
function lateArrivalOk(lib: string): boolean {
  const c = stripComments(lib)
  return /addEventListener\(\s*'load'/.test(c) && /addEventListener\(\s*'error'/.test(c)
    && /publishState\('unavailable'\)/.test(bodyOf(c, 'function ensureScript'))
}

// renderOptsObject 取 `ts.render(el, { ... })` 里那个**入参对象**的原文（花括号配平）。
// 判"size 传没传"必须问那一个对象，不能问函数体——函数体里 `const size = ...` 到处算命中，
// 反证会把"摘掉入参"读成"仍然合规"（本轮首跑真踩：反证 L6 假绿）。
function renderOptsObject(body: string): string {
  const at = body.indexOf('ts.render(')
  if (at < 0) return ''
  const open = body.indexOf('{', at)
  if (open < 0) return ''
  let d = 0
  for (let i = open; i < body.length; i++) {
    if (body[i] === '{') d++
    else if (body[i] === '}') { d--; if (d === 0) return body.slice(open, i + 1) }
  }
  return ''
}

// L6：size 必须进 ts.render 的**入参对象**（不传档＝一律 normal 300px 硬宽）
function sizeIntoRenderOk(lib: string): boolean {
  const body = bodyOf(stripComments(lib), 'export function renderTurnstile')
  const opts = renderOptsObject(body)
  return opts !== '' && /\bsize\b\s*[:,}]/.test(opts)
}

// L7：每个消费方都要有「unavailable 那一张脸 + 按得下去的重试」两半，缺一半就是零出路。
// ★ 2026-10-27 靶子搬家：重试入口现在可以是分发口的 `retryCaptcha(provider)`，
//   也可以是直给的 `retryTurnstile()`／`retryGeetest()`——问的是"有没有按下重试"，不是函数名。
function consumersHaveWayOutOk(list: Array<{ name: string; code: string }>): boolean {
  return list.every(({ code }) => {
    const c = stripComments(code)
    return c.includes('unavailable') && /retry(Captcha|Turnstile|Geetest)\s*\(/.test(c)
  })
}

// L8：脚本源只许在 lib 一处（按 src/ 全集扫，测试件与被 mock 的桩不算实现面）
function collectImplSources(): Array<{ name: string; code: string }> {
  const out: Array<{ name: string; code: string }> = []
  const walk = (dir: string) => {
    for (const f of readdirSync(dir)) {
      const full = join(dir, f)
      const st = statSync(full)
      if (st.isDirectory()) { walk(full); continue }
      if (!/\.(ts|tsx)$/.test(f) || /\.test\.|\.spec\./.test(f)) continue
      out.push({ name: full, code: readFileSync(full, 'utf8') })
    }
  }
  walk(SRC_ROOT)
  return out
}

// singleDomainOk：某家第三方脚本源在**整个 src/ 实现件**里只许出现一次，且落在期望的那份 lib 文件里。
// 判据按全集判（不是"看一眼 LeadForm 有没有"）：抄到任何一处都会红，包括将来新增的面。
function singleDomainOk(files: Array<{ name: string; code: string }>, domain: string, expectFile: string): boolean {
  const hits = files.filter((f) => stripComments(f.code).includes(domain))
  return hits.length === 1 && hits[0].name.endsWith(expectFile)
}

// L10：等待尺子只有一把——提供方文件不许再声明那几个时间常数，也不许自带轮询。
const TIMING_DECLS = ['BASE_DEADLINE_MS', 'SLOW_DEADLINE_MS', 'HARD_LIMIT_MS', 'EXTEND_MS']
function oneRulerOk(providerFiles: Array<{ name: string; code: string }>): boolean {
  const redeclared = providerFiles.some(({ code }) => {
    const c = stripComments(code)
    return TIMING_DECLS.some((n) => new RegExp(`${n}\\s*=`).test(c)) || /setInterval\(/.test(c)
  })
  // 建通道那一行带**泛型实参**（`createLoadChannel<TurnstileApi>({...})`），
  // 所以判据必须允许中间夹 `<...>`；只写 `createLoadChannel\(` 会读成"没走通道"（本批首跑真红在这里）。
  const usesChannel = providerFiles.every(({ code }) => /=\s*createLoadChannel\s*(<[^>]+>)?\(/.test(stripComments(code)))
  // 反向对照（这条锁的射程自证）：真源里那四个常数**必须**各命中一次——
  // 万一锚点漂了（改名/挪走），stripComments 会返回空串让 redeclared 恒假，
  // 于是"闸门失明"会被读成"尺子只有一把"。判据不许拿"读不到"当通过。
  const anchorLive = TIMING_DECLS.every((n) => new RegExp(`${n}\\s*=`).test(stripComments(loadSrc)))
  return !redeclared && usesChannel && anchorLive
}

// L11：分发口必须把 size 真传进 renderTurnstile——消费方选了档、分发口不传＝照样 300px 硬宽，
// 而只看消费方的锁会一路绿灯（判据级代替不了守卫级，AGENTS §三 那条）。
function sizeForwardedOk(disp: string): boolean {
  const body = bodyOf(stripComments(disp), 'export function mountCaptcha')
  const at = body.indexOf('renderTurnstile(')
  if (at < 0) return false
  const open = body.indexOf('{', at)
  if (open < 0) return false
  return /\bsize\b\s*[:,}]/.test(body.slice(open, open + 200))
}

// L12：消费方与各门面不许自己调 SDK 渲染（渲染只走 lib/captcha 那一个口）。
function noDirectSdkOk(list: Array<{ name: string; code: string }>): boolean {
  return list.every(({ code }) => {
    const c = stripComments(code)
    return !/renderTurnstile\s*\(/.test(c) && !/initGeetest4\s*\(/.test(c) && !/renderGeetest\s*\(/.test(c)
  })
}

// 反证通用夹具：改一处喂**同一个判据函数**，并先证明"真的改动过"（空替换会伪装成抓住了破坏）
function mutate(src: string, from: string, to: string, where: string): string {
  const out = src.replace(from, to)
  expect(out, `反证夹具无效：${where} 没改动任何字节`).not.toBe(src)
  return out
}

describe('D-02 装载链 ＋ 多提供方形状源码锁', () => {
  it('L1 截止按链路自适应（三档都在，且读 navigator.connection）', () => {
    expect(adaptiveDeadlineOk(loadSrc)).toBe(true)
  })

  it('L2 到期时链路还在动就再等一拍（判据＝Resource Timing 条目数变化）', () => {
    expect(extendBeatOk(loadSrc)).toBe(true)
  })

  it('L3 放弃必须出声，且顺序是「先停表 → 后出声」', () => {
    expect(giveUpAudibleOk(loadSrc)).toBe(true)
  })

  it('L4 重试是真重起表（ensureScript(true) + startPoll()）', () => {
    expect(retryArmedOk(loadSrc)).toBe(true)
  })

  it('L5 晚到追得回、被拒当场出声（load/error 两个事件腿都在）', () => {
    expect(lateArrivalOk(loadSrc)).toBe(true)
  })

  it('L6 size 进 ts.render 入参', () => {
    expect(sizeIntoRenderOk(libSrc)).toBe(true)
  })

  it('L7 三个消费方各有 unavailable 那张脸 + 按得下去的重试', () => {
    expect(consumersHaveWayOutOk(consumers)).toBe(true)
  })

  it('L8 CF 脚本源全集扫描：只许 lib/turnstile.ts 一处（LeadForm 那份自建注入已摘）', () => {
    const files = collectImplSources()
    expect(files.length, '射程自证：src/ 实现件读不到就是闸门失明').toBeGreaterThan(100)
    expect(singleDomainOk(files, 'challenges.cloudflare.com', 'lib/turnstile.ts')).toBe(true)
  })

  it('L9 极验脚本源全集扫描：只许 lib/geetest.ts 一处', () => {
    expect(singleDomainOk(collectImplSources(), 'static.geetest.com', 'lib/geetest.ts')).toBe(true)
  })

  it('L10 等待尺子只有一把：两家提供方文件都不自带时间常数与轮询', () => {
    expect(oneRulerOk([{ name: 'lib/turnstile.ts', code: libSrc }, { name: 'lib/geetest.ts', code: gtSrc }])).toBe(true)
  })

  it('L11 分发口把 size 真传进 renderTurnstile（消费方选档不白选）', () => {
    expect(sizeForwardedOk(dispSrc)).toBe(true)
  })

  it('L12 消费方与 ProfileSetupGate 都不自己调 SDK（渲染只走 lib/captcha）', () => {
    expect(noDirectSdkOk(carriers)).toBe(true)
  })

  // ===================== 反证：逐腿摘一次 =====================
  it('反证 L1：把三档写回单个 WAIT_TICKS → L1 判红', () => {
    const mutated = mutate(loadSrc, 'const BASE_DEADLINE_MS = 12_000', 'const WAIT_TICKS = 80', 'L1')
    expect(adaptiveDeadlineOk(mutated)).toBe(false)
  })

  it('反证 L2：把"再等一拍"改回直接放弃 → L2 判红', () => {
    const mutated = mutateIn(loadSrc, 'function tick', 'deadlineAt = now + EXTEND_MS', '/* 不再延长 */ void EXTEND_MS', 'L2')
    expect(extendBeatOk(mutated)).toBe(false)
  })

  it('反证 L3：把出声挪到停表之前（先宣布再停表＝没人再探也没人知道）→ L3 判红', () => {
    const mutated = mutateIn(loadSrc, 'function tick', "stopPoll()\n    publishState('unavailable')",
      "publishState('unavailable')\n    stopPoll()", 'L3')
    expect(giveUpAudibleOk(mutated)).toBe(false)
  })

  it('反证 L4：重试只重开轮询、不重注入脚本 → L4 判红（失败那次永远不会翻身）', () => {
    const mutated = mutateIn(loadSrc, 'function retryChannel', 'ensureScript(true)', 'void 0', 'L4')
    expect(retryArmedOk(mutated)).toBe(false)
  })

  it('反证 L5：摘掉 load 追回事后（脚本 18s 到了界面还说"加载不出来"）→ L5 判红', () => {
    const mutated = mutateIn(loadSrc, 'function ensureScript', "s.addEventListener('load'", "s.addEventListener('late'", 'L5')
    expect(lateArrivalOk(mutated)).toBe(false)
  })

  it('反证 L6：把 size 从 render 入参里摘掉 → L6 判红', () => {
    const mutated = mutateIn(libSrc, 'export function renderTurnstile', '    sitekey: siteKey,\n    size,', '    sitekey: siteKey,', 'L6')
    expect(sizeIntoRenderOk(mutated)).toBe(false)
  })

  it('反证 L7：消费方只留文案、把重试钮摘掉 → L7 判红', () => {
    const lead = consumers.find((c) => c.name.endsWith('LeadForm.tsx'))
    expect(lead, '夹具错：找不到 LeadForm').toBeTruthy()
    const mutated = mutate(lead!.code, 'onClick={() => retryCaptcha(captchaProvider)}', 'onClick={() => {}}', 'L7')
    expect(consumersHaveWayOutOk([{ name: lead!.name, code: mutated }, ...consumers.filter((c) => c !== lead)])).toBe(false)
  })

  it('反证 L8：把 CF 脚本源抄回某个消费方（第二处注入）→ L8 判红', () => {
    const lead = consumers.find((c) => c.name.endsWith('LeadForm.tsx'))
    expect(lead, '夹具错：找不到 LeadForm').toBeTruthy()
    const files = collectImplSources().map((f) =>
      f.name === lead!.name ? { ...f, code: f.code + '\nconst __uatSecondSrc = "https://challenges.cloudflare.com/x"\n' } : f)
    expect(singleDomainOk(files, 'challenges.cloudflare.com', 'lib/turnstile.ts')).toBe(false)
  })

  it('反证 L9：把极验脚本源抄进分发口（第二家也长出第二处注入）→ L9 判红', () => {
    const files = collectImplSources().map((f) =>
      f.name.endsWith('lib/captcha.ts') ? { ...f, code: f.code + '\nconst __uatGtSrc = "https://static.geetest.com/v4/gcaptcha4.js"\n' } : f)
    expect(singleDomainOk(files, 'static.geetest.com', 'lib/geetest.ts')).toBe(false)
  })

  it('反证 L10：极验自带一份时间常数（下一批改截止只改一家）→ L10 判红', () => {
    const mutated = gtSrc + '\nconst BASE_DEADLINE_MS = 9_000\n'
    expect(stripComments(mutated)).not.toBe(stripComments(gtSrc))
    expect(oneRulerOk([{ name: 'lib/turnstile.ts', code: libSrc }, { name: 'lib/geetest.ts', code: mutated }])).toBe(false)
  })

  it('反证 L11：分发口把 size 传参摘掉（消费方选了档也没用）→ L11 判红', () => {
    const mutated = mutateIn(dispSrc, 'export function mountCaptcha', ', { size: opts?.size ?? pickTurnstileSize(el) }', '', 'L11')
    expect(sizeForwardedOk(mutated)).toBe(false)
  })

  it('反证 L12：消费方直接调 renderTurnstile 绕过分发口 → L12 判红', () => {
    const login = carriers.find((c) => c.name.endsWith('Login.tsx'))
    expect(login, '夹具错：找不到 Login').toBeTruthy()
    const mutated = login!.code + '\nfunction __uatDirect() { return renderTurnstile(document.body, "k", () => {}) }\n'
    expect(noDirectSdkOk([{ name: login!.name, code: mutated }, ...carriers.filter((c) => c !== login)])).toBe(false)
  })
})
