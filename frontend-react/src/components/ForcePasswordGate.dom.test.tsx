// ============================================================================
// ForcePasswordGate.dom.test.tsx — 全局「强制改密」闸门的界面侧行为锁
// （★ 2026-10-21 IAM 重做 P0-8）
//
// 为什么单独锁这一件：后端 withPasswordGate 已对 must_change_pwd 账号的非认证接口回 428，
// 但**按钮长在界面上**——旧形态只有登录屏一个改密弹窗，进了工作台之后被打 428 的用户
// 既看不到框、也不知道该去哪儿改，屏幕上只剩一句「请求失败 (428)」。本组用例把三条口径
// 钉住（都是产品决策，不是实现细节）：
//   ① 事件驱动：`lc:force-password` 一到就开框；不靠路由/登录态分诊（428 可能发生在任何已登录页）；
//   ② 校验在前、请求在后：当前密码为空 / 新密码过短 / 两次不一致时**一次 API 都不发**，
//      框内出中文提示——否则用户会拿着空表单反复打后端，把限流也一起触发；
//   ③ 成功必须落地新 token：改密会抬 token_version，服务端随响应重签一条；
//      不落地的话下一个请求立刻又吃 401/428（旧 token 从这一刻起已被撤销）。
// 另附「取消＝退出登录」这一条：忘了管理员给的初始口令是合理出路，不能把人锁死在框里。
//
// 运行：npx vitest run src/components/ForcePasswordGate.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import ForcePasswordGate from './ForcePasswordGate'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang } from '@/i18n'
import { FORCE_PASSWORD_EVENT, getAuthToken, setAuthToken } from '@/api/core'
import { PWD_MIN_LEN } from '@/lib/accountForm'

// 只替换本组件真正调用的那一个接口；其余保持真实导出（auth.ts 里还有登录/注册等，
// 整模块打空会把同批其他单测的依赖一起弄没）。
const mocks = vi.hoisted(() => ({ changePassword: vi.fn() }))
vi.mock('@/api/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/auth')>()
  return { ...actual, changePassword: mocks.changePassword }
})

/** 触发一次 428 事件（与 api/core.ts 的 handlePasswordChangeRequired 同一事件名） */
function fireForceEvent() {
  window.dispatchEvent(new CustomEvent(FORCE_PASSWORD_EVENT))
}

/** 渲染闸门（它依赖 useToast，必须包在 ToastProvider 里——与 main.tsx 的挂法一致） */
function renderGate() {
  return render(
    <ToastProvider>
      <ForcePasswordGate />
    </ToastProvider>,
  )
}

/** 弹窗是否打开（Dialog 走 portal 到 body，用 role 取） */
const dialogOpen = () => !!screen.queryByRole('alertdialog')

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  setAuthToken('')
})

