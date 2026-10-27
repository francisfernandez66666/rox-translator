// ============================================================================
// LeadForm.turnstileSize.lock.test.ts — 320 视口「挂件不许顶破页面」源码锁
//
// 背景（2026-10-25 云端 UAT ＋ 2026-10-27 D-02 批复核）：Turnstile normal 固有宽 300px。
//   第一批只修了官网留资那**一个**容器（LeadForm 自己数一遍宽度 + Landing 一条 CSS 兜底），
//   于是「修了一处、其余三处照旧」——登录屏/注册屏/AI 面板的容器**从来没有**宽度约束，
//   `renderTurnstile` 也**从不传 size**（拿到的一律是 300px 硬宽）。
//   复核同批还把选档收敛成唯一实现（lib/turnstile.ts 的 pickTurnstileSize），
//   并摘掉 LeadForm 自己那份脚本注入（那正是 2026-09-24「两方各注入、另一方回调永远等不到」的形态复发）。
//
// 本锁钉四件事（缺一件都算没修好）：
//   ① 选档只有**一处实现**（在 lib），四个消费方一律委托；
//   ② 四处挂件容器都带 CSS 宽度兜底（max-width:100% + overflow-x:auto）；
//   ③ 每个消费方都把 size 传给 Turnstile（不传＝一律 300px）；
//   ④ LeadForm 不许再自己注入 api.js（装载收敛在 lib，全局只注入一次）。
// 反证：摘掉任一条腿，**同一个判据函数**必须判红（判据级代替不了守卫级，见 AGENTS §三）。
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'

const landingSrc = readFileSync(resolve(__dirname, 'Landing.tsx'), 'utf8')
const leadSrc = readFileSync(resolve(__dirname, 'LeadForm.tsx'), 'utf8')
const loginSrc = readFileSync(resolve(__dirname, 'Login.tsx'), 'utf8')
const aiSrc = readFileSync(resolve(__dirname, 'AiRegisterFlow.tsx'), 'utf8')
const libSrc = readFileSync(resolve(__dirname, '../lib/turnstile.ts'), 'utf8')

