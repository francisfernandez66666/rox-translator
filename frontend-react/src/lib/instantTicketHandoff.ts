// ============================================================================
// lib/instantTicketHandoff.ts — 「带文件跳工单页」的一次性交接（★ 2026-10-30 D-5）
//
// 背景：即时翻译把超限文件引导到翻译工单时，客户已经在工作台选过一次文件；
//   到了工单页还要再选一遍，是这条转化路径上最容易掉人的一步（方案 §6.1 ③）。
//   本模块就是那一次交接：工作台把文件（能内联就内联）写进 sessionStorage，
//   工单页挂载时**读一次就删**，重建 File 并预填到建单区。
//
// 三条口径：
//   ① **一次性**：读侧先 removeItem 再返回，任何一次读取（包括读到坏数据）都会清键——
//      否则上一单的文件会在下次进工单页时"阴魂不散"地又出现一次。
//   ② **体积诚实**：sessionStorage 只有几 MB 量级，超过内联上限（1.5 MB）的文件只带
//      `name/size/type` 元数据；工单页据此提示「请在下方重新选择这个文件」。
//      绝不把截断的 base64 拼成一个坏 File——那会让客户建出 0 字节的工单。
//   ③ **存储失败不阻断跳转**：写不进去（隐私模式/配额满）只返回 false，调用方照常跳页
//      （最坏是回到"重新选一次"的旧体验），不许因为一次存储异常把墙的出路堵死
//      （同 AGENTS §一·10「新增防护不拿可用性交押金」）。
//
// ⚠️ 读存储一律 try/catch：浏览器拒绝站点数据时读 sessionStorage 会**抛 SecurityError**，
//   而不是返回 null（AGENTS §一·5 启动链黑屏批①）。
// ============================================================================

/** sessionStorage 键（带站点前缀，避免与其它模块撞名） */
export const INSTANT_TICKET_HANDOFF_KEY = 'lc-instant-ticket-handoff'

/** 能内联的文件上限：base64 膨胀 4/3，留出 sessionStorage 剩余配额 */
export const INSTANT_HANDOFF_INLINE_MAX_BYTES = 1_500_000

/** 交接载荷：dataUrl 可选（超限或读取失败时缺席，读侧退回"请重新选择"） */
export type InstantTicketHandoff = {
  name: string
  size: number
  type: string
  dataUrl?: string
  /** 来源原因码（排障用；界面不直接显示，文案由调用方按词典取） */
  reason?: string
  /** 写入时刻（毫秒），用于丢弃明显过期的交接（例如浏览器恢复了上次会话的标签页） */
  at?: number
}

/** fileToDataUrl 读成 data URL；失败返回 null（不抛——调用方要的是"能不能带"，不是异常） */
export function fileToDataUrl(blob: Blob): Promise<string | null> {
  return new Promise((resolve) => {
    try {
      const reader = new FileReader()
      reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : null)
      reader.onerror = () => resolve(null)
      reader.readAsDataURL(blob)
    } catch {
      resolve(null)
    }
  })
}

/**
 * writeInstantTicketHandoff 把文件写进一次性交接槽。
 * 返回 true＝至少元数据已写入（dataUrl 可能因体积缺席）；false＝存储不可用。
 */
export async function writeInstantTicketHandoff(
  blob: Blob,
  name: string,
  reason = '',
): Promise<boolean> {
  const dataUrl = blob.size <= INSTANT_HANDOFF_INLINE_MAX_BYTES ? await fileToDataUrl(blob) : undefined
  const payload: InstantTicketHandoff = {
    name: name || (blob instanceof File ? blob.name : '') || 'file',
    size: blob.size,
    type: blob.type || '',
    reason,
    at: Date.now(),
  }
  if (dataUrl) payload.dataUrl = dataUrl
  try {
    sessionStorage.setItem(INSTANT_TICKET_HANDOFF_KEY, JSON.stringify(payload))
    return true
  } catch {
    return false
  }
}

/** readInstantTicketHandoffOnce 读一次即删；坏数据同样清键并返回 null */
export function readInstantTicketHandoffOnce(): InstantTicketHandoff | null {
  let raw: string | null = null
  try {
    raw = sessionStorage.getItem(INSTANT_TICKET_HANDOFF_KEY)
    sessionStorage.removeItem(INSTANT_TICKET_HANDOFF_KEY)
  } catch {
    return null
  }
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as Partial<InstantTicketHandoff>
    if (!parsed || typeof parsed.name !== 'string' || !parsed.name) return null
    if (typeof parsed.size !== 'number' || parsed.size < 0) return null
    return {
      name: parsed.name,
      size: parsed.size,
      type: typeof parsed.type === 'string' ? parsed.type : '',
      dataUrl: typeof parsed.dataUrl === 'string' ? parsed.dataUrl : undefined,
      reason: typeof parsed.reason === 'string' ? parsed.reason : '',
      at: typeof parsed.at === 'number' ? parsed.at : undefined,
    }
  } catch {
    return null
  }
}

/** dataUrlToBytes 解 base64（atob 是浏览器能力；解不开返回 null，不抛） */
function dataUrlToBytes(dataUrl: string): { bytes: Uint8Array; type: string } | null {
  try {
    const m = /^data:([^;,]*)[^,]*,(.*)$/s.exec(dataUrl)
    if (!m) return null
    const binary = atob(m[2])
    const bytes = new Uint8Array(binary.length)
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
    return { bytes, type: m[1] || '' }
  } catch {
    return null
  }
}

/** fileFromHandoff 把交接载荷重建为 File；没有内联数据或解不开时返回 null（调用方提示重选） */
export function fileFromHandoff(h: InstantTicketHandoff | null): File | null {
  if (!h || !h.dataUrl) return null
  const decoded = dataUrlToBytes(h.dataUrl)
  if (!decoded) return null
  try {
    return new File([decoded.bytes], h.name, { type: decoded.type || h.type || 'application/octet-stream' })
  } catch {
    return null
  }
}
