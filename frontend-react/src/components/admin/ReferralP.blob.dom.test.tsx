// ============================================================================
// ReferralP.blob.dom.test.tsx — §4.2-1 邀请二维码 blob URL 生命周期回归（内存泄漏闸门）
// 背景：ReferralP 拉二维码走 fetch→blob→URL.createObjectURL 挂 <img>/<a>。旧实现 create 后
//   从不 revoke，面板卸载即永久泄漏一块 PNG Blob。#57 修复批补 qrUrlRef 持有当前 objectURL，
//   卸载时释放、异步竞态（拉取途中已卸载）时就地释放。此处钉死两条配对，改回旧写法即红。
// 只测 blob 生命周期（create↔revoke），不重跑面板的表格/统计渲染。
// 运行：npx vitest run src/components/admin/ReferralP.blob.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, cleanup, waitFor } from '@testing-library/react'
import { setLang } from '@/i18n'
import { useAdminStore } from '@/stores/admin'

// 面板只从 @/api 用 referralMy + fetchReferralQrBlob；stores/admin/auth 也依赖 @/api 的
// core 原语（getAuthToken/setActiveTenantId 等），故用 importOriginal 部分桩——只换掉这两个网络函数，
// 保留其余真实导出，避免把整层 api 抽空导致 store 初始化崩。
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    referralMy: vi.fn(async () => ({ success: false })),
    fetchReferralQrBlob: vi.fn(),
  }
})

import { ReferralP } from './ReferralP'
import { fetchReferralQrBlob } from '@/api'

const createSpy = vi.hoisted(() => vi.fn())
const revokeSpy = vi.hoisted(() => vi.fn())
const created: string[] = []
const revoked: string[] = []

beforeEach(() => {
  cleanup()
  setLang('zh')
  created.length = 0
  revoked.length = 0
  createSpy.mockReset()
  revokeSpy.mockReset()
  // 只有个人用户面板才拉二维码（isPersonal=false 直接 return null）
  useAdminStore.setState({ isPersonal: true })
  ;(URL as unknown as { createObjectURL: (b: unknown) => string }).createObjectURL = (b) => {
    const u = `blob:qr-${created.length + 1}`
    created.push(u)
    createSpy(b)
    return u
  }
  ;(URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = (u) => {
    revoked.push(u); revokeSpy(u)
  }
})

afterEach(() => {
  useAdminStore.setState({ isPersonal: false })
  vi.unstubAllGlobals()
})

describe('ReferralP · 二维码 blob URL create↔revoke 配对', () => {
  it('① 正常路径：挂载生成 objectURL，卸载时被恰好释放一次', async () => {
    vi.mocked(fetchReferralQrBlob).mockResolvedValue({ kind: 'png' } as unknown as Blob)
    const { unmount } = render(<ReferralP />)
    await waitFor(() => expect(created).toHaveLength(1))
    expect(revoked, '挂载期间不得提前 revoke').toHaveLength(0)

    unmount()
    // 卸载释放：qrUrlRef 里那张必须 revoke（配对）
    expect(revoked).toEqual([created[0]])
    expect(revokeSpy).toHaveBeenCalledTimes(1)
  })

  it('② 竞态路径：blob 拉取途中卸载 → 落地后就地 revoke，不泄漏', async () => {
    let resolveBlob: (b: Blob | null) => void = () => {}
    vi.mocked(fetchReferralQrBlob).mockReturnValue(
      new Promise<Blob | null>((res) => { resolveBlob = res }),
    )
    const { unmount } = render(<ReferralP />)
    unmount() // QR effect 的 alive 翻否
    resolveBlob({ kind: 'png' } as unknown as Blob)
    await waitFor(() => expect(created).toHaveLength(1))
    // 已销毁实例无人再 revoke → 落地分支必须就地释放
    expect(revoked).toEqual([created[0]])
  })
})
