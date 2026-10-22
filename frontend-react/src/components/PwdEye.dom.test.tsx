// @vitest-environment jsdom
// ============================================================================
// components/PwdEye.dom.test.tsx — 密码可见切换钮的组件级行为锁
// （★ 2026-10-22 一致性核查：Login / ForcePasswordGate / ProfileSetupGate
//   三处共用这一个实现，替代原先 Login.tsx 内联写法）
//
// 为什么值得单锁一层：这件钮子在登录面是"锦上添花"，在强制改密弹窗里却是
// **批量导入账号的唯一出路**——初始口令是随机串，三个框（当前/新/确认）
// 全是盲输，敲错一个字符就得找管理员重置。本文件把四条契约钉住：
//   ① type=button：嵌在表单里点它绝不能误提交（旧写法若漏 type，回车/点击
//      会直接触发表单 submit）；
//   ② aria-pressed 与 shown 同步：这是开关钮的标准可访问语义，屏幕阅读器
//      靠它报"已按下/未按下"，不是靠猜图标；
//   ③ aria-label 走 auth.showPwd / auth.hidePwd 两键，且随 shown 翻转——
//      历史事故：某处写成词典里不存在的键，读屏软件把键名字符串念给用户；
//   ④ 图标随 shown 翻转（睁眼=点击显示 / 闭眼=点击隐藏），onToggle 点击即回调
//      （状态外提：本组件无内部状态，三处密码框的显隐由调用方同进同退）。
// ============================================================================
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { setLang } from '@/i18n'

import PwdEye from './PwdEye'

afterEach(() => {
  cleanup() // vitest 非全局模式不自动清理，手清防上一例 DOM 残留污染查询
})

/** 挂载一颗眼睛钮（shown/onToggle 由用例给定） */
function mountEye(shown: boolean, onToggle: () => void) {
  return render(<PwdEye shown={shown} onToggle={onToggle} />)
}

describe('PwdEye 密码可见切换钮（三处共用的唯一实现）', () => {
  it('type=button：嵌在表单里点击不会误触发表单提交', () => {
    // 回归防护：这颗钮长在 Input 的 trailing 位，若写成默认 type（submit），
    // 在登录表单里点一下就会把表单发出去——用户只是想看看自己输的口令
    mountEye(false, () => {})
    const btn = screen.getByRole('button')
    expect(btn.getAttribute('type')).toBe('button')
  })

  it('shown=false（密文态）：aria-pressed=false、aria-label=显示密码、渲染睁眼图标', () => {
    setLang('zh') // 锁中文：文案断言不随默认语种漂移
    mountEye(false, () => {})
    const btn = screen.getByRole('button')
    expect(btn.getAttribute('aria-pressed')).toBe('false')
    expect(btn.getAttribute('aria-label')).toBe('显示密码')
    // 睁眼图标（EyeIcon）带一枚圆形瞳孔；闭眼图标（EyeOffIcon）只有路径——
    // 用 circle 存在性做判别，不依赖图标内部结构细节
    expect(btn.querySelector('svg circle'), '密文态应显示睁眼图标（暗示点击可见）').not.toBeNull()
  })

  it('shown=true（明文态）：aria-pressed=true、aria-label=隐藏密码、渲染闭眼图标', () => {
    setLang('zh')
    mountEye(true, () => {})
    const btn = screen.getByRole('button')
    expect(btn.getAttribute('aria-pressed')).toBe('true')
    expect(btn.getAttribute('aria-label')).toBe('隐藏密码')
    expect(btn.querySelector('svg circle'), '明文态应显示闭眼图标（暗示点击隐藏）').toBeNull()
  })

  it('点击即回调 onToggle（显隐状态由调用方持有，本组件无内部状态）', () => {
    // 状态外提是 PwdEye 的核心契约：三个密码框必须同进同退，
    // 内部状态会出现"第一个框亮了、后两个还盲着"。这里锁"点击 = 通知调用方"。
    const onToggle = vi.fn()
    mountEye(false, onToggle)
    fireEvent.click(screen.getByRole('button'))
    expect(onToggle).toHaveBeenCalledTimes(1)
  })
})
