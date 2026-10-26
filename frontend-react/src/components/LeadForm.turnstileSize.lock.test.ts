// ============================================================================
// LeadForm.turnstileSize.lock.test.ts — P2-01 320 视口溢出的源码级锁
//
// 背景（2026-10-25 云端 UAT）：Turnstile normal 固有宽 300px，320 视口下
// 表单容器从 x=40 起，right=340 ⇒ 页面稳定横向溢出 20px。
//
// 本锁钉住两条互补的实现：
//   ① Landing 的 .lc-lead-captcha 必须带 max-width:100% + overflow-x:auto（CSS 兜底）；
//   ② LeadForm 必须按容器/视口选 Turnstile 尺寸（compact/normal），不能只写死 normal。
// 反证：把 CSS 的 max-width 摘掉 / 把 size 参数摘掉，同一个判据函数必须判红。
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'

// landingSrc/leadSrc 读真源文件；判据只吃源码，不跑浏览器/Turnstile 网络。
const landingSrc = readFileSync(resolve(__dirname, 'Landing.tsx'), 'utf8')
// leadSrc 与 landingSrc 同时现读，避免只改一侧导致判据空转。
const leadSrc = readFileSync(resolve(__dirname, 'LeadForm.tsx'), 'utf8')

// stripComments 剥掉 TS/JS 注释：说明注释里就写着 max-width/size 这些词，
// 不剥会让“注释冒充实现”恒绿（本仓老坑）。
function stripComments(s: string): string {
  return s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

// leadCaptchaRule 取 `.lc-lead-captcha{...}` 规则体；取不到返回空串（调用方判锁失明）。
function leadCaptchaRule(src: string): string {
  const m = /\.lc-lead-captcha\s*\{([^}]*)\}/.exec(stripComments(src))
  return m ? m[1] : ''
}

// leadCaptchaOk 是唯一判据函数：真源与反证夹具喂同一个函数。
function leadCaptchaOk(src: string): boolean {
  const rule = leadCaptchaRule(src)
  return /max-width:\s*100%/.test(rule) && /overflow-x:\s*auto/.test(rule)
}

// turnstileSizeOk 判 LeadForm 是否真的按容器/视口选尺寸（而不是写死 normal）。
function turnstileSizeOk(src: string): boolean {
  const code = stripComments(src)
  // size 允许写成 `size,` 简写或 `size: size`；两种都表达“把选出的尺寸传下去”。
  return /function\s+pickTurnstileSize\s*\(/.test(code) &&
    /\bsize\s*(?::\s*size)?\s*,/.test(code) &&
    code.includes("'compact'") &&
    code.includes("'normal'")
}

// 四条：CSS 兜底、CSS 反证、JS 尺寸选择、JS 反证。
describe('P2-01 320 视口溢出源码锁', () => {
  it('Landing .lc-lead-captcha 必须带 max-width:100% + overflow-x:auto', () => {
    expect(leadCaptchaRule(landingSrc), '锁失明：取不到 .lc-lead-captcha 规则').not.toBe('')
    expect(leadCaptchaOk(landingSrc)).toBe(true)
  })

  it('反证：摘掉 max-width 后同一判据必须判红', () => {
    const mutated = landingSrc.replace(/\.lc-lead-captcha\s*\{([^}]*)\}/, (_m, body: string) =>
      `.lc-lead-captcha{${body.replace(/max-width:\s*100%\s*;?/, '')}}`)
    expect(leadCaptchaOk(mutated)).toBe(false)
  })

  it('LeadForm 必须按容器/视口选 compact/normal 并把 size 传给 Turnstile', () => {
    expect(turnstileSizeOk(leadSrc)).toBe(true)
  })

  it('反证：把 size 参数摘掉后同一判据必须判红', () => {
    const mutated = leadSrc.replace(/\n\s*size\s*(?::\s*size)?\s*,/, '\n')
    expect(turnstileSizeOk(mutated)).toBe(false)
  })
})
