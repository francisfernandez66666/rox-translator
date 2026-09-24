/**
 * 语种 → 文字系统 / BCP47 / 排版与动效档位（★ 2026-09-23 〇-Q「补齐 12 语种的设计与动效」）
 *
 * 为什么单独成文件：字体栈（:lang() 选择器）、排版微调（行高/字距/断行）、动效速度分档
 * 三处都需要「这个语种属于哪个文字系统」这一份事实。散在各处写 `lang === 'th'` 判断，
 * 加一个语种就要改三个地方且必然漏——集中在这里，加语种只改 LANG_SCRIPT 一张表。
 *
 * ⚠️ 与 `Lang` 的差异：`Lang` 是**界面语种代码**（`zh_hant` 这种下划线写法是本项目内部代码），
 * 不是合法 BCP47。浏览器 `document.documentElement.lang` 与 CSS `:lang()` 只认 BCP47，
 * 所以必须经 `BCP47_BY_LANG` 映射后再写出去——历史上漏了这层，`:lang()` 规则全部不生效。
 */

import type { Lang } from './index'

/** 文字系统分档：决定字体栈、行高、断行策略、打字单位与速度 */
export type Script =
  | 'hans' // 简体中文
  | 'hant' // 繁体中文
  | 'latin' // 拉丁（en / fr / es / pt / de）
  | 'cyrillic' // 西里尔（ru）
  | 'arabic' // 阿拉伯（ar，RTL）
  | 'thai' // 泰文（叠音符）
  | 'kana' // 日文（汉字+假名混排）
  | 'hangul' // 韩文

/** 语种 → 文字系统。加新语种只需在这里加一行，其余档位表按 Script 自动覆盖。 */
export const LANG_SCRIPT: Record<Lang, Script> = {
  zh: 'hans',
  zh_hant: 'hant',
  en: 'latin',
  ru: 'cyrillic',
  fr: 'latin',
  ar: 'arabic',
  es: 'latin',
  pt: 'latin',
  de: 'latin',
  ja: 'kana',
  ko: 'hangul',
  th: 'thai',
}

/**
 * 语种 → 合法 BCP47 标签（写进 `document.documentElement.lang`，供 CSS `:lang()` 匹配）。
 * ⚠️ 只有 zh / zh_hant 需要改写（下划线非法）；其余与 Lang 同名。
 *    zh → zh-Hans、zh_hant → zh-Hant：两者都满足「以 zh 开头后接连字符」，
 *    故 `:lang(zh)` 会**同时**命中简繁，需要简繁差异的规则必须写在 `:lang(zh)` 之后才生效。
 */
export const BCP47_BY_LANG: Record<Lang, string> = {
  zh: 'zh-Hans',
  zh_hant: 'zh-Hant',
  en: 'en',
  ru: 'ru',
  fr: 'fr',
  ar: 'ar',
  es: 'es',
  pt: 'pt',
  de: 'de',
  ja: 'ja',
  ko: 'ko',
  th: 'th',
}

/** RTL 语种（UI 语种里目前只有 ar；保留完整表以便以后加 he/fa 等直接生效） */
export const RTL_LANGS: ReadonlySet<Lang> = new Set<Lang>(['ar'])

/** 该语种是否从右向左排版（ar 等）：为真时文档根挂 dir=rtl，整套布局随之镜像翻转 */
export function isRTL(lang: Lang): boolean {
  return RTL_LANGS.has(lang)
}

/** 语种 → 文字系统映射：排版与打字机动效按文字系统分档（词/字步进、速度档），不按语种逐个配置 */
export function scriptOf(lang: Lang): Script {
  return LANG_SCRIPT[lang]
}

/** 文字系统 → CSS `:lang()` 需要的最小标签（用于生成样式与闸门断言） */
export const SCRIPT_BCP47: Record<Script, string> = {
  hans: 'zh-Hans',
  hant: 'zh-Hant',
  latin: 'en',
  cyrillic: 'ru',
  arabic: 'ar',
  thai: 'th',
  kana: 'ja',
  hangul: 'ko',
}

/** 该 Script 对应的全部界面语种（闸门用来断言「每档都真的被规则覆盖」） */
export const LANGS_BY_SCRIPT: Record<Script, readonly Lang[]> = {
  hans: ['zh'],
  hant: ['zh_hant'],
  latin: ['en', 'fr', 'es', 'pt', 'de'],
  cyrillic: ['ru'],
  arabic: ['ar'],
  thai: ['th'],
  kana: ['ja'],
  hangul: ['ko'],
}

/**
 * 打字机的**推进单位**：
 * - 表音/字母文字（`word`）逐词推进——逐字母打会像乱码闪烁，且长词打到一半换行很难看；
 * - 表意/音节文字（`char`）逐字推进——汉字、假名、韩文一字即一词，逐字才是自然节奏；
 *   泰文虽然不是表意文字，但没有空格分词，只能逐字推进。
 */
export const TYPING_UNIT: Record<Script, 'word' | 'char'> = {
  hans: 'char',
  hant: 'char',
  latin: 'word',
  cyrillic: 'word',
  arabic: 'word',
  thai: 'char',
  kana: 'char',
  hangul: 'char',
}

/**
 * 打字机每单位基准毫秒（实际耗时 = base + random()*base*0.5，均值 1.25×base）。
 * 取值依据：CJK 一字承载的信息量远大于一个拉丁字母，同样 38ms 打汉字会得到
 * 「一秒蹦十几个字」的抽搐感，必须放慢；泰文带上下叠音符，逐字推进太快会糊成一团。
 */
export const TYPING_SPEED_MS: Record<Script, number> = {
  hans: 76,
  hant: 76,
  latin: 38,
  cyrillic: 38,
  arabic: 44, // 阿拉伯字母连写，逐词推进但词形较长
  thai: 58, // 叠音符 + 无空格分词
  kana: 68,
  hangul: 60,
}

/** 打字机动效步进单位：拉丁系按「词」、汉字/泰文等按「字」——步进错了会让动效看起来忽快忽慢 */
export function typingUnitOf(lang: Lang): 'word' | 'char' {
  return TYPING_UNIT[LANG_SCRIPT[lang]]
}

/** 打字机动效每步毫秒数：按文字系统给可读速度（如泰文有组合元音符号，过快会闪断） */
export function typingSpeedOf(lang: Lang): number {
  return TYPING_SPEED_MS[LANG_SCRIPT[lang]]
}

/**
 * 聊天流式揭示速度（AI 接管注册引导等逐字"打字"场景）。
 * 比展示卡（typingSpeedOf）快一档：聊天回复可能很长，逐字等展示卡的原速会让人干等；
 * 单位仍按文字系统（CJK 逐字 / 拉丁逐词），所以「动效也用对应语言」在聊天里同样成立。
 */
export function chatTypingMs(lang: Lang): number {
  return Math.round(typingSpeedOf(lang) * 0.45)
}
