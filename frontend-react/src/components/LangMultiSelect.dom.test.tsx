// ============================================================================
// LangMultiSelect.dom.test.tsx — ★ #23 面板去重回归锁（2026-09-20）
// 背景：后端 /api/translation/langs 扩容为 34 KB 语 + zh 后，组件把 apiKb 整体
// 并入 KB 分组，但本地 OTHER_LANGS（九条常用语）未剔除已进 KB 的 ja/ko/th——
// 面板同一语言出现两个 option，勾选态计数翻倍（e2e admin_tabs_lang T3 红灯实证）。
// 锁死规则：面板内 data-lang 零重复；已升级进 KB 的语言只出现在 KB 分组一次。
// 运行：npx vitest run src/components/LangMultiSelect.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import LangMultiSelect from './LangMultiSelect'

// 后端 KB 名单最小仿真：34 KB 代码（取九语 + 其他常用语的代表集）+ 末尾 zh
const KB_CODES = ['en', 'ru', 'ar', 'es', 'pt', 'fr', 'kk', 'de', 'zh_hant', 'ja', 'ko', 'th', 'vi', 'it', 'zh']

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function mockLangsFetch() {
  const kb_langs = KB_CODES.map((code) => ({ code, name: code, name_en: code, flag: '', kb: code === 'zh' ? 'false' : 'true' }))
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true, json: async () => ({ success: true, kb_langs }),
  })))
}

describe('LangMultiSelect 分组去重（#23 修复锁）', () => {
  it('后端扩容后同一语言在面板只出现一次（ja/ko/th 不再重复进其他语言组）', async () => {
    mockLangsFetch()
    const onChange = vi.fn()
    render(<LangMultiSelect value={['en']} onChange={onChange} />)
    fireEvent.click(screen.getByTestId('lang-multi-trigger'))
    const panel = await screen.findByTestId('lang-multi-panel')
    // apiKb 落地后等一帧重渲染（fetch mock 微任务即回）
    await vi.waitFor(() => {
      const codes = [...panel.querySelectorAll('[role="option"]')].map((el) => el.getAttribute('data-lang'))
      expect(new Set(codes).size).toBe(codes.length)
      expect(codes).toContain('ja')
    })
    const codes = [...panel.querySelectorAll('[role="option"]')].map((el) => el.getAttribute('data-lang'))
    // 已进 KB 的三个常用语各只出现一次；未升级的 it 仍在其他语言组
    for (const c of ['ja', 'ko', 'th']) expect(codes.filter((x) => x === c)).toHaveLength(1)
    expect(codes).toContain('it')
  })
})
