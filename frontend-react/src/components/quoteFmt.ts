// ============================================================================
// components/quoteFmt.ts — 多币种报价展示工具（★ #75，2026-09-23）
// 职责：把「报价币种 + 金额」渲染成客户可读的本币文案（$13.75 / ₩18,000），
//       以及收银台把人民币金额按倍率折算成本币展示值。
// ★ 口径红线：本文件只做展示层格式化——结算事实源恒为人民币（微信/支付宝收单
//   报文币种不可变），换算结果不参与任何资金判定；服务端 store/currency.go 的
//   fail-closed（缺倍率回落 CNY）先行，前端只在拿到非 CNY 报价时才会走到这里。
// 币种符号表与后端白名单（store.SupportedQuoteCurrencies）对齐；未收录币种
// 回落「币种码 + 空格」裸展示，绝不报错——报价展示坏了不该把定价页整页拖红。
// ============================================================================

// QUOTE_ZERO_DECIMAL 无辅币单位币种：JPY/KRW 日常价签不写小数，按 0 位取整展示。
const QUOTE_ZERO_DECIMAL = new Set(['JPY', 'KRW'])

import { fmtInt } from '../lib/format'

// QUOTE_SYMBOLS 币种符号（CHF 无常用单符，按国际惯例前缀 "CHF "）。
const QUOTE_SYMBOLS: Record<string, string> = {
  CNY: '¥', USD: '$', EUR: '€', JPY: '¥', GBP: '£', HKD: 'HK$',
  KRW: '₩', SGD: 'S$', AUD: 'A$', CAD: 'C$', CHF: 'CHF ', THB: '฿',
}

// quoteSymbol 取币种展示前缀；白名单外币种回落「币种码 + 空格」。
export function quoteSymbol(code: string): string {
  return QUOTE_SYMBOLS[code] || `${code} `
}

// fmtQuoteMoney 格式化报价币种金额：CNY/空币种走既有 ¥ 口径（原样数值，不加重格式化，
// 防止改动历史渲染结果）；JPY/KRW 零位小数并加千分位；其余保留 2 位（与后端 price_display 同口径）。
export function fmtQuoteMoney(value: number, code: string): string {
  if (!code || code === 'CNY') return `¥${value}`
  // ★ 〇-Q：千分位按界面语种（原写死 'en-US'）
  const shown = QUOTE_ZERO_DECIMAL.has(code)
    ? fmtInt(Math.round(value))
    : value.toFixed(2)
  return `${quoteSymbol(code)}${shown}`
}

// cnyToQuote 收银台折算：人民币金额 ÷ 倍率（1 外币 = rate 人民币）→ 本币展示值（2 位）。
// 倍率非法（0/负数/缺配置）时原样返回 cny——与后端 Resolve 回落 CNY 同一 fail-closed 思路，
// 前端永远不拿臆测倍率凑数。
export function cnyToQuote(cny: number, rate: number): number {
  if (!rate || rate <= 0) return cny
  return Math.round((cny / rate) * 100) / 100
}
