// ============================================================================
// quoteFmt.test.ts — 多币种报价展示工具（★ #75，2026-09-23）纯函数单测
// 钉死三条口径：
//   ① CNY/空币种走历史 ¥ 原口径（逐字 `¥${value}`，不加强格式化——定价页/商店卡
//     既有渲染结果和 dom 断言都依赖它，报价功能上线不许改变 CNY 用户的任何像素）；
//   ② JPY/KRW 零位小数（无辅币单位币种），其余币种 2 位；CHF 用「CHF 」前缀；
//   ③ cnyToQuote fail-closed：倍率非法（0/负数/缺配置）原样返回人民币值——
//     前端永不拿臆测倍率凑数（与服务端 QuoteCurrencyConfig.Resolve 回落 CNY 同思路）。
// 运行：npx vitest run src/components/quoteFmt.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { fmtQuoteMoney, cnyToQuote, quoteSymbol } from './quoteFmt'

describe('quoteFmt · 报价展示格式化（#75）', () => {
  it('① CNY 与空币种：原样 ¥ 口径，不做二次格式化', () => {
    expect(fmtQuoteMoney(21.6, 'CNY')).toBe('¥21.6')
    expect(fmtQuoteMoney(99, '')).toBe('¥99')
    expect(fmtQuoteMoney(21.6, 'CNY')).not.toContain('21.60') // 千分位/补零都不许动老口径
  })

  it('② 非 CNY：常规币种 2 位小数；JPY/KRW 零位并加千分位；CHF 前缀；白名单外回落币种码', () => {
    expect(fmtQuoteMoney(3, 'USD')).toBe('$3.00')
    expect(fmtQuoteMoney(13.755, 'EUR')).toBe('€13.76') // 展示层四舍五入到分位
    expect(fmtQuoteMoney(18333.4, 'JPY')).toBe('¥18,333')
    expect(fmtQuoteMoney(1350, 'KRW')).toBe('₩1,350')
    expect(fmtQuoteMoney(12.5, 'CHF')).toBe('CHF 12.50')
    expect(quoteSymbol('XXX')).toBe('XXX ') // 未知币种不报错：报价展示坏了不该拖红整页
  })

  it('③ cnyToQuote：除法折算 2 位；倍率非法一律原值返回（fail-closed）', () => {
    expect(cnyToQuote(21.6, 7.2)).toBe(3)
    expect(cnyToQuote(100, 3)).toBe(33.33)
    expect(cnyToQuote(50, 0)).toBe(50)
    expect(cnyToQuote(50, -7)).toBe(50)
    expect(cnyToQuote(50, NaN)).toBe(50)
  })
})
