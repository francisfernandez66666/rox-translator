// ============================================================================
// components/admin/SdkP.dom.test.tsx — SDK 页「本地交付物下载」收口断言（★ F-27，2026-09-25）
// 锁死三件事：
//   ① 反向清零：整页渲染文本不得再出现假安装命令 `pip install langcross-translator` /
//      `npm install @langcross`（旧版 :35/:44 的假话，用户照抄必然 404）；
//   ② 等值锁：manifest 加载后，安装命令与每卡下载 href 必须精确等于
//      `/sdk/ + manifest 里的带版本文件名`（前端源码不写死版本，见 ③）；
//   ③ 源码级负向锁：组件源码不得出现写死的产物文件名/版本号（.whl/.tgz 字面量、
//      `translator-sdk-<数字>` 等），杜绝把版本又刻回组件里。
// manifest fetch 用 vi.stubGlobal 钉死为夹具：jsdom 无网络，且真实文件名口径由
// e2e/sdk_download.spec.ts（读盘现取 manifest）负责，两层各测各的。
// ============================================================================
// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { render, screen, waitFor, cleanup } from '@testing-library/react'

import SdkP from '@/components/admin/SdkP'

// 夹具：与 scripts/build_sdk.sh 生成的 manifest.json 同形状（文件名与当前盘上一致，
// 但断言只相对本夹具成立——盘上真值漂移由各 lane 自己的闸门负责，这里测的是组件行为）
const FIXTURE = {
  python: {
    version: '9.9.9',
    wheel: 'langcross_translator-9.9.9-py3-none-any.whl',
    sdist: 'langcross_translator-9.9.9.tar.gz',
  },
  typescript: { version: '8.8.8', tarball: 'langcross-translator-sdk-8.8.8.tgz' },
}

/** mockFetch 把 global.fetch 钉成返回夹具（ok=true）或指定失败形态 */
function mockFetch(json: unknown, ok = true) {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok, json: async () => json })))
}

describe('SdkP · 本地交付物下载收口（F-27）', () => {
  beforeEach(() => cleanup())
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it('manifest 加载后：安装命令/下载链接按 manifest 等值渲染，且全文无假命令', async () => {
    mockFetch(FIXTURE)
    const { container } = render(<SdkP />)
    // 等值锁①：python 卡安装命令 = `pip install <夹具 wheel 名>`
    await waitFor(() => expect(container.textContent).toContain(`pip install ${FIXTURE.python.wheel}`))
    expect(container.textContent).toContain(`npm install ./${FIXTURE.typescript.tarball}`)
    // 等值锁②：每卡下载 href 精确指向 /sdk/ 带版本文件名（禁 latest 之外的猜测值）
    const pyLink = screen.getByRole('link', { name: new RegExp(FIXTURE.python.wheel.replace(/\./g, '\\.')) })
    expect(pyLink.getAttribute('href')).toBe(`/sdk/${FIXTURE.python.wheel}`)
    const tsLink = screen.getByRole('link', { name: new RegExp(FIXTURE.typescript.tarball.replace(/\./g, '\\.')) })
    expect(tsLink.getAttribute('href')).toBe(`/sdk/${FIXTURE.typescript.tarball}`)
    // 版本徽章取 manifest 值（不再是组件里写死的旧基线）
    expect(container.textContent).toContain(`v${FIXTURE.typescript.version}`)
    // ★ 反向清零：假安装命令一条都不许残留（旧版 :35/:44 写法）
    expect(container.textContent).not.toContain('pip install langcross-translator')
    expect(container.textContent).not.toContain('npm install @langcross')
  })

  it('manifest 取不到时：宁可不显示，也不回退假命令/死链', async () => {
    mockFetch(null, false) // ok=false → 视为产物未部署
    const { container } = render(<SdkP />)
    // Java 卡的源码分发文案维持不动（本批 Java 不做产物托管）
    await waitFor(() => expect(container.textContent).toContain('Maven 源码分发'))
    // python/ts 安装行整体缺席：没有任何 pip/npm install 行
    expect(container.textContent).not.toContain('pip install')
    expect(container.textContent).not.toContain('npm install')
    // 下载链接只剩扩展卡（/extensions/…）一条，没有 /sdk/ 死链
    expect(container.querySelectorAll('a[href^="/sdk/"]').length).toBe(0)
    const ext = container.querySelector('a[href^="/extensions/"]')
    expect(ext).not.toBeNull()
  })

  it('扩展卡下载入口仍在（防本批改动误伤 2026-09-23 的扩展交付链）', async () => {
    mockFetch(FIXTURE)
    const { container } = render(<SdkP />)
    await waitFor(() => expect(container.textContent).toContain(`pip install ${FIXTURE.python.wheel}`))
    const ext = container.querySelector('a[href="/extensions/langcross-extension-latest.zip"]')
    expect(ext).not.toBeNull()
  })

  it('源码级负向锁：组件源码不写死产物文件名/版本号，也不含旧假命令', () => {
    const src = readFileSync(path.resolve(__dirname, 'SdkP.tsx'), 'utf8')
    expect(src).not.toContain('pip install langcross-translator')
    expect(src).not.toContain('npm install @langcross')
    // 产物文件名只能来自 manifest 运行时拼接：源码里不许出现 .whl/.tgz/.tar.gz 字面量
    expect(src).not.toContain('.whl')
    expect(src).not.toContain('.tgz')
    expect(src).not.toContain('.tar.gz')
    // 形如 langcross-translator-sdk-1.0.1 的「包名+版本号」写死形态一律禁止
    expect(src).not.toMatch(/langcross[-_]translator[-_]sdk-\d/)
  })
})
