// ============================================================================
// LangMultiSelect.dom.test.tsx — ★ #23 面板去重回归锁（2026-09-20）
// 背景：后端 /api/translation/langs 扩容为 34 KB 语 + zh 后，组件把 apiKb 整体
// 并入 KB 分组，但本地 OTHER_LANGS（九条常用语）未剔除已进 KB 的 ja/ko/th——
// 面板同一语言出现两个 option，勾选态计数翻倍（e2e admin_tabs_lang T3 红灯实证）。
// 锁死规则：面板内 data-lang 零重复；已升级进 KB 的语言只出现在 KB 分组一次。
// ★ 〇-M（2026-09-23）追加：胶囊档触发器 + 面板向上弹、LangChips 的 max 折叠可展开（见下第二个 describe）。
// 运行：npx vitest run src/components/LangMultiSelect.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import LangMultiSelect, { LangChips } from './LangMultiSelect'

// 后端 KB 名单最小仿真：34 KB 代码（取九语 + 其他常用语的代表集）+ 末尾 zh
const KB_CODES = ['en', 'ru', 'ar', 'es', 'pt', 'fr', 'kk', 'de', 'zh_hant', 'ja', 'ko', 'th', 'vi', 'it', 'zh']

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

// 打桩 /api/translation/langs：固定返回一份 KB 名单（微任务即回，测完由 afterEach 撤桩）
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

// ★ 〇-M（2026-09-23）即时翻译输入区压成一行工具条带出的两条新契约：
//   ① 胶囊档触发器 + 面板向上弹（贴底触发时向下弹会被宿主 overflow:hidden 裁掉＝打不开）；
//   ② LangChips 的 max 折叠必须是「可展开」而不是只读计数——折走的语种仍要能一键 × 掉，
//      否则压缩就等于减功能（本项目口径：实装优先，压缩不减尺寸）。
describe('LangMultiSelect 胶囊档与 chips 折叠（〇-M 一行工具条）', () => {
  it('compact 触发器走胶囊档、面板向上弹', async () => {
    mockLangsFetch()
    render(<LangMultiSelect compact value={['en']} onChange={vi.fn()} />)
    const trigger = screen.getByTestId('lang-multi-trigger')
    expect(trigger.className).toContain('lms-trigger--pill')
    // 胶囊位窄，文案取短档「目标语言」（长档「选择目标语言」是整宽触发框用的）
    expect(trigger.textContent).toContain('目标语言')
    fireEvent.click(trigger)
    const panel = await screen.findByTestId('lang-multi-panel')
    expect(panel.className).toContain('lms-panel--up')
  })

  it('非 compact（工单页）保持整宽触发框与向下弹，形态不受本次压缩影响', async () => {
    mockLangsFetch()
    render(<LangMultiSelect value={['en']} onChange={vi.fn()} />)
    const trigger = screen.getByTestId('lang-multi-trigger')
    expect(trigger.className).not.toContain('lms-trigger--pill')
    fireEvent.click(trigger)
    const panel = await screen.findByTestId('lang-multi-panel')
    expect(panel.className).not.toContain('lms-panel--up')
  })

  it('LangChips 超 max 折成「+n」，点开后全部展开且每颗仍可移除', () => {
    const onRemove = vi.fn()
    const langs = ['en', 'ru', 'ar', 'ja', 'ko']
    const { container } = render(<LangChips langs={langs} onRemove={onRemove} max={3} dense />)
    const chipSel = () => container.querySelectorAll('[data-testid="lang-chips"] .tag-lang')
    expect(chipSel()).toHaveLength(3)
    const more = screen.getByTestId('lang-chips-more')
    expect(more.textContent).toBe('+2')
    // 折叠位必须交代被折走的是哪几个（title/aria 走语言名，读屏与悬浮都看得到）
    expect(more.getAttribute('title')).toBeTruthy()
    fireEvent.click(more)
    expect(chipSel()).toHaveLength(5)
    expect(container.querySelector('[data-testid="lang-chips-more"]')).toBeNull()
    // 展开后第 4 颗的 × 仍按「去掉自己」回传完整列表
    fireEvent.click(container.querySelectorAll('.lms-chip-close')[3])
    expect(onRemove).toHaveBeenCalledWith(['en', 'ru', 'ar', 'ko'])
  })

  it('未超 max 时不出现折叠位；dense 不再带独立成行的 8px 下外边距', () => {
    const { container } = render(<LangChips langs={['en', 'ru']} onRemove={vi.fn()} max={3} dense />)
    expect(container.querySelector('[data-testid="lang-chips-more"]')).toBeNull()
    expect((container.querySelector('[data-testid="lang-chips"]') as HTMLElement).style.paddingBottom).toBe('0px')
    // 不传 dense（工单页老用法）时行距保持原样
    const { container: c2 } = render(<LangChips langs={['en']} onRemove={vi.fn()} />)
    expect((c2.querySelector('[data-testid="lang-chips"]') as HTMLElement).style.paddingBottom).toBe('8px')
  })
})
