// ============================================================================
// hooks/useChat.dom.test.tsx — 聊天 SSE 编排核心行为回归（★ §4.2-11 盲区补齐）
// useChat（356 行）此前零测。它是即时翻译流式链路的中枢：发送占位、progress/delta 双态、
//   按帧合批写 draft、done/停止/错误三条收尾、卸载竞态。任一处静默退化都不会报错，只表现为
//   「译文看不见 / 草稿不刷新 / 停不下来 / 完成后进度条还在转」，是最该有断言兜底的高价值代码。
// 本测用可控的假 chatStream（捕获 onProgress/onDelta 回调 + 外部 resolve/reject）+ 手动 rAF 队列，
//   把「逐 token → 合帧 → 写 store」变成可确定驱动的同步流程。
// 口径说明：① 段级事件（segment_*）的「行对号」在 api/translate.consumeSSEStream 解析层，
//     已由 translate.segments.test.ts 覆盖；#36 下线文件流后 useChat.sendMessage 只透传 progress/delta，
//     不再消费 segments，故此处不重复段级断言（见汇报盲区）。
//   ② done 分支旧实现整包 `{...res}` 并入 ChatResponse（字段名 reply），展示层读 message.content
//     ⇒ 纯文本问答气泡空白。2026-09-22 已修（useChat 显式 reply→content），本测钉住该映射，
//     并锁「降级路径（无 delta）done 后必须有可见正文」。
// 运行：npx vitest run src/hooks/useChat.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import type { ReactNode } from 'react'
import { renderHook, act, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { setLang, t } from '@/i18n'
import { ApiError } from '@/api'
import { ChatProvider, useChat } from '@/hooks/useChat'
import { setChatMaxChars } from '@/lib/chatLimit' // ★ F-52② 本地长度闸（上限由服务端喂养）
import type { ChatMessage } from '@/types'

// chatStream/healthCheck 换成可控桩：保留真实 ApiError（instanceof 分流依赖它）
const h = vi.hoisted(() => ({
  chatStream: vi.fn(),
  healthCheck: vi.fn(async () => ({ status: 'ok' })),
}))
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return { ...actual, chatStream: h.chatStream, healthCheck: h.healthCheck }
})
// 错误分支会弹充值确认框（会 navigate），桩掉只留可断言的调用记录
const confirmDialog = vi.hoisted(() => vi.fn(async () => false))
vi.mock('@/components/uiDialogs', () => ({ confirmDialog }))

import { useAdminStore } from '@/stores/admin'
import { useAuthStore } from '@/stores/auth'

/** 每个用例捕获到的流回调（onProgress/onDelta），供测试手动喂帧 */
let cap: { onProgress?: (e: unknown) => void; onDelta?: (lang: string, text: string) => void } = {}
/** 手动 rAF 队列：把「合帧」变成可确定触发的同步步骤 */
let rafQueue: Array<() => void> = []

function Harness({ children }: { children: ReactNode }) {
  return <MemoryRouter><ChatProvider>{children}</ChatProvider></MemoryRouter>
}

/** 起一次发送：chatStream 被同步调用即捕获回调，返回外部 resolve/reject 控制收尾
 *  （控制器对象延迟填充——resolve/reject 在 Promise 构造器内才拿到，故返回引用而非解构值）*/
function armStream() {
  const ctl = { resolve: null as unknown as (r: unknown) => void, reject: null as unknown as (e: unknown) => void }
  h.chatStream.mockImplementation((_m: string, _s: string, _o: unknown, onProgress: (e: unknown) => void, _sig: unknown, onDelta: (l: string, t2: string) => void) => {
    cap = { onProgress, onDelta }
    return new Promise((res, rej) => { ctl.resolve = res; ctl.reject = rej })
  })
  return ctl
}

const assistant = (msgs: ChatMessage[]) => [...msgs].reverse().find((m) => m.role === 'assistant')!

/** 冲刷在途的合帧回调（等价于浏览器走了一帧） */
function flushFrames() {
  act(() => {
    const cbs = rafQueue.splice(0, rafQueue.length)
    cbs.forEach((cb) => cb())
  })
}

beforeEach(() => {
  cleanup()
  setLang('zh')
  cap = {}
  rafQueue = []
  h.chatStream.mockReset()
  h.healthCheck.mockClear()
  setChatMaxChars(0) // ★ F-52②：本地长度闸默认关闭，逐用例自行喂上限（防跨用例残留把后面的句子拦了）
  confirmDialog.mockClear()
  useAuthStore.setState({ user: null, restoring: false })
  useAdminStore.setState({})
  // 接管 rAF：jsdom 自带的会异步跑，这里换成手动队列以精确控制合帧时机
  ;(window as unknown as { requestAnimationFrame: (cb: () => void) => number }).requestAnimationFrame = (cb) => {
    rafQueue.push(cb)
    return rafQueue.length
  }
})

