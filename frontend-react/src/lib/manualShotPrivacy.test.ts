// ============================================================================
// src/lib/manualShotPrivacy.test.ts — 手册截图隐私闸的判据（E 组，2026-11-06）
//
// 这把闸存在的唯一理由是：**带截图的手册一旦发出去就收不回**。
// 所以这里的用例分成两半，缺一不可：
//   正向（P 段）＝每一种真实泄漏形态都必须被抓到；
//   反向（N 段）＝夹具假值与界面固定文案必须**不误杀**，否则采集脚本会一路红、
//                  最后被人随手放宽——那比没有闸更坏（AGENTS 那条"锁的归宿是被摘掉"）。
// 另配 S 段自证：判据真在扫文本（空文本判红＝不许拿骨架屏冒充章节截图）。
//
// 运行：npx vitest run src/lib/manualShotPrivacy.test.ts
// ============================================================================
import { describe, expect, it } from 'vitest'
import { FIXTURE_ALLOW, formatVerdict, judgeShotPrivacy, scanShotText } from './manualShotPrivacy'

/** 一段"干净的"夹具页面文字：只含界面固定文案 ＋ 白名单里那一个邮箱 */
const CLEAN = [
  '产品详情', '基本信息', '产品名称', '目标语言', '允许被搜索引擎收录',
  '示例整机（演示数据）', 'manual-clip@example.test', '下一步', '稍后再说',
].join('\n')

