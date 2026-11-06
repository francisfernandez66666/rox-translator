// ============ lib/manualShotPrivacy.ts · 职责说明 ============
// E 组（2026-11-06）「手册配图只准用夹具假数据截图」那条裁定里的**隐私闸**本体。
//
// 为什么要有这道闸（不是形式主义）：手册 PDF 是**可公开下载**的产物，一旦带截图发出去就收不回
// ——邮件被转发、被爬、被客服反复转发。截图里只要有一次出现真实邮箱／余额／组织成员名单／
// 可访问的对外页短码，泄漏面就是"所有拿到这份手册的人"。所以判据必须比人眼可靠：
//   ① 判的是**画面上的文字**（DOM 文本），不是文件字节——因为画面里的"数据"只有两个来源：
//      界面固定文案（词典现值）与写进页面的业务数据；两者都能从 DOM 读到，不需要 OCR；
//   ② 允许集是**白名单**而不是黑名单：词典现值 ∪ 夹具假值，白名单之外的一切文本 ⇒ 判红。
//      黑名单（"邮箱／手机号／金额"）挡不住新型态（比如把客户公司名放进工单标题）。
//
// ★ 三条口径：
//  ① **命中必须逐条点名**（值＋规则名＋上下文片段），只回"检查通过/失败"等于没做
//     （AGENTS §一·5「什么都没出与四种没清缓存必须不同形」同族）。
//  ② 空文本＝**判红**（判据失明）。一段没读到任何文字的截图，恰恰意味着采集时页面还没渲染完
//     ——那种图截出来是骨架屏，既没内容也没隐私，但它冒充"这一章截好了"。
//  ③ 本文件**不许**出现任何真实账号／公司名，夹具白名单里的串一律是明显的演示值。
//
// 运行测试：npx vitest run src/lib/manualShotPrivacy.test.ts
// ========================================

/** 一条违规命中：规则名＋命中的原文＋它在画面里的上下文片段（截 60 字，够定位即可，不再多带内容） */
export type PrivacyHit = { rule: string; value: string; around: string }

/** 隐私闸的判定结果：pass 仅代表"可以保存这张图"；hits 永远逐条给全 */
export type PrivacyVerdict = { pass: boolean; hits: PrivacyHit[]; checkedChars: number }

/** 夹具假值白名单（唯一允许出现在截图里的"业务数据"）。★ 这些串本身就是演示值，不含任何真实主体。 */
export const FIXTURE_ALLOW = [
  'manual-clip@example.test',
  '示例用户（演示数据）',
  '示例机械（演示数据）',
  '示例整机（演示数据）',
  '演示数据',
  'Sample Machine (demo data)',
]

/**
 * 硬规则表：与白名单并列的**负向**判据。
 * 每条都写清"它挡的是哪一种现网形态"，方便下一位判断该不该加新规则，而不是随手放宽。
 */
