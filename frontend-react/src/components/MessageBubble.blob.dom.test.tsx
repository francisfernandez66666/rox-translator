// ============================================================================
// MessageBubble.blob.dom.test.tsx — §4.2-1 附件 blob URL 生命周期回归（内存泄漏闸门）
// 背景：URL.createObjectURL 生成的引用不随 React 卸载自动回收。旧实现气泡挂载时
//   为图片附件 create 了 objectURL 却从不 revoke——每打开一条带图消息就永久泄漏一块
//   Blob 内存，随会话线性增长。#57 修复批补了「登记 create↔卸载 revoke」配对。
// 本测锁三件事（改坏即红）：
//   ① 正常路径：挂载预取图片 → 卸载时对本组件创建过的每个 objectURL 恰好 revoke 一次；
//   ② 竞态路径：图片尚在 fetch 途中组件就被卸载 → 落地后**就地** revoke（不挂进已销毁实例的登记表）；
//   ③ 鉴权路径：附件 401 必须交给 core 的 handleUnauthorized（统一清登录态），不得静默返回空串。
// 只测 blob 生命周期，不重跑渲染分支（渲染契约见同目录 draft/segments 测试）。
// 运行：npx vitest run src/components/MessageBubble.blob.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, cleanup, waitFor } from '@testing-library/react'
import { setLang } from '@/i18n'
import type { ChatMessage } from '@/types'

// 气泡只在附件 blob 鉴权时用 API_BASE/getAuthToken（+ 401 走 core 的 handleUnauthorized），
// 本测整体桩掉 @/api 免打网络；handleUnauthorized 桩成可断言的 spy（见用例 ③）。
const apiMocks = vi.hoisted(() => ({ handleUnauthorized: vi.fn() }))
vi.mock('@/api', () => ({ API_BASE: '', getAuthToken: () => 'tk', handleUnauthorized: apiMocks.handleUnauthorized }))

import MessageBubble from './MessageBubble'

/** objectURL 计数器：每次 createObjectURL 返回唯一串，供 revoke 配对断言 */
let urlSeq = 0
const created: string[] = []
const revoked: string[] = []

beforeEach(() => {
  cleanup()
  setLang('zh')
  urlSeq = 0
  created.length = 0
  revoked.length = 0
  apiMocks.handleUnauthorized.mockClear()
  // jsdom 的 URL 不实现 object URL，显式装桩以观测 create/revoke 配对
  ;(URL as unknown as { createObjectURL: (b: unknown) => string }).createObjectURL = vi.fn(() => {
    const u = `blob:gen-${++urlSeq}`
    created.push(u)
    return u
  })
  ;(URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = vi.fn((u: string) => {
    revoked.push(u)
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const imgMsg = (): ChatMessage => ({
  id: 'm1', role: 'assistant', content: '', timestamp: Date.now(),
  files: ['/out/photo.png'], // 命中 isImage → 挂载即预取 blob
})

/** blobResp 造 fetch 返回：ok + blob() */
function blobResp(blob: unknown): Response {
  return { ok: true, status: 200, blob: async () => blob } as unknown as Response
}

describe('MessageBubble · 附件 blob URL create↔revoke 配对', () => {
  it('① 正常路径：挂载预取生成一个 objectURL，卸载时被恰好释放一次', async () => {
    vi.stubGlobal('fetch', async () => blobResp({ kind: 'blob' }))
    const { unmount } = render(<MessageBubble message={imgMsg()} />)
    // 预取是异步的：等待 createObjectURL 被调用
    await waitFor(() => expect(created).toHaveLength(1))
    expect(revoked, '挂载期间不得提前 revoke').toHaveLength(0)

    unmount()
    // 卸载：本组件创建过的 objectURL 必须逐个 revoke（配对释放）
    expect(revoked).toEqual([created[0]])
    expect(URL.revokeObjectURL, '走的是全局 URL.revokeObjectURL').toHaveBeenCalledTimes(1)
  })

  it('② 竞态路径：fetch 途中卸载 → 落地后就地 revoke，不泄漏给已销毁实例', async () => {
    let resolveFetch: (r: Response) => void = () => {}
    vi.stubGlobal('fetch', () => new Promise<Response>((res) => { resolveFetch = res }))
    const { unmount } = render(<MessageBubble message={imgMsg()} />)
    // 卸载发生在响应落地之前（aliveRef 翻否）
    unmount()
    resolveFetch(blobResp({ kind: 'blob' }))
    // 等微任务把 loadBlobUrl 续体跑完
    await waitFor(() => expect(created).toHaveLength(1))
    // 关键：卸载后创建的 objectURL 立即被就地 revoke（否则就是无人认领的泄漏）
    expect(revoked).toEqual([created[0]])
  })

  it('③ 401 路径：走 core 的 handleUnauthorized 统一清登录态，不再静默留半登录 UI', async () => {
    // 旧实现把 401 和其余非 2xx 一起 `return ''`：登录过期时图片全裂、按钮点了没反应，
    // 用户仍停在「看起来已登录」的会话页——补这条断言钉死：401 必须上交给 core。
    vi.stubGlobal('fetch', async () => ({ ok: false, status: 401 }) as unknown as Response)
    render(<MessageBubble message={imgMsg()} />)
    await waitFor(() => expect(apiMocks.handleUnauthorized).toHaveBeenCalledTimes(1))
    expect(created, '401 不应产生 objectURL').toHaveLength(0)
  })
})
