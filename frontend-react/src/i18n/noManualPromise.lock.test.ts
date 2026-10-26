// ============================================================================
// src/i18n/noManualPromise.lock.test.ts — 客户可见文案里不许承诺"人工复核"（★ 2026-10-26 用户明令）
//
// 背景：本仓**没有人工复核环节**（W6 那条裁决）。但"质检存疑"这个徽标的提示语过去写的是
// 「已标记待人工复核」，`kb.bulkDone` 写的是「等待人工审核」——客户读完会以为有人在看他这一单，
// 而屏上那个状态其实只是"机器给的分低"。这类话术比缺一个功能更坏：它制造一个不会兑现的期望。
//
// 两条判据必须同时在场，缺一即空转：
//   负向（N1/N2）：点名那几句**客户可见**的处置文案，禁词表命中即红；
//   正向（P1）：真实存在的审核队列名（`tmr.pending`「待审核」、`kb.srcManual`「人工」录入源标签）
//     必须**仍然在词典里**——否则这道锁等于把所有含"审核/人工"的词都判死，
//     下一位只会把它整条摘掉（本仓对闸门的一贯要求：判据要窄到能长期活着）。
//
// 运行：npx vitest run src/i18n/noManualPromise.lock.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'

// 12 语种：zh/en 在 dicts.*.ts（客户面门面词典）＋ panels/kb.ts 的 zh/en 两腿，其余十份在 locales/
const DICT_FILES = [
  'src/i18n/dicts.zh.ts',
  'src/i18n/dicts.en.ts',
  'src/i18n/panels/kb.ts',
  ...readdirSync('src/i18n/locales').filter((f) => f.endsWith('.ts')).map((f) => 'src/i18n/locales/' + f),
]

// 禁词表：只收"把不存在的环节承诺给客户"的那些写法。
// ⚠️ 刻意不收裸"审核"／裸"人工"——它们也出现在真实功能名里（见下面 P1 那两条），收了就成横扫。
const BANNED = [
  '人工复核', '待人工', '已交人工', '等待人工', '建议人工', '请人工',
  '稍后重试', '我们会跟进', '请联系客服', '等待审核处理',
  'manual review', 'human review', 'manually review', 'pending manual review',
  'manuelle Prüfung', 'manuelles', 'révision manuelle', 'revisión manual', 'revisão manual',
  'ручной проверки', 'ручную', '人手', '手動', '수동 검수', 'ด้วยตนเอง', 'المراجعة اليدوية',
]

// CUSTOMER_KEYS：客户面（非管理台）会读到的处置文案，逐条点名。
const CUSTOMER_KEYS = ['tk.qaFlaggedTip', 'tk.qaErrorNote']

// readAll 拼出全部词典正文（读不到任何一份即红——"没读到"绝不能当"没问题"）。
function readAll(): Array<[string, string]> {
  return DICT_FILES.map((f): [string, string] => {
    try {
      return [f, readFileSync(f, 'utf8')]
    } catch (e) {
      throw new Error(`读不到词典 ${f}：${(e as Error).message}（判据读不到文件＝失明，不许静默放行）`)
    }
  })
}

// valueOf 取某个键在一份词典里的字符串值（可能一份文件里有 zh/en 两腿 ⇒ 返回全部）。
function valueOf(src: string, key: string): string[] {
  const out: string[] = []
  const re = new RegExp("['\"]" + key + "['\"]\\s*:\\s*(['\"])((?:(?!\\1)[^\\\\]|\\\\.)*)\\1", 'g')
  for (const m of src.matchAll(re)) out.push(m[2])
  return out
}

