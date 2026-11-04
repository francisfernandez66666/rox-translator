// ============================================================================
// tenantChargeOutcome.test.ts — §4.1「三张脸」判据的穷举行为锁（纯函数，node 环境）。
//
// 六档各一腿 + 三条"不许串档"的判据：
//   O1 建单失败 ⇒ create-failed，且此时**后面的读数一概不看**（喂一份"余额很足"的假读数进去，
//      仍必须是 create-failed——否则就是"前一步没成却替后一步报成功"）
//   O2 置付失败 ⇒ pay-failed，success=false（这条就是本体缺陷：旧形态这里无条件绿）
//   O3 余额读数缺席 ⇒ unverified（既不判成功也不判失败：探针没读数不是客户的钱没进去）
//   O4 余额装不下本单 ⇒ verify-failed
//   O5 全部对上 ⇒ credited（**唯一** success=true 的一档）
//   O6 只有 credited 那一档 success=true（横扫全部组合，数成功档数＝1）
// ============================================================================
import { describe, it, expect } from 'vitest'
import { decideChargeOutcome, type ChargeFacts } from './tenantChargeOutcome'

const base: ChargeFacts = {
  created: true,
  hasOrderId: true,
  payReached: true,
  paySuccess: true,
  balancePoints: 200,
  orderPoints: 100,
}
const f = (o: Partial<ChargeFacts>): ChargeFacts => ({ ...base, ...o })

describe('超管注入资金的六档判定（§4.1/§4.2）', () => {
  it('O1：建单失败 ⇒ create-failed，且后面的读数不参与判定', () => {
    const r = decideChargeOutcome(f({ created: false, balancePoints: 999999, orderPoints: 1 }))
    expect(r.kind).toBe('create-failed')
    expect(r.success).toBe(false)
    expect(r.msgKey).toBe('tenants.chargeCreateFailed')
  })

  it('O1b：建单成功却没订单号（半套回执）也不能继续判到账', () => {
    const r = decideChargeOutcome(f({ hasOrderId: false }))
    expect(r.kind).toBe('no-order-id')
    expect(r.success).toBe(false)
  })

  it('O2：置付失败 / 置付请求没走通 ⇒ 都是 pay-failed，绝不绿（旧形态的缺陷本体）', () => {
    expect(decideChargeOutcome(f({ paySuccess: false })).kind).toBe('pay-failed')
    expect(decideChargeOutcome(f({ payReached: false })).kind).toBe('pay-failed')
    expect(decideChargeOutcome(f({ paySuccess: false })).success).toBe(false)
  })

  it('O3：置付成功但后端没回余额 ⇒ unverified（不是 credited，也不是 verify-failed）', () => {
    const r = decideChargeOutcome(f({ balancePoints: null }))
    expect(r.kind).toBe('unverified')
    expect(r.success).toBe(false)
    // 只缺 order_points 也算没测到（连单向比较都做不了）
    expect(decideChargeOutcome(f({ orderPoints: null })).kind).toBe('unverified')
  })

  it('O4：余额装不下这一笔 ⇒ verify-failed（接口全绿而账上没动的那个形态）', () => {
    const r = decideChargeOutcome(f({ balancePoints: 10, orderPoints: 100 }))
    expect(r.kind).toBe('verify-failed')
    expect(r.success).toBe(false)
    expect(r.msgKey).toBe('tenants.chargeVerifyFail')
  })

  it('O5：余额装得下且两步都成 ⇒ 唯一的 credited', () => {
    const r = decideChargeOutcome(f({ balancePoints: 100, orderPoints: 100 })) // 边界：恰好等于
    expect(r.kind).toBe('credited')
    expect(r.success).toBe(true)
    expect(r.msgKey).toBe('tenants.charged')
  })

  it('O6：横扫全部组合，success=true 的那档**有且只有 credited**', () => {
    const bools = [true, false]
    const nums: Array<number | null> = [null, 0, 100]
    let successCount = 0
    let creditedCount = 0
    const seen = new Set<string>()
    for (const created of bools) {
      for (const hasOrderId of bools) {
        for (const payReached of bools) {
          for (const paySuccess of bools) {
            for (const balancePoints of nums) {
              for (const orderPoints of nums) {
                const r = decideChargeOutcome({ created, hasOrderId, payReached, paySuccess, balancePoints, orderPoints })
                seen.add(r.kind)
                if (r.success) {
                  successCount++
                  expect(r.kind).toBe('credited') // 成功徽标只配挂在 credited 上
                }
                if (r.kind === 'credited') creditedCount++
              }
            }
          }
        }
      }
    }
    // 六档都必须可达（少一档＝某个分支是死码或判据串档）
    expect([...seen].sort()).toEqual(
      ['create-failed', 'credited', 'no-order-id', 'pay-failed', 'unverified', 'verify-failed'].sort(),
    )
    expect(creditedCount).toBeGreaterThan(0)
    expect(successCount).toBe(creditedCount) // 两个数不等＝有一档挂着成功徽标却不是 credited
  })
})
