// ============================================================================
// api/instantCaps.ts — 即时翻译「对外档位宽度」的公开读数（★ 2026-10-30 组4 / S2）
// 职责：读 `GET /api/instant/caps`，把运营现值（免费/付费两档的图片体积与录音时长）交给
//       `lib/instantTier.ts` 的覆盖层；界面据此压缩/录音/提示，与后端判档同源。
// 口径：
//   · 只读、只含宽度（后端**不出**任何日额度数字，见 H-2/W-4）；
//   · 失败一律 fail-open：保留出厂默认，不弹错、不阻塞首屏（一次抖动不该把档位钉死）；
//   · 这是公开端点，不带鉴权头（未登录的官网/引导卡也要能读到同一份数字）。
// ============================================================================
import { request, bizResp } from './core'
import { applyInstantCaps } from '@/lib/instantTier'

/** 后端公开档位字段（只声明消费到的部分；未知字段忽略）。 */
export type InstantCaps = {
  free_on?: boolean
  free_image_max_mb?: number
  free_audio_max_sec?: number
  image_max_mb?: number
  audio_max_sec?: number
  free_lang_limit?: number
  ticket_max_mb?: number
}

/** done 成功拉取过就置位（同一页面生命周期内只读一次；运营改档要刷新页面才生效，与其它公开读数同档）。 */
let done = false

/**
 * loadInstantCaps 拉一次现值并应用到两档宽度。
 * 返回：成功＝响应体（调用方可读 free_on 决定是否收起「免费」徽标）；失败＝null（保留默认）。
 */
export async function loadInstantCaps(): Promise<InstantCaps | null> {
  if (done) return null
  try {
    const resp = await bizResp(() => request('/api/instant/caps'))
    const payload = (resp || {}) as InstantCaps & { success?: boolean }
    if (!resp || payload.success === false) return null
    const caps = payload
    applyInstantCaps(caps)
    done = true
    return caps
  } catch {
    // 网络抖动/被拦截：保留出厂默认（见文件头 fail-open 口径），下次挂载再试。
    return null
  }
}

/** resetInstantCapsCache 测试/登出用：清掉"已读"标记（配合 lib 的 resetInstantCaps）。 */
export function resetInstantCapsCache(): void {
  done = false
}
