// ============================================================================
// lib/captchaProvider.crossSide.lock.test.ts — 「提供方名单与令牌字段」跨语言的等值锁（2026-10-27 多提供方批）
//
// 为什么必须跨语言判：这两组东西都是**两端各写一份**，而它们的失效形态全部是静默的——
//   · 后端 `captchaEnabled()` 多认一个提供方、前端没跟上 ⇒ 后台一开，前端仍按旧的挂件走，
//     token 形状对不上 ⇒ **所有人过不了验证**，而两侧各自的测试全绿（都在测自己那一份）；
//   · 令牌字段名改动（lot_number → lotNumber）⇒ 服务端解析出空对象 ⇒ 判"凭据不完整"，
//     同样是"两端都对，合起来不通"。
// 判据只问**集合相等**，不钉个数、不钉顺序（名单加第三家时这条锁不该先红）：
//   前端真源＝src/lib/captcha.ts 的 `CaptchaProvider` 联合类型；
//   后端真源＝backend-go/internal/api/captcha.go 的 `captchaEnabled()` switch；
//   令牌真源＝src/lib/geetest.ts 的 JSON.stringify 键 ↔ captcha_geetest.go 的 json tag。
// 每个判据都配一枚"改一侧必须判红"的反证，并带射程自证（解析结果为空＝闸门失明，直接红）。
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'

const FRONT_DIR = resolve(__dirname, '..')
const BE_API = resolve(FRONT_DIR, '../../backend-go/internal/api')

const captchaTs = readFileSync(resolve(__dirname, 'captcha.ts'), 'utf8')
const geetestTs = readFileSync(resolve(__dirname, 'geetest.ts'), 'utf8')
const beCaptchaGo = readFileSync(resolve(BE_API, 'captcha.go'), 'utf8')
const beGeetestGo = readFileSync(resolve(BE_API, 'captcha_geetest.go'), 'utf8')

// stripComments 剥注释：两侧的实现说明里就写着 turnstile/geetest 这些词，
// 不剥会让"注释冒充实现"把名单判成齐的（本仓老坑）。
function stripComments(s: string): string {
  return s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

// frontProviders 取前端 `export type CaptchaProvider = 'turnstile' | 'geetest'` 里的字面量集合
function frontProviders(src: string): Set<string> {
  const m = /export type CaptchaProvider\s*=\s*([^\n]+)/.exec(stripComments(src))
  if (!m) return new Set()
  return new Set(Array.from(m[1].matchAll(/'([a-z0-9_-]+)'/g)).map((x) => x[1]))
}

// backProviders 取后端 captchaEnabled() 函数体里 case 之后那串字面量集合
function backProviders(src: string): Set<string> {
  // ⚠️ 下标必须在**原文**上找：函数头之前的那段注释会被 stripComments 删掉，
  // 拿剥后文本的下标去切原文＝整体错位（本批首跑真红在这里，读数还长得像"名单不等值"）。
  const at = src.indexOf('func (s *Server) captchaEnabled()')
  if (at < 0) return new Set()
  const rest = src.slice(at)
  const end = rest.indexOf('\n}\n')
  const body = end > 0 ? rest.slice(0, end) : ''
  return new Set(Array.from(body.matchAll(/"([a-z0-9_-]+)"/g)).map((x) => x[1]))
}

// frontTokenKeys 取极验令牌那段 JSON.stringify 的键集合
function frontTokenKeys(src: string): Set<string> {
  const m = /JSON\.stringify\(\{\s*([^}]+)\}\)/.exec(stripComments(src))
  if (!m) return new Set()
  return new Set(Array.from(m[1].matchAll(/([a-z_]+)\s*:/g)).map((x) => x[1]))
}

// backTokenKeys 取 geetestTokenPayload 结构体上的 json tag 集合
function backTokenKeys(src: string): Set<string> {
  const at = src.indexOf('type geetestTokenPayload struct')
  if (at < 0) return new Set()
  const rest = src.slice(at)
  const end = rest.indexOf('\n}\n')
  const body = end > 0 ? rest.slice(0, end) : ''
  return new Set(Array.from(body.matchAll(/json:"([a-z_]+)"/g)).map((x) => x[1]))
}

// sameSet 集合等值（不钉个数/顺序：加第三家提供方时这条锁不该先红）
function sameSet(a: Set<string>, b: Set<string>): boolean {
  if (a.size === 0 || b.size === 0) return false // 解析为空＝闸门失明，判红而不是"通过"
  if (a.size !== b.size) return false
  for (const v of a) if (!b.has(v)) return false
  return true
}

describe('人机验证提供方与令牌字段的跨语言等值锁', () => {
  it('P1 提供方名单：前端 CaptchaProvider ↔ 后端 captchaEnabled() 逐值相等', () => {
    const f = frontProviders(captchaTs)
    const b = backProviders(beCaptchaGo)
    expect(Array.from(f).sort(), '射程自证：前端名单没解析到').not.toEqual([])
    expect(Array.from(b).sort(), '射程自证：后端名单没解析到').not.toEqual([])
    expect(sameSet(f, b)).toBe(true)
  })

  it('P2 极验令牌字段：前端序列化的键 ↔ 后端 json tag 逐值相等', () => {
    const f = frontTokenKeys(geetestTs)
    const b = backTokenKeys(beGeetestGo)
    expect(Array.from(f).sort(), '射程自证：前端令牌键没解析到').not.toEqual([])
    expect(Array.from(b).sort(), '射程自证：后端 json tag 没解析到').not.toEqual([])
    expect(sameSet(f, b)).toBe(true)
  })

  // ===== 反证：两侧各改一次，必须由同一个判据函数抓住 =====
  it('反证 P1：后端多认一家（前端没跟上）→ P1 判红', () => {
    const mutated = beCaptchaGo.replace('case "turnstile", "geetest":', 'case "turnstile", "geetest", "hcaptcha":')
    expect(stripComments(mutated)).not.toBe(stripComments(beCaptchaGo)) // 防空替换
    expect(sameSet(frontProviders(captchaTs), backProviders(mutated))).toBe(false)
  })

  it('反证 P1b：前端先加一家而后端没有 → P1 判红（两个方向都要抓）', () => {
    const mutated = captchaTs.replace("export type CaptchaProvider = 'turnstile' | 'geetest'",
      "export type CaptchaProvider = 'turnstile' | 'geetest' | 'hcaptcha'")
    expect(stripComments(mutated)).not.toBe(stripComments(captchaTs))
    expect(sameSet(frontProviders(mutated), backProviders(beCaptchaGo))).toBe(false)
  })

  it('反证 P2：前端把 lot_number 写成驼峰 → P2 判红（服务端会解析成空对象）', () => {
    const mutated = geetestTs.replace('{ lot_number: lot, captcha_output: out, pass_token: pass, gen_time: gen }',
      '{ lotNumber: lot, captcha_output: out, pass_token: pass, gen_time: gen }')
    expect(stripComments(mutated)).not.toBe(stripComments(geetestTs))
    expect(sameSet(frontTokenKeys(mutated), backTokenKeys(beGeetestGo))).toBe(false)
  })
})
