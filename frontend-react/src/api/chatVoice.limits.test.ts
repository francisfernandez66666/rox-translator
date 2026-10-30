// ============================================================================
// api/chatVoice.limits.test.ts — 录音 24 kbps ＋ 按档自动停 ＋ 计时器清理（★ 2026-10-30 D-3 / G11）
//
// 为什么单独一条（而不并进 chatVoice.lock.test.ts）：那份锁的是**失败档位文案**的跨语言等值；
//   本文件锁的是录音器行为（码率、到点自动停、取消后不再跳秒）。两者用不同的假件
//   （那边只读源码文本，这边要假 MediaRecorder + 假计时器）。
//
// 三条腿：
//   ① `audioBitsPerSecond: 24000` 必须真的传进 MediaRecorder（免费档的成本顶压在这一层）；
//   ② maxSec 到点自动停：秒回调逐格递减、到 0 自动收尾并回调 onAutoStop（免费 30 / 付费 60 由调用方给）；
//   ③ cancel 之后计时器必须清掉（旧形态的计时器只活在调用方，取消/卸载会留下后台跳秒）。
// 运行：npx vitest run src/api/chatVoice.limits.test.ts
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { startMicRecording } from './chatVoice'

/** FakeRecorder 只实现本模块用到的那一面（start/stop/onstop/ondataavailable） */
class FakeRecorder {
  static last: FakeRecorder | null = null
  state = 'inactive'
  mimeType = 'audio/webm'
  ondataavailable: ((e: { data: Blob }) => void) | null = null
  onstop: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(public stream: unknown, public opts?: { audioBitsPerSecond?: number }) {
    FakeRecorder.last = this
  }
  start() {
    this.state = 'recording'
  }
  stop() {
    this.state = 'inactive'
    this.ondataavailable?.({ data: new Blob(['audio']) })
    this.onstop?.()
  }
}

const track = { stop: vi.fn() }

beforeEach(() => {
  vi.useFakeTimers()
  FakeRecorder.last = null
  track.stop.mockClear()
  Object.defineProperty(window, 'MediaRecorder', { configurable: true, writable: true, value: FakeRecorder })
  Object.defineProperty(navigator, 'mediaDevices', {
    configurable: true,
    value: { getUserMedia: vi.fn(async () => ({ getTracks: () => [track] })) },
  })
})

afterEach(() => {
  vi.useRealTimers()
  // @ts-expect-error 清理测试桩（jsdom 原本没有 MediaRecorder）
  delete window.MediaRecorder
})

describe('startMicRecording：码率与到点自动停', () => {
  it('① 码率固定 24 kbps（语音识别够用，免费档成本顶靠它）', async () => {
    const ctrl = startMicRecording(() => {}, { maxSec: 30 })
    await vi.advanceTimersByTimeAsync(0)
    expect(FakeRecorder.last?.opts).toMatchObject({ audioBitsPerSecond: 24000 })
    ctrl?.cancel()
  })

  it('② maxSec=3：逐秒回调到 0，到点自动收尾并回调 onAutoStop（带 Blob）', async () => {
    const ticks: Array<[number, number]> = []
    const auto = vi.fn()
    const ctrl = startMicRecording(() => {}, {
      maxSec: 3,
      onTick: (elapsed, remaining) => ticks.push([elapsed, remaining]),
      onAutoStop: auto,
    })
    await vi.advanceTimersByTimeAsync(3000)
    expect(ticks).toEqual([[1, 2], [2, 1], [3, 0]])
    expect(auto).toHaveBeenCalledTimes(1)
    expect(auto.mock.calls[0][0]).toBeInstanceOf(Blob)
    expect(FakeRecorder.last?.state).toBe('inactive')
    // 自动停之后不应再跳秒
    await vi.advanceTimersByTimeAsync(3000)
    expect(ticks.length).toBe(3)
    ctrl?.cancel()
  })

  it('③ cancel：计时器清掉、麦克风轨道释放、后续不再回调', async () => {
    const ticks: number[] = []
    const ctrl = startMicRecording(() => {}, { maxSec: 10, onTick: (e) => ticks.push(e) })
    await vi.advanceTimersByTimeAsync(1000)
    expect(ticks).toEqual([1])
    ctrl?.cancel()
    await vi.advanceTimersByTimeAsync(5000)
    expect(ticks).toEqual([1])
    expect(track.stop).toHaveBeenCalled()
  })
})
