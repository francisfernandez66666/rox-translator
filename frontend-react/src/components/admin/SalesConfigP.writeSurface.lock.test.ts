// ============================================================================
// SalesConfigP.writeSurface.lock.test.ts — 二期销售配置面「写口全集」锁
//
// 判据：接口层只许有四个 POST 写口（配置保存／词表保存／词表删除／平台补种），
// 组件不许自己发请求、删除类动作必须经 confirmDialog 且带 danger。
// 配反证：往接口层塞第五个写法，扫描器必须当场看得见（不许退化成恒绿）。
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

/** 允许存在的写口全集（与 backend-go/internal/api/sales_config_api.go 的路由逐一对应） */
const ALLOWED_WRITES = [
  '/api/admin/sales/config/save',
  '/api/admin/sales/lexicon/save',
  '/api/admin/sales/lexicon/delete',
  '/api/admin/sales/lexicon/seed',
].sort()

/** 从源码里抽出发 POST 的 request 路径；判据只落在**同一次调用的配置对象**里，
 *  不按字符窗口往后扫——窗口会读到下一个函数的 method: 'POST'，把 GET 误记成写口。 */
function writePaths(src: string): string[] {
  const out: string[] = []
  const re = /request\(\s*'([^']+)',\s*\{([\s\S]*?)\}\)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(src))) {
    if (m[2].includes("method: 'POST'")) out.push(m[1])
  }
  return out.sort()
}

const apiSrc = readFileSync(fileURLToPath(new URL('../../api/salesConfig.ts', import.meta.url)), 'utf8')
const panelSrc = readFileSync(fileURLToPath(new URL('./SalesConfigP.tsx', import.meta.url)), 'utf8')

describe('销售配置面写口全集', () => {
  it('接口层写口恰好四个，且全部经 bizResp', () => {
    expect(writePaths(apiSrc)).toEqual(ALLOWED_WRITES)
    // 只数代码行（文件头注释里也写了同一句范例，按行首缩进排除，防判据自伤）
    const calls = apiSrc.match(/^  return bizResp\(/gm) || []
    expect(calls.length).toBe(5) // 1 读 + 4 写
  })

  it('组件不许自己发请求；删除必须弹一次确认框且 danger', () => {
    expect(panelSrc.includes('request(')).toBe(false)
    expect(panelSrc.includes('fetch(')).toBe(false)
    expect(panelSrc.includes('confirmDialog')).toBe(true)
    expect(panelSrc.includes('danger: true')).toBe(true)
    for (const fn of ['salesConfigGet', 'salesConfigSave', 'salesLexiconSave', 'salesLexiconDelete', 'salesLexiconSeed']) {
      expect(panelSrc.includes(fn)).toBe(true)
    }
  })

  it('反证：塞第五个写口扫描器必须看得见', () => {
    const bad = apiSrc.replace("'/api/admin/sales/config/save'", "'/api/admin/sales/config/reset'")
    expect(writePaths(bad).some((p) => !ALLOWED_WRITES.includes(p))).toBe(true)
  })
})
