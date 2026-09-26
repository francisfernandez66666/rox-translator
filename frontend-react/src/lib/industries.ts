// ============ lib/industries.ts · 职责说明 ============
// 行业 code → 展示名映射与查询工具。
// 与后端内置行业 code 对齐（auto/realestate/b2b/education/ecommerce/wedding/retail/media/general）。
// 用途：各下拉/表格展示统一取此工具，界面语种为简体/繁体/英文时各取对应词条，其余语种落英文。
// ★ F-61（批 I-8 2026-09-26）：繁体界面曾整排显示简体（判据只有 `startsWith('zh')`），
//   现按 locale 精确分 zh / zh_hant；行业名是**人工词条**（不属机翻高风险项），故补繁体能零风险。
// =============================================
import type { Lang } from '@/i18n'

/** 行业元数据：代码、简体名、繁体名、英文名（zhHant 的 value 仍用简体，见 industryOptions 注释） */
export interface IndustryMeta {
  code: string
  zh: string
  /** ★ F-61：繁体界面展示名（人工词条，2026-09-26） */
  zhHant: string
  en: string
}

/** 行业 code → 展示名映射表（与后端内置行业对齐） */
export const INDUSTRY_META: Record<string, IndustryMeta> = {
  general: { code: 'general', zh: '通用行业', zhHant: '通用行業', en: 'General' },
  auto: { code: 'auto', zh: '汽车', zhHant: '汽車', en: 'Automobile' },
  realestate: { code: 'realestate', zh: '房产/装修', zhHant: '房產/裝修', en: 'Real Estate & Renovation' },
  b2b: { code: 'b2b', zh: '企业服务/B2B', zhHant: '企業服務/B2B', en: 'Business Services / B2B' },
  education: { code: 'education', zh: '教育/留学', zhHant: '教育/留學', en: 'Education & Study Abroad' },
  ecommerce: { code: 'ecommerce', zh: '跨境独立站', zhHant: '跨境獨立站', en: 'Cross-border E-commerce' },
  wedding: { code: 'wedding', zh: '婚庆/高端服务', zhHant: '婚慶/高端服務', en: 'Wedding & Premium Services' },
  retail: { code: 'retail', zh: '电商/零售', zhHant: '電商/零售', en: 'E-commerce & Retail' },
  media: { code: 'media', zh: '自媒体/内容创作', zhHant: '自媒體/內容創作', en: 'Content Creation & Media' },
}

/** 行业 code → 当前语言展示名（未知 code 回退 code 本身，供表单/表格展示）
 *  ★ F-61：判据从「zh* 一律取中文」改为**精确分简/繁**——旧写法让 zh_hant 界面
 *  （IndustrySelect、品牌词、KB 表单等）整排显示简体，客户实测抓到。
 *  其余十语种仍落英文名（行业表没有 12 份词条，与 #23 的取舍一致）。 */
export function industryName(code: string, lang: Lang): string {
  if (!code) return ''
  const m = INDUSTRY_META[code]
  if (!m) return code
  if (lang === 'zh_hant') return m.zhHant
  return lang.startsWith('zh') ? m.zh : m.en
}

/** 行业下拉选项（值=简体中文名，label 按当前语言自适应；后端仍按 code 存储）
 *  ⚠️ value 必须继续用简体 m.zh：`industryCodeOf()` 按 m.zh 反查 code，且历史数据里
 *  存的就是简体名——换成繁体 label 同串会让老数据反查不中（后端存储契约，不动）。 */
export function industryOptions(lang: Lang): Array<{ value: string; label: string; code: string }> {
  return Object.values(INDUSTRY_META).map((m) => ({
    value: m.zh, // 值用简体中文名（后端存储用 code 由调用方转换）
    label: industryName(m.code, lang),
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