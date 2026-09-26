// ============================================================================
// appPkgLineO9.test.ts — 顶栏积分行「平台上下文守卫」源码级锁（★ O-9，〇-U 补断言）
//
// 为什么是源码锁而不是 dom 锁（子代理实测结论，2026-09-27）：
//   refreshPkgLine 挂在 App.tsx 的 FrontShell 里，FrontShell 未导出且整套
//   Provider/Router/lazy 壳都要拉起才能在 jsdom 里触达这一个回调——成本远超收益。
//   行为面已由 ChatWindow.platformBilling.dom.test.tsx 锁住（同一判据函数
//   isPlatformBillingContext 的正反三态）；本锁补最后一层：**钉调用点在场**，
//   谁把守卫删了/挪了，这里先红，而不是等线上超管再看到「余额 0 积分」胶囊。
//
// 判据（全部在 refreshPkgLine 函数体射程内）：
//   ① 守卫调用 isPlatformBillingContext(user.role) 存在；
//   ② 守卫命中时清空 pkgLine 与 depleted 并 return（`setPkgLine(''); setDepleted(false); return`）
//      ——只 setPkgLine('') 不置 depleted 会点亮「余额不足」横幅，正是 O-9 的原始症状；
//   ③ 守卫必须位于 setPkgLine(gtpl(...)) 渲染赋值**之前**（写在后面等于没写）。
// ============================================================================
import { readFileSync } from 'fs'
import { resolve } from 'path'
import { describe, it, expect } from 'vitest'

const src = readFileSync(resolve(__dirname, 'App.tsx'), 'utf-8')

// 取 refreshPkgLine 函数体（从声明到本文件的 `}, [user])` 依赖表闭合为止）
const start = src.indexOf('const refreshPkgLine')
const end = start >= 0 ? src.indexOf('}, [user])', start) : -1
const body = start >= 0 && end > start ? src.slice(start, end) : ''

describe('App.tsx · O-9 顶栏积分行平台上下文守卫（源码锁）', () => {
  it('refreshPkgLine 函数体可被本锁圈定（形态漂移即红，逼锁随重构同步）', () => {
    expect(start, '未找到 const refreshPkgLine 声明').toBeGreaterThanOrEqual(0)
    expect(end, '未找到 `}, [user])` 闭合——依赖表形态变了，请同步本锁的圈定逻辑')
      .toBeGreaterThan(start)
    expect(body.length, '函数体圈定异常（空或倒挂）').toBeGreaterThan(50)
  })

  it('① 平台上下文守卫调用在场（isPlatformBillingContext）', () => {
    expect(body).toContain('isPlatformBillingContext(user.role)')
  })

  it('② 守卫命中必须同时清 pkgLine 与 depleted 并 return（防「余额不足」横幅误亮）', () => {
    expect(body).toMatch(/isPlatformBillingContext\(user\.role\)\)\s*\{\s*setPkgLine\(''\);\s*setDepleted\(false\);\s*return\s*\}/)
  })

  it('③ 守卫在渲染赋值之前（写在 setPkgLine(gtpl…) 后面＝形同虚设）', () => {
    const guardAt = body.indexOf('isPlatformBillingContext(user.role)')
    const renderAt = body.indexOf('setPkgLine(gtpl(')
    expect(guardAt, '守卫缺失').toBeGreaterThanOrEqual(0)
    expect(renderAt, '渲染赋值缺失').toBeGreaterThanOrEqual(0)
    expect(guardAt, '守卫必须前置于渲染赋值').toBeLessThan(renderAt)
  })
})
