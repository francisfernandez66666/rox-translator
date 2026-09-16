// ============================================================================
// api/assist.test.ts — AI 销售/客服前端接口层单测
// 覆盖：会话 ID 本地持久化、隐藏路径判定、历史消息 actions 容错解析
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

import { getAssistSid, setAssistSid } from '@/api/assist'
import { isHiddenPath, safeParse } from '@/components/AiAssist'

beforeEach(() => store.clear())
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
