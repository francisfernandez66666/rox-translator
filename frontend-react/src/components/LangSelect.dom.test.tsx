// ============================================================================
// LangSelect.dom.test.tsx — 语言切换触发钮「固定词」正向词表锁
//
// ★ 2026-10-13 用户后令覆盖拍板⑦的一半：触发钮**可见文字**不分界面语言、统一固定英文词
//   （2026-10-17 用户再令把那个词由 `Language` 收短为 `Lang`，配地球图标；词面仍固定一处）；
//   `aria-label` 仍随界面语言本地化（读屏按用户母语念），当前语种信息仍在 title 与菜单选中项。
//
// 口径（三段，各拦一种回潮）：
//   ① 12 语种逐档：可见文字 === LANG_BTN_FIXED_TEXT（'Lang'；改回随界面语言本地化 → 非 en 档全红）
//     ＋ aria-label === 该语种词典的 app.langBtn（把 aria-label 也写成固定英文 → 非 zh 档红）
//   ② 可见文字永不等于 LANG_OPTIONS 的任何 native 自称名（把自称名加回触发钮 → 红）
//   ③ title 与菜单选中项仍携带当前语种自称名（信息两条腿不许一起丢掉 → 红）
//
// ★ 反证口径（收口时实跑）：
//   把可见文字写回 `{t('app.langBtn')}` ⇒ ① 在除 en 外的 11 档红；
//   写成 `{current.native}` ⇒ ① 等值红、② 自称名撞词表红（两条都不是空转）；
//   把 aria-label 写成固定 'Language' ⇒ ① 的 aria 腿在除 en 外的 11 档红。
//   把可见文字改回长词 'Language' ⇒ ① 逐档等值红（本锁按常量判，常量改回即红）。
//
// 运行：npx vitest run src/components/LangSelect.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeAll, afterEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { LANG_OPTIONS, setLang, translateIn, ensureLocale, type Lang } from '@/i18n'
import { LangSelect, LANG_BTN_FIXED_TEXT } from './LangSelect'

// ★ 2026-10-11 懒加载配套：本文件的 aria-label 判据取自「界面文字 == 该语种词典的 app.langBtn」，
//   两侧都来自同一份运行时词典 ⇒ 词典未到手时两边**同时**回落到英文，等值锁照样绿（典型空转）。
//   先把十二语种全部到手，这条锁才真的在判「本地化那一半没被一起抹掉」。
beforeAll(async () => {
  await Promise.all(LANG_OPTIONS.map((o) => ensureLocale(o.code as Lang)))
})

afterEach(() => {
  cleanup()
  setLang('zh') // 复位，别把非中文态漏给后续用例
})

describe('LangSelect 触发钮固定词（★ 2026-10-13 后令 ＋ 10-17 收短：可见文字统一 Lang）', () => {
  // 遍历 12 语种：可见文字恒等固定英文，aria-label 恒等该语种本地词
  it('① 12 语种逐一：可见文字 === LANG_BTN_FIXED_TEXT(Lang)，aria-label === 该语种 app.langBtn', () => {
    for (const { code } of LANG_OPTIONS) {
      setLang(code as Lang)
      render(<LangSelect />)
      const span = document.querySelector('.lang-sel-cur') as HTMLElement
      const btn = document.querySelector('.lang-sel-btn') as HTMLElement
      // 可见文字：不分前端语言，逐档等值同一串英文（用户后令的本体判据）
      expect(span?.textContent, `${code} 界面下触发钮可见文字必须仍是固定 'Lang'`).toBe(LANG_BTN_FIXED_TEXT)
      // 读屏腿：仍随界面语言本地化，与词典同源（旧 app.langSwitch 是二元时代键，禁止回潮）
      expect(btn.getAttribute('aria-label'), `${code} aria-label 应为该语种本地词`).toBe(translateIn(code, 'app.langBtn'))
      cleanup()
    }
  })

  // 负向：可见文字永不是任何自称名（12 项逐一对撞）
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
