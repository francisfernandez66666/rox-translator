// @vitest-environment jsdom
// ============================================================================
// components/Login.captcha_remount.dom.test.tsx — F-07 回归（2026-09-25 发布前 UAT）
// 线上事故形态：/register 进页 2.35s 被 AI 面板接管（默认必然发生），此后点 ×
//   退回传统表单——Turnstile 挂件随 AI 接管被卸载，而 mountCaptcha 只在启动配置
//   回调里调过一次，退回后没有任何逻辑再挂它。结果：容器是空壳、没有可操作的
//   验证框，captchaTokenRef 永远为空，「发送验证码」连请求都不发（静默）、
//   「注册并登录」同样被拦。企业注册在传统表单这条腿也被掐死（与 F-06 叠加＝全路径无生路）。
// 修复：Login.tsx 增加「注册屏回到 form 阶段且容器内无挂件 iframe → 重挂」的 effect，
//   mountCaptcha 内加 iframe 存在性判据防首帧双挂。本文件锁三段：
//   ① 首次进注册屏挂载恰好一次（双保险不过挂）；
//   ② AI 接管 → 点 × 退回后必须再挂一次，且容器里真的出现了挂件（iframe）；
//   ③ 退回重挂的是新容器（不是握着一个已从 DOM 摘掉的旧引用）。
// ============================================================================
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import Login from './Login'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang } from '@/i18n'

// 录制 renderTurnstile 的调用对象（容器元素），供「挂了几次、挂在哪个容器」断言
const mounts: HTMLElement[] = []

vi.mock('@/lib/turnstile', () => ({
  // jsdom 没有 window.turnstile：loadTurnstile 直接同步回调一个假 API，
  // 让 mountCaptcha 的装载链路在测试里真实走完（包括容器 iframe 判据）
  loadTurnstile: (onReady: (ts: { render: (el: HTMLElement) => string; execute: () => void }) => void) => {
    onReady({ render: () => 'w1', execute: () => {} })
    return () => {}
  },
  renderTurnstile: (el: HTMLElement) => {
    mounts.push(el)
    const f = document.createElement('iframe') // 模拟真挂件往容器里塞 iframe（判据就靠它）
    el.appendChild(f)
    return 'wid'
  },
  reexecTurnstile: () => {},
}))

// AI 面板桩：只保留「点 × 关闭」这一个行为（onClose 由 Login 传入，触发 setRegPhase('form')）
vi.mock('./AiRegisterFlow', () => ({
  default: ({ onClose }: { onClose: () => void }) => (
    <button data-testid="ai-close" onClick={() => onClose()}>close-ai</button>
  ),
}))

// 网络层整体替换：注册配置回「人机验证开 + 站点钥匙」，其余出口给齐（缺一个就是 undefined is not a function）
vi.mock('@/api', () => ({
  getAuthToken: vi.fn(() => null),
  login: vi.fn(async () => ({ success: false })),
  authRegister: vi.fn(async () => ({ success: false })),
  sendEmailCode: vi.fn(async () => ({ success: true })),
  registerConfig: vi.fn(async () => ({
    success: true, captcha_enabled: true, captcha_site_key: 'k-test', email_verify_enabled: false,
  })),
  forgotPassword: vi.fn(), resetPassword: vi.fn(), changePassword: vi.fn(),
  setAuthToken: vi.fn(), setActiveTenantId: vi.fn(),
  ssoProviders: vi.fn(async () => ({ enabled: false, providers: [] })),
  ssoLoginUrl: (p: string) => `/api/sso/login?provider=${encodeURIComponent(p)}`,
}))

vi.mock('@/branding', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/branding')>()
  return { ...actual, useBranding: () => ({ brandName: '', dedicatedRegister: false, tenantName: '', tenantId: 0, brandLogo: '', domain: '' }) }
})

// 动效不豁免：AI 接管节拍（1300/2220/2350ms）正是本缺陷的触发路径，必须真跑
beforeEach(() => {
  setLang('zh')
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (q: string) => ({ matches: false, media: q, onchange: null, addEventListener: () => {}, removeEventListener: () => {} }),
  })
})
afterEach(() => { cleanup(); mounts.length = 0 })

describe('Login F-07 · AI 接管退回后 Turnstile 必须重挂', () => {
  it('注册屏首挂载一次；AI 接管点 × 退回后再挂一次且新容器里有挂件', async () => {
    render(<ToastProvider><Login mode="home" onLogin={() => {}} /></ToastProvider>)
    // 切到注册屏：点「没有账号？自助注册试用」
    fireEvent.click(Array.from(document.querySelectorAll('button'))
      .find((b) => (b.textContent || '').includes('自助注册试用'))!)

    // ① 首挂载：配置回调与重挂 effect 双路到达，容器里只允许一个 iframe、恰好一次挂载调用
    await waitFor(() => {
      expect(mounts.length, '人机验证开启时进注册屏必须挂载挂件').toBe(1)
    }, { timeout: 3000 })
    const firstBox = mounts[0]
    expect(firstBox.querySelector('iframe')).not.toBeNull()

    // ② 等 AI 接管（regPhase 三段节拍 2.35s）：传统表单整支卸载，容器离开 DOM
    await waitFor(() => {
      expect(document.querySelector('[data-testid=ai-close]'), 'AI 面板应按时接管').not.toBeNull()
    }, { timeout: 4000 })
    expect(document.body.contains(firstBox), '接管后旧容器应已离开文档').toBe(false)
    expect(document.querySelector('#__ts_widget__'), '接管期间不应有传统表单容器').toBeNull()

    // ③ 点 × 退回传统表单：effect 必须对着新容器再挂一次
    fireEvent.click(document.querySelector('[data-testid=ai-close]')!)
    await waitFor(() => {
      expect(mounts.length, '退回传统表单后挂件必须重挂').toBe(2)
    }, { timeout: 3000 })
    const secondBox = mounts[1]
    expect(secondBox).not.toBe(firstBox) // 挂的是重渲染出来的新容器，不是旧引用
    const live = document.querySelector('#__ts_widget__')!
    expect(document.body.contains(secondBox)).toBe(true)
    expect(live.querySelector('iframe'), '用户必须看得见可操作的人机验证框').not.toBeNull()
  })
})
