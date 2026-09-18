// @vitest-environment jsdom
// ============================================================================
// ui/langcross/src/Checkbox.dom.test.tsx — Checkbox / Switch 的 children 回归
//
// 回归的真实缺陷（2026-09-18）：`InputHTMLAttributes` 继承自 DOMAttributes，
// 自带 `children?: ReactNode`，所以 `<Checkbox>文字</Checkbox>` 能过类型检查；
// 但 children 会随 `{...rest}` 摊到 `<input>` 上，而 input 是 void 元素 →
// 运行期抛「input is a void element tag and must neither have children nor use
// dangerouslySetInnerHTML」，整屏被 ErrorBoundary 兜掉。
// 线上表现：注册页点「自助注册试用」后整页变成「页面出现异常」（注册流程全崩）。
// ============================================================================

import { cleanup, render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Checkbox, Switch } from './Checkbox'
import { Input } from './Input'

describe('Checkbox / Switch / Input · children 不得摊进 <input>', () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks() })

  it('用 children 传文字时不抛错，且 <input> 内没有任何子节点', () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => { /* 静音 React 的 dev 警告 */ })
    const { container } = render(
      <Checkbox checked={false} onChange={() => { /* noop */ }}>同意条款</Checkbox>,
    )
    const input = container.querySelector('input')
    expect(input).toBeTruthy()
    // 关键断言：void 元素里不能再有子节点（原缺陷正是这里被塞进了 children）
    expect(input!.childNodes.length).toBe(0)
    expect(input!.children.length).toBe(0)
    // 文字仍要渲染出来，只是落在 label 的 <span> 里
    expect(container.textContent).toContain('同意条款')
    expect(errSpy).not.toHaveBeenCalled()
  })

  it('label 属性与 children 等价，两者同时给时 label 优先', () => {
    const a = render(<Checkbox label="来自 label"/>)
    expect(a.container.textContent).toContain('来自 label')
    a.unmount()

    const b = render(<Checkbox label="优先项">被忽略</Checkbox>)
    expect(b.container.textContent).toContain('优先项')
    expect(b.container.textContent).not.toContain('被忽略')
    expect(b.container.querySelector('input')!.childNodes.length).toBe(0)
  })

  it('Switch 同样把 children 当 label，不摊进 <input>', () => {
    const { container } = render(<Switch checked onChange={() => { /* noop */ }}>开启通知</Switch>)
    expect(container.querySelector('input')!.childNodes.length).toBe(0)
    expect(container.textContent).toContain('开启通知')
  })

  // Input 是同一个坑（`InputHTMLAttributes` 自带 children 类型 → TS 不拦）
  it('Input 即使被误传 children 也不抛错，且 <input> 内无子节点', () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => { /* 静音 */ })
    const { container } = render(<Input value="x"onChange={() => { /* noop */ }}>误传的内容</Input>)
    const input = container.querySelector('input')
    expect(input).toBeTruthy()
    expect(input!.childNodes.length).toBe(0)
    expect(container.textContent).not.toContain('误传的内容')
    expect(errSpy).not.toHaveBeenCalled()
  })

  // 自校验：确认本测试环境真的会拦「void 元素带 children」。
  // 若这条不抛，说明上面的断言全是空转（回归测试就失去意义）。
  it('自校验：裸 <input> 带 children 必须抛错（证明断言有效）', () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => { /* 静音 React 的 dev 日志 */ })
    expect(() => render(<input type="checkbox">{'x'}</input>)).toThrow(/void element tag/i)
    errSpy.mockRestore()
  })
})
