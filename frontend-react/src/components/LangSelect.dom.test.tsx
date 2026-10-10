// ============================================================================
// LangSelect.dom.test.tsx — 语言切换触发钮「固定词」正向词表锁（★ 用户拍板⑦，2026-10）
//
// 口径：触发钮文字必须 = 当前界面语言词典的 app.langBtn（语言/Language/Langue/…），
//   且**永不等于** LANG_OPTIONS 的任何 native 自称名——防止将来有人把自称名加回触发钮。
//
// ★ 反证口径（收口时实跑）：把 LangSelect.tsx 触发钮的 `t('app.langBtn')` 写回
//   `{current.native}` ⇒ 正向等值锁（zh 档期望「语言」≠「简体中文」）当场红；
//   非负向空转：zh 档「简体中文」同时命中 NATIVES 词表一条，负向锁也红。
//
// 运行：npx vitest run src/components/LangSelect.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { LANG_OPTIONS, setLang, translateIn, type Lang } from '@/i18n'
import { LangSelect } from './LangSelect'

afterEach(() => {
  cleanup()
  setLang('zh') // 复位，别把非中文态漏给后续用例
})

describe('LangSelect 触发钮固定词（用户拍板⑦）', () => {
  // 遍历 12 语种：每个界面语言下触发钮文字 = 该语种词典的 app.langBtn 值
  it('① 12 语种逐一：触发钮文字 = 该语种 app.langBtn（固定词随界面语言本地化）', () => {
    for (const { code } of LANG_OPTIONS) {
      setLang(code as Lang)
      render(<LangSelect />)
      const span = document.querySelector('.lang-sel-cur') as HTMLElement
      const btn = document.querySelector('.lang-sel-btn') as HTMLElement
      const want = translateIn(code, 'app.langBtn')
      expect(span?.textContent, `${code} 触发钮应显示固定词`).toBe(want)
      // aria-label 与固定词同源（旧 app.langSwitch 是二元时代键，禁止回潮）
      expect(btn.getAttribute('aria-label'), `${code} aria-label 应为固定词`).toBe(want)
      cleanup()
    }
  })

  // 负向：触发钮文字永不是任何自称名（12 项逐一对撞）
  it('② 12 语种逐一：触发钮文字 ≠ 任何 LANG_OPTIONS 自称名（自称名不许回占触发钮）', () => {
    for (const { code } of LANG_OPTIONS) {
      setLang(code as Lang)
      render(<LangSelect />)
      const span = document.querySelector('.lang-sel-cur') as HTMLElement
      for (const { native: n } of LANG_OPTIONS) {
        expect(span?.textContent, `${code} 界面下触发钮出现了自称名「${n}」`).not.toBe(n)
      }
      cleanup()
    }
  })

  // 自称名降级腿仍在：菜单选中项（is-cur）携带当前语种自称名，title 悬停携带同值
  it('③ 12 语种逐一：菜单选中项自称名在位 + title 悬停 = 当前语种自称名（信息腿未丢）', () => {
    for (const { code, native } of LANG_OPTIONS) {
      setLang(code as Lang)
      render(<LangSelect />)
      const btn = document.querySelector('.lang-sel-btn') as HTMLElement
      expect(btn.getAttribute('title'), `${code} title 悬停腿应=自称名`).toBe(native)
      fireEvent.click(btn)
      const cur = document.querySelector('.lang-sel-menu .lang-sel-item.is-cur') as HTMLElement
      expect(cur?.textContent, `${code} 菜单选中项应携带自称名`).toContain(native)
      cleanup()
    }
  })
})
