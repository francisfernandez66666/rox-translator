// ============================================================================
// lib/imageCompress.ts — 即时翻译「选中图片先压缩」的唯一实现（★ 2026-10-30 免费档批 D-1）
//
// 为什么必须压缩而不是直接拒收（方案 §4.4 的取向）：
//   手机截图 2~8 MB 是常态，免费档上限 1 MiB 若直接判死，免费入口就变成劝退入口。
//   文档类截图压到长边 1600 / JPEG 0.72 通常落在 300–600 KB，OCR 检测与识别档都够用。
//
// 三条硬口径（改这里之前逐条读）：
//   ① **长边地板 1200px 不许破**：把图压到 OCR 读不出字，是免费档自己制造差评
//      （G16 的反证腿钉的就是「质量压到 0.4 也算通过」这条）。因此候选长边只会走到
//      `floorSide`（默认 1200）就停手；再压不下去就返回 `compressed:false` 并把**原图**
//      交回调用方，由调用方出「按原图付费 / 工单」二选一，而不是擅自交出糊图。
//   ② **质量档只有三档**（0.85 → 0.72 → 0.6）：四档以上收益极小，却把大图的编码时间翻倍
//      （用户点完图片一直转圈，比多几十 KB 更坏）。
//   ③ **不做任何 DOM 全局依赖的硬编码**：解码/绘制通过 `deps.decode` 可注入。
//      这不是为了「方便测试」——jsdom 里 canvas 是空实现，没有这条缝就无法对
//      「压得下 / 压不下 / 地板不破」三种行为做**行为级**断言（只能测纯函数，等于没测到管线）。
// ============================================================================

/** compressImage 的目标档：字节上限与允许的最长边（档位真值见 lib/instantTier.ts） */
export type CompressImageOptions = {
  /** 目标字节上限（超过它才压缩；压到它以内即成功） */
  maxBytes: number
  /** 允许缩放到的最大长边（免费档 1600 / 付费档 2000） */
  maxSide: number
  /** 长边地板，默认 1200：任何候选缩放都不会小于它（可识别性地板，见文件头①） */
  floorSide?: number
  /** 质量迭代档，默认 [0.85, 0.72, 0.6] */
  qualities?: number[]
}

/** 压缩结果：`compressed=false` 时 blob 是**原文件**（调用方据此走二选一，不许当成功件） */
export type CompressImageResult = {
  blob: Blob
  width: number
  height: number
  compressed: boolean
}

/** 解码后的可绘制源：render(longSide, quality) 返回一次编码产物 */
type DecodedImage = {
  width: number
  height: number
  render: (longSide: number, quality: number) => Promise<Blob | null>
  close?: () => void
}

/** 可注入依赖：生产走 canvas；测试可喂一个假解码器（见文件头③） */
export type CompressDeps = {
  decode?: (file: File) => Promise<DecodedImage | null>
}

/** 缺省质量档（顺序即尝试顺序：先高质量、再逐步降） */
const DEFAULT_QUALITIES = [0.85, 0.72, 0.6]
/** 长边地板缺省值：1200px（方案 §4.4 的「可识别性地板」） */
export const IMAGE_COMPRESS_FLOOR_SIDE = 1200

/** loadDrawable 取一个可 drawImage 的源。优先 createImageBitmap（不占 DOM、能关），
 *  否则回落 `<img>` + objectURL（Safari 旧版与 jsdom 走这条）。 */
async function loadDrawable(file: File): Promise<{
  source: CanvasImageSource
  width: number
  height: number
  close: () => void
} | null> {
  const anyGlobal = globalThis as unknown as { createImageBitmap?: (b: Blob) => Promise<ImageBitmap> }
  if (typeof anyGlobal.createImageBitmap === 'function') {
    try {
      const bmp = await anyGlobal.createImageBitmap(file)
      return { source: bmp, width: bmp.width, height: bmp.height, close: () => bmp.close?.() }
    } catch {
      // HEIC / 多页 TIFF 等解不开的形态：直接返回 null，由调用方走「不压，二选一」
      return null
    }
  }
  if (typeof Image === 'undefined' || typeof URL === 'undefined' || !URL.createObjectURL) return null
  const url = URL.createObjectURL(file)
  try {
    const img = new Image()
    const loaded = new Promise<boolean>((resolve) => {
      img.onload = () => resolve(true)
      img.onerror = () => resolve(false)
    })
    img.src = url
    if (!(await loaded)) return null
    return {
      source: img,
      width: img.naturalWidth || img.width,
      height: img.naturalHeight || img.height,
      close: () => URL.revokeObjectURL(url),
    }
  } catch {
    URL.revokeObjectURL(url)
    return null
  }
}

