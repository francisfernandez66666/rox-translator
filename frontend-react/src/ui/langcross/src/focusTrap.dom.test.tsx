// @vitest-environment jsdom
// ============================================================================
// ui/langcross/src/focusTrap.dom.test.tsx — 模态焦点闭环回归（★ #43 a11y 补齐）
//
// 钉住的四条真实故障（都来自评估报告 §四「Dialog/Drawer 无 focus-trap」）：
//   ① 打开模态后焦点仍在背景：键盘用户要 Tab 穿整页才够到对话框；
//   ② Tab 走到边界后泄漏到遮罩背后：背景已被 aria-modal 对读屏隐藏，
//      焦点停在「念不出来的按钮」上（WCAG 2.4.3）；
//   ③ 关闭后焦点掉回 <body>：键盘用户从头再 Tab 整页；
//   ④ 嵌套模态（抽屉上再开确认框）互相抢焦点：外层把内层刚拿到的焦点拽走，
//      表现为「弹层按钮 Tab 没反应」——这条是加栈顶判定后才成立的。
//
// 环境说明（两条都会静默改变结论，故写清楚）：
//  - jsdom **不实现** Tab 的顺序焦点导航，所以「中间元素之间按 Tab」不可断言；
//    本测只断言 handler 主动接管的那几条边界（首尾环绕、越界拉回、还焦、栈顶让位），
//    这些恰好就是缺 trap 时会出错的位置。
//  - 本仓 vitest 未开 globals，@testing-library/react 的**自动 cleanup 不生效**，
//    而 Dialog/Drawer 走 createPortal 挂在 document.body 上：不显式 cleanup() 就会
//    跨用例堆积，document.querySelector('.lc-dialog') 取到上一个用例留下的旧框，
//    断言拿到的是「长得一模一样的另一个节点」（Object.is 失败但序列化相同）。
// ============================================================================
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { useState } from 'react'
import { Dialog } from './Dialog'
import { Drawer } from './Drawer'

afterEach(cleanup)

const tab = (shift = false) =>
  fireEvent.keyDown(document, { key: 'Tab', shiftKey: shift })

/** 按类名取模态盒（本文件已保证同一时刻只有一个未清理的实例） */
function box(className: string): HTMLElement {
  const el = document.querySelector(className) as HTMLElement | null
  if (!el) throw new Error(`未找到 ${className}：模态未渲染，后续断言全部无意义`)
  return el
}

describe('focusTrap · Dialog', () => {
  it('打开即入焦：焦点落在框内而不是背景', () => {
    const outer = document.createElement('button')
    document.body.appendChild(outer)
    outer.focus()
    render(
      <Dialog open title="标题" onConfirm={() => {}} onCancel={() => {}}>
        <button type="button">内联A</button>
      </Dialog>,
    )
    expect(box('.lc-dialog').contains(document.activeElement), '焦点未进对话框').toBe(true)
    outer.remove()
  })

  it('末元素按 Tab 绕回首元素；首元素 Shift+Tab 绕到末元素', () => {
    render(
      <Dialog open title="标题" onConfirm={() => {}} onCancel={() => {}}>
        <button type="button">内联A</button>
      </Dialog>,
    )
    const all = Array.from(box('.lc-dialog').querySelectorAll<HTMLElement>('button'))
    const first = all[0]
    const last = all[all.length - 1]
    last.focus()
    tab()
    expect(document.activeElement, 'Tab 越过末元素泄漏到背景').toBe(first)
    tab(true)
    expect(document.activeElement, 'Shift+Tab 越过首元素泄漏到背景').toBe(last)
  })

  it('焦点被移到背景后再按 Tab 必须被拉回框内', () => {
    const stray = document.createElement('button')
    document.body.appendChild(stray)
    render(
      <Dialog open title="标题" onConfirm={() => {}} onCancel={() => {}}>
        <button type="button">内联A</button>
      </Dialog>,
    )
    const b = box('.lc-dialog')
    const first = b.querySelector('button') as HTMLElement
    stray.focus()
    tab()
    expect(b.contains(document.activeElement), '背景焦点没被拉回模态内').toBe(true)
    expect(document.activeElement).toBe(first)
    stray.remove()
  })

  it('关闭后把焦点还给打开前的元素（不掉回 body）', () => {
    // 真实键盘链路：焦点在触发按钮上 → 回车打开 → Esc 关闭 → 焦点回到触发按钮。
    // Dialog 常驻、用 open prop 开关（组件树不卸载，只有内部返回 null），
    // 所以还焦发生在 trap 的 effect cleanup 里，而不是整棵子树被 remove 之后。
    function Harness() {
      const [open, setOpen] = useState(false)
      return (
        <>
          <button type="button" data-testid="opener" onClick={() => setOpen(true)}>
            打开
          </button>
          <Dialog open={open} title="标题" onConfirm={() => {}} onCancel={() => setOpen(false)}>
            <button type="button">内联A</button>
          </Dialog>
        </>
      )
    }
    render(<Harness />)
    const opener = screen.getByTestId('opener')
    opener.focus() // fireEvent.click 不移动焦点，手动模拟「在这个按钮上按回车」
    fireEvent.click(opener)
    expect(box('.lc-dialog').contains(document.activeElement), '打开后焦点没进对话框').toBe(true)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(document.querySelector('.lc-dialog'), '对话框未随状态关闭').toBeNull()
    expect(
      document.activeElement,
      '关闭后焦点没还给触发元素，键盘用户要重新 Tab 整页',
    ).toBe(opener)
  })
})

describe('focusTrap · 嵌套与 Drawer', () => {
  it('内层模态在场时，外层 trap 让位（焦点归最内层，不被抢走）', () => {
    // 先渲染 Drawer 再渲染 Dialog ⇒ 栈顶为 Dialog（真实叠加顺序：抽屉里点危险操作）
    render(
      <>
        <Drawer open title="抽屉" onClose={() => {}}>
          <button type="button">抽屉内</button>
        </Drawer>
        <Dialog open title="确认" onConfirm={() => {}} onCancel={() => {}}>
          <button type="button">弹窗内</button>
        </Dialog>
      </>,
    )
    const stray = document.createElement('button')
    document.body.appendChild(stray)
    stray.focus()
    tab()
    expect(
      box('.lc-dialog').contains(document.activeElement),
      'Tab 被外层抽屉抢走，内层弹层按不动',
    ).toBe(true)
    stray.remove()
  })

  it('Drawer 同样闭环：入焦 + 末元素 Tab 环绕', () => {
    render(
      <Drawer open title="抽屉" onClose={() => {}}>
        <button type="button">抽屉内A</button>
        <button type="button">抽屉内B</button>
      </Drawer>,
    )
    const b = box('.lc-drawer')
    const all = Array.from(b.querySelectorAll<HTMLElement>('button'))
    expect(b.contains(document.activeElement), '抽屉打开后焦点没进来').toBe(true)
    const last = all[all.length - 1]
    last.focus()
    tab()
    expect(document.activeElement).toBe(all[0])
  })
})
