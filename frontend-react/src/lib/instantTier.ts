// ============================================================================
// lib/instantTier.ts — 即时翻译「免费档 / 付费档」两档尺寸与语种上限的唯一事实源
// （★ 2026-10-30 三事合一批 D-2/D-3）
//
// 为什么要有这个文件：图片压缩目标、长边上限、录音时长上限在**三处**被消费
//   （ChatWindow 选图、ChatWindow 录音、以及将来任何即时入口），而对外的档位宽度是
//   公开卖点（1 MiB / 3 MiB / 30 s / 60 s）。把两套断点抄进组件，就是 F-12「三口径打架」
//   的同族形态——运营改一档、界面还按旧档说话，且没有任何闸门看得见。
//   ⇒ 数字只住在这里，消费方一律 `instantLimits(tier)`。
//
// 「付费档」怎么判定：**拿不到就按免费档处理**（用户口径）。
//   · `/api/me/context` 若将来下发显式档位（`instant_tier` / `paid`），按它；
//   · 否则看 `/api/me/package` 的 `package_code`：`trial` / `free` / 空串＝免费，
//     其余（有在效付费包）＝付费。
//   方向必须是保守的：误判成免费只会把图压小一点（OCR 仍可用），
//   误判成付费会**把超过免费档的请求直接按积分计费**——那是静默收费，比压小坏得多。
// ============================================================================

/** 即时翻译的两档（顺序即从窄到宽，`instantLimits` 的单调性由它保证） */
export type InstantTier = 'free' | 'paid'

/** 一档的对外宽度：免费档也是公开卖点（必须大方写），只有剩余额度是隐形的 */
export type InstantLimits = {
  /** 图片体积上限（字节） */
  imageMaxBytes: number
  /** 图片长边上限（px；压缩时允许的下限另见 imageCompress 的地板） */
  imageMaxSide: number
  /** 录音时长上限（秒） */
  audioMaxSec: number
}

const MIB = 1024 * 1024

/**
 * 两档真值（与方案 §1.1 的卖点表逐项一致）：
 *   免费档 图片 1 MiB / 长边 1600 / 音频 30 s
 *   付费档 图片 3 MiB / 长边 2000 / 音频 60 s
 * ⚠️ 付费档的图片 3 MiB 同时是**即时翻译硬顶**：超过它不再压缩，直接引导翻译工单。
 */
export const INSTANT_TIER_LIMITS: Record<InstantTier, InstantLimits> = {
  free: { imageMaxBytes: 1 * MIB, imageMaxSide: 1600, audioMaxSec: 30 },
  paid: { imageMaxBytes: 3 * MIB, imageMaxSide: 2000, audioMaxSec: 60 },
}

/** instantLimits 取某一档的宽度；未知值一律回落免费档（保守方向见文件头） */
export function instantLimits(tier: InstantTier | null | undefined): InstantLimits {
  return tier === 'paid' ? INSTANT_TIER_LIMITS.paid : INSTANT_TIER_LIMITS.free
}

/** 即时翻译的**硬顶**（超过即引导工单）：付费档就是硬顶，免费档不是 */
export function instantHardLimits(): InstantLimits {
  return INSTANT_TIER_LIMITS.paid
}

/** 从单个来源里读显式档位；读不到返回 null（不猜） */
function explicitTierOf(source: unknown): InstantTier | null {
  if (!source || typeof source !== 'object') return null
  const src = source as Record<string, unknown>
  const raw = src.instant_tier ?? src.paid
  if (typeof raw === 'boolean') return raw ? 'paid' : 'free'
  if (typeof raw === 'string') {
    const v = raw.trim().toLowerCase()
    if (v === 'paid') return 'paid'
    if (v === 'free') return 'free'
  }
  // /api/me/package 的包编码：空串 / trial / free 视为免费档，其余视为有在效付费包
  const code = typeof src.package_code === 'string' ? src.package_code.trim().toLowerCase() : ''
  if (code) return code === 'trial' || code === 'free' ? 'free' : 'paid'
  return null
}

/** resolveInstantTier 合并多个来源（context、package…）：任一确认付费即付费，否则免费 */
export function resolveInstantTier(...sources: unknown[]): InstantTier {
  for (const src of sources) {
    if (explicitTierOf(src) === 'paid') return 'paid'
  }
  return 'free'
}

/**
 * isKnownFreeAccount 是否**明确**知道这是免费档账号（`instant_tier=free` 或包编码为 trial/free）。
 *
 * 与 resolveInstantTier 的分工（别合并成一个函数）：
 *   · 压缩/录音的档位**取不到也按免费**（保守方向，见文件头）；
 *   · 但「首屏隐藏余额条」这件事**只能对明确免费的账号做**——读不到档位时把余额条藏起来
 *     会让老后端/降级态下的正常用户看不到自己的余额（那是一条既有的产品契约，
 *     O-9 与 F-48 两条锁都在钉它）。所以这里刻意只认显式信号，读不到返回 false。
 */
export function isKnownFreeAccount(source: unknown): boolean {
  if (explicitTierOf(source) === 'free') return true
  if (!source || typeof source !== 'object') return false
  const src = source as Record<string, unknown>
  // `package_code` **键存在但值为空**＝后端表态过"没有在效付费包"（/api/me/package 的既有语义，
  // 见 selfservice 的 hasPlan = code && code !== 'trial'）。键不存在（老后端／降级体）才算"读不到"，
  // 那种形态保留余额展示，不按免费藏。
  return Object.prototype.hasOwnProperty.call(src, 'package_code')
    && (src.package_code == null || src.package_code === '')
}

/**
 * isInstantFreeOff 读「免费档被运营关掉」的状态词。
 * 后端在 `instant_free` 上出 `off`（H-2 口径：只出状态词、不出额度数字）。
 * 语义：关掉免费档时界面不许再显「免费」徽标（那会是假承诺），并改按付费档展示。
 */
export function isInstantFreeOff(source: unknown): boolean {
  if (!source || typeof source !== 'object') return false
  const raw = (source as Record<string, unknown>).instant_free
  if (raw === false) return true
  if (typeof raw === 'string') {
    const v = raw.trim().toLowerCase()
    return v === 'off' || v === 'false' || v === 'disabled'
  }
  return false
}