const RULES: Array<{ rule: string; re: RegExp }> = [
  // 邮箱（除夹具那一个）：注册邮箱／成员邮箱都会出现在后台列表与工单详情里
  { rule: 'email', re: /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/g },
  // 手机号（含国际前缀与常见分隔写法）：成员管理、留资列表都是高危画面。
  // ★ 形态必须是**三段分组**（+86 138 1234 5678／138-1234-5678），不能写成"任意 7 个以上数字带任意分隔"——
  //   首跑那版把 `2026-11-06 12:31` 这种日期时间整串吞成手机号，误杀的代价是采集一路红、
  //   然后被人当噪声放宽（比漏抓更坏，见 AGENTS 那条"锁的归宿是被摘掉"）。
  //   连续不分组的一串手机号由 long-number（≥8 位）那条兜住，两条各有射程、不重复。
  { rule: 'phone', re: /(?<!\d)(?:\+?\d{2,3}[-\s])?\d{3,4}[-\s]\d{4}(?!\d)/g },
  // 连续 ≥8 位纯数字：余额／积分数／订单号／流水号都落在这一族（白名单里的年份型短数字达不到 8 位）
  { rule: 'long-number', re: /\d{8,}/g },
  // ★ 千分位分组形态（首跑就是这条漏的）：余额在界面上**永远带逗号或空格**（"1,234,567.89"），
  //   只判 \d{8,} 会结构性放过最典型的那一种泄漏。分组规则要单独在，不能靠"去掉逗号再扫"——
  //   去掉分隔符会让 "编号 A-12,345,678" 这类合法展示串也变成命中，误杀面比漏抓更难收拾。
  { rule: 'grouped-number', re: /\d{1,3}(?:[,\s]\d{3}){2,}(?:[.,]\d{1,2})?/g },
  // 对外页短码形态：两段不可枚举码本身就是**可访问链接**，写进手册等于把租户段＋页面段一起公开
  { rule: 'page-code', re: /\/p\/[A-Za-z0-9_-]{8,}\/[A-Za-z0-9_-]{8,}/g },
  { rule: 'short-code-token', re: /\b[A-Fa-f0-9]{16,}\b/g },
  // 凭证形态：API Key / Bearer / sk_ 前缀 / JWT 三段式
  // ★ 密钥前缀这一段同样首跑漏过：真实键形如 `sk_live_abcd…`／`sk_test_abcd…`，
  //   `sk_` 后面紧跟的 `live_` 里带下划线 ⇒ `[A-Za-z0-9]{8,}` 直接匹配不到（首跑只抓到 "abcdefgh12345678"？不，整条都不抓）。
  //   字符集必须含下划线；同时把长度门槛放在**前缀之后整段**，别把 "sk_" 这种占位符本身判成密钥。
  { rule: 'credential', re: /\b(?:sk_[A-Za-z0-9_]{6,}|Bearer\s+[A-Za-z0-9._-]{12,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,})\b/g },
]

/** 把画面文字按规则扫一遍，返回逐条命中（值＋规则＋上下文）。 */
export function scanShotText(text: string): PrivacyHit[] {
  const hits: PrivacyHit[] = []
  for (const { rule, re } of RULES) {
    // 每条规则各建一个 lastIdx 安全的遍历：共享 RegExp 对象会被 exec 的 lastIndex 串味
    const scanner = new RegExp(re.source, re.flags.includes('g') ? re.flags : re.flags + 'g')
    let m: RegExpExecArray | null
    while ((m = scanner.exec(text)) !== null) {
      const value = m[0]
      if (FIXTURE_ALLOW.includes(value)) continue // 白名单精确等值才算放行（子串不算，防"把真号藏进长串里"）
      const start = Math.max(0, m.index - 20)
      hits.push({ rule, value, around: text.slice(start, m.index + value.length + 20) })
      if (m.index === scanner.lastIndex) scanner.lastIndex++
    }
  }
  return hits
}

/**
 * 隐私闸主入口。参数：text=截图时抓到的 `document.body.innerText`；
 * allowTexts=该语种的界面固定文案集（词典现值），命中值若等于其中某条固定文案则放行。
 *
 * ★ 空文本判红（口径②）：采到空串说明页面还没渲染完，那张图是骨架屏，冒充"这章截好了"。
 */
export function judgeShotPrivacy(text: string, allowTexts: string[] = []): PrivacyVerdict {
  const raw = (text ?? '').toString()
  if (raw.trim().length === 0) {
    return {
      pass: false,
      checkedChars: 0,
      hits: [{ rule: 'empty-text', value: '(画面文字为空)', around: '采集时页面未渲染完成，截图会是骨架屏而不是功能页' }],
    }
  }
  const hits = scanShotText(raw).filter((h) => !allowTexts.includes(h.value))
  return { pass: hits.length === 0, hits, checkedChars: raw.length }
}

/** 把判定结果拼成一行可贴进日志/验收文档的文字（**逐条点名**，不许只出"FAIL"）。 */
export function formatVerdict(v: PrivacyVerdict, chapter: string, lang: string): string {
  if (v.pass) return `OK|shot ${chapter}/${lang} 已扫 ${v.checkedChars} 字，白名单外零命中`
  const detail = v.hits.slice(0, 8).map((h) => `${h.rule}=${JSON.stringify(h.value)}（上下文…${h.around}…）`).join(' ; ')
  const more = v.hits.length > 8 ? ` ; 另有 ${v.hits.length - 8} 条` : ''
  return `FAIL|shot ${chapter}/${lang} 隐私闸 ${v.hits.length} 处命中 ⇒ 该图不保存：${detail}${more}`
}
