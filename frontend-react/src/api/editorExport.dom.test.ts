// ============================================================================
// api/editorExport.dom.test.ts — ★ D-4 回写导出下载链回归（2026-09-29）
// 被测：segmentsExport() ＋ editorExportFetch()。
// 为什么要单独锁：后端 `/api/tickets/segments/export` 与 `/api/editor/export/download`
//   自 2026-08 就存在，但界面零入口（全量审计 D-4）——补上钮之后，这条链有三个最易回归的点：
//   ① 下载必须带鉴权头（该路由先 authUser 再做 B8 产物归属校验，裸 <a href>／window.open 必然 401）；
//   ② **必须真验字节**：AGENTS §一·6 的兜底陷阱——spa.go 对未知路径回 200 + 整页 index.html，
//      只判 r.ok 就会把 HTML 壳存成 .docx 交给客户（状态码绿、内容说谎）；
//   ③ 失败要抛后端那句 message（「该工单不支持在线回写（需 docx 结果文件）」一类），
//      不能静默产出一个空文件让用户以为导出成功。
// 环境：jsdom（要 document.createElement／anchor.click 承接下载动作；createObjectURL 由本文件打桩）。
// 运行：npx vitest run src/api/editorExport.dom.test.ts
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// 不 mock ./core：segmentsExport 的 bizResp 收敛、editorExportFetch 的 401/403 语义
// 都是 core 的真实行为，替身会把「接线是否正确」这一层整个让过去——只把网络换成桩。
import { segmentsExport, editorExportFetch } from './tickets'
import { setAuthToken, getAuthToken } from './core'

/** 造一段合法 docx 外观的字节（PK 头 ＋ 补到 800B 以上，满足实现里的体积下限） */
function docxBytes(body = 'x'): Uint8Array {
  const raw = new TextEncoder().encode('PK' + body)
  const buf = new Uint8Array(Math.max(raw.length, 1200))
  buf.set(raw)
  return buf
}

interface Stub { status?: number; bytes?: Uint8Array; disposition?: string; json?: unknown }

/** fetch 桩：只实现被测代码真正消费到的成员（status/ok/headers/arrayBuffer/json） */
function makeResp(s: Stub) {
  // headers 用最小对象而不是 new Headers()：真实后端回写产物的文件名常含中文
  //（strconv.Quote(filepath.Base) 直接拼头），jsdom 的 Headers 只收 Latin-1 会先替我们拒掉，
  // 而被测代码只消费 get('Content-Disposition')，这里按同一契约打桩才测得到中文名那条形态。
  const headers = { get: (k: string) => (k === 'Content-Disposition' ? s.disposition ?? '' : null) }
  const bytes = s.bytes ?? new Uint8Array(0)
  const status = s.status ?? 200
  return {
    ok: status >= 200 && status < 300,
    status,
    headers,
    arrayBuffer: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
    json: async () => (s.json ?? {}) as never,
  }
}

const clicked: { download: string; href: string }[] = []
let responder: (url: string, init?: RequestInit) => Promise<unknown> = async () => makeResp({})
const fetchMock = vi.fn((url: string, init?: RequestInit) => responder(url, init))

/** 下一个请求按 s 应答，返回可断言调用参数的 fetch mock */
function respondWith(s: Stub | ((url: string, init?: RequestInit) => unknown)) {
  responder = typeof s === 'function'
    ? async (url, init) => s(url, init) as never
    : async () => makeResp(s)
  return fetchMock
}

beforeEach(() => {
  clicked.length = 0
  fetchMock.mockClear()
  responder = async () => makeResp({})
  vi.stubGlobal('fetch', fetchMock)
  // jsdom 没有 createObjectURL/revokeObjectURL：给假 URL，下载动作由 anchor.click 承接（同 kb.tmx 口径）
  Object.defineProperty(URL, 'createObjectURL', { value: () => 'blob:fake', configurable: true })
  Object.defineProperty(URL, 'revokeObjectURL', { value: () => { /* noop */ }, configurable: true })
  vi.spyOn(window.HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    clicked.push({ download: this.download, href: this.href })
  })
  setAuthToken('test-token')
})

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); setAuthToken('') })

