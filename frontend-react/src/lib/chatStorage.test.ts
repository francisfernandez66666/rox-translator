// ============================================================================
// chatStorage.test.ts — 聊天持久化纯函数（★ F11：E1/E2 修复项回归锚）
// ============================================================================
import { describe, expect, it } from 'vitest'
import { msgsKeyFor, loadMsgs, serializeForPersist, MAX_MESSAGES } from './chatStorage'
import type { ChatMessage } from '@/types'

/** msg 构造最小会话消息（测试用厂） */
function msg(id: string, extra: Partial<ChatMessage> = {}): ChatMessage {
  return { id, role: 'user', content: 'x', timestamp: Date.now(), ...extra } as ChatMessage
}

describe('chatStorage（E1/E2 持久化与隔离）', () => {
  it('E2：存储键按账号隔离，匿名统一 :anon', () => {
    expect(msgsKeyFor(7)).toBe('chat_msgs_v1:7')
    expect(msgsKeyFor(8)).toBe('chat_msgs_v1:8')
    expect(msgsKeyFor(undefined)).toBe('chat_msgs_v1:anon')
    expect(msgsKeyFor(0)).toBe('chat_msgs_v1:anon')
    expect(msgsKeyFor(7)).not.toBe(msgsKeyFor(8))
  })

  it('E2：匿名键不落盘（serializeForPersist 返回 null）', () => {
    expect(serializeForPersist('chat_msgs_v1:anon', [msg('1')])).toBeNull()
    expect(serializeForPersist('chat_msgs_v1:7', [msg('1')])).toBeTypeOf('string')
  })

  it('loadMsgs：损坏/空输入返回空数组（不抛错）', () => {
    expect(loadMsgs(null)).toEqual([])
    expect(loadMsgs('not-json{')).toEqual([])
    expect(loadMsgs('{"not":"array"}')).toEqual([])
  })

  it('loadMsgs：超限收敛到尾部 MAX_MESSAGES 条', () => {
    const many = Array.from({ length: MAX_MESSAGES + 30 }, (_, i) => msg(`m${i}`))
    const back = loadMsgs(JSON.stringify(many))
    expect(back).toHaveLength(MAX_MESSAGES)
    expect(back[back.length - 1].id).toBe(`m${many.length - 1}`)
    expect(back[0].id).toBe(`m30`)
  })

  it('E1：落盘剥离 progress 字段（回填态持久化不夹带瞬态）', () => {
    const raw = serializeForPersist('chat_msgs_v1:7', [msg('a', { progress: { step: 'x' } } as any)])!
    expect(JSON.parse(raw)[0].progress).toBeUndefined()
  })

  it('★ B1：落盘同样剥离 draft（初译草稿是会话进行态，刷新后无在途 SSE 可续）', () => {
    const raw = serializeForPersist('chat_msgs_v1:7', [msg('a', { draft: { en: 'Hallo' } })])!
    const back = JSON.parse(raw)[0]
    expect(back.draft).toBeUndefined()
    expect(raw).not.toContain('Hallo')
  })

  it('★ B3：落盘剥离 segments/segmentsAborted（逐段实时态同属进行态，刷新后不复活半成品）', () => {
    const raw = serializeForPersist('chat_msgs_v1:7', [msg('a', {
      segments: { en: { sealed: false, rows: { 0: { text: '半成品段落', final: false, placeholder: false } } } },
      segmentsAborted: true,
    })])!
    const back = JSON.parse(raw)[0]
    expect(back.segments).toBeUndefined()
    expect(back.segmentsAborted).toBeUndefined()
    expect(raw).not.toContain('半成品段落')
  })

  it('>2MB 且 >50 条时裁剪到最近 50 条', () => {
    const fat = Array.from({ length: 80 }, (_, i) => ({ ...msg(`f${i}`), content: 'x'.repeat(40000) }))
    const raw = serializeForPersist('chat_msgs_v1:7', fat as ChatMessage[])!
    const arr = JSON.parse(raw)
    expect(arr.length).toBeLessThanOrEqual(50)
    expect(arr[arr.length - 1].id).toBe('f79')
  })
})