// stripComments 剥掉 TS/JS 注释：说明注释里就写着 max-width/size/pickTurnstileSize 这些词，
// 不剥会让「注释冒充实现」恒绿（本仓老坑，AGENTS §一·5 那条同源）。
function stripComments(s: string): string {
  return s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
    .replace(/\/\*[\s\S]*?\*\//g, '') // CSS 内注释（内联 style 模板串里只能这么写）
}

// ruleBody 取某条 CSS 规则的规则体；**选择器组要按逗号拆开**
// （兜底写成 `#__ts_widget__,#__ts_login_widget__{...}` 一条规则——同一纪律只写一处，
//   按"两个选择器各写一条"去判会把合法形态读成"规则不存在"，那是判据自伤不是实现缺失）。
// 取不到回空串，调用方必须按「锁失明」判红，不许当"没有这条规则=通过"。
function ruleBody(src: string, selector: string): string {
  const code = stripComments(src)
  const ruleRe = /([^{}]+)\{([^{}]*)\}/g
  let m: RegExpExecArray | null
  while ((m = ruleRe.exec(code)) !== null) {
    const parts = m[1].split(',').map((x) => x.trim()).filter(Boolean)
    if (parts.includes(selector)) return m[2]
  }
  return ''
}

// cssGuardOk 是容器兜底的唯一判据：宽度收住 ＋ 溢出可滚，两者都要在。
function cssGuardOk(body: string): boolean {
  return /max-width:\s*100%/.test(body) && /overflow-x:\s*auto/.test(body)
}

// singlePickerOk 判「选档只有一处实现」：lib 里有导出定义，消费方里**一处都不许**再声明。
// 入参化＝真源与反证夹具走同一个判据函数（AGENTS §三：判据级代替不了守卫级）。
function singlePickerOk(lib: string, consumers: string[]): boolean {
  const declaresInLib = /export function pickTurnstileSize\s*\(/.test(stripComments(lib))
  const redeclared = consumers.some((c) => /function\s+pickTurnstileSize\s*\(/.test(stripComments(c)))
  return declaresInLib && !redeclared
}

// sizePassedOk 判某消费方真的把选出的尺寸交给 Turnstile：
// 既要有选档调用，也要有「渲染入参里带 size」——两种合法写法都放行（`{ size }` 简写 / `{ size: ... }`），
// 只写选档不传下去（＝旧形态，挂件一律拿 300px）必须判红。
function sizePassedOk(src: string): boolean {
  const code = stripComments(src)
  const picks = /pickTurnstileSize\s*\(/.test(code)
  const passes = /[,({]\s*\{\s*size\b\s*[:,}]/.test(code) || /\bsize\s*,/.test(code)
  return picks && passes
}

// noSelfInjectOk 判消费方不再自建脚本注入（装载唯一出口在 lib，全局只注入一次）。
function noSelfInjectOk(src: string): boolean {
  const code = stripComments(src)
  return !code.includes('challenges.cloudflare.com') && !code.includes('__tsLoading')
    && !/document\.createElement\(\s*['"]script['"]\s*\)/.test(code)
}

describe('320 视口挂件宽度锁（选档唯一实现 ＋ 四处容器兜底）', () => {
  it('① 选档只有一处实现：lib 导出它，LeadForm/Login/AiRegisterFlow 都不许再各写一份', () => {
    expect(singlePickerOk(libSrc, [leadSrc, loginSrc, aiSrc])).toBe(true)
  })

  it('② 四处容器都带 CSS 兜底（留资/登录屏/注册屏/AI 面板）', () => {
    const boxes: Array<[string, string]> = [
      ['Landing .lc-lead-captcha', ruleBody(landingSrc, '.lc-lead-captcha')],
      ['Login #__ts_login_widget__', ruleBody(loginSrc, '#__ts_login_widget__')],
      ['Login #__ts_widget__', ruleBody(loginSrc, '#__ts_widget__')],
      ['AiRegisterFlow .ar-ts', ruleBody(aiSrc, '.ar-ts')],
    ]
    for (const [name, body] of boxes) {
      expect(body, `锁失明：读不到 ${name} 的规则体`).not.toBe('')
      expect(cssGuardOk(body), `${name} 缺 max-width:100% 或 overflow-x:auto`).toBe(true)
    }
  })

  it('③ 三个消费方都把 size 传给挂件（不传＝normal 300px 硬宽）', () => {
    for (const [name, src] of [['LeadForm', leadSrc], ['Login', loginSrc], ['AiRegisterFlow', aiSrc]] as const) {
      expect(sizePassedOk(src), `${name} 没有选档或没把 size 传下去`).toBe(true)
    }
  })

  it('④ LeadForm 不再自己注入 api.js（装载收敛 lib：2026-09-24 双注入事故那一族）', () => {
    expect(noSelfInjectOk(leadSrc)).toBe(true)
  })

  // ===== 反证：每条腿各摘一次，必须由**同一个判据函数**抓住 =====
  it('反证 1：把 lib 里的 pickTurnstileSize 改名 → ① 判红（否则这条锁根本不看实现）', () => {
    const mutated = libSrc.replace('export function pickTurnstileSize(', 'export function pickTurnstileSizeRenamed(')
    expect(stripComments(mutated)).not.toBe(stripComments(libSrc)) // 防空替换（no-op 会伪装成"抓住了破坏"）
    expect(singlePickerOk(mutated, [leadSrc, loginSrc, aiSrc])).toBe(false)
  })

  it('反证 2：消费方私自再写一份 pickTurnstileSize → ① 判红', () => {
    const mutated = leadSrc + '\nfunction pickTurnstileSize() { return "normal" }\n'
    expect(singlePickerOk(libSrc, [mutated, loginSrc, aiSrc])).toBe(false)
  })

  it('反证 3：摘掉登录屏容器的 max-width → ② 判红（逐容器各摘一次）', () => {
    const mutated = loginSrc.replace(/(#__ts_widget__[^{]*\{[^}]*)max-width:\s*100%;?/, '$1')
    expect(stripComments(mutated)).not.toBe(stripComments(loginSrc))
    const boxes = [ruleBody(mutated, '#__ts_login_widget__'), ruleBody(mutated, '#__ts_widget__')]
    expect(boxes.some((b) => !cssGuardOk(b))).toBe(true)
  })

  it('反证 4：把 AI 面板的 .ar-ts 换成没有兜底的旧形态 → ② 判红', () => {
    const mutated = aiSrc.replace('.ar-ts{display:flex;justify-content:center;min-height:65px;max-width:100%;overflow-x:auto;}', '.ar-ts{display:flex;justify-content:center;min-height:65px;}')
    expect(stripComments(mutated)).not.toBe(stripComments(aiSrc))
    expect(cssGuardOk(ruleBody(mutated, '.ar-ts'))).toBe(false)
  })

  it('反证 5：把 size 传参摘掉（写回不传档的旧形态）→ ③ 判红', () => {
    const mutated = leadSrc.replace(/, \{ size\s*(:[^}]*)?\}/, '')
    expect(stripComments(mutated)).not.toBe(stripComments(leadSrc))
    expect(sizePassedOk(mutated)).toBe(false)
  })

  it('反证 6：LeadForm 写回自建注入（旧 `__tsLoading` 那份形态）→ ④ 判红', () => {
    // 夹具刻意写成**代码行**（不是注释）：判据先剥注释，所以注释喂不饱它——这正是本锁要的形状
    const mutated = leadSrc + '\nconst __uatOldInjectShape = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit"\n'
    expect(stripComments(mutated)).not.toBe(stripComments(leadSrc))
    expect(noSelfInjectOk(mutated)).toBe(false)
  })
})