describe('ForcePasswordGate（★ IAM P0-8 强制改密闸门）', () => {
  it('默认关闭；收到 lc:force-password 才开框（不靠路由/登录态分诊）', async () => {
    renderGate()
    expect(dialogOpen(), '还没收到 428，不该先弹框').toBe(false)
    fireForceEvent()
    await waitFor(() => expect(dialogOpen()).toBe(true))
    expect(screen.getByRole('alertdialog').textContent).toContain('首次登录请修改密码')
  })

  it('点击遮罩不关框（这条闸门不可绕过；取消键才是退出登录）', async () => {
    renderGate()
    fireForceEvent()
    await waitFor(() => expect(dialogOpen()).toBe(true))
    const overlay = document.querySelector('.lc-overlay') as HTMLElement
    expect(overlay, 'Dialog 的遮罩节点应当存在（形态锚点）').toBeTruthy()
    fireEvent.mouseDown(overlay)
    expect(dialogOpen(), 'dismissOnOverlay=false：点遮罩必须留在框内').toBe(true)
    // 反向对照：遮罩上的 mousedown 确实被 Dialog 收到（不是"事件压根没触发"）。
    // 注意取 document 而不是 render 的 container——Dialog 走 createPortal 挂 body。
    expect(document.querySelector('.lc-dialog'), '弹窗仍挂在 body 的 portal 里').toBeTruthy()
  })

  it('校验在前：当前密码为空 → 出提示、零 API 调用', async () => {
    renderGate()
    fireForceEvent()
    await waitFor(() => expect(dialogOpen()).toBe(true))
    fireEvent.click(screen.getByText('设置新密码'))
    await waitFor(() => expect(screen.getByRole('alertdialog').textContent).toContain('请先输入当前密码'))
    expect(mocks.changePassword).not.toHaveBeenCalled()
  })

  it('校验在前：新密码过短 / 两次不一致 → 各自出提示、零 API 调用', async () => {
    renderGate()
    fireForceEvent()
    await waitFor(() => expect(dialogOpen()).toBe(true))
    // 三个字段（当前密码 / 新密码 / 确认）；用 label 定位，避免依赖 DOM 次序
    fireEvent.change(screen.getByLabelText('当前密码（初始密码）'), { target: { value: 'old-pass' } })
    fireEvent.change(screen.getByLabelText(/^新密码/), { target: { value: 'x' } })
    fireEvent.click(screen.getByText('设置新密码'))
    await waitFor(() => expect(screen.getByRole('alertdialog').textContent).toContain(`新密码至少 ${PWD_MIN_LEN} 位`))
    expect(mocks.changePassword).not.toHaveBeenCalled()

    // 长度够了但两次不一致：仍然不发请求
    fireEvent.change(screen.getByLabelText(/^新密码/), { target: { value: 'new-pass-1' } })
    fireEvent.change(screen.getByLabelText('再次输入新密码'), { target: { value: 'new-pass-2' } })
    fireEvent.click(screen.getByText('设置新密码'))
    await waitFor(() => expect(screen.getByRole('alertdialog').textContent).toContain('两次输入的新密码不一致'))
    expect(mocks.changePassword).not.toHaveBeenCalled()
  })

  it('后端拒绝（success:false）→ 原样出后端文案、不落地 token、不关框', async () => {
    mocks.changePassword.mockResolvedValue({ success: false, message: '当前密码不正确' })
    renderGate()
    fireForceEvent()
    await waitFor(() => expect(dialogOpen()).toBe(true))
    fireEvent.change(screen.getByLabelText('当前密码（初始密码）'), { target: { value: 'wrong-old' } })
    fireEvent.change(screen.getByLabelText(/^新密码/), { target: { value: 'new-pass-1' } })
    fireEvent.change(screen.getByLabelText('再次输入新密码'), { target: { value: 'new-pass-1' } })
    fireEvent.click(screen.getByText('设置新密码'))
    await waitFor(() => expect(screen.getByRole('alertdialog').textContent).toContain('当前密码不正确'))
    expect(mocks.changePassword).toHaveBeenCalledTimes(1)
    expect(mocks.changePassword).toHaveBeenCalledWith('wrong-old', 'new-pass-1')
    expect(getAuthToken(), '失败时不得替换 token').toBe('')
    expect(dialogOpen(), '失败留在框内可重试（Dialog 确认不自动关）').toBe(true)
  })

  it('成功：请求参数正确、新 token 落地、框关闭', async () => {
    mocks.changePassword.mockResolvedValue({ success: true, token: 'new-token-after-change' })
    renderGate()
    fireForceEvent()
    await waitFor(() => expect(dialogOpen()).toBe(true))
    fireEvent.change(screen.getByLabelText('当前密码（初始密码）'), { target: { value: 'old-pass' } })
    fireEvent.change(screen.getByLabelText(/^新密码/), { target: { value: 'new-pass-1' } })
    fireEvent.change(screen.getByLabelText('再次输入新密码'), { target: { value: 'new-pass-1' } })
    fireEvent.click(screen.getByText('设置新密码'))
    await waitFor(() => expect(getAuthToken()).toBe('new-token-after-change'))
    expect(mocks.changePassword).toHaveBeenCalledWith('old-pass', 'new-pass-1')
    await waitFor(() => expect(dialogOpen()).toBe(false))
  })

  it('取消＝退出登录：清本地会话（忘了初始口令的人要有一条出路，不能被锁死在框里）', async () => {
    setAuthToken('still-logged-in')
    renderGate()
    fireForceEvent()
    await waitFor(() => expect(dialogOpen()).toBe(true))
    fireEvent.click(screen.getByText('取消'))
    // 清 token 是同步发生的；随后的 location.assign('/login') 在 jsdom 里只报 not-implemented，
    // 不影响这条判据（这里锁的是"离开时不许把旧会话留着"）。
    expect(getAuthToken()).toBe('')
  })
})

