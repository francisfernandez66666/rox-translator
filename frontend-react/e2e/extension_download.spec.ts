// ============ e2e/extension_download.spec.ts · 职责说明 ============
// 浏览器划词插件 zip 的**站点可下载性**冒烟（★ 2026-09-23 补扩展交付链）。
//
// 为什么要有这条：扩展长期没有交付渠道（无打包脚本、站点无托管物），〇-LI 改完
// popup/content.css 后改动只能停在仓库里。现在 zip 落 `public/extensions/`、
// 后台 SDK 页挂 `/extensions/langcross-extension-latest.zip`，但「文件在仓库里」
// 不等于「线上点得开」——`spa.go` 对不存在的路径会**回退成 index.html 且仍是 200**，
// 所以只判 `status === 200` 会一路绿灯（AGENTS.md §6「链路型用例必须带可达探针」）。
// 本用例的判据因此是：200 + 不是 HTML 兜底 + 头两字节是 zip 的 `PK` 魔数 + 体积合理。
//
// 版本号不在这里写死：带版本号的包名从 `extension/manifest.json` 现读，
// 否则每发一版都要来改一次测试（而「测试落后于版本」正是假绿的温床）。
// 后台入口是否挂对链接，由 `src/extensionPackage.test.ts` 的静态锁负责（更快、不需登录态）。
// =============================================
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from '@playwright/test'

// 用 import.meta.url 而不是 __dirname 解析：Playwright 以 ESM 加载本文件，
// 后者在这套配置下取不到；cwd 也会随调用方（本地 / run_uat）变化，不能当基准。
const HERE = path.dirname(fileURLToPath(import.meta.url))
// 当前插件版本：唯一事实源是 manifest.json，测试只做读取不做复制
const VER = (JSON.parse(
  readFileSync(path.resolve(HERE, '../../extension/manifest.json'), 'utf8'),
) as { version: string }).version

// latest = 页面上的稳定入口；带版本号 = 回溯与留存用（两者都必须在产物里）
const ZIPS = ['/extensions/langcross-extension-latest.zip', `/extensions/langcross-extension-${VER}.zip`]

for (const p of ZIPS) {
  test(`插件包可下载：${p}`, async ({ request }) => {
    const res = await request.get(p)
    expect(res.status(), `${p} 应 200`).toBe(200)
    const body = await res.body()
    // 可达探针①：SPA 兜底会回一整个 index.html——那说明文件没进 dist（没重打包或没随源发布）
    const head = body.subarray(0, 64).toString('utf8').toLowerCase()
    expect(head, `${p} 返回的是 HTML ⇒ 文件不在前端产物里 ⇒ /extensions 落进了 SPA 兜底`).not.toContain('<!doctype html')
    // 可达探针②：zip 魔数必须是 PK（0x50 0x4B），空文件或占位文件都会在这里红
    expect(body.length, `${p} 体积异常（插件包含 7 个文件，约 11KB）`).toBeGreaterThan(8000)
    expect(body[0], `${p} 首字节不是 'P'`).toBe(0x50)
    expect(body[1], `${p} 次字节不是 'K'`).toBe(0x4b)
  })
}
