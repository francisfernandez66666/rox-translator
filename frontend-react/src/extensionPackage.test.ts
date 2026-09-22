// ============ src/extensionPackage.test.ts · 职责说明 ============
// 浏览器划词插件（../extension/）的**交付漂移闸门**。
//
// 为什么必须有这条锁（2026-09-23 建立）：`extension/` 长期没有交付渠道——没有打包脚本、
// 站点上没有托管物、manifest 版本自初始就停在 1.0.0。〇-LI 把 popup/content.css 按交付真值
// 还原之后，改动静默地停在仓库里，谁也拿不到。补上 `scripts/build_extension.sh` 之后，
// 新的风险变成「改了源码忘了重打包，站点还在发旧包」——那是最坏的一类漂移：
// 用户在装、客服在答，发的却是上一个版本的包，而且没有任何自动化会报警。
// 所以这里把 zip 与源码绑成一对：**内容指纹不一致就红灯**。
//
// 指纹算法与 build_extension.sh 的 digest() 必须逐字节同口径
// （按 FILES 固定顺序拼接「文件名 + NUL + 文件字节 + NUL」再 sha256；缺文件记 `<missing>`）。
// 之所以不直接比 zip 字节：zip 条目带 mtime，同样内容两次打包字节不同，比字节的锁会假红。
// =============================================
import { createHash } from 'node:crypto'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

// 仓库根：本文件在 frontend-react/src 下，向上两级即仓库根（extension/ 与 frontend-react/ 的公共父目录）
const ROOT = path.resolve(__dirname, '../..')
// 插件源码目录（打包输入）
const EXT = path.join(ROOT, 'extension')
// 站点托管目录（打包输出，随前端构建产物一起被 spa.go 直出）
const OUT = path.join(ROOT, 'frontend-react', 'public', 'extensions')
// 与 scripts/build_extension.sh 的 FILES 数组一致（新增文件两处一起改，漏改即红灯）
const FILES = ['manifest.json', 'background.js', 'content.js', 'content.css', 'popup.html', 'popup.js', 'INSTALL.txt']

/** 与打包脚本同口径的内容指纹 */
function sourceDigest(): string {
  const h = createHash('sha256')
  for (const name of FILES) {
    h.update(Buffer.from(name + '\0', 'utf8'))
    const p = path.join(EXT, name)
    h.update(existsSync(p) ? readFileSync(p) : Buffer.from('<missing>', 'utf8'))
    h.update(Buffer.from('\0', 'utf8'))
  }
  return h.digest('hex')
}

describe('浏览器划词插件交付链（extension → 站点托管 zip）', () => {
  const manifest = JSON.parse(readFileSync(path.join(EXT, 'manifest.json'), 'utf8')) as {
    manifest_version: number; version: string; name: string
  }

  it('manifest 版本是 x.y.z，且不再是历史遗留的 1.0.0', () => {
    expect(manifest.version, 'extension/manifest.json 的 version 必须是语义化版本').toMatch(/^\d+\.\d+\.\d+$/)
    expect(manifest.version, '1.0.0 是「从未发过版」的历史停点，发版必须推进版本号').not.toBe('1.0.0')
  })

  it('带版本号的 zip 与 latest 副本都在位，且指纹文件成对存在', () => {
    const v = manifest.version
    for (const f of [`langcross-extension-${v}.zip`, 'langcross-extension-latest.zip',
      `langcross-extension-${v}.sha256`, 'langcross-extension-latest.sha256']) {
      const p = path.join(OUT, f)
      expect(existsSync(p), `托管产物缺失：frontend-react/public/extensions/${f} —— 跑 scripts/build_extension.sh 重打包`).toBe(true)
      expect(readFileSync(p).length, `${f} 是空文件`).toBeGreaterThan(0)
    }
  })

  it('zip 内容与 extension/ 源码一致（改了源码必须重打包）', () => {
    const want = sourceDigest()
    const v = manifest.version
    for (const f of [`langcross-extension-${v}.sha256`, 'langcross-extension-latest.sha256']) {
      const got = readFileSync(path.join(OUT, f), 'utf8').trim()
      expect(got, `交付漂移：${f} 记的是 ${got}，当前源码指纹是 ${want} —— 请重跑 scripts/build_extension.sh 并一并提交 zip`).toBe(want)
    }
  })

  it('站点下载入口与页面常量同路径（前端挂的链接不能是死链）', () => {
    // 后台「外部调用 → SDK」卡片里的链接常量与 public 目录下的产物必须同名，
    // 否则上线后点下载是 404（spa.go 对不存在的文件会回退到 SPA 壳，前端拿到一坨 HTML）
    const sdkP = readFileSync(path.join(ROOT, 'frontend-react', 'src', 'components', 'admin', 'SdkP.tsx'), 'utf8')
    const m = sdkP.match(/const EXT_ZIP = '([^']+)'/)
    expect(m, 'SdkP.tsx 里找不到 EXT_ZIP 常量（下载入口被改动，本锁需同步）').toBeTruthy()
    const rel = (m as RegExpMatchArray)[1].replace(/^\//, '')
    expect(existsSync(path.join(ROOT, 'frontend-react', 'public', rel)), `入口链接指向的产物不存在：${rel}`).toBe(true)
  })
})