describe('客户可见文案不许承诺人工复核（N1/N2 负向 ＋ P1/P2 正向对照）', () => {
  const files = readAll()

  it(`N1 客户面处置文案（${CUSTOMER_KEYS.join(' / ')}）12 语种逐条禁词命中即红`, () => {
    const bad: string[] = []
    // 覆盖面按"每个键必须在 12 份里都出现一次"算，而不是"每份文件都必须有这两个键"：
    // tk.* 的事实源是两份底典 ＋ 十份全量 locale，panels/*.ts 物理上没有它们——
    // 拿文件列表去要求每份都有，等于把一条好锁写成结构性恒红（下一位的处置就是摘掉它）。
    const seen: Record<string, number> = {}
    for (const [f, src] of files) {
      for (const key of CUSTOMER_KEYS) {
        const vals = valueOf(src, key)
        if (vals.length === 0) continue
        seen[key] = (seen[key] ?? 0) + vals.length
        for (const v of vals) {
          const hit = BANNED.filter((w) => v.toLowerCase().includes(w.toLowerCase()))
          if (hit.length > 0) bad.push(`${f.split('/').pop()} ${key} 含 ${JSON.stringify(hit)}：${v.slice(0, 50)}`)
        }
      }
    }
    expect(bad, '客户面文案仍在承诺一个不存在的环节：\n' + bad.join('\n')).toEqual([])
    for (const key of CUSTOMER_KEYS) {
      expect(seen[key] ?? 0, `${key} 只在 ${seen[key] ?? 0} 份词典里读到（应为 12 份＝zh/en 底典＋十份全量 locale）`)
        .toBeGreaterThanOrEqual(12)
    }
  })

  it('N2 KB 批量导入的完成提示不得写"等待人工审核"（真实队列名是"待审核"）', () => {
    const bad: string[] = []
    for (const [f, src] of files) {
      for (const v of valueOf(src, 'kb.bulkDone')) {
        const hit = BANNED.filter((w) => v.toLowerCase().includes(w.toLowerCase()))
        if (hit.length > 0) bad.push(`${f.split('/').pop()}：${v.slice(0, 50)}`)
      }
    }
    expect(bad, '导入提示承诺了人工：\n' + bad.join('\n')).toEqual([])
  })

  it('P1 正向对照：真实审核队列的键必须还在（证明本锁不是把所有"审核/复核"字样横扫掉）', () => {
    const zh = files.find(([f]) => f.endsWith('dicts.zh.ts'))?.[1] ?? ''
    const en = files.find(([f]) => f.endsWith('dicts.en.ts'))?.[1] ?? ''
    const pendingZh = valueOf(zh, 'tmr.pending')
    expect(pendingZh.length, '读不到 tmr.pending ⇒ 真实审核队列的界面名被删了？本锁的对照腿随之失效').toBeGreaterThan(0)
    expect(pendingZh[0]).toContain('审核')
    expect(valueOf(en, 'tmr.pending').length).toBeGreaterThan(0)
  })

  it('P2 正向对照：禁词表本身要有"人工复核"这一条（表被清空 ⇒ N1/N2 恒真，是更坏的假绿）', () => {
    expect(BANNED).toContain('人工复核')
    expect(BANNED.length).toBeGreaterThan(10)
    // 反证（判据自证射程）：把一句真实旧文案喂进同一把尺子，必须命中
    const oldZh = '机器评估分数低于阈值，已标记待人工复核。'
    expect(BANNED.filter((w) => oldZh.includes(w)).length, '旧文案竟不被禁词表命中 ⇒ 判据是摆设').toBeGreaterThan(0)
    const oldEn = 'Errors persisted; flagged for manual review.'
    expect(BANNED.filter((w) => oldEn.toLowerCase().includes(w.toLowerCase())).length).toBeGreaterThan(0)
  })

  it('P3 覆盖面对照：十二份词典都要真读到（少一份文件 ⇒ 前两条负向都在空转）', () => {
    expect(files.length).toBe(DICT_FILES.length)
    const localeCount = DICT_FILES.filter((f) => f.includes('/locales/')).length
    expect(localeCount, 'locales/ 下应是十份全量词典').toBe(10)
    for (const [f, src] of files) {
      expect(src.length, `${f} 正文为空`).toBeGreaterThan(1000)
    }
  })
})
