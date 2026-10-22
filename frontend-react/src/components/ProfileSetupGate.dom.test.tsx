// @vitest-environment jsdom
// ============================================================================
// ProfileSetupGate.dom.test.tsx — 会话恢复后的资料补全闸门
//
// 运行：npx vitest run src/components/ProfileSetupGate.dom.test.tsx
// 核心回归：登录响应之外的 /me 会话恢复也必须有资料补全入口；跳过不得把标记写死，
// 完成才更新全局用户并落地重签 token。
// ★ 2026-10-22 统一登录批：闸门按品牌租户分流——
//   · 主站（branding.tenantId===0）：弹 AI 完善面板（AiRegisterFlow mode='complete'），
//     props 必须带齐 loginEmail / prefillUsername / sessionUser，× = 本轮暂时跳过；
//   · 租户/演示站（tenantId>0）：保持原对话框不动（三个旧用例原样守）。
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
// ★ 2026-10-22：闸门自己拉 registerConfig 供 AI 面板的人机验证（登录屏那份随卸载消失）
const registerConfig = vi.hoisted(() => vi.fn(async () => ({ success: true, captcha_enabled: false, captcha_site_key: '' })))

vi.mock('@/api/auth', () => ({ profileSetup, registerConfig }))
vi.mock('@/api/core', () => ({ setAuthToken }))

// 品牌租户可切（0=主站 / 7=租户站）；vi.hoisted 防 TDZ（mock 工厂先于模块体执行）
const brandCtx = vi.hoisted(() => ({ tenantId: 0 }))
vi.mock('@/branding', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/branding')>()
  return {
    ...actual,
    useBranding: () => ({ brandName: '', dedicatedRegister: false, tenantName: '', tenantId: brandCtx.tenantId, brandLogo: '', domain: '' }),
  }
})

// AI 完善面板桩：记录上层 props 并暴露「关闭 / 完成」两个动作钮
const aiFlowProps = vi.hoisted(() => ({ last: null as null | Record<string, unknown> }))
vi.mock('./AiRegisterFlow', () => ({
  default: (props: Record<string, unknown>) => {
    aiFlowProps.last = props
    return (
      <div data-testid="ai-flow-stub">
        <button data-testid="ai-close" onClick={() => (props.onClose as () => void)()}>close</button>
        <button data-testid="ai-done" onClick={() => (props.onDone as (u: unknown) => void)(authState.user)}>done</button>
      </div>
    )
  },
}))

import ProfileSetupGate from './ProfileSetupGate'

function renderGate(tid = 0) {
  brandCtx.tenantId = tid
  return render(
    <ToastProvider>
      <ProfileSetupGate />
    </ToastProvider>,
  )
}

// ============================================================================
// ★ 2026-10-22 统一登录批：主站分支（AI 完善面板）
// ============================================================================
describe('ProfileSetupGate · 主站 AI 完善面板（2026-10-22）', () => {
  afterEach(cleanup)

  beforeEach(() => {
    authState.user = { id: 7, username: 'temporary', email: 'temporary@example.com', role: 'user', tenant_id: 3, profile_setup_required: 1 }
    authState.onLogin.mockReset()
    profileSetup.mockReset()
    setAuthToken.mockReset()
    registerConfig.mockClear()
    aiFlowProps.last = null
    brandCtx.tenantId = 0
  })

  it('主站不渲染旧对话框，改弹 AI 完善面板并带齐 complete 模式入参', async () => {
    renderGate(0)
    await waitFor(() => expect(screen.getByTestId('ai-flow-stub')).toBeTruthy())
    // 旧对话框（标题「完善你的资料」与「保存并进入」钮）不得出现
    expect(screen.queryByText('完善你的资料')).toBeNull()
    expect(screen.queryByRole('button', { name: '保存并进入' })).toBeNull()
    const p = aiFlowProps.last!
    expect(p.mode).toBe('complete')
    expect(p.loginEmail).toBe('temporary@example.com')
    expect(p.prefillUsername).toBe('temporary')
    expect(p.sessionUser).toBe(authState.user)
    expect(p.dedicatedRegister).toBe(false)
    // 人机验证配置由闸门自己拉取（登录屏那份已随卸载消失；失败=关闭）
    await waitFor(() => expect(registerConfig).toHaveBeenCalled())
  })

  it('× 关闭＝本轮暂时跳过：面板收起且不再自动重开', async () => {
    renderGate(0)
    await waitFor(() => expect(screen.getByTestId('ai-flow-stub')).toBeTruthy())
    fireEvent.click(screen.getByTestId('ai-close'))
    await waitFor(() => expect(screen.queryByTestId('ai-flow-stub')).toBeNull())
    // 库内标记不动（profileSetup 未被调用），刷新页面后仍会提醒
    expect(profileSetup).not.toHaveBeenCalled()
  })

  it('完成（onDone）后 onLogin 换上新用户、面板收摊', async () => {
    renderGate(0)
    await waitFor(() => expect(screen.getByTestId('ai-flow-stub')).toBeTruthy())
    fireEvent.click(screen.getByTestId('ai-done'))
    await waitFor(() => expect(authState.onLogin).toHaveBeenCalledWith(authState.user))
    await waitFor(() => expect(screen.queryByTestId('ai-flow-stub')).toBeNull())
  })
})

// ============================================================================
// 租户/演示站分支：原对话框三用例（2026-10-22 起只在这些站生效，逐字未动）
// ============================================================================
describe('ProfileSetupGate · 租户站原对话框（不随主站改动）', () => {
  afterEach(cleanup)

  beforeEach(() => {
    authState.user = { id: 7, username: 'temporary', role: 'user', tenant_id: 3, profile_setup_required: 1 }
    authState.onLogin.mockReset()
    profileSetup.mockReset()
    setAuthToken.mockReset()
    brandCtx.tenantId = 7 // 租户站：保持旧对话框
  })

  it('profile_setup_required=1 时自动出现', () => {
    renderGate(7)
    expect(screen.getByText('完善你的资料')).toBeTruthy()
    // 主站的 AI 面板在租户站不得出现
    expect(screen.queryByTestId('ai-flow-stub')).toBeNull()
  })

  it('暂时跳过只收本轮，不伪造"已完成"', async () => {
    profileSetup.mockResolvedValueOnce({ success: true, token: 'skip-token', user: authState.user })
    renderGate(7)
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
    renderGate(7)
    fireEvent.change(screen.getByPlaceholderText('用户名'), { target: { value: 'real-name' } })
    fireEvent.click(screen.getByRole('button', { name: '保存并进入' }))
    await waitFor(() => expect(profileSetup).toHaveBeenCalledWith({ skip: false, username: 'real-name' }))
    expect(setAuthToken).toHaveBeenCalledWith('done-token')
    expect(authState.onLogin).toHaveBeenCalledWith(expect.objectContaining({ username: 'real-name', profile_setup_required: 0 }))
    await waitFor(() => expect(screen.queryByText('完善你的资料')).toBeNull())
  })
})
