// ============================================================================
// api/assist.test.ts — AI 销售/客服前端接口层单测
// 覆盖：会话 ID 本地持久化、会话消息本地缓存（读写/上限截断/损坏容错）、
//       隐藏路径判定、历史消息 actions 容错解析
// 运行环境：vitest node（localStorage 以内存桩模拟）
// ============================================================================

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// node 环境无 localStorage：挂内存桩（assist.ts 内部 try/catch 兜底，桩保证可读写）
const store = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
})

import { getAssistSid, setAssistSid, loadAssistMsgs, saveAssistMsgs, assistGreet, assistChat } from '@/api/assist'
import { isHiddenPath, safeParse } from '@/components/AiAssist'

beforeEach(() => {
  store.clear()
  // ★ 必须走 localStorage API 清理：模块顶层的 vi.stubGlobal 会被下方 afterEach 的
  //   unstubAllGlobals 撤掉，之后读写落到 vitest.setup.ts 的内存桩（跨用例常驻），
  //   只清 store 这个 Map 会留下上一条用例的缓存，造成假红。
  localStorage.removeItem('ny_assist_sid')
  localStorage.removeItem('ny_assist_tok')
  localStorage.removeItem('ny_assist_msgs')
})
afterEach(() => vi.unstubAllGlobals())

// ---------- 会话 ID 持久化 ----------
describe('assist session id 本地持久化', () => {
  it('初始为空串', () => {
    expect(getAssistSid()).toBe('')
  })
  it('写入后可读回', () => {
    setAssistSid('s123')
    expect(getAssistSid()).toBe('s123')
  })
  it('空串写入等于清除', () => {
    setAssistSid('s123')
    setAssistSid('')
    expect(getAssistSid()).toBe('')
  })
})

// ---------- 会话消息本地缓存（★ 2026-09-22「刷新一次页面就没了」） ----------
describe('assist 会话消息本地缓存', () => {
  it('无缓存返回空数组且不抛错', () => {
    expect(loadAssistMsgs()).toEqual({ sid: '', msgs: [] })
  })
  it('写入后可读回 sid 与消息（含 actions 原样保留）', () => {
    const acts = [{ key: 'k', name: '文件翻译', url: '/tickets', ftype: 'route' }]
    saveAssistMsgs('s9', [{ role: 'assistant', content: '回答', actions: acts }])
    const r = loadAssistMsgs()
    expect(r.sid).toBe('s9')
    expect(r.msgs).toEqual([{ role: 'assistant', content: '回答', actions: acts }])
  })
  it('超出上限只保留最近 40 条', () => {
    const many = Array.from({ length: 55 }, (_, i) => ({ role: 'user' as const, content: `m${i}` }))
    saveAssistMsgs('s', many)
    const r = loadAssistMsgs()
    expect(r.msgs).toHaveLength(40)
    expect(r.msgs[0].content).toBe('m15')
    expect(r.msgs[39].content).toBe('m54')
  })
  it('损坏 JSON / 非法行一律回空，绝不让挂件白屏', () => {
    localStorage.setItem('ny_assist_msgs', '{not json')
    expect(loadAssistMsgs().msgs).toEqual([])
    localStorage.setItem('ny_assist_msgs', JSON.stringify({ sid: 's', msgs: [{ role: 'system', content: 'x' }, { role: 'user' }] }))
    expect(loadAssistMsgs().msgs).toEqual([])
  })
})

// ---------- 隐藏路径（登录/注册页不显示挂件） ----------
describe('AiAssist isHiddenPath', () => {
  it('登录/注册页隐藏', () => {
    expect(isHiddenPath('/login')).toBe(true)
    expect(isHiddenPath('/register')).toBe(true)
    expect(isHiddenPath('/login/extra')).toBe(true)
    expect(isHiddenPath('/register/extra')).toBe(true)
  })
  it('主站路径常驻', () => {
    expect(isHiddenPath('/')).toBe(false)            // 官网落地页
    expect(isHiddenPath('/tickets')).toBe(false)     // 文件翻译
    expect(isHiddenPath('/admin')).toBe(false)       // 管理后台
    expect(isHiddenPath('/pricing')).toBe(false)     // 公开定价页
    expect(isHiddenPath('/my')).toBe(false)          // 个人中心
  })
  it('前缀相似路径不误伤', () => {
    expect(isHiddenPath('/loginx')).toBe(false)
    expect(isHiddenPath('/registerx')).toBe(false)
  })
})

