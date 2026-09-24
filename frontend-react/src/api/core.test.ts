// ============================================================================
// core.test.ts — API 基础设施回归（★ F11：E4 headers 合并 / E5 401 处理 /
// 超时语义 / 统一错误码透传；另含 2026-09-14 multipart（FormData 不预设 Content-Type）回归）
// ★ 2026-09-22 §4.2-11 盲区补齐：追加非 JSON 错误体容错、非 2xx 无 message 回退、
//   handleForbidden 收口（未注册/注册两态）、bizErrorCode 双字段提取（code 优先）——
//   这几条是「改回旧写法就静默退化、却无人报警」的高价值行为，务必常驻。
// node 环境：先 stub sessionStorage/window，再动态 import core（模块顶层读 storage）。
// ============================================================================
import { beforeAll, describe, expect, it, vi } from 'vitest'

/** Core 动态导入类型（便于单测内 vi.resetModules 后重取） */
type Core = typeof import('./core')
let core: Core
let fakeWindow: { location: { href: string } }

beforeAll(async () => {
  const mem = new Map<string, string>()
  vi.stubGlobal('sessionStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, v) },
    removeItem: (k: string) => { mem.delete(k) },
  })
  fakeWindow = { location: { href: 'http://localhost/' } }
  vi.stubGlobal('window', fakeWindow)
  core = await import('./core')
})

/** jsonResponse 构造 fetch mock 的 Response（含 JSON 头） */
function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response
}

