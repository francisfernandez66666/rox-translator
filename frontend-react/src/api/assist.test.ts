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

import { getAssistSid, setAssistSid, loadAssistMsgs, saveAssistMsgs } from '@/api/assist'
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
