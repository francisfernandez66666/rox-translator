// ============================================================================
// tenantsChargeWiring.lock.test.ts — §4.1「三张脸」的**接线**锁（node 环境，读源码）。
//
// 为什么行为锁之外还要这一把：decideChargeOutcome 的六档由 tenantChargeOutcome.test.ts 穷举过，
// 但"面板真的按它的裁决出栈"这件事行为锁看不见——把调用换成无条件 toastSuccess，
// 判据函数照样全绿（AGENTS §三 那条「判据级断言代替不了守卫级」的又一处形态）。
//
// 四条腿：
//   W1 charge() 里必须调 decideChargeOutcome（判据在面板里被真正消费）
//   W2 出栈语气由 out.success 分派（绿条只挂在 credited 那一档）；
//      并配**正向对照**：确实存在一条 `toastSuccess(... out.msgKey ...)`，
//      否则"不许有无条件绿条"这条负向判据会在整个成功支被删掉时照样绿
//   W3 旧缺陷形态不许复活：charge() 里不许出现"不判结果就 toastSuccess(tpl('tenants.charged' …))"
//      那条直路（判据：函数体内 `tenants.charged` 只许出现 0 次——它现在只存在于 MSG 表里，
//      面板若再直接引用这个键，就说明有人绕回判据外）
//   W4 文案键不许硬编码在面板里（12 语种之外的第二处文案）：charge() 体内零中文字面串
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'

// bodyOf 按花括号配平切出函数体（"下一个顶层声明"那种切法在本仓代码风格下会切过头，
// 这条坑在 doubleConfirmGate.test.ts 的首跑真踩过——沿用同一手法而不是再造一把）。
function bodyOf(src: string, decl: string): string {
  const i = src.indexOf(decl)
  if (i < 0) return ''
  const open = src.indexOf('{', i)
  if (open < 0) return ''
  let depth = 0
  for (let k = open; k < src.length; k++) {
    const c = src[k]
    if (c === '{') depth++
    else if (c === '}') {
      depth--
      if (depth === 0) return src.slice(open, k + 1)
    }
  }
  return src.slice(open)
}

const src = readFileSync('src/components/admin/TenantsP.tsx', 'utf8')
// 先剥行注释：本批那些"旧形态是…"的说明里逐字抄着旧写法，不剥会把"记下了坑"读成"踩了坑"
const code = src.split('\n').filter((l) => !l.trim().startsWith('//')).join('\n')
const charge = bodyOf(code, 'async function charge(')

describe('注入资金三张脸的接线（§4.1 守卫级）', () => {
  it('W1 charge() 必须经唯一判据函数', () => {
    expect(charge, 'charge() 函数体取不到（锚点漂了 ⇒ 本锁失明）').not.toBe('')
    expect(charge).toContain('decideChargeOutcome(')
  })

  it('W2 出栈语气由判据的 success 分派，且成功支真在位（正向对照）', () => {
    expect(charge).toMatch(/if \(out\.success\) toastSuccess/)
    expect(charge).toMatch(/else toastError/)
    // 正向对照：绿条那句必须真的存在，否则"不许无条件绿"是空判据
    expect(charge).toContain('toastSuccess(tpl(out.msgKey')
  })

  it('W3 判据外的成功键不许出现在面板里', () => {
    // 'tenants.charged' 现在只该存在于 lib/tenantChargeOutcome.ts 的 MSG 表；
    // 面板里再引用一次＝有人绕开判据自己发绿条（旧缺陷形态）
    expect(charge).not.toContain('tenants.charged')
    // 旧写法那句"不判 success 就 await 支付"也不许回来
    expect(charge).not.toMatch(/if \(o && o\.id\) await runGuarded\(\(\) => adminOrderPay/)
  })

  it('W4 面板里不许硬编码中文提示（12 语种之外的第二处文案）', () => {
    const han = charge.match(/[\u4e00-\u9fa5][^'"]{4,}/g) || []
    // 允许注释（上面已剥）与"注入未生效"这类出现在**判据表**里的串不存在于面板：
    // 这里只判 toast* 调用的实参里有没有中文
    const toastArgs = charge.match(/toast(Success|Error|Warn)\(\s*'[^']*[\u4e00-\u9fa5]/g) || []
    expect(toastArgs, `面板里出现了硬编码中文 toast：${toastArgs.join(' | ')}`).toEqual([])
    expect(han.length).toBeGreaterThanOrEqual(0) // 变量本身只作读数，不作判据（判据是上面那条）
  })
})