// ============================================================================
// ★ 2026-10-21 IAM 重做 P1-3 / 2026-10-22 一致性核查：强制改密窗的密码可见切换
//   背景（PwdEye.tsx 文件头）：这颗钮子原先只写在 Login.tsx 里，注释承诺"登录/注册/
//   找回/强制改密四处共用"，而 ForcePasswordGate 是独立组件、import 不到它——于是这个
//   "必须过"的弹窗里三个密码框（当前口令 + 新口令 + 确认口令）全是盲输，而它是批量
//   导入账号的必经之路，初始口令还是随机串。修复 = 三框共用 components/PwdEye.tsx。
//   本 describe 锁两条口径：
//   ① 三框各挂一颗眼睛钮（不再有盲输框）；
//   ② 三颗钮共享同一个显隐状态——点任意一颗，三个 input 的 type 同时翻转
//      （"同进同退"：绝不出现第一个框亮了、后两个还盲着）。
// ============================================================================
describe('ForcePasswordGate 密码可见切换（三框同进同退）', () => {
  /** 打开弹窗并返回三个密码输入框（按 label 定位，不依赖 DOM 次序） */
  async function openGate() {
    setLang('zh') // 锁中文：aria-label 断言不随默认语种漂移
    renderGate()
    fireForceEvent()
    await waitFor(() => expect(dialogOpen()).toBe(true))
    return {
      old: screen.getByLabelText('当前密码（初始密码）'),
      next: screen.getByLabelText(/^新密码/),
      confirm: screen.getByLabelText('再次输入新密码'),
    }
  }

  it('三个密码框各挂一颗眼睛钮（批量导入的必经弹窗不允许有盲输框）', async () => {
    await openGate()
    // Dialog 走 portal 挂 body，从 document 查（render 的 container 里没有）
    const eyes = document.querySelectorAll('.auth-pwd-eye')
    expect(eyes.length, '当前/新/确认三个框必须各有一颗眼睛钮').toBe(3)
    // 初始全是密文态：aria-label 为「显示密码」、aria-pressed=false
    eyes.forEach((e) => {
      expect(e.getAttribute('aria-label')).toBe('显示密码')
      expect(e.getAttribute('aria-pressed')).toBe('false')
    })
  })

  it('点任意一颗钮：三个 input 的 type 同时翻转（同进同退，再点同时翻回）', async () => {
    const { old, next, confirm } = await openGate()
    expect([old.getAttribute('type'), next.getAttribute('type'), confirm.getAttribute('type')])
      .toEqual(['password', 'password', 'password'])
    // 点第一颗（当前口令那颗）——共享状态必须让三个框一起亮
    fireEvent.click(document.querySelectorAll('.auth-pwd-eye')[0])
    expect([old.getAttribute('type'), next.getAttribute('type'), confirm.getAttribute('type')])
      .toEqual(['text', 'text', 'text'])
    // 三颗钮的 aria 态同步翻面（可访问语义与视觉一致）
    const eyes = document.querySelectorAll('.auth-pwd-eye')
    eyes.forEach((e) => {
      expect(e.getAttribute('aria-label')).toBe('隐藏密码')
      expect(e.getAttribute('aria-pressed')).toBe('true')
    })
    // 点第三颗（确认口令那颗）——同样三个框一起收回去
    fireEvent.click(eyes[2])
    expect([old.getAttribute('type'), next.getAttribute('type'), confirm.getAttribute('type')])
      .toEqual(['password', 'password', 'password'])
  })
})
