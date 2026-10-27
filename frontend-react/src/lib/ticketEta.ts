// ============================================================================
// lib/ticketEta.ts — 「这类件通常要等多久」的唯一事实源（★ 2026-10-27 P2-01）
//
// 立项实证（2026-10-27 云端 UAT）：一张 1.67 MB 的品牌海报 PNG 走 OCR＋原位回填，
//   07:13:31 建单 → 07:19:02 完成 ＝ **5 分 31 秒**，而界面上只有「进度 30%」——
//   访客无从区分"还在跑"与"已经卡死"，只能反复刷或以为失败了重发一单（重发＝再扣一次积分）。
//
// 两条口径：
//   ① 判档**按扩展名集合派生**，而那两个集合的唯一事实源在 `api/translate.ts`
//      （`IMAGE_EXTS`／`AUDIO_EXTS`）。这里**禁止**再抄一份扩展名清单——
//      本仓两份图片/音频列表漂过一次（一边加了 `.heic`／`.opus` 另一边没加），
//      漂的后果就是"图片按文档档报耗时"，比不报更坏（给了一个错的预期）。
//   ② 只给**保守上限**，不给"剩余时间"（我们没有任何可靠的进度速率读数可以支撑它，
//      猜一个"还剩 2 分钟"就是承诺；宁可报长不报短——与 §一·5④ 那句
//      "少算＝客户以为更便宜"同方向，这里是"少算＝客户以为卡死"）。
// ============================================================================
import { AUDIO_EXTS, IMAGE_EXTS } from '@/api/translate'

/** 等待档位：无文件的文本单 / 普通文档件 / 图片件（OCR＋原位回填） / 音频件（转写＋配音） */
export type WaitTier = 'text' | 'document' | 'image' | 'audio'

// 档位分钟数（上限口径，来源＝2026-10-27 现网读数与同批图片件实测 5 分 31 秒）
const WAIT_MINUTES: Record<WaitTier, number> = { text: 1, document: 3, image: 6, audio: 8 }

/** ticketWaitTier 按件名判档：无扩展名一律按文本单（最保守的那一档）。
    参数 name: 工单标题——文件工单的标题就是上传件原名（含中文与空格都可能）。 */
export function ticketWaitTier(name?: string): WaitTier {
  const raw = String(name || '').trim().toLowerCase()
  if (!raw) return 'text'
  const dot = raw.lastIndexOf('.')
  if (dot < 0 || dot === raw.length - 1) return 'document' // 有名字但没扩展名：按文档件给上限
  const ext = raw.slice(dot)
  if ((IMAGE_EXTS as readonly string[]).includes(ext)) return 'image'
  if ((AUDIO_EXTS as readonly string[]).includes(ext)) return 'audio'
  return 'document'
}

/** ticketWaitMinutes 取档位对应的"通常约需几分钟"（界面只报事实，不报剩余、不报承诺）。 */
export function ticketWaitMinutes(tier: WaitTier): number {
  return WAIT_MINUTES[tier]
}