function mountChat() {
  const { result } = renderHook(() => useChat(), { wrapper: Harness })
  return result
}

describe('useChat · 发送与占位', () => {
  it('发送即追加用户+助手占位（progress=准备中、content 空）并置 isLoading', async () => {
    armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('你好', { target_langs: ['en'] }) })
    expect(res.current.messages).toHaveLength(2)
    expect(res.current.messages[0]).toMatchObject({ role: 'user', content: '你好' })
    expect(assistant(res.current.messages).progress).toMatchObject({ step: t('chat.preparing'), percent: 0 })
    expect(res.current.isLoading, '流式进行中必须 isLoading=true（否则停止/并发保护失效）').toBe(true)
  })

  it('空文本与并发二次发送都被拦下（不产生多余气泡）', async () => {
    armStream()
    const res = mountChat()
    await act(async () => { await res.current.sendMessage('   ') })
    expect(res.current.messages).toHaveLength(0)
    await act(async () => { void res.current.sendMessage('第一条') })
    // isLoading 期间再次发送被 s0.isLoading 早退
    await act(async () => { void res.current.sendMessage('第二条') })
    expect(res.current.messages.filter((m) => m.role === 'user')).toHaveLength(1)
  })
})

describe('useChat · progress/delta 双态与合帧', () => {
  it('progress 帧直接回填助手气泡（不经 rAF），step/percent 落定', async () => {
    armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    act(() => { cap.onProgress?.({ type: 'progress', step: '机器翻译', percent: 40 }) })
    expect(assistant(res.current.messages).progress).toEqual({ step: '机器翻译', percent: 40 })
  })

  it('同帧多条 delta 合批成一次 draft 写入，且经 <t> 契约清洗、与 progress 共存', async () => {
    armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    act(() => { cap.onProgress?.({ type: 'progress', step: '机器翻译', percent: 50 }) })
    // 两次增量同帧到达：仅累积，尚未冲刷 → draft 应还不存在（合帧证明）
    act(() => { cap.onDelta?.('en', '<t>Hel'); cap.onDelta?.('en', 'lo</t>') })
    expect(assistant(res.current.messages).draft, '未冲刷前不得半帧上屏').toBeUndefined()
    flushFrames()
    const a = assistant(res.current.messages)
    // 半截标签清洗后只剩译文本体（口径锚点 lib/draftClean / postprocess.go）
    expect(a.draft).toEqual({ en: 'Hello' })
    // 双态共存：有草稿的同时进度不丢（改回旧互斥就会只剩量尺）
    expect(a.progress).toMatchObject({ step: '机器翻译', percent: 50 })
  })

  it('多语言 delta 各自累积成独立草稿行', async () => {
    armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    act(() => { cap.onDelta?.('en', '<t>A</t>'); cap.onDelta?.('de', '<t>B</t>') })
    flushFrames()
    expect(assistant(res.current.messages).draft).toEqual({ en: 'A', de: 'B' })
  })
})

