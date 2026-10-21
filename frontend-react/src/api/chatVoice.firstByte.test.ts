// @vitest-environment node
// ============================================================================
// chatVoice.firstByte.test.ts — M8 朗读首字节 5s 探针
//
// 缺陷形态：TTS 进程已启动但上游卡在模型加载/排队，迟迟不吐第一个字节；
// 旧写法 `fetch` 只等响应头，按钮永远停在 loading。这里用真 ReadableStream 钉住：
//   F1 首字节按时到 ⇒ 完整拼回 Blob（不能把分块读坏）；
//   F2 5s 无首字节 ⇒ 必须 abort 并抛 reason=timeout（不是 failed）。
// ============================================================================
import { afterEach, describe, expect, it, vi } from 'vitest'
import { speakAloud } from './chatVoice'

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('speakAloud 首字节探针（M8）', () => {
  it('F1 首字节按时到 ⇒ 分块完整拼回 Blob', async () => {
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        c.enqueue(new Uint8Array([1, 2]))
        setTimeout(() => { c.enqueue(new Uint8Array([3])); c.close() }, 5)
      },
    })
    vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { headers: { 'Content-Type': 'audio/wav' } })))
    const blob = await speakAloud('hello', 'en')
    expect(Array.from(new Uint8Array(await blob.arrayBuffer()))).toEqual([1, 2, 3])
    expect(blob.type).toBe('audio/wav')
  })

  it('F2 5s 无首字节 ⇒ abort 并抛 timeout 档', async () => {
    vi.useFakeTimers()
    const body = new ReadableStream<Uint8Array>({ start() { /* 永不吐字节 */ } })
    vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { headers: { 'Content-Type': 'audio/wav' } })))
    // 先挂上处理函数，避免 fake timer 触发时被判成 unhandled rejection。
    const p = speakAloud('hello', 'en').catch((e) => e)
    await vi.advanceTimersByTimeAsync(5_001)
    const err = await p
    expect(err?.reason).toBe('timeout')
  })
})
