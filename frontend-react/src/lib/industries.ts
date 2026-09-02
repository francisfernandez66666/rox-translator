// ============ lib/industries.ts · 职责说明 ============
// 行业 code → 中英文名映射与查询工具。
// 与后端内置行业 code 对齐（auto/realestate/b2b/education/ecommerce/wedding/retail/media/general）。
// 用途：各下拉/表格展示统一取此工具，语言切换自动在 中文/English 间切换（如 汽车→automobile）。
// =============================================
import type { Lang } from '@/i18n'

/** 行业元数据：代码、中文名、英文名 */
export interface IndustryMeta {
  code: string
  zh: string
  en: string
}

/** 行业 code → 中英文名映射表（与后端内置行业对齐） */
export const INDUSTRY_META: Record<string, IndustryMeta> = {
  general: { code: 'general', zh: '通用行业', en: 'General' },
  auto: { code: 'auto', zh: '汽车', en: 'Automobile' },
  realestate: { code: 'realestate', zh: '房产/装修', en: 'Real Estate & Renovation' },
  b2b: { code: 'b2b', zh: '企业服务/B2B', en: 'Business Services / B2B' },
  education: { code: 'education', zh: '教育/留学', en: 'Education & Study Abroad' },
  ecommerce: { code: 'ecommerce', zh: '跨境独立站', en: 'Cross-border E-commerce' },
  wedding: { code: 'wedding', zh: '婚庆/高端服务', en: 'Wedding & Premium Services' },
  retail: { code: 'retail', zh: '电商/零售', en: 'E-commerce & Retail' },
  media: { code: 'media', zh: '自媒体/内容创作', en: 'Content Creation & Media' },
}

/** 行业 code → 当前语言展示名（未知 code 回退 code 本身，供表单/表格展示） */
export function industryName(code: string, lang: Lang): string {
  if (!code) return ''
  const m = INDUSTRY_META[code]
  if (!m) return code
  return lang === 'en' ? m.en : m.zh
}

/** 行业下拉选项（值=中文名，label 按当前语言自适应；后端仍按 code 存储） */
export function industryOptions(lang: Lang): Array<{ value: string; label: string; code: string }> {
  return Object.values(INDUSTRY_META).map((m) => ({
    value: m.zh, // 值用中文名（后端存储用 code 由调用方转换）
    label: lang === 'en' ? m.en : m.zh,
    code: m.code,
  }))
}

/** 中文名 → 行业 code（提交后端前转换；未命中回退空串） */
export function industryCodeOf(zhName: string): string {
  if (!zhName) return ''
  for (const m of Object.values(INDUSTRY_META)) {
    if (m.zh === zhName) return m.code
  }
  return zhName // 已是非中文名（如 code 自身）直接透传
}