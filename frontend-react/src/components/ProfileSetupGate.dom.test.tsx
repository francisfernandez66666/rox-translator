// ============================================================================
// @vitest-environment jsdom
// ProfileSetupGate.dom.test.tsx — 会话恢复后的资料补全闸门
//
// 运行：npx vitest run src/components/ProfileSetupGate.dom.test.tsx
// 核心回归：登录响应之外的 /me 会话恢复也必须有资料补全入口；跳过不得把标记写死，
// 完成才更新全局用户并落地重签 token。
// ============================================================================
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ToastProvider } from '@/ui/langcross/src'

const authState = vi.hoisted(() => ({
  user: { id: 7, username: 'temporary', role: 'user', tenant_id: 3, profile_setup_required: 1 } as any,
  onLogin: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useAuth: () => authState,
}))

const profileSetup = vi.hoisted(() => vi.fn())
const setAuthToken = vi.hoisted(() => vi.fn())

vi.mock('@/api/auth', () => ({ profileSetup }))
vi.mock('@/api/core', () => ({ setAuthToken }))

import ProfileSetupGate from './ProfileSetupGate'

function renderGate() {
  return render(
    <ToastProvider>
      <ProfileSetupGate />
    </ToastProvider>,
  )
}

describe('ProfileSetupGate（IAM 重做：已登录会话恢复仍提醒）', () => {
  afterEach(cleanup)

  beforeEach(() => {
    authState.user = { id: 7, username: 'temporary', role: 'user', tenant_id: 3, profile_setup_required: 1 }
    authState.onLogin.mockReset()
    profileSetup.mockReset()
    setAuthToken.mockReset()
  })

  it('profile_setup_required=1 时自动出现', () => {
    renderGate()
    expect(screen.getByText('完善你的资料')).toBeTruthy()
  })

  it('暂时跳过只收本轮，不伪造"已完成"', async () => {
    profileSetup.mockResolvedValueOnce({ success: true, token: 'skip-token', user: authState.user })
    renderGate()
    fireEvent.click(screen.getByRole('button', { name: '暂时跳过' }))
    await waitFor(() => expect(profileSetup).toHaveBeenCalledWith({ skip: true }))
    await waitFor(() => expect(setAuthToken).toHaveBeenCalledWith('skip-token'))
    expect(authState.onLogin).toHaveBeenCalledWith(expect.objectContaining({ profile_setup_required: 1 }))
    await waitFor(() => expect(screen.queryByText('完善你的资料')).toBeNull())
  })

  it('完成资料会保存用户名、重签 token 并清标记', async () => {
    profileSetup.mockResolvedValueOnce({
      success: true,
      token: 'done-token',
      user: { ...authState.user, username: 'real-name', display_name: 'real-name', profile_setup_required: 0 },
    })
    renderGate()
    fireEvent.change(screen.getByPlaceholderText('用户名'), { target: { value: 'real-name' } })
    fireEvent.click(screen.getByRole('button', { name: '保存并进入' }))
    await waitFor(() => expect(profileSetup).toHaveBeenCalledWith({ skip: false, username: 'real-name' }))
    expect(setAuthToken).toHaveBeenCalledWith('done-token')
    expect(authState.onLogin).toHaveBeenCalledWith(expect.objectContaining({ username: 'real-name', profile_setup_required: 0 }))
    await waitFor(() => expect(screen.queryByText('完善你的资料')).toBeNull())
  })
})
