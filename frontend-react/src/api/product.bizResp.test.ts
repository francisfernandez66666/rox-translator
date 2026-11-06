// ============================================================================
// api/product.bizResp.test.ts — 一期新接口文件的 bizResp 接线锁（施工_一期 §9.2）
//
// 复用 bizRespGate.test.ts 的**主判据口径**（零裸返、每个信封调用都经 bizResp 收敛），
// 专门钉住三件事：
//   ① src/api/product.ts 里每个导出函数的返回都包 bizResp（后端 F-64① 之后失败走结构化 4xx，
//      绕开 bizResp 直返裸 request 会让对应面板对着 ApiError 白屏）；
//   ② 该文件**不进任何豁免档**（INFRA／EXEMPT／READ_THROW_EXEMPT／LEGACY_UNWRAPPED 四张表
//      都不许出现 product.ts）——新文件走主判据，不靠登记成存量蒙混；
//   ③ 射程自证：判据读得到真源码、数得到函数，反证把一处 bizResp 摘掉必须当场红。
// 运行：npx vitest run src/api/product.bizResp.test.ts
// ============================================================================
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const PRODUCT = fileURLToPath(new URL('./product.ts', import.meta.url))
const GATE = fileURLToPath(new URL('./bizRespGate.test.ts', import.meta.url))

/** 剥注释：块注释 + 整行行注释（判据只认执行代码；理由注释里写「不包 bizResp」不算点位） */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/**
 * 主判据（与 bizRespGate.test.ts 的 BARE_RETURN／await request 同口径）：
 *  ① 零裸返点位；② 导出函数个数 ≡ `return bizResp(` 个数（每个信封调用都被包）。
 * 抽成纯函数，真实源码与变异体喂同一把尺子。
 */
// 二进制出栈档的唯一标记（translate.ts 那条「正当豁免」的同族形态，但**必须写在函数自己的说明里**）。
// 为什么不开第二张白名单表：文件名会漂、函数会搬，写在被豁免函数头上的标记让"豁免"与"被豁免的那一个"
// 永远同处一地——函数删了标记跟着没，把标记偷渡到别的函数上则立刻多点一位（反证 C 抓这个）。
const BINARY_MARK = 'bizResp 正当豁免：二进制'

/** 数"带着二进制标记的导出函数"：标记必须出现在该 export 声明之前，且中间不夹另一个 export。 */
function binaryMarkedExports(raw: string): string[] {
  const hits: string[] = []
  const re = /export async function ([A-Za-z0-9_]+)/g
  let m: RegExpExecArray | null
  let last = 0
  while ((m = re.exec(raw))) {
    if (raw.slice(last, m.index).includes(BINARY_MARK)) hits.push(m[1])
    last = m.index
  }
  return hits
}

function productBizRespFailures(src: string): string[] {
  const bad: string[] = []
  const code = stripComments(src)
  const lines = code.split('\n')
  lines.forEach((raw, i) => {
    const t = raw.trim()
    if (/^return\s+(?:await\s+)?request[<(]/.test(t)) bad.push(`第 ${i + 1} 行裸返 request（未包 bizResp）`)
    if (t.includes('await request(') && !t.includes('bizResp')) bad.push(`第 ${i + 1} 行裸用 await request`)
  })
  const fnCount = (code.match(/export async function /g) || []).length
  const wrapCount = (code.match(/return bizResp\(/g) || []).length
  const binary = binaryMarkedExports(src)
  if (fnCount === 0) bad.push('一个导出函数都没读到（锚点漂了，本锁失明）')
  if (wrapCount + binary.length !== fnCount) {
    bad.push(`导出函数 ${fnCount} 个＝bizResp ${wrapCount} 处＋二进制档 ${binary.length} 个（${binary.join('/') || '无'}）对不上 ⇒ 有信封调用漏包`)
  }
  // 带标记的那个函数**必须真的没包 bizResp**：既标豁免又走信封＝把标记当挡箭牌，判红。
  for (const name of binary) {
    const at = src.indexOf('export async function ' + name)
    const body = src.slice(at, at + 1200)
    if (/return bizResp\(/.test(body.slice(0, body.indexOf('\n}') + 1))) {
      bad.push(name + ' 带着二进制标记却又包了 bizResp ⇒ 标记被当挡箭牌')
    }
  }
  return bad
}

/** 豁免档判据：bizRespGate.test.ts 的四张表里不许出现 product.ts（新文件走主判据，不登记豁免） */
function exemptionFailures(gateSrc: string): string[] {
  const code = stripComments(gateSrc)
  if (code.includes("'product.ts'") || code.includes('"product.ts"')) {
    return ['product.ts 出现在 bizRespGate.test.ts 的豁免表定义里（新文件必须走主判据，不进任何豁免档）']
  }
  return []
}

const productSrc = readFileSync(PRODUCT, 'utf-8')
const gateSrc = readFileSync(GATE, 'utf-8')

describe('api/product.ts 的 bizResp 接线（一期新文件走主判据）', () => {
  it('每个信封调用都被 bizResp 包，且零裸返点位', () => {
    expect(productBizRespFailures(productSrc), '接口层失败必须经 bizResp 收敛：\n').toEqual([])
  })

  it('射程自证：读到的确是 product.ts 本体，且导出函数数量有下界', () => {
    expect(productSrc).toContain('export async function productList')
    expect((stripComments(productSrc).match(/export async function /g) || []).length).toBeGreaterThanOrEqual(10)
  })

  it('不进任何豁免档：四张表里都没有 product.ts', () => {
    expect(exemptionFailures(gateSrc)).toEqual([])
  })

  it('反证 A：把一处 bizResp 摘成裸返 request —— 必须判红', () => {
    const mutated = productSrc.replace(
      "return bizResp(() => request('/api/product/list'",
      "return request('/api/product/list'")
    expect(mutated, '反证失效：变异没落地（替换字符串没命中）').not.toBe(productSrc)
    expect(productBizRespFailures(mutated).length, '裸返没被判红').toBeGreaterThan(0)
    expect(productBizRespFailures(productSrc), '对照组：真实实现必须干净').toEqual([])
  })

  it('反证 C：把二进制档的标记摘掉 —— 必须判红（豁免靠的是标记，不是文件进了白名单）', () => {
    const mutated = productSrc.replace(BINARY_MARK, '（标记已摘）')
    expect(mutated, '反证失效：标记没落地').not.toBe(productSrc)
    expect(productBizRespFailures(mutated).length, '摘掉标记没被判红').toBeGreaterThan(0)
    expect(productBizRespFailures(productSrc), '对照组：真实实现必须干净').toEqual([])
  })

  it('反证 B：把 product.ts 登记进 EXEMPT 豁免表 —— 必须判红', () => {
    const mutated = gateSrc.replace(
      'const EXEMPT: Record<string, string> = {',
      "const EXEMPT: Record<string, string> = {\n  'product.ts': 'bizResp 豁免',")
    expect(mutated, '反证失效：变异没落地').not.toBe(gateSrc)
    expect(exemptionFailures(mutated).length, '登记进豁免表没被判红').toBeGreaterThan(0)
    expect(exemptionFailures(gateSrc), '对照组：真实门禁必须干净').toEqual([])
  })
})
