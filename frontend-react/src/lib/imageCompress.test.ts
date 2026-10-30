// ============================================================================
// lib/imageCompress.test.ts — 选图压缩的三条行为腿（★ 2026-10-30 免费档批 D-1 / G16）
//
// 为什么用**注入解码器**而不是真 canvas：jsdom 里 `getContext('2d')` 是空实现，
//   `toBlob` 也不产出字节；没有注入缝就只能测纯函数，而「压得下/压不下/地板不破」
//   这三条恰恰是管线行为（见 imageCompress.ts 文件头③）。
//   本文件喂的是与生产同一条 `compressImage`，只把「解码 + 编码」换成可计数的假实现。
//
// 三条腿：
//   ① 压得下：先按 maxSide 高档试、不够再降质量，命中目标即返回 compressed=true；
//   ② 压不下：所有候选都超目标 ⇒ **原图**返回、compressed=false（调用方出二选一）；
//   ③ 长边地板 1200：候选长边永不低于地板（G16 的反证腿：把地板破掉就是交出糊图）；
//   另配两条边界：已在目标内不重编码、解码失败同样原图返回。
// 运行：npx vitest run src/lib/imageCompress.test.ts
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { compressImage, toJpegFile, IMAGE_COMPRESS_FLOOR_SIDE } from './imageCompress'

/** makeFile 造一个指定字节数的假图片文件（内容不参与判据，只有 size 有意义） */
function makeFile(bytes: number, name = 'shot.png'): File {
  return new File([new Uint8Array(bytes)], name, { type: 'image/png' })
}

/** fakeDecoder 记录每次 render 的入参；按 plan 返回对应大小的 blob */
function fakeDecoder(width: number, height: number, plan: (side: number, quality: number) => number | null) {
  const calls: Array<{ side: number; quality: number }> = []
  return {
    calls,
    decode: async () => ({
      width,
      height,
      render: async (side: number, quality: number) => {
        calls.push({ side, quality })
        const size = plan(side, quality)
        return size == null ? null : new Blob([new Uint8Array(size)])
      },
    }),
  }
}

describe('compressImage（免费档图片压缩）', () => {
  it('① 压得下：maxSide 高档先试，质量降一档命中目标 ⇒ compressed=true', async () => {
    const file = makeFile(4_000_000)
    // 1600/0.85 仍超 1 MB，1600/0.72 落到 900 KB
    const fake = fakeDecoder(3000, 2000, (side, q) => (side === 1600 && q > 0.8 ? 2_000_000 : 900_000))
    const out = await compressImage(file, { maxBytes: 1_000_000, maxSide: 1600 }, { decode: fake.decode })
    expect(out.compressed).toBe(true)
    expect(out.blob.size).toBe(900_000)
    expect(out.blob).not.toBe(file)
    // 先试最高质量档（顺序即尝试顺序）
    expect(fake.calls[0]).toEqual({ side: 1600, quality: 0.85 })
    expect(fake.calls.some((c) => c.quality === 0.72)).toBe(true)
  })

  it('② 压不下：所有候选都超目标 ⇒ 原图返回、compressed=false', async () => {
    const file = makeFile(4_000_000)
    const fake = fakeDecoder(3000, 2000, () => 2_500_000)
    const out = await compressImage(file, { maxBytes: 1_000_000, maxSide: 1600 }, { decode: fake.decode })
    expect(out.compressed).toBe(false)
    expect(out.blob).toBe(file) // 逐引用等值：调用方据此"保留原图"
    expect(out.width).toBe(3000)
    expect(out.height).toBe(2000)
  })

  it('③ 长边地板 1200 不许破：所有候选长边都 ≥ 1200（再压不下去就交原图）', async () => {
    const file = makeFile(6_000_000)
    const fake = fakeDecoder(4000, 2500, () => 3_000_000)
    const out = await compressImage(file, { maxBytes: 1_000_000, maxSide: 2000 }, { decode: fake.decode })
    expect(out.compressed).toBe(false)
    const sides = fake.calls.map((c) => c.side)
    expect(Math.max(...sides)).toBe(2000)
    expect(Math.min(...sides)).toBe(IMAGE_COMPRESS_FLOOR_SIDE)
    expect(sides.every((s) => s >= IMAGE_COMPRESS_FLOOR_SIDE), `候选长边越过地板：${sides.join(',')}`).toBe(true)
  })

  it('④ 已在目标内：不重编码，原样返回', async () => {
    const file = makeFile(800_000)
    const fake = fakeDecoder(1200, 800, () => 1)
    const out = await compressImage(file, { maxBytes: 1_000_000, maxSide: 1600 }, { decode: fake.decode })
    expect(out.compressed).toBe(false)
    expect(out.blob).toBe(file)
    expect(fake.calls.length).toBe(0)
  })

  it('⑤ 解码失败（HEIC/多页 TIFF）：原图返回，不抛异常', async () => {
    const file = makeFile(2_000_000)
    const out = await compressImage(file, { maxBytes: 1_000_000, maxSide: 1600 }, { decode: async () => null })
    expect(out.compressed).toBe(false)
    expect(out.blob).toBe(file)
  })

  it('⑥ 压缩产物换名为 .jpg / image/jpeg（扩展名与 MIME 必须同步改）', () => {
    const f = toJpegFile(new Blob([new Uint8Array(10)]), 'IMG_0001.HEIC')
    expect(f.name).toBe('IMG_0001.jpg')
    expect(f.type).toBe('image/jpeg')
  })
})