describe('useChat · done / 停止 / 错误三条收尾', () => {
  it('done：清空 progress 与 draft 并并入结果字段（译文表/附件/消耗）', async () => {
    const ctl = armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    act(() => { cap.onDelta?.('en', '<t>Hi</t>') })
    flushFrames()
    await act(async () => {
      ctl.resolve({ skill: 'translation', reply: 'Hi', data: { translations: { en: 'Hello' } }, files: ['/out/a.docx'], points_used: 3 })
    })
    const a = assistant(res.current.messages)
    expect(a.progress, 'done 后进度态必须收尾，否则气泡永卡在量尺').toBeUndefined()
    expect(a.draft, 'done 后草稿必须清空，定稿态盖过初译').toBeUndefined()
    expect(a.data).toMatchObject({ translations: { en: 'Hello' } })
    expect(a.files).toEqual(['/out/a.docx'])
    expect(a.points_used).toBe(3)
    expect(res.current.isLoading).toBe(false)
  })

  it('done：reply→content 必须映射（降级路径无 delta 时气泡不能空白）', async () => {
    const ctl = armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('支持哪些语言') })
    // 浑元/熔断降级：整个过程一帧 delta 都没有，正文只能来自 done 的 ChatResponse.reply
    await act(async () => { ctl.resolve({ skill: 'assistant', reply: '支持 40+ 语种互译。', data: {} }) })
    const a = assistant(res.current.messages)
    expect(a.content, 'ChatResponse.reply 未映射进 content ⇒ 展示层读 content，气泡一片空白').toBe('支持 40+ 语种互译。')
    expect((a as unknown as Record<string, unknown>).reply, '不该再把后端 reply 字段混进消息').toBeUndefined()
    expect(a.skill).toBe('assistant')
  })

  it('done 落定后迟到的 delta 不再回灌 draft（streamClosed 竞态护栏）', async () => {
    const ctl = armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    await act(async () => { ctl.resolve({ skill: 'translation', reply: 'ok', data: {} }) })
    // done 之后再喂增量（真实网络里 done 与末帧 delta 可能乱序）
    act(() => { cap.onDelta?.('en', '<t>late</t>') })
    flushFrames()
    expect(assistant(res.current.messages).draft, '收尾后在途帧不得复活草稿').toBeUndefined()
  })

  it('stopGeneration：未出结果的气泡落「生成已停止」并清 progress/draft', async () => {
    armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    act(() => { cap.onProgress?.({ type: 'progress', step: '机器翻译', percent: 30 }); cap.onDelta?.('en', '<t>半句') })
    flushFrames()
    act(() => { res.current.stopGeneration() })
    const a = assistant(res.current.messages)
    expect(a.content).toBe(t('chat.stopped'))
    expect(a.progress).toBeUndefined()
    expect(a.draft, '停在一半的初译不是交付物，必须清掉').toBeUndefined()
    expect(res.current.isLoading).toBe(false)
  })

  it('余额不足（INSUFFICIENT_BALANCE）：气泡给充值引导文案并弹确认框（大写码归一化）', async () => {
    const ctl = armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    await act(async () => { ctl.reject(new ApiError('余额不足', undefined, 'INSUFFICIENT_BALANCE')) })
    expect(assistant(res.current.messages).content).toBe(t('chat.quotaExhausted'))
    expect(confirmDialog, '余额不足需触发充值引导确认框').toHaveBeenCalledTimes(1)
    expect(res.current.isLoading).toBe(false)
  })

  it('通用错误：errorMessage 透出、气泡落错误文案、清 draft/progress', async () => {
    const ctl = armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    await act(async () => { ctl.reject(new Error('网络中断')) })
    expect(assistant(res.current.messages).content).toContain('网络中断')
    expect(assistant(res.current.messages).draft).toBeUndefined()
    expect(res.current.errorMessage).toBe('网络中断')
    expect(res.current.isLoading).toBe(false)
  })

  // ★ F-29 前端半（批G 2026-09-25）：网关 ~90s 超时回吐的 HTML 错误页（非 SSE 整页）
  // 经 chatStream 的 !response.ok 分支拼进 Error message——旧实现原样进气泡，
  // 用户看到一屏 `<!DOCTYPE html>…Ray ID…` 小作文。现在整条替换为 chat.timeoutTicket。
  // 等值锁（气泡 === 超时文案）+ 负向锁（不含响应体原文标记）。
  it('F-29：网关 HTML 错误页整页进气泡被拦截，替换为超时文案（等值锁 + 负向锁）', async () => {
    const ctl = armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    const gatewayHtml = '<!DOCTYPE html><html><head><title>Error 524</title></head>'
      + '<body><span>Ray ID: 8a1b2c3d4e5f6789</span><span>Cloudflare</span></body></html>'
    await act(async () => { ctl.reject(new Error(`请求失败 (524): ${gatewayHtml}`)) })
    const a = assistant(res.current.messages)
    // 等值锁：气泡精确等于超时文案（vitest 已 setLang('zh')，t() 取 zh 词典值）
    expect(a.content).toBe(t('chat.timeoutTicket'))
    // 负向锁：响应体原文的任何标记都不得出现在气泡里
    expect(a.content).not.toContain('Ray ID')
    expect(a.content).not.toContain('<!DOCTYPE')
    expect(a.content).not.toContain('Cloudflare')
    // errorMessage 同步换友好文案，禁止原始 HTML 从顶部提示条二次泄漏
    expect(res.current.errorMessage).toBe(t('chat.timeoutTicket'))
    expect(a.progress).toBeUndefined()
    expect(a.draft).toBeUndefined()
  })
})

