// ============================================================================
// api/tickets.download.test.ts — 交付件下载「慢/坏分开报」＋字节进度（★ 2026-10-19 §五#2）
//
// 由来（境内链路与全球加速方案 §五#2）：跨境链路上一份交付件常常「还在传」就超时，
//   旧实现把 fetch 失败、4xx/5xx、SPA 兜底 HTML 三种情况**一律包成同一个 Error**，
//   用户看到的是一句含混的"下载失败"，排查方向被引向"文件坏了"（其实只是这一跳没走完，
//   重试就好）。本文件把三类分开钉死：
//     · N1 fetch 被拒 ⇒ kind='network'（重试有用）；
//     · N2 5xx ⇒ kind='broken' + status（重试没用）；
//     · N3 200 text/html（缺件时的 SPA 兜底页，§一·6）⇒ kind='broken'；
//     · N4 收流中途断掉 ⇒ kind='network' 且 received>0（"还在传，已收到 N 字节"的现场）；
//     · N5 正常收完 ⇒ onProgress 递进、拿到 blob、触发一次下载（回退了也不能没有进度）；
//     · N6 401 ⇒ 交给 handleUnauthorized（登录态语义与统一 client 同源，不吞成 network）。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const coreMocks = vi.hoisted(() => ({
  handleUnauthorized: vi.fn(),
}))
vi.mock('./core', () => ({
  bizResp: (fn: () => unknown) => fn(),
  request: vi.fn(),
  authHeaders: () => ({ Authorization: 'Bearer test' }),
  API_BASE: '',
  handleUnauthorized: coreMocks.handleUnauthorized,
  handleForbidden: (m: string) => m,
  apiMsg: (_k: string, f: string) => f,
  ApiError: class extends Error {
    code?: string
    constructor(m: string, _s?: number, c?: string) { super(m); this.code = c }
  },
}))

import { ticketDownload, DownloadError } from './tickets'

type ResShell = {
  ok: boolean
  status: number
  headers: { get: (k: string) => string | null }
  body?: { getReader: () => { read: () => Promise<{ done: boolean; value?: Uint8Array }> } } | null
  blob?: () => Promise<Blob>
  json?: () => Promise<unknown>
}

function mkRes(opts: {
  status?: number
  ctype?: string | null
  clen?: number
  chunks?: Uint8Array[] | null
  failAt?: number
  json?: unknown
}): ResShell {
  const status = opts.status ?? 200
  const h = new Map<string, string>()
  if (opts.ctype !== null && opts.ctype !== undefined) h.set('Content-Type', opts.ctype)
  if (opts.clen !== undefined) h.set('Content-Length', String(opts.clen))
  h.set('Content-Disposition', 'attachment; filename="out.xlsx"')
  const res: ResShell = {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (k: string) => h.get(k) ?? h.get(k.toLowerCase()) ?? null },
  }
  if (opts.json !== undefined) {
    res.json = async () => opts.json
  } else {
    res.json = async () => { throw new Error('not json') }
  }
  if (opts.chunks !== undefined) {
    const chunks = opts.chunks || []
    let i = 0
    res.body = {
      getReader: () => ({
        read: async () => {
          if (opts.failAt !== undefined && i === opts.failAt) throw new Error('network reset')
          if (i >= chunks.length) return { done: true }
          return { done: false, value: chunks[i++] }
        },
      }),
    }
  }
  res.blob = async () => new Blob([new Uint8Array([1, 2, 3])])
  return res
}

function installFetch(handler: (url: string, init?: RequestInit) => unknown) {
  const fn = vi.fn(async (url: string, init?: RequestInit) => handler(url, init))
  ;(globalThis as unknown as { fetch: unknown }).fetch = fn
  return fn
}

// jsdom 没有 URL.createObjectURL；桩掉它并让 <a>.click 成为空操作
const anchorClick = vi.fn()
const realCreateElement = document.createElement.bind(document)
beforeEach(() => {
  coreMocks.handleUnauthorized.mockClear()
  anchorClick.mockClear()
  ;(URL as unknown as { createObjectURL: (b: Blob) => string }).createObjectURL = () => 'blob:fake'
  ;(URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = () => undefined
  vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
    if (tag === 'a') return { href: '', download: '', click: anchorClick } as unknown as HTMLElement
    return realCreateElement(tag)
  }) as unknown as typeof document.createElement)
})
afterEach(() => vi.restoreAllMocks())

describe('ticketDownload · 慢/坏分开报 + 字节进度', () => {
  it('N1 fetch 被拒（断网/TLS/超时）⇒ kind=network（重试有用）', async () => {
    installFetch(() => { throw new Error('Failed to fetch') })
    await expect(ticketDownload(1)).rejects.toMatchObject({ kind: 'network' })
  })

  it('N2 5xx ⇒ kind=broken + status（重试没用）', async () => {
    installFetch(() => mkRes({ status: 500, ctype: 'application/json', json: { message: '服务端错误' } }))
    await expect(ticketDownload(1)).rejects.toMatchObject({ kind: 'broken', status: 500 })
  })

  it('N3 200 但回的是 SPA 兜底 HTML（§一·6）⇒ kind=broken（绝不把 HTML 壳存成文件）', async () => {
    installFetch(() => mkRes({ status: 200, ctype: 'text/html; charset=utf-8' }))
    await expect(ticketDownload(1)).rejects.toMatchObject({ kind: 'broken', status: 200 })
  })

  it('N4 收流中途断掉 ⇒ kind=network 且 received>0（"还在传，已收到 N 字节"）', async () => {
    const chunk = new Uint8Array(1024).fill(7)
    installFetch(() => mkRes({ status: 200, ctype: 'application/zip', clen: 4096, chunks: [chunk, chunk], failAt: 2 }))
    const err: any = await ticketDownload(1).catch((e) => e)
    expect(err).toBeInstanceOf(DownloadError)
    expect([err.kind, err.received > 0]).toEqual(['network', true])
  })

  it('N5 正常收完 ⇒ onProgress 递进、blob 落盘、触发一次下载', async () => {
    const chunk = new Uint8Array(2048).fill(1)
    installFetch(() => mkRes({ status: 200, ctype: 'application/zip', clen: 4096, chunks: [chunk, chunk] }))
    const seen: number[] = []
    await ticketDownload(1, { onProgress: (received) => seen.push(received) })
    expect(seen.length).toBe(2)
    expect(seen[seen.length - 1]).toBe(4096)
    expect(anchorClick).toHaveBeenCalledTimes(1)
  })

  it('N6 401 ⇒ 交给 handleUnauthorized（登录态语义与统一 client 同源）', async () => {
    installFetch(() => mkRes({ status: 401, ctype: 'application/json', json: { message: 'unauthorized' } }))
    await ticketDownload(1).catch(() => undefined)
    expect(coreMocks.handleUnauthorized).toHaveBeenCalledTimes(1)
  })
})
