// ============================================================================
// e2eRetryGuard.lock.test.ts — ★ 2026-11-04 体验批 §8 挂账的**源码级守卫**
//
// 钉住的那件事：e2e/mobile_uat.spec.ts 里「移动端全站页面无横向溢出巡检」这条用例
// 过去靠 playwright.config.ts 的全局 retries=1 兜过首轮超时（旧注释逐字自认）。
// AGENTS §三 已立判据：passed with retries 是真缺陷伸出的手指——用重试把"慢"盖住，
// 屏幕上是 passed，而 §2 那批提速到底生效没有永远测不出来。
//
// 本批的闭合形态不是把全局 retries 一删了事（那会让其余 103 条 e2e 失去环境抖动兜底，
// 把一次冷启动抖动打成发布闸门红灯），而是**给这一条用例单开例外**：
// 文件内 test.describe.configure({ retries: 0 })。这把锁守的正是这个例外不许被删、
// 不许挪到用例后面（Playwright 要求配置在用例声明之前才生效）、也不许把全局档位
// 悄悄改成 0 来冒充"已闭合"（那是另一形态的假绿）。
//
// 三条腿：
//   G1 例外在位且恰一处；
//   G2 顺序：describe → configure → 巡检用例，三者索引严格递增（锚点漂移即失明）；
//   G3 全局档位仍是 1，且 config 注释点名了这个例外（注释不许比实现新／旧）。
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'

const spec = readFileSync('e2e/mobile_uat.spec.ts', 'utf8')
const cfg = readFileSync('playwright.config.ts', 'utf8')
const CONFIGURE = "test.describe.configure({ retries: 0 })"
const DESCRIBE = "test.describe('移动端全站巡检（本用例禁用重试，慢必须首轮现形）'"
const CASE = "test('移动端全站页面无横向溢出巡检'"

describe('移动端巡检禁用重试的接线（§8 挂账守卫级）', () => {
  it('G1 例外配置在位且恰一处', () => {
    expect(spec.split(CONFIGURE).length - 1, 'retries:0 例外必须恰一处').toBe(1)
    expect(spec, 'describe 包裹体取不到（锚点漂了 ⇒ 本锁失明）').toContain(DESCRIBE)
  })

  it('G2 配置必须在用例声明之前（挪到后面 Playwright 不生效）', () => {
    const d = spec.indexOf(DESCRIBE)
    const c = spec.indexOf(CONFIGURE)
    const t = spec.indexOf(CASE)
    expect(d, 'describe 起点缺失').toBeGreaterThanOrEqual(0)
    expect(c, 'configure 缺失').toBeGreaterThan(d)
    expect(t, '巡检用例标题缺失 ⇒ 本锁已失明').toBeGreaterThan(c)
  })

  it('G3 全局档位仍是 1，且配置注释点名了这个例外', () => {
    expect(cfg).toMatch(/retries:\s*1,/)
    expect(cfg, '全局注释必须说明本例外的存在，否则下一位会按旧口径删掉它').toContain('retries: 0')
    expect(cfg).toContain('e2eRetryGuard.lock.test.ts')
  })
})