// ============================================================================
// ★ 2026-09-26 〇-U 批 I-8：F-52 / F-53 / F-52② 三条气泡级回归锁
// 共同形状：喂给聊天的错误**必须**是 api/core 的统一错误对象（ApiError 带 message/code/body），
// 断言落在「用户实际看到的那口气泡」上，而不是抛出的异常上——本轮三条缺陷全都是
// 异常侧正确、展示侧失真（整段 JSON 进正文 / 裸键名进正文 / 长文本白跑一次请求）。
// ============================================================================
describe('批 I-8 · 错误气泡内容诚实（F-52 / F-53）', () => {
  it('F-52：4xx 结构化错误体进气泡的只有后端 message，整包 JSON 与 trace_id 一律不外露', async () => {
    const ctl = armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    // 这正是 api/core.parseErrEnvelope 对 apierrors.WriteError 出参的解析结果形态
    await act(async () => {
      ctl.reject(new ApiError('目标语言不支持，请重新选择', 400, 'unsupported_target_lang', {
        success: false,
        code: 'unsupported_target_lang',
        message: '目标语言不支持，请重新选择',
        details: { lang: 'zzz', supported: ['en', 'ja'] },
        trace_id: 'tr-9f3c01',
      }))
    })
    const a = assistant(res.current.messages)
    // 等值锁：气泡正文 = 通用兜底前缀 + 后端那句 message（旧形态是整段 JSON 拼进来）
    expect(a.content.trim()).toBe('目标语言不支持，请重新选择')
    expect(res.current.errorMessage).toBe('目标语言不支持，请重新选择')
    // ★ 负向锁（本缺陷的原形）：JSON 结构字符与排查字段不得出现在用户可见文案里
    for (const text of [a.content, res.current.errorMessage]) {
      expect(text).not.toContain('{')
      expect(text).not.toContain('trace_id')
      expect(text).not.toContain('tr-9f3c01')
      expect(text).not.toContain('supported')
    }
  })

  it('F-53：敏感词拒译按 error_code 取本语种词条，裸码 sensitive_blocked 不得进气泡', async () => {
    const ctl = armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    // 后端（api/stream.go engineErrorPayload）现在的下发形态：error=人话、error_code=稳定码
    await act(async () => { ctl.reject(new ApiError('该内容包含敏感信息，已停止翻译。', undefined, 'sensitive_blocked')) })
    const a = assistant(res.current.messages)
    // 等值锁：气泡 === 当前语种（zh）词条值，不是后端原句、更不是键名
    expect(a.content).toBe(t('chat.sensitiveBlocked'))
    expect(res.current.errorMessage).toBe(t('chat.sensitiveBlocked'))
    expect(a.content).not.toContain('sensitive_blocked')
    expect(a.progress).toBeUndefined()
    expect(a.draft).toBeUndefined()

    // 语种切换同样生效（12 份全量词典都补了这个键；漏一份的话这里会拿 en/zh 兜底而不是键名）
    setLang('en')
    const ctl2 = armStream()
    await act(async () => { void res.current.sendMessage('y') })
    await act(async () => { ctl2.reject(new ApiError('sensitive_blocked', undefined, 'sensitive_blocked')) })
    expect(assistant(res.current.messages).content).toBe(t('chat.sensitiveBlocked'))
    expect(assistant(res.current.messages).content).toContain('sensitive terms')
    setLang('zh')
  })

  it('F-53 回落：老后端只把码当文案发（无 error_code）时，气泡仍是那句话、不渲染成本地词条占位', async () => {
    const ctl = armStream()
    const res = mountChat()
    await act(async () => { void res.current.sendMessage('x') })
    // 没有 code → 走通用兜底分支，正文原样透出 msg（后端未升级时不能出现空气泡）
    await act(async () => { ctl.reject(new Error('上游模型暂时不可用')) })
    expect(assistant(res.current.messages).content.trim()).toBe('上游模型暂时不可用')
  })

  it('F-52②：超过服务端下发的 chat_max_chars 时**不发请求**，提示带真实计数与上限', async () => {
    armStream()
    const res = mountChat()
    setChatMaxChars(6) // 模拟 /api/me/package 回 chat_max_chars=6
    const long = '一二三四五六七' // 7 字符 > 6
    await act(async () => { await res.current.sendMessage(long) })
    expect(h.chatStream, '超限文本必须在本地拦下：一次请求都不该发（旧形态是白跑一次 400）').not.toHaveBeenCalled()
    expect(res.current.messages, '拦下时不产生气泡对，避免留下一条永远转圈的助手气泡').toHaveLength(0)
    const copy = res.current.errorMessage
    expect(copy).toContain('7')
    expect(copy).toContain('6')
  })

  it('F-52② 负向：上限未知（chat_max_chars 缺失/为 0）时本地不设闸，正常放行给后端兜底', async () => {
    armStream()
    const res = mountChat()
    setChatMaxChars(0)
    await act(async () => { void res.current.sendMessage('一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十') })
    expect(h.chatStream, '上限未知就宁可不拦——误拦合法请求比少一次前置提示严重得多').toHaveBeenCalledTimes(1)
    expect(res.current.messages).toHaveLength(2)
  })

  it('F-52② 口径：字符数按码点计（emoji/增补平面一字一符），与后端 len([]rune) 同形', async () => {
    armStream()
    const res = mountChat()
    setChatMaxChars(3)
    // 3 个 emoji：每个是 2 个 UTF-16 code unit 但 1 个码点——按 .length 算会误判成 6 超限
    await act(async () => { void res.current.sendMessage('😀😀😀') })
    expect(h.chatStream, '码点计数下 3 字符恰好等于上限，不该拦（用 .length 就假红了）').toHaveBeenCalledTimes(1)
  })
})
