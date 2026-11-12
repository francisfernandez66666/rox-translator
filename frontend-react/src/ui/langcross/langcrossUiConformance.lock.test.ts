// ============================================================================
// langcrossUiConformance.lock.test.ts — 销售域页面「统一用能言 UI」机械闸（三期 W1 / A01）
//
// 判据：扫描面由目录枚举 ⇄ 显式 SCOPE 双向比对（多出/缺失即红，防扫描集悄悄变空＝恒绿）；
//       去注释后禁止裸 <button|table|input|select|textarea|form>、style={{}}、硬编码
//       #RRGGBB、--adm-/--npz- 别名直写；每个文件必须命中统一组件源 import。
//
// ★ 唯一例外（点名、带理由、配反证）：ProductPublicPage.tsx 的原生
//   <form action="/api/lead/public" method="post"> 是公开留资契约——后端只认表单 POST，
//   且公开面不得挂 JS 拦截（SEO/受控字段口径见 个人对外沟通方案/README.md:28-40）。
//   例外只对该文件、该属性组合生效；其他文件出现 form 一律判红。
//
// 反向对照用内存字符串（等价于临时文件样本，避开本机沙盒临时目录清理限制）。
// 运行：cd frontend-react && npx vitest run src/ui/langcross/langcrossUiConformance.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

// 本文件位于 src/ui/langcross/ 下，退两级才是 src/
const SRC = fileURLToPath(new URL('../..', import.meta.url))

/** 已收敛页面全集（W1 九页 + SalesAreaTable 抽取件 + W3/W6 四面板；新增销售域页面必须同步登记，否则双向比对判红） */
const SCOPE = [
  'components/admin/AssistP.tsx',
  'components/admin/PersonasP.tsx',
  'components/admin/SalesConfigP.tsx',
  'components/admin/SalesInboxP.tsx',
  'components/admin/SalesLeadsP.tsx',
  'components/admin/SalesOpsP.tsx',
  'components/admin/SalesRolesP.tsx',
  'components/admin/sales/SalesAreaTable.tsx',
  'components/ProductAnswers.tsx',
  'components/ProductDetail.tsx',
  'components/ProductFacts.tsx',
  'components/ProductPublicPage.tsx',
  'components/ProductShelf.tsx',
  'components/SalesDrafts.tsx',
].sort()

const EXTRA = ['components/admin/AssistP.tsx', 'components/admin/PersonasP.tsx']

function enumerate(dir: string, re: RegExp): string[] {
  return readdirSync(join(SRC, dir))
    .filter((f) => re.test(f) && f.endsWith('.tsx'))
    .map((f) => `${dir}/${f}`)
}

function enumerated(): string[] {
  return [
    ...enumerate('components', /^(Sales|Product)/),
    ...enumerate('components/admin', /^Sales/),
    ...enumerate('components/admin/sales', /^Sales/),
    ...EXTRA,
  ].sort()
}

/** 去块注释与行注释：公开页注释里写了 <form action=...> 的契约说明，不能算违规 */
function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|\s)\/\/[^\n]*/g, '$1')
}

const NATIVE_FORM_OK = (code: string) =>
  /<form[^>]*action="\/api\/lead\/public"[^>]*method="post"/.test(code)

const BARE_TAG = /<(button|table|input|select|textarea|form)(?=[\s>/])/g

/** 统一组件源：@/ui/langcross、其 /src 子路径，或后台面板的 parts.tsx */
const IMPORT_RE = /from\s+['"](?:@\/ui\/langcross(?:\/src)?|[^'"]*\/parts)['"]/

function violations(src: string, rel: string): string[] {
  const code = stripComments(src)
  const out: string[] = []
  BARE_TAG.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = BARE_TAG.exec(code))) {
    const tag = m[1]
    if (tag === 'form' && rel.endsWith('ProductPublicPage.tsx') && NATIVE_FORM_OK(code)) continue
    out.push(`bare <${tag}>`)
  }
  if (/style=\{\{/.test(code)) out.push('inline style')
  if (/#[0-9A-Fa-f]{6}\b/.test(code)) out.push('hardcoded color')
  if (/--(?:adm|npz)-/.test(code)) out.push('legacy token alias')
  return out
}

function hasComponentImport(src: string): boolean {
  return IMPORT_RE.test(src)
}

const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

describe('能言 UI 归一（销售域页面全集）', () => {
  it('扫描面由目录枚举 ⇄ SCOPE 双向一致，射程非空', () => {
    expect(SCOPE.length).toBeGreaterThanOrEqual(9)
    expect(enumerated()).toEqual(SCOPE)
  })

  it('每个页面都用统一组件源，且无裸标签 / 内联样式 / 硬编码色 / 别名令牌', () => {
    const bad: string[] = []
    for (const rel of SCOPE) {
      const src = read(rel)
      if (!hasComponentImport(src)) bad.push(`${rel}: 未 import 统一组件源`)
      for (const v of violations(src, rel)) bad.push(`${rel}: ${v}`)
    }
    expect(bad).toEqual([])
  })

  it('公开留资 form 例外只此一处，且例外不得泛化', () => {
    const publicPage = read('components/ProductPublicPage.tsx')
    expect(violations(publicPage, 'components/ProductPublicPage.tsx')).toEqual([])
    // 同样的 form 出现在别的销售页面必须判红
    const elsewhere = `<form action="/api/lead/public" method="post"><span>x</span></form>`
    expect(violations(elsewhere, 'components/SalesDrafts.tsx')).toContain('bare <form>')
    // 例外必须属性齐全：去掉 method 即失效
    const stripped = `<form action="/api/lead/public">x</form>`
    expect(violations(stripped, 'components/ProductPublicPage.tsx')).toContain('bare <form>')
  })

  it('反向对照：违规样本必红、合法样本必放行、parts-only 必放行', () => {
    const badCase = `export default function P(){ return <button className="x">go</button> }`
    expect(violations(badCase, 'bad.tsx')).toContain('bare <button>')
    expect(hasComponentImport(badCase)).toBe(false)

    const goodCase = `import { Button } from '@/ui/langcross/src'\nexport default function P(){ return <Button>go</Button> }`
    expect(violations(goodCase, 'good.tsx')).toEqual([])
    expect(hasComponentImport(goodCase)).toBe(true)

    // 后台面板的 Panel/Field 来自 parts.tsx，不在 ui/langcross 出口；只认前者会误判红
    const partsCase = `import { Panel } from './parts'\nexport default function P(){ return <Panel title="x">ok</Panel> }`
    expect(violations(partsCase, 'parts-only.tsx')).toEqual([])
    expect(hasComponentImport(partsCase)).toBe(true)

    const styled = `import { Button } from '@/ui/langcross/src'\nexport default function P(){ return <Button style={{ color: '#E7E9EA' }}>x</Button> }`
    expect(violations(styled, 'styled.tsx')).toEqual(
      expect.arrayContaining(['inline style', 'hardcoded color']),
    )
  })
})
