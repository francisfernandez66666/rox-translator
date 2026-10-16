// ============================================================================
// api/voiceCapability.ts — 服务端语音能力的唯一取词口（/api/health 的 tts_ready）
//
// ★ 立这条的原因（2026-10-16 用户报「播放语音译文报错」）：
//   部署机上没投放 TTS 权重时 `/api/health.tts_ready=false`，朗读接口恒回
//   `capability_missing` ⇒ 客户点一次「朗读」就吃一句失败 toast。**这不是故障，是能力缺失**，
//   而界面上却摆着一颗点了必然失败的按钮——ChatWindow 的麦克风早已立过同款口径
//   （「浏览器不支持就直接**不渲染**这颗按钮，不给一个点了必然报错的入口」），本模块把同一口径
//   接到服务端能力上：tts_ready=false ⇒ 不渲染朗读钮；译文、复制、反馈一件都不受影响（译文第一）。
//
// ★ fail-open：探不到（离线／健康接口超时／字段缺失／形态不对）一律算「未知」，
//   **保持按钮**。理由与 §一·10 的「公开接口新增防护一律软档」同族：
//   探针自己坏掉不该摘掉产品入口；真点了仍然会走 voiceFail 那条本地化 toast 兜底。
//   未知与「已知可用」在渲染上同形（都露出按钮），只有**已知不可用**才摘。
//
// ★ 单一事实源：只有本文件读 tts_ready。别在组件里再 fetch 一次 /api/health 判能力
//   （那会长出第二把尺子：一处按 tts_ready、另一处按拿到的错误文案猜）。
// ============================================================================
import { useEffect, useSyncExternalStore } from 'react'

// VoiceCap 语音能力快照：null＝未知（探不到，按可用处理）
// ★ langs（2026-10-16 补）：TTS 覆盖的目标语种清单（/api/health.tts_langs，**有效档**：
//   中英档 zh/en ＋ 多语档 ja/ko/fr/de/es/pt/ru/ar，按服务端两条路线资产实况收敛）。
//   语音是**按语种**的能力——"服务器有 TTS" 不等于"这个语种有语音"，
//   只有 tts 一个布尔时，未覆盖语种（如 th）译文的同一排会摆出一颗点下去必然失败的朗读钮。
//   null＝未知（字段缺失/形态不对/探不到）⇒ 照旧 fail-open 露出按钮。
export type VoiceCap = { tts: boolean | null; langs: string[] | null }

const HEALTH_URL = '/api/health'
// 探针 TTL：健康读数变化很慢（要重投放权重才有意义），5 分钟一次足够；
// 同一次会话里几十个气泡共用同一份状态，不会每个气泡各拨一次（见 inflight 去重）。
const TTL_MS = 5 * 60 * 1000

// normTtsLang 语种码归一化：小写、剥地域段（zh-hant / zh_Hant → zh）。
// ★ 与后端**同一把尺子**（fileproc 的 ttsLangCovered / tts_speak.py 的 norm_lang）：
//   两侧各判一次必然漂（历史形态＝前端送 zh_hant、后端按 zh-hant 判，边界永远差一档）。
//   这里不额外做任何"聪明"映射（别把 th 之类顺手映成别的码）。
export function normTtsLang(lang: string): string {
  return (lang || '').trim().toLowerCase().replace('_', '-').split('-')[0]
}

let snap: VoiceCap = { tts: null, langs: null }
// listeners 订阅者集合（各气泡的 useSyncExternalStore 各订一份，状态只此一份）
const listeners = new Set<() => void>()
let inflight: Promise<void> | null = null
let fetchedAt = 0

// emit 只在**值真的变了**时通知订阅者：useSyncExternalStore 的语义是"快照没变就不重渲染"，
// 无条件 emit 会让每个气泡空转（同 frontend 那条「词典到达要进快照」的坑，反过来用）。
function emit(next: VoiceCap) {
  // 两个字段任一变化都要发通知：只比 tts 会让"覆盖语种补齐/收回"永远不重渲染
  // （形态同那条「emit 说变了才算变」的反向坑：这里漏比一个字段，界面上就是按钮该出现时不出现）。
  const sameLangs =
    (next.langs === null && snap.langs === null) ||
    (next.langs !== null && snap.langs !== null &&
      next.langs.length === snap.langs.length && next.langs.every((l, i) => l === snap.langs![i]))
  if (next.tts === snap.tts && sameLangs) return
  snap = next
  listeners.forEach((l) => l())
}

// probeVoiceCapability 拨一次健康接口（幂等：同一次在途复用，TTL 内不重复拨）。
export function probeVoiceCapability(force = false): Promise<void> {
  if (!force && inflight) return inflight
  if (!force && fetchedAt && Date.now() - fetchedAt < TTL_MS) return Promise.resolve()
  inflight = (async () => {
    try {
      const r = await fetch(HEALTH_URL, { headers: { accept: 'application/json' } })
      if (!r.ok) throw new Error('http-' + r.status)
      const j = (await r.json()) as { tts_ready?: unknown; tts_langs?: unknown }
      // 字段不是布尔 ⇒ 保持「未知」：把「读不到」写成「不可用」就会无端摘掉入口
      if (typeof j?.tts_ready === 'boolean') {
        // 覆盖语种：只认"非空字符串数组"，其余（缺失/null/混进非字符串/空数组）一律按**未知**处理。
        //   空数组若被当成"一个语种都不支持"，一次字段解析意外就会把全站朗读钮摘光；
        //   而"未知"的代价只是回到 2026-10-16 之前的形态（露出按钮，失败有本地化 toast 兜底）。
        const raw = j?.tts_langs
        const langs =
          Array.isArray(raw) && raw.length > 0 && raw.every((x) => typeof x === 'string')
            ? raw.map((x) => normTtsLang(x as string))
            : null
        fetchedAt = Date.now()
        emit({ tts: j.tts_ready, langs })
      }
    } catch { /* 探不到 ⇒ 保持未知，按钮照旧露出（fail-open） */ } finally {
      inflight = null
    }
  })()
  return inflight
}

// ttsLangCovered 这个目标语种在当前服务端能力下**有没有语音**。
// 三档与 tts 同源：未知（langs=null）⇒ 放行（fail-open）；空 lang ⇒ 放行（调用方没指定）；
// 命中覆盖清单 ⇒ 放行；明确不在清单里 ⇒ 收门（这一语种的朗读钮不渲染）。
export function ttsLangCovered(cap: VoiceCap, lang: string): boolean {
  if (cap.langs === null) return true
  const lc = normTtsLang(lang)
  if (!lc) return true
  return cap.langs.includes(lc)
}

// voiceCapability 同步快照（首帧 / 非 React 侧用；getSnapshot 必须返回缓存对象，现算会无限重渲染）
export function voiceCapability(): VoiceCap { return snap }

// subscribeVoiceCapability 订阅能力变化（返回退订函数）
export function subscribeVoiceCapability(fn: () => void): () => void {
  listeners.add(fn)
  return () => { listeners.delete(fn) }
}

// resetVoiceCapability 复位（仅测试用：模块级单例要能一例一清，否则用例间互相污染）
export function resetVoiceCapability() {
  snap = { tts: null, langs: null }
  inflight = null
  fetchedAt = 0
  listeners.clear()
}

// useVoiceCapability 取当前语音能力；挂载时拨一次探针（全站共用同一份状态）。
export function useVoiceCapability(): VoiceCap {
  const s = useSyncExternalStore(subscribeVoiceCapability, voiceCapability, voiceCapability)
  useEffect(() => { void probeVoiceCapability() }, [])
  return s
}