/** 缺省解码器：`<canvas>` 等比缩放 + `toBlob('image/jpeg', quality)`。
 *  canvas 只建一次、每次 render 重设尺寸——反复 createElement 在移动端会累积 GPU 纹理。 */
async function decodeWithCanvas(file: File): Promise<DecodedImage | null> {
  const drawable = await loadDrawable(file)
  if (!drawable || !drawable.width || !drawable.height) return null
  const canvas = document.createElement('canvas')
  const ctx = canvas.getContext('2d')
  if (!ctx) {
    drawable.close()
    return null
  }
  const { source, width, height } = drawable
  const render = (longSide: number, quality: number): Promise<Blob | null> => new Promise((resolve) => {
    const scale = Math.min(1, longSide / Math.max(width, height))
    canvas.width = Math.max(1, Math.round(width * scale))
    canvas.height = Math.max(1, Math.round(height * scale))
    // 白底：PNG 透明区直接转 JPEG 会变黑（截图里半透明遮罩很常见）
    ctx.fillStyle = '#FFFFFF'
    ctx.fillRect(0, 0, canvas.width, canvas.height)
    ctx.drawImage(source, 0, 0, canvas.width, canvas.height)
    canvas.toBlob((b) => resolve(b), 'image/jpeg', quality)
  })
  return { width, height, render, close: drawable.close }
}

/**
 * compressImage 把图片压到目标字节以内。
 *
 * 行为（与调用方的分工）：
 *  · 原图已 ≤ maxBytes ⇒ 原样返回（`compressed:false`），不做任何重编码（无损优先）；
 *  · 解码失败（HEIC/多页 TIFF）⇒ 原图返回 `compressed:false`，调用方提示二选一；
 *  · 逐档尝试「长边 × 质量」，命中目标即返回 `compressed:true`；
 *  · 长边地板（1200）都不够 ⇒ 原图返回 `compressed:false`，**绝不**交出糊图。
 */
export async function compressImage(
  file: File,
  opts: CompressImageOptions,
  deps: CompressDeps = {},
): Promise<CompressImageResult> {
  const target = Math.max(1, Math.floor(opts.maxBytes))
  const floorSide = Math.max(1, Math.floor(opts.floorSide ?? IMAGE_COMPRESS_FLOOR_SIDE))
  const qualities = (opts.qualities && opts.qualities.length ? opts.qualities : DEFAULT_QUALITIES)
    .filter((q) => q > 0 && q <= 1)
  // 已经在目标内：不重编码（每次重编码都会掉一点质量，且白白多一次 CPU 等待）
  if (file.size <= target) {
    return { blob: file, width: 0, height: 0, compressed: false }
  }

  let decoded: DecodedImage | null = null
  try {
    decoded = await (deps.decode ?? decodeWithCanvas)(file)
  } catch {
    decoded = null
  }
  if (!decoded || !decoded.width || !decoded.height) {
    return { blob: file, width: 0, height: 0, compressed: false }
  }

  // 候选长边：maxSide（不放大）→ 中间档 → 地板。全部 ≥ floorSide，见文件头①。
  const naturalLong = Math.max(decoded.width, decoded.height)
  const startSide = Math.min(naturalLong, Math.max(1, Math.floor(opts.maxSide)))
  const lowestSide = Math.min(naturalLong, floorSide)
  const sides: number[] = [startSide]
  if (startSide > lowestSide) {
    const mid = Math.max(lowestSide, Math.round(startSide * 0.75))
    if (mid < startSide) sides.push(mid)
    if (lowestSide < mid) sides.push(lowestSide)
  }

  let best: Blob | null = null
  try {
    for (const side of sides) {
      for (const quality of qualities) {
        const out = await decoded.render(side, quality)
        if (!out || out.size === 0) continue
        if (!best || out.size < best.size) best = out
        if (out.size <= target) {
          return { blob: out, width: decoded.width, height: decoded.height, compressed: true }
        }
      }
    }
  } finally {
    decoded.close?.()
  }
  // 压不到目标：保留**原图**（不交糊图、也不交「best」这种半成品——它的长边可能已被缩到地板）
  return { blob: file, width: decoded.width, height: decoded.height, compressed: false }
}

/** toJpegFile 把压缩产物换名为 `.jpg` / `image/jpeg`（后端按扩展名与 MIME 分流，两者必须同步改）。 */
export function toJpegFile(blob: Blob, originalName: string): File {
  const base = (originalName || 'image').replace(/\.[^./\\]+$/, '') || 'image'
  return new File([blob], `${base}.jpg`, { type: 'image/jpeg' })
}
