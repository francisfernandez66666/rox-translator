// ============================================================================
// i18n/hardcodedCjkGate.test.ts — ★「后台/接口层写死中文」静态闸门（2026-09-24 建立）
//
// 背景（用户投诉）：英文界面后台出现成屏中文——任务说明、tab 名、授权弹窗等
//   直接硬编码中文字符串，绕过 i18n 管线。本批（〇-R）已把这些收敛到词典，
//   此闸门负责「不得回潮 + 不得新增」：
//   扫描 src/components/admin/ 与 src/api/ 的源码（去注释后），任何含中文字符的
//   字符串字面量都必须满足以下豁免之一，否则红灯：
//   ① 所在行出现 apiMsg( —— api 层中文兜底句是「未注册翻译器时的回落值」，
//      属设计（见 core.ts 注入器注释：api 层禁止静态 import i18n）；
//   ② 文件在 ALLOW_FILES 白名单（当前仅 SdkP.tsx：SDK 代码示例里的中文示例句，
//      示例语义即「翻译中文」，不属于界面文案）。
//   另：t('键') 里词典自身的中文值在 src/i18n/ 下，不在扫描面内。
// 运行：npx vitest run src/i18n/hardcodedCjkGate.test.ts
// ============================================================================
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const SRC = fileURLToPath(new URL('..', import.meta.url))
const CJK = /[\u4e00-\u9fff]/

/** 去注释：/* *\/ 块注释（非贪婪）+ 行注释（避开 http:// 这类冒号后双斜杠） */
function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
    .replace(/(^|[^:'"`])\/\/[^\n]*$/gm, '$1')
}

/** 找出去注释源码里的字符串字面量（单引号/双引号/模板串），返回 {line, text} */
function stringLiterals(src: string): Array<{ line: number; text: string }> {
  const out: Array<{ line: number; text: string }> = []
  const re = /(['"`])((?:(?!\1)[\s\S])*)\1/g
  let m: RegExpExecArray | null
  while ((m = re.exec(src))) {
    if (CJK.test(m[2])) out.push({ line: src.slice(0, m.index).split('\n').length, text: m[2] })
  }
  return out
}

describe('后台/接口层写死中文闸门（★ 2026-09-24 〇-R）', () => {
  const ALLOW_FILES = new Set(['components/admin/SdkP.tsx'])
  const dirs = ['components/admin', 'api']
  const offenders: string[] = []
  let scanned = 0
  for (const d of dirs) {
    for (const f of readdirSync(`${SRC}${d}`)) {
      if (!/\.tsx?$/.test(f) || f.includes('.test.')) continue
      const rel = `${d}/${f}`
      scanned++
      if (ALLOW_FILES.has(rel)) continue
      const raw = readFileSync(`${SRC}${d}/${f}`, 'utf-8')
      const lines = raw.split('\n')
      const stripped = stripComments(raw)
      for (const lit of stringLiterals(stripped)) {
        // 豁免①：apiMsg 兜底句——原文里**任一**含该字面量的物理行同时写了 apiMsg( 即放行
        //   （不能只看首次出现行：注释里的同名中文会把首现位置带到非代码行上，2026-09-24 首跑即踩）
        if (lines.some((l) => l.includes(lit.text.slice(0, 24)) && l.includes('apiMsg('))) continue
        offenders.push(`${rel}:${lit.line}: ${lit.text.slice(0, 40)}`)
      }
    }
  }
  it(`扫描 ${scanned} 个文件，admin 面板与 api 层不得新增写死中文（豁免：apiMsg 兜底句 / SdkP 示例）`, () => {
    expect(offenders, '写死中文清单：\n' + offenders.join('\n')).toEqual([])
  })
})
