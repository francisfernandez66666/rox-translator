// ============ e2e/sdk_download.spec.ts · 职责说明 ============
// 官方 SDK 本地交付物（F-27）的**站点可下载性**冒烟（★ 2026-09-25）。
//
// 为什么要有这条：SDK 页从「假安装命令」收口为「本站托管下载」后，风险从「命令是假的」
// 变成「链接指向的产物没随源发布」。`spa.go` 对**不存在的路径**会回退成 index.html 且
// 状态码仍是 200——只判 200 等于没判（AGENTS.md §6 同一兜底陷阱，判据口径照
// e2e/extension_download.spec.ts：200 + 非 HTML 兜底 + 格式魔数 + 体积下限）。
//
// ⚠️ 魔数不是一律 `PK`：whl 是 zip 容器（PK），而 npm pack 的 tgz 与 Python sdist 都是
// **gzip**（首两字节 1f 8b），照抄扩展用例的 PK 会把好产物判死。两种魔数都经实测钉死。
//
// 版本号禁止写死：文件名与版本全部现读 public/sdk/manifest.json，并交叉核对
// sdk/python/pyproject.toml 与 sdk/typescript/package.json——manifest 落后于版本源
// 说明「改了 SDK 没重跑 scripts/build_sdk.sh」，在这里直接红灯。
// =============================================
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from '@playwright/test'

// 与 extension_download.spec.ts 同口径：用 import.meta.url 解析路径，不依赖 cwd
const HERE = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(HERE, '../..')

/** public/sdk/manifest.json：build_sdk.sh 生成的交付物清单（页面链接的唯一文件名来源） */
interface SdkManifest {
  python: { version: string; wheel: string; sdist: string; wheelLatest: string; sdistLatest: string }
  typescript: { version: string; tarball: string; tarballLatest: string }
}
const MANIFEST = JSON.parse(
  readFileSync(path.resolve(ROOT, 'frontend-react/public/sdk/manifest.json'), 'utf8'),
) as SdkManifest

// 版本源现读（manifest 必须与之一致，否则是「重打包忘同步」的交付漂移）
const PY_VER = (readFileSync(path.resolve(ROOT, 'sdk/python/pyproject.toml'), 'utf8').match(
  /^version\s*=\s*"([^"]+)"/m,
) as RegExpMatchArray)[1]
const TS_VER = (JSON.parse(
  readFileSync(path.resolve(ROOT, 'sdk/typescript/package.json'), 'utf8'),
) as { version: string }).version

test('manifest 与版本源一致（build_sdk.sh 漂移在站点侧的镜像锁）', () => {
  expect(MANIFEST.python.version, 'manifest 的 python 版本落后/超前于 pyproject').toBe(PY_VER)
  expect(MANIFEST.python.wheel).toContain(PY_VER)
  expect(MANIFEST.typescript.version, 'manifest 的 typescript 版本落后/超前于 package.json').toBe(TS_VER)
  expect(MANIFEST.typescript.tarball).toContain(TS_VER)
})

// 每个产物一条：路径 + 期望魔数 + 体积下限（whl=zip 容器；tar.gz/tgz=gzip，实测钉死）
const ARTIFACTS: Array<{ p: string; magic: [number, number]; minBytes: number; why: string }> = [
  { p: `/sdk/${MANIFEST.python.wheel}`, magic: [0x50, 0x4b], minBytes: 4000, why: 'whl 是 zip 容器，首字节须为 PK' },
  { p: `/sdk/${MANIFEST.python.sdist}`, magic: [0x1f, 0x8b], minBytes: 4000, why: 'sdist 是 gzip，首字节须为 1f 8b' },
  { p: `/sdk/${MANIFEST.typescript.tarball}`, magic: [0x1f, 0x8b], minBytes: 3000, why: 'npm pack 的 tgz 是 gzip，首字节须为 1f 8b' },
  // latest 别名（外部固定链路的稳定下载名）也必须在位且同源
  { p: `/sdk/${MANIFEST.python.wheelLatest}`, magic: [0x50, 0x4b], minBytes: 4000, why: 'whl latest 别名' },
  { p: `/sdk/${MANIFEST.typescript.tarballLatest}`, magic: [0x1f, 0x8b], minBytes: 3000, why: 'tgz latest 别名' },
]

for (const a of ARTIFACTS) {
  test(`SDK 交付物可下载：${a.p}`, async ({ request }) => {
    const res = await request.get(a.p)
    expect(res.status(), `${a.p} 应 200`).toBe(200)
    const body = await res.body()
    // 可达探针①：SPA 兜底会回一整个 index.html——那说明产物没进 dist（没随源发布）
    const head = body.subarray(0, 64).toString('utf8').toLowerCase()
    expect(head, `${a.p} 返回的是 HTML ⇒ 产物不在前端交付目录里 ⇒ /sdk 落进了 SPA 兜底`).not.toContain('<!doctype html')
    // 可达探针②：体积下限 + 格式魔数（空文件/占位文件/HTML 兜底都会在这里红）
    expect(body.length, `${a.p} 体积异常（下限 ${a.minBytes} 字节）`).toBeGreaterThan(a.minBytes)
    expect(body[0], `${a.p} 首字节异常：${a.why}`).toBe(a.magic[0])
    expect(body[1], `${a.p} 次字节异常：${a.why}`).toBe(a.magic[1])
  })
}
