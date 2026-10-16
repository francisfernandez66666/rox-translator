// ============================================================================
// src/components/Landing.demoCard.lock.test.ts — Hero 演示卡「跟着卡片宽度缩放」口径锁
// （★ 2026-10-16 用户报障：屏幕变小后动效超格子——术语行的正译词顶出卡片右缘被 overflow:hidden 裁掉）
//
// 缺陷形态：卡片宽度由 hero 栅格决定，**与视口不成正比**（1280 视口下卡片只有 560px，1440 以上才是满 720px），
// 而旧写法用 vw 做 clamp ⇒ 视口一越过 720，所有度量都钉在上限、卡片却还在变窄，横向必然溢出
// （Playwright 实测 1280：正译词越出卡缘 131px，700：57px；同一份 src 在 1440 以上完全正常）。
// 修法＝把卡片自身声明成容器（container-type:inline-size），内部度量改 cqw（1cqw = 卡片宽/100），
// 每个中项都按 720 设计宽反推（165/720 = 22.917%）⇒ 卡片满 720 时取值与改造前的 vw 写法**逐字等值**。
//
// 本文件钉住修完的那一侧（四项都必须成立，缺一项锁就是空的）：
//   P1 射程自证：能从 Landing.tsx 切出 hd 卡 CSS 段（长度下界）——切不到就是锚点漂了、判据已失明，
//      这一条要**判红**而不是静默放行（同 §三「度量腿必须先自证射程」）。
//   P2 正向等值：**逐选择器**钉住 720 设计宽下的 clamp 字面（不是"段里出现过就算"——
//      窄屏媒体块会重复同一批中项，按段判的话主规则被改坏它照样绿，本文件首版就这么假绿过一次）。
//   P3 负向清零：hd 段内不许再出现 vw（视口基准是这条缺陷的根，改回它必须红）。
//   P4 容器腿：`.hd` 必须声明 container-type:inline-size——没有它 cqw 会退回按小视口解析，
//      P3 就算"零 vw"也照样溢出（这条是"负向锁必须配正向对照"的那个对照）。
//   R1 反证：把 cqw 换回 vw 的变异文本喂给同一个判据必须被抓到（否则 P3 可能是空转）。
//
// ⚠️ 判据只问 CSS 段本体：说明注释里刻意写着 vw/cqw 的来龙去脉（AGENTS 要求写理由），
//    所以扫描前先剥注释，别把历史记录读成违规写法。
// 运行：npx vitest run src/components/Landing.demoCard.lock.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const LANDING = path.join(HERE, 'Landing.tsx')

// 剥注释：先块注释再行注释。CSS 段里没有裸 URL，这条保守剥法够用；
// 宁可少剥也不许把正文剥掉——剥过头会让负向锁"看起来更干净"＝假绿方向。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

// 切 hd 卡 CSS 段：从 Hero 演示卡的横幅注释起，到 CSS 模板串收尾的最后一个反引号止。
function hdBlock(src: string): string {
  const start = src.indexOf('================= Hero 演示卡')
  const end = src.lastIndexOf('\n`')
  if (start < 0 || end <= start) return ''
  return stripComments(src.slice(start, end))
}

// 段内某选择器的声明块：本仓 CSS 一条规则一行，取首个「行首即该选择器 + {」的行。
function ruleLine(block: string, sel: string): string {
  return block.split('\n').map((l) => l.trim()).find((l) => l.startsWith(`${sel}{`)) ?? ''
}