describe('手册截图隐私闸（E 组）', () => {
  // ── P 段：真实泄漏形态必须被抓 ──────────────────────────────────────
  it.each([
    ['email', '成员邮箱 zhang.wei@real-company.com 在列'],
    ['phone', '联系电话 +86 138 1234 5678'],
    ['long-number', '本月已用 积分 12345678 余额'],
    ['grouped-number', '本月已用 积分 1,234,567 余额'],
    ['page-code', '对外页 /p/a1b2c3d4e5f6a7b8/c9d8e7f6a5b4c3d2'],
    ['credential', '密钥 sk_live_abcdefgh12345678'],
    ['credential', 'Authorization: Bearer eyJhbGciOi.abcdef.SIGNATURE1'],
  ])('P 段｜%s 必须抓到真实形态', (rule, text) => {
    const hits = scanShotText(text)
    expect(hits.length, `未抓到：${text}`).toBeGreaterThan(0)
    expect(hits.some((h) => h.rule === rule), `抓到了但不是这条规则：${hits.map((h) => h.rule).join(',')}`).toBe(true)
    // 合法展示串不许被同一份文本顺带误杀（日期、百分比在这类文本里常与数字混排）
  })

  it('P 段｜命中必须**逐条点名**（规则＋原值＋上下文），只回 pass/fail 等于没做', () => {
    const v = judgeShotPrivacy('客户邮箱 lin@customer.cn 与流水 87654321')
    expect(v.pass).toBe(false)
    expect(v.hits.length).toBeGreaterThanOrEqual(2)
    const one = v.hits[0]
    expect(one.value.length, '命中项没带原值，运维无法定位是哪一格').toBeGreaterThan(0)
    expect(one.around, '命中项没带上下文，看不出在画面哪里').toContain(one.value)
    const line = formatVerdict(v, '05-工单', 'zh')
    expect(line).toContain('FAIL|shot 05-工单/zh')
    expect(line).toContain('隐私闸')
  })

  // ── N 段：夹具与固定文案不许误杀 ────────────────────────────────────
  it('N 段｜夹具假值白名单必须放行（不许因为"长得像邮箱/中文带括号"就判红）', () => {
    const v = judgeShotPrivacy(CLEAN, ['基本信息', '产品名称', '目标语言'])
    expect(v.hits.map((h) => `${h.rule}=${h.value}`), `夹具页面被自己判红了：${v.hits.map((h) => h.value).join(',')}`).toEqual([])
    expect(v.pass).toBe(true)
  })

  it('N 段｜白名单是**精确等值**，不是子串（把真号藏进长串里不许蒙过去）', () => {
    const sneaky = 'xmanual-clip@example.test.attacker.net'
    const hits = scanShotText(sneaky)
    expect(hits.length, '夹具串被当成子串放行 ⇒ 白名单判据写成了 includes').toBeGreaterThan(0)
    expect(hits[0].rule).toBe('email')
  })

  it('N 段｜allowTexts 放行的是**命中值本身**相等，不是"这行文案相等"（语义钉死，防成泄漏后门）', () => {
    const text = '账单流水 12345678'
    // 允许集里写整行 ⇒ 不许放行：命中值是 "12345678"，那一行只是恰好包含它。
    //   如果这里放行，采集脚本就可以靠"把整页文字抄进 allowTexts"把闸彻底关成常绿——那是本文件最危险的放宽方向。
    expect(judgeShotPrivacy(text, ['账单流水 12345678']).pass, '整行相等就放行 ⇒ allowTexts 会变成泄漏后门').toBe(false)
    expect(judgeShotPrivacy(text, ['账单']).pass, '部分相等不许放行').toBe(false)
    // 只有把**那一串数字本身**登记成固定文案（例如界面上写死的示例值）才放行
    expect(judgeShotPrivacy(text, ['12345678']).pass, '命中值精确相等应当放行').toBe(true)
  })

  it('P 段｜千分位余额必须抓到（首跑真实漏过的那一种：界面余额永远带逗号）', () => {
    for (const text of ['本月已用 1,234,567 积分', '余额 12 345 678 元', '余额 $1,234,567.89']) {
      const hits = scanShotText(text)
      expect(hits.length, `漏抓：${text}`).toBeGreaterThan(0)
      expect(hits.some((h) => h.rule === 'grouped-number' || h.rule === 'long-number'), `抓到了但不是数字那条：${text}`).toBe(true)
    }
  })

  it('P 段｜合法短数字不许误杀（年份／档位／百分比都不该被判成余额）', () => {
    for (const ok of ['2026 年', '支持 40+ 语言', '约 5 折', '10 分钟', '2026-11-06 12:31']) {
      expect(scanShotText(ok).length, `误杀：${ok}`).toBe(0)
    }
  })

  // ── S 段：判据射程自证 ──────────────────────────────────────────────
  it('S1 空文本必须判红——骨架屏冒充章节截图比缺一张图更坏', () => {
    for (const blank of ['', '   ', '\n\t ']) {
      const v = judgeShotPrivacy(blank)
      expect(v.pass, `空文本被放行（${JSON.stringify(blank)}）`).toBe(false)
      expect(v.hits[0].rule).toBe('empty-text')
    }
  })

  it('S2 射程自证：同一份文本，去掉规则表里任意一条规则就会漏抓（证明每条都在出力）', () => {
    // 逐条构造"只有这一条规则能抓住"的靶子：若某条规则是摆设，这里会报出来
    const targets: Array<[string, string]> = [
      ['email', 'a@b.co'],
      ['long-number', '12345678'],
      ['page-code', '/p/abcdefghabcdefgh/ijklmnopijklmnop'],
      ['credential', 'sk_abcdefgh1234'],
    ]
    for (const [rule, text] of targets) {
      const hit = scanShotText(text)
      expect(hit.some((h) => h.rule === rule), `规则 ${rule} 对自己的最小靶子都不生效 ⇒ 空转`).toBe(true)
    }
  })

  it('S3 一次扫多段文本不许互相污染（正则 lastIndex 串味是本类判据的老 bug）', () => {
    const a = scanShotText('zhang@corp.com')
    const b = scanShotText('zhang@corp.com')
    // 第三段刻意用**分组手机号**：连续 11 位那一串归 long-number 兜（见规则表注释），
    //   这里要测的是"跨段扫不许串味"，所以靶子必须落在 phone 那条规则自己的射程上。
    const c = scanShotText('李四 +86 139 1234 5678')
    expect(a.length).toBeGreaterThan(0)
    expect(b.map((h) => h.rule)).toEqual(a.map((h) => h.rule))
    expect(c.some((h) => h.rule === 'phone'), '第三段被前两段的 lastIndex 影响 ⇒ 漏抓').toBe(true)
  })

  it('S4 夹具白名单不许含任何像真主体的串（这份表会被贴进验收文档）', () => {
    for (const one of FIXTURE_ALLOW) {
      expect(/example\.test|演示数据|demo data|Sample/i.test(one), `夹具白名单里出现可疑真实值：${one}`).toBe(true)
    }
  })
})
