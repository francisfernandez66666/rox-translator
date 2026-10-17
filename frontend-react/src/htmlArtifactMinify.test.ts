// ============ htmlArtifactMinify.test.ts · 职责说明 ============
// 产物 index.html 精简器的判据（★ 2026-10-18 慢链路批 #1）。
//
// 判据取向：**每一腿都问"删掉它会不会让站点坏掉"**，不是"省了多少字节"——
// 省字节是副产品，产物坏掉是事故。四腿 + 两枚反证：
//   P1 真件（本仓 index.html）：语义标记（哨兵 id/骨架/#root/入口 script/入口 link/JSON-LD）
//      一律还在，注释清零，体积显著变小；
//   P2 raw text 不被咬：`<script>` 体内含 `<!--`／`-->` 的字符串必须逐字保留；
//   P3 保留条件注释/许可注记（`<!--[if`、`<!--!`）与未闭合注释（不许吃文件）；
//   P4 内联 JS 压缩后**行为等价**：把压缩前后的脚本在同一段输入上各跑一次，出参逐字相同；
//   R1 反证：把 stripHtmlComments 换成朴素正则 `<!--[\s\S]*?-->` ⇒ P2 必须红（那条朴素写法正是首跑真踩的形态）；
//   R2 反证：让 compress 用 css loader 压 JS ⇒ P4 必须红。

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  collapseInterTagWhitespace,
  minifyIndexHtml,
  minifyInlineBlocks,
  stripHtmlComments,
} from './lib/htmlArtifactMinify'

const SRC = resolve(__dirname, '../index.html')

describe('产物 index.html 精简器', () => {
  it('P1 真件：语义标记全在、注释清零、体积显著变小', async () => {
    const src = readFileSync(SRC, 'utf8')
    const out = await minifyIndexHtml(src)
    // 语义标记（缺一个首屏就坏）
    for (const anchor of [
      'id="__boot_skeleton"',
      'class="lc-boot-cover"',
      'id="root"',
      '<noscript>',
      '<script type="application/ld+json">',
      'ld+json',
      'data-kind',
      '/src/main.tsx',
    ]) {
      expect(out, `产物里丢了 ${anchor}`).toContain(anchor)
    }
    // 注释清零（源码里的说明一条都不该上屏）
    expect(out).not.toContain('<!--')
    // 体积：源码 22 KB 级 ⇒ 产物必须显著更小（守卫掉"精简器被摘掉"这类静默失效）
    expect(src.length).toBeGreaterThan(15_000)
    expect(out.length).toBeLessThan(src.length * 0.6)
  })

  it('P2 raw text 不被咬：脚本体里的 `<!--` 是文本', () => {
    const html = '<body><script>var s = "<!-- x --\u003e"; if (a < b) f()</script><div>hi</div></body>'
    const out = stripHtmlComments(html)
    expect(out).toContain('var s = "<!-- x -->"')
    expect(out).toContain('if (a < b) f()')
    expect(out).toContain('<div>hi</div>')
  })

  it('P3 条件注释/许可注记保留、未闭合注释不吃文件', () => {
    const keep = '<!--[if IE]><p>ie</p><![endif]--><!--! license --><p>ok</p>'
    const out = stripHtmlComments(keep)
    expect(out).toContain('<!--[if IE]')
    expect(out).toContain('<!--! license -->')
    expect(out).toContain('<p>ok</p>')
    // 未闭合：原样保留（不许把后半篇文件当注释吃掉）
    const broken = '<p>a</p><!-- 未闭合 <p>b</p>'
    expect(stripHtmlComments(broken)).toBe(broken)
  })

  it('P4 内联 JS 压缩后行为等价（同输入同输出）', async () => {
    const body = `
      // 说明注释：压缩后不该留
      var calls = []
      function probe(n) {
        if (n < 0) { return 'neg' }
        var s = ''
        for (var i = 0; i < n; i++) { s += '(' + i + ')' }
        calls.push(s)
        return s
      }
      window.__probe = function (n) { probe(n); return calls.join('|') }
    `
    const html = `<body><script>${body}</script></body>`
    const out = await minifyInlineBlocks(html)
    expect(out).not.toContain('说明注释')
    const inner = (h: string) => /<script>([\s\S]*?)<\/script>/.exec(h)![1]
    const run = (code: string) => {
      const captured: unknown[] = []
      const win = { __probe: undefined as unknown }
      // eslint-disable-next-line no-new-func
      new Function('window', code)(win)
      const fn = win.__probe as (n: number) => string
      captured.push(fn(3))
      captured.push(fn(0))
      captured.push(fn(-1))
      return captured.join(';')
    }
    expect(run(inner(out))).toBe(run(inner(html)))
  })

  it('R1 反证：朴素正则会把脚本体咬掉 ⇒ P2 判据有射程', () => {
    const html = '<body><script>var s = "<!-- x --\u003e"</script><div>hi</div></body>'
    const naive = html.replace(/<!--[\s\S]*?-->/g, '')
    // 朴素写法把字符串里的 `<!-- x -->` 也当注释吃掉 ⇒ 交付的脚本已被改坏（`var s = ""`）
    expect(naive).not.toContain('<!-- x -->')
    expect(naive).toContain('var s = ""')
    expect(stripHtmlComments(html)).toContain('var s = "<!-- x -->"')
  })

  it('R2 反证：loader 必须按标签分派（两种 loader 对同一段体的产出不同）', async () => {
    const { transformWithEsbuild } = await import('vite')
    // `a:b` 两种 loader 都收得下，但产出不同 ⇒ "两种都试一遍"会静默改成另一种语义
    const body = 'a:b'
    const asCss = (await transformWithEsbuild(body, 'x.css', { loader: 'css', minify: true })).code.trim()
    const asJs = (await transformWithEsbuild(body, 'x.js', { loader: 'js', minify: true })).code.trim()
    expect(asCss).not.toBe(asJs)
    // 真实现：<script> 走 js（产出与 js loader 等值），<style> 走 css
    const scriptOut = await minifyInlineBlocks(`<body><script>${body}</script></body>`)
    expect(scriptOut).toContain(asJs)
    const styleOut = await minifyInlineBlocks(`<body><style>${body}</style></body>`)
    expect(styleOut).toContain(asCss)
  })

  it('标签间空白折叠只动标签之间', () => {
    const html = '<div>\n  <p>保留  文本</p>\n\n\n  <span>x</span>\n</div>'
    const out = collapseInterTagWhitespace(html)
    // 标签之间的空白（含缩进与空行）全清；文本节点内部的连续空格一字不动
    expect(out).toBe('<div><p>保留  文本</p><span>x</span></div>')
  })
})