describe('segmentsExport 触发回写（★ D-4 界面入口的另一半）', () => {
  it('① 数字工单 ID 与语种都进查询串，且带鉴权头、走 POST', async () => {
    const m = respondWith({ json: { success: true, download: '/api/editor/export/download?file=a_edited_1.docx' } })
    const r = await segmentsExport(42, 'zh-hant')
    expect(String(m.mock.calls[0][0])).toBe('/api/tickets/segments/export?id=42&lang=zh-hant')
    const init = m.mock.calls[0][1] as RequestInit
    expect(init.method).toBe('POST')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer test-token')
    expect(r.download).toBe('/api/editor/export/download?file=a_edited_1.docx')
  })

  it('② 业务失败经 bizResp 还原成 {success:false} 信封（AGENTS §一·5：调用方按 success 分支出文案）', async () => {
    // 后端 F-64② 之后这类失败是结构化 4xx，不是 200 壳。
    // core.readErrEnvelope 先 text() 再解析，桩体必须按 Response 的真实消费顺序给（只给 json() 会假红）。
    const body = JSON.stringify({ success: false, message: '回写仅支持 docx 结果文件' })
    responder = async () => ({
      ok: false, status: 400, headers: { get: () => '' },
      text: async () => body, json: async () => JSON.parse(body),
    }) as never
    const r = await segmentsExport(7, 'en')
    expect(r.success).toBe(false)
    expect(r.message).toBe('回写仅支持 docx 结果文件')
  })
})

describe('editorExportFetch 取回落写稿（★ D-4 下载面）', () => {
  it('③ 正常 docx：按 Content-Disposition 命名并触发一次下载', async () => {
    const m = respondWith({ bytes: docxBytes(), disposition: 'attachment; filename="合同_en_edited_5.docx"' })
    const name = await editorExportFetch('/api/editor/export/download?file=%2Fa.docx')
    expect(String(m.mock.calls[0][0])).toBe('/api/editor/export/download?file=%2Fa.docx')
    expect((m.mock.calls[0][1] as RequestInit).headers).toMatchObject({ Authorization: 'Bearer test-token' })
    expect(name).toBe('合同_en_edited_5.docx')
    expect(clicked).toHaveLength(1)
    expect(clicked[0].download).toBe('合同_en_edited_5.docx')
  })

  it('④ 反证（AGENTS §一·6 的兜底陷阱）：200 ＋ SPA 的 HTML 壳必须抛错且零下载', async () => {
    // 这就是「文件不存在 → spa.go 回 200 整页 index.html」的形态：状态码绿、内容根本不是 docx。
    // 旧写法只判 r.ok 时，这里会安静地存下一个 .docx 的空壳——比报错更贵。
    const shell = new TextEncoder().encode('<!DOCTYPE html>\n<html><head><title>LangCross</title></head><body>' + 'a'.repeat(1500) + '</body></html>')
    respondWith({ bytes: shell, disposition: 'attachment; filename="edited.docx"' })
    await expect(editorExportFetch('/api/editor/export/download?file=nope.docx')).rejects.toThrow(/docx/)
    expect(clicked).toHaveLength(0)
  })

  it('⑤ 反证：PK 头对但体积不足（截断产物）同样判失败', async () => {
    const tiny = new Uint8Array(300)
    tiny[0] = 0x50; tiny[1] = 0x4b // 'PK'
    respondWith({ bytes: tiny })
    await expect(editorExportFetch('/api/editor/export/download?file=x.docx')).rejects.toThrow(/docx/)
    expect(clicked).toHaveLength(0)
  })

  it('⑥ 403 越权：抛带后端文案的 FORBIDDEN，不产出文件', async () => {
    responder = async () => ({
      ok: false, status: 403, headers: new Headers(), json: async () => ({ success: false, message: '无权下载该产物' }),
    }) as never
    // 用 try/catch 收错误对象而不是 .catch(x => x as Error)：后者把 Promise<string> 的成功支
    // 也并进联合类型，tsc 会在 e.message 上报「Property 'message' does not exist on type 'string'」
    let e: unknown
    try {
      await editorExportFetch('/api/editor/export/download?file=y.docx')
    } catch (x) {
      e = x
    }
    expect(e).toBeInstanceOf(Error)
    expect((e as Error & { code?: string }).code).toBe('FORBIDDEN')
    expect((e as Error).message).toBe('无权下载该产物')
    expect(clicked).toHaveLength(0)
  })

  it('⑦ 401 走统一登录失效链路（清 token），与其余接口同口径', async () => {
    responder = async () => ({ ok: false, status: 401, headers: new Headers(), json: async () => ({}) }) as never
    await expect(editorExportFetch('/api/editor/export/download?file=z.docx')).rejects.toThrow()
    expect(getAuthToken()).toBe('') // 真清登录态＝handleUnauthorized 确实被调用过
    expect(clicked).toHaveLength(0)
  })

  it('⑧ 缺 Content-Disposition 时回落 edited.docx（不渲染空文件名）', async () => {
    respondWith({ bytes: docxBytes() })
    const name = await editorExportFetch('/api/editor/export/download?file=w.docx')
    expect(name).toBe('edited.docx')
    expect(clicked[0].download).toBe('edited.docx')
  })
})