// 逐选择器钉住的 720 设计宽口径（中项 cqw 按 720 反推，上限＝交付档字面值）。
// 右侧每一项都必须原样出现在该选择器自己的声明块里——所以改坏主规则时不会被子选择器/媒体块的重复值掩护。
const DESIGN: Array<[string, string[]]> = [
  ['.hd-w', ['width:clamp(74px,22.917cqw,165px)', 'font-size:clamp(21px,5.278cqw,38px)', 'line-height:clamp(32px,7.778cqw,56px)']],
  ['.hd-r', ['font-size:clamp(30px,6.944cqw,50px)', 'line-height:clamp(42px,8.333cqw,60px)']],
  ['.hd-cn', ['width:clamp(78px,12.778cqw,92px)', 'font-size:clamp(14px,2.361cqw,17px)']],
  ['.hd-wt.typing::after', ['height:clamp(19px,4.167cqw,30px)']],
  ['.hd-r.typing::after', ['height:clamp(26px,5.833cqw,42px)']],
]

const vwHits = (code: string): string[] =>
  code.split('\n').filter((l) => /\d(?:\.\d+)?vw\b/.test(l)).map((l) => l.trim().slice(0, 80))

describe('Hero 演示卡：内部度量按卡片宽度（cqw）缩放', () => {
  const raw = readFileSync(LANDING, 'utf8')
  const block = hdBlock(raw)

  it('P1 射程自证：hd 卡 CSS 段必须切得到（切不到＝锚点漂了、下面几条会静默空转）', () => {
    expect(block.length, 'hd 演示卡 CSS 段过短，判据已失明').toBeGreaterThan(5000)
    expect(block).toContain('.hd-panel')
    expect(block).toContain('.hd-r')
  })

  it('P2 正向等值：逐选择器钉住 720 设计宽下的 clamp 字面', () => {
    const missing: string[] = []
    for (const [sel, decls] of DESIGN) {
      const line = ruleLine(block, sel)
      if (!line) { missing.push(`${sel}：规则整条不见了`); continue }
      for (const d of decls) if (!line.includes(d)) missing.push(`${sel} 缺 ${d}`)
    }
    expect(missing, '演示卡口径被改动（720 设计宽下的取值必须与交付档逐字等值）').toEqual([])
  })

  it('P2b 正向等值：≤640 窄屏媒体块的两条覆盖仍在（它们也吃 cqw 中项）', () => {
    expect(block).toContain('.hd-w{font-size:clamp(17px,5.278cqw,38px);line-height:clamp(26px,7.778cqw,56px)}')
    expect(block).toContain('.hd-r{font-size:clamp(24px,6.944cqw,50px);line-height:clamp(34px,8.333cqw,60px)}')
  })

  it('P3 负向清零：hd 段内不得再出现 vw（视口基准＝这条缺陷的根因）', () => {
    expect(vwHits(block), 'hd 演示卡又回到了视口基准，窄屏必然溢出卡缘').toEqual([])
  })

  it('P4 容器腿：.hd 必须声明 container-type:inline-size（否则 cqw 退回视口解析，P3 形同虚设）', () => {
    expect(ruleLine(block, '.hd'), '.hd 未声明容器，cqw 会按小视口解析').toContain('container-type:inline-size')
  })

  it('R1 反证：把 cqw 换回 vw 必须被 P3 抓到（负向锁不许空转）', () => {
    const mutated = block.replace(/cqw/g, 'vw')
    expect(mutated, '变异体没改动任何字符，说明 R1 是空替换（假反证）').not.toBe(block)
    expect(vwHits(mutated).length, 'cqw→vw 的变异没有被抓到').toBeGreaterThan(0)
  })

  it('R2 反证：把主规则的 50px 上限改坏必须被 P2 抓到（按选择器判，不许被媒体块重复值掩护）', () => {
    const mutated = block.replace(
      '.hd-r{font-family:var(--lc-font-latin);font-size:clamp(30px,6.944cqw,50px)',
      '.hd-r{font-family:var(--lc-font-latin);font-size:clamp(30px,6.944cqw,44px)',
    )
    expect(mutated, '变异体没落地，R2 是空替换').not.toBe(block)
    const line = ruleLine(mutated, '.hd-r')
    expect(line.includes('font-size:clamp(30px,6.944cqw,50px)'), '主规则被改坏却没有被抓到').toBe(false)
  })
})
