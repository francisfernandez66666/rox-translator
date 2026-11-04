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
// ★ 2026-11-03 复核批补**后端射程腿**（B1/B2/B3）：
// 这条锁过去只扫前端 12 份词典，而真正的病文本体住在一个 **Go 硬编码常量**里——
// `engine.sensitiveReplyBlocked` 曾写着「已拒绝翻译并转人工复核通道」，它被直接送回客户气泡，
// 却因为"后端不在本锁的射程内"而静默活了很久。同族的还有两条交付面 Lead
// （`SensitiveSkippedLead` / `SensitiveBlockedLeadForFile`），本批新增，也必须一并纳管。
// ⇒ 判据不是"再抄一份期望值"，而是**从 Go 源码里抽出那三个常量的真值**再喂同一把尺子：
// 抄期望值的话，改常量不会红（锁读的是副本），那正是"锁跟实现一起漂"的形态。
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

// ---------------------------------------------------------------------------
// 后端射程腿（B1/B2/B3）：客户可见的**Go 硬编码常量**也必须过同一把尺子。
// ---------------------------------------------------------------------------

// BACKEND_DICT 三条常量的事实源（相对 frontend-react 工作目录）。
const BACKEND_GO = '../backend-go/internal/engine/sensitive.go'

// 三条常量的名字。前两条是交付面 Lead（.md 尾节 / 前端详情区），
// 第三条是对话与文本通道的**统一拒译话术**（直接进客户气泡）。
// ⚠️ 第三条是小写未导出常量：它是包内事实源，不许因为"不是导出的"就漏出射程。
const BACKEND_CONSTS = [
  { name: 'SensitiveSkippedLead', what: '工单/文件通道「合规跳过」的处置说明' },
  { name: 'SensitiveBlockedLeadForFile', what: '文件通道「合规跳过」的处置说明（交付位是占位符）' },
  { name: 'sensitiveReplyBlocked', what: '对话/文本通道的统一拒译话术（直接进客户气泡）' },
]

// constValueOf 从一份 Go 源码里抽出 `<name> = "..."[ + "..." ...]` 的字符串真值。
//
// 为什么不用正则一把抓：`sensitiveReplyBlocked` 是**跨行拼接**（`"…" +\n\t"…"`），
// 单行正则只能拿到前半句，于是"禁词恰好在后半句"那一档会静默放行（判据射程少一半）。
// 这里逐字符扫：只接受「空白 / `+` / 字符串字面量」三种记号，遇到别的就收手——
// 既支持跨行，又不会一路吞到下一个声明里。
function constValueOf(src: string, name: string): string {
  const re = new RegExp('(?:^|\\s)(?:const\\s+)?' + name + '\\s*=\\s*')
  for (const m of src.matchAll(new RegExp(re.source, 'g'))) {
    // 跳过注释里的提及：注释里写着"这个常量曾经是什么"是必要的（本仓要求把理由写在代码旁），
    // 拿它当声明会把注释读成实现（同类自伤在本仓已踩过多次）。
    const lineStart = src.lastIndexOf('\n', m.index!) + 1
    if (src.slice(lineStart, m.index).includes('//')) continue

    let i = (m.index ?? 0) + m[0].length
    const parts: string[] = []
    while (i < src.length) {
      while (i < src.length && /\s/.test(src[i])) i++
      if (src[i] === '"') {
        let j = i + 1
        let buf = ''
        while (j < src.length && src[j] !== '"') {
          if (src[j] === '\\') {
            buf += src[j] + (src[j + 1] ?? '')
            j += 2
            continue
          }
          buf += src[j]
          j++
        }
        parts.push(buf)
        i = j + 1
        continue
      }
      if (src[i] === '+') {
        i++
        continue
      }
      break
    }
    return parts.join('')
  }
  return ''
}

describe('客户可见文案不许承诺人工复核（后端硬编码常量 B1/B2/B3）', () => {
  // 读不到就判红：路径变了而锁静默变空，是本仓最不能接受的一类失效（比红灯更坏）。
  let src = ''
  try {
    src = readFileSync(BACKEND_GO, 'utf8')
  } catch (e) {
    throw new Error(
      `读不到后端事实源 ${BACKEND_GO}：${(e as Error).message}\n` +
        '⇒ 这条锁的射程覆盖后端常量，读不到就等于放行（不许静默跳过）',
    )
  }

  const values = BACKEND_CONSTS.map((c) => ({ ...c, value: constValueOf(src, c.name) }))

  it('B1 三条后端常量的真值都能抽出来（抽不到 ⇒ 后面两条负向全在空转）', () => {
    // 扫描面自证：文件要真是那一份（几句话的文件被换成别的，抽出来的"老实"毫无意义）。
    expect(src.length, `${BACKEND_GO} 正文过短 ⇒ 扫错文件了`).toBeGreaterThan(1000)
    const missing = values.filter((v) => v.value === '').map((v) => v.name)
    expect(missing, `这些常量没抽到值（改名/改形态会让 B2 恒绿）：${missing.join(', ')}`).toEqual([])
    // 三条都必须带同一个前缀：这才是"抽到的是那条客户可见话术"的证据，
    // 而不是抽到了一段恰好不含禁词的别的东西。
    for (const v of values) {
      expect(v.value, `${v.name}（${v.what}）抽到的内容不像合规话术：${JSON.stringify(v.value)}`).toContain(
        '内容合规审核',
      )
    }
  })

  it('B2 三条后端常量 12 语种禁词表命中即红（后端不再游离于本锁之外）', () => {
    const bad: string[] = []
    for (const v of values) {
      const hit = BANNED.filter((w) => v.value.toLowerCase().includes(w.toLowerCase()))
      if (hit.length > 0) bad.push(`${v.name} 含 ${JSON.stringify(hit)}：${v.value.slice(0, 80)}`)
    }
    expect(
      bad,
      '后端硬编码的客户可见话术仍在承诺一个不存在的环节（本仓无人工复核）：\n' + bad.join('\n'),
    ).toEqual([])
  })

  it('B3 判据自证：病文本体喂进同一把尺子必须命中（否则 B2 是空转的）', () => {
    // 这句正是 `sensitiveReplyBlocked` 曾经的真值（2026-11-03 复核批改写前的原文）。
    // 它必须能被 BANNED 命中——命中不了说明禁词表已经和这条缺陷脱钩，
    // 那时 B2 的"零命中"只是"尺子够不到"，不是"文案干净"。
    const oldWording = '⚠️ 内容合规审核：本次请求包含平台不予受理的内容，已拒绝翻译并转人工复核通道。'
    expect(
      BANNED.filter((w) => oldWording.toLowerCase().includes(w.toLowerCase())).length,
      '旧拒译话术竟不被禁词表命中 ⇒ 后端射程腿是摆设（这条锁的成立前提就是"它抓得住本体"）',
    ).toBeGreaterThan(0)
    // 反向对照：改写后的真值必须**不**命中（判据不能宽到把合规文案本身也判死）。
    const nowWording = values.find((v) => v.name === 'sensitiveReplyBlocked')?.value ?? ''
    expect(
      BANNED.filter((w) => nowWording.toLowerCase().includes(w.toLowerCase())).length,
      `改写后的拒译话术被自己的禁词表误伤：${nowWording}`,
    ).toBe(0)
  })
})