// ---------- 历史消息 actions 解析容错 ----------
describe('AiAssist safeParse', () => {
  it('合法 JSON 数组正常解析', () => {
    const acts = safeParse('[{"key":"tickets","name":"文件翻译","url":"/tickets","ftype":"route"}]')
    expect(acts).toHaveLength(1)
    expect(acts?.[0].key).toBe('tickets')
  })
  it('非法 JSON 返回 undefined 不抛错', () => {
    expect(safeParse('not-json')).toBeUndefined()
  })
  it('非数组 JSON 返回 undefined', () => {
    expect(safeParse('{"a":1}')).toBeUndefined()
  })
  it('空串返回 undefined', () => {
    expect(safeParse('')).toBeUndefined()
  })
})

// ---------- ★ 082x：访客界面语言必须随请求送出去 ----------
// 后端拿这个字段决定两件事：回复用什么语言（【回复语言】段）、
// 中文话术/流程能不能直出（visitorWantsChinese）、欢迎词与 chips 翻不翻。
// 前端少送一次，这三条判据全部退回「一律中文」——就是用户报的那个现网现象。
// 判据抓的是**真发出去的 URL 与请求体**，不是模块里有没有 import getLang：
// 后者只证明代码存在，不证明值真的落到了链路上。
describe('assist 请求随带访客界面语言', () => {
  /** 起一个记录请求的假 fetch，返回 greet/chat 各自的最小可用响应体 */
  function captureFetch() {
    const seen: { greetUrl?: string; chatBody?: Record<string, unknown> } = {}
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: { body?: string }) => {
      const u = String(url)
      if (u.includes('/api/assist/greeting')) {
        seen.greetUrl = u
      } else {
        seen.chatBody = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
      }
      return {
        ok: true, status: 200,
        json: async () => (u.includes('greeting')
          ? { session: 's-1', tok: 't-1', greeting: 'hi', chips: [] }
          : { reply: 'ok', actions: [] }),
      }
    }))
    return seen
  }

  it('greet 送 query.lang、chat 送 body.lang', async () => {
    const { setLang } = await import('@/i18n')
    setLang('en')
    const seen = captureFetch()
    await assistGreet('/')
    expect(seen.greetUrl ?? '').toMatch(/[?&]lang=en(?:&|$)/)
    await assistChat('s-1', 'how much?', '/')
    expect(seen.chatBody?.lang).toBe('en')
  })

  // 第二次必须**真的换掉**：只锁"送过一次"的话，把值写成常量 'zh' 也能过，
  // 而那正是这次要修的形态（挂件永远按中文档走）。
  it('切换界面语言后两次请求都跟着变（值是每次现读的，不是模块加载时定死的）', async () => {
    const { setLang } = await import('@/i18n')
    setLang('de')
    const seen = captureFetch()
    await assistGreet('/')
    expect(seen.greetUrl ?? '').toMatch(/[?&]lang=de(?:&|$)/)
    setLang('ja')
    await assistChat('s-1', 'いくら？', '/')
    expect(seen.chatBody?.lang).toBe('ja')
  })

  it('繁体用 zh_hant（后端按汉字语种分档，写成 zh 会让繁体访客收到简体话术）', async () => {
    const { setLang } = await import('@/i18n')
    setLang('zh_hant')
    const seen = captureFetch()
    await assistChat('s-1', '多少積分？', '/')
    expect(seen.chatBody?.lang).toBe('zh_hant')
  })
})
