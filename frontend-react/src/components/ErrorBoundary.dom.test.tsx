// ============================================================================
// components/ErrorBoundary.dom.test.tsx — ★ 链路批④（#29）边界那一头的行为锁
//
// 现网症状：已开着的工作台里切换翻译工单，整页翻成「页面出现异常 / Importing a module script failed.」
// 根因与恢复腿见 src/lib/chunkRecovery.ts（发版换源后浏览器还拿着上一代的哈希 chunk 名）。
// 本文件只管**边界出给客户的那张脸**，四条：
//  ① 分片失败必须出**人话**（i18n 的 common.chunkLoadFailed），浏览器原文一个词都不许上屏
//     ——客户读不懂 "Importing a module script failed."，而它现在就是整屏唯一的文字；
//  ② 主按钮跟着换成「重新加载页面」，点它＝**整页重载**（走 chunkRecovery 的 force 支，
//     归还自动那一枪的预算）；原来的「重试」对分片失败是空动作（React.lazy 永久缓存已 reject
//     的 import），所以这一支不再出现在分片形态里；
//  ③ 非分片异常保持旧形态：原始 message 上屏（排障要信息）＋「重试」走本地复位，点完子树真能回来；
//  ④ resetKey 变了（用户切到别的页面）⇒ 异常态自动复位，外壳不许一直卡在崩溃卡上。
//
// ⚠️ 判据都从 i18n 现取（t(...)），不在此重写字面量——文案改一份词典就同步生效，
//    写死会让闸门比界面先过时（本仓既有口径）。
//
// 运行：npx vitest run src/components/ErrorBoundary.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { setLang, t } from '@/i18n'
import ErrorBoundary from './ErrorBoundary'
import { CHUNK_RETRY_MARK, __setChunkReloaderForTest } from '@/lib/chunkRecovery'

/** 抛错子树：throwNow 翻 true 即渲染期抛出给定异常（模拟懒加载分片解析失败） */
let throwNow = false
function Boom({ error }: { error: Error }) {
  if (throwNow) throw error
  return <div data-testid="ok">子树正常</div>
}
const CHUNK_ERR = new Error('Importing a module script failed.')
const OTHER_ERR = new Error('TypeError: cannot read x')

beforeEach(() => {
  setLang('zh')
  throwNow = false
  window.history.replaceState(null, '', '/')
})
afterEach(() => {
  __setChunkReloaderForTest(null)
  cleanup()
})

describe('①② 分片失败：出人话 + 主按钮是整页重载', () => {
  it('界面只有 i18n 文案，浏览器原文一个词都不上屏（负向锁配正向对照）', () => {
    throwNow = true
    render(<ErrorBoundary><Boom error={CHUNK_ERR} /></ErrorBoundary>)
    const body = screen.getByText(t('common.chunkLoadFailed'))
    expect(body).toBeTruthy()
    // 正向对照：这句「该出现」的关键词必须在（否则「不含英文原文」那条负向是空转）
    expect(t('common.chunkLoadFailed')).toContain('网站刚更新过')
    // 负向：分片异常的浏览器措辞不许出现在屏幕上（三种家措辞逐个点名）
    for (const raw of ['Importing a module script failed', 'dynamically imported module', 'preload CSS']) {
      expect(document.body.textContent ?? '', `屏幕上不该出现浏览器原文 ${raw}`).not.toContain(raw)
    }
  })
  it('主按钮＝重新加载页面，点它是整页重载（force）而不是本地复位', () => {
    const reload = vi.fn()
    __setChunkReloaderForTest(reload)
    throwNow = true
    render(<ErrorBoundary><Boom error={CHUNK_ERR} /></ErrorBoundary>)
    const btn = screen.getByText(t('common.reloadPage'))
    expect(btn).toBeTruthy()
    // 分片形态里「重试」那条空动作按钮必须不再出现（否则用户点两次还在同一屏）
    expect(screen.queryByText(t('common.retry')), '分片失败时不该给本地复位按钮').toBeNull()
    fireEvent.click(btn)
    expect(reload).toHaveBeenCalledTimes(1)
    // force 支的语义：URL 里不许留着自动重拨的护栏标记
    expect(window.location.hash).not.toContain(CHUNK_RETRY_MARK)
  })
  it('英文界面走英文文案（这套卡不是中文专属）', () => {
    setLang('en')
    throwNow = true
    render(<ErrorBoundary><Boom error={CHUNK_ERR} /></ErrorBoundary>)
    expect(screen.getByText(t('common.chunkLoadFailed')).textContent).toBe(t('common.chunkLoadFailed'))
    expect(t('common.chunkLoadFailed')).toContain('updated')
  })
})

describe('③ 非分片异常：保留旧形态（原文上屏 + 本地复位可用）', () => {
  it('出原始 message 与「重试」，点「重试」子树真能回来', () => {
    throwNow = true
    render(<ErrorBoundary><Boom error={OTHER_ERR} /></ErrorBoundary>)
    expect(screen.getByText(OTHER_ERR.message)).toBeTruthy()
    expect(screen.queryByText(t('common.chunkLoadFailed')), '非分片异常不该冒充分片文案').toBeNull()
    const retry = screen.getByText(t('common.retry'))
    throwNow = false // 下一次渲染不再抛，验证「本地复位」确实把子树接回来了
    fireEvent.click(retry)
    expect(screen.getByTestId('ok')).toBeTruthy()
  })
  it('「返回首页」两条形态都在（崩溃时永远留一条整页出口）', () => {
    throwNow = true
    const { unmount } = render(<ErrorBoundary><Boom error={CHUNK_ERR} /></ErrorBoundary>)
    expect(screen.getByText(t('app.backHome'))).toBeTruthy()
    unmount()
    throwNow = true
    render(<ErrorBoundary><Boom error={OTHER_ERR} /></ErrorBoundary>)
    expect(screen.getByText(t('app.backHome'))).toBeTruthy()
  })
})

describe('④ resetKey：换页面即自动复位', () => {
  it('pathname 变了，崩溃卡自己让位（不用用户点任何按钮）', () => {
    throwNow = true
    const { rerender } = render(<ErrorBoundary resetKey="/tickets"><Boom error={CHUNK_ERR} /></ErrorBoundary>)
    expect(screen.getByText(t('common.chunkLoadFailed'))).toBeTruthy()
    // 切到别的工单页＝换一屏懒加载件（那一屏的分片是好的，故这里把抛错闸落下）：
    // 异常态必须由边界自己清掉，否则一条腿的失败会粘住整个工作台（现网体感就是「只能返回首页」）
    throwNow = false
    rerender(<ErrorBoundary resetKey="/tickets/1001"><Boom error={CHUNK_ERR} /></ErrorBoundary>)
    expect(screen.queryByText(t('common.chunkLoadFailed'))).toBeNull()
    expect(screen.getByTestId('ok')).toBeTruthy()
  })
  it('同一路径重渲染不复位（异常态不许被无关 props 抖掉）', () => {
    throwNow = true
    const { rerender } = render(<ErrorBoundary resetKey="/tickets"><Boom error={CHUNK_ERR} /></ErrorBoundary>)
    rerender(<ErrorBoundary resetKey="/tickets"><Boom error={CHUNK_ERR} /></ErrorBoundary>)
    expect(screen.getByText(t('common.chunkLoadFailed'))).toBeTruthy()
  })
})
