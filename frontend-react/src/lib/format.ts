// ============================================================================
// lib/format.ts — 按**界面语种**格式化数字 / 日期 / 货币（★ 2026-09-23 〇-Q）
// ----------------------------------------------------------------------------
// 背景：改造前站内有两类不一致的写法，都会让「数字与界面语言对不上」：
//   ① 写死 `toLocaleString('en-US')`（lib/ui.ts、utils/points.ts、quoteFmt.ts）——
//      德语界面里照样出 1,234.56，而德语应是 1.234,56；
//   ② 用浏览器默认 locale（App.tsx、TicketsPage、TicketsP）——
//      用户手动切了界面语言后，数字仍跟着**浏览器**语言走，两者互相打架。
// 现在统一走 `getLang()` → BCP47，切语种即切换格式。
//
// ⚠️ 为什么不用 `navigator.language`：界面语言是用户显式选择并持久化的（localStorage
//    app_lang），与浏览器语言可能不同；Intl 必须用界面语言才是「所见即所配」。
//
// ⚠️ 全部函数对非法输入 fail-closed（返回原串 / '0' / '—'），不抛异常——
//    这些函数在列表渲染里被逐行调用，抛一次整页白屏。
// ============================================================================

import { getLang } from '../i18n'
import { BCP47_BY_LANG } from '../i18n/script'

/** 当前界面语种对应的 BCP47 标签（Intl 唯一入口，别再自己拼 locale） */
export function intlLocale(): string {
  try {
    return BCP47_BY_LANG[getLang()] || 'en'
  } catch {
    return 'en' // 非浏览器环境 / 取词失败时兜英文格式，不阻断渲染
  }
}

/** 整数千分位（积分、次数、条数等） */
export function fmtInt(n: number | string | null | undefined): string {
  const x = Number(n || 0)
  if (!Number.isFinite(x)) return '0'
  try {
    return new Intl.NumberFormat(intlLocale(), { maximumFractionDigits: 0 }).format(Math.round(x))
  } catch {
    return String(Math.round(x))
  }
}

/** 通用数字格式化（默认最多 2 位小数） */
export function fmtNum(
  n: number | string | null | undefined,
  opts?: { min?: number; max?: number },
): string {
  const x = Number(n || 0)
  if (!Number.isFinite(x)) return '0'
  try {
    return new Intl.NumberFormat(intlLocale(), {
      minimumFractionDigits: opts?.min ?? 0,
      maximumFractionDigits: opts?.max ?? 2,
    }).format(x)
  } catch {
    return String(x)
  }
}

/** 日期时间（列表时间列、导出文件名后缀等） */
export function fmtDateTime(v: Date | string | number | null | undefined): string {
  if (v == null || v === '') return '—'
  const d = v instanceof Date ? v : new Date(v as string)
  if (Number.isNaN(d.getTime())) return String(v)
  try {
    return new Intl.DateTimeFormat(intlLocale(), {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }).format(d)
  } catch {
    return d.toISOString()
  }
}

/** 仅日期（不含时分） */
export function fmtDate(v: Date | string | number | null | undefined): string {
  if (v == null || v === '') return '—'
  const d = v instanceof Date ? v : new Date(v as string)
  if (Number.isNaN(d.getTime())) return String(v)
  try {
    return new Intl.DateTimeFormat(intlLocale(), {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).format(d)
  } catch {
    return d.toISOString().slice(0, 10)
  }
}

/**
 * 货币金额。
 * ⚠️ CNY 保持 `¥<原值>` 的既有口径**不加重格式化**——quoteFmt 里明确写了
 *    「防止改动历史渲染结果」，收银台/报价单的历史快照与对账单都按这个字面值走，
 *    动了会让同一笔钱在两处显示不一致。其余币种按界面语种出千分位与小数位。
 */
export function fmtMoney(value: number, code: string): string {
  const v = Number(value || 0)
  if (!Number.isFinite(v)) return '0'
  if (!code || code === 'CNY') return `¥${v}`
  try {
    return new Intl.NumberFormat(intlLocale(), {
      style: 'currency',
      currency: code,
      maximumFractionDigits: 2,
    }).format(v)
  } catch {
    return `${code} ${fmtNum(v)}`
  }
}