describe('api/core', () => {
  it('authHeaders：登录 token 与生效租户头', () => {
    core.setAuthToken('tk-test')
    core.setActiveTenantId(42)
    const h = core.authHeaders()
    expect(h.Authorization).toBe('Bearer tk-test')
    expect(h['X-Tenant-ID']).toBe('42')
  })

  it('E4：调用方自带 headers 不再吞掉 Content-Type/认证头（合并在展开之后）', async () => {
    let seen: Record<string, string> | undefined
    vi.stubGlobal('fetch', async (_u: string, init: RequestInit) => {
      seen = init.headers as Record<string, string>
      return jsonResponse({ success: true })
    })
    await core.request('/x', { method: 'POST', headers: { 'X-Custom': '1' }, body: '{}' })
    expect(seen!['X-Custom']).toBe('1')
    expect(seen!['Content-Type']).toBe('application/json')
    expect(seen!.Authorization).toBe('Bearer tk-test')
  })

  // 回归锁：与上一条 E4 互补——E4 保证「调用方自带 headers 时默认的 Content-Type 与
  // 认证头不被吞掉」，本条保证「body 为 FormData 时不得预设 Content-Type，
  // 必须留给浏览器生成 multipart boundary」（任何一侧回退都会红）。
  it('★ 2026-09-14 multipart 回归：FormData 请求不得预设 Content-Type（否则 boundary 丢失 → 后端 400「文件解析失败或超过大小上限」）', async () => {
    let seen: Record<string, string> | undefined
    vi.stubGlobal('fetch', async (_u: string, init: RequestInit) => {
      seen = init.headers as Record<string, string>
      return jsonResponse({ success: true })
    })
    const fd = new FormData()
    fd.append('files', new Blob(['hi'], { type: 'text/plain' }), 'a.txt')
    await core.request('/api/tickets/create-file', { method: 'POST', headers: core.authHeaders(), body: fd })
    expect(seen!['Content-Type'], 'FormData 请求必须交由浏览器自动生成 multipart boundary').toBeUndefined()
    expect(seen!.Authorization).toBe('Bearer tk-test') // 认证头不因此丢失
  })

  it('非 2xx：ApiError 携带 message 与稳定 code（含 error_code 别名）', async () => {
    vi.stubGlobal('fetch', async () => jsonResponse({ success: false, message: '余额不足', error_code: 'insufficient_balance' }, 402))
    await expect(core.request('/pay')).rejects.toMatchObject({
      name: 'ApiError', message: '余额不足', code: 'insufficient_balance', status: 402,
    })
  })

  it('★ P0-6：401 清 token 并触发复位钩子（原地落登录、不再整页跳首页丢回跳）', async () => {
    core.setAuthToken('will-expire')
    expect(core.getAuthToken()).toBe('will-expire')
    fakeWindow.location.href = 'http://localhost/'
    let hookCalls = 0
    core.setUnauthorizedHandler(() => { hookCalls++ })
    vi.stubGlobal('fetch', async () => jsonResponse({ success: false }, 401))
    await expect(core.request('/api/me')).rejects.toBeTruthy()
    expect(core.getAuthToken()).toBe('')
    expect(hookCalls).toBe(1)
    expect(fakeWindow.location.href, '不得再整页跳转（回跳目标=当前路径，由 Root 守卫原地出登录页）').toBe('http://localhost/')
    // 二次 401：钩子幂等可重入（复位 user→null 无副作用），token 保持为空
    await expect(core.request('/api/me2')).rejects.toBeTruthy()
    expect(hookCalls).toBe(2)
    core.setUnauthorizedHandler(null) // 复位：后续用例不受钩子影响
  })

  it('E5：登录接口自身 401 不清 token、不触发复位钩子', async () => {
    core.setAuthToken('keep-me')
    let hookCalls = 0
    core.setUnauthorizedHandler(() => { hookCalls++ })
    fakeWindow.location.href = 'http://localhost/'
    vi.stubGlobal('fetch', async () => jsonResponse({ success: false, message: '密码错误' }, 401))
    await expect(core.request('/api/auth/login', { method: 'POST' })).rejects.toBeTruthy()
    expect(core.getAuthToken()).toBe('keep-me')
    expect(hookCalls).toBe(0)
    expect(fakeWindow.location.href).toBe('http://localhost/')
  })

  // 回归锁（★ §4.2-3 403 统一处理）：403 是与 401 语义不同的「已登录但越权」，
  //   必须钉三件事——① 抛 ApiError(status=403, code=FORBIDDEN) 供调用方分支；
  //   ② 不清登录态、不触发 401 复位钩子（用户仍在线，误清会把好用户踢下线）；
  //   ③ 对外文案收口到 handleForbidden：注册解析器时用其结果，未注册时回落后端 message。
  it('★ §4.2-3：403 抛 FORBIDDEN、不清登录态、文案走统一解析器（未注册时回落后端 message）', async () => {
    core.setAuthToken('still-valid')
    let unauthorizedCalls = 0
    core.setUnauthorizedHandler(() => { unauthorizedCalls++ })
    // 未注册解析器：对外文案回落后端 message
    vi.stubGlobal('fetch', async () => jsonResponse({ success: false, message: '仅超管可操作' }, 403))
    await expect(core.request('/api/admin/nuke')).rejects.toMatchObject({
      name: 'ApiError', message: '仅超管可操作', code: 'FORBIDDEN', status: 403,
    })
    expect(core.getAuthToken(), '403 不得清登录态').toBe('still-valid')
    expect(unauthorizedCalls, '403 不得触发 401 复位钩子').toBe(0)
    // 注册解析器：文案统一收口到本地化既有键（这里用假解析器模拟 ToastBridge 注入）
    core.setForbiddenCopyResolver(() => '无权限访问')
    await expect(core.request('/api/admin/nuke')).rejects.toMatchObject({
      message: '无权限访问', code: 'FORBIDDEN', status: 403,
    })
    core.setForbiddenCopyResolver(null) // 复位：后续用例不受解析器影响
    core.setUnauthorizedHandler(null)
  })

  // 回归锁（★ 2026-09-24 AI 注册发码 403 事故）：/api/auth/* 是匿名链路，其 403 的
  //   真实原因是人机验证/注册关闭，若也被解析器改写成「当前账号无管理权限」，
  //   未登录用户会被指引去登一个不存在的管理员账号——发码点了没反应还查不到方向。
  //   故该前缀必须直传后端 message；豁免只对 auth 前缀生效，admin 路由解析器优先不变。
  it('★ 2026-09-24：/api/auth/* 的 403 直传后端 message（即便已注册解析器），非 auth 路由仍解析器优先', async () => {
    core.setForbiddenCopyResolver(() => '当前账号无管理权限，请使用管理员账号登录。')
    vi.stubGlobal('fetch', async () => jsonResponse({ success: false, message: '请完成人机验证' }, 403))
    await expect(core.request('/api/auth/email-code', { method: 'POST' })).rejects.toMatchObject({
      name: 'ApiError', message: '请完成人机验证', code: 'FORBIDDEN', status: 403,
    })
    // 反向对照：同一次 stub 下 admin 路由依旧收口到解析器，豁免不得扩散
    await expect(core.request('/api/admin/users')).rejects.toMatchObject({
      message: '当前账号无管理权限，请使用管理员账号登录。', code: 'FORBIDDEN', status: 403,
    })
    core.setForbiddenCopyResolver(null)
  })

  it('超时：timeoutMs 内未决 → 明确中文错误（区别于外部 abort）', async () => {
    vi.stubGlobal('fetch', (_u: string, init: RequestInit) => new Promise((_res, rej) => {
      init.signal!.addEventListener('abort', () => rej(new DOMException('aborted', 'AbortError')))
    }))
    await expect(core.request('/slow', { timeoutMs: 20 })).rejects.toThrow(/超时/)
  })

  // 回归锁（§4.2-11 盲区补齐）：非 JSON 错误体容错——后端偶发返回 HTML 错误页/纯文本
  //   （网关 502、栈溢出页等）时，response.json() 会抛，若不做兜底 request() 会把「解析失败」
  //   当成未知异常冒泡，调用方拿不到可读 message。改坏表现：非 JSON 响应直接抛 SyntaxError。
  it('非 JSON 错误体：回落「请求失败 (状态码): 原文」而非抛出 JSON 解析异常', async () => {
    vi.stubGlobal('fetch', async () => ({
      ok: false, status: 502,
      json: async () => { throw new SyntaxError('Unexpected token <') },
      text: async () => '<html>Bad Gateway</html>',
    } as unknown as Response))
    await expect(core.request('/api/x')).rejects.toMatchObject({
      name: 'ApiError', status: 502,
      // 回退链：json 失败 → text 原文拼进 message，且不得吞成 SyntaxError
      message: '请求失败 (502): <html>Bad Gateway</html>',
    })
  })

  // 非 2xx 但错误体是合法 JSON 且 message/error 皆缺：仍走 text 回退，不产生空 message。
  it('错误体 JSON 无 message/error：回落到「请求失败 (码): 原文」不出现空文案', async () => {
    vi.stubGlobal('fetch', async () => ({
      ok: false, status: 400,
      json: async () => ({ success: false }), // 无 message/error/code
      text: async () => '{"success":false}',
    } as unknown as Response))
    await expect(core.request('/api/bad')).rejects.toMatchObject({
      name: 'ApiError', status: 400, message: '请求失败 (400): {"success":false}',
    })
  })

  // handleForbidden 是 403 对外文案的唯一收口：未注册解析器且后端无 message 时给固定兜底串，
  //   注册后一律用解析器结果（忽略后端原文）。改坏表现：越权提示随各处后端文案漂移或出现空串。
  it('handleForbidden：未注册回落固定兜底串、注册后统一取解析器结果', () => {
    core.setForbiddenCopyResolver(null)
    expect(core.handleForbidden('')).toBe('无权限访问该资源')
    expect(core.handleForbidden('后端越权原文')).toBe('后端越权原文') // 未注册时用后端 message
    core.setForbiddenCopyResolver((msg) => `本地化[${msg}]`)
    expect(core.handleForbidden('后端越权原文'), '注册后一律走解析器（本地化既有键口径）').toBe('本地化[后端越权原文]')
    core.setForbiddenCopyResolver(null)
  })

  // ★ 2026-09-24 〇-R：apiMsg 是 api 层「本地化错误文案」的唯一注入口（用户诉求：
  //   英文后台不得出现中文提示）。未注册回落中文原句（node 单测/SSR 口径），
  //   注册后按键取词（ToastBridge 注入 i18n.tpl 同款链路）。
  it('apiMsg：未注册回落中文兜底句、注册后按当前语言取词', () => {
    expect(core.apiMsg('common.timeout', '请求超时，请检查后端服务是否正常')).toBe('请求超时，请检查后端服务是否正常')
    core.setApiMsgCopier((key, fallback, vars) => {
      void fallback
      return vars ? `EN[${key}:${JSON.stringify(vars)}]` : `EN[${key}]`
    })
    expect(core.apiMsg('common.netFail', '无法连接后端服务')).toBe('EN[common.netFail]')
    expect(core.apiMsg('common.reqFail', '请求失败 (500)', { status: 500 })).toBe('EN[common.reqFail:{"status":500}]')
    core.setApiMsgCopier(null)
    expect(core.apiMsg('common.netFail', '无法连接后端服务'), '复位后回到兜底句').toBe('无法连接后端服务')
  })

  // bizErrorCode：业务错误常以 HTTP 200 + success:false 下发，不抛异常，靠此从响应体取稳定码。
  //   兼容 code / error_code 双字段且 code 优先。改坏表现：充值引导/限额提示因取不到码而失效。
  it('bizErrorCode：优先 code、回落 error_code、非对象安全返回 undefined', () => {
    expect(core.bizErrorCode({ code: 'INSUFFICIENT_BALANCE' })).toBe('INSUFFICIENT_BALANCE')
    expect(core.bizErrorCode({ error_code: 'daily_quota_exceeded' })).toBe('daily_quota_exceeded')
    expect(core.bizErrorCode({ code: 'a', error_code: 'b' }), 'code 优先于 error_code').toBe('a')
    expect(core.bizErrorCode(null)).toBeUndefined()
    expect(core.bizErrorCode('字符串')).toBeUndefined()
  })
})
