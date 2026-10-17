// ============ htmlArtifactMinify.ts · 职责说明 ============
// 交付产物 index.html 的精简器（**唯一实现**，只被 vite.config.ts 的 transformIndexHtml 调用）。
//
// 为什么要有这一个文件（★ 2026-10-18 慢链路批，#1「国内加载慢」）：
//   index.html 是跨境首屏的**第一个必经字节**，实测线上直出 **22,656 B**（未压缩）、
//   TTFB 2.26 s；其中 **6,114 B 是给维护者看的 HTML 注释**、**12,992 B 是那份未压缩的
//   启动哨兵内联脚本**。这些字节在 6~20 KB/s 的链路上就是实打实的 1~2 s 白等，
//   而它们对访客零价值（注释是给读源码的人的，脚本只有压缩后才该上屏）。
//   于是：**注释留在源码 `frontend-react/index.html`，产物里去掉**——
//   这与仓库既有口径一致（直出面改注释要换二进制、产物里不留内部说明）。
//
// 三条实现纪律：
//   ① **不许动语义**：注释整段删除、标签之间的空白折叠、内联 JS/CSS/JSON-LD 只做压缩；
//      不合并行、不重排属性、不改动任何文本节点内容（只有 `>` 与 `<` 之间才算空白）。
//   ② **raw text 元素内的 `<!--` 是文本不是注释**（`<script>`/`<style>` 里可能出现），
//      扫描器必须跳过它们，否则会把脚本体咬掉一块（首跑就踩的形态）。
//   ③ **条件注释/许可注记/SSI 一律保留**（`<!--[if`、`<!--!`、`<!--#`）：它们是功能或法律注记，
//      不是"给读源码的人看的说明"。其余解析不出来的形态（未闭合注释）**原样留着**——
//      精简器的失败方向只能是"少省几个字节"，不许是"把文件咬掉一半"。

import { transformWithEsbuild } from 'vite'

/**
 * 删除 HTML 注释（保留 `<!--[if`／`<!--!`／`<!--#` 三类），并跳过 raw text 元素内部。
 */
export function stripHtmlComments(html: string): string {
  let out = ''
  let i = 0
  const n = html.length
  while (i < n) {
    const lt = html.indexOf('<', i)
    if (lt < 0) {
      out += html.slice(i)
      break
    }
    out += html.slice(i, lt)
    // raw text 元素：体内一切（含 `<!--`）都是文本，整段照抄
    const rawOpen = /^<(script|style|textarea|title)\b/i.exec(html.slice(lt, lt + 12))
    if (rawOpen) {
      const closeTag = '</' + rawOpen[1].toLowerCase()
      const end = html.toLowerCase().indexOf(closeTag, lt)
      const stop = end < 0 ? n : end
      out += html.slice(lt, stop)
      i = stop
      continue
    }
    if (html.startsWith('<!--', lt)) {
      const end = html.indexOf('-->', lt + 4)
      if (end < 0) {
        // 未闭合：原样保留到文件末尾（宁可少省字节，不许吃文件）
        out += html.slice(lt)
        break
      }
      const body = html.slice(lt + 4, end)
      if (/^\s*(\[if|!|#)/i.test(body)) out += html.slice(lt, end + 3)
      i = end + 3
      continue
    }
    out += '<'
    i = lt + 1
  }
  return out
}

/**
 * 标签之间的空白折叠（含行尾空白与多余空行）。文本节点内部的空白一字不动。
 */
export function collapseInterTagWhitespace(html: string): string {
  return html
    .replace(/>[ \t\r\n]+</g, '><')
    .replace(/[ \t]+$/gm, '')
    .replace(/\n{3,}/g, '\n')
    .trim()
}

/**
 * 把内联脚本/样式/JSON-LD 压到最短（用 Vite 自带的 esbuild，不引新依赖）。
 * 带 `src` 的标签与未知 `type` 的脚本一律原样返回。
 */
export async function minifyInlineBlocks(html: string): Promise<string> {
  type Job = { start: number; end: number; text: string; loader?: 'js' | 'css' }
  const jobs: Job[] = []
  const re = /<(script|style)\b([^>]*)>([\s\S]*?)<\/\1>/gi
  let m: RegExpExecArray | null
  while ((m = re.exec(html)) !== null) {
    const [full, tag, attrs, body] = m
    if (!body.trim()) continue
    if (/\bsrc\s*=/i.test(attrs)) continue
    const typeM = /\btype\s*=\s*["']([^"']*)["']/i.exec(attrs)
    const type = (typeM ? typeM[1] : '').toLowerCase()
    const start = m.index + full.indexOf('>') + 1
    if (tag.toLowerCase() === 'style') {
      jobs.push({ start, end: start + body.length, text: body, loader: 'css' })
      continue
    }
    if (type === 'application/ld+json') {
      // 结构化数据：只做 JSON 空白压缩，**不再过 esbuild**（JSON 当 JS 压会改语义）
      try {
        jobs.push({ start, end: start + body.length, text: JSON.stringify(JSON.parse(body)) })
      } catch {
        /* 解析不了就原样留着 */
      }
      continue
    }
    if (type && type !== 'module' && type !== 'text/javascript') continue
    jobs.push({ start, end: start + body.length, text: body, loader: 'js' })
  }
  if (!jobs.length) return html
  const parts: string[] = []
  let cursor = 0
  for (const job of jobs) {
    parts.push(html.slice(cursor, job.start))
    parts.push(job.loader ? await compress(job.text, job.loader) : '\n' + job.text + '\n')
    cursor = job.end
  }
  parts.push(html.slice(cursor))
  return parts.join('')
}

/**
 * 单段压缩：**按标签本身决定 loader**（script→js、style→css），失败原样返回。
 * ★ 不许"两种 loader 都试一遍"——CSS 解析器碰上简单 JS 有可能"成功"地把它揉成别的语义，
 * 那种静默损坏比不压缩坏得多。
 */
async function compress(code: string, loader: 'js' | 'css'): Promise<string> {
  const trimmed = code.trim()
  if (!trimmed) return code
  try {
    const out = await transformWithEsbuild(trimmed, 'inline.' + loader, {
      loader,
      minify: true,
      legalComments: 'none',
    })
    if (out.code && out.code.trim()) return '\n' + out.code.trim() + '\n'
  } catch {
    /* 压缩失败：保留原样 */
  }
  return code
}

/**
 * 产物 HTML 的总入口：删注释 → 压内联块 → 折叠标签间空白。
 */
export async function minifyIndexHtml(html: string): Promise<string> {
  const withoutComments = stripHtmlComments(html)
  const minifiedBlocks = await minifyInlineBlocks(withoutComments)
  return collapseInterTagWhitespace(minifiedBlocks)
}
