// ============================================================================
// api/trial.ts — 免登录即时翻译试用（★ 〇-Z，2026-09-28）
// 职责：官网首页 hero 的「点一下就翻一句」体验 → POST /api/trial/translate；
//       语种下拉的真值来源 → GET /api/translation/langs（与后端试用白名单同一份事实）。
// 口径：
//   1. 匿名接口：不依赖登录态（request() 自带的 Authorization 头在有会话时会照发，
//      服务端试用面显式把成本记到租户 0，不会动任何客户余额，见 backend trial.go 文件头）；
//   2. 失败必须还原成响应体：试用面的 429（额度用完／手太快）与 400（内容过长／语种不在范围）
//      都是**要展示给人的正常分支**，不是异常。后端这批已按语义给诚实状态码（F-64①），
//      故本层一律走 bizResp——直返裸 request 会让面板对着抛出的 ApiError 白屏
//      （AGENTS.md §一·5「接口层失败必须经 bizResp 收敛」）；
//   3. 超时放宽到 45 秒：试用固定走专业校对档（pro），一句要经「初翻＋AI 校对」两趟模型，
//      默认 30 秒在高峰期会把一次正常试用掐成「暂时不可用」，那是把产品体验判死给网络抖动。
// ============================================================================
import { request, bizResp, type AdminResp } from './core'

/** 试用单次原文上限（字符），与后端 trialMaxTextRunes 同值：前端只做提前提示，服务端才是判据 */
export const TRIAL_MAX_CHARS = 300

/** 试用请求体：字段名与后端 trialReq 的 json tag 一一对应，改名必须两头同步 */
export interface TrialTranslatePayload {
  text: string // 待试译原文（≤300 字符）
  target_lang: string // 单个目标语种码（须在 /api/translation/langs 那份名单里）
  device_id: string // 浏览器设备号（lib/trialDevice.ts 生成并持久化）
}

/**
 * trialTranslate 试翻一句。
 * 成功：{success:true, translation, source_lang, target_lang, mode:'pro', left, exhausted}
 * 失败：{success:false, code, message, reason?, retry_after?}
 * 其中 code=TRIAL_EXHAUSTED 时 reason ∈ device|ip|global（前端据此决定「注册引导」文案），
 * code=RATE_LIMITED 时 retry_after 是建议等待秒数（手太快，不是额度见底）。
 */
export async function trialTranslate(payload: TrialTranslatePayload): Promise<AdminResp> {
  return bizResp(() => request('/api/trial/translate', {
    method: 'POST',
    body: JSON.stringify(payload),
    timeoutMs: 45000,
  }))
}

/** 试用可选目标语种（与产品语种盘同源）：{code, name(中文), name_en, kb} */
export interface TrialLang {
  code: string
  name: string
  name_en: string
}

/**
 * trialLangs 拉取试用可选语种。
 * ⚠️ 判据来源必须是 /api/translation/langs：后端试用白名单就是按这份表（config.TranslateLangs ∪ zh）
 *    判的，前端另抄一份常量表迟早和语种盘扩容脱节——访客选得到一个后端不肯收的语种，
 *    只会换来一句看不懂的「该语言暂不在试用范围内」。
 */
export async function trialLangs(): Promise<{ ok: boolean; langs: TrialLang[] }> {
  try {
    const r = await bizResp(() => request('/api/translation/langs')) as unknown as {
      success?: boolean
      kb_langs?: Array<{ code?: string; name?: string; name_en?: string }>
    }
    const raw = Array.isArray(r.kb_langs) ? r.kb_langs : []
    const langs: TrialLang[] = raw
      .filter((l) => typeof l.code === 'string' && l.code !== '')
      .map((l) => ({
        code: String(l.code),
        name: l.name || String(l.code),
        name_en: l.name_en || l.name || String(l.code),
      }))
    return { ok: r.success !== false && langs.length > 0, langs }
  } catch {
    // 网络层失败（断网/网关坏体）：返回空表，由面板回落内置常用语种，试用入口不能因此点不开
    return { ok: false, langs: [] }
  }
}
