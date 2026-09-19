// ============================================================================
// api/translate.segments.test.ts — ★ B3（方案 A2）SSE 段级事件解析回归（node 环境）
// 锁定的风险：后端新增 segment_done / segment_final / segments_sealed 三类帧，
// 前端解析器若漏接/接错（draft/target 拉平错位、placeholder 未收敛为布尔），
// 逐段上屏会整条链路静默失效。本测试直接喂假 reader 给导出的 consumeSSEStream。
// 运行：npx vitest run src/api/translate.segments.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { consumeSSEStream } from './translate'
import type { FileSegmentEvent, ProgressEvent } from '@/types'

/** sse 把对象包成一帧 SSE data 行（尾部空行会被解析器跳过） */
const sse = (o: unknown) => `data: ${JSON.stringify(o)}\n\n`

/** fakeReader 把若干字符串块包成 ReadableStream reader；分块边界即网络分片模拟 */
function fakeReader(frames: string[]): ReadableStreamDefaultReader<Uint8Array> {
  const chunks = frames.map((f) => new TextEncoder().encode(f))
  let i = 0
  return {
    read: async () => (i < chunks.length ? { done: false, value: chunks[i++] } : { done: true, value: undefined }),
    cancel: async () => {},
  } as unknown as ReadableStreamDefaultReader<Uint8Array>
}

const DONE = { type: 'done', result: { skill: 'file_translate', reply: '完成', data: { translations: { en: 'x' } }, files: ['/out/r.docx'] } }

describe('B3 consumeSSEStream · 段级事件分流', () => {
  it('① segment_* 归一化回调：done→draft、final→target 拉平为 text，sealed 只带 lang', async () => {
    const segs: FileSegmentEvent[] = []
    const progs: ProgressEvent[] = []
    const res = await consumeSSEStream(
      fakeReader([
        sse({ type: 'progress', step: '初翻', percent: 10 }),
        sse({ type: 'segment_done', lang: 'en', index: 0, source: '你好', source_hash: 'aaaa', draft: 'Hallo', stage: 'initial' }),
        sse({ type: 'segment_final', lang: 'en', index: 0, source: '你好', source_hash: 'aaaa', target: 'Hello', stage: 'reviewed' }),
        sse({ type: 'segment_final', lang: 'en', index: 3, source: '敏感', source_hash: 'bbbb', target: '[已屏蔽]', stage: 'gated', placeholder: true }),
        sse({ type: 'segments_sealed', lang: 'en' }),
        sse(DONE),
      ]),
      (e) => progs.push(e),
      '文件翻译出错',
      undefined,
      (e) => segs.push(e),
    )
    expect(res.reply).toBe('完成')
    expect(progs).toHaveLength(1)
    expect(segs).toHaveLength(4)
    // segment_done：draft 拉平进 text，字段全透传
    expect(segs[0]).toMatchObject({ kind: 'segment_done', lang: 'en', index: 0, source: '你好', sourceHash: 'aaaa', text: 'Hallo', stage: 'initial' })
    expect(segs[0].placeholder).toBe(false) // 缺省收敛为 false，消费端不必判别 undefined
    // segment_final：target 拉平进 text
    expect(segs[1]).toMatchObject({ kind: 'segment_final', text: 'Hello', stage: 'reviewed' })
    // placeholder:true 原样透传（敏感占位行）
    expect(segs[2]).toMatchObject({ kind: 'segment_final', index: 3, text: '[已屏蔽]', stage: 'gated', placeholder: true })
    // sealed：只有 kind+lang
    expect(segs[3]).toEqual({ kind: 'segments_sealed', lang: 'en' })
  })

  it('② 不传 onSegment：段级帧静默跳过，不影响 done 结果（旧调用方零改动）', async () => {
    const res = await consumeSSEStream(
      fakeReader([sse({ type: 'segment_done', lang: 'en', index: 0, draft: 'x' }), sse(DONE)]),
    )
    expect(res.reply).toBe('完成')
  })

  it('③ 帧跨网络分片被切开：buffer 续帧后仍完整解析', async () => {
    const raw = sse({ type: 'segment_done', lang: 'de', index: 7, draft: 'Guten Tag', stage: 'initial' })
    const segs: FileSegmentEvent[] = []
    await consumeSSEStream(fakeReader([raw.slice(0, 18), raw.slice(18), sse(DONE)]), undefined, '翻译出错', undefined, (e) => segs.push(e))
    expect(segs).toEqual([{ kind: 'segment_done', lang: 'de', index: 7, source: undefined, sourceHash: undefined, text: 'Guten Tag', stage: 'initial', placeholder: false }])
  })

  it('④ error 帧：已上屏段照常回调，随后抛 ApiError 且透传稳定错误码', async () => {
    const segs: FileSegmentEvent[] = []
    await expect(
      consumeSSEStream(
        fakeReader([
          sse({ type: 'segment_done', lang: 'en', index: 0, draft: 'Hallo' }),
          sse({ type: 'error', error: '余额不足', error_code: 'insufficient_balance' }),
        ]),
        undefined, '翻译出错', undefined, (e) => segs.push(e),
      ),
    ).rejects.toMatchObject({ code: 'insufficient_balance', message: '余额不足' })
    expect(segs).toHaveLength(1)
  })

  it('⑤ 注释帧（: ping）与非 data 行被忽略，不产生段事件', async () => {
    const segs: FileSegmentEvent[] = []
    await consumeSSEStream(
      fakeReader([': ping\n\n', sse(DONE)]),
      undefined, '翻译出错', undefined, (e) => segs.push(e),
    )
    expect(segs).toHaveLength(0)
  })
})
