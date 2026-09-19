// ============ lib/langNames.ts · 职责说明 ============
// 语言代码 → 中英文名称与国旗的映射及查询工具
// 与后端 config.LangNames / LangNamesEn 同源；UI 语言切换时按当前语言取名称。
// 2026-09-17 纯黑换肤：表意不再靠 emoji，所有 flag 值统一置空（键结构保留），
// langLabel 因此恒定走「无 flag 只回名称」分支；如需恢复图形标识，应改挂
// LangCross <Icon/> 或图片资源，而不是把 emoji 塞回词条。
// =============================================
/** 语言元数据：语言代码、中英文名称与可选国旗，供语言选择器与展示统一使用 */
export interface LangMeta {
  code: string // 语言代码（与后端下发/前端选项一致）
  zh: string   // 中文展示名
  en: string   // 英文展示名
  flag?: string // 图形标识位：换肤后一律为空串，仅保留字段以兼容既有调用方
}

/** 语言代码 → 元数据映射表（中/英名称 + 国旗），与后端配置同源，供语言选择器与展示统一使用
 *  注意两个历史别名：
 *  - id_lang = 印尼语的「KB 列名口径」（后端 kb 表 id 是主键，故译文列改叫 id_lang，
 *    见 backend-go/internal/api/kb.go mapDBLangCode）；id 则是常规 ISO 码，两者并存
 *    以免任一入口取不到名称；
 *  - zh_hant = 繁体中文（非 zh-TW/zh_HK 之类的区域码）。 */
export const LANG_META: Record<string, LangMeta> = {
 zh: { code:'zh', zh:'中文', en:'Chinese', flag:''},
 en: { code:'en', zh:'英语', en:'English', flag:''},
 ru: { code:'ru', zh:'俄语', en:'Russian', flag:''},
 ar: { code:'ar', zh:'阿拉伯语', en:'Arabic', flag:''},
 es: { code:'es', zh:'西班牙语', en:'Spanish', flag:''},
 pt: { code:'pt', zh:'葡萄牙语', en:'Portuguese', flag:''},
 fr: { code:'fr', zh:'法语', en:'French', flag:''},
 kk: { code:'kk', zh:'哈萨克语（哈萨克斯坦）', en:'Kazakh (Kazakhstan)', flag:''},
 de: { code:'de', zh:'德语', en:'German', flag:''},
 zh_hant: { code:'zh_hant', zh:'繁体中文', en:'Traditional Chinese', flag:''},
 ja: { code:'ja', zh:'日语', en:'Japanese', flag:''},
 ko: { code:'ko', zh:'韩语', en:'Korean', flag:''},
 th: { code:'th', zh:'泰语', en:'Thai', flag:''},
 vi: { code:'vi', zh:'越南语', en:'Vietnamese', flag:''},
 ms: { code:'ms', zh:'马来语', en:'Malay', flag:''},
 id_lang: { code:'id_lang', zh:'印尼语', en:'Indonesian', flag:''},
 it: { code:'it', zh:'意大利语', en:'Italian', flag:''},
 pl: { code:'pl', zh:'波兰语', en:'Polish', flag:''},
 tr: { code:'tr', zh:'土耳其语', en:'Turkish', flag:''},
 mn: { code:'mn', zh:'蒙古语', en:'Mongolian', flag:''},
 id: { code:'id', zh:'印尼语', en:'Indonesian', flag:''},
 nl: { code:'nl', zh:'荷兰语', en:'Dutch', flag:''},
 uk: { code:'uk', zh:'乌克兰语', en:'Ukrainian', flag:''},
 hi: { code:'hi', zh:'印地语', en:'Hindi', flag:''},
 fa: { code:'fa', zh:'波斯语', en:'Persian', flag:''},
 he: { code:'he', zh:'希伯来语', en:'Hebrew', flag:''},
 el: { code:'el', zh:'希腊语', en:'Greek', flag:''},
 my: { code:'my', zh:'缅甸语', en:'Burmese', flag:''},
 km: { code:'km', zh:'柬埔寨语', en:'Khmer', flag:''},
 lo: { code:'lo', zh:'老挝语', en:'Lao', flag:''},
 tl: { code:'tl', zh:'菲律宾语', en:'Filipino', flag:''},
 gu: { code:'gu', zh:'古吉拉特语', en:'Gujarati', flag:''},
 ur: { code:'ur', zh:'乌尔都语', en:'Urdu', flag:''},
 te: { code:'te', zh:'泰卢固语', en:'Telugu', flag:''},
 mr: { code:'mr', zh:'马拉地语', en:'Marathi', flag:''},
 bn: { code:'bn', zh:'孟加拉语', en:'Bengali', flag:''},
 ta: { code:'ta', zh:'泰米尔语', en:'Tamil', flag:''},
 bo: { code:'bo', zh:'藏语', en:'Tibetan', flag:''},
 ug: { code:'ug', zh:'维吾尔语', en:'Uyghur', flag:''},
 yue: { code:'yue', zh:'粤语', en:'Cantonese', flag:''},
 sv: { code:'sv', zh:'瑞典语', en:'Swedish', flag:''},
}

/** 按当前界面语言返回语言展示名（含国旗）；未知代码原样返回
 *  换肤后 flag 恒为空串，实际返回纯名称；保留 m.flag 分支是为了日后恢复图形标识时
 *  不必再改调用点（届时应换成图标/图片资源，而非 emoji）。
 * @param code - 语言代码（如 "en" / "ja"）
 * @param lang - 当前界面语言，决定取中文名还是英文名（★ #23 起收 12 语种代码：
 *               只有 zh* 取中文名，其余语种一律英文名——语言名没有 12 份的词条表）
 */
export function langLabel(code: string, lang: string): string {
  const m = LANG_META[code]
  if (!m) return code
  const name = lang.startsWith('zh') ? (m.zh || m.en) : (m.en || m.zh)
  return m.flag ? `${m.flag} ${name}` : name
}
