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

  // ★ 2026-09-24 〇-S #12 后端语言识别：X-App-Lang 直读 localStorage.app_lang（不 import i18n），
  // 无语种时不带头——后端按 Accept-Language→默认中文降级，存量请求零变化。
  it('authHeaders：X-App-Lang 随 app_lang 附带，无值不带头', () => {
    const lmem = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (k: string) => lmem.get(k) ?? null,
      setItem: (k: string, v: string) => { lmem.set(k, v) },
      removeItem: (k: string) => { lmem.delete(k) },
    })
    expect(core.authHeaders()['X-App-Lang']).toBeUndefined()
    lmem.set('app_lang', 'en')
    expect(core.authHeaders()['X-App-Lang']).toBe('en')
    lmem.set('app_lang', 'ja')
    expect(core.currentUiLang()).toBe('ja')
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

  // 非 2xx 但错误体是合法 JSON 且 message/error 皆缺：走状态码兜底，
  // ★ F-52（〇-U 批 I-8）改了这里的期望值——旧契约是「把整段 JSON 原文拼进 message」，
  //   于是聊天/提示条里会出现 `请求失败 (400): {"success":false,...,"trace_id":"..."}`。
  //   新契约：JSON 对象体**一律不进正文**（display 置空），只留状态码那句人话；
  //   原对象仍在 ApiError.body 里供程序化消费（bizResp 收敛、trace_id 复制排查）。
  //   ⚠ 这条不是「断言写错了」，是契约翻新：改回带原文就是 F-52 回归。
  it('★ F-52：错误体 JSON 无 message/error → 兜底文案只留状态码，整段 JSON 不进正文', async () => {
    vi.stubGlobal('fetch', async () => ({
      ok: false, status: 400,
      json: async () => ({ success: false }), // 无 message/error/code
      text: async () => '{"success":false}',
    } as unknown as Response))
    const e = await core.request('/api/bad').catch((x: unknown) => x) as import('./core').ApiError
    expect(e.name).toBe('ApiError')
    expect(e.status).toBe(400)
    expect(e.message).toBe('请求失败 (400)')
    expect(e.message).not.toContain('{') // 大括号一进正文就是本缺陷复活
  })

  // ★ F-52 本体锁：后端结构化错误体（{success,code,message,details,trace_id}）必须
  //   只把 message 交出去，code 透传成稳定码、整个对象挂到 body；**trace_id 不得进正文**
  //   （它只在 ApiError.body 里，供「复制排查信息」用，见 core.ts errTraceId）。
  it('★ F-52：结构化 4xx 错误体 → message 取后端那句、code 取稳定码、整包不进气泡', async () => {
    const envBody = {
      success: false, code: 'chat_text_too_long',
      message: '文本过长（5,001 字符，单次对话上限 5,000 字符），长文本请创建翻译工单处理',
      details: { count: 5001 }, trace_id: 'tr-abc123',
    }
    vi.stubGlobal('fetch', async () => jsonResponse(envBody, 400))
    const e = await core.request('/api/chat/stream').catch((x: unknown) => x) as import('./core').ApiError
    expect(e).toBeInstanceOf(core.ApiError)
    expect(e.status).toBe(400)
    expect(e.code).toBe('chat_text_too_long')
    expect(e.message).toBe(envBody.message)
    expect(e.message).not.toContain('{')
    expect(e.message).not.toContain('trace_id')
    expect(e.message).not.toContain('tr-abc123')
    expect(e.body).toMatchObject({ trace_id: 'tr-abc123', details: { count: 5001 } })
    expect(core.errTraceId(e)).toBe('tr-abc123') // 排查信息走这里，不进正文
  })

  // ★ 顺带修掉的「读侧自伤」：旧 request() 先 response.json() 失败、再 response.text()——
  //   真实 Response 的 body 只能消费一次，第二次直接抛，被 .catch(()=>'') 吞成空串，
  //   于是非 JSON 错误体的兜底文案恒为「请求失败 (502): 」（后面是空的）。
  //   本用例用**真 Response**（不是手搓的 json()/text() 双桩，那桩永远不会暴露这个 bug）
  //   断言原文确实拼进了兜底文案。
  it('★ F-52 附带：真 Response 只能读一次——非 JSON 体的兜底文案必须带上原文（旧实现恒为空）', async () => {
    vi.stubGlobal('fetch', async () => new Response('Gateway Bleed-through 原文', { status: 502 }))
    const e = await core.request('/api/x').catch((x: unknown) => x) as import('./core').ApiError
    expect(e.status).toBe(502)
    expect(e.message).toBe('请求失败 (502): Gateway Bleed-through 原文')
  })

  // parseErrEnvelope 纯函数锁（三档规则见 core.ts 注释）：坏 JSON / 数组 / 空体都不能崩，
  //   且 JSON 对象档的 display 恒为空——这条是「把整段体送进界面」的机制闸。
  describe('parseErrEnvelope（★ F-52 错误体统一解析）', () => {
    it('JSON 对象：取 message/error、code/error_code 双别名、display 必为空', () => {
      expect(core.parseErrEnvelope('{"message":"甲","code":"A"}')).toMatchObject({ message: '甲', code: 'A', display: '' })
      expect(core.parseErrEnvelope('{"error":"乙","error_code":"B"}')).toMatchObject({ message: '乙', code: 'B', display: '' })
      // message 不是字符串（脏数据）时不得把对象/数字 stringify 成文案
      expect(core.parseErrEnvelope('{"message":{"nested":1}}')).toMatchObject({ message: '', display: '' })
    })
    it('HTML 网关页：原文保留（F-29 的 isHtmlErrorBody 判据依赖它），并截断到 200 字', () => {
      const html = '<!DOCTYPE html><html>' + 'x'.repeat(400) + '</html>'
      const r = core.parseErrEnvelope(html)
      expect(r.message).toBe('')
      expect(r.display).toContain('<!DOCTYPE html>')
      expect((r.display as string).length).toBe(200)
    })
    it('坏 JSON / 数组 / 纯文本 / 空体：都不抛，按原文截断兜底', () => {
      expect(core.parseErrEnvelope('{"success":fal').display).toContain('{"success":fal')
      expect(core.parseErrEnvelope('[1,2]').display).toBe('[1,2]')
      expect(core.parseErrEnvelope('plain text').display).toBe('plain text')
      expect(core.parseErrEnvelope('')).toEqual({ message: '', display: '' })
      expect(core.parseErrEnvelope(undefined as unknown as string)).toEqual({ message: '', display: '' })
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
