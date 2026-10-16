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
export type VoiceCap = { tts: boolean | null }

const HEALTH_URL = '/api/health'
// 探针 TTL：健康读数变化很慢（要重投放权重才有意义），5 分钟一次足够；
// 同一次会话里几十个气泡共用同一份状态，不会每个气泡各拨一次（见 inflight 去重）。
const TTL_MS = 5 * 60 * 1000

let snap: VoiceCap = { tts: null }
// listeners 订阅者集合（各气泡的 useSyncExternalStore 各订一份，状态只此一份）
const listeners = new Set<() => void>()
let inflight: Promise<void> | null = null
let fetchedAt = 0

// emit 只在**值真的变了**时通知订阅者：useSyncExternalStore 的语义是"快照没变就不重渲染"，
// 无条件 emit 会让每个气泡空转（同 frontend 那条「词典到达要进快照」的坑，反过来用）。
function emit(next: VoiceCap) {
  if (next.tts === snap.tts) return
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
      const j = (await r.json()) as { tts_ready?: unknown }
      // 字段不是布尔 ⇒ 保持「未知」：把「读不到」写成「不可用」就会无端摘掉入口
      if (typeof j?.tts_ready === 'boolean') {
        fetchedAt = Date.now()
        emit({ tts: j.tts_ready })
      }
    } catch { /* 探不到 ⇒ 保持未知，按钮照旧露出（fail-open） */ } finally {
      inflight = null
    }
  })()
  return inflight
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
  snap = { tts: null }
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
